// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A natural close waits for transactional RPCs admitted before it, as the Go Server waits for
//! in-flight updates, and finalizes once they commit or fail.

use std::time::Duration;

use tokio::time::Instant;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, ChannelCondition, FlowErrorType, FlowStatus, GetAttributesRequest,
    GetFlowSummaryRequest, InvokeRpcRequest, InvokeWaitForMethodResponse, InvokeWorkerRpcResponse,
    StartFlowRequest, Value, WaitForFlowRequest, WaitingCondition, WaitingConditionType,
    poll_worker_task, poll_worker_task_frame, value,
};
use tonic::transport::Channel;

use crate::harness::{DevServer, ScriptedWorker, pull_mode_start_options, wait_for_result};

const FLOW_TYPE: &str = "HeldCloseFlow";
/// Long enough for a debug-build runner to admit the RPC before the timeout fires.
const FLOW_TIMEOUT: Duration = Duration::from_secs(5);

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

/// Starts a run with a short Flow timeout whose Step then waits on a Channel nobody uses.
///
/// Returns when the run started, so callers can check that later steps beat the timeout.
async fn start_waiting_run(
    flow_client: &mut FlowServiceClient<Channel>,
    worker: &mut ScriptedWorker,
    flow_id: &str,
) -> Instant {
    let started_at = Instant::now();
    flow_client
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            flow_timeout_seconds: i32::try_from(FLOW_TIMEOUT.as_secs()).expect("fits i32"),
            start_step_type: "WaitStep".to_owned(),
            flow_start_options: Some(pull_mode_start_options()),
            request_id: format!("{flow_id}-start"),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow succeeds");
    let wait_for = worker.next_task().await;
    assert!(matches!(
        wait_for.request,
        Some(poll_worker_task::Request::WaitFor(_))
    ));
    worker.reply(
        wait_for.task_id,
        wait_for_result(InvokeWaitForMethodResponse {
            waiting_condition: Some(WaitingCondition {
                waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                channel_conditions: vec![ChannelCondition {
                    condition_id: "never".to_owned(),
                    channel_name: "never".to_owned(),
                    ..ChannelCondition::default()
                }],
                ..WaitingCondition::default()
            }),
            ..InvokeWaitForMethodResponse::default()
        }),
    );
    started_at
}

/// Fails with the real cause when the runner was too slow to admit the RPC before the timeout.
fn assert_admitted_before_timeout(started_at: Instant) {
    assert!(
        started_at.elapsed() < FLOW_TIMEOUT - Duration::from_secs(1),
        "the RPC must reach the Worker before the Flow timeout; it took {:?}",
        started_at.elapsed()
    );
}

fn transactional_rpc(flow_id: &str, timeout_seconds: i32) -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        rpc_name: "approveOrder".to_owned(),
        is_transactional: true,
        timeout_seconds,
        request_id: format!("{flow_id}-rpc"),
        ..InvokeRpcRequest::default()
    }
}

async fn flow_status(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) -> i32 {
    flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowSummaryRequest::default()
        })
        .await
        .expect("GetFlowSummary succeeds")
        .into_inner()
        .flow_status
}

/// The timeout fires while the Worker holds the RPC; the close waits for the RPC's commit.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_flow_timeout_waits_for_an_admitted_transactional_rpc_to_commit() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "held-close-commit";
    let started_at = start_waiting_run(&mut flow_client, &mut worker, flow_id).await;
    let mut rpc_client = flow_client.clone();
    let rpc =
        tokio::spawn(async move { rpc_client.invoke_rpc(transactional_rpc(flow_id, 30)).await });
    let rpc_task = worker.next_task().await;
    assert!(matches!(
        rpc_task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    assert_admitted_before_timeout(started_at);
    tokio::time::sleep_until(started_at + FLOW_TIMEOUT + Duration::from_secs(2)).await;
    assert_eq!(
        flow_status(&mut flow_client, flow_id).await,
        FlowStatus::Running as i32,
        "the timed-out run waits for the admitted RPC"
    );
    let still_running = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 1,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect_err("WaitForFlow keeps waiting");
    assert_eq!(still_running.code(), tonic::Code::DeadlineExceeded);

    worker.reply(
        rpc_task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse {
            output: Some(string_value("approved")),
            upsert_attributes: vec![AttributeWrite {
                key: "approval".to_owned(),
                value: Some(string_value("granted")),
                ..AttributeWrite::default()
            }],
            ..InvokeWorkerRpcResponse::default()
        }),
    );
    let response = rpc
        .await
        .expect("the RPC task finishes")
        .expect("the admitted RPC commits")
        .into_inner();
    assert_eq!(response.output, Some(string_value("approved")));
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("the close finalizes after the RPC")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Failed as i32);
    assert_eq!(result.error_type, FlowErrorType::FlowTimeout as i32);
    let attributes = flow_client
        .get_attributes(GetAttributesRequest {
            flow_id: flow_id.to_owned(),
            all_keys: true,
            ..GetAttributesRequest::default()
        })
        .await
        .expect("GetAttributes succeeds")
        .into_inner();
    assert!(
        attributes
            .attributes
            .iter()
            .any(|attribute| attribute.key == "approval"
                && attribute.value == Some(string_value("granted"))),
        "the RPC's write is part of the closed run: {attributes:?}"
    );
    worker.stop().await;
    server.shutdown().await;
}

/// The admitted RPC times out without a result; the held close finalizes right after.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_held_close_finalizes_when_the_admitted_rpc_times_out() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let flow_id = "held-close-timeout";
    let started_at = start_waiting_run(&mut flow_client, &mut worker, flow_id).await;
    let mut rpc_client = flow_client.clone();
    let rpc =
        tokio::spawn(async move { rpc_client.invoke_rpc(transactional_rpc(flow_id, 9)).await });
    let rpc_task = worker.next_task().await;
    assert!(matches!(
        rpc_task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    assert_admitted_before_timeout(started_at);
    tokio::time::sleep_until(started_at + FLOW_TIMEOUT + Duration::from_secs(1)).await;
    assert_eq!(
        flow_status(&mut flow_client, flow_id).await,
        FlowStatus::Running as i32,
        "the timed-out run waits for the admitted RPC"
    );
    let error = rpc
        .await
        .expect("the RPC task finishes")
        .expect_err("the Worker never answered");
    assert_eq!(error.code(), tonic::Code::DeadlineExceeded);
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 10,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("the close finalizes once the RPC ends")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Failed as i32);
    assert_eq!(result.error_type, FlowErrorType::FlowTimeout as i32);
    worker.stop().await;
    server.shutdown().await;
}
