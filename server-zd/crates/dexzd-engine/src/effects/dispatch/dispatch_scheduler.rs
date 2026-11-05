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

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::time::Instant;

use dexzd_keyspace::RunId;
use tokio::sync::watch;
use tokio_util::sync::CancellationToken;

use crate::effects::dispatch::attempt_entries::HandoutRestart;
use crate::effects::dispatch::attempt_handout::{
    AttemptHandout, HandoutEnd, HandoutProgress, InterruptedAttempt, fail_interrupted_attempt,
};
use crate::effects::method_request::MethodAttemptKey;
use crate::effects::{AttemptDrainProgress, EffectContext, RouterMessage};
use crate::flow_coordination::FlowAddress;

struct RunningAttempt {
    cancel: CancellationToken,
}

/// Dispatch state owned by the effect router task; attempt entries are shared.
pub(crate) struct DispatchScheduler {
    context: Arc<EffectContext>,
    drain: Option<watch::Sender<AttemptDrainProgress>>,
    /// Hand-out tasks this router spawned, by attempt and entry generation, until they end.
    in_flight: BTreeSet<(MethodAttemptKey, u64)>,
    /// Claim tasks this router spawned until they end.
    claim_tasks: usize,
    /// Started attempts by task ID, then by attempt key.
    running: BTreeMap<Vec<u8>, BTreeMap<MethodAttemptKey, RunningAttempt>>,
}

impl DispatchScheduler {
    pub(crate) fn new(context: Arc<EffectContext>) -> Self {
        Self {
            context,
            drain: None,
            in_flight: BTreeSet::new(),
            claim_tasks: 0,
            running: BTreeMap::new(),
        }
    }

    /// Hands out a pending attempt unless a task already holds its entry.
    pub(crate) fn start_pending_invocation(
        &mut self,
        flow: FlowAddress,
        key: MethodAttemptKey,
        scheduled_at: Option<Instant>,
    ) {
        let Some(generation) = self.context.attempt_entries().register(&key) else {
            return;
        };
        let progress = HandoutProgress { scheduled_at };
        self.start_handout(flow, key, generation, progress);
    }

    /// Spawns the hand-out task of `generation`; while attempts drain, releases its entry instead.
    fn start_handout(
        &mut self,
        flow: FlowAddress,
        key: MethodAttemptKey,
        generation: u64,
        progress: HandoutProgress,
    ) {
        if self.drain.is_some() {
            // The attempt stays pending for the successor, as after a refused reservation.
            let _released = self
                .context
                .attempt_entries()
                .finish_handout(&key, generation, None);
            return;
        }
        self.in_flight.insert((key.clone(), generation));
        spawn_handout(&self.context, flow, key, generation, progress);
    }

    /// Records the end of a hand-out task; one that met busy locks waits for their release.
    pub(crate) fn finish_handout(
        &mut self,
        flow: FlowAddress,
        key: MethodAttemptKey,
        generation: u64,
        end: HandoutEnd,
    ) {
        let locks_busy = match end {
            HandoutEnd::Released => None,
            HandoutEnd::LocksBusy(progress) => Some((flow, progress)),
        };
        self.in_flight.remove(&(key.clone(), generation));
        let restart = self
            .context
            .attempt_entries()
            .finish_handout(&key, generation, locks_busy);
        if let HandoutRestart::Again(flow, progress, generation) = restart {
            self.start_handout(flow, key, generation, progress);
        }
        self.publish_attempt_drain();
    }

    /// Counts the task of a delivered Claim before the router spawns it.
    ///
    /// Returns false while attempts drain: like a new hand-out, the Claim does not run, and its
    /// row stays queued for the successor.
    pub(crate) fn admit_claim(&mut self) -> bool {
        if self.drain.is_some() {
            return false;
        }
        self.claim_tasks += 1;
        true
    }

    /// Records the end of a Claim's task.
    pub(crate) fn finish_claim(&mut self) {
        self.claim_tasks = self
            .claim_tasks
            .checked_sub(1)
            .expect("each admitted Claim's task reports its end once");
        self.publish_attempt_drain();
    }

    /// Retries the run's invocations that waited for locks.
    pub(crate) fn release_locks(&mut self, run_id: RunId) {
        let unparked = self.context.attempt_entries().release_locks(run_id);
        for (key, flow, progress, generation) in unparked {
            self.start_handout(flow, key, generation, progress);
        }
    }

    /// Retries every parked invocation; in-memory RPC locks release without an effect.
    pub(crate) fn retry_parked_invocations(&mut self) {
        let unparked = self.context.attempt_entries().release_all_parked();
        for (key, flow, progress, generation) in unparked {
            self.start_handout(flow, key, generation, progress);
        }
    }

    /// Registers the cancellation of an attempt whose start is about to be proposed.
    pub(crate) fn register_attempt(
        &mut self,
        task_id: Vec<u8>,
        key: MethodAttemptKey,
        cancel: CancellationToken,
    ) {
        if let Some(drain) = &self.drain {
            drain.send_modify(|progress| progress.attempts_waited += 1);
        }
        self.running
            .entry(task_id)
            .or_default()
            .insert(key, RunningAttempt { cancel });
        self.publish_attempt_drain();
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

    pub(crate) fn interrupt_attempts(&self) {
        for attempt in self.running.values().flat_map(BTreeMap::values) {
            attempt.cancel.cancel();
        }
    }

    pub(crate) fn finish_attempt(&mut self, task_id: Vec<u8>, key: &MethodAttemptKey) {
        if let Some(attempts) = self.running.get_mut(&task_id) {
            if attempts.remove(key).is_some()
                && let Some(drain) = &self.drain
            {
                drain.send_modify(|progress| progress.attempts_finished += 1);
            }
            if attempts.is_empty() {
                self.running.remove(&task_id);
            }
        }
        self.publish_attempt_drain();
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

    pub(crate) fn begin_attempt_drain(&mut self, progress: watch::Sender<AttemptDrainProgress>) {
        let attempts_waited = self.running.values().map(BTreeMap::len).sum();
        progress.send_replace(AttemptDrainProgress {
            attempts_waited,
            attempts_running: attempts_waited,
            outstanding_handouts: self.outstanding_handouts(),
            has_started: true,
            ..AttemptDrainProgress::default()
        });
        self.drain = Some(progress);
    }

    fn publish_attempt_drain(&self) {
        if let Some(drain) = &self.drain {
            drain.send_modify(|progress| {
                progress.attempts_running = self.running.values().map(BTreeMap::len).sum();
                progress.outstanding_handouts = self.outstanding_handouts();
            });
        }
    }

    fn outstanding_handouts(&self) -> usize {
        self.in_flight.len() + self.claim_tasks
    }

    pub(crate) fn has_parked_invocations(&self) -> bool {
        self.context.attempt_entries().has_parked()
    }
}

/// Spawns the hand-out task of `generation`; it reports its end to the router.
pub(crate) fn spawn_handout(
    context: &Arc<EffectContext>,
    flow: FlowAddress,
    key: MethodAttemptKey,
    generation: u64,
    progress: HandoutProgress,
) {
    let task_context = Arc::clone(context);
    context.tracker.spawn(async move {
        let handout = AttemptHandout {
            context: Arc::clone(&task_context),
            flow: flow.clone(),
            key: key.clone(),
            generation,
            progress,
        };
        let end = handout.run().await;
        task_context.report(RouterMessage::HandoutFinished {
            flow,
            key,
            generation,
            end,
        });
    });
}
