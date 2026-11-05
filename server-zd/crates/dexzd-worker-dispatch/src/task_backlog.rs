// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The in-memory side of the durable task queue (plan section 3.2): per queue, a min-heap of
//! the head ref of every partition with backlog, the Claim in flight per (partition, queue),
//! and the bookkeeping of periodic Heads scans. Matching never reads partition storage; the
//! partition owner sends refs with Queued, Heads, and Claim replies.

use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};

use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::{DispatchQueueName, DispatchQueuePosition};

/// A DispatchQueue row that matching may hand to a Worker. It may be stale; the partition owner
/// checks it when it is claimed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct QueuedTaskRef {
    /// Partition that owns the row; Claims go to its owner.
    pub partition: PartitionId,
    /// The row's queue.
    pub queue: DispatchQueueName,
    /// The row's key position in its queue.
    pub position: DispatchQueuePosition,
    /// Run attempt the row was queued in.
    pub run_attempt: u32,
    /// Flow ID of the row's run.
    pub flow_id: String,
}

/// How the partition owner answered a Claim.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ClaimResult {
    /// The attempt started on the claimed stream, which the owner keeps.
    Started,
    /// The row is no longer dispatchable.
    Stale,
    /// The row is valid, but a holder that sends it back later has it.
    Busy,
    /// The row waits for its locks; the owner sends it back once they are released.
    LockBusy,
    /// A transient failure; the owner sends the row back after a backoff.
    RetryLater,
}

/// The partition owner's reply to a Claim.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ClaimReply {
    /// What happened to the claimed row.
    pub result: ClaimResult,
    /// The queue's next row after the claimed key that no holder sends back, or `None` when the
    /// owner found none or could not read it (it then sends the row later itself).
    pub next: Option<QueuedTaskRef>,
}

/// One queue's result of a periodic Heads scan.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ScannedQueueHead {
    /// The scanned queue.
    pub queue: DispatchQueueName,
    /// Its first dispatchable row that no holder sends back, or `None` when it has none.
    pub head: Option<QueuedTaskRef>,
    /// Whether the head's Queued was sent and may still be on its way to matching.
    pub is_head_queued_in_flight: bool,
    /// Whether the scan skipped rows that a holder sends back later.
    pub has_skipped_held_rows: bool,
}

/// Identifies one periodic Heads scan of a partition.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HeadsScanToken {
    /// The scanned partition.
    pub partition: PartitionId,
    token_id: u64,
}

/// A ref that a periodic Heads scan put back because matching had lost it: an implementation bug.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct RestoredTaskRef {
    pub(crate) task: QueuedTaskRef,
}

type HeadOrder = (u8, i64, PartitionId);

#[derive(Default)]
struct PartitionRefs {
    /// The smallest ref inserted since the last Claim; claimable while no Claim is in flight.
    head: Option<QueuedTaskRef>,
    /// The Claim in flight: its ID and the ref it took.
    claim: Option<(u64, QueuedTaskRef)>,
}

#[derive(Default)]
struct QueueBacklog {
    partitions: BTreeMap<PartitionId, PartitionRefs>,
    /// Claimable heads in dispatch order.
    claimable: BTreeSet<HeadOrder>,
}

struct HeadsScan {
    token_id: u64,
    /// Queues that changed after the token, so the scan's view of them is old.
    changed: HashSet<DispatchQueueName>,
    /// The smallest key matching held or saw for each queue since the token.
    smallest_seen: HashMap<DispatchQueueName, DispatchQueuePosition>,
}

/// Matching's refs of every partition's backlog. The caller holds the registry lock and runs
/// matching for a queue after any change that may make it claimable.
#[derive(Default)]
pub(crate) struct TaskBacklog {
    queues: HashMap<DispatchQueueName, QueueBacklog>,
    /// Step types with a claimable head, per (tenant, Flow type).
    claimable_step_types: HashMap<(u64, String), BTreeSet<String>>,
    next_claim_id: u64,
    next_token_id: u64,
    scans: HashMap<PartitionId, HeadsScan>,
    /// Queues with refs or Claims per partition.
    partition_queues: HashMap<PartitionId, HashSet<DispatchQueueName>>,
}

impl TaskBacklog {
    /// Keeps the smaller of `task` and the partition's ref of its queue (Queued, rebuilt Heads,
    /// a Claim's next ref).
    pub(crate) fn insert(&mut self, task: QueuedTaskRef) {
        self.observe(task.partition, &task.queue, Some(&task.position));
        self.partition_queues
            .entry(task.partition)
            .or_default()
            .insert(task.queue.clone());
        let queue = self.queues.entry(task.queue.clone()).or_default();
        let refs = queue.partitions.entry(task.partition).or_default();
        if refs
            .head
            .as_ref()
            .is_some_and(|head| head.position <= task.position)
        {
            return;
        }
        let partition = task.partition;
        let name = task.queue.clone();
        refs.head = Some(task);
        self.refresh_claimable(&name, partition);
    }

    /// Returns whether the queue has backlog: a claimable head or a Claim in flight.
    pub(crate) fn has_backlog(&self, name: &DispatchQueueName) -> bool {
        self.queues.get(name).is_some_and(|queue| {
            queue
                .partitions
                .values()
                .any(|refs| refs.head.is_some() || refs.claim.is_some())
        })
    }

    /// Returns the queue and partition of the earliest claimable head among the Step types of
    /// `flow_types` that `is_eligible` accepts, by (priority, pending time).
    pub(crate) fn earliest_claimable<'a>(
        &self,
        tenant_id: u64,
        flow_types: impl Iterator<Item = &'a String>,
        mut is_eligible: impl FnMut(&DispatchQueueName) -> bool,
    ) -> Option<(DispatchQueueName, PartitionId)> {
        let mut best: Option<(HeadOrder, DispatchQueueName)> = None;
        for flow_type in flow_types {
            let Some(step_types) = self
                .claimable_step_types
                .get(&(tenant_id, flow_type.clone()))
            else {
                continue;
            };
            for step_type in step_types {
                let name = DispatchQueueName {
                    tenant_id: dexzd_keyspace::TenantId(tenant_id),
                    flow_type: flow_type.clone(),
                    step_type: step_type.clone(),
                };
                let Some(order) = self
                    .queues
                    .get(&name)
                    .and_then(|queue| queue.claimable.first())
                else {
                    continue;
                };
                if best
                    .as_ref()
                    .is_some_and(|(best_order, _)| best_order <= order)
                    || !is_eligible(&name)
                {
                    continue;
                }
                best = Some((*order, name));
            }
        }
        best.map(|((_, _, partition), name)| (name, partition))
    }

    /// Takes the claimable head of (`partition`, `name`) for a new Claim.
    ///
    /// Returns the Claim ID and its ref, or `None` when nothing is claimable there.
    pub(crate) fn start_claim(
        &mut self,
        name: &DispatchQueueName,
        partition: PartitionId,
    ) -> Option<(u64, QueuedTaskRef)> {
        let refs = self.queues.get_mut(name)?.partitions.get_mut(&partition)?;
        if refs.claim.is_some() {
            return None;
        }
        let task = refs.head.take()?;
        self.next_claim_id += 1;
        let claim_id = self.next_claim_id;
        refs.claim = Some((claim_id, task.clone()));
        self.observe(partition, name, Some(&task.position));
        self.refresh_claimable(name, partition);
        Some((claim_id, task))
    }

    /// Applies a Claim reply: drops the claimed ref, whatever the result, and keeps the smaller
    /// of the reply's next ref and any ref inserted while the Claim was in flight.
    ///
    /// Returns false for a reply whose Claim matching no longer tracks; it changes nothing.
    pub(crate) fn finish_claim(
        &mut self,
        name: &DispatchQueueName,
        partition: PartitionId,
        claim_id: u64,
        next: Option<QueuedTaskRef>,
    ) -> bool {
        let is_tracked = self
            .queues
            .get(name)
            .and_then(|queue| queue.partitions.get(&partition))
            .and_then(|refs| refs.claim.as_ref())
            .is_some_and(|(tracked_id, _)| *tracked_id == claim_id);
        if !is_tracked {
            return false;
        }
        if let Some(refs) = self
            .queues
            .get_mut(name)
            .and_then(|queue| queue.partitions.get_mut(&partition))
        {
            refs.claim = None;
        }
        self.observe(partition, name, None);
        if let Some(next) = next.filter(|next| next.partition == partition && next.queue == *name) {
            self.insert(next);
        }
        self.refresh_claimable(name, partition);
        true
    }

    /// Forgets every ref and Claim of `partition`. Replies to its Claims are then dropped.
    pub(crate) fn remove_partition(&mut self, partition: PartitionId) {
        let names = self
            .partition_queues
            .get(&partition)
            .cloned()
            .unwrap_or_default();
        for name in names {
            if let Some(queue) = self.queues.get_mut(&name) {
                queue.partitions.remove(&partition);
            }
            self.refresh_claimable(&name, partition);
        }
        self.partition_queues.remove(&partition);
        self.scans.remove(&partition);
    }

    /// Starts a periodic Heads scan of `partition`, replacing an unfinished one.
    pub(crate) fn begin_scan(&mut self, partition: PartitionId) -> HeadsScanToken {
        self.next_token_id += 1;
        let mut smallest_seen = HashMap::new();
        let names = self.partition_queues.get(&partition).into_iter().flatten();
        for (name, queue) in names.filter_map(|name| Some((name, self.queues.get(name)?))) {
            if let Some(refs) = queue.partitions.get(&partition) {
                let held = refs
                    .head
                    .iter()
                    .chain(refs.claim.iter().map(|(_, task)| task))
                    .map(|task| task.position.clone())
                    .min();
                if let Some(position) = held {
                    smallest_seen.insert(name.clone(), position);
                }
            }
        }
        self.scans.insert(
            partition,
            HeadsScan {
                token_id: self.next_token_id,
                changed: HashSet::new(),
                smallest_seen,
            },
        );
        HeadsScanToken {
            partition,
            token_id: self.next_token_id,
        }
    }

    /// Merges a periodic Heads scan (plan section 3.2).
    ///
    /// Returns the refs the scan restored because matching had lost them; each one is a bug.
    pub(crate) fn finish_scan(
        &mut self,
        token: HeadsScanToken,
        results: Vec<ScannedQueueHead>,
    ) -> Vec<RestoredTaskRef> {
        let Some(scan) = self
            .scans
            .remove(&token.partition)
            .filter(|scan| scan.token_id == token.token_id)
        else {
            return Vec::new();
        };
        let mut restored = Vec::new();
        for result in results {
            let partition = token.partition;
            let current = self
                .queues
                .get(&result.queue)
                .and_then(|queue| queue.partitions.get(&partition));
            let is_claim_in_flight = current.is_some_and(|refs| refs.claim.is_some());
            let current_head = current.and_then(|refs| refs.head.clone());
            let is_crossed = is_claim_in_flight || scan.changed.contains(&result.queue);
            let Some(head) = result
                .head
                .filter(|head| head.partition == partition && head.queue == result.queue)
            else {
                if !is_crossed && !result.has_skipped_held_rows {
                    self.clear_head(&result.queue, partition);
                }
                continue;
            };
            let is_lost = if is_crossed {
                scan.smallest_seen
                    .get(&result.queue)
                    .is_none_or(|smallest| head.position < *smallest)
            } else {
                current_head
                    .as_ref()
                    .is_none_or(|current| head.position < current.position)
            };
            if is_lost && !result.is_head_queued_in_flight {
                restored.push(RestoredTaskRef { task: head.clone() });
            }
            let is_current_smaller = current_head
                .as_ref()
                .is_some_and(|current| current.position < head.position);
            if is_current_smaller && !is_crossed && !result.has_skipped_held_rows {
                // A smaller ref is stale; the scan saw every row before the head.
                self.clear_head(&result.queue, partition);
            }
            self.insert(head);
        }
        restored
    }

    /// Returns the earliest pending time among every head and Claim, for the oldest-age gauge.
    pub(crate) fn oldest_pending_since_micros(&self) -> Option<i64> {
        self.queues
            .values()
            .flat_map(|queue| queue.partitions.values())
            .flat_map(|refs| {
                refs.head
                    .iter()
                    .chain(refs.claim.iter().map(|(_, task)| task))
            })
            .map(|task| task.position.pending_since_micros)
            .min()
    }

    /// Returns the queues with backlog.
    pub(crate) fn backlogged_queues(&self) -> impl Iterator<Item = &DispatchQueueName> {
        self.queues
            .iter()
            .filter(|(_, queue)| !queue.partitions.is_empty())
            .map(|(name, _)| name)
    }

    fn clear_head(&mut self, name: &DispatchQueueName, partition: PartitionId) {
        if let Some(refs) = self
            .queues
            .get_mut(name)
            .and_then(|queue| queue.partitions.get_mut(&partition))
        {
            refs.head = None;
        }
        self.refresh_claimable(name, partition);
    }

    /// Records a change of (`partition`, `name`) for a running scan of the partition.
    fn observe(
        &mut self,
        partition: PartitionId,
        name: &DispatchQueueName,
        position: Option<&DispatchQueuePosition>,
    ) {
        let Some(scan) = self.scans.get_mut(&partition) else {
            return;
        };
        scan.changed.insert(name.clone());
        if let Some(position) = position {
            let smallest = scan
                .smallest_seen
                .entry(name.clone())
                .or_insert_with(|| position.clone());
            if position < smallest {
                *smallest = position.clone();
            }
        }
    }

    /// Re-indexes the claimable head of (`partition`, `name`) and drops empty entries.
    fn refresh_claimable(&mut self, name: &DispatchQueueName, partition: PartitionId) {
        let Some(queue) = self.queues.get_mut(name) else {
            return;
        };
        queue
            .claimable
            .retain(|(_, _, claimable_partition)| *claimable_partition != partition);
        if let Some(refs) = queue.partitions.get(&partition) {
            match (&refs.head, &refs.claim) {
                (Some(head), None) => {
                    queue.claimable.insert((
                        head.position.priority,
                        head.position.pending_since_micros,
                        partition,
                    ));
                }
                (None, None) => {
                    queue.partitions.remove(&partition);
                }
                _ => {}
            }
        }
        if !queue.partitions.contains_key(&partition)
            && let Some(names) = self.partition_queues.get_mut(&partition)
        {
            names.remove(name);
            if names.is_empty() {
                self.partition_queues.remove(&partition);
            }
        }
        let type_key = (name.tenant_id.0, name.flow_type.clone());
        if queue.claimable.is_empty() {
            if let Some(step_types) = self.claimable_step_types.get_mut(&type_key) {
                step_types.remove(&name.step_type);
                if step_types.is_empty() {
                    self.claimable_step_types.remove(&type_key);
                }
            }
        } else {
            self.claimable_step_types
                .entry(type_key)
                .or_default()
                .insert(name.step_type.clone());
        }
        if queue.partitions.is_empty() {
            self.queues.remove(name);
        }
    }
}

#[cfg(test)]
#[path = "task_backlog/task_backlog_tests.rs"]
mod task_backlog_tests;
