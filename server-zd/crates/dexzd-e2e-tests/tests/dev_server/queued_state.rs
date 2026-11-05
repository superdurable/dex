// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What GetFlowState and history report for a Step that waits in the durable task queue (plan
//! TB14).

use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    FlowStatus, GetFlowStateRequest, GetHistoryEventsRequest, InvokeExecuteMethodResponse,
    PollWorkerTaskRejectReason, StepMethodInvocationPhase, StepMethodInvocationState,
    StepMethodPendingReason, StepOptions, WaitForFlowRequest, flow_history_event,
};
use tonic::transport::Channel;

use crate::harness::{
    DevServer, ScriptedWorker, execute_request, execute_result, force_complete, start_pull_flow,
};
use crate::string_value;

const FLOW_TYPE: &str = "QueuedStateFlow";
const STEP_EXECUTION_ID: &str = "ShipStep-1";
const STATE_BUDGET: Duration = Duration::from_secs(30);

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_queued_step_reports_why_it_waits_and_when_it_was_scheduled() {
    let server = DevServer::start_with_config_overrides(Some(
        "matching:\n  stickyWait: 50ms\n  syncMatchWait: 50ms\n",
    ))
    .await;
    let mut flow_client = server.flow_client();
    let flow_id = "queued-state";
    let started = start_pull_flow(
        &mut flow_client,
        flow_id,
        FLOW_TYPE,
        "ShipStep",
        string_value("parcel"),
        StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        },
    )
    .await;

    // No Worker of the Flow type is connected.
    let waiting = wait_for_execute_state(&mut flow_client, flow_id, |state| {
        state.pending_reason == StepMethodPendingReason::WaitingForWorker as i32
    })
    .await;
    assert_eq!(waiting.phase, StepMethodInvocationPhase::Pending as i32);
    assert_eq!(waiting.attempt, 1);
    let pending_since = waiting
        .pending_since_time
        .expect("a pending attempt reports when it became pending");
    assert!(waiting.attempt_started_time.is_none());

    // The only connected Worker declines the Step type.
    let mut decliner =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "decliner").await;
    let declined = decliner.next_task().await;
    assert_eq!(attempt(&declined), 1);
    decliner.reject(declined.task_id, PollWorkerTaskRejectReason::UnknownStep);
    let unsupported = wait_for_execute_state(&mut flow_client, flow_id, |state| {
        state.pending_reason == StepMethodPendingReason::WaitingForSupportingWorker as i32
    })
    .await;
    assert_eq!(unsupported.phase, StepMethodInvocationPhase::Pending as i32);
    assert_eq!(
        unsupported.attempt, 1,
        "a declined attempt keeps its number"
    );
    assert_eq!(
        unsupported.pending_since_time,
        Some(pending_since),
        "a declined attempt keeps its pending time"
    );

    // A supporting Worker connects and runs the attempt.
    let mut runner =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "runner").await;
    let task = runner.next_task().await;
    assert_eq!(attempt(&task), 1);
    runner.reply(
        task.task_id,
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("shipped"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 10,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Completed as i32);

    let events = flow_client
        .get_history_events(GetHistoryEventsRequest {
            flow_id: flow_id.to_owned(),
            run_id: started.run_id,
            estimate_page_size: 100,
            ..GetHistoryEventsRequest::default()
        })
        .await
        .expect("GetHistoryEvents succeeds")
        .into_inner()
        .events;
    let context = events
        .iter()
        .find_map(|event| match &event.payload {
            Some(flow_history_event::Payload::StepExecuteCompleted(completed)) => {
                completed.context.clone()
            }
            _ => None,
        })
        .expect("the history has the Execute completion");
    assert_eq!(context.final_attempt, 1);
    let scheduled = context
        .scheduled_time
        .expect("the completion reports when the attempt was scheduled");
    let attempt_started = context
        .started_time
        .expect("the completion reports when the attempt started");
    assert_eq!(scheduled, pending_since, "scheduled when it became pending");
    assert!(
        (scheduled.seconds, scheduled.nanos) <= (attempt_started.seconds, attempt_started.nanos),
        "scheduled {scheduled:?} after started {attempt_started:?}"
    );
    decliner.stop().await;
    runner.stop().await;
    server.shutdown().await;
}

/// Polls GetFlowState until the Execute invocation of the Flow's Step satisfies `is_done`.
///
/// Returns that invocation state.
async fn wait_for_execute_state(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    is_done: impl Fn(&StepMethodInvocationState) -> bool,
) -> StepMethodInvocationState {
    let mut last_seen = None;
    let polled = tokio::time::timeout(STATE_BUDGET, async {
        loop {
            let state = flow_client
                .get_flow_state(GetFlowStateRequest {
                    flow_id: flow_id.to_owned(),
                    ..GetFlowStateRequest::default()
                })
                .await
                .expect("GetFlowState succeeds")
                .into_inner();
            let invocation = state
                .active_step_executions
                .iter()
                .find(|step| step.step_execution_id == STEP_EXECUTION_ID)
                .and_then(|step| step.execute_invocation.clone());
            if let Some(invocation) = invocation {
                if is_done(&invocation) {
                    return invocation;
                }
                last_seen = Some(invocation);
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await;
    polled.unwrap_or_else(|_| {
        panic!("the Execute state did not match within {STATE_BUDGET:?}; last saw {last_seen:?}")
    })
}

fn attempt(task: &dexzd_protocol::dex::PollWorkerTask) -> i32 {
    execute_request(task)
        .context
        .as_ref()
        .expect("the task has a context")
        .attempt
}
