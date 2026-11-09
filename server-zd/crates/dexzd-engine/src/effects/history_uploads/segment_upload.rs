// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Uploads one run's sealed history segments, oldest first, and proposes each acknowledgment
//! (plan section 5.4).

use std::collections::BTreeSet;

use bytes::Bytes;
use dexzd_apply::ApplyError;
use dexzd_apply::FlowStateReader;
use dexzd_apply::read_model::{
    ReadModelError, oldest_sealed_history_segment, read_history_segment_content,
};
use dexzd_history_segment::{
    HistorySegmentCodecError, decode_history_segment, decode_history_segment_header,
    encode_history_segment, history_segment_object_key,
};
use dexzd_keyspace::{RunId, TenantId};
use dexzd_metrics::HistoryUploadOutcome;
use dexzd_object_store::{
    ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, WriteCondition, WriteOutcome,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistorySegmentUploadedCommand, command_reply, flow_command,
};

use super::until_stopped;
use super::upload_limiter::UploadSlot;
use super::upload_rescan::sealed_segment_bytes;
use crate::blocking_work::BlockingWorkFailure;
use crate::effects::state_reads::RunLocation;
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// gRPC NOT_FOUND, which apply returns for a run that is gone or purging.
const GRPC_NOT_FOUND: i32 = 5;
/// First reservation of an upload; it covers a short run's segment without a second read.
const INITIAL_RESERVATION_BYTES: u64 = 64 * 1024;
/// Fixed reservation headroom over a segment's row bytes, for block framing and the trailer.
const RESERVATION_FRAMING_BYTES: u64 = 4 * 1024;
/// Conditional writes one upload attempt makes before it backs off; each extra write means the
/// object changed between our read and write.
const MAX_CONDITIONAL_WRITES: usize = 4;

/// One sealed segment encoded from committed state. It owns its upload slot, so encoded bytes
/// never outlive the slot and memory stays bounded by `history.uploadMaxInFlightBytes`.
struct EncodedSealedSegment {
    identity: SealedSegmentIdentity,
    body: Bytes,
    upload_slot: UploadSlot,
}

/// What encoding the oldest sealed segment under a byte reservation found.
enum SealedSegmentEncoding {
    /// No SEALED segment remains, or the run is gone or purging.
    NoneSealed,
    /// The segment needs a reservation of this many bytes; nothing was kept.
    NeedsReservation(u64),
    Encoded(SealedSegmentIdentity, Bytes),
}

/// What names a sealed segment and its object; holds no object bytes.
struct SealedSegmentIdentity {
    first_seq: u64,
    last_seq: u64,
    object_key: String,
    content_checksum: [u8; 16],
}

/// A segment whose object is stored, with how the upload ended; holds no object bytes.
struct StoredSealedSegment {
    identity: SealedSegmentIdentity,
    outcome: HistoryUploadOutcome,
    object_size_bytes: u64,
    /// Bytes this upload wrote; zero when the object already existed.
    written_bytes: u64,
}

/// Uploads the run's SEALED history segments in seq order and acknowledges each one, until
/// committed state has none left. Retryable object storage failures retry with backoff and
/// never block commands.
///
/// A run whose upload failed without a retry is skipped by every trigger until the rescan
/// allows it again, so it is retried at most once per rescan.
///
/// Returns `Ok` once no SEALED segment remains, the run is gone or purging, or the run waits
/// for the rescan after a failure. Returns an error when committed state cannot be read or
/// encoded, when object storage rejects a call permanently, or when an acknowledgment is
/// rejected or does not advance the run; nothing is acknowledged meanwhile.
pub(crate) async fn upload_sealed_history_segments(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<(), EffectError> {
    if failed_runs(context).contains(&run_id) {
        tracing::debug!(%run_id, "a failed history upload waits for the next rescan");
        return Ok(());
    }
    let result = upload_run_segments(context, flow, run_id).await;
    match &result {
        Ok(()) => {
            failed_runs(context).remove(&run_id);
        }
        Err(EffectError::Stopped) => {}
        Err(_) => {
            failed_runs(context).insert(run_id);
        }
    }
    result
}

/// Lets the next trigger retry `run_id` after a failure; the rescan calls it.
pub(super) fn allow_upload_retry(context: &EffectContext, run_id: RunId) {
    failed_runs(context).remove(&run_id);
}

fn failed_runs(context: &EffectContext) -> std::sync::MutexGuard<'_, BTreeSet<RunId>> {
    context
        .history_upload_failed_runs
        .lock()
        .expect("the failed upload set lock is never poisoned")
}

async fn upload_run_segments(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<(), EffectError> {
    let run = context.run_location(flow, run_id);
    let mut backoff = context.retry_backoff();
    let mut acknowledged_first_seq = None;
    let limiter = &context.history_upload_limiter;
    loop {
        let upload_slot = limiter.acquire_slot(&context.shutdown).await?;
        let segment = match encode_oldest_sealed_segment(context, flow, run, upload_slot).await {
            Ok(Some(segment)) => segment,
            Ok(None) => return Ok(()),
            Err(EffectError::Stopped) => return Err(EffectError::Stopped),
            Err(error) if error.is_retryable() => {
                tracing::warn!(%error, %run_id, "reading a sealed history segment failed; retrying");
                if !backoff.wait(&context.shutdown).await {
                    return Err(EffectError::Stopped);
                }
                continue;
            }
            Err(error) => return Err(failed_until_rescan(context, run_id, None, error)),
        };
        let object_key = segment.identity.object_key.clone();
        if acknowledged_first_seq == Some(segment.identity.first_seq) {
            let error = EffectError::InvariantViolation {
                detail: format!(
                    "history segment {}..={} of run {run_id} stays SEALED after its \
                     acknowledgment applied",
                    segment.identity.first_seq, segment.identity.last_seq
                ),
            };
            return Err(failed_until_rescan(
                context,
                run_id,
                Some(&object_key),
                error,
            ));
        }
        let granted_after_reductions = segment.upload_slot.granted_after_reductions();
        let stored = match store_segment_object(context, segment).await {
            Ok(stored) => {
                limiter.record_stored_upload();
                stored
            }
            Err(EffectError::Stopped) => return Err(EffectError::Stopped),
            Err(error) if error.is_retryable() => {
                limiter.record_retryable_failure(granted_after_reductions);
                context
                    .metrics
                    .history_uploads
                    .record(HistoryUploadOutcome::Failed);
                tracing::warn!(
                    %error,
                    %run_id,
                    object_key,
                    "uploading a history segment failed; retrying"
                );
                if !backoff.wait(&context.shutdown).await {
                    return Err(EffectError::Stopped);
                }
                continue;
            }
            Err(error) => {
                return Err(failed_until_rescan(
                    context,
                    run_id,
                    Some(&object_key),
                    error,
                ));
            }
        };
        backoff = context.retry_backoff();
        let is_run_uploading = match acknowledge_segment(context, flow, run_id, &stored).await {
            Ok(is_run_uploading) => is_run_uploading,
            Err(EffectError::Stopped) => return Err(EffectError::Stopped),
            Err(error) => {
                return Err(failed_until_rescan(
                    context,
                    run_id,
                    Some(&object_key),
                    error,
                ));
            }
        };
        // Recorded once the acknowledgment committed, so the counter shows finished uploads.
        record_stored_segment(context, &stored);
        if !is_run_uploading {
            return Ok(());
        }
        let partition = context
            .hasher
            .partition_of_flow(flow.tenant_id, &flow.flow_id);
        context
            .metrics
            .history_upload_backlog
            .remove_uploaded_segment(usize::from(partition.0));
        acknowledged_first_seq = Some(stored.identity.first_seq);
    }
}

/// Counts and logs an upload failure that waits for the next rescan, and returns its error.
fn failed_until_rescan(
    context: &EffectContext,
    run_id: RunId,
    object_key: Option<&str>,
    error: EffectError,
) -> EffectError {
    context
        .metrics
        .history_uploads
        .record(HistoryUploadOutcome::Failed);
    tracing::error!(
        %error,
        %run_id,
        object_key,
        "a history segment upload failed without a retry; retrying at the next rescan"
    );
    error
}

/// Reads the run's oldest SEALED segment from committed state and encodes it on a blocking
/// thread, so zstd compression never stalls async tasks.
///
/// The slot reserves the segment's bytes before the read. A segment that needs more releases
/// what it read and reserves again.
///
/// Returns `None` when no SEALED segment remains or the run is gone or purging. Returns an
/// error, and writes nothing, when the segment's content cannot be read or encoded.
async fn encode_oldest_sealed_segment(
    context: &EffectContext,
    flow: &FlowAddress,
    run: RunLocation,
    mut upload_slot: UploadSlot,
) -> Result<Option<EncodedSealedSegment>, EffectError> {
    let limiter = &context.history_upload_limiter;
    let max_reservation = limiter.max_reservation_bytes();
    let mut wanted_bytes = INITIAL_RESERVATION_BYTES;
    loop {
        let reserved = limiter
            .reserve_bytes(&mut upload_slot, wanted_bytes, &context.shutdown)
            .await?;
        // A reservation of the whole budget lets any segment proceed alone.
        let reservation_limit = if reserved >= max_reservation {
            u64::MAX
        } else {
            reserved
        };
        let reader = context
            .flow_effect_operations
            .flow_state_reader(flow, false)?;
        let tenant_id = flow.tenant_id;
        let encoding = context
            .config
            .blocking_work
            .run_tracked(&context.tracker, move || {
                encode_from_committed_state(reader.as_ref(), tenant_id, run, reservation_limit)
            });
        let encoded = match encoding.await {
            Ok(result) => result?,
            Err(BlockingWorkFailure::Panicked(payload)) => std::panic::resume_unwind(payload),
            Err(BlockingWorkFailure::Cancelled) => return Err(EffectError::Stopped),
        };
        match encoded {
            SealedSegmentEncoding::NoneSealed => return Ok(None),
            SealedSegmentEncoding::NeedsReservation(bytes) => wanted_bytes = bytes,
            SealedSegmentEncoding::Encoded(identity, body) => {
                limiter.trim_reservation(&mut upload_slot, body.len() as u64);
                return Ok(Some(EncodedSealedSegment {
                    identity,
                    body,
                    upload_slot,
                }));
            }
        }
    }
}

/// Encodes the oldest SEALED segment when its estimate and its encoding fit in
/// `reservation_limit` bytes.
///
/// Returns what it found: no segment, the reservation the segment needs, or the encoding.
fn encode_from_committed_state(
    reader: &dyn FlowStateReader,
    tenant_id: TenantId,
    run: RunLocation,
    reservation_limit: u64,
) -> Result<SealedSegmentEncoding, EffectError> {
    let segment = match oldest_sealed_history_segment(reader, run.flow_hash, run.run_id) {
        Ok(Some(segment)) => segment,
        Ok(None) | Err(ReadModelError::RunNotFound { .. }) => {
            return Ok(SealedSegmentEncoding::NoneSealed);
        }
        Err(error) => return Err(read_model_error(error)),
    };
    let row_bytes = sealed_segment_bytes(reader, run, &segment)?;
    // zstd output stays within about 1/128 of its input; framing adds a fixed amount.
    let estimate = row_bytes
        .saturating_add(row_bytes / 64)
        .saturating_add(RESERVATION_FRAMING_BYTES);
    if estimate > reservation_limit {
        return Ok(SealedSegmentEncoding::NeedsReservation(estimate));
    }
    let content =
        read_history_segment_content(reader, run.flow_hash, run.run_id, segment.first_seq)
            .map_err(read_model_error)?
            .ok_or_else(|| EffectError::InvariantViolation {
                detail: format!(
                    "history segment {}..={} of run {} disappeared within one committed view",
                    segment.first_seq, segment.last_seq, run.run_id
                ),
            })?;
    let encoded = encode_history_segment(&content)
        .map_err(|error| encoding_error(run.run_id, segment.first_seq, &error))?;
    let encoded_size = encoded.bytes.len() as u64;
    if encoded_size > reservation_limit {
        return Ok(SealedSegmentEncoding::NeedsReservation(encoded_size));
    }
    let identity = SealedSegmentIdentity {
        first_seq: segment.first_seq,
        last_seq: segment.last_seq,
        object_key: history_segment_object_key(
            tenant_id,
            run.flow_hash,
            run.run_id,
            segment.first_seq,
            segment.last_seq,
        ),
        content_checksum: encoded.content_checksum,
    };
    Ok(SealedSegmentEncoding::Encoded(
        identity,
        Bytes::from(encoded.bytes),
    ))
}

/// Creates the segment's object, or confirms an existing one (plan R11), then releases the
/// encoded bytes and the upload slot before returning.
///
/// An existing object that decodes with the same content checksum came from an earlier upload
/// of this segment. Any other existing object came from committed history that no longer
/// exists, such as an older restored snapshot, or from a determinism bug; committed state is
/// authoritative while the segment is SEALED, so it is replaced with an If-Match write.
///
/// Returns the stored segment, the object storage error, which leaves no partial object, or
/// [`EffectError::Stopped`] when the node stops during a call.
async fn store_segment_object(
    context: &EffectContext,
    segment: EncodedSealedSegment,
) -> Result<StoredSealedSegment, EffectError> {
    let store = &context.history_object_store;
    let key = segment.identity.object_key.as_str();
    let body_size = segment.body.len() as u64;
    let mut condition = WriteCondition::CreateOnly;
    for _ in 0..MAX_CONDITIONAL_WRITES {
        let is_replacement = matches!(condition, WriteCondition::ReplaceIfEtagMatches { .. });
        let written =
            until_stopped(context, store.put(key, segment.body.clone(), condition)).await?;
        if let WriteOutcome::Written { .. } = written {
            let outcome = if is_replacement {
                HistoryUploadOutcome::Replaced
            } else {
                HistoryUploadOutcome::Created
            };
            return Ok(stored_segment(segment, outcome, body_size, body_size));
        }
        let Some(existing) = until_stopped(context, store.get(key)).await? else {
            condition = WriteCondition::CreateOnly;
            continue;
        };
        let existing_checksum = verified_content_checksum(context, existing.bytes).await?;
        if existing_checksum == Some(segment.identity.content_checksum) {
            let outcome = HistoryUploadOutcome::AlreadyPresent;
            return Ok(stored_segment(segment, outcome, existing.object_size, 0));
        }
        context.metrics.history_upload_checksum_mismatches.inc();
        tracing::error!(
            object_key = key,
            existing_object_size_bytes = existing.object_size,
            "an existing history segment object differs from committed state; replacing it"
        );
        condition = WriteCondition::ReplaceIfEtagMatches {
            etag: existing.etag,
        };
    }
    Err(EffectError::ObjectStore(ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        ObjectStoreErrorKind::Transient,
        format!("the object changed during each of {MAX_CONDITIONAL_WRITES} conditional writes"),
    )))
}

/// Drops the segment's bytes and upload slot, keeping what its acknowledgment needs.
fn stored_segment(
    segment: EncodedSealedSegment,
    outcome: HistoryUploadOutcome,
    object_size_bytes: u64,
    written_bytes: u64,
) -> StoredSealedSegment {
    StoredSealedSegment {
        identity: segment.identity,
        outcome,
        object_size_bytes,
        written_bytes,
    }
}

/// Decodes an existing object completely on a blocking thread, under the caller's upload slot.
///
/// Returns its content checksum, or `None` for an object that fails any block, value, trailer,
/// or content check.
async fn verified_content_checksum(
    context: &EffectContext,
    object: Bytes,
) -> Result<Option<[u8; 16]>, EffectError> {
    let verification = context
        .config
        .blocking_work
        .run_tracked(&context.tracker, move || {
            decode_history_segment(&object).ok()?;
            decode_history_segment_header(&object, object.len() as u64)
                .ok()
                .map(|layout| layout.content_checksum())
        });
    match verification.await {
        Ok(checksum) => Ok(checksum),
        Err(BlockingWorkFailure::Panicked(payload)) => std::panic::resume_unwind(payload),
        Err(BlockingWorkFailure::Cancelled) => Err(EffectError::Stopped),
    }
}

fn record_stored_segment(context: &EffectContext, stored: &StoredSealedSegment) {
    let metrics = &context.metrics;
    metrics.history_uploads.record(stored.outcome);
    metrics.history_upload_bytes.inc_by(stored.written_bytes);
    tracing::debug!(
        object_key = stored.identity.object_key,
        outcome = ?stored.outcome,
        object_size_bytes = stored.object_size_bytes,
        "stored a history segment object"
    );
}

/// Proposes the segment's acknowledgment and waits until it commits.
///
/// Returns false when the run is gone or purging, which ends the run's uploads. A duplicate or
/// stale acknowledgment applies as a no-op and returns true.
async fn acknowledge_segment(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
    stored: &StoredSealedSegment,
) -> Result<bool, EffectError> {
    let identity = &stored.identity;
    let command = FlowCommand {
        flow_id: flow.flow_id.clone(),
        run_id: run_id.as_bytes().to_vec(),
        tenant_id: flow.tenant_id.0,
        command: Some(flow_command::Command::HistorySegmentUploaded(
            HistorySegmentUploadedCommand {
                first_seq: identity.first_seq,
                last_seq: identity.last_seq,
                content_checksum: identity.content_checksum.to_vec(),
                object_size_bytes: stored.object_size_bytes,
            },
        )),
        ..FlowCommand::default()
    };
    let reply = context
        .propose_until_committed(flow, command, CommitUrgency::Batched)
        .await
        .ok_or(EffectError::Stopped)?;
    match reply.reply {
        Some(command_reply::Reply::Error(error)) if error.grpc_code == GRPC_NOT_FOUND => {
            tracing::debug!(%run_id, "stopping history uploads of a run that is gone or purging");
            Ok(false)
        }
        Some(command_reply::Reply::Error(error)) => Err(EffectError::InvariantViolation {
            detail: format!(
                "HistorySegmentUploaded {}..={} of run {run_id} was rejected: {}",
                identity.first_seq, identity.last_seq, error.message
            ),
        }),
        _ => Ok(true),
    }
}

/// Maps a committed read failure; a failed store read stays retryable.
fn read_model_error(error: ReadModelError) -> EffectError {
    match error {
        ReadModelError::State(ApplyError::StateRead(error)) => EffectError::StateRead(error),
        ReadModelError::State(ApplyError::CorruptState { table, key, detail }) => {
            EffectError::CorruptState { table, key, detail }
        }
        error => EffectError::InvariantViolation {
            detail: format!("reading a sealed history segment failed: {error}"),
        },
    }
}

/// Maps a segment encoding failure; committed content always encodes, so a failure is a bug.
fn encoding_error(run_id: RunId, first_seq: u64, error: &HistorySegmentCodecError) -> EffectError {
    EffectError::InvariantViolation {
        detail: format!("encoding history segment {first_seq} of run {run_id} failed: {error}"),
    }
}
