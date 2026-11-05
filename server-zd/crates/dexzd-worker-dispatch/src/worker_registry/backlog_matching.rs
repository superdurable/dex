// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The durable task queue in matching (plan section 3.2): Queued, Heads, Claims and their
//! replies, partition removal, and the wakeup invariant. An idle stream that no RPC, sticky, or
//! sync waiter takes claims the earliest head it may run (plan D56).

use super::*;
use std::collections::{BTreeMap, BTreeSet, VecDeque};

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
        let mut registry = self.dispatch.lock_registry();
        if let Some(searches) = registry.queue_matching.searches.get_mut(&self.partition) {
            searches.remove(&self.search_id);
            if searches.is_empty() {
                registry.queue_matching.searches.remove(&self.partition);
            }
        }
    }
}

/// Durable queue state guarded by the registry lock.
#[derive(Default)]
pub(super) struct QueueMatching {
    pub(super) backlog: TaskBacklog,
    claims: VecDeque<PendingClaim>,
    claims_ready: Arc<Notify>,
    next_search_id: u64,
    searches: HashMap<PartitionId, HashMap<u64, Arc<PartitionSearch>>>,
    /// Connected Step-capable streams per (tenant, Flow type), counted per app Worker.
    step_workers: HashMap<(u64, String), HashMap<String, usize>>,
    /// Idle Step-capable streams per (tenant, Flow type), in idle order.
    idle_step_streams: HashMap<(u64, String), BTreeMap<u64, Uuid>>,
}

impl QueueMatching {
    pub(super) fn add_step_stream(&mut self, worker: &WorkerStream) {
        for flow_type in &worker.backlog_flow_types {
            *self
                .step_workers
                .entry((worker.tenant_id, flow_type.clone()))
                .or_default()
                .entry(worker.app_worker_id.clone())
                .or_default() += 1;
        }
    }

    pub(super) fn remove_step_stream(&mut self, worker: &WorkerStream) {
        for flow_type in &worker.backlog_flow_types {
            let key = (worker.tenant_id, flow_type.clone());
            let Some(workers) = self.step_workers.get_mut(&key) else {
                continue;
            };
            if let Some(count) = workers.get_mut(&worker.app_worker_id) {
                *count -= 1;
                if *count == 0 {
                    workers.remove(&worker.app_worker_id);
                }
            }
            if workers.is_empty() {
                self.step_workers.remove(&key);
            }
        }
    }

    pub(super) fn add_idle_step_stream(&mut self, worker: &WorkerStream, position: u64) {
        for flow_type in &worker.backlog_flow_types {
            self.idle_step_streams
                .entry((worker.tenant_id, flow_type.clone()))
                .or_default()
                .insert(position, worker.id);
        }
    }

    pub(super) fn take_idle_step_stream(&mut self, worker: &WorkerStream, position: u64) {
        for flow_type in &worker.backlog_flow_types {
            let key = (worker.tenant_id, flow_type.clone());
            if let Some(idle) = self.idle_step_streams.get_mut(&key) {
                idle.remove(&position);
                if idle.is_empty() {
                    self.idle_step_streams.remove(&key);
                }
            }
        }
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

    /// Removes every ref, Claim in flight, and in-memory waiter of `partition`. Its sticky,
    /// sync, and RPC searches end as if matching were closing.
    pub fn remove_partition(&self, partition: PartitionId) {
        let mut registry = self.lock_registry();
        registry.queue_matching.backlog.remove_partition(partition);
        let searches = registry
            .queue_matching
            .searches
            .remove(&partition)
            .unwrap_or_default();
        drop(registry);
        for search in searches.into_values() {
            search.is_removed.store(true, Ordering::Release);
            search.wakeup.notify_one();
        }
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
                    drop(registry);
                    return Some(TaskClaim {
                        claim_id: pending.claim_id,
                        task: pending.task,
                        stream: ReservedStepStream::new(self.clone(), pending.worker),
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
        let mut registry = self.lock_registry();
        registry.queue_matching.next_search_id += 1;
        let search_id = registry.queue_matching.next_search_id;
        registry
            .queue_matching
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
    /// Claims for idle Step streams of the queue's Flow type while the queue has a claimable
    /// head and an idle stream may run it, keeping the wakeup invariant.
    pub(super) fn match_backlog_queue(&mut self, name: &DispatchQueueName) {
        let flow_type_key = (name.tenant_id.0, name.flow_type.clone());
        loop {
            let Some(idle) = self.queue_matching.idle_step_streams.get(&flow_type_key) else {
                return;
            };
            let candidate = idle
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
        if self.is_closing
            || !self.idle_positions.contains_key(&worker.id)
            || !worker.slot.may_run_steps()
            || worker.step_backoff_end(Instant::now()).is_some()
        {
            return false;
        }
        let unsupported_types = &self.unsupported_types;
        let earliest = self.queue_matching.backlog.earliest_claimable(
            worker.tenant_id,
            worker.backlog_flow_types.iter(),
            |name| !unsupported_types.contains(&step_type_key(name), &worker.app_worker_id),
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
            && !self
                .unsupported_types
                .contains(&step_type_key(name), &worker.app_worker_id)
    }

    /// Returns whether a woken waiter may still take `worker`.
    fn is_promised_to_waiter(&self, worker: &WorkerStream) -> bool {
        worker.queues.iter().any(|queue_name| {
            self.queues
                .get(&(worker.tenant_id, queue_name.clone()))
                .is_some_and(|queue| {
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
        let declining = self
            .unsupported_types
            .declining_workers(&step_type_key(name))
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
