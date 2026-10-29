// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! StopFlow, SkipTimer, and UpdateFlowConfig answer like the Go Server.

use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    ActiveStepSearchMode, ConditionStatus, ErrorSubStatus, FlowConfig, FlowErrorType, FlowResult,
    FlowStartOptions, FlowStatus, GetFlowStateRequest, GetFlowSummaryRequest,
    GetHistoryEventsRequest, IdReusePolicy, InvokeExecuteMethodResponse,
    InvokeWaitForMethodResponse, SkipTimerRequest, StartFlowRequest, StepDurability, StepOptions,
    StopFlowRequest, StopType, TimerCondition, TimerResult, UpdateFlowConfigRequest,
    WaitForFlowRequest, WaitingCondition, WaitingConditionType, WorkerTarget, flow_history_event,
};
use tonic::transport::Channel;

use crate::harness::{
    DevServer, ScriptedWorker, execute_request, execute_result, force_complete,
    pull_mode_start_options, service_error, start_pull_flow, wait_for_request, wait_for_result,
    wait_until_step_waits,
};
use crate::string_value;

/// No Worker serves this Flow type, so its first Step stays scheduled until the run closes.
const IDLE_FLOW_TYPE: &str = "IdleControlFlow";
const IDLE_STEP_TYPE: &str = "IdleStep";
const TIMER_FLOW_TYPE: &str = "ReminderFlow";
const TIMER_STEP_TYPE: &str = "ReminderStep";

/// How long a call that must stay pending is watched before the test acts.
const PENDING_WINDOW: Duration = Duration::from_millis(500);

/// The Go Server's SkipTimer rejection when no pending timer matches.
const MISSING_TIMER_MESSAGE: &str = "requested timer condition does not exist or is not pending";

struct StopCase {
    flow_id: &'static str,
    stop_type: StopType,
    reason: &'static str,
    flow_status: FlowStatus,
    error_type: FlowErrorType,
    /// The Flow result message; only a failure keeps a reason.
    error_message: &'static str,
    /// The FlowClosed history message; only a termination or a failure keeps a reason.
    history_message: &'static str,
}

/// Every stop type closes a waiting run, wakes WaitForFlow, and records the close in history.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn stop_flow_closes_a_waiting_run_and_wakes_wait_for_flow() {
    let server = DevServer::start().await;
    let cases = [
        StopCase {
            flow_id: "terminate-without-reason",
            stop_type: StopType::Terminate,
            reason: "",
            flow_status: FlowStatus::Terminated,
            error_type: FlowErrorType::Unspecified,
            error_message: "",
            // The Go Server's default termination reason, spelling included.
            history_message: "Force termiantion from user",
        },
        StopCase {
            flow_id: "terminate-with-reason",
            stop_type: StopType::Terminate,
            reason: "operator terminated",
            flow_status: FlowStatus::Terminated,
            error_type: FlowErrorType::Unspecified,
            error_message: "",
            history_message: "operator terminated",
        },
        StopCase {
            flow_id: "cancel-with-reason",
            stop_type: StopType::Cancel,
            reason: "operator canceled",
            flow_status: FlowStatus::Canceled,
            error_type: FlowErrorType::Unspecified,
            error_message: "",
            history_message: "",
        },
        StopCase {
            flow_id: "stop-without-type",
            stop_type: StopType::Unspecified,
            reason: "",
            flow_status: FlowStatus::Canceled,
            error_type: FlowErrorType::Unspecified,
            error_message: "",
            history_message: "",
        },
        StopCase {
            flow_id: "fail-with-reason",
            stop_type: StopType::Fail,
            reason: "operator failed",
            flow_status: FlowStatus::Failed,
            error_type: FlowErrorType::ClientApiFailingFlow,
            error_message: "operator failed",
            history_message: "operator failed",
        },
        StopCase {
            flow_id: "fail-without-reason",
            stop_type: StopType::Fail,
            reason: "",
            flow_status: FlowStatus::Failed,
            error_type: FlowErrorType::ClientApiFailingFlow,
            // The Go Server's default failure reason.
            error_message: "fail by client",
            history_message: "fail by client",
        },
    ];
    for case in &cases {
        check_stop(&server, case).await;
    }
    server.shutdown().await;
}

async fn check_stop(server: &DevServer, case: &StopCase) {
    let mut flow_client = server.flow_client();
    let started = start_pull_flow(
        &mut flow_client,
        case.flow_id,
        IDLE_FLOW_TYPE,
        IDLE_STEP_TYPE,
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    let mut waiter_client = server.flow_client();
    let waited_flow_id = case.flow_id.to_owned();
    let mut waiter = tokio::spawn(async move {
        waiter_client
            .wait_for_flow(WaitForFlowRequest {
                flow_id: waited_flow_id,
                needs_results: true,
                wait_time_seconds: 30,
                ..WaitForFlowRequest::default()
            })
            .await
    });
    assert!(
        tokio::time::timeout(PENDING_WINDOW, &mut waiter)
            .await
            .is_err(),
        "{}: WaitForFlow returns before the stop",
        case.flow_id
    );

    flow_client
        .stop_flow(StopFlowRequest {
            flow_id: case.flow_id.to_owned(),
            reason: case.reason.to_owned(),
            stop_type: case.stop_type.into(),
            ..StopFlowRequest::default()
        })
        .await
        .expect("StopFlow succeeds");
    let result = tokio::time::timeout(Duration::from_secs(10), waiter)
        .await
        .expect("the stop wakes WaitForFlow")
        .expect("the waiting task finishes")
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(
        result,
        FlowResult {
            flow_status: case.flow_status.into(),
            error_type: case.error_type.into(),
            error_message: case.error_message.to_owned(),
            ..FlowResult::default()
        },
        "{}",
        case.flow_id
    );
    assert_summary_status(&mut flow_client, case.flow_id, case.flow_status).await;
    assert_stop_history(&mut flow_client, case, &started.run_id).await;
}

async fn assert_summary_status(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    flow_status: FlowStatus,
) {
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            run_id: String::new(),
        })
        .await
        .expect("GetFlowSummary succeeds")
        .into_inner();
    assert_eq!(summary.flow_status, flow_status as i32, "{flow_id}");
    assert!(summary.close_time.is_some(), "{flow_id} has a close time");
}

/// The history ends with the close and reports the unresolved WaitFor as pending, unless the
/// run failed.
async fn assert_stop_history(
    flow_client: &mut FlowServiceClient<Channel>,
    case: &StopCase,
    run_id: &str,
) {
    let events = flow_client
        .get_history_events(GetHistoryEventsRequest {
            flow_id: case.flow_id.to_owned(),
            run_id: run_id.to_owned(),
            estimate_page_size: 100,
            ..GetHistoryEventsRequest::default()
        })
        .await
        .expect("GetHistoryEvents succeeds")
        .into_inner()
        .events;
    let pending = events.iter().find_map(|event| match &event.payload {
        Some(flow_history_event::Payload::StepWaitForPending(pending)) => Some(pending),
        _ => None,
    });
    if case.flow_status == FlowStatus::Failed {
        // As in the Go Server, a failed close drops pending Step methods.
        assert!(
            pending.is_none(),
            "{}: a failed run reports a pending WaitFor in {events:?}",
            case.flow_id
        );
    } else {
        let pending =
            pending.unwrap_or_else(|| panic!("{}: no pending WaitFor in {events:?}", case.flow_id));
        let context = pending
            .context
            .as_ref()
            .expect("the pending event has a context");
        assert_eq!(context.step_execution_id, format!("{IDLE_STEP_TYPE}-1"));
        assert_eq!(context.step_type, IDLE_STEP_TYPE);
    }
    let Some(flow_history_event::Payload::FlowClosed(closed)) =
        events.last().and_then(|event| event.payload.as_ref())
    else {
        panic!(
            "{}: the history does not end with FlowClosed: {events:?}",
            case.flow_id
        );
    };
    assert_eq!(
        closed.flow_status, case.flow_status as i32,
        "{}",
        case.flow_id
    );
    assert_eq!(
        closed.error_type, case.error_type as i32,
        "{}",
        case.flow_id
    );
    assert_eq!(
        closed.error_message, case.history_message,
        "{}",
        case.flow_id
    );
    assert!(closed.results.is_empty());
}

/// Like the Go Server, StopFlow on a closed run is NotFound, also when a newer run exists.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn stop_flow_on_a_closed_run_is_not_found() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let flow_id = "stop-closed-run";
    let first_run = start_pull_flow(
        &mut flow_client,
        flow_id,
        IDLE_FLOW_TYPE,
        IDLE_STEP_TYPE,
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    flow_client
        .stop_flow(StopFlowRequest {
            flow_id: flow_id.to_owned(),
            stop_type: StopType::Terminate.into(),
            ..StopFlowRequest::default()
        })
        .await
        .expect("StopFlow succeeds");
    assert_summary_status(&mut flow_client, flow_id, FlowStatus::Terminated).await;
    for stop_type in [StopType::Terminate, StopType::Cancel, StopType::Fail] {
        assert_closed_run_stop_is_not_found(&mut flow_client, flow_id, "", stop_type).await;
    }
    assert_summary_status(&mut flow_client, flow_id, FlowStatus::Terminated).await;

    let second_run = flow_client
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: IDLE_FLOW_TYPE.to_owned(),
            start_step_type: IDLE_STEP_TYPE.to_owned(),
            flow_start_options: Some(FlowStartOptions {
                id_reuse_policy: IdReusePolicy::AllowIfNoRunning.into(),
                ..pull_mode_start_options()
            }),
            request_id: format!("{flow_id}-second-start"),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow reuses the Flow ID of the closed run")
        .into_inner();
    assert_ne!(second_run.run_id, first_run.run_id);
    for stop_type in [StopType::Terminate, StopType::Cancel] {
        assert_closed_run_stop_is_not_found(
            &mut flow_client,
            flow_id,
            &first_run.run_id,
            stop_type,
        )
        .await;
    }
    flow_client
        .stop_flow(StopFlowRequest {
            flow_id: flow_id.to_owned(),
            stop_type: StopType::Cancel.into(),
            ..StopFlowRequest::default()
        })
        .await
        .expect("StopFlow without a run ID stops the newer run");
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            run_id: String::new(),
        })
        .await
        .expect("GetFlowSummary succeeds")
        .into_inner();
    assert_eq!(
        summary
            .flow_execution_id
            .map(|execution| (execution.run_id, summary.flow_status)),
        Some((second_run.run_id, FlowStatus::Canceled as i32))
    );
    server.shutdown().await;
}

/// Asserts the Go Server's answer to stopping a closed run: NotFound with the backend's text.
async fn assert_closed_run_stop_is_not_found(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    run_id: &str,
    stop_type: StopType,
) {
    let rejection = flow_client
        .stop_flow(StopFlowRequest {
            flow_id: flow_id.to_owned(),
            run_id: run_id.to_owned(),
            stop_type: stop_type.into(),
            ..StopFlowRequest::default()
        })
        .await
        .expect_err("a closed run cannot be stopped");
    assert_eq!(rejection.code(), tonic::Code::NotFound, "{stop_type:?}");
    assert_eq!(
        rejection.message(),
        "workflow execution already completed",
        "{stop_type:?}"
    );
    assert_eq!(
        service_error(&rejection).sub_status,
        ErrorSubStatus::FlowNotExists as i32,
        "{stop_type:?}"
    );
}

/// Like the Go Server, an unknown stop type is InvalidArgument before the Flow is looked up.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn stop_flow_with_an_unknown_stop_type_is_invalid_argument() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let flow_id = "stop-with-unknown-type";
    start_pull_flow(
        &mut flow_client,
        flow_id,
        IDLE_FLOW_TYPE,
        IDLE_STEP_TYPE,
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    for (target_flow_id, stop_type) in [
        (flow_id, 4),
        (flow_id, -1),
        ("missing-flow-with-unknown-stop-type", 4),
    ] {
        let rejection = flow_client
            .stop_flow(StopFlowRequest {
                flow_id: target_flow_id.to_owned(),
                stop_type,
                reason: "unknown stop type".to_owned(),
                ..StopFlowRequest::default()
            })
            .await
            .expect_err("an unknown stop type is rejected");
        assert_eq!(
            rejection.code(),
            tonic::Code::InvalidArgument,
            "{target_flow_id} {stop_type}: {rejection:?}"
        );
        assert_eq!(rejection.message(), "stop type is required");
        assert_eq!(
            service_error(&rejection).sub_status,
            ErrorSubStatus::Uncategorized as i32,
            "{target_flow_id} {stop_type}"
        );
    }
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            run_id: String::new(),
        })
        .await
        .expect("GetFlowSummary succeeds")
        .into_inner();
    assert_eq!(summary.flow_status, FlowStatus::Running as i32);
    server.shutdown().await;
}

/// SkipTimer selects a pending timer by condition ID or index; the Step proceeds once both fire.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn skip_timer_lets_a_step_waiting_on_timers_proceed() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), TIMER_FLOW_TYPE).await;
    let flow_id = "skip-timer-flow";
    let step_execution_id = format!("{TIMER_STEP_TYPE}-1");
    start_pull_flow(
        &mut flow_client,
        flow_id,
        TIMER_FLOW_TYPE,
        TIMER_STEP_TYPE,
        string_value("remind"),
        StepOptions::default(),
    )
    .await;
    let wait_for_task = worker.next_task().await;
    let wait_for = wait_for_request(&wait_for_task);
    assert_eq!(
        wait_for
            .context
            .as_ref()
            .map(|context| context.step_execution_id.as_str()),
        Some(step_execution_id.as_str())
    );
    worker.reply(
        wait_for_task.task_id.clone(),
        wait_for_result(InvokeWaitForMethodResponse {
            waiting_condition: Some(WaitingCondition {
                waiting_condition_type: WaitingConditionType::AllCompleted.into(),
                timer_conditions: vec![hour_timer("first-reminder"), hour_timer("second-reminder")],
                ..WaitingCondition::default()
            }),
            ..InvokeWaitForMethodResponse::default()
        }),
    );
    wait_until_step_waits(&mut flow_client, flow_id, &step_execution_id).await;

    let unselected = skip_timer(&mut flow_client, flow_id, &step_execution_id, "", None)
        .await
        .expect_err("SkipTimer needs a condition ID or index");
    assert_eq!(unselected.code(), tonic::Code::InvalidArgument);
    assert!(
        unselected
            .message()
            .contains("timer condition ID or index is required"),
        "{unselected:?}"
    );
    for (timer_condition_id, timer_condition_index) in [("unknown-reminder", None), ("", Some(2))] {
        let rejection = skip_timer(
            &mut flow_client,
            flow_id,
            &step_execution_id,
            timer_condition_id,
            timer_condition_index,
        )
        .await
        .expect_err("a timer that does not exist cannot be skipped");
        assert_eq!(
            rejection.code(),
            tonic::Code::InvalidArgument,
            "{rejection:?}"
        );
        assert_eq!(rejection.message(), MISSING_TIMER_MESSAGE);
    }
    let unknown_step = skip_timer(&mut flow_client, flow_id, "MissingStep-1", "", Some(0))
        .await
        .expect_err("a timer of an unknown Step execution cannot be skipped");
    assert_eq!(unknown_step.code(), tonic::Code::InvalidArgument);
    assert_eq!(unknown_step.message(), MISSING_TIMER_MESSAGE);

    skip_timer(
        &mut flow_client,
        flow_id,
        &step_execution_id,
        "first-reminder",
        None,
    )
    .await
    .expect("SkipTimer by condition ID succeeds");
    let early_task = tokio::time::timeout(PENDING_WINDOW, worker.next_task()).await;
    assert!(
        early_task.is_err(),
        "the Step waits for both timers, but Execute was dispatched"
    );
    let repeated = skip_timer(
        &mut flow_client,
        flow_id,
        &step_execution_id,
        "first-reminder",
        None,
    )
    .await
    .expect_err("a skipped timer is no longer pending");
    assert_eq!(repeated.code(), tonic::Code::InvalidArgument);
    assert_eq!(repeated.message(), MISSING_TIMER_MESSAGE);
    skip_timer(&mut flow_client, flow_id, &step_execution_id, "", Some(1))
        .await
        .expect("SkipTimer by index succeeds");

    let execute_task = worker.next_task().await;
    let execute = execute_request(&execute_task);
    assert_eq!(execute.step_type, TIMER_STEP_TYPE);
    let timer_results = execute
        .condition_results
        .as_ref()
        .map(|results| results.timer_results.clone());
    assert_eq!(
        timer_results,
        Some(vec![
            completed_timer("first-reminder"),
            completed_timer("second-reminder"),
        ])
    );
    worker.reply(
        execute_task.task_id.clone(),
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("reminded"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Completed as i32);
    worker.stop().await;
    server.shutdown().await;
}

fn hour_timer(condition_id: &str) -> TimerCondition {
    TimerCondition {
        condition_id: condition_id.to_owned(),
        duration_seconds: 3600,
        ..TimerCondition::default()
    }
}

fn completed_timer(condition_id: &str) -> TimerResult {
    TimerResult {
        condition_id: condition_id.to_owned(),
        condition_status: ConditionStatus::Completed.into(),
    }
}

async fn skip_timer(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    step_execution_id: &str,
    timer_condition_id: &str,
    timer_condition_index: Option<i32>,
) -> Result<tonic::Response<()>, tonic::Status> {
    flow_client
        .skip_timer(SkipTimerRequest {
            flow_id: flow_id.to_owned(),
            step_execution_id: step_execution_id.to_owned(),
            timer_condition_id: timer_condition_id.to_owned(),
            timer_condition_index,
            ..SkipTimerRequest::default()
        })
        .await
}

/// UpdateFlowConfig replaces only the fields it sets, and GetFlowState returns the merged config.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn update_flow_config_merges_into_the_stored_config() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let flow_id = "update-config-flow";
    start_pull_flow(
        &mut flow_client,
        flow_id,
        IDLE_FLOW_TYPE,
        IDLE_STEP_TYPE,
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    let started_config = stored_flow_config(&mut flow_client, flow_id).await;
    assert_eq!(
        started_config.worker_target,
        Some(WorkerTarget { is_pull_mode: true })
    );

    flow_client
        .update_flow_config(UpdateFlowConfigRequest {
            flow_id: flow_id.to_owned(),
            flow_config: Some(FlowConfig {
                active_step_search_mode: Some(ActiveStepSearchMode::Disabled.into()),
                step_durability: Some(StepDurability::Async.into()),
                ..FlowConfig::default()
            }),
            ..UpdateFlowConfigRequest::default()
        })
        .await
        .expect("UpdateFlowConfig succeeds");
    assert_eq!(
        stored_flow_config(&mut flow_client, flow_id).await,
        FlowConfig {
            active_step_search_mode: Some(ActiveStepSearchMode::Disabled.into()),
            step_durability: Some(StepDurability::Async.into()),
            ..started_config.clone()
        }
    );

    let missing = flow_client
        .update_flow_config(UpdateFlowConfigRequest {
            flow_id: "missing-config-flow".to_owned(),
            flow_config: Some(started_config),
            ..UpdateFlowConfigRequest::default()
        })
        .await
        .expect_err("an unknown Flow has no config");
    assert_eq!(missing.code(), tonic::Code::NotFound);
    assert_eq!(
        missing.message(),
        "Flow run is not found for flow ID missing-config-flow"
    );
    server.shutdown().await;
}

async fn stored_flow_config(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
) -> FlowConfig {
    flow_client
        .get_flow_state(GetFlowStateRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowStateRequest::default()
        })
        .await
        .expect("GetFlowState succeeds")
        .into_inner()
        .flow_config
        .expect("a running Flow has a config")
}
