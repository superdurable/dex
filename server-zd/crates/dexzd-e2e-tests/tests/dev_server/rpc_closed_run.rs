// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use dexzd_protocol::dex::{
    AttributeWrite, ChannelCondition, ErrorSubStatus, InvokeRpcRequest,
    InvokeWaitForMethodResponse, InvokeWorkerRpcResponse, StepOptions, StopFlowRequest, StopType,
    WaitingCondition, WaitingConditionType, poll_worker_task, poll_worker_task_frame,
};
use tonic::transport::Channel;

use crate::harness::{DevServer, ScriptedWorker, service_error, start_pull_flow, wait_for_result};
use crate::string_value;

const FLOW_ID: &str = "closed-rpc-flow";
const FLOW_TYPE: &str = "ClosedRpcFlow";

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn closed_run_rejects_transactional_rpcs_before_the_worker_and_serves_plain_rpcs() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut worker = ScriptedWorker::start(server.worker_client(), FLOW_TYPE).await;
    let started = start_pull_flow(
        &mut flow_client,
        FLOW_ID,
        FLOW_TYPE,
        "WaitStep",
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    let wait_task = worker.next_task().await;
    assert!(matches!(
        wait_task.request,
        Some(poll_worker_task::Request::WaitFor(_))
    ));
    worker.reply(
        wait_task.task_id,
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

    let writing_rpc = InvokeRpcRequest {
        flow_id: FLOW_ID.to_owned(),
        run_id: started.run_id.clone(),
        rpc_name: "updateOrder".to_owned(),
        request_id: "open-writing-rpc".to_owned(),
        is_transactional: true,
        ..InvokeRpcRequest::default()
    };
    let mut writing_client = flow_client.clone();
    let open_request = writing_rpc.clone();
    let open_rpc = tokio::spawn(async move { writing_client.invoke_rpc(open_request).await });
    let rpc_task = worker.next_task().await;
    assert!(matches!(
        rpc_task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    worker.reply(
        rpc_task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse {
            output: Some(string_value("updated")),
            upsert_attributes: vec![AttributeWrite {
                key: "status".to_owned(),
                value: Some(string_value("updated")),
                ..AttributeWrite::default()
            }],
            ..InvokeWorkerRpcResponse::default()
        }),
    );
    let open_response = open_rpc
        .await
        .expect("the open RPC task finishes")
        .expect("the writing RPC commits")
        .into_inner();

    flow_client
        .stop_flow(StopFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
            stop_type: StopType::Terminate as i32,
            ..StopFlowRequest::default()
        })
        .await
        .expect("StopFlow closes the run");

    let repeated_response =
        rpc_without_worker_call(&mut flow_client, &mut worker, writing_rpc.clone())
            .await
            .expect("a recorded request ID returns its earlier reply")
            .into_inner();
    assert_eq!(repeated_response, open_response);
    let reused_request_id = rpc_without_worker_call(
        &mut flow_client,
        &mut worker,
        InvokeRpcRequest {
            rpc_name: "getOrder".to_owned(),
            ..writing_rpc.clone()
        },
    )
    .await
    .expect_err("a request ID cannot name a different RPC after close");
    assert_eq!(reused_request_id.code(), tonic::Code::InvalidArgument);

    for (name, request) in [
        (
            "locking",
            InvokeRpcRequest {
                request_id: "closed-locking-rpc".to_owned(),
                is_transactional: false,
                lock_attribute_keys: vec!["status".to_owned()],
                ..writing_rpc.clone()
            },
        ),
        (
            "transactional",
            InvokeRpcRequest {
                request_id: "closed-transactional-rpc".to_owned(),
                ..writing_rpc.clone()
            },
        ),
    ] {
        let status = rpc_without_worker_call(&mut flow_client, &mut worker, request)
            .await
            .expect_err("a closed run rejects a new RPC");
        assert_eq!(status.code(), tonic::Code::NotFound, "{name}: {status}");
        assert_eq!(
            service_error(&status).sub_status,
            ErrorSubStatus::FlowNotExists as i32,
            "{name}: {status}"
        );
    }

    let mut reading_client = flow_client.clone();
    let reading_rpc = tokio::spawn(async move {
        reading_client
            .invoke_rpc(InvokeRpcRequest {
                flow_id: FLOW_ID.to_owned(),
                run_id: started.run_id.clone(),
                rpc_name: "getOrder".to_owned(),
                request_id: "closed-reading-rpc".to_owned(),
                ..InvokeRpcRequest::default()
            })
            .await
    });
    let reading_task = worker.next_task().await;
    assert!(matches!(
        reading_task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    worker.reply(
        reading_task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse {
            output: Some(string_value("retained")),
            ..InvokeWorkerRpcResponse::default()
        }),
    );
    let reading_response = reading_rpc
        .await
        .expect("the plain RPC task finishes")
        .expect("a plain RPC reads the retained run")
        .into_inner();
    assert_eq!(reading_response.output, Some(string_value("retained")));
    worker.stop().await;
    server.shutdown().await;
}

async fn rpc_without_worker_call(
    flow_client: &mut dexzd_protocol::dex::flow_service_client::FlowServiceClient<Channel>,
    worker: &mut ScriptedWorker,
    request: InvokeRpcRequest,
) -> Result<tonic::Response<dexzd_protocol::dex::InvokeRpcResponse>, tonic::Status> {
    tokio::time::timeout(Duration::from_secs(10), async {
        tokio::select! {
            result = flow_client.invoke_rpc(request) => result,
            task = worker.next_task() => panic!("RPC reached the Worker: {task:?}"),
        }
    })
    .await
    .expect("InvokeRPC answers before the Worker is called")
}
