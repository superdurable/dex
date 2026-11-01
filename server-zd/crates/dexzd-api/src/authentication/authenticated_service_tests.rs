// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Authenticated gRPC services on an in-process server: statuses, exemptions, and open streams.

use std::pin::Pin;
use std::sync::Arc;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dexzd_protocol::dex::worker_task_service_server::{WorkerTaskService, WorkerTaskServiceServer};
use dexzd_protocol::dex::{
    ErrorSubStatus, PollWorkerTaskPing, PollWorkerTaskRequest, PollWorkerTaskResponse,
    ServiceErrorResponse, poll_worker_task_response,
};
use futures::Stream;
use prost::Message;
use serde_json::json;
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio_stream::StreamExt;
use tokio_stream::wrappers::{ReceiverStream, TcpListenerStream};
use tonic::transport::Channel;
use tonic::{Code, Request, Response, Status, Streaming};
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

use super::json_web_key_set::{JsonWebKeySetCache, KeySetRefreshPolicy};
use super::json_web_token::{JwsAlgorithm, JwtVerifier};
use super::static_token_file::StaticTokenHashes;
use super::token_signing_tests::{
    ManualWallClock, ScriptedKeySetSource, TEST_AUDIENCE, TEST_ISSUER, TEST_START_UNIX_SECONDS,
    TestSigningKey, key_set_document,
};
use super::watched_file::WatchedFile;
use super::{
    AuthenticatedService, CredentialAuthenticator, JwtAuthenticatorConfig, PublicApiAuthenticator,
};
use crate::flow_operation_error::GoogleRpcStatus;

const KEY_ID: &str = "worker-key";
const STATIC_TOKEN: &str = "static-token-for-authenticated-service-tests";

/// Answers every Worker frame with a ping, so a test can tell an open stream from a closed one.
struct EchoWorkerTaskService;

type ResponseStream = Pin<Box<dyn Stream<Item = Result<PollWorkerTaskResponse, Status>> + Send>>;

#[tonic::async_trait]
impl WorkerTaskService for EchoWorkerTaskService {
    type PollWorkerTaskStream = ResponseStream;

    async fn poll_worker_task(
        &self,
        request: Request<Streaming<PollWorkerTaskRequest>>,
    ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
        let pings = request.into_inner().map(|frame| {
            frame.map(|_| PollWorkerTaskResponse {
                frame: Some(poll_worker_task_response::Frame::Ping(
                    PollWorkerTaskPing {},
                )),
            })
        });
        Ok(Response::new(Box::pin(pings)))
    }
}

struct TestServer {
    channel: Channel,
    clock: Arc<ManualWallClock>,
    signing_key: TestSigningKey,
    token_file: tempfile::NamedTempFile,
    serving: JoinHandle<()>,
}

impl Drop for TestServer {
    fn drop(&mut self) {
        self.serving.abort();
    }
}

impl TestServer {
    async fn start() -> Self {
        let clock = ManualWallClock::starting_at_test_start();
        let signing_key = TestSigningKey::generate(JwsAlgorithm::EdDsa);
        let source =
            ScriptedKeySetSource::serving(key_set_document(&[signing_key.public_jwk(KEY_ID)]));
        let token_file = tempfile::NamedTempFile::new().expect("a temporary token file");
        std::fs::write(
            token_file.path(),
            format!(
                "tokens:\n  - sha256: {}\n",
                hex(&super::sha256(STATIC_TOKEN))
            ),
        )
        .expect("the token file is written");
        let authenticator = PublicApiAuthenticator {
            authenticators: vec![
                CredentialAuthenticator::StaticTokens(
                    WatchedFile::<StaticTokenHashes>::open_with_check_interval(
                        token_file.path(),
                        Duration::ZERO,
                    )
                    .expect("the token file loads"),
                ),
                CredentialAuthenticator::Jwt(JwtVerifier::new(
                    JwtAuthenticatorConfig {
                        issuer: TEST_ISSUER.to_owned(),
                        audiences: vec![TEST_AUDIENCE.to_owned()],
                        admin_audiences: Vec::new(),
                        jwks_url: None,
                        allow_insecure_jwks_url: false,
                        ca_file: None,
                    },
                    JsonWebKeySetCache::new(
                        TEST_ISSUER.to_owned(),
                        source,
                        KeySetRefreshPolicy::STANDARD,
                    ),
                    Arc::clone(&clock) as Arc<_>,
                )),
            ],
        };
        let (health_reporter, health_service) = tonic_health::server::health_reporter();
        health_reporter
            .set_serving::<WorkerTaskServiceServer<EchoWorkerTaskService>>()
            .await;
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("a loopback port is available");
        let address = listener.local_addr().expect("the listener has an address");
        let authenticated_worker_service = AuthenticatedService::new(
            WorkerTaskServiceServer::new(EchoWorkerTaskService),
            Some(Arc::new(authenticator)),
        );
        let serving = tokio::spawn(async move {
            tonic::transport::Server::builder()
                .add_service(health_service)
                .add_service(authenticated_worker_service)
                .serve_with_incoming(TcpListenerStream::new(listener))
                .await
                .expect("the test server serves");
        });
        let channel = tonic::transport::Endpoint::from_shared(format!("http://{address}"))
            .expect("the endpoint URL is valid")
            .connect()
            .await
            .expect("the test server accepts connections");
        Self {
            channel,
            clock,
            signing_key,
            token_file,
            serving,
        }
    }

    fn jwt_expiring_after(&self, validity: Duration) -> String {
        let validity = i64::try_from(validity.as_secs()).expect("test validity fits in i64");
        self.signing_key.token(
            KEY_ID,
            &json!({
                "iss": TEST_ISSUER,
                "aud": TEST_AUDIENCE,
                "exp": TEST_START_UNIX_SECONDS + validity,
            }),
        )
    }

    /// Opens a Worker stream with `credential`, or returns the status that refused it.
    async fn open_worker_stream(&self, credential: Option<&str>) -> Result<WorkerStream, Status> {
        let (frames, outbound) = mpsc::channel(4);
        let mut request = Request::new(ReceiverStream::new(outbound));
        if let Some(credential) = credential {
            request.metadata_mut().insert(
                "authorization",
                format!("Bearer {credential}")
                    .parse()
                    .expect("the credential is a valid header value"),
            );
        }
        let mut stream = WorkerStream {
            frames,
            responses: None,
        };
        // A rejected call answers before reading frames; send the first one only after it opens.
        let responses = WorkerTaskServiceClient::new(self.channel.clone())
            .poll_worker_task(request)
            .await?
            .into_inner();
        stream.responses = Some(responses);
        Ok(stream)
    }
}

struct WorkerStream {
    frames: mpsc::Sender<PollWorkerTaskRequest>,
    responses: Option<Streaming<PollWorkerTaskResponse>>,
}

impl WorkerStream {
    /// Sends one frame and waits for the server's ping.
    async fn exchange(&mut self) {
        self.frames
            .send(PollWorkerTaskRequest::default())
            .await
            .expect("the stream accepts frames");
        let responses = self.responses.as_mut().expect("the stream is open");
        let response = tokio::time::timeout(Duration::from_secs(5), responses.message())
            .await
            .expect("the server answers within five seconds")
            .expect("the stream stays healthy")
            .expect("the stream stays open");
        assert!(matches!(
            response.frame,
            Some(poll_worker_task_response::Frame::Ping(_))
        ));
    }
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn assert_unauthenticated(result: Result<WorkerStream, Status>, detail: &str) {
    let Err(status) = result else {
        panic!("the call must be refused");
    };
    assert_eq!(status.code(), Code::Unauthenticated);
    assert_eq!(status.message(), detail);
    let details =
        GoogleRpcStatus::decode(status.details()).expect("the details are a google.rpc.Status");
    assert_eq!(details.code, Code::Unauthenticated as i32);
    assert_eq!(details.message, detail);
    assert_eq!(details.details.len(), 1);
    assert_eq!(
        details.details[0].type_url,
        "type.googleapis.com/dex.ServiceErrorResponse"
    );
    let response = ServiceErrorResponse::decode(details.details[0].value.as_slice())
        .expect("the detail is a ServiceErrorResponse");
    assert_eq!(
        response,
        ServiceErrorResponse {
            sub_status: ErrorSubStatus::Uncategorized.into(),
            detail: detail.to_owned(),
            ..ServiceErrorResponse::default()
        }
    );
}

#[tokio::test]
async fn worker_calls_need_a_credential_and_health_does_not() {
    let server = TestServer::start().await;
    assert_unauthenticated(
        server.open_worker_stream(None).await,
        "a bearer credential is required",
    );
    assert_unauthenticated(
        server.open_worker_stream(Some("not-a-known-token")).await,
        "the credential is not valid",
    );
    let expired = server.signing_key.token(
        KEY_ID,
        &json!({"iss": TEST_ISSUER, "aud": TEST_AUDIENCE, "exp": TEST_START_UNIX_SECONDS - 60}),
    );
    assert_unauthenticated(
        server.open_worker_stream(Some(&expired)).await,
        "the credential is not valid",
    );
    for credential in [
        STATIC_TOKEN.to_owned(),
        server.jwt_expiring_after(Duration::from_secs(60)),
    ] {
        server
            .open_worker_stream(Some(&credential))
            .await
            .expect("a valid credential opens the stream")
            .exchange()
            .await;
    }

    let health = HealthClient::new(server.channel.clone())
        .check(HealthCheckRequest {
            service: String::new(),
        })
        .await
        .expect("health needs no credential");
    assert_eq!(health.into_inner().status, ServingStatus::Serving as i32);
    let unregistered = FlowServiceClient::new(server.channel.clone())
        .get_server_info(())
        .await
        .expect_err("FlowService is not registered on this test server");
    assert_eq!(unregistered.code(), Code::Unimplemented);
}

#[tokio::test]
async fn an_open_worker_stream_outlives_token_expiry_and_revocation() {
    let server = TestServer::start().await;
    let token = server.jwt_expiring_after(Duration::from_secs(60));
    let mut jwt_stream = server
        .open_worker_stream(Some(&token))
        .await
        .expect("the token is valid when the stream opens");
    jwt_stream.exchange().await;
    let mut static_stream = server
        .open_worker_stream(Some(STATIC_TOKEN))
        .await
        .expect("the static token is listed when the stream opens");
    static_stream.exchange().await;

    server.clock.advance(Duration::from_secs(3600));
    std::fs::write(server.token_file.path(), "").expect("the token file is emptied");

    jwt_stream.exchange().await;
    static_stream.exchange().await;
    assert_unauthenticated(
        server.open_worker_stream(Some(&token)).await,
        "the credential is not valid",
    );
    assert_unauthenticated(
        server.open_worker_stream(Some(STATIC_TOKEN)).await,
        "the credential is not valid",
    );
}
