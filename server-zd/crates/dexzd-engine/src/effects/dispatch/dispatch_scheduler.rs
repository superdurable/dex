// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Tracks pending and running method attempts for the effect router: one hand-out task per
//! attempt, invocations parked on busy locks, cancellation of started attempts, and attempts
//! that a restart interrupted.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Instant;

use dexzd_keyspace::RunId;
use tokio_util::sync::CancellationToken;

use crate::effects::dispatch::attempt_handout::{
    AttemptHandout, HandoutEnd, HandoutProgress, InterruptedAttempt, fail_interrupted_attempt,
};
use crate::effects::method_request::MethodAttemptKey;
use crate::effects::{EffectContext, RouterMessage};
use crate::flow_coordination::FlowAddress;

struct RunningAttempt {
    cancel: CancellationToken,
}

/// An attempt whose hand-out task runs.
struct InFlightEntry {
    /// Lock releases of the run already seen when the task last started.
    lock_release_count: u64,
}

struct ParkedInvocation {
    flow: FlowAddress,
    progress: HandoutProgress,
}

/// Dispatch state owned by the effect router task.
pub(crate) struct DispatchScheduler {
    context: Arc<EffectContext>,
    /// At most one hand-out task per attempt; duplicate pending effects find it here.
    in_flight: BTreeMap<MethodAttemptKey, InFlightEntry>,
    parked: BTreeMap<RunId, BTreeMap<MethodAttemptKey, ParkedInvocation>>,
    lock_release_counts: BTreeMap<RunId, u64>,
    /// Started attempts by task ID, then by attempt key.
    running: BTreeMap<Vec<u8>, BTreeMap<MethodAttemptKey, RunningAttempt>>,
}

impl DispatchScheduler {
    pub(crate) fn new(context: Arc<EffectContext>) -> Self {
        Self {
            context,
            in_flight: BTreeMap::new(),
            parked: BTreeMap::new(),
            lock_release_counts: BTreeMap::new(),
            running: BTreeMap::new(),
        }
    }

    /// Hands out a pending attempt unless a task already owns it or it waits for locks.
    pub(crate) fn start_pending_invocation(
        &mut self,
        flow: FlowAddress,
        key: MethodAttemptKey,
        scheduled_at: Option<Instant>,
    ) {
        let is_parked = self
            .parked
            .get(&key.run_id)
            .is_some_and(|parked| parked.contains_key(&key));
        if self.in_flight.contains_key(&key) || is_parked {
            return;
        }
        let progress = HandoutProgress {
            search_started_at: None,
            scheduled_at,
        };
        self.spawn_handout(flow, key, progress);
    }

    fn spawn_handout(
        &mut self,
        flow: FlowAddress,
        key: MethodAttemptKey,
        progress: HandoutProgress,
    ) {
        let lock_release_count = self.lock_release_count(key.run_id);
        self.in_flight
            .insert(key.clone(), InFlightEntry { lock_release_count });
        let context = Arc::clone(&self.context);
        self.context.tracker.spawn(async move {
            let handout = AttemptHandout {
                context: Arc::clone(&context),
                flow: flow.clone(),
                key: key.clone(),
                progress,
            };
            let end = handout.run().await;
            context.report(RouterMessage::HandoutFinished { flow, key, end });
        });
    }

    /// Records the end of a hand-out task; one that met busy locks waits for their release.
    pub(crate) fn finish_handout(
        &mut self,
        flow: FlowAddress,
        key: MethodAttemptKey,
        end: HandoutEnd,
    ) {
        let Some(entry) = self.in_flight.remove(&key) else {
            return;
        };
        let HandoutEnd::LocksBusy(progress) = end else {
            return;
        };
        if entry.lock_release_count != self.lock_release_count(key.run_id) {
            // Locks were released while the task checked them; try again now.
            self.spawn_handout(flow, key, progress);
            return;
        }
        self.parked
            .entry(key.run_id)
            .or_default()
            .insert(key, ParkedInvocation { flow, progress });
    }

    /// Retries the run's invocations that waited for locks.
    pub(crate) fn release_locks(&mut self, run_id: RunId) {
        *self.lock_release_counts.entry(run_id).or_insert(0) += 1;
        for (key, parked) in self.parked.remove(&run_id).unwrap_or_default() {
            self.spawn_handout(parked.flow, key, parked.progress);
        }
    }

    /// Retries every parked invocation; in-memory RPC locks release without an effect.
    pub(crate) fn retry_parked_invocations(&mut self) {
        let parked = std::mem::take(&mut self.parked);
        for (key, parked) in parked.into_values().flatten() {
            self.spawn_handout(parked.flow, key, parked.progress);
        }
    }

    /// Registers the cancellation of an attempt whose start is about to be proposed.
    pub(crate) fn register_attempt(
        &mut self,
        task_id: Vec<u8>,
        key: MethodAttemptKey,
        cancel: CancellationToken,
    ) {
        self.running
            .entry(task_id)
            .or_default()
            .insert(key, RunningAttempt { cancel });
    }

    /// Stops the attempts started as `task_id`; their Workers receive a cancel request.
    pub(crate) fn cancel_attempt(&mut self, task_id: &[u8]) {
        for attempt in self
            .running
            .get(task_id)
            .into_iter()
            .flat_map(BTreeMap::values)
        {
            attempt.cancel.cancel();
        }
    }

    pub(crate) fn finish_attempt(&mut self, task_id: Vec<u8>, key: &MethodAttemptKey) {
        if let Some(attempts) = self.running.get_mut(&task_id) {
            attempts.remove(key);
            if attempts.is_empty() {
                self.running.remove(&task_id);
            }
        }
    }

    /// Fails started attempts that a partition rebuild found running, except those this node
    /// started itself after it began serving.
    pub(crate) fn fail_interrupted_attempts(
        &mut self,
        flow: FlowAddress,
        attempts: Vec<InterruptedAttempt>,
    ) {
        for attempt in attempts {
            if self.running.contains_key(&attempt.task_id) {
                continue;
            }
            let context = Arc::clone(&self.context);
            let flow = flow.clone();
            self.context.tracker.spawn(async move {
                let run_id = attempt.key.run_id;
                fail_interrupted_attempt(&context, &flow, run_id, attempt).await;
            });
        }
    }

    pub(crate) fn has_parked_invocations(&self) -> bool {
        !self.parked.is_empty()
    }

    fn lock_release_count(&self, run_id: RunId) -> u64 {
        self.lock_release_counts.get(&run_id).copied().unwrap_or(0)
    }
}
