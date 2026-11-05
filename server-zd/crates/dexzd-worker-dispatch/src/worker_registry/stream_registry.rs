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
    pub(super) unsupported_types: UnsupportedTypeCache,
    pub(super) queue_matching: QueueMatching,
}

impl Registry {
    /// Adds `worker` to the idle order of every queue it serves.
    pub(super) fn add_idle(&mut self, worker: &WorkerStream) {
        self.next_idle_position += 1;
        let position = self.next_idle_position;
        self.idle_positions.insert(worker.id, position);
        for queue_name in &worker.queues {
            if let Some(queue) = self.queues.get_mut(&(worker.tenant_id, queue_name.clone())) {
                queue.idle.insert(position, worker.id);
            }
        }
        self.queue_matching.add_idle_step_stream(worker, position);
    }

    /// Removes `worker` from the idle order of every queue it serves.
    pub(super) fn take_idle(&mut self, worker: &WorkerStream) {
        let Some(position) = self.idle_positions.remove(&worker.id) else {
            return;
        };
        for queue_name in &worker.queues {
            if let Some(queue) = self.queues.get_mut(&(worker.tenant_id, queue_name.clone())) {
                queue.idle.remove(&position);
            }
        }
        self.queue_matching.take_idle_step_stream(worker, position);
    }

    /// Wakes the waiter that should take the newly idle `worker` among all queues it serves:
    /// the longest-waiting RPC, else the longest-waiting Step (plan D56).
    pub(super) fn wake_waiter_for(&mut self, worker: &WorkerStream, now: Instant) {
        let is_in_step_backoff = worker.step_backoff_end(now).is_some();
        let mut step_choice: Option<(u64, String)> = None;
        let mut rpc_choice: Option<(u64, String)> = None;
        for queue_name in &worker.queues {
            let Some(queue) = self.queues.get(&(worker.tenant_id, queue_name.clone())) else {
                continue;
            };
            let mut has_queue_step_choice = false;
            for (sequence, waiter) in &queue.waiters {
                if !waiter.can_use(worker, now) {
                    continue;
                }
                let is_step_allowed =
                    !is_in_step_backoff && worker.slot != StreamSlot::ReservedRpcOnly;
                let choice = match waiter.kind {
                    TaskKind::Rpc => &mut rpc_choice,
                    TaskKind::Step if !is_step_allowed || has_queue_step_choice => continue,
                    TaskKind::Step => {
                        has_queue_step_choice = true;
                        &mut step_choice
                    }
                };
                if choice.as_ref().is_none_or(|(chosen, _)| sequence < chosen) {
                    *choice = Some((*sequence, queue_name.clone()));
                }
                // Waiters are in arrival order, so the queue's first eligible RPC is its oldest.
                if waiter.kind == TaskKind::Rpc {
                    break;
                }
            }
        }
        let Some((sequence, queue_name)) = rpc_choice.or(step_choice) else {
            // Backlog comes after every waiter (plan D56).
            if let Some(worker) = self.streams.get(&worker.id).cloned() {
                self.claim_backlog_for(&worker);
            }
            return;
        };
        let queue = self
            .queues
            .get_mut(&(worker.tenant_id, queue_name))
            .expect("the chosen waiter's queue exists");
        let waiter = queue
            .waiters
            .remove(&sequence)
            .expect("the chosen waiter exists");
        queue.woken_waiter_streams.insert(sequence, worker.id);
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
        workers.peek().is_some()
            && workers.all(|worker| {
                self.unsupported_types
                    .contains(task_type, &worker.app_worker_id)
            })
    }
}
