// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dex::{
    CloseDecision, CloseDecisionType, ExecuteMethodFailurePolicy, FlowConfig, FlowResetStepMethod,
    FlowResetType, FlowStartOptions, FlowStatus, FlowTimeoutHandlerOptions, FlowTimeoutPolicy,
    GetHistoryEventsRequest, InvokeExecuteMethodOutput, InvokeExecuteMethodResponse,
    LoadBlobRequestEntry, LoadBlobsRequest, PollWorkerTask, ResetFlowRequest, RetryPolicy,
    StartFlowRequest, StepDecision, StepMovement, StepOptions, Value, WaitForFlowRequest,
    WaitForMethodFailurePolicy, WorkerErrorResponse, WorkerTarget, flow_history_event,
    invoke_execute_method_output, poll_worker_task, poll_worker_task_frame, value,
};
use prost::Message;
use std::time::Duration;
use tokio::time::Instant;

use crate::harness::{DevServer, ScriptedWorker, force_complete};

const FLOW_TYPE: &str = "FailurePolicyFlow";

/// The dev server's default `values.wireInlineThresholdBytes`.
const DEFAULT_WIRE_INLINE_THRESHOLD_BYTES: usize = 100;

fn text_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn execute_output(response: InvokeExecuteMethodResponse) -> poll_worker_task_frame::Output {
    poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
        output: Some(invoke_execute_method_output::Output::Result(response)),
    })
}

fn worker_error(error_type: &str) -> poll_worker_task_frame::Output {
    poll_worker_task_frame::Output::WorkerError(WorkerErrorResponse {
        detail: "step failed".to_owned(),
        error_type: error_type.to_owned(),
        ..WorkerErrorResponse::default()
    })
}

async fn start_flow(server: &DevServer, flow_id: &str, input: Value, options: StepOptions) {
    start_flow_at_step(server, flow_id, "FailStep", input, options).await;
}

async fn start_flow_at_step(
    server: &DevServer,
    flow_id: &str,
    start_step_type: &str,
    input: Value,
    options: StepOptions,
) {
    server
        .flow_client()
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: start_step_type.to_owned(),
            step_input: Some(input),
            step_options: Some(options),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget {
                        is_pull_mode: true,
                        ..WorkerTarget::default()
                    }),
                    ..FlowConfig::default()
                }),
                ..FlowStartOptions::default()
            }),
            request_id: format!("{flow_id}-start"),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow accepts the policy");
}

/// Loads a Step input that the Worker received as a stored value reference.
async fn load_referenced_input(server: &DevServer, flow_id: &str, value: &Value) -> Value {
    let Some(value::Kind::InternalBlobIdForStringValue(reference)) = value.kind.as_ref() else {
        panic!("the recovery Step input arrives as a stored value reference, got {value:?}");
    };
    server
        .flow_client()
        .load_blobs(LoadBlobsRequest {
            entries: vec![LoadBlobRequestEntry {
                flow_id: flow_id.to_owned(),
                blob_value: Some(value.clone()),
            }],
        })
        .await
        .expect("recovery input remains readable")
        .into_inner()
        .values
        .get(reference)
        .cloned()
        .expect("the value holder remains live")
}

fn execute_step_type(task: &PollWorkerTask) -> &str {
    let Some(poll_worker_task::Request::Execute(request)) = task.request.as_ref() else {
        panic!("the task runs Execute, got {task:?}");
    };
    &request.step_type
}

fn complete_flow_output() -> poll_worker_task_frame::Output {
    execute_output(InvokeExecuteMethodResponse {
        step_decision: Some(force_complete(text_value("done"))),
        ..InvokeExecuteMethodResponse::default()
    })
}

async fn assert_flow_completed(server: &DevServer, flow_id: &str) {
    let result = server
        .flow_client()
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 10,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Completed as i32);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn execute_failure_starts_recovery_with_durable_input_and_error() {
    let server = DevServer::start().await;
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "execute-failure-recovery";
    let input = text_value(&"saved-input".repeat(512));
    assert!(
        input.encoded_len() > DEFAULT_WIRE_INLINE_THRESHOLD_BYTES,
        "the input must be stored by reference"
    );
    start_flow(
        &server,
        flow_id,
        input.clone(),
        StepOptions {
            skip_wait_for: true,
            execute_retry_policy: Some(RetryPolicy {
                maximum_attempts: 1,
                ..RetryPolicy::default()
            }),
            execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
            execute_failure_proceed_step_type: "RecoverStep".to_owned(),
            execute_failure_proceed_step_options: Some(Box::new(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            })),
            ..StepOptions::default()
        },
    )
    .await;
    let failed = worker.next_task().await;
    worker.reply(failed.task_id, worker_error("BusinessFailure"));
    let recovery = worker.next_task().await;
    let Some(poll_worker_task::Request::Execute(request)) = recovery.request.as_ref() else {
        panic!("recovery Step runs Execute");
    };
    assert_eq!(request.step_type, "RecoverStep");
    let context = request.context.as_ref().expect("recovery context");
    assert_eq!(context.from_step_execution_id, "FailStep-1");
    assert_eq!(
        context
            .recovery_error
            .as_ref()
            .expect("recovery error")
            .error_type,
        "BusinessFailure"
    );
    assert_eq!(
        load_referenced_input(
            &server,
            flow_id,
            request.step_input.as_ref().expect("input")
        )
        .await,
        input
    );
    worker.reply(
        recovery.task_id,
        execute_output(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(text_value("recovered"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_flow_completed(&server, flow_id).await;
    server
        .flow_client()
        .reset_flow(ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::StepExecutionId as i32,
            step_execution_id: "RecoverStep-1".to_owned(),
            step_method: FlowResetStepMethod::Execute as i32,
            reason: "verify recovery replay".to_owned(),
            ..ResetFlowRequest::default()
        })
        .await
        .expect("time travel crosses the failure edge");
    let replayed = worker.next_task().await;
    let Some(poll_worker_task::Request::Execute(replayed_request)) = replayed.request.as_ref()
    else {
        panic!("recovery Step Execute is restored");
    };
    assert_eq!(replayed_request.step_type, "RecoverStep");
    assert_eq!(
        replayed_request
            .context
            .as_ref()
            .and_then(|context| context.recovery_error.as_ref())
            .expect("recovery error survives time travel")
            .error_type,
        "BusinessFailure"
    );
    assert_eq!(
        load_referenced_input(
            &server,
            flow_id,
            replayed_request.step_input.as_ref().expect("input")
        )
        .await,
        input
    );
    worker.reply(
        replayed.task_id,
        execute_output(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(text_value("recovered again"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_flow_completed(&server, flow_id).await;
    worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn wait_for_failure_proceeds_to_execute_with_failure_result() {
    let server = DevServer::start().await;
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "wait-failure-proceed";
    start_flow(
        &server,
        flow_id,
        text_value("input"),
        StepOptions {
            wait_for_retry_policy: Some(RetryPolicy {
                maximum_attempts: 1,
                ..RetryPolicy::default()
            }),
            wait_for_failure_policy: WaitForMethodFailurePolicy::ProceedOnFailure as i32,
            ..StepOptions::default()
        },
    )
    .await;
    let wait_for = worker.next_task().await;
    assert!(matches!(
        wait_for.request,
        Some(poll_worker_task::Request::WaitFor(_))
    ));
    worker.reply(wait_for.task_id, worker_error("WaitFailure"));
    let execute = worker.next_task().await;
    let Some(poll_worker_task::Request::Execute(request)) = execute.request.as_ref() else {
        panic!("the same Step proceeds to Execute");
    };
    assert_eq!(request.step_type, "FailStep");
    assert!(
        request
            .condition_results
            .as_ref()
            .expect("condition results")
            .wait_for_failed
    );
    assert_eq!(
        request
            .context
            .as_ref()
            .and_then(|context| context.recovery_error.as_ref())
            .expect("recovery error")
            .error_type,
        "WaitFailure"
    );
    worker.reply(
        execute.task_id,
        execute_output(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(text_value("continued"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_flow_completed(&server, flow_id).await;
    worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn timeout_handler_failure_starts_recovery_with_null_input() {
    let server = DevServer::start().await;
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "timeout-handler-recovery";
    server
        .flow_client()
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: String::new(),
            flow_timeout_seconds: 1,
            flow_timeout_policy: FlowTimeoutPolicy::Handler as i32,
            flow_start_options: Some(FlowStartOptions {
                timeout_handler_options: Some(FlowTimeoutHandlerOptions {
                    retry_policy: Some(RetryPolicy {
                        maximum_attempts: 1,
                        ..RetryPolicy::default()
                    }),
                    failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
                    failure_proceed_step_type: "RecoverTimeoutStep".to_owned(),
                    failure_proceed_step_options: Some(StepOptions {
                        skip_wait_for: true,
                        ..StepOptions::default()
                    }),
                    ..FlowTimeoutHandlerOptions::default()
                }),
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget {
                        is_pull_mode: true,
                        ..WorkerTarget::default()
                    }),
                    ..FlowConfig::default()
                }),
                ..FlowStartOptions::default()
            }),
            request_id: format!("{flow_id}-start"),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow accepts timeout recovery");
    let timeout = worker.next_task().await;
    let Some(poll_worker_task::Request::Execute(request)) = timeout.request.as_ref() else {
        panic!("timeout handler runs Execute");
    };
    assert_eq!(request.step_type, "sys:timeout_handler");
    worker.reply(timeout.task_id, worker_error("TimeoutHandlerFailure"));
    let recovery = worker.next_task().await;
    let Some(poll_worker_task::Request::Execute(request)) = recovery.request.as_ref() else {
        panic!("timeout recovery runs Execute");
    };
    assert_eq!(request.step_type, "RecoverTimeoutStep");
    assert_eq!(
        request
            .context
            .as_ref()
            .expect("recovery context")
            .from_step_execution_id,
        "sys:timeout_handler-1"
    );
    assert_eq!(
        request
            .step_input
            .as_ref()
            .and_then(|input| input.kind.as_ref()),
        Some(&value::Kind::NullValue(0))
    );
    assert_eq!(
        request
            .context
            .as_ref()
            .and_then(|context| context.recovery_error.as_ref())
            .expect("recovery error")
            .error_type,
        "TimeoutHandlerFailure"
    );
    worker.reply(
        recovery.task_id,
        execute_output(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(text_value("recovered"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_flow_completed(&server, flow_id).await;
    worker.stop().await;
    server.shutdown().await;
}

async fn assert_timeout_recovery(
    flow_id: &str,
    method_timeout_seconds: i32,
    heartbeat_timeout_seconds: i32,
    total_duration_seconds: i32,
    expected_error_type: &str,
    expected_seconds: u64,
) {
    let server = DevServer::start().await;
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    start_flow(
        &server,
        flow_id,
        text_value("input"),
        StepOptions {
            skip_wait_for: true,
            execute_timeout_seconds: method_timeout_seconds,
            heartbeat_timeout_seconds,
            execute_retry_policy: Some(RetryPolicy {
                maximum_attempts: 1,
                total_duration_seconds,
                ..RetryPolicy::default()
            }),
            execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
            execute_failure_proceed_step_type: "RecoverStep".to_owned(),
            execute_failure_proceed_step_options: Some(Box::new(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            })),
            ..StepOptions::default()
        },
    )
    .await;
    let failed = worker.next_task().await;
    assert!(matches!(
        failed.request,
        Some(poll_worker_task::Request::Execute(_))
    ));
    let started = Instant::now();
    let mut interval = tokio::time::interval(Duration::from_millis(100));
    tokio::time::timeout(Duration::from_secs(30), async {
        loop {
            let history = server
                .flow_client()
                .get_history_events(GetHistoryEventsRequest {
                    flow_id: flow_id.to_owned(),
                    estimate_page_size: 100,
                    ..GetHistoryEventsRequest::default()
                })
                .await
                .expect("history reads")
                .into_inner();
            if history.events.iter().any(|event| {
                matches!(
                    event.payload,
                    Some(flow_history_event::Payload::StepExecuteFailed(_))
                )
            }) {
                break;
            }
            interval.tick().await;
        }
    })
    .await
    .expect("the final attempt fails");
    let elapsed = started.elapsed();
    worker.stop().await;
    let mut recovery_worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let recovery = recovery_worker.next_task().await;
    let Some(poll_worker_task::Request::Execute(request)) = recovery.request.as_ref() else {
        panic!("timeout recovery runs Execute");
    };
    assert_eq!(request.step_type, "RecoverStep");
    assert_eq!(
        request
            .context
            .as_ref()
            .and_then(|context| context.recovery_error.as_ref())
            .expect("recovery error")
            .error_type,
        expected_error_type
    );
    assert!(
        elapsed >= Duration::from_secs(expected_seconds).saturating_sub(Duration::from_millis(900))
            && elapsed < Duration::from_secs(expected_seconds + 10),
        "{expected_error_type} recovered after {elapsed:?}"
    );
    recovery_worker.reply(
        recovery.task_id,
        execute_output(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(text_value("recovered"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_flow_completed(&server, flow_id).await;
    recovery_worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn method_heartbeat_and_retry_deadlines_keep_go_recovery_error_types() {
    assert_timeout_recovery("method-timeout-recovery", 1, 10, 30, "StartToClose", 1).await;
    assert_timeout_recovery("heartbeat-timeout-recovery", 20, 10, 30, "Heartbeat", 10).await;
    assert_timeout_recovery("retry-deadline-recovery", 20, 10, 1, "ScheduleToClose", 1).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn lock_wait_before_the_first_attempt_does_not_consume_the_retry_total_duration() {
    let server = DevServer::start().await;
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "lock-wait-retry-duration";
    start_flow_at_step(
        &server,
        flow_id,
        "ParentStep",
        text_value("input"),
        StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        },
    )
    .await;
    let locked_options = |total_duration_seconds| StepOptions {
        skip_wait_for: true,
        execute_lock_attribute_keys: vec!["k".to_owned()],
        execute_retry_policy: Some(RetryPolicy {
            total_duration_seconds,
            ..RetryPolicy::default()
        }),
        ..StepOptions::default()
    };
    let parent = worker.next_task().await;
    assert_eq!(execute_step_type(&parent), "ParentStep");
    worker.reply(
        parent.task_id,
        execute_output(InvokeExecuteMethodResponse {
            step_decision: Some(StepDecision {
                next_steps: vec![
                    StepMovement {
                        step_type: "HoldStep".to_owned(),
                        step_options: Some(locked_options(0)),
                        ..StepMovement::default()
                    },
                    StepMovement {
                        step_type: "QueuedStep".to_owned(),
                        step_options: Some(locked_options(2)),
                        ..StepMovement::default()
                    },
                ],
                ..StepDecision::default()
            }),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    let hold = worker.next_task().await;
    assert_eq!(execute_step_type(&hold), "HoldStep");
    // The Worker holds the lock longer than QueuedStep's total duration.
    tokio::time::sleep(Duration::from_secs(3)).await;
    worker.reply(
        hold.task_id,
        execute_output(InvokeExecuteMethodResponse {
            step_decision: Some(StepDecision {
                close_decision: Some(CloseDecision {
                    close_decision_type: CloseDecisionType::DeadEnd as i32,
                    ..CloseDecision::default()
                }),
                ..StepDecision::default()
            }),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    let queued = worker.next_task().await;
    assert_eq!(execute_step_type(&queued), "QueuedStep");
    worker.reply(queued.task_id, complete_flow_output());
    assert_flow_completed(&server, flow_id).await;
    worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn reset_after_the_retry_total_duration_expired_dispatches_the_restored_step() {
    let server = DevServer::start().await;
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "reset-after-retry-duration";
    start_flow_at_step(
        &server,
        flow_id,
        "WorkStep",
        text_value("input"),
        StepOptions {
            skip_wait_for: true,
            execute_retry_policy: Some(RetryPolicy {
                total_duration_seconds: 2,
                ..RetryPolicy::default()
            }),
            ..StepOptions::default()
        },
    )
    .await;
    let work = worker.next_task().await;
    assert_eq!(execute_step_type(&work), "WorkStep");
    worker.reply(work.task_id, complete_flow_output());
    assert_flow_completed(&server, flow_id).await;
    // Reset after WorkStep's original total duration expired.
    tokio::time::sleep(Duration::from_secs(3)).await;
    server
        .flow_client()
        .reset_flow(ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::StepExecutionId as i32,
            step_execution_id: "WorkStep-1".to_owned(),
            step_method: FlowResetStepMethod::Execute as i32,
            reason: "replay after the total duration".to_owned(),
            ..ResetFlowRequest::default()
        })
        .await
        .expect("ResetFlow restores WorkStep");
    let replayed = worker.next_task().await;
    assert_eq!(execute_step_type(&replayed), "WorkStep");
    worker.reply(replayed.task_id, complete_flow_output());
    assert_flow_completed(&server, flow_id).await;
    worker.stop().await;
    server.shutdown().await;
}
