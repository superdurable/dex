// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Heads (plan section 3.2): the queue heads a partition rebuild reports, and the periodic scan
//! that only catches implementation bugs. Matching's refs are soft state that Heads rebuilds.

use std::collections::BTreeMap;
use std::ops::Bound;
use std::sync::Arc;
use std::time::Duration;

use dexzd_apply::BoundedRowSeek;
use dexzd_keyspace::dispatch_queue_keys::{
    DispatchQueueEntry, DispatchQueueName, DispatchQueuePosition, decode_dispatch_queue_key,
    dispatch_queue_key, dispatch_queue_partition_prefix, dispatch_queue_prefix,
};
use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_worker_dispatch::{QueuedTaskRef, ScannedQueueHead};

use super::{SEEK_PAGE_ROWS, decode_queue_row, is_ref_pending, ref_attempt_key};
use crate::effects::{EffectContext, EffectError};

/// How often the task queue gauges that matching owns are refreshed.
const GAUGE_REFRESH_INTERVAL: Duration = Duration::from_secs(5);

/// Scans every queue of `partition` after a rebuild, before any of its Dispatch, and hands
/// matching each queue's first row that no holder sends back. Also counts the rows.
///
/// Returns the number of heads, or the read's error.
pub(crate) fn rebuild_heads(
    context: &EffectContext,
    partition: PartitionId,
) -> Result<usize, EffectError> {
    let reader = context.flow_effect_operations.committed_state_reader()?;
    let (mut cursor, end) = prefix_range(&dispatch_queue_partition_prefix(partition));
    let mut heads = Vec::new();
    let mut scanned_heads = BTreeMap::new();
    let mut row_count = 0_i64;
    while let Some((first_key, _)) = reader
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
        let (queue_start, queue_end) =
            prefix_range(&dispatch_queue_prefix(partition, &entry.queue));
        let mut page_start = queue_start;
        let mut head: Option<QueuedTaskRef> = None;
        loop {
            let rows = reader.scan(
                ColumnFamily::PartitionIndex,
                &page_start,
                &queue_end,
                SEEK_PAGE_ROWS,
            )?;
            for (key, value) in &rows {
                row_count += 1;
                if head.is_some() {
                    continue;
                }
                let Some(task) = decode_queue_row(reader.as_ref(), key, value)? else {
                    continue;
                };
                scanned_heads
                    .entry(entry.queue.clone())
                    .or_insert_with(|| task.position.clone());
                if !context.attempt_entries().is_held(&ref_attempt_key(&task)) {
                    head = Some(task);
                }
            }
            match rows.last() {
                Some((last_key, _)) if rows.len() == SEEK_PAGE_ROWS => {
                    page_start.clone_from(last_key);
                    page_start.push(0);
                }
                _ => break,
            }
        }
        heads.extend(head);
        cursor = queue_end;
    }
    let head_count = heads.len();
    // Heads only lower refs, so a Queued that arrived first is never lost.
    context.dispatcher.report_rebuilt_heads(heads);
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
    memory.entry_counts.insert(partition, row_count);
    memory.owned_partitions.insert(partition);
    let total = memory.entry_counts.values().sum();
    drop(memory);
    context.metrics.step_task_queue_entries.set(total);
    Ok(head_count)
}

/// Runs periodic Heads, one partition at a time spread over the interval, and refreshes the
/// task queue gauges. A zero interval disables the scans only.
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
                    scan_partition(&context, partition);
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

/// Scans the queues of `partition` that had backlog at the last scan or were written since,
/// from the smaller of the remembered head and the smallest written key.
fn scan_partition(context: &EffectContext, partition: PartitionId) {
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
    let mut results = Vec::new();
    for queue in queues {
        let from = [scanned.get(&queue), written.get(&queue)]
            .into_iter()
            .flatten()
            .min()
            .cloned()
            .expect("a scanned queue was remembered or written");
        match scan_queue(context, partition, &queue, &from) {
            Ok(Some((result, first_live))) => {
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
        }
    }
    context.dispatcher.report_scanned_heads(token, results);
}

/// Seeks one queue from `from`, skipping rows a holder sends back and rows no longer pending.
///
/// Returns the scan result and the first live row seen, `None` when a seek met too many
/// deleted entries, or the read's error.
fn scan_queue(
    context: &EffectContext,
    partition: PartitionId,
    queue: &DispatchQueueName,
    from: &DispatchQueuePosition,
) -> Result<Option<(ScannedQueueHead, Option<DispatchQueuePosition>)>, EffectError> {
    let reader = context.flow_effect_operations.committed_state_reader()?;
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
        let Some(task) = decode_queue_row(reader.as_ref(), &key, &value)? else {
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
