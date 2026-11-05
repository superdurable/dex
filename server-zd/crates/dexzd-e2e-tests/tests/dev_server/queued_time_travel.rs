// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Time travel while a Step waits in the durable task queue (plan TB21, TB23).

use std::time::Duration;

use dexzd_protocol::dex::{
    ChannelMessage, FlowResetType, FlowStatus, GetFlowStateRequest, GetFlowSummaryRequest,
    GetHistoryEventsRequest, InvokeExecuteMethodResponse, PublishToChannelRequest,
    ResetFlowRequest, StepMethodInvocationPhase, StepOptions, WaitForFlowRequest,
    flow_history_event,
};

use crate::harness::{
    DevServer, ScriptedWorker, execute_request, execute_result, force_complete,
    free_loopback_address, start_pull_flow,
};
use crate::queued_state::wait_for_queue_entries;
use crate::string_value;

const FLOW_TYPE: &str = "QueuedTravelFlow";

/// Where the time travel goes.
#[derive(Clone, Copy, Debug)]
enum TravelTarget {
    /// The Flow's beginning, before the attempt was queued.
    Beginning,
    /// A Channel publication recorded after the attempt was queued.
    PointAfterQueueing,
}

/// TB23: time travel to the beginning requeues the Step once in the new run attempt.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn time_travel_to_the_beginning_requeues_a_queued_step_once() {
    check_time_travel_while_queued(TravelTarget::Beginning).await;
}

/// TB21: with replay checks on, time travel to a point after queueing replays without a halt.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn time_travel_after_queueing_replays_with_checks_and_runs_the_step_once() {
    check_time_travel_while_queued(TravelTarget::PointAfterQueueing).await;
}

async fn check_time_travel_while_queued(target: TravelTarget) {
    let metrics = free_loopback_address();
    let server = DevServer::start_with_config_overrides(Some(&format!(
        "commit:\n  debugReplayChecks: true\nmetrics:\n  listen: '{metrics}'\nmatching:\n  stickyWait: 50ms\n  syncMatchWait: 50ms\n"
    )))
    .await;
    let mut flow_client = server.flow_client();
    let flow_id = "queued-travel";
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
    wait_for_queue_entries(metrics, 1).await;
    flow_client
        .publish_to_channel(PublishToChannelRequest {
            flow_id: flow_id.to_owned(),
            messages: vec![ChannelMessage {
                channel_name: "notes".to_owned(),
                value: Some(string_value("gift wrap")),
                ..ChannelMessage::default()
            }],
            ..PublishToChannelRequest::default()
        })
        .await
        .expect("PublishToChannel succeeds");

    let reset = match target {
        TravelTarget::Beginning => ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::Beginning as i32,
            ..ResetFlowRequest::default()
        },
        TravelTarget::PointAfterQueueing => ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            history_event_time: last_event_nanos(&server, flow_id, &started.run_id).await,
            run_attempt: 1,
            ..ResetFlowRequest::default()
        },
    };
    let reset = flow_client
        .reset_flow(reset)
        .await
        .unwrap_or_else(|status| panic!("{target:?}: ResetFlow succeeds: {status}"))
        .into_inner();
    assert_eq!(reset.run_attempt, 2, "{target:?}");
    // The old attempt's row is gone and the new attempt is queued.
    wait_for_queue_entries(metrics, 1).await;
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            run_id: String::new(),
        })
        .await
        .expect("the partition still serves after the time travel")
        .into_inner();
    assert_eq!(
        summary.flow_status,
        FlowStatus::Running as i32,
        "{target:?}"
    );
    assert_eq!(summary.run_attempt, 2, "{target:?}");
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
        .find_map(|step| step.execute_invocation)
        .unwrap_or_else(|| panic!("{target:?}: Execute is open: {state:?}"));
    assert_eq!(invocation.phase, StepMethodInvocationPhase::Pending as i32);
    assert_eq!(invocation.attempt, 1, "{target:?}");

    let mut worker =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "runner").await;
    let task = worker.next_task().await;
    let context = execute_request(&task)
        .context
        .clone()
        .expect("the task has a context");
    assert_eq!(
        (context.run_attempt, context.attempt),
        (2, 1),
        "{target:?}: only the new run attempt's task starts"
    );
    worker.reply(
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
    assert_eq!(
        result.flow_status,
        FlowStatus::Completed as i32,
        "{target:?}"
    );
    wait_for_queue_entries(metrics, 0).await;
    assert!(
        worker
            .next_task_within(Duration::from_millis(500))
            .await
            .is_none(),
        "{target:?}: no stale ref starts another task"
    );
    worker.stop().await;
    server.shutdown().await;
}

/// Returns the time of the run's last event, the publication, in Unix nanoseconds.
async fn last_event_nanos(server: &DevServer, flow_id: &str, run_id: &str) -> String {
    let events = server
        .flow_client()
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
    let last = events.last().expect("the run has events");
    assert!(
        matches!(
            last.payload,
            Some(flow_history_event::Payload::ChannelExternalPublish(_))
        ),
        "the publication is the last event: {last:?}"
    );
    let event_time = last.event_time.expect("the event has a time");
    (i128::from(event_time.seconds) * 1_000_000_000 + i128::from(event_time.nanos)).to_string()
}
