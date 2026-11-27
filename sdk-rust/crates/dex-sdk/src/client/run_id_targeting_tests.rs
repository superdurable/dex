// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Client calls that target one run send its run ID, and send an empty run ID by default.

use std::convert::Infallible;
use std::marker::PhantomData;
use std::sync::{Arc, LazyLock, Mutex};
use std::task::Poll;

use dex_protocol::dex::{
    FlowResult as ProtoFlowResult, GetAttributesRequest, GetAttributesResponse,
    GetFlowSummaryRequest, GetFlowSummaryResponse, InvokeRpcRequest, InvokeRpcResponse, ServerInfo,
    StopFlowRequest, WaitForFlowRequest,
};
use tokio::net::TcpListener;
use tokio::sync::oneshot;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::codegen::{BoxFuture, Service, StdError, http};
use tonic::server::{Grpc, NamedService, UnaryService};
use tonic::transport::Server;
use tonic::{Request, Response, Status};

use crate::server_protocol::MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION;
use crate::{
    Attribute, BlobCache, BlobCacheConfig, Client, ClientOptions, Context, Flow, HandlerResult,
    PersistenceSchema, Registry, Rpc, RpcInvokeOptions, RpcList, RpcResult, SdkError,
    StopFlowOptions, WaitForFlowOptions,
};

const TRACKED_RUN_ID: &str = "0b6f3c52-4f1e-4d3a-9c1f-2a7e5d8b9c10";

static SHIPMENT_OUTCOME: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("shipment-outcome"));

struct ShipmentFlow;

impl ShipmentFlow {
    const GET_SHIPMENT_OUTCOME: Rpc<(), String> = Rpc::new("get_shipment_outcome");

    fn get_shipment_outcome(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new(
            SHIPMENT_OUTCOME.get(context)?.unwrap_or_default(),
        ))
    }
}

impl Flow for ShipmentFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&SHIPMENT_OUTCOME)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(Self::GET_SHIPMENT_OUTCOME, Self::get_shipment_outcome)
    }
}

#[derive(Debug, Eq, PartialEq)]
struct RecordedRunId {
    path: &'static str,
    run_id: String,
}

type RecordedRunIds = Arc<Mutex<Vec<RecordedRunId>>>;

#[derive(Clone)]
struct RunIdRecordingFlowService {
    recorded: RecordedRunIds,
}

impl NamedService for RunIdRecordingFlowService {
    const NAME: &'static str = "dex.FlowService";
}

impl<B> Service<http::Request<B>> for RunIdRecordingFlowService
where
    B: tonic::codegen::Body + Send + 'static,
    B::Error: Into<StdError> + Send + 'static,
{
    type Response = http::Response<tonic::body::Body>;
    type Error = Infallible;
    type Future = BoxFuture<Self::Response, Self::Error>;

    fn poll_ready(&mut self, _: &mut std::task::Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: http::Request<B>) -> Self::Future {
        let recorded = Arc::clone(&self.recorded);
        Box::pin(async move {
            let response = match request.uri().path() {
                "/dex.FlowService/GetServerInfo" => {
                    Grpc::new(tonic_prost::ProstCodec::default())
                        .unary(ServerInfoReply, request)
                        .await
                }
                "/dex.FlowService/GetFlowSummary" => {
                    record_run_id::<_, GetFlowSummaryRequest, GetFlowSummaryResponse>(
                        request,
                        recorded,
                        "GetFlowSummary",
                        |request| request.run_id.clone(),
                    )
                    .await
                }
                "/dex.FlowService/GetAttributes" => {
                    record_run_id::<_, GetAttributesRequest, GetAttributesResponse>(
                        request,
                        recorded,
                        "GetAttributes",
                        |request| request.run_id.clone(),
                    )
                    .await
                }
                "/dex.FlowService/WaitForFlow" => {
                    record_run_id::<_, WaitForFlowRequest, ProtoFlowResult>(
                        request,
                        recorded,
                        "WaitForFlow",
                        |request| request.run_id.clone(),
                    )
                    .await
                }
                "/dex.FlowService/StopFlow" => {
                    record_run_id::<_, StopFlowRequest, ()>(
                        request,
                        recorded,
                        "StopFlow",
                        |request| request.run_id.clone(),
                    )
                    .await
                }
                "/dex.FlowService/InvokeRPC" => {
                    record_run_id::<_, InvokeRpcRequest, InvokeRpcResponse>(
                        request,
                        recorded,
                        "InvokeRPC",
                        |request| request.run_id.clone(),
                    )
                    .await
                }
                _ => Status::unimplemented("fake FlowService").into_http(),
            };
            Ok(response)
        })
    }
}

async fn record_run_id<B, Req, Resp>(
    request: http::Request<B>,
    recorded: RecordedRunIds,
    path: &'static str,
    run_id_of: fn(&Req) -> String,
) -> http::Response<tonic::body::Body>
where
    B: tonic::codegen::Body + Send + 'static,
    B::Error: Into<StdError> + Send + 'static,
    Req: prost::Message + Default + Send + 'static,
    Resp: prost::Message + Default + Send + 'static,
{
    let service = RecordRunIdThenFail::<Req, Resp> {
        recorded,
        path,
        run_id_of,
        response: PhantomData,
    };
    Grpc::new(tonic_prost::ProstCodec::default())
        .unary(service, request)
        .await
}

struct RecordRunIdThenFail<Req, Resp> {
    recorded: RecordedRunIds,
    path: &'static str,
    run_id_of: fn(&Req) -> String,
    response: PhantomData<fn() -> Resp>,
}

impl<Req: Send + 'static, Resp: Send + 'static> UnaryService<Req>
    for RecordRunIdThenFail<Req, Resp>
{
    type Response = Resp;
    type Future = BoxFuture<Response<Resp>, Status>;

    fn call(&mut self, request: Request<Req>) -> Self::Future {
        self.recorded
            .lock()
            .expect("recorded run IDs lock")
            .push(RecordedRunId {
                path: self.path,
                run_id: (self.run_id_of)(request.get_ref()),
            });
        Box::pin(async {
            Err(Status::failed_precondition(
                "fake FlowService records run IDs only",
            ))
        })
    }
}

struct ServerInfoReply;

impl UnaryService<()> for ServerInfoReply {
    type Response = ServerInfo;
    type Future = BoxFuture<Response<Self::Response>, Status>;

    fn call(&mut self, _: Request<()>) -> Self::Future {
        Box::pin(async {
            Ok(Response::new(ServerInfo {
                server_version: "fake".to_string(),
                minimum_supported_protocol_version: 1,
                current_protocol_version: MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION,
                server_engine: 0,
            }))
        })
    }
}

struct RunIdRecordingServer {
    runtime: tokio::runtime::Runtime,
    address: String,
    recorded: RecordedRunIds,
    shutdown: oneshot::Sender<()>,
    server: tokio::task::JoinHandle<Result<(), tonic::transport::Error>>,
}

impl RunIdRecordingServer {
    fn start() -> Self {
        let runtime = tokio::runtime::Runtime::new().expect("server runtime");
        let listener = runtime
            .block_on(TcpListener::bind("127.0.0.1:0"))
            .expect("bind fake FlowService");
        let address = listener
            .local_addr()
            .expect("fake server address")
            .to_string();
        let recorded = RecordedRunIds::default();
        let (shutdown, stopped) = oneshot::channel::<()>();
        let server = runtime.spawn(
            Server::builder()
                .add_service(RunIdRecordingFlowService {
                    recorded: Arc::clone(&recorded),
                })
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async {
                    stopped.await.expect("FlowService shutdown signal");
                }),
        );
        Self {
            runtime,
            address,
            recorded,
            shutdown,
            server,
        }
    }

    fn client(&self, cache_directory: &tempfile::TempDir) -> Client {
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 1024 * 1024, 0)
                    .expect("blob cache config"),
            )
            .expect("blob cache"),
        );
        Client::try_new(
            Registry::new()
                .register(ShipmentFlow)
                .expect("register shipment Flow"),
            cache,
            ClientOptions::new().server_address(&self.address),
        )
        .expect("Client")
    }

    fn recorded_run_ids(&self) -> Vec<RecordedRunId> {
        std::mem::take(&mut *self.recorded.lock().expect("recorded run IDs lock"))
    }

    fn finish(self) {
        self.shutdown.send(()).expect("stop fake FlowService");
        self.runtime
            .block_on(self.server)
            .expect("fake FlowService task")
            .expect("fake FlowService");
    }
}

fn expected_run_ids(run_id: &str) -> Vec<RecordedRunId> {
    [
        "GetFlowSummary",
        "GetAttributes",
        "WaitForFlow",
        "StopFlow",
        "InvokeRPC",
    ]
    .into_iter()
    .map(|path| RecordedRunId {
        path,
        run_id: run_id.to_string(),
    })
    .collect()
}

#[test]
fn calls_without_a_run_id_send_an_empty_run_id() {
    let server = RunIdRecordingServer::start();
    let cache_directory = tempfile::tempdir().expect("blob cache directory");
    let client = server.client(&cache_directory);

    client
        .describe_flow("shipment-42")
        .expect_err("fake GetFlowSummary fails");
    client
        .get_attributes("shipment-42", (&SHIPMENT_OUTCOME,))
        .expect_err("fake GetAttributes fails");
    client
        .wait_for_flow_with_options("shipment-42", WaitForFlowOptions::new())
        .expect_err("fake WaitForFlow fails");
    client
        .stop_flow("shipment-42", StopFlowOptions::cancel())
        .expect_err("fake StopFlow fails");
    client
        .invoke_rpc_without_input("shipment-42", ShipmentFlow::GET_SHIPMENT_OUTCOME)
        .expect_err("fake InvokeRPC fails");

    assert_eq!(expected_run_ids(""), server.recorded_run_ids());
    server.finish();
}

#[test]
fn calls_with_a_run_id_send_that_run_id() {
    let server = RunIdRecordingServer::start();
    let cache_directory = tempfile::tempdir().expect("blob cache directory");
    let client = server.client(&cache_directory);

    client
        .describe_flow_run("shipment-42", TRACKED_RUN_ID)
        .expect_err("fake GetFlowSummary fails");
    client
        .get_flow_run_attributes("shipment-42", TRACKED_RUN_ID, (&SHIPMENT_OUTCOME,))
        .expect_err("fake GetAttributes fails");
    client
        .wait_for_flow_with_options(
            "shipment-42",
            WaitForFlowOptions::new().run_id(TRACKED_RUN_ID),
        )
        .expect_err("fake WaitForFlow fails");
    client
        .stop_flow(
            "shipment-42",
            StopFlowOptions::cancel().run_id(TRACKED_RUN_ID),
        )
        .expect_err("fake StopFlow fails");
    client
        .invoke_rpc_without_input_with_options(
            "shipment-42",
            ShipmentFlow::GET_SHIPMENT_OUTCOME,
            RpcInvokeOptions::new().run_id(TRACKED_RUN_ID),
        )
        .expect_err("fake InvokeRPC fails");

    assert_eq!(expected_run_ids(TRACKED_RUN_ID), server.recorded_run_ids());
    server.finish();
}

#[test]
fn run_reads_reject_a_blank_run_id_before_contacting_dex() {
    let server = RunIdRecordingServer::start();
    let cache_directory = tempfile::tempdir().expect("blob cache directory");
    let client = server.client(&cache_directory);

    let describe = client.describe_flow_run("shipment-42", " ").map(|_| ());
    let attributes = client
        .get_flow_run_attributes("shipment-42", "", (&SHIPMENT_OUTCOME,))
        .map(|_| ());
    for result in [describe, attributes] {
        let Err(SdkError::InvalidArgument { message }) = result else {
            panic!("expected a local InvalidArgument, got {result:?}");
        };
        assert!(message.contains("requires a non-blank run ID"));
    }

    assert!(server.recorded_run_ids().is_empty());
    server.finish();
}
