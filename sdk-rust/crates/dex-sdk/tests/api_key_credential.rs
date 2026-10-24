// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(feature = "api-key-credential")]

use std::collections::VecDeque;
use std::convert::Infallible;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use std::time::Duration;

use axum::Router;
use axum::extract::State;
use axum::http::StatusCode;
use axum::routing::post;
use dex_protocol::dex::poll_worker_task_response::Frame as PollResponseFrame;
use dex_protocol::dex::worker_task_service_server::{WorkerTaskService, WorkerTaskServiceServer};
use dex_protocol::dex::{
    HealthInfo, PollWorkerTaskRegistered, PollWorkerTaskRequest, PollWorkerTaskResponse,
    ServerInfo, SyncAttributeIndexRequest, SyncAttributeIndexResponse,
};
use dex_sdk::{
    ApiKeyCredential, BlobCache, BlobCacheConfig, Client, ClientOptions, PullWorkerOptions,
    Registry, Worker, WorkerOptions,
};
use tokio::net::TcpListener;
use tokio::sync::{Notify, mpsc};
use tokio_stream::wrappers::{ReceiverStream, TcpListenerStream};
use tonic::codegen::{BoxFuture, Service, http};
use tonic::server::{NamedService, UnaryService};
use tonic::transport::Server;
use tonic::{Request, Response, Status, Streaming};

const TOKEN_A: &str = "api-key-test-token-a";
const TOKEN_B: &str = "api-key-test-token-b";

struct TokenEndpoint {
    responses: Mutex<VecDeque<&'static str>>,
    count: Mutex<usize>,
    refresh_started: Notify,
    release_refresh: Option<Arc<Notify>>,
}

async fn exchange_token(State(endpoint): State<Arc<TokenEndpoint>>) -> (StatusCode, String) {
    let exchange_count = {
        let mut count = endpoint.count.lock().expect("exchange count lock");
        *count += 1;
        *count
    };
    if exchange_count == 2
        && let Some(release_refresh) = &endpoint.release_refresh
    {
        endpoint.refresh_started.notify_one();
        release_refresh.notified().await;
    }
    let token = endpoint
        .responses
        .lock()
        .expect("token response lock")
        .pop_front()
        .unwrap_or(TOKEN_B);
    (
        StatusCode::OK,
        format!(r#"{{"access_token":"{token}","token_type":"Bearer","expires_in":300}}"#),
    )
}

#[derive(Clone)]
struct AuthenticatedFlowService {
    headers: Arc<Mutex<Vec<String>>>,
    accepts_token_b: bool,
}

impl NamedService for AuthenticatedFlowService {
    const NAME: &'static str = "dex.FlowService";
}

impl<RequestBody> Service<http::Request<RequestBody>> for AuthenticatedFlowService
where
    RequestBody: tonic::codegen::Body + Send + 'static,
    RequestBody::Error: Into<tonic::codegen::StdError> + Send + 'static,
{
    type Response = http::Response<tonic::body::Body>;
    type Error = Infallible;
    type Future = BoxFuture<Self::Response, Self::Error>;

    fn poll_ready(&mut self, _: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: http::Request<RequestBody>) -> Self::Future {
        let authorization = request
            .headers()
            .get("authorization")
            .map(|header| header.to_str().expect("ASCII authorization header"))
            .unwrap_or_default()
            .to_string();
        self.headers
            .lock()
            .expect("header lock")
            .push(authorization.clone());
        let accepts_token_b = self.accepts_token_b;
        Box::pin(async move {
            if !accepts_token_b || authorization != format!("Bearer {TOKEN_B}") {
                return Ok(Status::unauthenticated("access token rejected").into_http());
            }
            let mut grpc = tonic::server::Grpc::new(tonic_prost::ProstCodec::default());
            Ok(grpc.unary(HealthCheck, request).await)
        })
    }
}

struct HealthCheck;

impl UnaryService<()> for HealthCheck {
    type Response = HealthInfo;
    type Future = BoxFuture<Response<Self::Response>, Status>;

    fn call(&mut self, _: Request<()>) -> Self::Future {
        Box::pin(async { Ok(Response::new(HealthInfo::default())) })
    }
}

#[derive(Clone)]
struct WorkerBootFlowService;

impl NamedService for WorkerBootFlowService {
    const NAME: &'static str = "dex.FlowService";
}

impl<RequestBody> Service<http::Request<RequestBody>> for WorkerBootFlowService
where
    RequestBody: tonic::codegen::Body + Send + 'static,
    RequestBody::Error: Into<tonic::codegen::StdError> + Send + 'static,
{
    type Response = http::Response<tonic::body::Body>;
    type Error = Infallible;
    type Future = BoxFuture<Self::Response, Self::Error>;

    fn poll_ready(&mut self, _: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: http::Request<RequestBody>) -> Self::Future {
        let is_server_info = request.uri().path() == "/dex.FlowService/GetServerInfo";
        Box::pin(async move {
            if is_server_info {
                let mut grpc = tonic::server::Grpc::new(tonic_prost::ProstCodec::default());
                Ok(grpc.unary(GetServerInfo, request).await)
            } else {
                let mut grpc = tonic::server::Grpc::new(tonic_prost::ProstCodec::default());
                Ok(grpc.unary(SyncIndexes, request).await)
            }
        })
    }
}

struct GetServerInfo;

impl UnaryService<()> for GetServerInfo {
    type Response = ServerInfo;
    type Future = BoxFuture<Response<Self::Response>, Status>;

    fn call(&mut self, _: Request<()>) -> Self::Future {
        Box::pin(async {
            Ok(Response::new(ServerInfo {
                server_version: "test".to_string(),
                minimum_supported_protocol_version: 1,
                current_protocol_version: 3,
                server_engine: 0,
            }))
        })
    }
}

struct SyncIndexes;

impl UnaryService<SyncAttributeIndexRequest> for SyncIndexes {
    type Response = SyncAttributeIndexResponse;
    type Future = BoxFuture<Response<Self::Response>, Status>;

    fn call(&mut self, _: Request<SyncAttributeIndexRequest>) -> Self::Future {
        Box::pin(async { Ok(Response::new(SyncAttributeIndexResponse {})) })
    }
}

#[derive(Clone)]
struct RejectedWorkerPollService {
    headers: Arc<Mutex<Vec<String>>>,
    accepts_token_b: bool,
}

#[tonic::async_trait]
impl WorkerTaskService for RejectedWorkerPollService {
    type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

    async fn poll_worker_task(
        &self,
        mut request: Request<Streaming<PollWorkerTaskRequest>>,
    ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
        let authorization = request
            .metadata()
            .get("authorization")
            .map(|value| value.to_str().expect("ASCII authorization header"))
            .unwrap_or_default()
            .to_string();
        self.headers
            .lock()
            .expect("worker headers lock")
            .push(authorization.clone());
        if !self.accepts_token_b || authorization != format!("Bearer {TOKEN_B}") {
            return Err(Status::unauthenticated("rejected access token"));
        }
        let first_frame = request
            .get_mut()
            .message()
            .await?
            .ok_or_else(|| Status::invalid_argument("missing register frame"))?;
        assert!(matches!(
            first_frame.frame,
            Some(dex_protocol::dex::poll_worker_task_request::Frame::Register(_))
        ));
        let (sender, receiver) = mpsc::channel(1);
        sender
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(PollResponseFrame::Registered(
                    PollWorkerTaskRegistered::default(),
                )),
            }))
            .await
            .map_err(|_| Status::internal("registration stream closed"))?;
        Ok(Response::new(ReceiverStream::new(receiver)))
    }
}

fn exercise_rejected_token_retry(accepts_token_b: bool, delayed_second_call: bool) {
    let runtime = tokio::runtime::Runtime::new().expect("server runtime");
    let (token_listener, grpc_listener) = runtime.block_on(async {
        let token_listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("token listener");
        let grpc_listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("gRPC listener");
        (token_listener, grpc_listener)
    });
    let token_url = format!(
        "http://{}/token",
        token_listener.local_addr().expect("token address")
    );
    let grpc_address = grpc_listener.local_addr().expect("gRPC address");
    let endpoint = Arc::new(TokenEndpoint {
        responses: Mutex::new(VecDeque::from([TOKEN_A, TOKEN_B])),
        count: Mutex::new(0),
        refresh_started: Notify::new(),
        release_refresh: delayed_second_call.then(|| Arc::new(Notify::new())),
    });
    let router = Router::new()
        .route("/token", post(exchange_token))
        .with_state(Arc::clone(&endpoint));
    let token_task = runtime.spawn(async move {
        axum::serve(token_listener, router)
            .await
            .expect("token server");
    });
    let headers = Arc::new(Mutex::new(Vec::new()));
    let grpc_task = runtime.spawn(
        Server::builder()
            .add_service(AuthenticatedFlowService {
                headers: Arc::clone(&headers),
                accepts_token_b,
            })
            .serve_with_incoming(TcpListenerStream::new(grpc_listener)),
    );
    let credential = Arc::new(
        runtime
            .block_on(
                ApiKeyCredential::builder(token_url, "server-1", "api-key-test-secret").build(),
            )
            .expect("initial token exchange"),
    );
    let directory = tempfile::tempdir().expect("cache directory");
    let blob_cache = Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(directory.path(), 1024 * 1024, 0).expect("cache config"),
        )
        .expect("blob cache"),
    );
    let client = Client::try_new(
        Registry::new(),
        blob_cache,
        ClientOptions::new()
            .server_address(grpc_address.to_string())
            .credential(credential.clone()),
    )
    .expect("Dex Client");
    let result = if delayed_second_call {
        let client = Arc::new(client);
        let first_client = Arc::clone(&client);
        let first = std::thread::spawn(move || first_client.health_check());
        runtime
            .block_on(async {
                tokio::time::timeout(Duration::from_secs(5), endpoint.refresh_started.notified())
                    .await
            })
            .expect("rejected-token exchange started");
        let second_client = Arc::clone(&client);
        let (second_result, second_outcome) = std::sync::mpsc::channel();
        let second = std::thread::spawn(move || {
            second_result
                .send(second_client.health_check().is_ok())
                .expect("send second call result");
        });
        assert!(matches!(
            second_outcome.recv_timeout(Duration::from_millis(100)),
            Err(std::sync::mpsc::RecvTimeoutError::Timeout)
        ));
        endpoint
            .release_refresh
            .as_ref()
            .expect("refresh gate")
            .notify_one();
        let first_result = first.join().expect("first Client call");
        second.join().expect("second Client call");
        assert!(second_outcome.recv().expect("second call succeeds"));
        first_result
    } else {
        client.health_check()
    };
    if accepts_token_b {
        result.expect("second attempt succeeds");
    } else {
        let error = result.expect_err("second attempt fails");
        assert_eq!(
            error.service_error().expect("service error").code(),
            tonic::Code::Unauthenticated
        );
    }
    let exchange_count = *endpoint.count.lock().expect("exchange count lock");
    if accepts_token_b {
        assert_eq!(exchange_count, 2);
    } else {
        assert!(exchange_count >= 2);
    }
    let mut expected_headers = vec![format!("Bearer {TOKEN_A}"), format!("Bearer {TOKEN_B}")];
    if delayed_second_call {
        expected_headers.push(format!("Bearer {TOKEN_B}"));
    }
    assert_eq!(*headers.lock().expect("header lock"), expected_headers);
    drop(credential);
    token_task.abort();
    grpc_task.abort();
}

#[test]
fn unary_client_retries_once_with_fresh_token() {
    exercise_rejected_token_retry(true, false);
}

#[test]
fn unary_call_started_during_refresh_shares_exchange() {
    exercise_rejected_token_retry(true, true);
}

#[test]
fn unary_client_stops_after_second_rejection() {
    exercise_rejected_token_retry(false, false);
}

fn exercise_worker_poll_rejection(accepts_token_b: bool, should_stop_on_rejection: bool) {
    let runtime = tokio::runtime::Runtime::new().expect("server runtime");
    let (token_listener, grpc_listener) = runtime.block_on(async {
        (
            TcpListener::bind("127.0.0.1:0")
                .await
                .expect("token listener"),
            TcpListener::bind("127.0.0.1:0")
                .await
                .expect("gRPC listener"),
        )
    });
    let token_url = format!(
        "http://{}/token",
        token_listener.local_addr().expect("token address")
    );
    let grpc_address = grpc_listener.local_addr().expect("gRPC address");
    let endpoint = Arc::new(TokenEndpoint {
        responses: Mutex::new(VecDeque::from([TOKEN_A, TOKEN_B])),
        count: Mutex::new(0),
        refresh_started: Notify::new(),
        release_refresh: accepts_token_b.then(|| Arc::new(Notify::new())),
    });
    let token_endpoint = Arc::clone(&endpoint);
    let token_task = runtime.spawn(async move {
        axum::serve(
            token_listener,
            Router::new()
                .route("/token", post(exchange_token))
                .with_state(token_endpoint),
        )
        .await
        .expect("token server");
    });
    let headers = Arc::new(Mutex::new(Vec::new()));
    let grpc_task = runtime.spawn(
        Server::builder()
            .add_service(WorkerBootFlowService)
            .add_service(WorkerTaskServiceServer::new(RejectedWorkerPollService {
                headers: Arc::clone(&headers),
                accepts_token_b,
            }))
            .serve_with_incoming(TcpListenerStream::new(grpc_listener)),
    );
    let credential = Arc::new(
        runtime
            .block_on(
                ApiKeyCredential::builder(token_url, "server-1", "api-key-test-secret").build(),
            )
            .expect("initial token exchange"),
    );
    let directory = tempfile::tempdir().expect("cache directory");
    let blob_cache = Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(directory.path(), 1024 * 1024, 0).expect("cache config"),
        )
        .expect("blob cache"),
    );
    let worker = Arc::new(
        Worker::try_new(
            Registry::new(),
            blob_cache,
            WorkerOptions::new()
                .server_address(grpc_address.to_string())
                .credential(credential)
                .pull(
                    PullWorkerOptions::new()
                        .app_worker_id("api-key-test-worker")
                        .stop_on_permanent_stream_rejection(should_stop_on_rejection),
                ),
        )
        .expect("pull Worker"),
    );
    let running = Arc::clone(&worker);
    let worker_thread = std::thread::spawn(move || running.start());
    if accepts_token_b {
        runtime
            .block_on(async {
                tokio::time::timeout(Duration::from_secs(5), endpoint.refresh_started.notified())
                    .await
            })
            .expect("reconnect exchange started");
        let early_readiness = runtime.block_on(async {
            tokio::time::timeout(Duration::from_millis(100), worker.wait_pull_registered()).await
        });
        assert!(
            early_readiness.is_err(),
            "first rejection is not a readiness failure"
        );
        endpoint
            .release_refresh
            .as_ref()
            .expect("reconnect gate")
            .notify_one();
        runtime
            .block_on(async {
                tokio::time::timeout(Duration::from_secs(10), worker.wait_pull_registered()).await
            })
            .expect("readiness deadline")
            .expect("reconnected Worker ready");
    }
    let observed = runtime.block_on(async {
        tokio::time::timeout(Duration::from_secs(10), async {
            let mut polling_interval = tokio::time::interval(Duration::from_millis(100));
            loop {
                let attempt_count = headers.lock().expect("worker headers lock").len();
                if attempt_count >= 2
                    && (!accepts_token_b || worker.pull_registration_state().is_ready)
                {
                    break true;
                }
                if worker_thread.is_finished() {
                    break false;
                }
                polling_interval.tick().await;
            }
        })
        .await
    });
    if !accepts_token_b && observed.as_ref().is_ok_and(|is_observed| *is_observed) {
        let third_attempt = runtime.block_on(async {
            tokio::time::timeout(Duration::from_secs(1), async {
                let mut polling_interval = tokio::time::interval(Duration::from_millis(100));
                loop {
                    if headers.lock().expect("worker headers lock").len() >= 3 {
                        break;
                    }
                    polling_interval.tick().await;
                }
            })
            .await
        });
        assert!(third_attempt.is_err(), "second rejection must use backoff");
    }
    worker.stop();
    let worker_result = worker_thread.join().expect("Worker thread");
    worker_result.expect("Worker stopped");
    assert!(observed.expect("Worker reconnected before timeout"));
    let recorded = headers.lock().expect("worker headers lock");
    assert!(recorded.len() >= 2);
    assert_eq!(recorded[0], format!("Bearer {TOKEN_A}"));
    assert_eq!(recorded[1], format!("Bearer {TOKEN_B}"));
    let exchange_count = *endpoint.count.lock().expect("exchange count lock");
    if accepts_token_b {
        assert_eq!(exchange_count, 2);
    } else {
        assert!(exchange_count >= 2);
    }
    token_task.abort();
    grpc_task.abort();
}

#[test]
fn worker_poll_reconnects_immediately_after_rejected_token() {
    exercise_worker_poll_rejection(true, false);
}

#[test]
fn worker_poll_reconnects_before_permanent_rejection_stop() {
    exercise_worker_poll_rejection(true, true);
}

#[test]
fn worker_poll_backs_off_after_second_rejection() {
    exercise_worker_poll_rejection(false, false);
}
