// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Periodic rescan of every owned partition's sealed history segments (plan section 5.15): it
//! publishes the upload backlog gauges and restarts the uploads of runs whose work stopped. A run
//! whose rows cannot be read is logged, counted, and skipped, so it never hides the others.

use std::collections::BTreeMap;
use std::sync::Arc;

use dexzd_apply::{ApplyEffect, FlowStateReader};
use dexzd_keyspace::row_keys::{history_segment_key, history_segment_value_prefix};
use dexzd_keyspace::{
    ColumnFamily, IndexKind, PartitionId, RunTable, TenantId, partition_index_prefix,
    run_table_prefix,
};
use dexzd_protocol::dexzd::v1::{
    HistoryRowMetadata, HistorySegmentRecord, HistorySegmentState, HistorySegmentValueRecord,
};

use super::segment_upload::allow_upload_retry;
use crate::blocking_work::BlockingWorkFailure;
use crate::effects::state_reads::{
    RunLocation, decode_row, parse_run_item_index_key, prefix_successor, read_run_record, scan_all,
};
use crate::effects::{EffectContext, EffectError, RouterMessage};
use crate::flow_coordination::FlowAddress;

/// Uncompressed bytes of each sealed segment seen by the last scan, keyed by partition, run, and
/// first seq. A sealed segment's rows never change before its acknowledgment, so a size is
/// computed once.
type SealedSegmentSizes = BTreeMap<(PartitionId, RunLocation, u64), u64>;

/// What one scan of a partition found.
#[derive(Default)]
struct PartitionUploadBacklog {
    segments: u64,
    bytes: u64,
    oldest_sealed_at_micros: Option<i64>,
    runs: Vec<(FlowAddress, RunLocation)>,
    sizes: SealedSegmentSizes,
    /// Index rows or runs that could not be read, with the run when its key parsed.
    unreadable: Vec<(Option<RunLocation>, EffectError)>,
}

/// What one scan found for one run.
struct RunUploadBacklog {
    flow: FlowAddress,
    segments: u64,
    bytes: u64,
    oldest_sealed_at_micros: Option<i64>,
    sizes: SealedSegmentSizes,
}

/// Rescans every owned partition each `history_upload_rescan_interval` until shutdown, then
/// clears the backlog gauges.
pub(crate) async fn run_history_upload_rescan(context: Arc<EffectContext>) {
    let interval = context.config.history_upload_rescan_interval;
    if interval.is_zero() {
        return;
    }
    let mut ticker = tokio::time::interval(interval);
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut sizes = SealedSegmentSizes::new();
    loop {
        // Biased for reproducible simulation; shutdown fires once.
        tokio::select! {
            biased;
            () = context.shutdown.cancelled() => break,
            _ = ticker.tick() => {}
        }
        let partitions = context.task_queue_memory().owned_partition_list();
        for partition in partitions {
            match rescan_partition(&context, partition, &sizes).await {
                Ok(backlog) => publish_partition_backlog(&context, partition, backlog, &mut sizes),
                Err(EffectError::Stopped) => break,
                Err(error) => {
                    tracing::warn!(%error, %partition, "rescanning sealed history segments failed");
                }
            }
        }
    }
    context.metrics.history_upload_backlog.clear();
}

/// Scans the partition's upload pending runs on a blocking thread.
async fn rescan_partition(
    context: &EffectContext,
    partition: PartitionId,
    sizes: &SealedSegmentSizes,
) -> Result<PartitionUploadBacklog, EffectError> {
    let reader = context.flow_effect_operations.committed_state_reader()?;
    let known_sizes: SealedSegmentSizes = sizes
        .iter()
        .filter(|((size_partition, _, _), _)| *size_partition == partition)
        .map(|(key, size)| (*key, *size))
        .collect();
    let scan = context
        .config
        .blocking_work
        .run_tracked(&context.tracker, move || {
            scan_partition_backlog(reader.as_ref(), partition, &known_sizes)
        });
    match scan.await {
        Ok(result) => result,
        Err(BlockingWorkFailure::Panicked(payload)) => std::panic::resume_unwind(payload),
        Err(BlockingWorkFailure::Cancelled) => Err(EffectError::Stopped),
    }
}

/// Publishes the partition's gauges, replaces its remembered sizes, and asks for the uploads of
/// its pending runs; the router ignores a request for a run whose upload already runs, except to
/// run it once more.
fn publish_partition_backlog(
    context: &EffectContext,
    partition: PartitionId,
    backlog: PartitionUploadBacklog,
    sizes: &mut SealedSegmentSizes,
) {
    context.metrics.history_upload_backlog.set_partition(
        usize::from(partition.0),
        backlog.segments,
        backlog.bytes,
        backlog.oldest_sealed_at_micros,
    );
    sizes.retain(|(size_partition, _, _), _| *size_partition != partition);
    sizes.extend(backlog.sizes);
    for (run, error) in backlog.unreadable {
        context.metrics.history_upload_rescan_errors.inc();
        tracing::error!(
            %error,
            %partition,
            run_id = run.map(|run| run.run_id.to_string()),
            "the history upload rescan could not read a run; skipping it"
        );
    }
    for (flow, run) in backlog.runs {
        allow_upload_retry(context, run.run_id);
        context.report(RouterMessage::Effects {
            flow,
            effects: vec![ApplyEffect::HistorySegmentSealed { run_id: run.run_id }],
            is_committed: true,
            scheduled_at: None,
            position: None,
        });
    }
}

fn scan_partition_backlog(
    reader: &dyn FlowStateReader,
    partition: PartitionId,
    known_sizes: &SealedSegmentSizes,
) -> Result<PartitionUploadBacklog, EffectError> {
    let mut backlog = PartitionUploadBacklog::default();
    let prefix = partition_index_prefix(partition, IndexKind::HistoryUploadPending);
    for (key, _) in scan_all(reader, ColumnFamily::PartitionIndex, &prefix)? {
        let run = match parse_run_item_index_key(partition, IndexKind::HistoryUploadPending, &key) {
            Ok((run, _)) => run,
            Err(error) => {
                backlog.unreadable.push((None, error));
                continue;
            }
        };
        match scan_run_backlog(reader, partition, run, known_sizes) {
            Ok(Some(run_backlog)) => {
                backlog.segments += run_backlog.segments;
                backlog.bytes = backlog.bytes.saturating_add(run_backlog.bytes);
                backlog.oldest_sealed_at_micros = [
                    backlog.oldest_sealed_at_micros,
                    run_backlog.oldest_sealed_at_micros,
                ]
                .into_iter()
                .flatten()
                .min();
                backlog.sizes.extend(run_backlog.sizes);
                backlog.runs.push((run_backlog.flow, run));
            }
            Ok(None) => {}
            Err(error) => backlog.unreadable.push((Some(run), error)),
        }
    }
    Ok(backlog)
}

/// Returns what the run waits to upload, or `None` when the run is gone or purging.
fn scan_run_backlog(
    reader: &dyn FlowStateReader,
    partition: PartitionId,
    run: RunLocation,
    known_sizes: &SealedSegmentSizes,
) -> Result<Option<RunUploadBacklog>, EffectError> {
    let Some(record) = read_run_record(reader, run)? else {
        return Ok(None);
    };
    if record.is_purging {
        return Ok(None);
    }
    let mut run_backlog = RunUploadBacklog {
        flow: FlowAddress {
            tenant_id: TenantId(record.tenant_id),
            flow_id: record.flow_id,
        },
        segments: 0,
        bytes: 0,
        oldest_sealed_at_micros: None,
        sizes: SealedSegmentSizes::new(),
    };
    for segment in sealed_segments(reader, run, record.history_uploaded_through_seq)? {
        let size_key = (partition, run, segment.first_seq);
        let bytes = match known_sizes.get(&size_key) {
            Some(bytes) => *bytes,
            None => sealed_segment_bytes(reader, run, &segment)?,
        };
        run_backlog.sizes.insert(size_key, bytes);
        run_backlog.segments += 1;
        run_backlog.bytes = run_backlog.bytes.saturating_add(bytes);
        run_backlog.oldest_sealed_at_micros = Some(
            run_backlog
                .oldest_sealed_at_micros
                .map_or(segment.sealed_at_micros, |oldest| {
                    oldest.min(segment.sealed_at_micros)
                }),
        );
    }
    Ok(Some(run_backlog))
}

/// Returns the run's SEALED segments: every segment after the uploaded seq.
fn sealed_segments(
    reader: &dyn FlowStateReader,
    run: RunLocation,
    uploaded_through_seq: u64,
) -> Result<Vec<HistorySegmentRecord>, EffectError> {
    let start = history_segment_key(
        run.flow_hash,
        run.run_id,
        uploaded_through_seq.saturating_add(1),
    );
    let end = prefix_successor(&run_table_prefix(
        run.flow_hash,
        run.run_id,
        RunTable::HistorySegment,
    ));
    let mut segments = Vec::new();
    for (key, value) in reader.scan(ColumnFamily::Flow, &start, &end, usize::MAX)? {
        let segment: HistorySegmentRecord = decode_row("HistorySegment", &key, &value)?;
        if segment.state == HistorySegmentState::Sealed as i32 {
            segments.push(segment);
        }
    }
    Ok(segments)
}

/// Returns the uncompressed bytes the segment uploads: its history rows, as their metadata
/// records them, and its superseded values.
fn sealed_segment_bytes(
    reader: &dyn FlowStateReader,
    run: RunLocation,
    segment: &HistorySegmentRecord,
) -> Result<u64, EffectError> {
    let metadata_prefix = run_table_prefix(run.flow_hash, run.run_id, RunTable::HistoryRowMetadata);
    let mut start = metadata_prefix.clone();
    start.extend_from_slice(&segment.first_seq.to_be_bytes());
    let mut end = metadata_prefix;
    end.extend_from_slice(&segment.last_seq.saturating_add(1).to_be_bytes());
    let mut bytes = 0_u64;
    for (key, value) in reader.scan(ColumnFamily::Flow, &start, &end, usize::MAX)? {
        let metadata: HistoryRowMetadata = decode_row("HistoryRowMetadata", &key, &value)?;
        bytes = bytes.saturating_add(metadata.row_size_bytes);
    }
    let value_prefix = history_segment_value_prefix(run.flow_hash, run.run_id, segment.first_seq);
    for (key, value) in scan_all(reader, ColumnFamily::Flow, &value_prefix)? {
        let listed: HistorySegmentValueRecord = decode_row("HistorySegmentValue", &key, &value)?;
        bytes = bytes.saturating_add(listed.size_bytes);
    }
    Ok(bytes)
}
