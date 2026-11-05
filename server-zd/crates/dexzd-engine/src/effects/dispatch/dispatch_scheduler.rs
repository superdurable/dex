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

use crate::effects::dispatch::attempt_entries::HandoutRestart;
use crate::effects::dispatch::attempt_handout::{
    AttemptHandout, HandoutEnd, HandoutProgress, InterruptedAttempt, fail_interrupted_attempt,
};
use crate::effects::method_request::MethodAttemptKey;
use crate::effects::{EffectContext, RouterMessage};
use crate::flow_coordination::FlowAddress;

struct RunningAttempt {
    cancel: CancellationToken,
}

/// Dispatch state owned by the effect router task. Attempt entries live in the shared
/// [`crate::effects::dispatch::AttemptEntries`] so that Claims can read them.
pub(crate) struct DispatchScheduler {
    context: Arc<EffectContext>,
    /// Started attempts by task ID, then by attempt key.
    running: BTreeMap<Vec<u8>, BTreeMap<MethodAttemptKey, RunningAttempt>>,
}

impl DispatchScheduler {
    pub(crate) fn new(context: Arc<EffectContext>) -> Self {
        Self {
            context,
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
        let restart = self
            .context
            .attempt_entries()
            .finish_handout(&key, generation, locks_busy);
        if let HandoutRestart::Again(flow, progress, generation) = restart {
            spawn_handout(&self.context, flow, key, generation, progress);
        }
    }

    /// Retries the run's invocations that waited for locks.
    pub(crate) fn release_locks(&mut self, run_id: RunId) {
        let unparked = self.context.attempt_entries().release_locks(run_id);
        for (key, flow, progress, generation) in unparked {
            spawn_handout(&self.context, flow, key, generation, progress);
        }
    }

    /// Retries every parked invocation; in-memory RPC locks release without an effect.
    pub(crate) fn retry_parked_invocations(&mut self) {
        let unparked = self.context.attempt_entries().release_all_parked();
        for (key, flow, progress, generation) in unparked {
            spawn_handout(&self.context, flow, key, generation, progress);
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
