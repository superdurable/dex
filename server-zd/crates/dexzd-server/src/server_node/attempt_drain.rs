// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_engine::{AttemptDrainProgress, EffectTasksHandle, EngineNode, FlowCoordinatorRegistry};
use dexzd_worker_dispatch::WorkerDispatch;
use tokio::sync::watch;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use crate::config::LifecycleConfig;

/// How a planned Step attempt drain ended.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum AttemptDrainOutcome {
    /// Every hand-out ended and its results committed.
    Completed,
    /// The drain timeout expired first; remaining attempts were interrupted.
    TimedOut,
    /// The engine stopped, the Flow runtime halted, or the effect router ended first.
    NodeStopped,
}

impl AttemptDrainOutcome {
    fn as_str(self) -> &'static str {
        match self {
            Self::Completed => "completed",
            Self::TimedOut => "timed_out",
            Self::NodeStopped => "node_stopped",
        }
    }
}

pub(super) async fn drain_attempts(
    config: &LifecycleConfig,
    dispatch: &WorkerDispatch,
    effect_tasks: &EffectTasksHandle,
    engine: &EngineNode,
    registry: &FlowCoordinatorRegistry,
) {
    if config.attempt_drain_timeout.is_zero() {
        tracing::info!("Step attempt drain disabled");
        return;
    }
    let deadline = Instant::now() + config.attempt_drain_timeout;
    dispatch.begin_attempt_drain();
    registry.close_rpc_admission();
    let mut progress = match effect_tasks.begin_attempt_drain() {
        Ok(progress) => progress,
        Err(error) => {
            tracing::warn!(%error, "Step attempt drain could not start");
            return;
        }
    };
    let halted = registry.halted();
    let mut outcome =
        wait_for_drain_progress(&mut progress, deadline, engine, &halted, |snapshot| {
            snapshot.has_started
        })
        .await;
    if outcome == AttemptDrainOutcome::Completed {
        let snapshot = *progress.borrow();
        tracing::info!(
            attempts_waited = snapshot.attempts_waited,
            outstanding_handouts = snapshot.outstanding_handouts,
            "Step attempt drain started"
        );
        outcome = wait_for_drain_progress(&mut progress, deadline, engine, &halted, |snapshot| {
            snapshot.outstanding_handouts == 0
        })
        .await;
    }
    // The router handles this after the drain request, even when it had not reached it yet.
    if outcome == AttemptDrainOutcome::TimedOut
        && let Err(error) = effect_tasks.interrupt_attempts()
    {
        tracing::warn!(%error, "interrupting remaining Step attempts failed");
    }
    let snapshot = *progress.borrow();
    tracing::info!(
        attempts_waited = snapshot.attempts_waited,
        attempts_finished = snapshot.attempts_finished,
        attempts_running = snapshot.attempts_running,
        outstanding_handouts = snapshot.outstanding_handouts,
        outcome = %outcome.as_str(),
        timeout_milliseconds = config.attempt_drain_timeout.as_millis(),
        "Step attempt drain ended"
    );
}

/// Waits until `is_reached` holds for the drain progress, giving the deadline priority.
///
/// Returns `Completed` once it holds, `TimedOut` when `deadline` passed first, and
/// `NodeStopped` when the engine stopped, the Flow runtime halted, or the router ended first.
async fn wait_for_drain_progress(
    progress: &mut watch::Receiver<AttemptDrainProgress>,
    deadline: Instant,
    engine: &EngineNode,
    halted: &CancellationToken,
    is_reached: impl FnMut(&AttemptDrainProgress) -> bool,
) -> AttemptDrainOutcome {
    tokio::select! {
        biased;
        () = tokio::time::sleep_until(deadline) => AttemptDrainOutcome::TimedOut,
        _ = engine.wait_for_stop() => AttemptDrainOutcome::NodeStopped,
        () = halted.cancelled() => AttemptDrainOutcome::NodeStopped,
        reached = progress.wait_for(is_reached) => match reached {
            Ok(_) => AttemptDrainOutcome::Completed,
            Err(_) => AttemptDrainOutcome::NodeStopped,
        },
    }
}
