// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Public API authentication of an in-process dev server with real key and token files.

use std::net::SocketAddr;
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use aws_lc_rs::digest;
use aws_lc_rs::signature::{Ed25519KeyPair, KeyPair};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dexzd_protocol::dex::{
    ErrorSubStatus, PollWorkerTaskRegister, PollWorkerTaskRequest, PollWorkerTaskResponse,
    ServiceErrorResponse, poll_worker_task_request, poll_worker_task_response,
};
use dexzd_server::{DevCommandOptions, RunningServer};
use prost::Message;
use serde_json::json;
use tempfile::TempDir;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio_stream::wrappers::ReceiverStream;
use tonic::transport::Channel;
use tonic::{Code, Request, Status, Streaming};
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

const AUDIENCE: &str = "dex";
const KEY_ID: &str = "dev-server-key";
const FIRST_STATIC_TOKEN: &str = "first-static-token-of-the-dev-server";
const SECOND_STATIC_TOKEN: &str = "second-static-token-of-the-dev-server";
/// Longer than the one-second static token file check interval.
const RELOAD_BUDGET: Duration = Duration::from_secs(5);

/// The `google.rpc.Status` message carried in `grpc-status-details-bin`.
#[derive(Clone, PartialEq, Message)]
struct GoogleRpcStatus {
    #[prost(int32, tag = "1")]
    code: i32,
    #[prost(string, tag = "2")]
    message: String,
    #[prost(message, repeated, tag = "3")]
    details: Vec<prost_types::Any>,
}

/// Serves OIDC discovery and one Ed25519 key over plain HTTP on loopback.
struct KeyServer {
    issuer: String,
    signing_key: Ed25519KeyPair,
    serving: JoinHandle<()>,
}

impl KeyServer {
    async fn start() -> Self {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("a loopback port is available");
        let issuer = format!(
            "http://{}",
            listener.local_addr().expect("the listener has an address")
        );
        let signing_key = Ed25519KeyPair::generate().expect("an Ed25519 key is generated");
        let discovery = json!({"issuer": issuer, "jwks_uri": format!("{issuer}/keys")}).to_string();
        let key_set = json!({"keys": [{
            "kty": "OKP",
            "crv": "Ed25519",
            "kid": KEY_ID,
            "alg": "EdDSA",
            "use": "sig",
            "x": URL_SAFE_NO_PAD.encode(signing_key.public_key().as_ref()),
        }]})
        .to_string();
        let serving = tokio::spawn(async move {
            loop {
                let Ok((mut connection, _peer)) = listener.accept().await else {
                    return;
                };
                let mut request = Vec::new();
                let mut buffer = [0_u8; 1024];
                while !request.windows(4).any(|window| window == b"\r\n\r\n") {
                    match connection.read(&mut buffer).await {
                        Ok(0) | Err(_) => break,
                        Ok(read) => request.extend_from_slice(&buffer[..read]),
                    }
                }
                let request = String::from_utf8_lossy(&request);
                let body = if request.starts_with("GET /.well-known/openid-configuration ") {
                    &discovery
                } else {
                    &key_set
                };
                let response = format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                );
                if connection.write_all(response.as_bytes()).await.is_err() {
                    continue;
                }
                if connection.shutdown().await.is_err() {
                    continue;
                }
            }
        });
        Self {
            issuer,
            signing_key,
            serving,
        }
    }

    /// Signs a JWT whose `exp` is `expires_in_seconds` from now, negative for the past.
    fn token(&self, issuer: &str, audience: &str, expires_in_seconds: i64) -> String {
        let now = i64::try_from(
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("the clock is after 1970")
                .as_secs(),
        )
        .expect("the time fits in i64");
        let header = json!({"alg": "EdDSA", "typ": "JWT", "kid": KEY_ID});
        let claims = json!({"iss": issuer, "aud": audience, "sub": "caller", "exp": now + expires_in_seconds});
        let signing_input = format!(
            "{}.{}",
            URL_SAFE_NO_PAD.encode(header.to_string()),
            URL_SAFE_NO_PAD.encode(claims.to_string())
        );
        let signature = self.signing_key.sign(signing_input.as_bytes());
        format!(
            "{signing_input}.{}",
            URL_SAFE_NO_PAD.encode(signature.as_ref())
        )
    }
}

impl Drop for KeyServer {
    fn drop(&mut self) {
        self.serving.abort();
    }
}

struct AuthenticatedDevServer {
    server: Option<RunningServer>,
    channel: Channel,
    token_file: std::path::PathBuf,
    _directory: TempDir,
}

impl AuthenticatedDevServer {
    async fn start(issuer: &str, extra_yaml: &str) -> Self {
        let directory = TempDir::new().expect("a temporary directory");
        let token_file = directory.path().join("tokens.yaml");
        write_static_tokens(&token_file, &[FIRST_STATIC_TOKEN], 1);
        let config = directory.path().join("overrides.yaml");
        std::fs::write(
            &config,
            format!(
                "cluster:\n  partitionCount: 16\nauth:\n  authenticators:\n    - kind: staticTokens\n      file: {}\n    - kind: jwt\n      issuer: {issuer}\n      audiences: [{AUDIENCE}]\n{extra_yaml}",
                token_file.display()
            ),
        )
        .expect("the configuration is written");
        let listen = std::net::TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
            .and_then(|listener| listener.local_addr())
            .expect("a free loopback port exists");
        let server = dexzd_server::start_dev_server(&DevCommandOptions {
            data_dir: directory.path().join("data"),
            listen,
            log_dir: None,
            config: Some(config),
        })
        .await
        .expect("the dev server starts");
        let channel = Channel::from_shared(format!("http://{}", server.local_address()))
            .expect("the server address is a valid URI")
            .connect()
            .await
            .expect("the gRPC channel connects");
        let started = Self {
            server: Some(server),
            channel,
            token_file,
            _directory: directory,
        };
        started.wait_until_serving_without_credentials().await;
        started
    }

    async fn wait_until_serving_without_credentials(&self) {
        let mut health = HealthClient::new(self.channel.clone());
        tokio::time::timeout(Duration::from_secs(30), async {
            loop {
                let status = health
                    .check(HealthCheckRequest {
                        service: "dex.FlowService".to_owned(),
                    })
                    .await
                    .expect("gRPC health needs no credential")
                    .into_inner()
                    .status;
                if status == ServingStatus::Serving as i32 {
                    return;
                }
                tokio::time::sleep(Duration::from_millis(50)).await;
            }
        })
        .await
        .expect("the dev server reports SERVING");
    }

    async fn server_info(&self, credential: Option<&str>) -> Result<(), Status> {
        let mut request = Request::new(());
        if let Some(credential) = credential {
            insert_bearer(&mut request, credential);
        }
        FlowServiceClient::new(self.channel.clone())
            .get_server_info(request)
            .await
            .map(|_| ())
    }

    async fn shutdown(mut self) {
        self.server
            .take()
            .expect("the server is shut down once")
            .shutdown()
            .await
            .expect("the server stops cleanly");
    }
}

fn insert_bearer<T>(request: &mut Request<T>, credential: &str) {
    request.metadata_mut().insert(
        "authorization",
        format!("Bearer {credential}")
            .parse()
            .expect("the credential is a valid header value"),
    );
}

fn write_static_tokens(path: &Path, tokens: &[&str], version: u64) {
    let mut contents = String::from("tokens:\n");
    for token in tokens {
        let hash: String = digest::digest(&digest::SHA256, token.as_bytes())
            .as_ref()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect();
        contents.push_str(&format!("  - sha256: {hash}\n"));
    }
    std::fs::write(path, contents).expect("the token file is written");
    // A distinct modification time marks the change even when the size is the same.
    std::fs::File::options()
        .write(true)
        .open(path)
        .expect("the token file opens")
        .set_modified(SystemTime::UNIX_EPOCH + Duration::from_secs(1_800_000_000 + version))
        .expect("the modification time is set");
}

fn assert_unauthenticated(result: Result<(), Status>, detail: &str) {
    let status = result.expect_err("the call must be refused");
    assert_eq!(status.code(), Code::Unauthenticated, "{status:?}");
    assert_eq!(status.message(), detail);
    let details = GoogleRpcStatus::decode(status.details()).expect("the details decode");
    assert_eq!(details.code, Code::Unauthenticated as i32);
    assert_eq!(details.message, detail);
    let [detail_message] = details.details.as_slice() else {
        panic!("one status detail is attached");
    };
    assert_eq!(
        detail_message.type_url,
        "type.googleapis.com/dex.ServiceErrorResponse"
    );
    assert_eq!(
        ServiceErrorResponse::decode(detail_message.value.as_slice())
            .expect("the detail is a ServiceErrorResponse"),
        ServiceErrorResponse {
            sub_status: ErrorSubStatus::Uncategorized.into(),
            detail: detail.to_owned(),
            ..ServiceErrorResponse::default()
        }
    );
}

async fn eventually(budget: Duration, mut condition: impl AsyncFnMut() -> bool) {
    tokio::time::timeout(budget, async {
        while !condition().await {
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await
    .expect("the condition holds within its budget");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn flow_and_worker_calls_need_valid_credentials() {
    let key_server = KeyServer::start().await;
    let issuer = key_server.issuer.clone();
    let server = AuthenticatedDevServer::start(&issuer, "").await;

    assert_unauthenticated(
        server.server_info(None).await,
        "a bearer credential is required",
    );
    let invalid = [
        "unknown-token".to_owned(),
        key_server.token(&issuer, AUDIENCE, -3600),
        key_server.token(&issuer, "another-audience", 3600),
        key_server.token("http://127.0.0.1:1", AUDIENCE, 3600),
    ];
    for credential in &invalid {
        assert_unauthenticated(
            server.server_info(Some(credential)).await,
            "the credential is not valid",
        );
    }
    server
        .server_info(Some(FIRST_STATIC_TOKEN))
        .await
        .expect("a listed static token is accepted");
    server
        .server_info(Some(&key_server.token(&issuer, AUDIENCE, 3600)))
        .await
        .expect("a JWT from the configured issuer is accepted");

    let (_frames, outbound) = mpsc::channel::<PollWorkerTaskRequest>(1);
    let refused = WorkerTaskServiceClient::new(server.channel.clone())
        .poll_worker_task(Request::new(ReceiverStream::new(outbound)))
        .await
        .map(|_| ());
    assert_unauthenticated(refused, "a bearer credential is required");

    write_static_tokens(&server.token_file, &[SECOND_STATIC_TOKEN], 2);
    eventually(RELOAD_BUDGET, async || {
        server.server_info(Some(SECOND_STATIC_TOKEN)).await.is_ok()
    })
    .await;
    assert_unauthenticated(
        server.server_info(Some(FIRST_STATIC_TOKEN)).await,
        "the credential is not valid",
    );
    std::fs::write(&server.token_file, "tokens: [").expect("a malformed update is written");
    tokio::time::sleep(Duration::from_millis(1500)).await;
    server
        .server_info(Some(SECOND_STATIC_TOKEN))
        .await
        .expect("a malformed update keeps the last valid tokens");
    std::fs::remove_file(&server.token_file).expect("the token file is removed");
    eventually(RELOAD_BUDGET, async || {
        server.server_info(Some(SECOND_STATIC_TOKEN)).await.is_err()
    })
    .await;
    server.shutdown().await;
}

/// Registers a Worker stream and returns its frame sender and responses after `registered`.
async fn register_worker(
    channel: Channel,
    credential: &str,
) -> (
    mpsc::Sender<PollWorkerTaskRequest>,
    Streaming<PollWorkerTaskResponse>,
) {
    let (frames, outbound) = mpsc::channel(4);
    frames
        .send(PollWorkerTaskRequest {
            frame: Some(poll_worker_task_request::Frame::Register(
                PollWorkerTaskRegister {
                    queue: "AuthenticatedWorkerFlow".to_owned(),
                    app_worker_id: "authenticated-worker".to_owned(),
                    flow_types: vec!["AuthenticatedWorkerFlow".to_owned()],
                    sdk_name: "test".to_owned(),
                    sdk_version: "0".to_owned(),
                },
            )),
        })
        .await
        .expect("the registration frame is queued");
    let mut request = Request::new(ReceiverStream::new(outbound));
    insert_bearer(&mut request, credential);
    let mut responses = WorkerTaskServiceClient::new(channel)
        .poll_worker_task(request)
        .await
        .expect("the stream opens with a valid token")
        .into_inner();
    let registered = responses
        .message()
        .await
        .expect("the stream stays healthy")
        .expect("the server confirms the registration");
    assert!(matches!(
        registered.frame,
        Some(poll_worker_task_response::Frame::Registered(_))
    ));
    (frames, responses)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_registered_worker_stream_outlives_its_token() {
    let key_server = Arc::new(KeyServer::start().await);
    let issuer = key_server.issuer.clone();
    let server = AuthenticatedDevServer::start(&issuer, "matching:\n  pingInterval: 300ms\n").await;
    // Expired 28 seconds ago: valid for about two more seconds of the 30-second leeway.
    let token = key_server.token(&issuer, AUDIENCE, -28);
    let (_frames, mut responses) = register_worker(server.channel.clone(), &token).await;

    eventually(Duration::from_secs(10), async || {
        server.server_info(Some(&token)).await.is_err()
    })
    .await;
    let mut pings_after_expiry = 0;
    while pings_after_expiry < 3 {
        let response = tokio::time::timeout(Duration::from_secs(5), responses.message())
            .await
            .expect("the open stream keeps receiving frames")
            .expect("the open stream stays healthy")
            .expect("the open stream stays open");
        if matches!(
            response.frame,
            Some(poll_worker_task_response::Frame::Ping(_))
        ) {
            pings_after_expiry += 1;
        }
    }
    let (_new_frames, new_outbound) = mpsc::channel::<PollWorkerTaskRequest>(1);
    let mut reconnect = Request::new(ReceiverStream::new(new_outbound));
    insert_bearer(&mut reconnect, &token);
    let refused = WorkerTaskServiceClient::new(server.channel.clone())
        .poll_worker_task(reconnect)
        .await
        .map(|_| ());
    assert_unauthenticated(refused, "the credential is not valid");
    drop(responses);
    server.shutdown().await;
}
