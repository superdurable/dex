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
use std::time::Duration;

use dexzd_apply::{ApplyContext, apply_flow_command};
use dexzd_keyspace::{RunId, VALUE_REFERENCE_PREFIX};
use dexzd_protocol::dex::{
    InvokeWaitForMethodResponse, PollWorkerTaskRejectReason, StepAttemptProgress, StepDurability,
    StepOptions, StopFlowRequest, StopType, value,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowStopCommand, StepAttemptFailedCommand, StepAttemptReturnReason,
    StepAttemptReturnedCommand, StepInvocationPhase, StepInvocationRecord, flow_command,
};

use super::{EffectHarness, FakeDispatch, is_attempt_failed, settings, start_request};
use crate::effects::MethodRequest;
use crate::effects::dispatch::render_task_id;
use crate::flow_coordination::CommitUrgency;

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
            // Each search after a return reserves again from scratch (plan D74).
            let reservations = harness
                .dispatcher
                .reservations
                .lock()
                .expect("lock")
                .clone();
            assert_eq!(reservations.len(), 2);
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

#[tokio::test(start_paused = true)]
async fn a_stop_while_the_start_commits_sends_no_task_frame() {
    let harness = EffectHarness::new();
    *harness
        .flow_effect_operations
        .start_commit_delay
        .lock()
        .expect("lock") = Duration::from_secs(5);
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    assert_eq!(started_task_ids(&harness).len(), 1, "the start is proposed");
    // The Flow stops after the start applied but before it committed (TA16).
    harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow("order-1"),
            stop_command(run_id),
            CommitUrgency::Prompt,
        )
        .expect("stop commits");
    tokio::time::sleep(Duration::from_secs(6)).await;
    harness.settle().await;
    assert_eq!(
        harness.dispatcher.dispatch_count.load(Ordering::SeqCst),
        0,
        "no task frame goes out"
    );
    assert_eq!(
        harness.dispatcher.reservation_count.load(Ordering::SeqCst),
        1
    );
    assert!(failures(&harness).is_empty());
    harness.shutdown().await;
}

/// The start commits and the request is built; then the Flow stops, but the cancellation has
/// not reached the attempt yet. The SYNC recheck of the state keeps the frame unsent (TA16).
#[tokio::test(start_paused = true)]
async fn a_stop_before_the_cancellation_arrives_sends_no_task_frame() {
    let harness = EffectHarness::new();
    *harness
        .flow_effect_operations
        .start_commit_delay
        .lock()
        .expect("lock") = Duration::from_secs(5);
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    assert_eq!(started_task_ids(&harness).len(), 1, "the start is proposed");
    // The stop applies without publishing its effects, so no cancellation reaches the attempt.
    let context = ApplyContext {
        partition: harness.partition_of("order-1"),
        flow_hash: harness.run_location("order-1", run_id).flow_hash,
    };
    harness
        .flow_effect_operations
        .add_override(Box::new(move |command, state| {
            matches!(command.command, Some(flow_command::Command::FlowStop(_))).then(|| {
                let outcome =
                    apply_flow_command(&context, &*state, command).expect("the stop applies");
                state.commit(&outcome.writes);
                outcome.reply
            })
        }));
    harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow("order-1"),
            stop_command(run_id),
            CommitUrgency::Prompt,
        )
        .expect("stop commits");
    tokio::time::sleep(Duration::from_secs(6)).await;
    harness.settle().await;
    assert_eq!(
        harness.dispatcher.dispatch_count.load(Ordering::SeqCst),
        0,
        "no task frame goes out"
    );
    assert!(failures(&harness).is_empty());
    harness.shutdown().await;
}

fn stop_command(run_id: RunId) -> FlowCommand {
    FlowCommand {
        flow_id: "order-1".to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: "order-1".to_owned(),
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        })),
        ..FlowCommand::default()
    }
}

#[tokio::test(start_paused = true)]
async fn a_lock_taken_after_the_pre_check_returns_the_slot_and_the_step_runs_later() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    // A transactional RPC takes the lock between the pre-check and the start (TA18).
    harness
        .flow_effect_operations
        .starts_finding_busy_locks
        .store(1, Ordering::SeqCst);
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_lock_attribute_keys: vec!["balance".to_owned()],
        ..StepOptions::default()
    });
    harness.start_flow(request, settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.context().attempt, 1, "the lock wait used no retry");
    assert_eq!(
        harness.dispatcher.reservation_count.load(Ordering::SeqCst),
        2,
        "the first slot went back when the start found the lock busy"
    );
    assert_eq!(
        started_task_ids(&harness).len(),
        1,
        "only one start applied"
    );
    assert!(failures(&harness).is_empty());
    // The search after the lock wait reserves again, with fresh waits.
    assert_eq!(
        harness.dispatcher.reservations.lock().expect("lock").len(),
        2
    );
    dispatch.complete_wait_for(InvokeWaitForMethodResponse::default());
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_sync_request_carries_references_and_an_async_one_checks_for_uncommitted_values() {
    for durability in [StepDurability::Sync, StepDurability::Async] {
        let harness = EffectHarness::new();
        harness.rebuild_all_partitions().await;
        let mut request = start_request("order-1");
        request.step_input = Some(super::string_value(&"large input ".repeat(20)));
        request.step_options = Some(StepOptions {
            wait_for_durability_override: durability as i32,
            ..StepOptions::default()
        });
        let committed_reads_before = harness
            .flow_effect_operations
            .committed_flow_reads
            .load(Ordering::SeqCst);
        harness.start_flow(request, settings());
        let dispatch = harness.dispatcher.next_dispatch().await;
        let committed_reads = harness
            .flow_effect_operations
            .committed_flow_reads
            .load(Ordering::SeqCst)
            - committed_reads_before;
        let MethodRequest::WaitFor(wait_for) = dispatch.method_request().clone() else {
            panic!("expected a WaitFor request");
        };
        // A SYNC start commits with every earlier write, so references always load (TA21).
        if durability == StepDurability::Sync {
            assert_eq!(committed_reads, 0, "SYNC never asks which values committed");
            assert!(matches!(
                wait_for.step_input.and_then(|input| input.kind),
                Some(value::Kind::InternalBlobIdForStringValue(reference))
                    if reference.starts_with(VALUE_REFERENCE_PREFIX)
            ));
        } else {
            assert!(
                committed_reads > 0,
                "ASYNC inlines values that are not committed"
            );
        }
        dispatch.complete_wait_for(InvokeWaitForMethodResponse::default());
        harness.shutdown().await;
    }
}
