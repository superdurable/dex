// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use dex_core::{
    CORE_PROTOCOL_VERSION, FlowSpec, InvocationFailure, InvocationKind, InvocationResult, Registry,
    RpcSpec, StepSpec, WorkerConfig, WorkerCore,
};
use dex_protocol::dex::worker_service_server::WorkerService as WorkerServiceApi;
use dex_protocol::dex::{
    InvokeWaitForMethodRequest, InvokeWaitForMethodResponse, WorkerErrorResponse,
};
use dex_runtime::WorkerService;
use prost::Message;
use prost_types::Any;
use tonic::{Code, Request};

#[tokio::test]
async fn validates_registry_and_routes_protobuf_activations() {
    let registry = Registry::new(vec![FlowSpec::new(
        "Orders",
        vec![StepSpec::starting("Validate")],
        vec![RpcSpec::new("Get")],
        vec![],
    )])
    .expect("Registry");
    let core = WorkerCore::new(WorkerConfig::new(4).expect("Worker config"));
    let service = WorkerService::new(registry, core.clone());

    let missing = service
        .invoke_wait_for_method(Request::new(InvokeWaitForMethodRequest {
            flow_type: "Missing".into(),
            step_type: "Validate".into(),
            ..Default::default()
        }))
        .await
        .expect_err("missing Flow");
    assert_eq!(missing.code(), Code::NotFound);

    let dispatch = tokio::spawn(async move {
        service
            .invoke_wait_for_method(Request::new(InvokeWaitForMethodRequest {
                flow_type: "Orders".into(),
                step_type: "Validate".into(),
                ..Default::default()
            }))
            .await
    });
    let invocation = core.poll_invocation().await.expect("activation");
    assert_eq!(invocation.kind(), InvocationKind::WaitFor);
    let decoded = InvokeWaitForMethodRequest::decode(invocation.request()).expect("request");
    assert_eq!(decoded.flow_type, "Orders");

    core.complete_invocation(
        CORE_PROTOCOL_VERSION,
        invocation.id(),
        InvocationResult::Success(InvokeWaitForMethodResponse::default().encode_to_vec()),
    )
    .expect("completion");
    dispatch.await.expect("join").expect("response");
}

#[tokio::test]
async fn preserves_language_failure_details_for_the_server() {
    let registry = Registry::new(vec![FlowSpec::new(
        "Orders",
        vec![StepSpec::starting("Validate")],
        vec![],
        vec![],
    )])
    .expect("Registry");
    let core = WorkerCore::new(WorkerConfig::new(1).expect("Worker config"));
    let service = WorkerService::new(registry, core.clone());
    let dispatch = tokio::spawn(async move {
        service
            .invoke_wait_for_method(Request::new(InvokeWaitForMethodRequest {
                flow_type: "Orders".into(),
                step_type: "Validate".into(),
                ..Default::default()
            }))
            .await
    });
    let invocation = core.poll_invocation().await.expect("activation");
    core.complete_invocation(
        CORE_PROTOCOL_VERSION,
        invocation.id(),
        InvocationResult::Failure(InvocationFailure::new(
            "java.lang.IllegalStateException",
            "wait failure",
            vec![],
        )),
    )
    .expect("completion");

    let failure = dispatch.await.expect("join").expect_err("failure");
    assert_eq!(failure.code(), Code::Unknown);
    let status = GoogleRpcStatus::decode(failure.details()).expect("status details");
    let detail = status.details.first().expect("Worker error detail");
    assert_eq!(
        detail.type_url,
        "type.googleapis.com/dex.WorkerErrorResponse"
    );
    let worker_error = WorkerErrorResponse::decode(detail.value.as_slice()).expect("Worker error");
    assert_eq!(worker_error.detail, "wait failure");
    assert_eq!(worker_error.error_type, "java.lang.IllegalStateException");
}

#[derive(Clone, PartialEq, Message)]
struct GoogleRpcStatus {
    #[prost(int32, tag = "1")]
    code: i32,
    #[prost(string, tag = "2")]
    message: String,
    #[prost(message, repeated, tag = "3")]
    details: Vec<Any>,
}
