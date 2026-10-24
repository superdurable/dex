// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::VecDeque;
use std::convert::Infallible;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::task::{Context, Poll};

use axum::Router;
use axum::body::Bytes;
use axum::extract::State;
use axum::http::{HeaderMap, StatusCode};
use axum::routing::post;
use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dex_protocol::dex::worker_task_service_server::{WorkerTaskService, WorkerTaskServiceServer};
use dex_protocol::dex::{PollWorkerTaskRequest, PollWorkerTaskResponse};
use tokio::net::TcpListener;
use tokio::sync::mpsc;
use tokio_stream::wrappers::{ReceiverStream, TcpListenerStream};
use tonic::codegen::{BoxFuture, Service, http};
use tonic::server::NamedService;
use tonic::transport::{Endpoint, Server};
use tonic::{Request, Response, Status, Streaming};

use super::*;
use crate::credential::{CredentialChannel, CredentialInterceptor};

const API_KEY: &str = "test-api-key-secret";
const TOKEN_A: &str = "test-access-token-secret-a";
const TOKEN_B: &str = "test-access-token-secret-b";

struct TokenEndpoint {
    responses: Mutex<VecDeque<(StatusCode, String)>>,
    requests: Mutex<Vec<(HeaderMap, Bytes)>>,
    request_count: AtomicUsize,
}

struct EndpointServer {
    url: String,
    endpoint: Arc<TokenEndpoint>,
    task: JoinHandle<()>,
}

#[derive(Clone)]
struct RejectingWorkerTaskService {
    headers: Arc<Mutex<Vec<String>>>,
}

#[derive(Clone)]
struct RejectingFlowService {
    attempts: Arc<AtomicUsize>,
}

impl NamedService for RejectingFlowService {
    const NAME: &'static str = "dex.FlowService";
}

impl<RequestBody> Service<http::Request<RequestBody>> for RejectingFlowService
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

    fn call(&mut self, _: http::Request<RequestBody>) -> Self::Future {
        self.attempts.fetch_add(1, Ordering::SeqCst);
        Box::pin(async { Ok(Status::unauthenticated("token rejected").into_http()) })
    }
}

#[tonic::async_trait]
impl WorkerTaskService for RejectingWorkerTaskService {
    type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

    async fn poll_worker_task(
        &self,
        request: Request<Streaming<PollWorkerTaskRequest>>,
    ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
        let authorization = request
            .metadata()
            .get("authorization")
            .map(|value| value.to_str().expect("ASCII authorization header"))
            .unwrap_or_default()
            .to_string();
        self.headers
            .lock()
            .expect("worker header lock")
            .push(authorization.clone());
        if authorization != format!("Bearer {TOKEN_B}") {
            return Err(Status::unauthenticated("rejected token"));
        }
        let (_sender, receiver) = mpsc::channel(1);
        Ok(Response::new(ReceiverStream::new(receiver)))
    }
}

impl Drop for EndpointServer {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn token_handler(
    State(endpoint): State<Arc<TokenEndpoint>>,
    headers: HeaderMap,
    body: Bytes,
) -> (StatusCode, [(String, String); 1], String) {
    endpoint.request_count.fetch_add(1, Ordering::SeqCst);
    endpoint
        .requests
        .lock()
        .expect("request lock")
        .push((headers, body));
    let (status, response) = endpoint
        .responses
        .lock()
        .expect("response lock")
        .pop_front()
        .unwrap_or((StatusCode::SERVICE_UNAVAILABLE, "{}".to_string()));
    (
        status,
        [("content-type".to_string(), "application/json".to_string())],
        response,
    )
}

async fn endpoint_server(responses: Vec<(StatusCode, String)>) -> EndpointServer {
    let endpoint = Arc::new(TokenEndpoint {
        responses: Mutex::new(responses.into()),
        requests: Mutex::new(Vec::new()),
        request_count: AtomicUsize::new(0),
    });
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind token endpoint");
    let url = format!(
        "http://{}/token",
        listener.local_addr().expect("listener address")
    );
    let router = Router::new()
        .route("/token", post(token_handler))
        .with_state(Arc::clone(&endpoint));
    let task = tokio::spawn(async move {
        axum::serve(listener, router)
            .await
            .expect("serve token endpoint");
    });
    EndpointServer {
        url,
        endpoint,
        task,
    }
}

fn token_response(token: &str, expires_in: u64) -> (StatusCode, String) {
    (
        StatusCode::OK,
        format!(r#"{{"access_token":"{token}","token_type":"Bearer","expires_in":{expires_in}}}"#),
    )
}

#[tokio::test]
async fn validates_request_and_response() {
    let server = endpoint_server(vec![token_response(TOKEN_A, 60)]).await;
    let credential = ApiKeyCredential::builder(&server.url, "server-1", API_KEY)
        .build()
        .await
        .expect("build credential");
    assert_eq!(credential.token().expect("cached token"), TOKEN_A);
    {
        let requests = server.endpoint.requests.lock().expect("request lock");
        assert_eq!(requests.len(), 1);
        assert_eq!(
            requests[0].1,
            r#"{"server_id":"server-1","api_key":"test-api-key-secret"}"#
        );
        assert_eq!(
            requests[0].0.get("content-type").expect("content type"),
            "application/json"
        );
        assert_eq!(
            requests[0].0.get("accept").expect("accept"),
            "application/json"
        );
    }
    let alternate = endpoint_server(vec![(
        StatusCode::OK,
        format!(
            r#"{{"access_token":"{TOKEN_B}","token_type":"bEaReR","expires_in":60,"extra":true}}"#
        ),
    )])
    .await;
    let credential = ApiKeyCredential::builder(&alternate.url, "server-1", API_KEY)
        .build()
        .await
        .expect("case-insensitive bearer and unknown field");
    assert_eq!(credential.token().expect("alternate token"), TOKEN_B);
}

#[tokio::test]
async fn rejects_invalid_urls_and_responses_without_leaking_secrets() {
    for url in [
        "",
        "not a url",
        "http://example.com/token",
        "http://localhost.example.com/token",
        "http://127.0.0.1.nip.io/token",
        "ftp://localhost/token",
    ] {
        let error = ApiKeyCredential::builder(url, "server-1", API_KEY)
            .build()
            .await
            .expect_err("invalid URL");
        assert!(!format!("{error:?} {error}").contains(API_KEY));
    }
    for (server_id, api_key) in [("", API_KEY), ("server-1", "")] {
        let error = ApiKeyCredential::builder("https://example.com/token", server_id, api_key)
            .build()
            .await
            .expect_err("empty input");
        assert!(error.to_string().contains("non-empty"));
    }
    for response in [
        (
            StatusCode::BAD_REQUEST,
            format!(
                r#"{{"access_token":"{TOKEN_A}","error":"invalid_key","error_description":"{API_KEY} {TOKEN_A}"}}"#
            ),
        ),
        (StatusCode::CREATED, token_response(TOKEN_A, 60).1),
        (StatusCode::OK, "not-json".to_string()),
        (StatusCode::OK, "[]".to_string()),
        (
            StatusCode::OK,
            r#"{"token_type":"Bearer","expires_in":1}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":"","token_type":"Bearer","expires_in":1}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":4,"token_type":"Bearer","expires_in":1}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":"x","expires_in":1}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":"x","token_type":"Basic","expires_in":1}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":"x","token_type":"Bearer"}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":"x","token_type":"Bearer","expires_in":0}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":"x","token_type":"Bearer","expires_in":-1}"#.to_string(),
        ),
        (
            StatusCode::OK,
            r#"{"access_token":"x","token_type":"Bearer","expires_in":1.5}"#.to_string(),
        ),
    ] {
        let server = endpoint_server(vec![response]).await;
        let error = ApiKeyCredential::builder(&server.url, "server-1", API_KEY)
            .build()
            .await
            .expect_err("invalid response");
        let formatted = format!("{error:?} {error}");
        assert!(formatted.contains(&server.url));
        assert!(formatted.contains("HTTP"));
        assert!(!formatted.contains(API_KEY));
        assert!(!formatted.contains(TOKEN_A));
        assert!(!formatted.contains("not-json"));
    }
    let long_description = "x".repeat(250);
    let server = endpoint_server(vec![(
        StatusCode::BAD_REQUEST,
        format!(r#"{{"error":"invalid_key","error_description":"{long_description}"}}"#),
    )])
    .await;
    let error = ApiKeyCredential::builder(&server.url, "server-1", API_KEY)
        .build()
        .await
        .expect_err("long error description");
    assert!(error.to_string().contains("invalid_key"));
    assert!(error.to_string().contains(&"x".repeat(200)));
    assert!(!error.to_string().contains(&"x".repeat(201)));
}

#[tokio::test]
async fn refreshes_once_and_serves_cached_token_until_expiry() {
    let server = endpoint_server(vec![
        token_response(TOKEN_A, 2),
        (
            StatusCode::SERVICE_UNAVAILABLE,
            format!(r#"{{"error":"unavailable","error_description":"{TOKEN_A} {API_KEY}"}}"#),
        ),
    ])
    .await;
    let credential = Arc::new(
        ApiKeyCredential::builder(&server.url, "server-1", API_KEY)
            .build()
            .await
            .expect("build credential"),
    );
    let mut callers = Vec::new();
    for _ in 0..20 {
        let credential = Arc::clone(&credential);
        let caller = tokio::spawn(async move { credential.token().expect("cached token") });
        callers.push(caller);
    }
    for caller in callers {
        assert_eq!(caller.await.expect("caller task"), TOKEN_A);
    }
    tokio::time::timeout(Duration::from_secs(3), async {
        while server.endpoint.request_count.load(Ordering::SeqCst) < 2 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("refresh attempt");
    assert_eq!(credential.token().expect("still cached"), TOKEN_A);
    let error = tokio::time::timeout(Duration::from_secs(2), async {
        let mut polling_interval = tokio::time::interval(Duration::from_millis(50));
        loop {
            if let Err(error) = credential.token() {
                break error;
            }
            polling_interval.tick().await;
        }
    })
    .await
    .expect("token expiry");
    assert!(error.to_string().contains("HTTP 503"));
    assert!(!format!("{error:?} {error}").contains(TOKEN_A));
    assert!(!format!("{error:?} {error}").contains(API_KEY));
    assert_eq!(server.endpoint.request_count.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn rejected_token_only_discards_the_cached_value() {
    let server = endpoint_server(vec![
        token_response(TOKEN_A, 60),
        token_response(TOKEN_B, 60),
    ])
    .await;
    let credential = ApiKeyCredential::builder(&server.url, "server-1", API_KEY)
        .build()
        .await
        .expect("build credential");
    credential.discard_rejected_token("older-token");
    assert_eq!(credential.token().expect("current token"), TOKEN_A);
    credential.discard_rejected_token(TOKEN_A);
    let refreshed = credential
        .token_after_rejection()
        .expect("refresh future")
        .await
        .expect("refreshed token");
    assert_eq!(refreshed, TOKEN_B);
    credential.discard_rejected_token(TOKEN_A);
    assert_eq!(credential.token().expect("newer token"), TOKEN_B);
    assert_eq!(server.endpoint.request_count.load(Ordering::SeqCst), 2);
    let debug = format!(
        "{credential:?} {:?}",
        ApiKeyCredential::builder(&server.url, "server-1", API_KEY)
    );
    assert!(!debug.contains(API_KEY));
    assert!(!debug.contains(TOKEN_A));
    assert!(!debug.contains(TOKEN_B));
}

#[tokio::test]
async fn transport_failures_and_timeouts_report_no_secret() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind unused port");
    let unavailable_url = format!(
        "http://{}/token",
        listener.local_addr().expect("unused address")
    );
    drop(listener);
    let error = ApiKeyCredential::builder(unavailable_url.clone(), "server-1", API_KEY)
        .build()
        .await
        .expect_err("transport failure");
    assert!(error.to_string().contains(&unavailable_url));
    assert!(error.to_string().contains("HTTP status unavailable"));
    assert!(!format!("{error:?} {error}").contains(API_KEY));
    let ipv6_error = ApiKeyCredential::builder("http://[::1]:9/token", "server-1", API_KEY)
        .exchange_timeout(Duration::from_millis(100))
        .build()
        .await
        .expect_err("closed IPv6 loopback endpoint");
    assert!(!ipv6_error.to_string().contains("requires HTTPS"));

    async fn slow_token_handler() -> (StatusCode, &'static str) {
        tokio::time::sleep(Duration::from_millis(100)).await;
        (StatusCode::OK, "{}")
    }
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind slow token endpoint");
    let url = format!(
        "http://{}/token",
        listener.local_addr().expect("slow address")
    );
    let task = tokio::spawn(async move {
        axum::serve(
            listener,
            Router::new().route("/token", post(slow_token_handler)),
        )
        .await
        .expect("slow token server");
    });
    let error = ApiKeyCredential::builder(&url, "server-1", API_KEY)
        .exchange_timeout(Duration::from_millis(10))
        .build()
        .await
        .expect_err("exchange timeout");
    assert!(error.to_string().contains("timeout"));
    assert!(error.to_string().contains(&url));
    assert!(!format!("{error:?} {error}").contains(API_KEY));
    task.abort();
}

#[tokio::test]
async fn rejected_poll_stream_discards_token_without_retrying_open() {
    let token_server = endpoint_server(vec![
        token_response(TOKEN_A, 60),
        token_response(TOKEN_B, 60),
    ])
    .await;
    let credential = Arc::new(
        ApiKeyCredential::builder(&token_server.url, "server-1", API_KEY)
            .build()
            .await
            .expect("credential"),
    );
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("worker listener");
    let address = listener.local_addr().expect("worker address");
    let headers = Arc::new(Mutex::new(Vec::new()));
    let task = tokio::spawn(
        Server::builder()
            .add_service(WorkerTaskServiceServer::new(RejectingWorkerTaskService {
                headers: Arc::clone(&headers),
            }))
            .serve_with_incoming(TcpListenerStream::new(listener)),
    );
    let channel = Endpoint::from_shared(format!("http://{address}"))
        .expect("worker endpoint")
        .connect()
        .await
        .expect("worker connection");
    let mut client = WorkerTaskServiceClient::new(CredentialChannel::new(
        channel,
        CredentialInterceptor::new(Some(credential.clone())),
    ));
    let error = client
        .poll_worker_task(tokio_stream::empty::<PollWorkerTaskRequest>())
        .await
        .expect_err("first poll rejection");
    assert_eq!(error.code(), tonic::Code::Unauthenticated);
    assert_eq!(headers.lock().expect("worker header lock").len(), 1);
    assert!(credential.token().is_err());
    let refreshed = credential
        .token_after_rejection()
        .expect("refresh future")
        .await
        .expect("fresh token");
    assert_eq!(refreshed, TOKEN_B);
    client
        .poll_worker_task(tokio_stream::empty::<PollWorkerTaskRequest>())
        .await
        .expect("reconnected stream");
    assert_eq!(
        *headers.lock().expect("worker header lock"),
        vec![format!("Bearer {TOKEN_A}"), format!("Bearer {TOKEN_B}"),]
    );
    assert_eq!(
        token_server.endpoint.request_count.load(Ordering::SeqCst),
        2
    );
    task.abort();
}

#[tokio::test]
async fn expired_initial_exchange_fails_build() {
    async fn delayed_token() -> (StatusCode, String) {
        tokio::time::sleep(Duration::from_millis(1_200)).await;
        token_response(TOKEN_A, 1)
    }
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind delayed endpoint");
    let url = format!("http://{}/token", listener.local_addr().expect("address"));
    let task = tokio::spawn(async move {
        axum::serve(listener, Router::new().route("/token", post(delayed_token)))
            .await
            .expect("serve delayed endpoint");
    });
    let error = ApiKeyCredential::builder(url, "server-1", API_KEY)
        .build()
        .await
        .expect_err("expired exchange result");
    assert!(
        error
            .to_string()
            .contains("token expired before exchange completed")
    );
    task.abort();
}

#[tokio::test]
async fn expired_refresh_result_uses_retry_floor() {
    async fn delayed_refresh(State(count): State<Arc<AtomicUsize>>) -> (StatusCode, String) {
        if count.fetch_add(1, Ordering::SeqCst) == 0 {
            token_response(TOKEN_A, 2)
        } else {
            tokio::time::sleep(Duration::from_millis(1_200)).await;
            token_response(TOKEN_B, 1)
        }
    }
    let count = Arc::new(AtomicUsize::new(0));
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind refresh endpoint");
    let url = format!("http://{}/token", listener.local_addr().expect("address"));
    let server_count = Arc::clone(&count);
    let task = tokio::spawn(async move {
        axum::serve(
            listener,
            Router::new()
                .route("/token", post(delayed_refresh))
                .with_state(server_count),
        )
        .await
        .expect("serve refresh endpoint");
    });
    let credential = ApiKeyCredential::builder(url, "server-1", API_KEY)
        .build()
        .await
        .expect("initial exchange");
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            if credential.token().is_err_and(|error| {
                error
                    .to_string()
                    .contains("token expired before exchange completed")
            }) {
                break;
            }
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("expired refresh recorded");
    assert_eq!(count.load(Ordering::SeqCst), 2);
    let extra_exchange = tokio::time::timeout(Duration::from_secs(1), async {
        while count.load(Ordering::SeqCst) == 2 {
            tokio::task::yield_now().await;
        }
    })
    .await;
    assert!(
        extra_exchange.is_err(),
        "failed refresh retries after five seconds"
    );
    task.abort();
}

#[tokio::test]
async fn injected_http_builder_never_follows_redirects() {
    async fn record_redirect_target(State(count): State<Arc<AtomicUsize>>) -> StatusCode {
        count.fetch_add(1, Ordering::SeqCst);
        StatusCode::OK
    }
    async fn redirect_to_target(
        State(target): State<(String, StatusCode)>,
    ) -> (StatusCode, [(String, String); 1]) {
        (target.1, [("location".to_string(), target.0)])
    }
    for status in [
        StatusCode::TEMPORARY_REDIRECT,
        StatusCode::PERMANENT_REDIRECT,
    ] {
        let target_count = Arc::new(AtomicUsize::new(0));
        let target_listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind redirect target");
        let target_url = format!(
            "http://{}/collect",
            target_listener.local_addr().expect("target address")
        );
        let target_router = Router::new()
            .route("/collect", post(record_redirect_target))
            .with_state(Arc::clone(&target_count));
        let target_task = tokio::spawn(async move {
            axum::serve(target_listener, target_router)
                .await
                .expect("serve redirect target");
        });
        let source_listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind redirect source");
        let source_url = format!(
            "http://{}/token",
            source_listener.local_addr().expect("source address")
        );
        let source_router = Router::new()
            .route("/token", post(redirect_to_target))
            .with_state((target_url, status));
        let source_task = tokio::spawn(async move {
            axum::serve(source_listener, source_router)
                .await
                .expect("serve redirect source");
        });
        let error = ApiKeyCredential::builder(source_url, "server-1", API_KEY)
            .http_client(Client::builder())
            .build()
            .await
            .expect_err("redirect is an exchange error");
        assert!(
            error
                .to_string()
                .contains(&format!("HTTP {}", status.as_u16()))
        );
        assert_eq!(target_count.load(Ordering::SeqCst), 0);
        source_task.abort();
        target_task.abort();
    }
}

#[tokio::test]
async fn unary_retry_wait_honors_request_deadline() {
    async fn delayed_refresh(State(count): State<Arc<AtomicUsize>>) -> (StatusCode, String) {
        if count.fetch_add(1, Ordering::SeqCst) == 0 {
            token_response(TOKEN_A, 60)
        } else {
            tokio::time::sleep(Duration::from_secs(1)).await;
            token_response(TOKEN_B, 60)
        }
    }
    let count = Arc::new(AtomicUsize::new(0));
    let token_listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind token endpoint");
    let token_url = format!(
        "http://{}/token",
        token_listener.local_addr().expect("token address")
    );
    let token_task = tokio::spawn(async move {
        axum::serve(
            token_listener,
            Router::new()
                .route("/token", post(delayed_refresh))
                .with_state(count),
        )
        .await
        .expect("serve token endpoint");
    });
    let credential = Arc::new(
        ApiKeyCredential::builder(token_url, "server-1", API_KEY)
            .build()
            .await
            .expect("initial exchange"),
    );
    let grpc_listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind gRPC endpoint");
    let grpc_address = grpc_listener.local_addr().expect("gRPC address");
    let attempts = Arc::new(AtomicUsize::new(0));
    let grpc_task = tokio::spawn(
        Server::builder()
            .add_service(RejectingFlowService {
                attempts: Arc::clone(&attempts),
            })
            .serve_with_incoming(TcpListenerStream::new(grpc_listener)),
    );
    let channel = Endpoint::from_shared(format!("http://{grpc_address}"))
        .expect("gRPC endpoint")
        .connect()
        .await
        .expect("connect gRPC endpoint");
    let mut client = FlowServiceClient::new(CredentialChannel::new(
        channel,
        CredentialInterceptor::new(Some(credential)),
    ));
    let mut request = Request::new(());
    request.set_timeout(Duration::from_millis(100));
    let started = Instant::now();
    let error = client
        .health_check(request)
        .await
        .expect_err("call deadline exceeded before retry");
    assert_eq!(error.code(), tonic::Code::DeadlineExceeded);
    assert!(started.elapsed() < Duration::from_millis(500));
    assert_eq!(attempts.load(Ordering::SeqCst), 1);
    grpc_task.abort();
    token_task.abort();
}
