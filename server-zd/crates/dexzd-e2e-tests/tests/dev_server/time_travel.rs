// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! ResetFlow (time travel) restores the state of its target point and runs the Step again.

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, ChannelCondition, ChannelMessage, FlowResetStepMethod, FlowResetType,
    FlowStatus, GetAttributesRequest, GetChannelMessagesRequest, GetFlowSummaryRequest,
    GetHistoryEventsRequest, InvokeExecuteMethodResponse, InvokeWaitForMethodResponse,
    PublishToChannelRequest, ResetFlowRequest, ResetFlowResponse, SetAttributesRequest,
    StepDecision, StepMovement, StepOptions, TimeTravelForkHistoryEvent, WaitForFlowRequest,
    WaitingCondition, WaitingConditionType, flow_history_event,
};
use tonic::transport::Channel;

use crate::harness::{
    DevServer, ScriptedWorker, execute_request, execute_result, force_complete, start_pull_flow,
    wait_for_request, wait_for_result, wait_until_step_waits,
};
use crate::string_value;

const FLOW_TYPE: &str = "OrderFlow";
const CHARGE_STEP_TYPE: &str = "ChargeStep";
const REVIEW_STEP_TYPE: &str = "ReviewStep";
const CHARGE_STEP_EXECUTION_ID: &str = "ChargeStep-1";
const REVIEW_STEP_EXECUTION_ID: &str = "ReviewStep-1";
const APPROVALS_CHANNEL: &str = "approvals";
const ORDER_INPUT: &str = "order-1";

/// The Step's own write; the target point precedes it, so time travel always removes it.
const STEP_ATTRIBUTE: &str = "stage";
/// A client write after the target point; time travel reapplies it unless told to skip.
const CLIENT_ATTRIBUTE: &str = "note";

/// Time travel to the beginning reopens a completed run and reapplies later client input.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn time_travel_to_the_beginning_reopens_a_completed_flow() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "travel-to-beginning";
    let run_id = run_until_review_waits(&mut flow_client, &mut worker, flow_id).await;
    flow_client
        .publish_to_channel(PublishToChannelRequest {
            flow_id: flow_id.to_owned(),
            messages: vec![ChannelMessage {
                channel_name: APPROVALS_CHANNEL.to_owned(),
                value: Some(string_value("approved")),
                ..ChannelMessage::default()
            }],
            ..PublishToChannelRequest::default()
        })
        .await
        .expect("PublishToChannel succeeds");
    let review_task = worker.next_task().await;
    assert_eq!(execute_request(&review_task).step_type, REVIEW_STEP_TYPE);
    worker.reply(
        review_task.task_id.clone(),
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("reviewed"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_eq!(
        wait_for_status(&mut flow_client, flow_id).await,
        FlowStatus::Completed
    );

    let reset = reset_flow(
        &mut flow_client,
        ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::Beginning.into(),
            reason: "replay the order".to_owned(),
            ..ResetFlowRequest::default()
        },
    )
    .await;
    assert_eq!(
        reset,
        ResetFlowResponse {
            run_id: run_id.clone(),
            run_attempt: 2,
        }
    );
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            run_id: String::new(),
        })
        .await
        .expect("GetFlowSummary succeeds")
        .into_inner();
    assert_eq!(summary.flow_status, FlowStatus::Running as i32);
    assert_eq!(summary.run_attempt, 2);
    assert!(summary.close_time.is_none());
    assert_eq!(
        attribute_keys(&mut flow_client, flow_id).await,
        vec![CLIENT_ATTRIBUTE.to_owned()]
    );
    let approvals = flow_client
        .get_channel_messages(GetChannelMessagesRequest {
            flow_id: flow_id.to_owned(),
            channel_name: APPROVALS_CHANNEL.to_owned(),
            ..GetChannelMessagesRequest::default()
        })
        .await
        .expect("GetChannelMessages succeeds")
        .into_inner()
        .messages;
    assert_eq!(
        approvals
            .iter()
            .map(|message| message.value.clone())
            .collect::<Vec<_>>(),
        vec![Some(string_value("approved"))],
        "the publication after the start is reapplied and pending again"
    );
    let fork = time_travel_fork(&mut flow_client, flow_id, &run_id).await;
    assert_eq!(fork.previous_run_id, run_id);
    assert_eq!((fork.previous_run_attempt, fork.target_run_attempt), (1, 1));

    finish_after_rerun(
        &mut flow_client,
        &mut worker,
        flow_id,
        &run_id,
        &[CLIENT_ATTRIBUTE],
    )
    .await;
    worker.stop().await;
    server.shutdown().await;
}

/// Time travel to a Step execution restores its Attributes and reapplies client writes unless skipped.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn time_travel_to_a_step_execution_reapplies_client_writes_unless_skipped() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    for (flow_id, is_skipping_writes) in [
        ("travel-to-step-reapplying", false),
        ("travel-to-step-skipping-writes", true),
    ] {
        let run_id = run_until_review_waits(&mut flow_client, &mut worker, flow_id).await;
        let reset = reset_flow(
            &mut flow_client,
            ResetFlowRequest {
                flow_id: flow_id.to_owned(),
                reset_type: FlowResetType::StepExecutionId.into(),
                step_execution_id: CHARGE_STEP_EXECUTION_ID.to_owned(),
                step_method: FlowResetStepMethod::Execute.into(),
                skip_writes_reapply: is_skipping_writes,
                ..ResetFlowRequest::default()
            },
        )
        .await;
        assert_eq!(reset.run_id, run_id, "{flow_id}");
        assert_eq!(reset.run_attempt, 2, "{flow_id}");
        let expected_attributes: &[&str] = if is_skipping_writes {
            &[]
        } else {
            &[CLIENT_ATTRIBUTE]
        };
        assert_eq!(
            attribute_keys(&mut flow_client, flow_id).await,
            expected_attributes,
            "{flow_id}"
        );
        finish_after_rerun(
            &mut flow_client,
            &mut worker,
            flow_id,
            &run_id,
            expected_attributes,
        )
        .await;
    }
    worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn reset_to_a_closed_point_returns_through_wait_for_flow_and_history() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "travel-to-closed-point";
    let started = start_pull_flow(
        &mut flow_client,
        flow_id,
        FLOW_TYPE,
        CHARGE_STEP_TYPE,
        string_value(ORDER_INPUT),
        StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        },
    )
    .await;
    let task = worker.next_task().await;
    worker.reply(
        task.task_id.clone(),
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("done"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_eq!(
        wait_for_status(&mut flow_client, flow_id).await,
        FlowStatus::Completed
    );
    let original_events = flow_client
        .get_history_events(GetHistoryEventsRequest {
            flow_id: flow_id.to_owned(),
            run_id: started.run_id.clone(),
            estimate_page_size: 100,
            ..GetHistoryEventsRequest::default()
        })
        .await
        .expect("GetHistoryEvents succeeds")
        .into_inner()
        .events;
    let original_close = original_events
        .iter()
        .find(|event| {
            matches!(
                event.payload,
                Some(flow_history_event::Payload::FlowClosed(_))
            )
        })
        .expect("original FlowClosed event");
    let event_time = original_close
        .event_time
        .as_ref()
        .expect("close event time");
    let close_nanos = i128::from(event_time.seconds) * 1_000_000_000 + i128::from(event_time.nanos);
    reset_flow(
        &mut flow_client,
        ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::Beginning as i32,
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        },
    )
    .await;
    let restored = reset_flow(
        &mut flow_client,
        ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            history_event_time: close_nanos.to_string(),
            run_attempt: 1,
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        },
    )
    .await;
    assert_eq!(restored.run_id, started.run_id);
    assert_eq!(restored.run_attempt, 3);
    assert_eq!(
        wait_for_status(&mut flow_client, flow_id).await,
        FlowStatus::Completed
    );
    let events = flow_client
        .get_history_events(GetHistoryEventsRequest {
            flow_id: flow_id.to_owned(),
            run_id: started.run_id,
            run_attempt: 3,
            estimate_page_size: 100,
            ..GetHistoryEventsRequest::default()
        })
        .await
        .expect("GetHistoryEvents succeeds after reset")
        .into_inner()
        .events;
    let fork_index = events
        .iter()
        .position(|event| {
            event.run_attempt == 3
                && matches!(
                    event.payload,
                    Some(flow_history_event::Payload::TimeTravelFork(_))
                )
        })
        .expect("fork event in attempt 3");
    let Some(flow_history_event::Payload::FlowClosed(closed)) = &events[fork_index + 1].payload
    else {
        panic!("FlowClosed follows TimeTravelFork");
    };
    assert_eq!(closed.flow_status, FlowStatus::Completed as i32);
    assert_eq!(closed.results.len(), 1);
    worker.stop().await;
    server.shutdown().await;
}

/// Runs ChargeStep, which writes an Attribute, until ReviewStep waits; then a client writes one.
///
/// Returns the run ID.
async fn run_until_review_waits(
    flow_client: &mut FlowServiceClient<Channel>,
    worker: &mut ScriptedWorker,
    flow_id: &str,
) -> String {
    let started = start_pull_flow(
        flow_client,
        flow_id,
        FLOW_TYPE,
        CHARGE_STEP_TYPE,
        string_value(ORDER_INPUT),
        StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        },
    )
    .await;
    let charge_task = worker.next_task().await;
    let charge = execute_request(&charge_task);
    let context = charge.context.as_ref().expect("Execute has a context");
    assert_eq!(context.step_execution_id, CHARGE_STEP_EXECUTION_ID);
    assert_eq!(context.run_attempt, 1);
    worker.reply(
        charge_task.task_id.clone(),
        execute_result(InvokeExecuteMethodResponse {
            upsert_attributes: vec![attribute_write(STEP_ATTRIBUTE, "charged")],
            step_decision: Some(StepDecision {
                next_steps: vec![StepMovement {
                    step_type: REVIEW_STEP_TYPE.to_owned(),
                    step_input: Some(string_value("review")),
                    ..StepMovement::default()
                }],
                ..StepDecision::default()
            }),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    let review_task = worker.next_task().await;
    let review = wait_for_request(&review_task);
    assert_eq!(review.step_type, REVIEW_STEP_TYPE);
    worker.reply(
        review_task.task_id.clone(),
        wait_for_result(InvokeWaitForMethodResponse {
            waiting_condition: Some(WaitingCondition {
                waiting_condition_type: WaitingConditionType::AllCompleted.into(),
                channel_conditions: vec![ChannelCondition {
                    channel_name: APPROVALS_CHANNEL.to_owned(),
                    ..ChannelCondition::default()
                }],
                ..WaitingCondition::default()
            }),
            ..InvokeWaitForMethodResponse::default()
        }),
    );
    wait_until_step_waits(flow_client, flow_id, REVIEW_STEP_EXECUTION_ID).await;
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: flow_id.to_owned(),
            attributes: vec![attribute_write(CLIENT_ATTRIBUTE, "gift wrap")],
            request_id: format!("{flow_id}-note"),
            ..SetAttributesRequest::default()
        })
        .await
        .expect("SetAttributes succeeds");
    assert_eq!(
        attribute_keys(flow_client, flow_id).await,
        vec![CLIENT_ATTRIBUTE.to_owned(), STEP_ATTRIBUTE.to_owned()]
    );
    started.run_id
}

/// ChargeStep runs again with its original input on run attempt 2, then completes the run.
async fn finish_after_rerun(
    flow_client: &mut FlowServiceClient<Channel>,
    worker: &mut ScriptedWorker,
    flow_id: &str,
    run_id: &str,
    expected_attributes: &[&str],
) {
    let rerun_task = worker.next_task().await;
    let rerun = execute_request(&rerun_task);
    assert_eq!(rerun.step_type, CHARGE_STEP_TYPE, "{flow_id}");
    assert_eq!(
        rerun.step_input,
        Some(string_value(ORDER_INPUT)),
        "{flow_id}"
    );
    let context = rerun.context.as_ref().expect("Execute has a context");
    assert_eq!(context.flow_id, flow_id);
    assert_eq!(context.run_id, run_id);
    assert_eq!(context.step_execution_id, CHARGE_STEP_EXECUTION_ID);
    assert_eq!(context.run_attempt, 2, "{flow_id}");
    assert_eq!(context.attempt, 1, "{flow_id}");
    assert_eq!(
        rerun
            .attributes
            .iter()
            .map(|attribute| attribute.key.as_str())
            .collect::<Vec<_>>(),
        expected_attributes,
        "{flow_id}: the rerun sees the restored Attributes"
    );
    worker.reply(
        rerun_task.task_id.clone(),
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("charged again"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_eq!(
        wait_for_status(flow_client, flow_id).await,
        FlowStatus::Completed,
        "{flow_id}"
    );
}

async fn reset_flow(
    flow_client: &mut FlowServiceClient<Channel>,
    request: ResetFlowRequest,
) -> ResetFlowResponse {
    flow_client
        .reset_flow(request)
        .await
        .expect("ResetFlow succeeds")
        .into_inner()
}

async fn wait_for_status(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
) -> FlowStatus {
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    FlowStatus::try_from(result.flow_status).expect("the status is a known value")
}

async fn attribute_keys(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
) -> Vec<String> {
    let mut keys: Vec<String> = flow_client
        .get_attributes(GetAttributesRequest {
            flow_id: flow_id.to_owned(),
            all_keys: true,
            ..GetAttributesRequest::default()
        })
        .await
        .expect("GetAttributes succeeds")
        .into_inner()
        .attributes
        .into_iter()
        .map(|attribute| attribute.key)
        .collect();
    keys.sort();
    keys
}

async fn time_travel_fork(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    run_id: &str,
) -> TimeTravelForkHistoryEvent {
    let events = flow_client
        .get_history_events(GetHistoryEventsRequest {
            flow_id: flow_id.to_owned(),
            run_id: run_id.to_owned(),
            estimate_page_size: 100,
            ..GetHistoryEventsRequest::default()
        })
        .await
        .expect("GetHistoryEvents succeeds")
        .into_inner()
        .events;
    events
        .iter()
        .find_map(|event| match &event.payload {
            Some(flow_history_event::Payload::TimeTravelFork(fork)) => Some(fork.clone()),
            _ => None,
        })
        .unwrap_or_else(|| panic!("no time travel fork in {events:?}"))
}

fn attribute_write(key: &str, text: &str) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value: Some(string_value(text)),
        ..AttributeWrite::default()
    }
}
