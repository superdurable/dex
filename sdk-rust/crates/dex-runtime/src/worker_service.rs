// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::error::Error;
use std::fmt::{Display, Formatter};

use dex_core::{InvocationFailure, InvocationKind, InvocationResult, Registry, WorkerCore};
use dex_protocol::dex::worker_service_server::WorkerService as WorkerServiceApi;
use dex_protocol::dex::{
    InvokeExecuteMethodRequest, InvokeExecuteMethodResponse, InvokeWaitForMethodRequest,
    InvokeWaitForMethodResponse, InvokeWorkerRpcRequest, InvokeWorkerRpcResponse,
    WorkerErrorResponse,
};
use prost::Message;
use prost_types::Any;
use tonic::{Code, Request, Response, Status};

/// gRPC WorkerService backed by the language-neutral activation queue.
#[derive(Clone)]
pub struct WorkerService {
    registry: Registry,
    core: WorkerCore,
}

/// Worker response decoding failure.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WorkerServiceError(String);

impl WorkerService {
    /// Creates a WorkerService from immutable shared dependencies.
    pub fn new(registry: Registry, core: WorkerCore) -> Self {
        Self { registry, core }
    }

    /// Returns the Core used by language bridges.
    pub fn core(&self) -> &WorkerCore {
        &self.core
    }

    async fn invoke<RequestMessage, ResponseMessage>(
        &self,
        kind: InvocationKind,
        request: RequestMessage,
    ) -> Result<Response<ResponseMessage>, Status>
    where
        RequestMessage: Message,
        ResponseMessage: Message + Default,
    {
        let result = self
            .core
            .dispatch(kind, request.encode_to_vec())
            .await
            .map_err(|error| Status::unavailable(error.to_string()))?;
        match result {
            InvocationResult::Success(payload) => ResponseMessage::decode(payload.as_slice())
                .map(Response::new)
                .map_err(|error| Status::internal(format!("invalid bridge response: {error}"))),
            InvocationResult::Failure(failure) => Err(language_failure(failure)),
        }
    }

    fn validate_step(&self, flow_type: &str, step_type: &str) -> Result<(), Status> {
        let flow = self
            .registry
            .flow(flow_type)
            .ok_or_else(|| Status::not_found(format!("Flow {flow_type:?} is not registered")))?;
        if flow.step(step_type).is_none() {
            return Err(Status::not_found(format!(
                "Step {step_type:?} is not registered in Flow {flow_type:?}"
            )));
        }
        Ok(())
    }

    fn validate_rpc(&self, flow_type: &str, rpc_name: &str) -> Result<(), Status> {
        let flow = self
            .registry
            .flow(flow_type)
            .ok_or_else(|| Status::not_found(format!("Flow {flow_type:?} is not registered")))?;
        if flow.rpc(rpc_name).is_none() {
            return Err(Status::not_found(format!(
                "RPC {rpc_name:?} is not registered in Flow {flow_type:?}"
            )));
        }
        Ok(())
    }
}

#[tonic::async_trait]
impl WorkerServiceApi for WorkerService {
    async fn invoke_wait_for_method(
        &self,
        request: Request<InvokeWaitForMethodRequest>,
    ) -> Result<Response<InvokeWaitForMethodResponse>, Status> {
        let request = request.into_inner();
        self.validate_step(&request.flow_type, &request.step_type)?;
        self.invoke(InvocationKind::WaitFor, request).await
    }

    async fn invoke_execute_method(
        &self,
        request: Request<InvokeExecuteMethodRequest>,
    ) -> Result<Response<InvokeExecuteMethodResponse>, Status> {
        let request = request.into_inner();
        self.validate_step(&request.flow_type, &request.step_type)?;
        self.invoke(InvocationKind::Execute, request).await
    }

    async fn invoke_worker_rpc(
        &self,
        request: Request<InvokeWorkerRpcRequest>,
    ) -> Result<Response<InvokeWorkerRpcResponse>, Status> {
        let request = request.into_inner();
        self.validate_rpc(&request.flow_type, &request.rpc_name)?;
        self.invoke(InvocationKind::WorkerRpc, request).await
    }
}

impl Display for WorkerServiceError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.0)
    }
}

impl Error for WorkerServiceError {}

fn language_failure(failure: InvocationFailure) -> Status {
    let message = failure.message().to_string();
    let worker_error = WorkerErrorResponse {
        detail: message.clone(),
        error_type: failure.error_type().to_string(),
    };
    let details = GoogleRpcStatus {
        code: Code::Unknown as i32,
        message: message.clone(),
        details: vec![Any {
            type_url: "type.googleapis.com/dex.WorkerErrorResponse".to_string(),
            value: worker_error.encode_to_vec(),
        }],
    };
    Status::with_details(Code::Unknown, message, details.encode_to_vec().into())
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
