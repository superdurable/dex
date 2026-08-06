// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use dex_core::{
    CORE_PROTOCOL_VERSION, FlowSpec, InvocationKind, InvocationResult, Registry, RpcSpec, StepSpec,
    WorkerConfig, WorkerCore,
};
use dex_protocol::dex::worker_service_server::WorkerService as WorkerServiceApi;
use dex_protocol::dex::{InvokeWaitForMethodRequest, InvokeWaitForMethodResponse};
use dex_runtime::WorkerService;
use prost::Message;
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
