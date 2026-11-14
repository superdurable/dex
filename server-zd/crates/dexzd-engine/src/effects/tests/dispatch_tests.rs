// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::Ordering;
use std::time::Duration;

use dexzd_clock::WallClock;
use tokio::time::Instant;

use dexzd_apply::ApplyEffect;
use dexzd_apply::read_model::get_history_events;
use dexzd_keyspace::VALUE_REFERENCE_PREFIX;

use dexzd_protocol::dex::{
    AttributeWrite, CloseDecision, CloseDecisionType, FlowConfig, FlowStatus, FlowTimeoutPolicy,
    InvokeExecuteMethodResponse, InvokeWaitForMethodResponse, PullRouting, PullRoutingSource,
    StartFlowRequest, StepDecision, StepOptions, StopFlowRequest, SubFlowCondition, SubFlowOptions,
    Value, WaitingCondition, flow_history_event, value,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowStopCommand, StepAttemptFailedCommand, StepAttemptFailureKind,
    StepInvocationKey, StepInvocationPhase, StepMethod, flow_command,
    step_attempt_heartbeat_command,
};

use super::{
    EffectHarness, SECOND_MICROS, START_MICROS, is_attempt_failed, is_method_completed, settings,
    start_request, string_value,
};
use crate::effects::{EffectsConfig, MethodRequest};
use crate::flow_coordination::{CommitUrgency, FlowCoordinationEffectSink};

pub(super) fn force_complete() -> InvokeExecuteMethodResponse {
    InvokeExecuteMethodResponse {
        step_decision: Some(StepDecision {
            close_decision: Some(CloseDecision {
                close_decision_type: CloseDecisionType::ForceComplete as i32,
                close_input: Some(string_value("done")),
                ..CloseDecision::default()
            }),
            ..StepDecision::default()
        }),
        ..InvokeExecuteMethodResponse::default()
    }
}

fn failures(harness: &EffectHarness) -> Vec<(i32, bool, String)> {
    harness
        .flow_effect_operations
        .commands_matching(is_attempt_failed)
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepAttemptFailed(failure)) => Some((
                failure.kind,
                failure.is_non_retryable,
                failure.server_detail,
            )),
            _ => None,
        })
        .collect()
}

#[tokio::test(start_paused = true)]
async fn sync_wait_for_and_execute_dispatch_with_go_server_context_and_complete_the_flow() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());

    let wait_for = harness.dispatcher.next_dispatch().await;
    let MethodRequest::WaitFor(request) = wait_for.method_request().clone() else {
        panic!("expected a WaitFor request");
    };
    let context = request.context.clone().expect("context");
    assert_eq!(context.flow_id, "order-1");
    assert_eq!(context.run_id, run_id.to_string());
    assert_eq!(context.step_execution_id, "ChargeStep-1");
    assert_eq!(context.attempt, 1);
    assert_eq!(context.run_attempt, 1);
    assert_eq!(context.flow_started_timestamp, START_MICROS / SECOND_MICROS);
    assert_eq!(
        context.first_attempt_timestamp,
        START_MICROS / SECOND_MICROS
    );
    assert_eq!(context.from_step_execution_id, "__start__");
    assert_eq!(request.flow_type, "OrderFlow");
    assert_eq!(request.step_type, "ChargeStep");
    assert_eq!(request.step_input, Some(string_value("small input")));
    assert_eq!(wait_for.request.flow_id, "order-1");
    assert_eq!(wait_for.request.flow_type, "OrderFlow");
    assert_eq!(wait_for.request.sticky_app_worker_id, None);
    wait_for.complete_wait_for(InvokeWaitForMethodResponse::default());

    let execute = harness.dispatcher.next_dispatch().await;
    assert!(matches!(
        execute.method_request(),
        MethodRequest::Execute(_)
    ));
    assert_eq!(
        execute.request.sticky_app_worker_id.as_deref(),
        Some("worker-a")
    );
    execute.complete_execute(force_complete());
    harness.settle().await;

    let record = harness.run_record("order-1", run_id);
    assert_eq!(record.status, FlowStatus::Completed as i32);
    let completed = harness
        .flow_effect_operations
        .commands_matching(is_method_completed);
    assert_eq!(completed.len(), 2);
    let run = harness.run_location("order-1", run_id);
    let history = get_history_events(
        &harness.flow_effect_operations.snapshot(),
        run.flow_hash,
        run_id,
        0,
        0,
        &[],
        0,
        usize::MAX,
    )
    .expect("history reads");
    let start_step = history
        .events
        .iter()
        .find_map(|event| match &event.payload {
            Some(flow_history_event::Payload::StepWaitForCompleted(completed)) => {
                completed.context.as_ref()
            }
            _ => None,
        });
    assert_eq!(
        start_step
            .expect("start Step history event")
            .from_step_execution_id,
        "__start__"
    );
    assert!(completed.iter().all(|recorded| {
        recorded.urgency == CommitUrgency::Prompt
            && recorded.command.expected_run_attempt == 1
            && recorded.command.run_id == run_id.as_bytes().to_vec()
    }));
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn flow_timeout_handler_dispatch_and_history_use_start_source() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let mut request = start_request("timeout-handler-source");
    request.start_step_type.clear();
    request.flow_timeout_seconds = 1;
    let run_id =
        harness.start_flow_with_timeout_policy(request, settings(), FlowTimeoutPolicy::Handler);

    tokio::time::sleep(Duration::from_secs(1)).await;
    let dispatch = harness.dispatcher.next_dispatch().await;
    let MethodRequest::Execute(execute_request) = dispatch.method_request().clone() else {
        panic!("expected timeout handler Execute request");
    };
    let context = execute_request.context.expect("timeout handler context");
    assert_eq!(context.step_execution_id, "sys:timeout_handler-1");
    assert_eq!(context.from_step_execution_id, "__start__");
    dispatch.complete_execute(force_complete());
    harness.settle().await;

    let run = harness.run_location("timeout-handler-source", run_id);
    let history = get_history_events(
        &harness.flow_effect_operations.snapshot(),
        run.flow_hash,
        run_id,
        0,
        0,
        &[],
        0,
        usize::MAX,
    )
    .expect("history reads");
    let timeout_handler = history
        .events
        .iter()
        .find_map(|event| match &event.payload {
            Some(flow_history_event::Payload::StepExecuteCompleted(completed)) => {
                completed.context.as_ref()
            }
            _ => None,
        });
    assert_eq!(
        timeout_handler
            .expect("timeout handler history event")
            .from_step_execution_id,
        "__start__"
    );
    harness.shutdown().await;
}

fn routing(sticky: &str, served_by: &str, source: PullRoutingSource) -> PullRouting {
    PullRouting {
        sticky_app_worker_id: sticky.to_owned(),
        served_by_app_worker_id: served_by.to_owned(),
        source: source as i32,
        task_queue: String::new(),
    }
}

#[tokio::test(start_paused = true)]
async fn starts_record_the_dispatch_routing_and_results_the_serving_worker() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    let wait_for = harness.dispatcher.next_dispatch().await;
    wait_for.complete_wait_for(InvokeWaitForMethodResponse::default());
    let execute = harness.dispatcher.next_dispatch().await;
    execute.complete_execute(force_complete());
    harness.settle().await;

    let start_routings: Vec<_> = harness
        .flow_effect_operations
        .commands_matching(|command| {
            matches!(command, flow_command::Command::StepAttemptStarted(_))
        })
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepAttemptStarted(started)) => started.pull_routing,
            _ => None,
        })
        .collect();
    assert_eq!(
        start_routings,
        vec![
            routing("", "worker-a", PullRoutingSource::FlowTypeQueue),
            routing("worker-a", "worker-a", PullRoutingSource::StickyQueue),
        ]
    );
    let result_routings: Vec<_> = harness
        .flow_effect_operations
        .commands_matching(is_method_completed)
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepMethodCompleted(completed)) => completed.pull_routing,
            _ => None,
        })
        .collect();
    assert_eq!(
        result_routings,
        vec![
            routing("", "worker-a", PullRoutingSource::FlowTypeQueue),
            routing("worker-a", "worker-a", PullRoutingSource::StickyQueue),
        ]
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_start_waits_until_the_run_routing_is_readable() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness
        .flow_effect_operations
        .is_flow_state_unreadable
        .store(true, Ordering::SeqCst);
    harness.start_flow(start_request("order-1"), settings());
    tokio::time::sleep(Duration::from_secs(60)).await;
    harness.settle().await;
    assert!(harness.dispatcher.try_next_dispatch().await.is_none());

    harness
        .flow_effect_operations
        .is_flow_state_unreadable
        .store(false, Ordering::SeqCst);
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.context().attempt, 1);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn worker_error_fails_the_attempt_and_the_backoff_timer_dispatches_attempt_two() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());

    let first = harness.dispatcher.next_dispatch().await;
    first.fail_with_worker_error("card declined");
    harness.settle().await;
    let failed = harness
        .flow_effect_operations
        .commands_matching(is_attempt_failed);
    let Some(flow_command::Command::StepAttemptFailed(failure)) = &failed[0].command.command else {
        panic!("expected a failure");
    };
    assert_eq!(failure.kind, StepAttemptFailureKind::WorkerError as i32);
    assert_eq!(
        failure.worker_error.as_ref().expect("worker error").detail,
        "card declined"
    );
    assert_eq!(
        failure
            .pull_routing
            .as_ref()
            .expect("routing")
            .served_by_app_worker_id,
        "worker-a"
    );

    let second = harness.dispatcher.next_dispatch().await;
    let context = second.context();
    assert_eq!(context.attempt, 2);
    assert_eq!(
        context.first_attempt_timestamp,
        START_MICROS / SECOND_MICROS
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn method_timeout_cancels_the_dispatch_and_fails_the_attempt() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_timeout_seconds: 5,
        heartbeat_timeout_seconds: 600,
        ..StepOptions::default()
    });
    harness.start_flow(request, settings());
    let dispatch = harness.dispatcher.next_dispatch().await;

    tokio::time::sleep(Duration::from_millis(4900)).await;
    assert!(failures(&harness).is_empty());
    tokio::time::sleep(Duration::from_millis(200)).await;
    harness.settle().await;
    let failures = failures(&harness);
    assert_eq!(failures[0].0, StepAttemptFailureKind::MethodTimeout as i32);
    assert!(dispatch.is_cancelled.load(Ordering::SeqCst));
    harness.shutdown().await;
}

fn start_with_heartbeat_timeout(harness: &EffectHarness, heartbeat_timeout_seconds: i32) {
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        heartbeat_timeout_seconds,
        ..StepOptions::default()
    });
    harness.start_flow(request, settings());
}

#[tokio::test(start_paused = true)]
async fn heartbeat_timeout_starts_when_the_worker_receives_the_task() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    start_with_heartbeat_timeout(&harness, 10);
    let dispatch = harness.dispatcher.next_dispatch().await;

    // The Worker received the task at once and sends nothing, like a SYNC Step that sleeps 12s.
    tokio::time::sleep(Duration::from_millis(9_900)).await;
    harness.settle().await;
    assert!(failures(&harness).is_empty());
    tokio::time::sleep(Duration::from_millis(200)).await;
    harness.settle().await;
    let failures = failures(&harness);
    assert_eq!(failures.len(), 1);
    assert_eq!(
        failures[0].0,
        StepAttemptFailureKind::HeartbeatTimeout as i32
    );
    assert!(dispatch.is_cancelled.load(Ordering::SeqCst));
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn each_worker_frame_extends_the_heartbeat_timeout() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    start_with_heartbeat_timeout(&harness, 20);
    let dispatch = harness.dispatcher.next_dispatch().await;

    tokio::time::sleep(Duration::from_secs(19)).await;
    dispatch.send_wait_for_heartbeat(None);
    tokio::time::sleep(Duration::from_secs(19)).await;
    harness.settle().await;
    assert!(
        failures(&harness).is_empty(),
        "the heartbeat extended the timeout"
    );
    tokio::time::sleep(Duration::from_secs(2)).await;
    harness.settle().await;
    assert_eq!(
        failures(&harness)[0].0,
        StepAttemptFailureKind::HeartbeatTimeout as i32
    );
    assert!(dispatch.is_cancelled.load(Ordering::SeqCst));
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn timeouts_start_when_the_task_frame_goes_out_after_the_start_commits() {
    let harness = EffectHarness::new();
    *harness
        .flow_effect_operations
        .start_commit_delay
        .lock()
        .expect("lock") = Duration::from_secs(15);
    harness.rebuild_all_partitions().await;
    let started_at = Instant::now();
    start_with_heartbeat_timeout(&harness, 10);
    // A SYNC attempt reaches its Worker only once its start commits (plan D51, D7).
    let dispatch = harness.dispatcher.next_dispatch().await;
    let sent_at = Instant::now();
    assert!(sent_at - started_at >= Duration::from_secs(15));
    let deadline_micros = dispatch
        .request
        .deadline
        .duration_since(std::time::UNIX_EPOCH)
        .expect("deadline after epoch")
        .as_micros();
    let sent_micros = u128::try_from(harness.clock.now_unix_micros()).expect("positive");
    assert_eq!(deadline_micros - sent_micros, 2 * 60 * 60 * 1_000_000);
    tokio::time::sleep(Duration::from_millis(9_900)).await;
    harness.settle().await;
    assert!(failures(&harness).is_empty(), "the frame went out 9.9s ago");
    tokio::time::sleep(Duration::from_millis(200)).await;
    harness.settle().await;
    assert_eq!(
        failures(&harness)[0].0,
        StepAttemptFailureKind::HeartbeatTimeout as i32
    );
    assert!(dispatch.is_cancelled.load(Ordering::SeqCst));
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn heartbeats_propose_only_checkpoint_changes_coalesced_within_the_batch_window() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    let heartbeats = || {
        harness
            .flow_effect_operations
            .commands_matching(|command| {
                matches!(command, flow_command::Command::StepAttemptHeartbeat(_))
            })
            .into_iter()
            .filter_map(|recorded| match recorded.command.command {
                Some(flow_command::Command::StepAttemptHeartbeat(heartbeat)) => {
                    heartbeat.checkpoint
                }
                _ => None,
            })
            .collect::<Vec<_>>()
    };

    // Without a checkpoint, an explicit heartbeat without a value changes nothing.
    dispatch.send_wait_for_heartbeat(None);
    dispatch.send_wait_for_heartbeat(Some(string_value("a")));
    dispatch.send_wait_for_heartbeat(Some(string_value("b")));
    tokio::time::sleep(Duration::from_millis(30)).await;
    harness.settle().await;
    assert_eq!(
        heartbeats(),
        vec![step_attempt_heartbeat_command::Checkpoint::Value(
            string_value("b")
        )]
    );
    dispatch.send_wait_for_heartbeat(Some(string_value("b")));
    tokio::time::sleep(Duration::from_millis(30)).await;
    assert_eq!(
        heartbeats().len(),
        1,
        "an unchanged checkpoint is not proposed"
    );
    dispatch.send_wait_for_heartbeat(None);
    tokio::time::sleep(Duration::from_millis(30)).await;
    harness.settle().await;
    assert_eq!(
        heartbeats().last(),
        Some(&step_attempt_heartbeat_command::Checkpoint::IsCleared(true))
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn the_next_attempt_receives_the_latest_heartbeat_checkpoint() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    let first = harness.dispatcher.next_dispatch().await;
    first.send_wait_for_heartbeat(Some(string_value("page-7")));
    tokio::time::sleep(Duration::from_millis(30)).await;
    first.fail_with_worker_error("transient");
    let second = harness.dispatcher.next_dispatch().await;
    assert_eq!(
        second.context().last_heartbeat_value,
        Some(string_value("page-7"))
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn hydration_sends_complete_step_inputs_and_heartbeat_checkpoints() {
    check_large_value_dispatch(true).await;
}

#[tokio::test(start_paused = true)]
async fn without_hydration_large_step_inputs_and_checkpoints_are_value_references() {
    check_large_value_dispatch(false).await;
}

/// Checks a step input and checkpoint above the 100-byte inline threshold across a retry.
async fn check_large_value_dispatch(is_hydrating: bool) {
    let config = EffectsConfig {
        hydrate_before_dispatch: is_hydrating,
        ..EffectsConfig::default()
    };
    let harness = EffectHarness::with_config(super::DurableState::new(), config);
    harness.rebuild_all_partitions().await;
    let step_input = string_value(&"large input ".repeat(20));
    harness.start_flow(
        StartFlowRequest {
            step_input: Some(step_input.clone()),
            ..start_request("order-1")
        },
        settings(),
    );
    let is_received_as = |received: Option<&Value>, original: &Value| {
        if is_hydrating {
            return received == Some(original);
        }
        matches!(
            received.and_then(|value| value.kind.as_ref()),
            Some(value::Kind::InternalBlobIdForStringValue(reference))
                if reference.starts_with(VALUE_REFERENCE_PREFIX)
        )
    };
    let first = harness.dispatcher.next_dispatch().await;
    let MethodRequest::WaitFor(request) = first.method_request().clone() else {
        panic!("expected a WaitFor request");
    };
    assert!(is_received_as(request.step_input.as_ref(), &step_input));
    let checkpoint = string_value(&"page ".repeat(40));
    first.send_wait_for_heartbeat(Some(checkpoint.clone()));
    tokio::time::sleep(Duration::from_millis(30)).await;
    first.fail_with_worker_error("transient");

    let second = harness.dispatcher.next_dispatch().await;
    assert!(is_received_as(
        second.context().last_heartbeat_value.as_ref(),
        &checkpoint
    ));
    // The Worker echoes what it received; an unchanged checkpoint is not proposed again.
    second.send_wait_for_heartbeat(second.context().last_heartbeat_value);
    tokio::time::sleep(Duration::from_millis(30)).await;
    harness.settle().await;
    let heartbeat_count = harness
        .flow_effect_operations
        .commands_matching(|command| {
            matches!(command, flow_command::Command::StepAttemptHeartbeat(_))
        })
        .len();
    assert_eq!(heartbeat_count, 1);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn invalid_worker_responses_fail_the_attempt_and_retry() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    dispatch.complete_wait_for(InvokeWaitForMethodResponse {
        upsert_attributes: vec![AttributeWrite {
            key: "report".to_owned(),
            value: Some(dexzd_protocol::dex::Value {
                kind: Some(
                    dexzd_protocol::dex::value::Kind::InternalBlobIdForStringValue(
                        "blob-1".to_owned(),
                    ),
                ),
            }),
            ..AttributeWrite::default()
        }],
        ..InvokeWaitForMethodResponse::default()
    });
    harness.settle().await;
    let failures = failures(&harness);
    assert_eq!(
        failures[0].0,
        StepAttemptFailureKind::InvalidResponse as i32
    );
    assert!(!failures[0].1);
    assert_eq!(
        failures[0].2,
        "upsert_attributes[0].value: worker response must not contain internal_blob_id arms"
    );
    assert!(
        harness
            .flow_effect_operations
            .commands_matching(is_method_completed)
            .is_empty()
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_out_of_range_sub_flow_window_fails_the_attempt_as_a_retryable_invalid_response() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    dispatch.complete_wait_for(InvokeWaitForMethodResponse {
        waiting_condition: Some(WaitingCondition {
            waiting_condition_type: 1,
            sub_flow_conditions: vec![SubFlowCondition {
                condition_id: "child".to_owned(),
                sub_flow_type: "ChildFlow".to_owned(),
                start_step_type: "ChildStep".to_owned(),
                options: Some(SubFlowOptions {
                    flow_config_override: Some(FlowConfig {
                        history_window_events: Some(100_001),
                        ..FlowConfig::default()
                    }),
                    ..SubFlowOptions::default()
                }),
                ..SubFlowCondition::default()
            }],
            ..WaitingCondition::default()
        }),
        ..InvokeWaitForMethodResponse::default()
    });
    harness.settle().await;
    let failures = failures(&harness);
    assert_eq!(
        failures[0].0,
        StepAttemptFailureKind::InvalidResponse as i32
    );
    assert!(!failures[0].1, "the Step's retry policy decides the retry");
    assert!(
        failures[0]
            .2
            .contains("history_window_events must be between 1 and 100000"),
        "{}",
        failures[0].2
    );
    assert!(
        harness
            .flow_effect_operations
            .commands_matching(is_method_completed)
            .is_empty()
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn values_over_the_run_limit_fail_the_attempt_without_retry() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let mut run_settings = settings();
    run_settings.max_value_bytes = 1000;
    let run_id = harness.start_flow(start_request("order-1"), run_settings);
    let dispatch = harness.dispatcher.next_dispatch().await;
    dispatch.complete_wait_for(InvokeWaitForMethodResponse {
        upsert_attributes: vec![AttributeWrite {
            key: "report".to_owned(),
            value: Some(string_value(&"x".repeat(1001))),
            ..AttributeWrite::default()
        }],
        ..InvokeWaitForMethodResponse::default()
    });
    harness.settle().await;
    let failures = failures(&harness);
    assert_eq!(failures[0].0, StepAttemptFailureKind::ValueTooLarge as i32);
    assert!(failures[0].1, "C19 failures are not retried");
    assert_eq!(
        harness.run_record("order-1", run_id).status,
        FlowStatus::Failed as i32
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn requests_over_the_message_limit_fail_before_dispatch() {
    let config = EffectsConfig {
        max_worker_request_bytes: 10,
        ..EffectsConfig::default()
    };
    let harness = EffectHarness::with_config(super::DurableState::new(), config);
    harness.rebuild_all_partitions().await;
    harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    assert_eq!(
        failures(&harness)[0].0,
        StepAttemptFailureKind::RequestTooLarge as i32
    );
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);
    harness.shutdown().await;
}

/// Returns the recorded attempt failures with their guards.
fn failure_commands(harness: &EffectHarness) -> Vec<StepAttemptFailedCommand> {
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

#[tokio::test(start_paused = true)]
async fn an_attempt_no_worker_takes_waits_in_the_queue_and_starts_when_claimed() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    let run_id = harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    tokio::time::sleep(Duration::from_secs(60)).await;
    harness.settle().await;
    assert!(
        failure_commands(&harness).is_empty(),
        "queueing is not a failure"
    );
    let queued = harness.flow_effect_operations.commands_matching(|command| {
        matches!(command, flow_command::Command::StepAttemptQueued(_))
    });
    assert_eq!(queued.len(), 1, "one row for the attempt");
    let refs = harness.dispatcher.queued_refs.lock().expect("lock").clone();
    assert_eq!(refs.len(), 1, "matching got the committed row's ref");
    let task = refs[0].clone();
    assert_eq!(task.queue.step_type, "ChargeStep");
    assert_eq!(task.position.step_execution_id, "ChargeStep-1");
    let invocation = harness
        .step_execution("order-1", run_id, "ChargeStep-1")
        .invocation
        .expect("invocation");
    assert_eq!(invocation.phase, StepInvocationPhase::Pending as i32);
    // A stale ref of another attempt number is answered as stale.
    let mut stale = task.clone();
    stale.position.attempt = 2;
    harness.dispatcher.claim(7, stale);
    harness.settle().await;
    harness.dispatcher.claim(8, task);
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.request.step_type, "ChargeStep");
    harness.settle().await;
    let replies = harness
        .dispatcher
        .claim_replies
        .lock()
        .expect("lock")
        .clone();
    assert_eq!(
        replies
            .iter()
            .map(|(claim_id, reply)| (*claim_id, reply.result))
            .collect::<Vec<_>>(),
        vec![
            (7, dexzd_worker_dispatch::ClaimResult::Stale),
            (8, dexzd_worker_dispatch::ClaimResult::Started),
        ]
    );
    assert!(replies.iter().all(|(_, reply)| reply.next.is_none()));
    dispatch.complete_wait_for(dexzd_protocol::dex::InvokeWaitForMethodResponse::default());
    harness.settle().await;
    assert!(failure_commands(&harness).is_empty());
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_closing_matching_owner_writes_nothing_for_a_pending_attempt() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.dispatcher.is_closing.store(true, Ordering::SeqCst);
    let run_id = harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    assert_eq!(
        harness.dispatcher.reservation_count.load(Ordering::SeqCst),
        1
    );
    assert!(failure_commands(&harness).is_empty());
    let invocation = harness
        .step_execution("order-1", run_id, "ChargeStep-1")
        .invocation
        .expect("invocation");
    assert_eq!(invocation.phase, StepInvocationPhase::Pending as i32);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn stopping_the_flow_cancels_the_running_attempt_without_a_failure() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    let stop = FlowCommand {
        flow_id: "order-1".to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: "order-1".to_owned(),
                stop_type: dexzd_protocol::dex::StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        })),
        ..FlowCommand::default()
    };
    harness
        .flow_effect_operations
        .commit(&EffectHarness::flow("order-1"), stop, CommitUrgency::Prompt)
        .expect("stop commits");
    harness.settle().await;
    assert!(dispatch.is_cancelled.load(Ordering::SeqCst));
    // A result after cancellation is dropped by the attempt.
    dispatch.complete_wait_for(InvokeWaitForMethodResponse::default());
    harness.settle().await;
    assert!(failures(&harness).is_empty());
    assert!(
        harness
            .flow_effect_operations
            .commands_matching(is_method_completed)
            .is_empty()
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn invocations_wait_for_busy_locks_and_start_once_free() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness
        .flow_effect_operations
        .busy_lock_keys
        .lock()
        .expect("lock")
        .insert("balance".to_owned());
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_lock_attribute_keys: vec!["balance".to_owned()],
        ..StepOptions::default()
    });
    harness.start_flow(request, settings());
    tokio::time::sleep(Duration::from_secs(3)).await;
    assert!(harness.dispatcher.try_next_dispatch().await.is_none());

    harness
        .flow_effect_operations
        .busy_lock_keys
        .lock()
        .expect("lock")
        .clear();
    tokio::time::sleep(Duration::from_millis(1100)).await;
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.context().attempt, 1);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn locks_released_effect_retries_a_parked_invocation_at_once() {
    let config = EffectsConfig {
        locked_invocation_retry_interval: Duration::from_secs(3600),
        ..EffectsConfig::default()
    };
    let harness = EffectHarness::with_config(super::DurableState::new(), config);
    harness.rebuild_all_partitions().await;
    harness
        .flow_effect_operations
        .busy_lock_keys
        .lock()
        .expect("lock")
        .insert("balance".to_owned());
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_lock_attribute_keys: vec!["balance".to_owned()],
        ..StepOptions::default()
    });
    let run_id = harness.start_flow(request, settings());
    harness.settle().await;
    harness
        .flow_effect_operations
        .busy_lock_keys
        .lock()
        .expect("lock")
        .clear();
    harness.runner.handle(
        &EffectHarness::flow("order-1"),
        &[ApplyEffect::LocksReleased {
            run_id,
            lock_keys: vec!["balance".to_owned()],
        }],
        true,
    );
    let dispatch = tokio::time::timeout(Duration::from_secs(1), harness.dispatcher.next_dispatch())
        .await
        .expect("dispatched without waiting for the periodic retry");
    assert_eq!(dispatch.context().step_execution_id, "ChargeStep-1");
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn unavailable_flow_proposals_are_retried_until_committed() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    harness
        .flow_effect_operations
        .unavailable_proposals
        .store(3, Ordering::SeqCst);
    dispatch.complete_wait_for(InvokeWaitForMethodResponse::default());
    let execute = harness.dispatcher.next_dispatch().await;
    assert!(matches!(
        execute.method_request(),
        MethodRequest::Execute(_)
    ));
    assert_eq!(
        harness
            .flow_effect_operations
            .commands_matching(is_method_completed)
            .len(),
        1
    );
    assert_eq!(
        harness
            .step_execution("order-1", run_id, "ChargeStep-1")
            .invocation
            .expect("invocation")
            .method,
        StepMethod::Execute as i32
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn speculative_and_committed_copies_of_a_start_dispatch_once() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    let dispatch = harness.dispatcher.next_dispatch().await;
    let invocation = harness
        .step_execution("order-1", run_id, "ChargeStep-1")
        .invocation
        .expect("invocation");
    let started = ApplyEffect::InvocationStarted {
        run_id,
        run_attempt: 1,
        invocation: StepInvocationKey {
            step_execution_id: "ChargeStep-1".to_owned(),
            method: StepMethod::WaitFor as i32,
        },
        attempt: 1,
        task_id: invocation.task_id,
        method_timeout_seconds: 0,
    };
    let flow = EffectHarness::flow("order-1");
    harness
        .runner
        .handle(&flow, std::slice::from_ref(&started), false);
    harness
        .runner
        .handle(&flow, std::slice::from_ref(&started), true);
    harness.settle().await;
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 1);
    dispatch.complete_wait_for(InvokeWaitForMethodResponse::default());
    harness.dispatcher.next_dispatch().await;
    // A late duplicate after the attempt finished does not redispatch it.
    harness.runner.handle(&flow, &[started], true);
    harness.settle().await;
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 2);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn recovery_policy_in_step_decision_starts_next_step() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    });
    let run_id = harness.start_flow(request, settings());
    let execute = harness.dispatcher.next_dispatch().await;
    execute.complete_execute(InvokeExecuteMethodResponse {
        step_decision: Some(StepDecision {
            next_steps: vec![dexzd_protocol::dex::StepMovement {
                step_type: "ShipStep".to_owned(),
                step_options: Some(StepOptions {
                    execute_failure_policy:
                        dexzd_protocol::dex::ExecuteMethodFailurePolicy::ProceedToConfiguredStep
                            as i32,
                    execute_failure_proceed_step_type: "RecoverStep".to_owned(),
                    ..StepOptions::default()
                }),
                ..dexzd_protocol::dex::StepMovement::default()
            }],
            ..StepDecision::default()
        }),
        ..InvokeExecuteMethodResponse::default()
    });
    harness.settle().await;
    assert!(failures(&harness).is_empty());
    assert_eq!(
        harness.run_record("order-1", run_id).status,
        FlowStatus::Running as i32
    );
    let next = harness.dispatcher.next_dispatch().await;
    let MethodRequest::WaitFor(next_request) = next.method_request() else {
        panic!("expected the next Step's WaitFor request");
    };
    assert_eq!(next_request.step_type, "ShipStep");
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn async_methods_propose_with_batched_urgency() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_durability_override: dexzd_protocol::dex::StepDurability::Async as i32,
        ..StepOptions::default()
    });
    harness.start_flow(request, settings());
    let wait_for = harness.dispatcher.next_dispatch().await;
    wait_for.complete_wait_for(InvokeWaitForMethodResponse::default());
    harness.dispatcher.next_dispatch().await;
    let completed = harness
        .flow_effect_operations
        .commands_matching(is_method_completed);
    assert_eq!(completed[0].urgency, CommitUrgency::Batched);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_unreadable_attempt_state_times_out_the_method_and_retains_its_locks_for_retry() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    // Like the production runtime, a start reads state, so the outage begins once it commits.
    harness
        .flow_effect_operations
        .is_flow_state_unreadable_after_start
        .store(true, Ordering::SeqCst);
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        wait_for_lock_attribute_keys: vec!["inventory".to_owned()],
        wait_for_timeout_seconds: 30,
        ..StepOptions::default()
    });
    let run_id = harness.start_flow(request, settings());
    harness.settle().await;
    let published_effects = || {
        harness
            .flow_effect_operations
            .published_effects
            .lock()
            .expect("lock")
            .clone()
    };
    assert!(
        published_effects().iter().any(|effect| matches!(
            effect,
            ApplyEffect::InvocationStarted {
                method_timeout_seconds: 30,
                ..
            }
        )),
        "apply reports the configured method timeout when the attempt starts"
    );

    tokio::time::sleep(Duration::from_millis(29_900)).await;
    harness.settle().await;
    assert!(failures(&harness).is_empty());
    tokio::time::sleep(Duration::from_millis(200)).await;
    harness.settle().await;
    assert_eq!(
        failures(&harness),
        vec![(
            StepAttemptFailureKind::MethodTimeout as i32,
            false,
            "method timeout of 30s expired while building the Worker request".to_owned()
        )]
    );
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);
    assert!(
        !published_effects().iter().any(|effect| matches!(
            effect,
            ApplyEffect::LocksReleased { run_id: released, lock_keys }
                if *released == run_id && lock_keys == &["inventory".to_owned()]
        )),
        "the timed-out method retains its locks for retry"
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_rebuild_reports_a_queued_row_as_a_head_and_does_not_queue_it_again() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    let run_id = harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    let queued_command_count = || {
        harness
            .flow_effect_operations
            .commands_matching(|command| {
                matches!(command, flow_command::Command::StepAttemptQueued(_))
            })
            .len()
    };
    assert_eq!(queued_command_count(), 1);
    let task = harness.dispatcher.queued_refs.lock().expect("lock")[0].clone();
    let reservations_before = harness.dispatcher.reservations.lock().expect("lock").len();
    // A restart rebuilds matching's refs from the index alone (plan section 3.6).
    harness.rebuild_all_partitions().await;
    harness.settle().await;
    let heads = harness
        .dispatcher
        .rebuilt_heads
        .lock()
        .expect("lock")
        .clone();
    assert_eq!(heads, vec![task.clone()]);
    assert_eq!(
        queued_command_count(),
        1,
        "the rebuild queues nothing again"
    );
    assert_eq!(
        harness.dispatcher.reservations.lock().expect("lock").len(),
        reservations_before,
        "a queued attempt reaches matching through Heads only"
    );
    harness.dispatcher.claim(1, task);
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.request.step_type, "ChargeStep");
    harness.settle().await;
    let invocation = harness
        .step_execution("order-1", run_id, "ChargeStep-1")
        .invocation
        .expect("invocation");
    assert_eq!(invocation.phase, StepInvocationPhase::Running as i32);
    assert_eq!(invocation.attempt, 1);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn periodic_heads_restore_a_ref_matching_lost_once() {
    let config = EffectsConfig {
        heads_resync_interval: Duration::from_secs(1),
        ..EffectsConfig::default()
    };
    let harness = EffectHarness::with_config(super::DurableState::new(), config);
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    // The fake never hands its Queued to the matching that merges scans: a lost ref.
    assert_eq!(
        harness.dispatcher.queued_refs.lock().expect("lock").len(),
        1
    );
    tokio::time::sleep(Duration::from_millis(2_500)).await;
    harness.settle().await;
    assert_eq!(
        harness
            .dispatcher
            .restored_head_count
            .load(Ordering::SeqCst),
        1,
        "the first round restores the lost ref and later rounds find it"
    );
    harness.shutdown().await;
}
