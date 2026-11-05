// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The matching registry: registered streams, their queues, idle order, and waiter wakeups.

use super::*;

/// Matching state guarded by the registry lock.
pub(super) struct Registry {
    pub(super) is_closing: bool,
    pub(super) streams: HashMap<Uuid, Arc<WorkerStream>>,
    pub(super) worker_counts: HashMap<(u64, String), usize>,
    pub(super) queues: HashMap<(u64, String), QueueState>,
    pub(super) next_waiter_sequence: u64,
    /// Idle streams and their position in every subscribed queue's idle order.
    pub(super) idle_positions: HashMap<Uuid, u64>,
    pub(super) next_idle_position: u64,
    /// Idle streams offered to a woken RPC waiter, by its sequence; no other dispatch takes
    /// them (plan D56).
    pub(super) promised_streams: HashMap<Uuid, u64>,
    pub(super) unsupported_types: UnsupportedTypeCache,
}

impl Registry {
    /// Adds `worker` to the idle order of every queue it serves.
    pub(super) fn add_idle(&mut self, worker: &WorkerStream) {
        self.unsupported_types
            .release_reprobes(worker.tenant_id, &worker.app_worker_id);
        self.next_idle_position += 1;
        let position = self.next_idle_position;
        self.idle_positions.insert(worker.id, position);
        for queue_name in &worker.queues {
            if let Some(queue) = self.queues.get_mut(&(worker.tenant_id, queue_name.clone())) {
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
        for queue_name in &worker.queues {
            if let Some(queue) = self.queues.get_mut(&(worker.tenant_id, queue_name.clone())) {
                queue.idle.remove(&position);
            }
        }
    }

    /// Ends the wait of `waiter_sequence` in the queue `key`.
    ///
    /// Returns the stream its wakeup named, as [`QueueState::remove_waiter`] does; that stream
    /// is no longer promised to it.
    pub(super) fn end_wait(&mut self, key: &(u64, String), waiter_sequence: u64) -> Option<Uuid> {
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
    /// evicted decline waits for the Worker's next idle stream.
    pub(super) fn is_unsupported_on(&self, task_type: &TaskTypeKey, worker: &WorkerStream) -> bool {
        self.unsupported_types
            .excludes(task_type, &worker.app_worker_id)
    }

    /// Wakes the waiter that should take the newly idle `worker` among all queues it serves:
    /// the longest-waiting RPC, else the longest-waiting Step (plan D56). A woken RPC waiter
    /// gets the stream promised, so no other dispatch takes it first.
    pub(super) fn wake_waiter_for(&mut self, worker: &WorkerStream, now: Instant) {
        let mut rpc_choice: Option<(u64, String)> = None;
        for queue_name in &worker.queues {
            let Some(queue) = self.queues.get(&(worker.tenant_id, queue_name.clone())) else {
                continue;
            };
            if let Some(sequence) = self.first_waiter_for(&queue.rpc_waiters, worker, now)
                && rpc_choice
                    .as_ref()
                    .is_none_or(|(chosen, _)| sequence < *chosen)
            {
                rpc_choice = Some((sequence, queue_name.clone()));
            }
        }
        if let Some((sequence, queue_name)) = rpc_choice {
            self.wake_waiter(worker, TaskKind::Rpc, sequence, queue_name);
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
        let mut step_choice: Option<(u64, String)> = None;
        for queue_name in &worker.queues {
            let Some(queue) = self.queues.get(&(worker.tenant_id, queue_name.clone())) else {
                continue;
            };
            if let Some(sequence) = self.first_waiter_for(&queue.step_waiters, worker, now)
                && step_choice
                    .as_ref()
                    .is_none_or(|(chosen, _)| sequence < *chosen)
            {
                step_choice = Some((sequence, queue_name.clone()));
            }
        }
        if let Some((sequence, queue_name)) = step_choice {
            self.wake_waiter(worker, TaskKind::Step, sequence, queue_name);
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
        queue_name: String,
    ) {
        let queue = self
            .queues
            .get_mut(&(worker.tenant_id, queue_name))
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

    /// Returns whether `queue_name` has streams and the unsupported type cache excludes every
    /// one of them for `task_type`.
    pub(super) fn has_only_unsupported_streams(
        &self,
        tenant_id: u64,
        queue_name: &str,
        task_type: &TaskTypeKey,
    ) -> bool {
        let Some(queue) = self.queues.get(&(tenant_id, queue_name.to_owned())) else {
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
