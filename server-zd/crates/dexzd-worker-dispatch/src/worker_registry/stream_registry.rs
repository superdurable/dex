// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The matching registry: registered streams, their queues, idle order, and waiter wakeups.

use super::*;
use dexzd_request_validation::quote_go_string;

/// Matching state guarded by the registry lock.
pub(super) struct Registry {
    pub(super) is_closing: bool,
    pub(super) streams: HashMap<Uuid, Arc<WorkerStream>>,
    /// Registered stream IDs of each tenant's app Worker.
    pub(super) worker_stream_ids: HashMap<(u64, String), HashSet<Uuid>>,
    pub(super) queues: HashMap<MatchingQueueKey, QueueState>,
    /// Idle streams and their position in every subscribed queue's idle order.
    pub(super) idle_positions: HashMap<Uuid, u64>,
    pub(super) next_idle_position: u64,
    /// Idle streams offered to a woken RPC waiter, by its sequence; no other dispatch takes
    /// them (plan D56).
    pub(super) promised_streams: HashMap<Uuid, u64>,
    pub(super) unsupported_types: UnsupportedTypeCache,
    pub(super) queue_matching: QueueMatching,
}

impl Registry {
    /// Rejects `worker` when another stream of its app Worker serves another task queue.
    pub(super) fn check_one_task_queue_per_worker(
        &self,
        worker: &WorkerStream,
    ) -> Result<(), Status> {
        let other_task_queue = self
            .worker_stream_ids
            .get(&(worker.tenant_id, worker.app_worker_id.clone()))
            .into_iter()
            .flatten()
            .filter_map(|id| self.streams.get(id))
            .map(|other| other.task_queue.as_str())
            .find(|task_queue| *task_queue != worker.task_queue);
        match other_task_queue {
            Some(task_queue) => Err(Status::invalid_argument(format!(
                "app_worker_id {} already serves task queue {}; one app Worker serves one task queue",
                quote_go_string(&worker.app_worker_id),
                quote_go_string(task_queue)
            ))),
            None => Ok(()),
        }
    }

    /// Adds `worker` to the idle order of every queue it serves.
    pub(super) fn add_idle(&mut self, worker: &WorkerStream) {
        self.next_idle_position += 1;
        let position = self.next_idle_position;
        self.idle_positions.insert(worker.id, position);
        for queue_key in &worker.queue_keys {
            if let Some(queue) = self.queues.get_mut(queue_key) {
                queue.idle.insert(position, worker.id);
            }
        }
    }

    /// Removes `worker` from the idle order of every queue it serves.
    pub(super) fn take_idle(&mut self, worker: &WorkerStream) {
        self.promised_streams.remove(&worker.id);
        let Some(position) = self.idle_positions.remove(&worker.id) else {
            return;
        };
        for queue_key in &worker.queue_keys {
            if let Some(queue) = self.queues.get_mut(queue_key) {
                queue.idle.remove(&position);
            }
        }
    }

    /// Counts a dispatch as a user of queue `key` while it waits; removes the queue once unused.
    pub(super) fn settle_waiting_user(
        &mut self,
        key: &MatchingQueueKey,
        was_waiting: bool,
        is_waiting: bool,
    ) {
        let queue = self.queues.get_mut(key).expect("a picked queue exists");
        match (was_waiting, is_waiting) {
            (false, true) => queue.users += 1,
            (true, false) => queue.users -= 1,
            (false, false) | (true, true) => {}
        }
        if queue.users == 0 {
            self.queues.remove(key);
        }
    }

    /// Ends the wait of `waiter_sequence` in the queue `key`.
    ///
    /// Returns the stream its wakeup named, as [`QueueState::remove_waiter`] does; that stream
    /// is no longer promised to it.
    pub(super) fn end_wait(
        &mut self,
        key: &MatchingQueueKey,
        waiter_sequence: u64,
    ) -> Option<Uuid> {
        let woken_for = self
            .queues
            .get_mut(key)
            .expect("a waiting dispatch's queue exists")
            .remove_waiter(waiter_sequence);
        if let Some(stream_id) = woken_for
            && self.promised_streams.get(&stream_id) == Some(&waiter_sequence)
        {
            self.promised_streams.remove(&stream_id);
        }
        woken_for
    }

    /// Returns whether `worker` may not run `task_type`: its Worker declined the type, or an
    /// evicted decline waits for the Worker's next completed Step.
    pub(super) fn is_unsupported_on(&self, task_type: &TaskTypeKey, worker: &WorkerStream) -> bool {
        self.unsupported_types
            .excludes(task_type, &worker.app_worker_id)
    }

    /// Lets the evicted declines of `worker`'s Worker be probed again after it completed a Step,
    /// and offers its other idle streams to waiters that those declines kept asleep.
    pub(super) fn release_reprobes_after_step(&mut self, worker: &WorkerStream, now: Instant) {
        if !self
            .unsupported_types
            .release_reprobes(worker.tenant_id, &worker.app_worker_id)
        {
            return;
        }
        let idle_streams: Vec<Arc<WorkerStream>> = self
            .worker_stream_ids
            .get(&(worker.tenant_id, worker.app_worker_id.clone()))
            .into_iter()
            .flatten()
            .filter(|id| self.idle_positions.contains_key(id))
            .filter_map(|id| self.streams.get(id))
            .cloned()
            .collect();
        for idle in idle_streams {
            self.wake_waiter_for(&idle, now);
        }
    }

    /// Wakes the waiter that should take the newly idle `worker` among all queues it serves:
    /// the longest-waiting RPC, else the longest-waiting Step (plan D56). A woken RPC waiter
    /// gets the stream promised, so no other dispatch takes it first.
    pub(super) fn wake_waiter_for(&mut self, worker: &WorkerStream, now: Instant) {
        // The RPC waiter it was promised to takes it; a second wakeup must not move it.
        if self.promised_streams.contains_key(&worker.id) {
            return;
        }
        let mut rpc_choice: Option<(u64, &MatchingQueueKey)> = None;
        for queue_key in &worker.queue_keys {
            let Some(queue) = self.queues.get(queue_key) else {
                continue;
            };
            if let Some(sequence) = self.first_waiter_for(&queue.rpc_waiters, worker, now)
                && rpc_choice.is_none_or(|(chosen, _)| sequence < chosen)
            {
                rpc_choice = Some((sequence, queue_key));
            }
        }
        if let Some((sequence, queue_key)) = rpc_choice {
            self.wake_waiter(worker, TaskKind::Rpc, sequence, queue_key);
            return;
        }
        let is_step_allowed = match worker.slot {
            StreamSlot::ReservedRpcOnly => false,
            StreamSlot::Legacy => worker.step_backoff_end(now).is_none(),
            StreamSlot::ReservedStepAndRpc => true,
        };
        if !is_step_allowed {
            return;
        }
        let mut step_choice: Option<(u64, &MatchingQueueKey)> = None;
        for queue_key in &worker.queue_keys {
            let Some(queue) = self.queues.get(queue_key) else {
                continue;
            };
            if let Some(sequence) = self.first_waiter_for(&queue.step_waiters, worker, now)
                && step_choice.is_none_or(|(chosen, _)| sequence < chosen)
            {
                step_choice = Some((sequence, queue_key));
            }
        }
        if let Some((sequence, queue_key)) = step_choice {
            self.wake_waiter(worker, TaskKind::Step, sequence, queue_key);
            return;
        }
        // Backlog comes after every waiter (plan D56).
        if let Some(worker) = self.streams.get(&worker.id).cloned() {
            self.claim_backlog_for(&worker);
        }
    }

    /// Returns the oldest of `waiters` that may use `worker` now.
    fn first_waiter_for(
        &self,
        waiters: &BTreeMap<u64, QueueWaiter>,
        worker: &WorkerStream,
        now: Instant,
    ) -> Option<u64> {
        waiters
            .iter()
            .find(|(_, waiter)| {
                waiter.can_use(worker, now) && !self.is_unsupported_on(&waiter.task_type, worker)
            })
            .map(|(sequence, _)| *sequence)
    }

    fn wake_waiter(
        &mut self,
        worker: &WorkerStream,
        kind: TaskKind,
        sequence: u64,
        queue_key: &MatchingQueueKey,
    ) {
        let queue = self
            .queues
            .get_mut(queue_key)
            .expect("the chosen waiter's queue exists");
        let waiters = match kind {
            TaskKind::Rpc => &mut queue.rpc_waiters,
            TaskKind::Step => &mut queue.step_waiters,
        };
        let waiter = waiters.remove(&sequence).expect("the chosen waiter exists");
        queue.woken_waiter_streams.insert(sequence, worker.id);
        if kind == TaskKind::Rpc {
            self.promised_streams.insert(worker.id, sequence);
        }
        waiter.wakeup.notify_one();
    }

    /// Hands on the wakeup a dispatch consumed for the stream `woken_for`.
    ///
    /// Applies when that stream is still idle and was not `taken_stream`.
    pub(super) fn pass_on_wakeup(
        &mut self,
        woken_for: Option<Uuid>,
        taken_stream: Option<Uuid>,
        now: Instant,
    ) {
        let Some(stream_id) = woken_for
            .filter(|id| Some(*id) != taken_stream && self.idle_positions.contains_key(id))
        else {
            return;
        };
        let worker = self
            .streams
            .get(&stream_id)
            .expect("idle stream is registered")
            .clone();
        self.wake_waiter_for(&worker, now);
    }

    /// Returns whether `queue` has streams and the unsupported type cache excludes every one of
    /// them for `task_type`.
    pub(super) fn has_only_unsupported_streams(
        &self,
        queue: &MatchingQueueKey,
        task_type: &TaskTypeKey,
    ) -> bool {
        let Some(queue) = self.queues.get(queue) else {
            return false;
        };
        let mut workers = queue
            .stream_ids
            .iter()
            .filter_map(|id| self.streams.get(id))
            .filter(|worker| {
                task_type.kind == TaskKind::Rpc || worker.slot != StreamSlot::ReservedRpcOnly
            })
            .peekable();
        workers.peek().is_some() && workers.all(|worker| self.is_unsupported_on(task_type, worker))
    }
}
