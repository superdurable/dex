// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::Arc;
use std::sync::atomic::{AtomicU8, Ordering};

use dex_protocol::dex::WorkerErrorResponse;
use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::worker_service_server::{WorkerService, WorkerServiceServer};
use dex_protocol::dex::{
    InvokeExecuteMethodRequest, InvokeExecuteMethodResponse, InvokeWaitForMethodRequest,
    InvokeWaitForMethodResponse, InvokeWorkerRpcRequest, InvokeWorkerRpcResponse,
    SyncAttributeIndexRequest,
};
use prost::Message;
use prost_types::Any;
use tokio::runtime::Runtime;
use tokio::sync::watch;
use tonic::transport::{Channel, Endpoint, Server};
use tonic::{Code, Request, Response, Status};

use crate::value_hydrator::ValueHydrator;
use crate::worker_dispatcher::WorkerDispatcher;
use crate::{BlobCache, HandlerError, Registry, SdkError, SdkResult, WorkerOptions, WorkerTarget};

const CREATED: u8 = 0;
const RUNNING: u8 = 1;
const STOPPED: u8 = 2;

pub struct Worker {
    runtime: Runtime,
    service: RustWorkerService,
    flow_service: FlowServiceClient<Channel>,
    attribute_indexes: HashMap<String, i32>,
    attribute_index_sync_timeout: std::time::Duration,
    bind_address: SocketAddr,
    worker_target: WorkerTarget,
    shutdown: watch::Sender<bool>,
    state: AtomicU8,
}

impl Worker {
    pub fn new(registry: Registry, blob_cache: Arc<BlobCache>, options: WorkerOptions) -> Self {
        Self::try_new(registry, blob_cache, options)
            .unwrap_or_else(|error| panic!("cannot create Rust Worker: {error}"))
    }

    pub fn try_new(
        registry: Registry,
        blob_cache: Arc<BlobCache>,
        options: WorkerOptions,
    ) -> SdkResult<Self> {
        let runtime = Runtime::new().map_err(service_error)?;
        if options.attribute_index_sync_timeout_value().is_zero() {
            return Err(SdkError::FlowDefinition {
                message: "attribute index sync timeout must be positive".to_string(),
            });
        }
        let bind_address = options
            .bind_address_value()
            .parse::<SocketAddr>()
            .map_err(|error| SdkError::FlowDefinition {
                message: format!("invalid Worker bind address: {error}"),
            })?;
        let worker_target = options.worker_target_value().cloned().unwrap_or_else(|| {
            let host = if bind_address.ip().is_unspecified() {
                "127.0.0.1".to_string()
            } else {
                bind_address.ip().to_string()
            };
            WorkerTarget::new(format!("{host}:{}", bind_address.port()))
        });
        let endpoint = Endpoint::from_shared(endpoint_address(options.server_address_value()))
            .map_err(service_error)?;
        let flow_service = {
            let _runtime_guard = runtime.enter();
            FlowServiceClient::new(endpoint.connect_lazy())
        };
        let attribute_indexes = registry.attribute_indexes().clone();
        let hydrator = ValueHydrator::new(flow_service.clone(), blob_cache);
        let dispatcher = WorkerDispatcher::new(registry, hydrator);
        let (shutdown, _) = watch::channel(false);
        Ok(Self {
            runtime,
            service: RustWorkerService { dispatcher },
            flow_service,
            attribute_indexes,
            attribute_index_sync_timeout: options.attribute_index_sync_timeout_value(),
            bind_address,
            worker_target,
            shutdown,
            state: AtomicU8::new(CREATED),
        })
    }

    pub fn worker_target(&self) -> &WorkerTarget {
        &self.worker_target
    }

    pub fn start(&self) -> SdkResult<()> {
        self.state
            .compare_exchange(CREATED, RUNNING, Ordering::AcqRel, Ordering::Acquire)
            .map_err(|_| SdkError::FlowDefinition {
                message: "Worker can only be started once".to_string(),
            })?;
        let mut shutdown = self.shutdown.subscribe();
        let mut flow_service = self.flow_service.clone();
        let sync_result = self.runtime.block_on(async {
            tokio::time::timeout(
                self.attribute_index_sync_timeout,
                flow_service.sync_attribute_indexes(SyncAttributeIndexRequest {
                    attribute_indexes: self.attribute_indexes.clone(),
                }),
            )
            .await
        });
        match sync_result {
            Ok(Ok(_)) => {}
            Ok(Err(status)) => {
                self.state.store(STOPPED, Ordering::Release);
                return Err(service_error(status));
            }
            Err(elapsed) => {
                self.state.store(STOPPED, Ordering::Release);
                return Err(service_error(elapsed));
            }
        }
        let result = self.runtime.block_on(
            Server::builder()
                .add_service(WorkerServiceServer::new(self.service.clone()))
                .serve_with_shutdown(self.bind_address, async move {
                    while !*shutdown.borrow() && shutdown.changed().await.is_ok() {}
                }),
        );
        self.state.store(STOPPED, Ordering::Release);
        result.map_err(service_error)
    }

    pub fn stop(&self) {
        let _ = self.shutdown.send(true);
    }
}

#[derive(Clone)]
struct RustWorkerService {
    dispatcher: WorkerDispatcher,
}

#[tonic::async_trait]
impl WorkerService for RustWorkerService {
    async fn invoke_wait_for_method(
        &self,
        request: Request<InvokeWaitForMethodRequest>,
    ) -> Result<Response<InvokeWaitForMethodResponse>, Status> {
        self.dispatcher
            .invoke_wait_for(request.into_inner())
            .await
            .map(Response::new)
            .map_err(worker_status)
    }

    async fn invoke_execute_method(
        &self,
        request: Request<InvokeExecuteMethodRequest>,
    ) -> Result<Response<InvokeExecuteMethodResponse>, Status> {
        self.dispatcher
            .invoke_execute(request.into_inner())
            .await
            .map(Response::new)
            .map_err(worker_status)
    }

    async fn invoke_worker_rpc(
        &self,
        request: Request<InvokeWorkerRpcRequest>,
    ) -> Result<Response<InvokeWorkerRpcResponse>, Status> {
        self.dispatcher
            .invoke_rpc(request.into_inner())
            .await
            .map(Response::new)
            .map_err(worker_status)
    }
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

fn worker_status(error: HandlerError) -> Status {
    let message = error.to_string();
    let detail = WorkerErrorResponse {
        detail: message.clone(),
        error_type: error.error_type().to_string(),
        stack_trace: String::new(),
    };
    let status = GoogleRpcStatus {
        code: Code::Unknown as i32,
        message: message.clone(),
        details: vec![Any {
            type_url: "type.googleapis.com/dex.WorkerErrorResponse".to_string(),
            value: detail.encode_to_vec(),
        }],
    };
    Status::with_details(Code::Unknown, message, status.encode_to_vec().into())
}

fn endpoint_address(address: &str) -> String {
    if address.contains("://") {
        address.to_string()
    } else {
        format!("http://{address}")
    }
}

fn service_error(error: impl std::fmt::Display) -> SdkError {
    SdkError::Service {
        service: crate::ServiceError::local("worker", error.to_string()),
    }
}
