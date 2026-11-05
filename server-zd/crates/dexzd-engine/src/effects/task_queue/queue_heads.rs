// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Rebuild Heads and the periodic Heads scan (plan section 3.2).

use std::collections::BTreeMap;
use std::ops::Bound;
use std::sync::Arc;
use std::time::Duration;

use dexzd_apply::{BoundedRowSeek, FlowStateReader};
use dexzd_keyspace::dispatch_queue_keys::{
    DEFAULT_DISPATCH_PRIORITY, DispatchQueueEntry, DispatchQueueName, DispatchQueuePosition,
    decode_dispatch_queue_key, dispatch_queue_key, dispatch_queue_partition_prefix,
    dispatch_queue_prefix,
};
use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, PartitionId, partition_meta_key};
use dexzd_protocol::dexzd::v1::PartitionMeta;
use dexzd_worker_dispatch::{QueuedTaskRef, ScannedQueueHead};

use super::{
    QueueChangePosition, QueueReadView, decode_queue_row, first_ordinary_pending_since,
    is_ref_pending, read_off_task, ref_attempt_key,
};
use crate::effects::state_reads::decode_row;
use crate::effects::{EffectContext, EffectError};

/// How often the task queue gauges that matching owns are refreshed.
const GAUGE_REFRESH_INTERVAL: Duration = Duration::from_secs(5);

/// Rows read per page while counting a partition's queue rows.
const COUNT_PAGE_ROWS: usize = 1024;

/// Reports a rebuilt partition's queue heads to matching with a bounded skip scan (plan
/// section 3.2).
///
/// Returns the number of heads, or the read's error.
pub(crate) async fn rebuild_heads(
    context: &Arc<EffectContext>,
    partition: PartitionId,
) -> Result<usize, EffectError> {
    let (heads, ordinary_backlog, scanned_heads) = read_off_task(
        context,
        &context.blocking_read_slots,
        QueueReadView::Committed,
        move |context, reader| discover_heads(context, reader, partition),
    )
    .await?;
    let head_count = heads.len();
    // Heads only lower refs, so a Queued that arrived first is never lost.
    context.dispatcher.report_rebuilt_heads(heads);
    for (queue, pending_since) in ordinary_backlog {
        context
            .dispatcher
            .report_ordinary_backlog(partition, &queue, pending_since);
    }
    let mut memory = context.task_queue_memory();
    let stale: Vec<_> = memory
        .scanned_heads
        .range(first_slot(partition)..)
        .take_while(|((slot_partition, _), _)| *slot_partition == partition)
        .map(|(slot, _)| slot.clone())
        .collect();
    for slot in stale {
        memory.scanned_heads.remove(&slot);
    }
    for (queue, position) in scanned_heads {
        memory.scanned_heads.insert((partition, queue), position);
    }
    memory.owned_partitions.insert(partition);
    Ok(head_count)
}

/// What a rebuild's skip scan found: heads, ordinary backlog ages, and first live rows.
type DiscoveredHeads = (
    Vec<QueuedTaskRef>,
    Vec<(DispatchQueueName, Option<i64>)>,
    BTreeMap<DispatchQueueName, DispatchQueuePosition>,
);

/// Skip-scans the partition's queues: one seek per queue, plus one per held row passed and
/// priority-0 head.
///
/// Returns the discovered heads, or the read's error.
fn discover_heads(
    context: &EffectContext,
    reader: &dyn FlowStateReader,
    partition: PartitionId,
) -> Result<DiscoveredHeads, EffectError> {
    let (mut cursor, end) = prefix_range(&dispatch_queue_partition_prefix(partition));
    let mut heads = Vec::new();
    let mut ordinary_backlog = Vec::new();
    let mut scanned_heads = BTreeMap::new();
    while let Some((first_key, first_value)) = reader
        .scan(ColumnFamily::PartitionIndex, &cursor, &end, 1)?
        .into_iter()
        .next()
    {
        let entry =
            decode_dispatch_queue_key(&first_key).ok_or_else(|| EffectError::CorruptState {
                table: "DispatchQueue",
                key: crate::effects::hex(&first_key),
                detail: "malformed key".to_owned(),
            })?;
        let (_, queue_end) = prefix_range(&dispatch_queue_prefix(partition, &entry.queue));
        let mut row = Some((first_key, first_value));
        while let Some((key, value)) = row.take() {
            if let Some(task) = decode_queue_row(reader, &key, &value)? {
                scanned_heads
                    .entry(entry.queue.clone())
                    .or_insert_with(|| task.position.clone());
                if !context.attempt_entries().is_held(&ref_attempt_key(&task)) {
                    if task.position.priority < DEFAULT_DISPATCH_PRIORITY {
                        let pending_since =
                            first_ordinary_pending_since(reader, u64::MAX, partition, &task.queue)?;
                        ordinary_backlog.push((task.queue.clone(), pending_since));
                    }
                    heads.push(task);
                    break;
                }
            }
            let mut next = key;
            next.push(0);
            row = reader
                .scan(ColumnFamily::PartitionIndex, &next, &queue_end, 1)?
                .into_iter()
                .next();
        }
        cursor = queue_end;
    }
    Ok((heads, ordinary_backlog, scanned_heads))
}

/// Counts a rebuilt partition's queue rows in the background; the entries gauge omits the
/// partition until then.
pub(crate) fn spawn_row_count(context: &Arc<EffectContext>, partition: PartitionId) {
    // Begins before the snapshot opens, so every later change is either in it or after it.
    let (_, count_generation) = context.task_queue_memory().begin_row_count(partition);
    let context = Arc::clone(context);
    let tracker = context.tracker.clone();
    tracker.spawn(async move {
        let mut backoff = context.retry_backoff();
        loop {
            let counted = read_off_task(
                &context,
                &context.row_count_slot,
                QueueReadView::Committed,
                move |_, reader| count_partition_rows(reader, partition),
            )
            .await;
            match counted {
                Ok((rows, watermark)) => {
                    let total = context.task_queue_memory().finish_row_count(
                        partition,
                        count_generation,
                        rows,
                        watermark,
                    );
                    if total.is_none() {
                        tracing::debug!(partition = partition.0, "a later row count replaced this one");
                    }
                    return;
                }
                Err(EffectError::Stopped) => return,
                Err(error) => {
                    tracing::warn!(%error, partition = partition.0, "retrying a task queue row count");
                    if !backoff.wait(&context.shutdown).await {
                        return;
                    }
                }
            }
        }
    });
}

/// Counts the partition's queue rows in committed `reader`.
///
/// Returns the count and the snapshot's applied batch, or the read's error.
fn count_partition_rows(
    reader: &dyn FlowStateReader,
    partition: PartitionId,
) -> Result<(i64, Option<QueueChangePosition>), EffectError> {
    let watermark = reader
        .get(ColumnFamily::PartitionMeta, &partition_meta_key(partition))?
        .map(|bytes| {
            decode_row::<PartitionMeta>("PartitionMeta", &partition_meta_key(partition), &bytes)
        })
        .transpose()?
        .and_then(|meta| meta.applied)
        .map(|applied| (applied.incarnation, applied.seq));
    let (mut cursor, end) = prefix_range(&dispatch_queue_partition_prefix(partition));
    let mut row_count = 0_i64;
    loop {
        let rows = reader.scan(ColumnFamily::PartitionIndex, &cursor, &end, COUNT_PAGE_ROWS)?;
        row_count += i64::try_from(rows.len()).expect("a page holds few rows");
        match rows.last() {
            Some((last_key, _)) if rows.len() == COUNT_PAGE_ROWS => {
                cursor.clone_from(last_key);
                cursor.push(0);
            }
            _ => break,
        }
    }
    Ok((row_count, watermark))
}

/// Runs periodic Heads one partition at a time and refreshes the queue gauges.
pub(crate) async fn run_heads_resync(context: Arc<EffectContext>) {
    let interval = context.config.heads_resync_interval;
    let mut gauges = tokio::time::interval(GAUGE_REFRESH_INTERVAL);
    gauges.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut next_partition: Option<PartitionId> = None;
    let scan_timer = tokio::time::sleep(scan_delay(&context, interval));
    tokio::pin!(scan_timer);
    loop {
        // Biased for reproducible simulation; shutdown fires once.
        tokio::select! {
            biased;
            () = context.shutdown.cancelled() => return,
            _ = gauges.tick() => context.dispatcher.refresh_task_queue_gauges(),
            () = &mut scan_timer, if !interval.is_zero() => {
                let partition = following_partition(&context, next_partition);
                if let Some(partition) = partition {
                    scan_partition(&context, partition).await;
                }
                next_partition = partition;
                let delay = scan_delay(&context, interval);
                scan_timer.as_mut().reset(tokio::time::Instant::now() + delay);
            }
        }
    }
}

/// Returns the first (partition, queue) slot of `partition`; slots of the partition follow it.
fn first_slot(partition: PartitionId) -> (PartitionId, DispatchQueueName) {
    let first_queue = DispatchQueueName {
        tenant_id: dexzd_keyspace::TenantId(0),
        flow_type: String::new(),
        step_type: String::new(),
    };
    (partition, first_queue)
}

/// Returns the delay between two partition scans, which spreads one round over `interval`.
fn scan_delay(context: &EffectContext, interval: Duration) -> Duration {
    let partition_count = context.task_queue_memory().owned_partitions.len().max(1);
    interval / u32::try_from(partition_count).unwrap_or(u32::MAX)
}

/// Returns the owned partition after `previous`, wrapping around.
fn following_partition(
    context: &EffectContext,
    previous: Option<PartitionId>,
) -> Option<PartitionId> {
    let memory = context.task_queue_memory();
    let owned = &memory.owned_partitions;
    previous
        .and_then(|previous| {
            owned
                .range((Bound::Excluded(previous), Bound::Unbounded))
                .next()
        })
        .or_else(|| owned.iter().next())
        .copied()
}

/// Scans the partition's remembered and written queues.
async fn scan_partition(context: &Arc<EffectContext>, partition: PartitionId) {
    let token = context.dispatcher.take_heads_token(partition);
    let (scanned, mut written) = {
        let mut memory = context.task_queue_memory();
        let scanned: BTreeMap<DispatchQueueName, DispatchQueuePosition> = memory
            .scanned_heads
            .range(first_slot(partition)..)
            .take_while(|((slot_partition, _), _)| *slot_partition == partition)
            .map(|((_, queue), position)| (queue.clone(), position.clone()))
            .collect();
        let written_slots: Vec<_> = memory
            .written_since_scan
            .range(first_slot(partition)..)
            .take_while(|((slot_partition, _), _)| *slot_partition == partition)
            .map(|(slot, _)| slot.clone())
            .collect();
        let mut written = BTreeMap::new();
        for slot in written_slots {
            if let Some(position) = memory.written_since_scan.remove(&slot) {
                written.insert(slot.1, position);
            }
        }
        (scanned, written)
    };
    let mut queues: Vec<DispatchQueueName> = scanned.keys().cloned().collect();
    queues.extend(
        written
            .keys()
            .filter(|queue| !scanned.contains_key(*queue))
            .cloned(),
    );
    let starts: Vec<(DispatchQueueName, DispatchQueuePosition)> = queues
        .into_iter()
        .map(|queue| {
            let from = [scanned.get(&queue), written.get(&queue)]
                .into_iter()
                .flatten()
                .min()
                .cloned()
                .expect("a scanned queue was remembered or written");
            (queue, from)
        })
        .collect();
    let read_starts = starts.clone();
    let read = read_off_task(
        context,
        &context.blocking_read_slots,
        QueueReadView::Committed,
        move |context, reader| {
            Ok(read_starts
                .into_iter()
                .map(|(queue, from)| {
                    let outcome = scan_queue(context, reader, partition, &queue, &from);
                    let ordinary = match &outcome {
                        Ok(Some((result, _))) => {
                            super::ordinary_backlog_behind(context, reader, result.head.as_ref())
                        }
                        _ => None,
                    };
                    (queue, outcome, ordinary)
                })
                .collect::<Vec<_>>())
        },
    )
    .await;
    let outcomes = match read {
        Ok(outcomes) => outcomes,
        Err(error) => {
            tracing::warn!(%error, partition = partition.0, "a periodic Heads scan could not read its partition");
            for (queue, _) in starts {
                note_incomplete_queue(context, partition, queue, &mut written);
            }
            return;
        }
    };
    let mut results = Vec::new();
    let mut ordinary_backlog = Vec::new();
    for (queue, outcome, ordinary) in outcomes {
        match outcome {
            Ok(Some((result, first_live))) => {
                if let Some(pending_since) = ordinary {
                    ordinary_backlog.push((queue.clone(), pending_since));
                }
                let mut memory = context.task_queue_memory();
                let slot = (partition, queue);
                match first_live {
                    Some(position) => memory.scanned_heads.insert(slot, position),
                    None => memory.scanned_heads.remove(&slot),
                };
                results.push(result);
            }
            outcome => {
                if let Err(error) = outcome {
                    tracing::warn!(%error, partition = partition.0, "a periodic Heads scan could not read a queue");
                }
                note_incomplete_queue(context, partition, queue, &mut written);
            }
        }
    }
    context.dispatcher.report_scanned_heads(token, results);
    for (queue, pending_since) in ordinary_backlog {
        context
            .dispatcher
            .report_ordinary_backlog(partition, &queue, pending_since);
    }
}

/// Counts an unfinished queue scan and keeps its smallest written key for the next scan.
fn note_incomplete_queue(
    context: &EffectContext,
    partition: PartitionId,
    queue: DispatchQueueName,
    written: &mut BTreeMap<DispatchQueueName, DispatchQueuePosition>,
) {
    context.metrics.matching_heads_incomplete_scans.inc();
    if let Some(position) = written.remove(&queue) {
        context
            .task_queue_memory()
            .note_written(&DispatchQueueEntry {
                partition,
                queue,
                position,
            });
    }
}

/// Seeks one queue from `from`, skipping rows a holder sends back and rows no longer pending.
///
/// Returns the scan result and the first live row seen, `None` when a seek met too many
/// deleted entries, or the read's error.
fn scan_queue(
    context: &EffectContext,
    reader: &dyn FlowStateReader,
    partition: PartitionId,
    queue: &DispatchQueueName,
    from: &DispatchQueuePosition,
) -> Result<Option<(ScannedQueueHead, Option<DispatchQueuePosition>)>, EffectError> {
    let mut cursor = dispatch_queue_key(&DispatchQueueEntry {
        partition,
        queue: queue.clone(),
        position: from.clone(),
    });
    let (_, end) = prefix_range(&dispatch_queue_prefix(partition, queue));
    let mut first_live = None;
    let mut result = ScannedQueueHead {
        queue: queue.clone(),
        head: None,
        is_head_queued_in_flight: false,
        has_skipped_held_rows: false,
    };
    loop {
        let max_skipped = context.config.heads_max_skipped_deletions;
        let (key, value) = match reader.seek_first_bounded(
            ColumnFamily::PartitionIndex,
            &cursor,
            &end,
            max_skipped,
        )? {
            BoundedRowSeek::Found(row) => row,
            BoundedRowSeek::Exhausted => return Ok(Some((result, first_live))),
            BoundedRowSeek::Incomplete => return Ok(None),
        };
        cursor.clone_from(&key);
        cursor.push(0);
        let Some(task) = decode_queue_row(reader, &key, &value)? else {
            continue;
        };
        first_live.get_or_insert_with(|| task.position.clone());
        let attempt_key = ref_attempt_key(&task);
        if context.attempt_entries().is_held(&attempt_key) {
            result.has_skipped_held_rows = true;
            continue;
        }
        // An entry may end before the command that deletes its row commits.
        if !is_ref_pending(context, &task)? {
            continue;
        }
        result.is_head_queued_in_flight = context.attempt_entries().is_queued_sent(&attempt_key);
        result.head = Some(task);
        return Ok(Some((result, first_live)));
    }
}
