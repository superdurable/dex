// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The partition owner's side of the durable task queue (plan section 3.2): refs of committed
//! DispatchQueue rows, Claims, Heads, and the seeks behind them. Matching never reads storage.

mod queue_claims;
mod queue_heads;

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex};

use dexzd_apply::dispatch_queue_entry;
use dexzd_keyspace::dispatch_queue_keys::{
    DispatchQueueEntry, DispatchQueueName, DispatchQueuePosition, decode_dispatch_queue_key,
    dispatch_queue_key, dispatch_queue_prefix,
};
use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_protocol::dexzd::v1::{DispatchQueueRecord, StepInvocationKey, StepInvocationPhase};
use dexzd_worker_dispatch::{ClaimReply, ClaimResult, QueuedTaskRef};

use crate::effects::method_request::MethodAttemptKey;
use crate::effects::state_reads::{RunLocation, decode_row, read_run_record, read_step_execution};
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::FlowAddress;

pub(crate) use queue_claims::run_claim_pump;
pub(crate) use queue_heads::{rebuild_heads, run_heads_resync};

/// Rows read per page while seeking past rows that holders send back.
const SEEK_PAGE_ROWS: usize = 64;

/// The owner's memory of its queues: what periodic Heads scans resume from and the row counts
/// behind the queue entries gauge. Losing it on restart is fine; rebuild scans everything.
#[derive(Default)]
pub(crate) struct TaskQueueMemory {
    /// Smallest key written since the last scan, per (partition, queue).
    written_since_scan: BTreeMap<(PartitionId, DispatchQueueName), DispatchQueuePosition>,
    /// First live row the last scan saw, per (partition, queue).
    scanned_heads: BTreeMap<(PartitionId, DispatchQueueName), DispatchQueuePosition>,
    /// DispatchQueue rows per owned partition.
    entry_counts: BTreeMap<PartitionId, i64>,
    /// Partitions this node rebuilt and serves.
    owned_partitions: BTreeSet<PartitionId>,
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

    /// Adds `delta` rows to the partition's count.
    ///
    /// Returns the count of every owned partition.
    pub(crate) fn add_entries(&mut self, partition: PartitionId, delta: i64) -> i64 {
        *self.entry_counts.entry(partition).or_default() += delta;
        self.entry_counts.values().sum()
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

/// Seeks the committed queue for its first row after `from` (or at it, when `is_inclusive`)
/// that no holder sends back, and, when `should_check_pending`, that is still pending.
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
        let rows = reader.scan(ColumnFamily::PartitionIndex, &start, &end, SEEK_PAGE_ROWS)?;
        for (key, value) in &rows {
            let Some(task) = decode_queue_row(reader.as_ref(), key, value)? else {
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
        let Some((last_key, _)) = rows.last() else {
            return Ok(None);
        };
        if rows.len() < SEEK_PAGE_ROWS {
            return Ok(None);
        }
        start.clone_from(last_key);
        start.push(0);
    }
}

/// Answers a Claim with the queue's next ref after the claimed row. When the seek fails, the
/// reply carries none and the owner sends it later (plan section 3.2, "owed next ref").
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
    context.dispatcher.reply_to_claim(
        task.partition,
        &task.queue,
        claim_id,
        ClaimReply { result, next },
    );
}

/// Sends the queue's first row at or after `task`'s key that no holder sends back and that is
/// still pending, retrying reads after a backoff until one succeeds.
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
    let total = memory.add_entries(partition, written_count - i64::from(removed_count));
    drop(memory);
    context.metrics.step_task_queue_entries.set(total);
}

/// A lock-guarded [`TaskQueueMemory`].
pub(crate) type SharedTaskQueueMemory = Mutex<TaskQueueMemory>;
