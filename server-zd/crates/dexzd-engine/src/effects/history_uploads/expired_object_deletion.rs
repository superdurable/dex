// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deletes the objects of a run's EXPIRED history segments, oldest first, once the deletion delay
//! after each expiry passed, and records each batch (plan section 5.7).

use std::time::Duration;

use dexzd_apply::ApplyError;
use dexzd_apply::read_model::{ExpiredHistorySegments, ReadModelError, expired_history_segments};
use dexzd_history_segment::history_segment_object_key;
use dexzd_keyspace::RunId;
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistorySegmentObjectsDeletedCommand, command_reply, flow_command,
};

use super::run_history_objects::{sleep_until_stopped, wait_after_failure};
use super::until_stopped;
use crate::effects::state_reads::RunLocation;
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// Time between a segment's expiry and the deletion of its object, by the owner's wall clock.
/// A read that found the segment UPLOADED before its expiry finishes with the object meanwhile.
pub(crate) const EXPIRED_OBJECT_DELETION_DELAY: Duration = Duration::from_secs(60 * 60);
/// Most EXPIRED segments whose objects one batch deletes.
const DELETION_BATCH_SEGMENTS: usize = 1000;
/// gRPC NOT_FOUND, which apply returns for a run that is gone or purging.
const GRPC_NOT_FOUND: i32 = 5;

/// Deletes the objects of the run's EXPIRED segments in seq order and records each batch, until
/// committed state has none left. Waits until the deletion delay after a segment's expiry passed
/// before deleting its object. Failed object storage calls retry with backoff.
///
/// Reads the run's delete switch and EXPIRED segments from a new committed view before each
/// attempt, retries included, so a run whose switch turned off starts no further deletion. A
/// deletion request already sent to object storage may still complete.
/// Returns `Ok` once no EXPIRED segment remains, the switch is off, or the run is gone or
/// purging. Returns an error when committed state cannot be read, or when a
/// recorded deletion is rejected or leaves its oldest segment in place.
pub(crate) async fn delete_expired_history_objects(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<(), EffectError> {
    let run = context.run_location(flow, run_id);
    let mut recorded_first_seq = None;
    let mut backoff = context.retry_backoff();
    let delay_micros =
        i64::try_from(EXPIRED_OBJECT_DELETION_DELAY.as_micros()).expect("one hour fits in i64");
    loop {
        let Some(expired) = read_expired_segments(context, flow, run)? else {
            return Ok(());
        };
        let Some(oldest) = expired.segments.first() else {
            return Ok(());
        };
        if !expired.is_deletion_enabled {
            return Ok(());
        }
        if recorded_first_seq == Some(oldest.first_seq) {
            return Err(EffectError::InvariantViolation {
                detail: format!(
                    "history segment {}..={} of run {run_id} stays EXPIRED after its deletion was \
                     recorded",
                    oldest.first_seq, oldest.last_seq
                ),
            });
        }
        let now_micros = context.clock.now_unix_micros();
        let due: Vec<_> = expired
            .segments
            .iter()
            .take_while(|segment| {
                segment.expired_at_micros.saturating_add(delay_micros) <= now_micros
            })
            .collect();
        if due.is_empty() {
            let wait_micros = oldest
                .expired_at_micros
                .saturating_add(delay_micros)
                .saturating_sub(now_micros);
            let wait = Duration::from_micros(u64::try_from(wait_micros).unwrap_or(0));
            if !sleep_until_stopped(context, wait).await {
                return Err(EffectError::Stopped);
            }
            continue;
        }
        let keys: Vec<String> = due
            .iter()
            .map(|segment| {
                history_segment_object_key(
                    flow.tenant_id,
                    run.flow_hash,
                    run.run_id,
                    segment.first_seq,
                    segment.last_seq,
                )
            })
            .collect();
        let deletion =
            until_stopped(context, context.history_object_store.delete_many(&keys)).await;
        match deletion {
            Ok(()) => backoff = context.retry_backoff(),
            Err(EffectError::Stopped) => return Err(EffectError::Stopped),
            Err(error) => {
                // The next attempt reads committed state again and drops what is no longer due.
                wait_after_failure(context, &mut backoff, run, &error).await?;
                continue;
            }
        }
        let deleted_count = u64::try_from(keys.len()).expect("a batch length fits in u64");
        context
            .metrics
            .history_expired_objects_deleted
            .inc_by(deleted_count);
        let first_seqs: Vec<u64> = due.iter().map(|segment| segment.first_seq).collect();
        if !record_deleted_objects(context, flow, run_id, first_seqs).await? {
            return Ok(());
        }
        recorded_first_seq = Some(oldest.first_seq);
    }
}

/// Returns the run's EXPIRED segments from a new committed view, or `None` when the run is gone
/// or purging.
fn read_expired_segments(
    context: &EffectContext,
    flow: &FlowAddress,
    run: RunLocation,
) -> Result<Option<ExpiredHistorySegments>, EffectError> {
    let reader = context
        .flow_effect_operations
        .flow_state_reader(flow, false)?;
    match expired_history_segments(
        reader.as_ref(),
        run.flow_hash,
        run.run_id,
        DELETION_BATCH_SEGMENTS,
    ) {
        Ok(expired) => Ok(Some(expired)),
        Err(ReadModelError::RunNotFound { .. }) => Ok(None),
        Err(ReadModelError::State(ApplyError::StateRead(error))) => {
            Err(EffectError::StateRead(error))
        }
        Err(ReadModelError::State(ApplyError::CorruptState { table, key, detail })) => {
            Err(EffectError::CorruptState { table, key, detail })
        }
        Err(error) => Err(EffectError::InvariantViolation {
            detail: format!("reading expired history segments failed: {error}"),
        }),
    }
}

/// Proposes `HistorySegmentObjectsDeleted` for the deleted objects and waits until it commits.
///
/// Returns false when the run is gone or purging, which ends the run's deletions.
async fn record_deleted_objects(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
    first_seqs: Vec<u64>,
) -> Result<bool, EffectError> {
    let command = FlowCommand {
        flow_id: flow.flow_id.clone(),
        run_id: run_id.as_bytes().to_vec(),
        tenant_id: flow.tenant_id.0,
        command: Some(flow_command::Command::HistorySegmentObjectsDeleted(
            HistorySegmentObjectsDeletedCommand { first_seqs },
        )),
        ..FlowCommand::default()
    };
    let reply = context
        .propose_until_committed(flow, command, CommitUrgency::Batched)
        .await
        .ok_or(EffectError::Stopped)?;
    match reply.reply {
        Some(command_reply::Reply::Error(error)) if error.grpc_code == GRPC_NOT_FOUND => {
            tracing::debug!(%run_id, "stopping history object deletions of a run that is gone");
            Ok(false)
        }
        Some(command_reply::Reply::Error(error)) => Err(EffectError::InvariantViolation {
            detail: format!(
                "HistorySegmentObjectsDeleted of run {run_id} was rejected: {}",
                error.message
            ),
        }),
        _ => Ok(true),
    }
}
