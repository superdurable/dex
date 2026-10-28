// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    ChannelCondition, InvokeRpcRequest, InvokeWaitForMethodResponse, InvokeWorkerRpcResponse,
    StepOptions, WaitingCondition, WaitingConditionType, WorkerErrorResponse, poll_worker_task,
    poll_worker_task_frame,
};
use tonic::transport::Channel;

use crate::harness::{DevServer, ScriptedWorker, service_error, start_pull_flow, wait_for_result};
use crate::string_value;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn concurrent_rpc_request_ids_share_one_worker_result() {
    for has_locks in [false, true] {
        let (server, mut worker, request) = start_rpc_flow("shared-rpc-result", has_locks).await;
        let mut first_client = server.flow_client();
        let first_request = request.clone();
        let first = tokio::spawn(async move { first_client.invoke_rpc(first_request).await });
        let task = worker.next_task().await;
        assert!(matches!(
            task.request,
            Some(poll_worker_task::Request::Rpc(_))
        ));

        let mut second_client = server.flow_client();
        let second_request = request.clone();
        let second = tokio::spawn(async move { second_client.invoke_rpc(second_request).await });
        assert_duplicate_stays_pending(&mut server.flow_client(), &mut worker, &request).await;
        worker.reply(
            task.task_id,
            poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse {
                output: Some(string_value("first result")),
                ..InvokeWorkerRpcResponse::default()
            }),
        );
        let first_response = without_worker_call(&mut worker, first)
            .await
            .expect("first caller joins")
            .expect("first RPC succeeds")
            .into_inner();
        let second_response = without_worker_call(&mut worker, second)
            .await
            .expect("second caller joins")
            .expect("second RPC succeeds")
            .into_inner();
        assert_eq!(first_response, second_response);
        assert_eq!(first_response.output, Some(string_value("first result")));
        let changed_request = InvokeRpcRequest {
            input: Some(string_value("changed")),
            ..request.clone()
        };
        let changed = without_worker_call(
            &mut worker,
            server.flow_client().invoke_rpc(changed_request),
        )
        .await
        .expect_err("reusing a request ID with changed input is rejected");
        assert_eq!(changed.code(), tonic::Code::InvalidArgument);
        let replay = without_worker_call(&mut worker, server.flow_client().invoke_rpc(request))
            .await
            .expect("completed request replays")
            .into_inner();
        assert_eq!(replay, first_response);
        worker.stop().await;
        server.shutdown().await;
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn first_rpc_caller_disconnect_preserves_the_worker_call() {
    let (server, mut worker, request) = start_rpc_flow("disconnected-rpc", false).await;
    let mut first_client = server.flow_client();
    let first_request = request.clone();
    let first = tokio::spawn(async move { first_client.invoke_rpc(first_request).await });
    let task = worker.next_task().await;
    let changed = without_worker_call(
        &mut worker,
        server.flow_client().invoke_rpc(InvokeRpcRequest {
            input: Some(string_value("changed while in flight")),
            ..request.clone()
        }),
    )
    .await
    .expect_err("a pending request ID cannot be reused with changed input");
    assert_eq!(changed.code(), tonic::Code::InvalidArgument);
    first.abort();
    assert!(
        first
            .await
            .expect_err("first caller was cancelled")
            .is_cancelled()
    );

    assert_duplicate_stays_pending(&mut server.flow_client(), &mut worker, &request).await;
    worker.reply(
        task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse {
            output: Some(string_value("survived disconnect")),
            ..InvokeWorkerRpcResponse::default()
        }),
    );
    let response = without_worker_call(&mut worker, server.flow_client().invoke_rpc(request))
        .await
        .expect("retry observes the original result")
        .into_inner();
    assert_eq!(response.output, Some(string_value("survived disconnect")));
    worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn rpc_worker_deadline_is_shared_with_concurrent_duplicates() {
    let (server, mut worker, mut request) = start_rpc_flow("shared-rpc-deadline", false).await;
    request.timeout_seconds = 2;
    let mut first_client = server.flow_client();
    let first_request = request.clone();
    let first = tokio::spawn(async move { first_client.invoke_rpc(first_request).await });
    let task = worker.next_task().await;
    assert!(matches!(
        task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    let mut second_client = server.flow_client();
    let second = second_client.invoke_rpc(request);
    let (first, second) =
        without_worker_call(&mut worker, async { tokio::join!(first, second) }).await;
    let first = first
        .expect("first caller joins")
        .expect_err("Worker deadline expires");
    let second = second.expect_err("duplicate shares the deadline");
    assert_eq!(first.code(), tonic::Code::DeadlineExceeded);
    assert_eq!(first.code(), second.code());
    assert_eq!(first.message(), second.message());
    assert_eq!(service_error(&first), service_error(&second));
    worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn rpc_worker_error_is_shared_but_a_later_retry_invokes_the_worker() {
    let (server, mut worker, request) = start_rpc_flow("shared-rpc-error", false).await;
    let mut first_client = server.flow_client();
    let first_request = request.clone();
    let first = tokio::spawn(async move { first_client.invoke_rpc(first_request).await });
    let task = worker.next_task().await;
    let mut second_client = server.flow_client();
    let second_request = request.clone();
    let second = tokio::spawn(async move { second_client.invoke_rpc(second_request).await });
    assert_duplicate_stays_pending(&mut server.flow_client(), &mut worker, &request).await;
    let worker_error = WorkerErrorResponse {
        error_type: "OrderRejected".to_owned(),
        detail: "order cannot be updated".to_owned(),
        ..WorkerErrorResponse::default()
    };
    worker.reply(
        task.task_id,
        poll_worker_task_frame::Output::WorkerError(worker_error.clone()),
    );
    let first = without_worker_call(&mut worker, first)
        .await
        .expect("first caller joins")
        .expect_err("Worker rejects the RPC");
    let second = without_worker_call(&mut worker, second)
        .await
        .expect("second caller joins")
        .expect_err("duplicate shares the Worker error");
    assert_eq!(first.code(), second.code());
    assert_eq!(service_error(&first), service_error(&second));
    assert_eq!(
        service_error(&first).original_worker_error_type,
        worker_error.error_type
    );
    assert_eq!(
        service_error(&first).original_worker_error_detail,
        worker_error.detail
    );

    let mut retry_client = server.flow_client();
    let retry = tokio::spawn(async move { retry_client.invoke_rpc(request).await });
    let task = worker.next_task().await;
    worker.reply(
        task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse::default()),
    );
    retry
        .await
        .expect("retry caller joins")
        .expect("retry succeeds");
    worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn non_transactional_rpc_request_ids_invoke_the_worker_independently() {
    let (server, mut worker, mut request) = start_rpc_flow("plain-rpc-requests", false).await;
    request.is_transactional = false;
    let mut first_client = server.flow_client();
    let first_request = request.clone();
    let first = tokio::spawn(async move { first_client.invoke_rpc(first_request).await });
    let first_task = worker.next_task().await;
    let mut second_worker = ScriptedWorker::start(server.worker_client(), "RequestDedupFlow").await;
    let mut second_client = server.flow_client();
    let second = tokio::spawn(async move { second_client.invoke_rpc(request).await });
    let second_task = second_worker.next_task().await;
    worker.reply(
        first_task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse::default()),
    );
    second_worker.reply(
        second_task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse::default()),
    );
    first
        .await
        .expect("first caller joins")
        .expect("first RPC succeeds");
    second
        .await
        .expect("second caller joins")
        .expect("second RPC succeeds");
    worker.stop().await;
    second_worker.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn node_shutdown_ends_an_in_flight_rpc_without_waiting_for_its_deadline() {
    let (server, mut worker, mut request) = start_rpc_flow("shutdown-rpc", false).await;
    request.timeout_seconds = 3600;
    let mut client = server.flow_client();
    let caller = tokio::spawn(async move { client.invoke_rpc(request).await });
    worker.next_task().await;
    tokio::time::timeout(Duration::from_secs(10), server.shutdown())
        .await
        .expect("shutdown cancels the registry-owned task");
    let status = caller.await.expect("caller joins").expect_err("node stops");
    assert_eq!(status.code(), tonic::Code::Unavailable);
    worker.stop().await;
}

async fn start_rpc_flow(
    flow_id: &str,
    has_locks: bool,
) -> (DevServer, ScriptedWorker, InvokeRpcRequest) {
    let server = DevServer::start().await;
    let mut worker = ScriptedWorker::start(server.worker_client(), "RequestDedupFlow").await;
    let started = start_pull_flow(
        &mut server.flow_client(),
        flow_id,
        "RequestDedupFlow",
        "WaitStep",
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    let task = worker.next_task().await;
    assert!(matches!(
        task.request,
        Some(poll_worker_task::Request::WaitFor(_))
    ));
    worker.reply(
        task.task_id,
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
    let request = InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        run_id: started.run_id,
        rpc_name: "updateOrder".to_owned(),
        request_id: "shared-request".to_owned(),
        is_transactional: !has_locks,
        lock_attribute_keys: if has_locks {
            vec!["status".to_owned()]
        } else {
            Vec::new()
        },
        ..InvokeRpcRequest::default()
    };
    (server, worker, request)
}

async fn assert_duplicate_stays_pending(
    client: &mut FlowServiceClient<Channel>,
    worker: &mut ScriptedWorker,
    request: &InvokeRpcRequest,
) {
    let mut request = tonic::Request::new(request.clone());
    request.set_timeout(Duration::from_secs(1));
    let response = without_worker_call(worker, client.invoke_rpc(request)).await;
    let status = response.expect_err("duplicate waits until its own transport deadline");
    assert!(
        matches!(
            status.code(),
            tonic::Code::Cancelled | tonic::Code::DeadlineExceeded
        ),
        "{status}"
    );
}

async fn without_worker_call<T>(worker: &mut ScriptedWorker, future: impl Future<Output = T>) -> T {
    tokio::time::timeout(Duration::from_secs(10), async {
        tokio::select! {
            result = future => result,
            task = worker.next_task() => panic!("duplicate invoked the Worker: {task:?}"),
        }
    })
    .await
    .expect("call finishes while the Worker stays held")
}
