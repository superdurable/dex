// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::Ordering;
use std::time::Duration;

use dexzd_apply::ApplyEffect;
use dexzd_keyspace::row_keys::{run_named_key, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, RunTable};
use dexzd_protocol::dex::{
    DispatchTaskErrorReason, InvokeWaitForMethodResponse, StepAttemptProgress, StepOptions,
};
use dexzd_protocol::dexzd::v1::{
    StepAttemptFailedCommand, StepAttemptFailureKind, StepInvocationKey, StepMethod, flow_command,
};
use dexzd_worker_dispatch::DispatchOutcome;

use crate::flow_coordination::FlowCoordinationEffectSink;

use super::{
    EffectHarness, SECOND_MICROS, START_MICROS, is_attempt_failed, settings, start_request,
};

fn attempt_failures(harness: &EffectHarness) -> Vec<StepAttemptFailedCommand> {
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

fn start_with_method_timeout(harness: &EffectHarness) {
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_timeout_seconds: 5,
        heartbeat_timeout_seconds: 600,
        ..StepOptions::default()
    });
    harness.start_flow(request, settings());
}

/// Matching sends the task before it forwards `Assigned`; a deadline in that gap still sees it.
#[tokio::test(start_paused = true)]
async fn a_task_delivered_before_its_assigned_event_fails_as_assigned() {
    let harness = EffectHarness::new();
    harness
        .dispatcher
        .is_assignment_manual
        .store(true, Ordering::SeqCst);
    harness.rebuild_all_partitions().await;
    start_with_method_timeout(&harness);
    let dispatch = harness.dispatcher.next_dispatch().await;

    tokio::time::sleep(Duration::from_millis(4_990)).await;
    dispatch.deliver_without_assigned_event();
    tokio::time::sleep(Duration::from_millis(100)).await;
    harness.settle().await;

    let failures = attempt_failures(&harness);
    assert_eq!(failures.len(), 1);
    assert_eq!(
        failures[0].kind,
        StepAttemptFailureKind::MethodTimeout as i32
    );
    assert_eq!(
        failures[0].attempt_progress,
        StepAttemptProgress::Assigned as i32
    );
    assert!(dispatch.is_cancelled.load(Ordering::SeqCst));
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_task_no_worker_received_fails_as_not_assigned() {
    let harness = EffectHarness::new();
    harness
        .dispatcher
        .is_assignment_manual
        .store(true, Ordering::SeqCst);
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;

    dispatch.complete(DispatchOutcome::DispatchError {
        reason: DispatchTaskErrorReason::NoWorkerAvailable,
        detail: "no Worker accepted the task".to_owned(),
    });
    harness.settle().await;

    let failures = attempt_failures(&harness);
    assert_eq!(failures.len(), 1);
    assert_eq!(
        failures[0].attempt_progress,
        StepAttemptProgress::NotAssigned as i32
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_assigned_task_that_a_worker_drops_fails_as_assigned() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;

    dispatch.complete(DispatchOutcome::DispatchError {
        reason: DispatchTaskErrorReason::NoWorkerAvailable,
        detail: "no Worker accepted the task".to_owned(),
    });
    harness.settle().await;

    assert_eq!(
        attempt_failures(&harness)[0].attempt_progress,
        StepAttemptProgress::Assigned as i32
    );
    harness.shutdown().await;
}

/// Time travel restores a running attempt; a restored mark fails it instead of dispatching it.
#[tokio::test(start_paused = true)]
async fn a_restored_marked_attempt_fails_without_a_dispatch() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    let original = harness.dispatcher.next_dispatch().await;
    let run = harness.run_location("order-1", run_id);
    let mut running_step = harness.step_execution("order-1", run_id, "ChargeStep-1");
    let invocation = running_step.invocation.as_mut().expect("invocation");
    invocation.marked_started_attempt = 1;
    let task_id = invocation.task_id.clone();
    original.complete_wait_for(InvokeWaitForMethodResponse::default());
    let _execute = harness.dispatcher.next_dispatch().await;

    {
        let mut record = harness.run_record("order-1", run_id);
        record.current_attempt = 2;
        let mut state = harness.flow_effect_operations.state.lock().expect("lock");
        state.put_message(
            ColumnFamily::Flow,
            run_named_key(
                run.flow_hash,
                run.run_id,
                RunTable::StepExecution,
                "ChargeStep-1",
            ),
            &running_step,
        );
        state.put_message(
            ColumnFamily::Flow,
            run_singleton_key(run.flow_hash, run.run_id, RunTable::RunRecord),
            &record,
        );
    }
    harness.runner.handle(
        &EffectHarness::flow("order-1"),
        &[ApplyEffect::InvocationStarted {
            run_id,
            run_attempt: 2,
            invocation: StepInvocationKey {
                step_execution_id: "ChargeStep-1".to_owned(),
                method: StepMethod::WaitFor as i32,
            },
            attempt: 1,
            task_id,
            method_timeout_seconds: 0,
            retry_deadline_micros: START_MICROS + 4 * 60 * 60 * SECOND_MICROS,
            is_redispatch: true,
        }],
        true,
    );
    harness.settle().await;

    let failures = attempt_failures(&harness);
    let restored = failures.last().expect("the restored attempt failed");
    assert_eq!(
        restored.kind,
        StepAttemptFailureKind::StartedAttemptInterrupted as i32
    );
    assert_eq!(
        restored.attempt_progress,
        StepAttemptProgress::Started as i32
    );
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 2);
    harness.shutdown().await;
}
