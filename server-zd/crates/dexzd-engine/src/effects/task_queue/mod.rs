// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The partition owner's side of the durable task queue (plan section 3.2).

mod queue_claims;
mod queue_heads;

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex};

use dexzd_apply::{BoundedRowSeek, dispatch_queue_entry};
use dexzd_keyspace::dispatch_queue_keys::{
    DEFAULT_DISPATCH_PRIORITY, DispatchQueueEntry, DispatchQueueName, DispatchQueuePosition,
    decode_dispatch_queue_key, dispatch_queue_key, dispatch_queue_prefix,
};
use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId, RunId};
use dexzd_protocol::dexzd::v1::{DispatchQueueRecord, StepInvocationKey, StepInvocationPhase};
use dexzd_worker_dispatch::{ClaimReply, ClaimResult, QueuedTaskRef};

use crate::effects::method_request::MethodAttemptKey;
use crate::effects::state_reads::{RunLocation, decode_row, read_run_record, read_step_execution};
use crate::effects::{EffectContext, EffectError, FlowEffectOperationError};
use crate::flow_coordination::FlowAddress;

pub(crate) use queue_claims::run_claim_pump;
pub(crate) use queue_heads::{count_partition_rows, rebuild_heads, run_heads_resync};

/// What periodic Heads resume from and the row counts; rebuild recreates it.
#[derive(Default)]
pub(crate) struct TaskQueueMemory {
    /// Smallest key written since the last scan, per (partition, queue).
    written_since_scan: BTreeMap<(PartitionId, DispatchQueueName), DispatchQueuePosition>,
    /// First live row the last scan saw, per (partition, queue).
    scanned_heads: BTreeMap<(PartitionId, DispatchQueueName), DispatchQueuePosition>,
    /// DispatchQueue rows per owned partition.
    entry_counts: BTreeMap<PartitionId, PartitionRowCount>,
    /// Counted rows of every owned partition.
    total_entries: i64,
    /// Partitions this node rebuilt and serves.
    owned_partitions: BTreeSet<PartitionId>,
}

/// A committed command's log batch as (incarnation, seq), the order of one node's batches.
pub(crate) type QueueChangePosition = (u32, u64);

/// DispatchQueue rows of one partition: a snapshot count plus the committed changes after it.
#[derive(Default)]
struct PartitionRowCount {
    /// Counted rows, or `None` while a count runs.
    rows: Option<i64>,
    /// Applied batch of the counted snapshot; changes up to it are already counted.
    watermark: Option<QueueChangePosition>,
    /// Changes committed while the count runs.
    early_changes: Vec<(Option<QueueChangePosition>, i64)>,
}

impl TaskQueueMemory {
    /// Lowers the queue's smallest written key, so the next scan starts at or before it.
    pub(crate) fn note_written(&mut self, entry: &DispatchQueueEntry) {
        let slot = (entry.partition, entry.queue.clone());
        let smallest = self
            .written_since_scan
            .entry(slot)
            .or_insert_with(|| entry.position.clone());
        if entry.position < *smallest {
            *smallest = entry.position.clone();
        }
    }

    /// Adds a committed change of `delta` rows in the batch at `position`.
    ///
    /// Returns the count of every owned partition.
    pub(crate) fn add_entries(
        &mut self,
        partition: PartitionId,
        position: Option<QueueChangePosition>,
        delta: i64,
    ) -> i64 {
        let count = self.entry_counts.entry(partition).or_default();
        match count.rows.as_mut() {
            None => count.early_changes.push((position, delta)),
            Some(rows) if is_after(position, count.watermark) => {
                *rows += delta;
                self.total_entries += delta;
            }
            Some(_) => {}
        }
        self.total_entries
    }

    /// Forgets the partition's count; changes wait for [`Self::finish_row_count`].
    ///
    /// Returns the count of every owned partition.
    pub(crate) fn begin_row_count(&mut self, partition: PartitionId) -> i64 {
        let count = self.entry_counts.entry(partition).or_default();
        self.total_entries -= count.rows.take().unwrap_or(0);
        count.watermark = None;
        count.early_changes.clear();
        self.total_entries
    }

    /// Sets the partition's count from a snapshot applied up to `watermark`, then adds the
    /// changes committed after it.
    ///
    /// Returns the count of every owned partition.
    pub(crate) fn finish_row_count(
        &mut self,
        partition: PartitionId,
        snapshot_rows: i64,
        watermark: Option<QueueChangePosition>,
    ) -> i64 {
        let count = self.entry_counts.entry(partition).or_default();
        let later: i64 = count
            .early_changes
            .drain(..)
            .filter(|(position, _)| is_after(*position, watermark))
            .map(|(_, delta)| delta)
            .sum();
        let previous = count.rows.replace(snapshot_rows + later).unwrap_or(0);
        count.watermark = watermark;
        self.total_entries += snapshot_rows + later - previous;
        self.total_entries
    }
}

/// Returns whether a change at `position` is missing from a snapshot applied up to `watermark`.
fn is_after(position: Option<QueueChangePosition>, watermark: Option<QueueChangePosition>) -> bool {
    match (position, watermark) {
        (Some(position), Some(watermark)) => position > watermark,
        _ => true,
    }
}

/// Returns the ref of a DispatchQueue row.
pub(crate) fn queued_task_ref(
    entry: DispatchQueueEntry,
    run_attempt: u32,
    flow_id: String,
) -> QueuedTaskRef {
    QueuedTaskRef {
        partition: entry.partition,
        queue: entry.queue,
        position: entry.position,
        run_attempt,
        flow_id,
    }
}

/// Returns the attempt that a ref names.
pub(crate) fn ref_attempt_key(task: &QueuedTaskRef) -> MethodAttemptKey {
    MethodAttemptKey::new(
        task.position.run_id,
        task.run_attempt,
        &StepInvocationKey {
            step_execution_id: task.position.step_execution_id.clone(),
            method: i32::from(task.position.method),
        },
        task.position.attempt,
    )
}

/// Returns the Flow a ref belongs to.
pub(crate) fn ref_flow(task: &QueuedTaskRef) -> FlowAddress {
    FlowAddress {
        tenant_id: task.queue.tenant_id,
        flow_id: task.flow_id.clone(),
    }
}

/// Returns whether the speculative state still has the ref's attempt pending with the ref's
/// run attempt, position, and queue.
pub(crate) fn is_ref_pending(
    context: &EffectContext,
    task: &QueuedTaskRef,
) -> Result<bool, EffectError> {
    let reader = context
        .flow_effect_operations
        .flow_state_reader(&ref_flow(task), true)?;
    let run = RunLocation {
        flow_hash: task.position.flow_hash,
        run_id: task.position.run_id,
    };
    let Some(record) = read_run_record(reader.as_ref(), run)? else {
        return Ok(false);
    };
    if record.current_attempt != task.run_attempt {
        return Ok(false);
    }
    let Some(step) = read_step_execution(reader.as_ref(), run, &task.position.step_execution_id)?
    else {
        return Ok(false);
    };
    let is_pending = step
        .invocation
        .as_ref()
        .is_some_and(|invocation| invocation.phase == StepInvocationPhase::Pending as i32);
    let entry = dispatch_queue_entry(
        task.partition,
        &record,
        task.position.flow_hash,
        task.position.run_id,
        &step,
    );
    Ok(is_pending
        && entry.is_some_and(|entry| entry.position == task.position && entry.queue == task.queue))
}

/// Decodes a committed DispatchQueue row and the Flow of its run.
///
/// Returns `None` when the row's run no longer exists.
fn decode_queue_row(
    reader: &dyn dexzd_apply::FlowStateReader,
    key: &[u8],
    value: &[u8],
) -> Result<Option<QueuedTaskRef>, EffectError> {
    let entry = decode_dispatch_queue_key(key).ok_or_else(|| EffectError::CorruptState {
        table: "DispatchQueue",
        key: crate::effects::hex(key),
        detail: "malformed key".to_owned(),
    })?;
    let row: DispatchQueueRecord = decode_row("DispatchQueue", key, value)?;
    let run = RunLocation {
        flow_hash: entry.position.flow_hash,
        run_id: entry.position.run_id,
    };
    let Some(record) = read_run_record(reader, run)? else {
        return Ok(None);
    };
    Ok(Some(queued_task_ref(
        entry,
        row.run_attempt,
        record.flow_id,
    )))
}

/// Seeks the queue's first row after `from` that no holder sends back.
///
/// Returns the row's ref, `None` when there is none, or the read's error.
pub(crate) fn next_queue_ref(
    context: &EffectContext,
    partition: PartitionId,
    queue: &DispatchQueueName,
    from: &DispatchQueuePosition,
    is_inclusive: bool,
    should_check_pending: bool,
) -> Result<Option<QueuedTaskRef>, EffectError> {
    let reader = context.flow_effect_operations.committed_state_reader()?;
    let mut start = dispatch_queue_key(&DispatchQueueEntry {
        partition,
        queue: queue.clone(),
        position: from.clone(),
    });
    if !is_inclusive {
        start.push(0);
    }
    let (_, end) = prefix_range(&dispatch_queue_prefix(partition, queue));
    loop {
        let (key, value) = match reader.seek_first_bounded(
            ColumnFamily::PartitionIndex,
            &start,
            &end,
            context.config.heads_max_skipped_deletions,
        )? {
            BoundedRowSeek::Found(row) => row,
            BoundedRowSeek::Exhausted => return Ok(None),
            BoundedRowSeek::Incomplete => return Err(too_many_deleted_entries()),
        };
        start.clone_from(&key);
        start.push(0);
        let Some(task) = decode_queue_row(reader.as_ref(), &key, &value)? else {
            continue;
        };
        if context.attempt_entries().is_held(&ref_attempt_key(&task)) {
            continue;
        }
        if should_check_pending && !is_ref_pending(context, &task)? {
            continue;
        }
        return Ok(Some(task));
    }
}

/// Seeks the pending time of the queue's first ordinary-priority row, held or not.
///
/// Returns `None` when the queue has none.
pub(crate) fn first_ordinary_pending_since(
    reader: &dyn dexzd_apply::FlowStateReader,
    max_skipped_deletions: u64,
    partition: PartitionId,
    queue: &DispatchQueueName,
) -> Result<Option<i64>, EffectError> {
    let start = dispatch_queue_key(&DispatchQueueEntry {
        partition,
        queue: queue.clone(),
        position: DispatchQueuePosition {
            priority: DEFAULT_DISPATCH_PRIORITY,
            pending_since_micros: i64::MIN,
            flow_hash: FlowHash(0),
            run_id: RunId([0; 16]),
            method: 0,
            attempt: 0,
            step_execution_id: String::new(),
        },
    });
    let (_, end) = prefix_range(&dispatch_queue_prefix(partition, queue));
    let key = match reader.seek_first_bounded(
        ColumnFamily::PartitionIndex,
        &start,
        &end,
        max_skipped_deletions,
    )? {
        BoundedRowSeek::Found((key, _)) => key,
        BoundedRowSeek::Exhausted => return Ok(None),
        BoundedRowSeek::Incomplete => return Err(too_many_deleted_entries()),
    };
    let entry = decode_dispatch_queue_key(&key).ok_or_else(|| EffectError::CorruptState {
        table: "DispatchQueue",
        key: crate::effects::hex(&key),
        detail: "malformed key".to_owned(),
    })?;
    Ok(Some(entry.position.pending_since_micros))
}

/// Returns the retryable error of a seek that skipped too many deleted entries.
fn too_many_deleted_entries() -> EffectError {
    EffectError::FlowEffectOperations(FlowEffectOperationError::Unavailable {
        detail: "a DispatchQueue seek skipped too many deleted entries".to_owned(),
    })
}

/// Answers a Claim with the queue's next ref, or owes it when the seek fails.
pub(crate) fn reply_to_claim(
    context: &Arc<EffectContext>,
    task: &QueuedTaskRef,
    claim_id: u64,
    result: ClaimResult,
) {
    let next = match next_queue_ref(
        context,
        task.partition,
        &task.queue,
        &task.position,
        false,
        false,
    ) {
        Ok(next) => next,
        Err(error) => {
            tracing::warn!(%error, partition = task.partition.0, "a Claim reply has no next ref; sending it later");
            owe_next_ref(context, task.clone());
            None
        }
    };
    let is_next_priority_zero = next
        .as_ref()
        .is_some_and(|next| next.position.priority < DEFAULT_DISPATCH_PRIORITY);
    context.dispatcher.reply_to_claim(
        task.partition,
        &task.queue,
        claim_id,
        ClaimReply { result, next },
    );
    if is_next_priority_zero {
        report_ordinary_backlog(context, task.partition, &task.queue);
    }
}

/// Reports the queue's first ordinary-priority pending time behind its priority-0 ref.
pub(crate) fn report_ordinary_backlog(
    context: &EffectContext,
    partition: PartitionId,
    queue: &DispatchQueueName,
) {
    let pending_since = context
        .flow_effect_operations
        .committed_state_reader()
        .map_err(EffectError::from)
        .and_then(|reader| {
            first_ordinary_pending_since(
                reader.as_ref(),
                context.config.heads_max_skipped_deletions,
                partition,
                queue,
            )
        });
    match pending_since {
        Ok(pending_since) => {
            context
                .dispatcher
                .report_ordinary_backlog(partition, queue, pending_since);
        }
        // Only the oldest-age gauge waits for the next reply or scan.
        Err(error) => {
            tracing::debug!(%error, partition = partition.0, "ordinary backlog age unread")
        }
    }
}

/// Sends the first pending, unheld row at or after `task`, retrying failed reads.
pub(crate) fn owe_next_ref(context: &Arc<EffectContext>, task: QueuedTaskRef) {
    let context = Arc::clone(context);
    let tracker = context.tracker.clone();
    tracker.spawn(async move {
        let mut backoff = context.retry_backoff();
        loop {
            if !backoff.wait(&context.shutdown).await {
                return;
            }
            match next_queue_ref(
                &context,
                task.partition,
                &task.queue,
                &task.position,
                true,
                true,
            ) {
                Ok(Some(next)) => {
                    context.dispatcher.queued(next);
                    return;
                }
                Ok(None) => return,
                Err(error) => {
                    tracing::warn!(%error, partition = task.partition.0, "retrying an owed next ref");
                }
            }
        }
    });
}

/// Applies a committed DispatchQueueChanged effect to the scan memory and the entries gauge.
pub(crate) fn note_queue_change(
    context: &EffectContext,
    partition: PartitionId,
    position: Option<QueueChangePosition>,
    written_keys: &[Vec<u8>],
    removed_count: u32,
) {
    let mut memory = context.task_queue_memory();
    for key in written_keys {
        if let Some(entry) = decode_dispatch_queue_key(key) {
            memory.note_written(&entry);
        }
    }
    let written_count = i64::try_from(written_keys.len()).unwrap_or(i64::MAX);
    let total = memory.add_entries(
        partition,
        position,
        written_count - i64::from(removed_count),
    );
    drop(memory);
    context.metrics.step_task_queue_entries.set(total);
}

/// A lock-guarded [`TaskQueueMemory`].
pub(crate) type SharedTaskQueueMemory = Mutex<TaskQueueMemory>;
