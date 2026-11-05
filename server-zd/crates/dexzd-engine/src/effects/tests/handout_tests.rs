// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The hand-out of an attempt: one task ID, returns of attempts the Worker never ran, failures
//! after a hand-out, and progress (plan sections 3.1 and 3.9).

use std::sync::atomic::Ordering;

use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{
    InvokeWaitForMethodResponse, PollWorkerTaskRejectReason, StepAttemptProgress, StepDurability,
    StepOptions,
};
use dexzd_protocol::dexzd::v1::{
    StepAttemptFailedCommand, StepAttemptReturnReason, StepAttemptReturnedCommand,
    StepInvocationPhase, StepInvocationRecord, flow_command,
};

use super::{EffectHarness, FakeDispatch, is_attempt_failed, settings, start_request};
use crate::effects::dispatch::render_task_id;

/// How a Worker declines a task it never ran.
type WorkerDecline = fn(&FakeDispatch);

fn started_task_ids(harness: &EffectHarness) -> Vec<Vec<u8>> {
    harness
        .flow_effect_operations
        .commands_matching(|command| {
            matches!(command, flow_command::Command::StepAttemptStarted(_))
        })
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepAttemptStarted(started)) => Some(started.task_id),
            _ => None,
        })
        .collect()
}

fn returns(harness: &EffectHarness) -> Vec<StepAttemptReturnedCommand> {
    harness
        .flow_effect_operations
        .commands_matching(|command| {
            matches!(command, flow_command::Command::StepAttemptReturned(_))
        })
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepAttemptReturned(returned)) => Some(returned),
            _ => None,
        })
        .collect()
}

fn failures(harness: &EffectHarness) -> Vec<StepAttemptFailedCommand> {
    harness
        .flow_effect_operations
        .commands_matching(is_attempt_failed)
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepAttemptFailed(failure)) => Some(failure),
            _ => None,
        })
        .collect()
}

fn invocation(harness: &EffectHarness, run_id: RunId) -> StepInvocationRecord {
    harness
        .step_execution("order-1", run_id, "ChargeStep-1")
        .invocation
        .expect("invocation")
}

async fn start(durability: StepDurability) -> (EffectHarness, RunId, FakeDispatch) {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_durability_override: durability as i32,
        ..StepOptions::default()
    });
    let run_id = harness.start_flow(request, settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    (harness, run_id, dispatch)
}

#[tokio::test(start_paused = true)]
async fn the_task_frame_results_and_fences_share_the_started_task_id() {
    let (harness, _, dispatch) = start(StepDurability::Sync).await;
    let started = started_task_ids(&harness);
    assert_eq!(started.len(), 1);
    assert_eq!(dispatch.request.task_id, render_task_id(&started[0]));
    assert_eq!(dispatch.request.app_worker_id, "worker-a");
    assert_eq!(dispatch.request.step_type, "ChargeStep");
    dispatch.complete_wait_for(InvokeWaitForMethodResponse::default());
    let _execute = harness.dispatcher.next_dispatch().await;
    let completed: Vec<_> = harness
        .flow_effect_operations
        .commands_matching(super::is_method_completed)
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepMethodCompleted(completed)) => Some(completed.task_id),
            _ => None,
        })
        .collect();
    assert_eq!(completed, vec![started[0].clone()]);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn attempts_the_worker_never_ran_return_with_the_same_attempt_number() {
    let cases: [(WorkerDecline, StepAttemptReturnReason); 3] = [
        (
            |dispatch| dispatch.reject(PollWorkerTaskRejectReason::UnknownStep, true),
            StepAttemptReturnReason::Unsupported,
        ),
        (
            |dispatch| dispatch.reject(PollWorkerTaskRejectReason::NoCapacity, false),
            StepAttemptReturnReason::LegacyNoCapacity,
        ),
        (
            FakeDispatch::half_close_before_output,
            StepAttemptReturnReason::NotDelivered,
        ),
    ];
    for (declines, reason) in cases {
        for durability in [StepDurability::Sync, StepDurability::Async] {
            let (harness, run_id, first) = start(durability).await;
            let declined_at = std::time::Instant::now();
            declines(&first);
            let second = harness.dispatcher.next_dispatch().await;
            assert_eq!(second.context().attempt, 1, "{reason:?} uses no retry");
            let returned = returns(&harness);
            assert_eq!(returned.len(), 1);
            assert_eq!(returned[0].reason, reason as i32);
            let started = started_task_ids(&harness);
            assert_eq!(started.len(), 2);
            assert_eq!(returned[0].expected_task_id, started[0]);
            assert_ne!(started[0], started[1], "each start gets its own task ID");
            assert!(failures(&harness).is_empty());
            // The search after a return gets its own dispatch wait.
            let reservations = harness
                .dispatcher
                .reservations
                .lock()
                .expect("lock")
                .clone();
            assert_eq!(reservations.len(), 2);
            assert!(reservations[1].search_started_at >= declined_at);
            assert_eq!(
                invocation(&harness, run_id).furthest_attempt_progress,
                StepAttemptProgress::NotStarted as i32
            );
            second.complete_wait_for(InvokeWaitForMethodResponse::default());
            let _execute = harness.dispatcher.next_dispatch().await;
            harness.shutdown().await;
        }
    }
}

#[tokio::test(start_paused = true)]
async fn a_task_frame_that_was_never_sent_returns_the_attempt() {
    let harness = EffectHarness::new();
    harness
        .dispatcher
        .undelivered_sends
        .store(1, Ordering::SeqCst);
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.context().attempt, 1);
    let returned = returns(&harness);
    assert_eq!(returned.len(), 1);
    assert_eq!(
        returned[0].reason,
        StepAttemptReturnReason::NotDelivered as i32
    );
    assert_eq!(
        invocation(&harness, run_id).phase,
        StepInvocationPhase::Running as i32
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_disconnect_after_the_hand_out_fails_the_attempt_and_retries_it() {
    for durability in [StepDurability::Sync, StepDurability::Async] {
        let (harness, run_id, first) = start(durability).await;
        first.disconnect();
        let retry = harness.dispatcher.next_dispatch().await;
        // The Worker may have run attempt 1, so no other Worker receives it (plan D2, D20).
        assert_eq!(retry.context().attempt, 2, "{durability:?}");
        let failures = failures(&harness);
        assert_eq!(failures.len(), 1);
        assert!(!failures[0].is_not_run);
        assert_eq!(failures[0].expected_task_id, started_task_ids(&harness)[0]);
        assert!(failures[0].server_detail.contains("WORKER_DISCONNECTED"));
        assert_eq!(
            invocation(&harness, run_id).furthest_attempt_progress,
            StepAttemptProgress::NotCompleted as i32
        );
        harness.shutdown().await;
    }
}

#[tokio::test(start_paused = true)]
async fn a_capacity_rejection_on_a_reserved_slot_fails_the_attempt_as_not_run() {
    for (durability, progress) in [
        (StepDurability::Sync, StepAttemptProgress::NotStarted),
        (StepDurability::Async, StepAttemptProgress::NotCompleted),
    ] {
        let (harness, run_id, first) = start(durability).await;
        first.reject(PollWorkerTaskRejectReason::NoCapacity, true);
        let retry = harness.dispatcher.next_dispatch().await;
        assert_eq!(retry.context().attempt, 2);
        let failures = failures(&harness);
        assert_eq!(failures.len(), 1);
        assert!(failures[0].is_not_run);
        assert!(returns(&harness).is_empty());
        assert_eq!(
            invocation(&harness, run_id).furthest_attempt_progress,
            progress as i32,
            "{durability:?}"
        );
        harness.shutdown().await;
    }
}

#[tokio::test(start_paused = true)]
async fn a_start_at_the_speculation_limit_releases_its_worker_slot_and_retries() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    // The first start proposal finds the Flow at its speculation limit.
    harness
        .flow_effect_operations
        .unavailable_proposals
        .store(1, Ordering::SeqCst);
    harness.settle().await;
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.context().attempt, 1);
    assert!(harness.dispatcher.reservation_count.load(Ordering::SeqCst) >= 1);
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 1);
    assert!(failures(&harness).is_empty());
    assert_eq!(
        invocation(&harness, run_id).phase,
        StepInvocationPhase::Running as i32
    );
    harness.shutdown().await;
}
