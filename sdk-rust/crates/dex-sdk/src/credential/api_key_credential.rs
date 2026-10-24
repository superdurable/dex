// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::error::Error;
use std::fmt;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use reqwest::{Client, Url};
use serde::Serialize;
use serde_json::Value;
use tokio::sync::Notify;
use tokio::task::JoinHandle;

use super::RejectedTokenRefreshFuture;
use crate::{Credential, CredentialError};

const DEFAULT_EXCHANGE_TIMEOUT: Duration = Duration::from_secs(10);
const FAILED_REFRESH_DELAY: Duration = Duration::from_secs(5);

/// Exchanges an application API key for short-lived Dex access tokens.
///
/// Use this when an application holds a long-lived API key and Dex requires bearer tokens. Use
/// [`crate::FileCredential`] when another process writes ready-to-use tokens to a file. Unlike
/// other [`Credential`] implementations, construction is async: [`ApiKeyCredentialBuilder::build`]
/// performs the first exchange so an invalid key fails before Client or Worker creation. The
/// synchronous [`Credential::token`] reads only the cache and never performs network I/O.
///
/// A background Tokio task refreshes at 80% of each token's lifetime. Concurrent callers use the
/// same cached token and only one exchange runs at a time. After a failed refresh, the current
/// token remains usable until expiry and refresh is retried no more often than every five seconds.
/// An expired token fails calls until a refresh succeeds. A unary Dex call rejected with gRPC
/// `Unauthenticated` discards that exact token and retries once after a fresh exchange. Dex rejects
/// unauthenticated calls before execution, so the retry is safe. Worker poll streams discard
/// rejected tokens and reconnect once immediately. A second rejection uses the normal backoff.
///
/// The credential is safe to share across threads. Its background task is aborted when the last
/// owner drops it. Client and Worker release their references when dropped; applications holding
/// another `Arc` should drop that reference to stop refreshing. Keep the Tokio runtime used for
/// construction alive while the credential is in use. Debug output and exchange errors omit the
/// API key and access tokens.
///
/// ```no_run
/// use std::{sync::Arc, time::Duration};
/// use dex_sdk::{ApiKeyCredential, ClientOptions, WorkerOptions};
/// # async fn example() -> Result<(), Box<dyn std::error::Error>> {
/// let credential = Arc::new(ApiKeyCredential::builder(
///     "https://auth.example.com/token", "my-dex-server", "application-api-key",
/// ).exchange_timeout(Duration::from_secs(10)).build().await?);
/// let client_options = ClientOptions::new().credential(credential.clone());
/// let worker_options = WorkerOptions::new().credential(credential);
/// # let _ = (client_options, worker_options);
/// # Ok(())
/// # }
/// ```
pub struct ApiKeyCredential {
    shared: Arc<SharedState>,
    refresh_task: JoinHandle<()>,
}

struct SharedState {
    token_url: Url,
    server_id: String,
    api_key: String,
    http_client: Client,
    exchange_timeout: Duration,
    state: Mutex<TokenState>,
    changed: Notify,
    refresh_requested: Notify,
}

struct TokenState {
    cached: Option<CachedToken>,
    known_tokens: Vec<String>,
    last_error: Option<ApiKeyCredentialError>,
    exchange_generation: u64,
}

struct CachedToken {
    value: String,
    refresh_at: Instant,
    expires_at: Instant,
}

impl fmt::Debug for ApiKeyCredential {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("ApiKeyCredential { secrets: <redacted> }")
    }
}

impl Drop for ApiKeyCredential {
    fn drop(&mut self) {
        self.refresh_task.abort();
    }
}

impl ApiKeyCredential {
    /// Starts configuration of an API-key credential.
    ///
    /// `token_url` identifies the exchange endpoint. `server_id` scopes its access token, and
    /// `api_key` is the application secret. All three values are required. Validation and the
    /// first exchange occur in [`ApiKeyCredentialBuilder::build`].
    pub fn builder(
        token_url: impl Into<String>,
        server_id: impl Into<String>,
        api_key: impl Into<String>,
    ) -> ApiKeyCredentialBuilder {
        ApiKeyCredentialBuilder {
            token_url: token_url.into(),
            server_id: server_id.into(),
            api_key: api_key.into(),
            exchange_timeout: DEFAULT_EXCHANGE_TIMEOUT,
            http_client: None,
        }
    }
}

impl Credential for ApiKeyCredential {
    fn token(&self) -> Result<String, CredentialError> {
        let state = self
            .shared
            .state
            .lock()
            .expect("API-key credential state lock");
        if let Some(cached) = &state.cached
            && Instant::now() < cached.expires_at
        {
            return Ok(cached.value.clone());
        }
        Err(CredentialError::new(state.last_error.as_ref().map_or_else(
            || {
                format!(
                    "token exchange at {} (HTTP status unavailable): access token unavailable",
                    self.shared.redacted_url()
                )
            },
            ToString::to_string,
        )))
    }

    fn discard_rejected_token(&self, rejected_token: &str) {
        let mut state = self
            .shared
            .state
            .lock()
            .expect("API-key credential state lock");
        if state
            .cached
            .as_ref()
            .is_some_and(|cached| cached.value == rejected_token)
        {
            state.cached = None;
            self.shared.changed.notify_waiters();
            self.shared.refresh_requested.notify_one();
        }
    }

    fn token_after_rejection(&self) -> Option<RejectedTokenRefreshFuture<'_>> {
        Some(Box::pin(async move {
            let original_generation = self
                .shared
                .state
                .lock()
                .expect("API-key credential state lock")
                .exchange_generation;
            let await_exchange = async {
                loop {
                    let notified = self.shared.changed.notified();
                    tokio::pin!(notified);
                    notified.as_mut().enable();
                    let has_result = {
                        let state = self
                            .shared
                            .state
                            .lock()
                            .expect("API-key credential state lock");
                        state
                            .cached
                            .as_ref()
                            .is_some_and(|cached| Instant::now() < cached.expires_at)
                            || state.exchange_generation != original_generation
                    };
                    if has_result {
                        return self.token();
                    }
                    notified.await;
                }
            };
            tokio::time::timeout(self.shared.exchange_timeout, await_exchange)
                .await
                .unwrap_or_else(|_| {
                    Err(CredentialError::new(format!(
                        "token exchange at {} (HTTP status unavailable): timeout",
                        self.shared.redacted_url()
                    )))
                })
        }))
    }
}

/// Configures validation, HTTP transport, and the first API-key exchange.
///
/// Obtain this from [`ApiKeyCredential::builder`]. The default timeout is ten seconds and covers
/// connecting and receiving the response. An injected HTTP client can configure proxies or test
/// transport; its ownership stays with the builder and credential.
pub struct ApiKeyCredentialBuilder {
    token_url: String,
    server_id: String,
    api_key: String,
    exchange_timeout: Duration,
    http_client: Option<Client>,
}

impl fmt::Debug for ApiKeyCredentialBuilder {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("ApiKeyCredentialBuilder { secrets: <redacted> }")
    }
}

impl ApiKeyCredentialBuilder {
    /// Sets the maximum duration of each exchange, including connection and response.
    ///
    /// The default is ten seconds. A zero timeout is rejected by [`Self::build`]. The timeout
    /// also bounds waiting for a fresh token after Dex rejects a unary call.
    pub fn exchange_timeout(mut self, exchange_timeout: Duration) -> Self {
        self.exchange_timeout = exchange_timeout;
        self
    }

    /// Uses an existing HTTP client for token exchange.
    ///
    /// This supports proxy settings and test transport. The credential owns a clone of the
    /// client's connection pool until it is dropped. Each request still uses the exchange timeout.
    pub fn http_client(mut self, http_client: Client) -> Self {
        self.http_client = Some(http_client);
        self
    }

    /// Validates settings, exchanges the key once, and starts automatic refresh.
    ///
    /// The URL must use HTTPS, except `localhost`, IPv4 loopback addresses, and `::1` may use
    /// HTTP. The server ID is supplied by the application and is never inferred from the URL.
    /// This must run inside a Tokio runtime. No background task remains if the initial exchange
    /// fails. See [`ApiKeyCredential`] for a complete example and lifecycle details.
    ///
    /// # Errors
    ///
    /// Returns [`ApiKeyCredentialError`] for an empty value, invalid or disallowed URL, zero
    /// timeout, HTTP client setup failure, transport failure, timeout, rejected HTTP status, or
    /// malformed response. The error names the URL and status without exposing secrets.
    pub async fn build(self) -> Result<ApiKeyCredential, ApiKeyCredentialError> {
        if self.token_url.is_empty() || self.server_id.is_empty() || self.api_key.is_empty() {
            return Err(
                self.configuration_error("token URL, server ID, and API key must be non-empty")
            );
        }
        let token_url = Url::parse(&self.token_url)
            .map_err(|_| self.configuration_error("invalid token URL"))?;
        let is_loopback = token_url.host_str().is_some_and(|host| {
            host.eq_ignore_ascii_case("localhost")
                || host
                    .trim_start_matches('[')
                    .trim_end_matches(']')
                    .parse::<std::net::IpAddr>()
                    .is_ok_and(|address| address.is_loopback())
        });
        if token_url.scheme() != "https" && !(token_url.scheme() == "http" && is_loopback) {
            return Err(self.configuration_error("token URL requires HTTPS except on loopback"));
        }
        if self.exchange_timeout.is_zero() {
            return Err(self.configuration_error("exchange timeout must be positive"));
        }
        let http_client = match self.http_client {
            Some(http_client) => http_client,
            None => Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .build()
                .map_err(|_| {
                    ApiKeyCredentialError::configuration(
                        &self.token_url,
                        &self.api_key,
                        "HTTP client initialization failed",
                    )
                })?,
        };
        let shared = Arc::new(SharedState {
            token_url,
            server_id: self.server_id,
            api_key: self.api_key,
            http_client,
            exchange_timeout: self.exchange_timeout,
            state: Mutex::new(TokenState {
                cached: None,
                known_tokens: Vec::new(),
                last_error: None,
                exchange_generation: 0,
            }),
            changed: Notify::new(),
            refresh_requested: Notify::new(),
        });
        let cached = shared.exchange().await?;
        let mut state = shared.state.lock().expect("API-key credential state lock");
        state.known_tokens.push(cached.value.clone());
        state.cached = Some(cached);
        drop(state);
        let refresh_task = tokio::spawn(SharedState::refresh_loop(Arc::clone(&shared)));
        Ok(ApiKeyCredential {
            shared,
            refresh_task,
        })
    }

    fn configuration_error(&self, detail: &str) -> ApiKeyCredentialError {
        ApiKeyCredentialError::configuration(&self.token_url, &self.api_key, detail)
    }
}

/// A validation, transport, timeout, or response failure during API-key exchange.
///
/// The message includes the token URL and HTTP status, or says the status is unavailable if no
/// response arrived. Server `error` and `error_description` strings are each limited to 200
/// characters. The API key, cached access token, and raw response body are never displayed.
#[derive(Clone)]
pub struct ApiKeyCredentialError {
    message: String,
}

impl ApiKeyCredentialError {
    fn configuration(token_url: &str, api_key: &str, detail: &str) -> Self {
        let safe_url = if api_key.is_empty() {
            token_url.to_string()
        } else {
            token_url.replace(api_key, "<redacted>")
        };
        Self {
            message: format!("token exchange at {safe_url} (HTTP status unavailable): {detail}"),
        }
    }
}

impl fmt::Debug for ApiKeyCredentialError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_tuple("ApiKeyCredentialError")
            .field(&self.message)
            .finish()
    }
}

impl fmt::Display for ApiKeyCredentialError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl Error for ApiKeyCredentialError {}

#[derive(Serialize)]
struct ExchangeRequest<'a> {
    server_id: &'a str,
    api_key: &'a str,
}

impl SharedState {
    fn redacted_url(&self) -> String {
        self.token_url.as_str().replace(&self.api_key, "<redacted>")
    }

    async fn exchange(&self) -> Result<CachedToken, ApiKeyCredentialError> {
        let started_at = Instant::now();
        let response = self
            .http_client
            .post(self.token_url.clone())
            .header(reqwest::header::ACCEPT, "application/json")
            .json(&ExchangeRequest {
                server_id: &self.server_id,
                api_key: &self.api_key,
            })
            .timeout(self.exchange_timeout)
            .send()
            .await
            .map_err(|error| {
                self.error(
                    "status unavailable",
                    if error.is_timeout() {
                        "timeout"
                    } else {
                        "transport failure"
                    },
                )
            })?;
        let status = response.status();
        let response_bytes = response.bytes().await.map_err(|error| {
            self.error(
                &status.as_u16().to_string(),
                if error.is_timeout() {
                    "timeout"
                } else {
                    "response read failed"
                },
            )
        })?;
        let response_json: Value = serde_json::from_slice(&response_bytes)
            .map_err(|_| self.error(&status.as_u16().to_string(), "malformed JSON"))?;
        let response_token = response_json
            .get("access_token")
            .and_then(Value::as_str)
            .filter(|token| !token.is_empty());
        let details = response_json
            .as_object()
            .map(|object| {
                ["error", "error_description"]
                    .into_iter()
                    .filter_map(|name| {
                        object.get(name).and_then(Value::as_str).map(|value| {
                            let redacted = self.redact(value);
                            let redacted = if let Some(token) = response_token {
                                redacted.replace(token, "<redacted>")
                            } else {
                                redacted
                            };
                            format!("{name}: {}", redacted.chars().take(200).collect::<String>())
                        })
                    })
                    .collect::<Vec<_>>()
                    .join("; ")
            })
            .unwrap_or_default();
        if status != reqwest::StatusCode::OK {
            return Err(self.error(&status.as_u16().to_string(), &details));
        }
        let object = response_json
            .as_object()
            .ok_or_else(|| self.response_error("JSON object required", &details))?;
        let token = object
            .get("access_token")
            .and_then(Value::as_str)
            .filter(|value| !value.is_empty())
            .ok_or_else(|| self.response_error("invalid access_token", &details))?;
        let token_type = object
            .get("token_type")
            .and_then(Value::as_str)
            .ok_or_else(|| self.response_error("invalid token_type", &details))?;
        if !token_type.eq_ignore_ascii_case("Bearer") {
            return Err(self.response_error("invalid token_type", &details));
        }
        let expires_in = object
            .get("expires_in")
            .and_then(Value::as_u64)
            .filter(|seconds| *seconds > 0)
            .ok_or_else(|| self.response_error("invalid expires_in", &details))?;
        let lifetime = Duration::from_secs(expires_in);
        let expires_at = started_at
            .checked_add(lifetime)
            .ok_or_else(|| self.response_error("invalid expires_in", &details))?;
        let refresh_at = started_at
            .checked_add(lifetime.mul_f64(0.8))
            .ok_or_else(|| self.response_error("invalid expires_in", &details))?;
        Ok(CachedToken {
            value: token.to_string(),
            refresh_at,
            expires_at,
        })
    }

    fn redact(&self, value: &str) -> String {
        let mut value = value.replace(&self.api_key, "<redacted>");
        let state = self.state.lock().expect("API-key credential state lock");
        for token in &state.known_tokens {
            value = value.replace(token, "<redacted>");
        }
        value
    }

    fn error(&self, status: &str, detail: &str) -> ApiKeyCredentialError {
        ApiKeyCredentialError {
            message: format!(
                "token exchange at {} (HTTP {status}): {detail}",
                self.redacted_url()
            ),
        }
    }

    fn response_error(&self, detail: &str, response_details: &str) -> ApiKeyCredentialError {
        if response_details.is_empty() {
            self.error("200", detail)
        } else {
            self.error("200", &format!("{detail}; {response_details}"))
        }
    }

    async fn refresh_loop(shared: Arc<Self>) {
        loop {
            let refresh_at = shared
                .state
                .lock()
                .expect("API-key credential state lock")
                .cached
                .as_ref()
                .map(|cached| cached.refresh_at);
            if let Some(refresh_at) = refresh_at {
                tokio::select! {
                    () = tokio::time::sleep_until(refresh_at.into()) => {},
                    () = shared.refresh_requested.notified() => {},
                }
            }
            let result = shared.exchange().await;
            let has_failed = result.is_err();
            {
                let mut state = shared.state.lock().expect("API-key credential state lock");
                match result {
                    Ok(cached) => {
                        if !state.known_tokens.contains(&cached.value) {
                            state.known_tokens.push(cached.value.clone());
                        }
                        state.cached = Some(cached);
                        state.last_error = None;
                    }
                    Err(error) => {
                        state.last_error = Some(error);
                    }
                }
                state.exchange_generation += 1;
            }
            shared.changed.notify_waiters();
            if has_failed {
                tokio::select! {
                    () = tokio::time::sleep(FAILED_REFRESH_DELAY) => {},
                    () = shared.refresh_requested.notified() => {},
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use std::collections::VecDeque;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use axum::Router;
    use axum::body::Bytes;
    use axum::extract::State;
    use axum::http::{HeaderMap, StatusCode};
    use axum::routing::post;
    use dex_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
    use dex_protocol::dex::worker_task_service_server::{
        WorkerTaskService, WorkerTaskServiceServer,
    };
    use dex_protocol::dex::{PollWorkerTaskRequest, PollWorkerTaskResponse};
    use tokio::net::TcpListener;
    use tokio::sync::mpsc;
    use tokio_stream::wrappers::{ReceiverStream, TcpListenerStream};
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
            format!(
                r#"{{"access_token":"{token}","token_type":"Bearer","expires_in":{expires_in}}}"#
            ),
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
        let alternate = endpoint_server(vec![(StatusCode::OK,
            format!(r#"{{"access_token":"{TOKEN_B}","token_type":"bEaReR","expires_in":60,"extra":true}}"#),
        )]).await;
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
}
