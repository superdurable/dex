// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_engine::{EffectTasksHandle, EngineNode, FlowCoordinatorRegistry};
use dexzd_worker_dispatch::WorkerDispatch;

use crate::config::LifecycleConfig;

pub(super) async fn drain_attempts(
    config: &LifecycleConfig,
    dispatch: &WorkerDispatch,
    effect_tasks: &EffectTasksHandle,
    engine: &EngineNode,
    registry: &FlowCoordinatorRegistry,
) {
    dispatch.begin_attempt_drain();
    registry.close_rpc_admission();
    let halted = registry.halted();
    let started = tokio::select! {
        result = effect_tasks.begin_attempt_drain() => result,
        _ = engine.wait_for_stop() => return,
        () = halted.cancelled() => return,
    };
    let mut progress = match started {
        Ok(progress) => progress,
        Err(error) => {
            tracing::warn!(%error, "Step attempt drain could not start");
            return;
        }
    };
    tracing::info!(
        attempts_waited = progress.borrow().attempts_waited,
        "Step attempt drain started"
    );
    let deadline = tokio::time::Instant::now() + config.attempt_drain_timeout;
    let has_timed_out = loop {
        let snapshot = *progress.borrow_and_update();
        if snapshot.has_started && snapshot.outstanding_handouts == 0 {
            break false;
        }
        tokio::select! {
            biased;
            () = tokio::time::sleep_until(deadline) => break true,
            _ = engine.wait_for_stop() => break false,
            () = halted.cancelled() => break false,
            changed = progress.changed() => {
                if changed.is_err() { break false; }
            }
        }
    };
    let snapshot = *progress.borrow();
    if has_timed_out && let Err(error) = effect_tasks.interrupt_attempts() {
        tracing::warn!(%error, "interrupting remaining Step attempts failed");
    }
    tracing::info!(
        attempts_waited = snapshot.attempts_waited,
        attempts_finished = snapshot.attempts_finished,
        attempts_running = snapshot.attempts_running,
        outstanding_handouts = snapshot.outstanding_handouts,
        has_timed_out,
        timeout_milliseconds = config.attempt_drain_timeout.as_millis(),
        "Step attempt drain ended"
    );
}
