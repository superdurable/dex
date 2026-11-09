// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The durable task queue in matching (plan section 3.2); backlog comes last (plan D56).

use super::*;
use std::collections::{BTreeSet, VecDeque};

use dexzd_metrics::UnservedTaskQueueReason;
use std::sync::atomic::{AtomicBool, Ordering};

use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;

use crate::task_backlog::{
    ClaimReply, HeadsScanToken, QueuedTaskRef, ScannedQueueHead, TaskBacklog,
};

/// Matching asks a row's partition owner to start it on a reserved stream. The owner must
/// answer with [`WorkerDispatch::reply_to_claim`]; dropping the stream returns it.
pub struct TaskClaim {
    /// Identifies the Claim in its reply.
    pub claim_id: u64,
    /// The claimed row, possibly stale.
    pub task: QueuedTaskRef,
    /// The reserved Worker slot.
    pub stream: ReservedStepStream,
    /// How often matching had removed the row's partition when it delivered the Claim; see
    /// [`WorkerDispatch::is_partition_unremoved_since`].
    pub partition_removals: u64,
}

/// A Claim waiting for the partition owner to take it.
pub(super) struct PendingClaim {
    claim_id: u64,
    task: QueuedTaskRef,
    worker: Arc<WorkerStream>,
}

/// Lets [`WorkerDispatch::remove_partition`] end a partition's in-memory searches.
pub(super) struct PartitionSearch {
    is_removed: AtomicBool,
    pub(super) wakeup: Notify,
}

impl PartitionSearch {
    pub(super) fn is_removed(&self) -> bool {
        self.is_removed.load(Ordering::Acquire)
    }
}

/// Completes once the search's partition is removed; never without a partition.
pub(super) async fn partition_removed(search: Option<&PartitionSearch>) {
    match search {
        Some(search) => search.wakeup.notified().await,
        None => std::future::pending().await,
    }
}

/// Unregisters a search from its partition when it ends.
pub(super) struct PartitionSearchGuard {
    dispatch: WorkerDispatch,
    partition: PartitionId,
    search_id: u64,
    pub(super) search: Arc<PartitionSearch>,
}

impl Drop for PartitionSearchGuard {
    fn drop(&mut self) {
        let mut partition_searches = self.dispatch.lock_partition_searches();
        if let Some(searches) = partition_searches.searches.get_mut(&self.partition) {
            searches.remove(&self.search_id);
            if searches.is_empty() {
                partition_searches.searches.remove(&self.partition);
            }
        }
    }
}

/// In-memory searches by partition, under their own lock so a search never takes the
/// registry lock to register.
#[derive(Default)]
pub(super) struct PartitionSearches {
    next_search_id: u64,
    searches: HashMap<PartitionId, HashMap<u64, Arc<PartitionSearch>>>,
}

#[cfg(test)]
impl PartitionSearches {
    pub(super) fn search_count_for_test(&self) -> usize {
        self.searches.values().map(HashMap::len).sum()
    }
}

/// Durable queue state guarded by the registry lock.
#[derive(Default)]
pub(super) struct QueueMatching {
    pub(super) backlog: TaskBacklog,
    claims: VecDeque<PendingClaim>,
    claims_ready: Arc<Notify>,
    /// Connected Step-capable streams per (tenant, Flow type), grouped by app Worker.
    step_workers: HashMap<(u64, String), HashMap<String, HashSet<Uuid>>>,
    /// How often each partition was removed; a delivered Claim must not start after a removal.
    partition_removals: HashMap<PartitionId, u64>,
}

impl QueueMatching {
    #[cfg(test)]
    pub(super) fn pending_claim_count_for_test(&self) -> usize {
        self.claims.len()
    }

    pub(super) fn add_step_stream(&mut self, worker: &WorkerStream) {
        for flow_type in &worker.backlog_flow_types {
            self.step_workers
                .entry((worker.tenant_id, flow_type.clone()))
                .or_default()
                .entry(worker.app_worker_id.clone())
                .or_default()
                .insert(worker.id);
        }
    }

    pub(super) fn remove_step_stream(&mut self, worker: &WorkerStream) {
        for flow_type in &worker.backlog_flow_types {
            let key = (worker.tenant_id, flow_type.clone());
            let Some(workers) = self.step_workers.get_mut(&key) else {
                continue;
            };
            if let Some(stream_ids) = workers.get_mut(&worker.app_worker_id) {
                stream_ids.remove(&worker.id);
                if stream_ids.is_empty() {
                    workers.remove(&worker.app_worker_id);
                }
            }
            if workers.is_empty() {
                self.step_workers.remove(&key);
            }
        }
    }

    fn removals_of(&self, partition: PartitionId) -> u64 {
        self.partition_removals
            .get(&partition)
            .copied()
            .unwrap_or_default()
    }
}

impl WorkerDispatch {
    /// Takes a committed DispatchQueue row's ref; the smaller ref per (partition, queue) wins.
    pub fn queued(&self, task: QueuedTaskRef) {
        let name = task.queue.clone();
        let mut registry = self.lock_registry();
        registry.queue_matching.backlog.insert(task);
        registry.match_backlog_queue(&name);
    }

    /// Takes the heads a partition rebuild read; each lowers the queue's ref, never raises it.
    pub fn report_rebuilt_heads(&self, heads: Vec<QueuedTaskRef>) {
        let mut registry = self.lock_registry();
        let names: BTreeSet<DispatchQueueName> =
            heads.iter().map(|head| head.queue.clone()).collect();
        for head in heads {
            registry.queue_matching.backlog.insert(head);
        }
        for name in &names {
            registry.match_backlog_queue(name);
        }
    }

    /// Starts a periodic Heads scan of `partition`; pass the token to
    /// [`Self::report_scanned_heads`].
    pub fn take_heads_token(&self, partition: PartitionId) -> HeadsScanToken {
        self.lock_registry()
            .queue_matching
            .backlog
            .begin_scan(partition)
    }

    /// Merges a periodic Heads scan and logs every ref it had to restore, which is a bug.
    ///
    /// Returns the number of restored refs.
    pub fn report_scanned_heads(
        &self,
        token: HeadsScanToken,
        results: Vec<ScannedQueueHead>,
    ) -> usize {
        let names: BTreeSet<DispatchQueueName> =
            results.iter().map(|result| result.queue.clone()).collect();
        let mut registry = self.lock_registry();
        let restored = registry.queue_matching.backlog.finish_scan(token, results);
        for name in &names {
            registry.match_backlog_queue(name);
        }
        drop(registry);
        let now_micros = self.inner.clock.now_unix_micros();
        for lost in &restored {
            let task = &lost.task;
            let waited_seconds =
                now_micros.saturating_sub(task.position.pending_since_micros) / 1_000_000;
            tracing::warn!(
                partition = task.partition.0,
                tenant_id = task.queue.tenant_id.0,
                flow_type = %task.queue.flow_type,
                step_type = %task.queue.step_type,
                waited_seconds,
                head = ?task.position,
                "periodic Heads restored a queued task that matching had lost"
            );
            self.inner.metrics.matching_heads_restored_refs.inc();
        }
        restored.len()
    }

    /// Applies the partition owner's reply to a Claim; a reply to a Claim that matching no
    /// longer tracks, such as after [`Self::remove_partition`], is dropped.
    pub fn reply_to_claim(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
        reply: ClaimReply,
    ) {
        let mut registry = self.lock_registry();
        if registry
            .queue_matching
            .backlog
            .finish_claim(queue, partition, claim_id, reply.next)
        {
            registry.match_backlog_queue(queue);
        }
    }

    /// Records when the first ordinary-priority row behind a priority-0 ref became pending, so
    /// the oldest-age gauge still sees it (plan section 3.10). It is ignored once no priority-0
    /// ref of the queue remains in `partition`.
    pub fn report_ordinary_backlog(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        pending_since_micros: Option<i64>,
    ) {
        self.lock_registry()
            .queue_matching
            .backlog
            .set_ordinary_pending_since(queue, partition, pending_since_micros);
    }

    /// Removes every ref, Claim in flight, and in-memory waiter of `partition`. Its sticky,
    /// sync, and RPC searches end as if matching were closing.
    ///
    /// Call it when this node gives up the partition; a successor, maybe this node, rebuilds it.
    /// A frame sent during the removal is an ordinary hand-out: its result commits through the
    /// owner, or the successor fails it as interrupted (plan D68).
    pub fn remove_partition(&self, partition: PartitionId) {
        let undelivered = self.remove_partition_matching_state(partition);
        self.end_partition_searches(partition);
        // Freed slots go to waiters first, then to other partitions' backlog.
        for pending in undelivered {
            self.make_idle(&pending.worker);
        }
    }

    /// Removes `partition`'s refs and undelivered Claims under the registry lock.
    ///
    /// Returns the removed undelivered Claims, whose streams the caller must make idle.
    pub(super) fn remove_partition_matching_state(
        &self,
        partition: PartitionId,
    ) -> VecDeque<PendingClaim> {
        let mut registry = self.lock_registry();
        *registry
            .queue_matching
            .partition_removals
            .entry(partition)
            .or_default() += 1;
        registry.queue_matching.backlog.remove_partition(partition);
        let (undelivered, kept): (VecDeque<PendingClaim>, VecDeque<PendingClaim>) = registry
            .queue_matching
            .claims
            .drain(..)
            .partition(|pending| pending.task.partition == partition);
        registry.queue_matching.claims = kept;
        undelivered
    }

    /// Ends every in-memory search registered for `partition` as if matching were closing.
    pub(super) fn end_partition_searches(&self, partition: PartitionId) {
        let searches = self
            .lock_partition_searches()
            .searches
            .remove(&partition)
            .unwrap_or_default();
        for search in searches.into_values() {
            search.is_removed.store(true, Ordering::Release);
            search.wakeup.notify_one();
        }
    }

    /// Returns how many times matching removed `partition`, to compare later with
    /// [`Self::is_partition_unremoved_since`].
    pub fn partition_removals(&self, partition: PartitionId) -> u64 {
        self.lock_registry().queue_matching.removals_of(partition)
    }

    /// Returns whether matching has not removed `partition` since a Claim was delivered with
    /// `partition_removals`. The partition owner checks it before it starts or sends the row.
    pub fn is_partition_unremoved_since(
        &self,
        partition: PartitionId,
        partition_removals: u64,
    ) -> bool {
        self.lock_registry().queue_matching.removals_of(partition) == partition_removals
    }

    /// Returns whether matching still waits for the reply to this Claim; a removed partition's
    /// Claims are no longer tracked, so their owner must not start them.
    pub fn is_claim_tracked(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
    ) -> bool {
        self.lock_registry()
            .queue_matching
            .backlog
            .is_claim_tracked(queue, partition, claim_id)
    }

    /// Waits for the next Claim. Claims stop once matching closes.
    ///
    /// Returns the Claim, or `None` when matching is closing and no Claim is left.
    pub async fn next_claim(&self) -> Option<TaskClaim> {
        let claims_ready = self.lock_registry().queue_matching.claims_ready.clone();
        loop {
            let notified = claims_ready.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            {
                let mut registry = self.lock_registry();
                if let Some(pending) = registry.queue_matching.claims.pop_front() {
                    let partition_removals =
                        registry.queue_matching.removals_of(pending.task.partition);
                    drop(registry);
                    return Some(TaskClaim {
                        claim_id: pending.claim_id,
                        task: pending.task,
                        stream: ReservedStepStream::new(self.clone(), pending.worker),
                        partition_removals,
                    });
                }
                if registry.is_closing {
                    return None;
                }
            }
            notified.await;
        }
    }

    /// Returns whether no connected stream of the Flow type may run the Step type, though the
    /// Flow type has Step streams: the reason for `WAITING_FOR_SUPPORTING_WORKER`.
    pub fn is_waiting_for_supporting_worker(
        &self,
        tenant_id: u64,
        flow_type: &str,
        step_type: &str,
    ) -> bool {
        let task_type = TaskTypeKey {
            tenant_id,
            flow_type: flow_type.to_owned(),
            kind: TaskKind::Step,
            name: step_type.to_owned(),
        };
        self.lock_registry()
            .has_only_unsupported_streams(tenant_id, flow_type, &task_type)
    }

    /// Refreshes the gauges of queues with backlog: the oldest pending time and the queues no
    /// connected stream can serve.
    pub fn refresh_task_queue_gauges(&self) {
        let registry = self.lock_registry();
        let now_micros = self.inner.clock.now_unix_micros();
        let oldest_age_seconds = registry
            .queue_matching
            .backlog
            .oldest_pending_since_micros()
            .map_or(0, |oldest| now_micros.saturating_sub(oldest) / 1_000_000);
        let mut no_worker = 0_i64;
        let mut no_supporting_worker = 0_i64;
        for name in registry.queue_matching.backlog.backlogged_queues() {
            match registry.backlog_service(name) {
                BacklogService::NoWorker => no_worker += 1,
                BacklogService::NoSupportingWorker => no_supporting_worker += 1,
                BacklogService::Served => {}
            }
        }
        drop(registry);
        let metrics = &self.inner.metrics;
        metrics
            .step_task_queue_oldest_age_seconds
            .set(oldest_age_seconds);
        metrics
            .matching_unserved_task_queues
            .set(UnservedTaskQueueReason::NoWorker, no_worker);
        metrics.matching_unserved_task_queues.set(
            UnservedTaskQueueReason::NoSupportingWorker,
            no_supporting_worker,
        );
    }

    /// Returns whether the queue has backlog that a connected stream may run (plan D53).
    pub(super) fn has_qualified_backlog(&self, name: &DispatchQueueName) -> bool {
        let registry = self.lock_registry();
        registry.queue_matching.backlog.has_backlog(name)
            && registry.backlog_service(name) == BacklogService::Served
    }

    /// Registers an in-memory search of `partition` so that removing the partition ends it.
    pub(super) fn register_partition_search(&self, partition: PartitionId) -> PartitionSearchGuard {
        let search = Arc::new(PartitionSearch {
            is_removed: AtomicBool::new(false),
            wakeup: Notify::new(),
        });
        let mut partition_searches = self.lock_partition_searches();
        partition_searches.next_search_id += 1;
        let search_id = partition_searches.next_search_id;
        partition_searches
            .searches
            .entry(partition)
            .or_default()
            .insert(search_id, search.clone());
        PartitionSearchGuard {
            dispatch: self.clone(),
            partition,
            search_id,
            search,
        }
    }
}

/// Whether a connected stream may serve a queue.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum BacklogService {
    Served,
    /// No connected stream serves the Flow type's Steps.
    NoWorker,
    /// Every connected Step stream declined the Step type.
    NoSupportingWorker,
}

impl Registry {
    /// Claims idle Step streams of the Flow type while the queue has a claimable head.
    pub(super) fn match_backlog_queue(&mut self, name: &DispatchQueueName) {
        // Skips walking idle streams when none may run the queue's Step type.
        if self.backlog_service(name) != BacklogService::Served {
            return;
        }
        // The Flow type's queue holds every idle stream that may claim its backlog.
        let flow_type_key = (name.tenant_id.0, name.flow_type.clone());
        loop {
            let Some(queue) = self.queues.get(&flow_type_key) else {
                return;
            };
            let candidate = queue
                .idle
                .values()
                .filter_map(|id| self.streams.get(id))
                .find(|worker| self.may_claim_for(worker, name))
                .cloned();
            let Some(worker) = candidate else {
                return;
            };
            if !self.claim_backlog_for(&worker) {
                return;
            }
        }
    }

    /// Takes `worker`, idle and promised to no waiter, for the earliest head it may run.
    ///
    /// Returns whether it claimed one.
    pub(super) fn claim_backlog_for(&mut self, worker: &Arc<WorkerStream>) -> bool {
        if !self.queue_matching.backlog.has_claimable_heads()
            || self.is_closing
            || !self.idle_positions.contains_key(&worker.id)
            || !worker.slot.may_run_steps()
            || self.is_promised_to_waiter(worker)
            || worker.step_backoff_end(Instant::now()).is_some()
        {
            return false;
        }
        let earliest = self.queue_matching.backlog.earliest_claimable(
            worker.tenant_id,
            worker.backlog_flow_types.iter(),
            |name| !self.is_unsupported_on(&step_type_key(name), worker),
        );
        let Some((name, partition)) = earliest else {
            return false;
        };
        let Some((claim_id, task)) = self.queue_matching.backlog.start_claim(&name, partition)
        else {
            return false;
        };
        self.take_idle(worker);
        let mut state = worker.state.lock().expect("Worker state lock poisoned");
        state.is_busy = true;
        drop(state);
        self.queue_matching.claims.push_back(PendingClaim {
            claim_id,
            task,
            worker: worker.clone(),
        });
        self.queue_matching.claims_ready.notify_one();
        true
    }

    /// Returns whether `worker` may claim a head of `name` now.
    fn may_claim_for(&self, worker: &WorkerStream, name: &DispatchQueueName) -> bool {
        worker.slot.may_run_steps()
            && worker.backlog_flow_types.contains(&name.flow_type)
            && !self.is_promised_to_waiter(worker)
            && worker.step_backoff_end(Instant::now()).is_none()
            && !self.is_unsupported_on(&step_type_key(name), worker)
    }

    /// Returns whether a woken waiter may still take `worker`.
    fn is_promised_to_waiter(&self, worker: &WorkerStream) -> bool {
        self.promised_streams.contains_key(&worker.id)
            || worker.queue_keys.iter().any(|queue_key| {
                self.queues.get(queue_key).is_some_and(|queue| {
                    queue
                        .woken_waiter_streams
                        .values()
                        .any(|id| *id == worker.id)
                })
            })
    }

    /// Returns whether any connected Step stream may serve `name`, in time proportional to
    /// the Workers that declined its Step type.
    pub(super) fn backlog_service(&self, name: &DispatchQueueName) -> BacklogService {
        let Some(workers) = self
            .queue_matching
            .step_workers
            .get(&(name.tenant_id.0, name.flow_type.clone()))
        else {
            return BacklogService::NoWorker;
        };
        // Agrees with Claim eligibility, which also excludes evicted declines awaiting a reprobe.
        let declining = self
            .unsupported_types
            .declining_or_reprobing_workers(&step_type_key(name))
            .filter(|app_worker_id| workers.contains_key(*app_worker_id))
            .count();
        if declining < workers.len() {
            BacklogService::Served
        } else {
            BacklogService::NoSupportingWorker
        }
    }
}

fn step_type_key(name: &DispatchQueueName) -> TaskTypeKey {
    TaskTypeKey {
        tenant_id: name.tenant_id.0,
        flow_type: name.flow_type.clone(),
        kind: TaskKind::Step,
        name: name.step_type.clone(),
    }
}

#[cfg(test)]
#[path = "backlog_matching_tests.rs"]
mod backlog_matching_tests;
