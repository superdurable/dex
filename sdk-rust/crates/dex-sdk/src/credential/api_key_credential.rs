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

use reqwest::{Client, ClientBuilder, Url};
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
/// construction alive and driving tasks while the credential is in use. A multi-thread runtime
/// does this while it remains alive; a current-thread runtime must be continuously driven.
/// Debug output and exchange errors omit the API key and access tokens.
///
/// ```no_run
/// use std::{sync::Arc, time::Duration};
/// use dex_sdk::{ApiKeyCredential, ClientOptions, WorkerOptions};
/// let runtime = tokio::runtime::Runtime::new()?;
/// let credential = Arc::new(runtime.block_on(ApiKeyCredential::builder(
///     "https://auth.example.com/token", "my-dex-server", "application-api-key",
/// ).exchange_timeout(Duration::from_secs(10)).build())?);
/// let client_options = ClientOptions::new().credential(credential.clone());
/// let worker_options = WorkerOptions::new().credential(credential);
/// # let _ = (client_options, worker_options);
/// # drop(runtime);
/// # Ok::<(), Box<dyn std::error::Error>>(())
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
        let last_error = state.last_error.clone();
        drop(state);
        Err(CredentialError::new(last_error.as_ref().map_or_else(
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
            self.shared.refresh_requested.notify_waiters();
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
/// transport. Redirects are disabled when the client is built.
pub struct ApiKeyCredentialBuilder {
    token_url: String,
    server_id: String,
    api_key: String,
    exchange_timeout: Duration,
    http_client: Option<ClientBuilder>,
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

    /// Configures the HTTP client for token exchange.
    ///
    /// This supports proxy settings and test transport. Redirects are always disabled so the
    /// API key is never sent to a redirect target. Each request uses the exchange timeout.
    pub fn http_client(mut self, http_client: ClientBuilder) -> Self {
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
        let http_client = self
            .http_client
            .unwrap_or_else(Client::builder)
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| {
                ApiKeyCredentialError::configuration(
                    &self.token_url,
                    &self.api_key,
                    "HTTP client initialization failed",
                )
            })?;
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
        let cached = shared.validate_exchange_result(shared.exchange().await?)?;
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
            redact_jwt_substrings(token_url)
        } else {
            redact_jwt_substrings(token_url).replace(api_key, "<redacted>")
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
    fn validate_exchange_result(
        &self,
        mut cached: CachedToken,
    ) -> Result<CachedToken, ApiKeyCredentialError> {
        let now = Instant::now();
        if now >= cached.expires_at {
            return Err(self.error("200", "token expired before exchange completed"));
        }
        if now >= cached.refresh_at {
            cached.refresh_at = now + FAILED_REFRESH_DELAY;
        }
        Ok(cached)
    }

    fn redacted_url(&self) -> String {
        self.redact(self.token_url.as_str())
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
                    } else if error.is_connect() {
                        "connection failed"
                    } else if error.is_body() {
                        "request body failed"
                    } else {
                        "transport failure"
                    },
                )
            })?;
        let status = response.status();
        if status.is_redirection() {
            return Err(self.error(&status.as_u16().to_string(), "redirect rejected"));
        }
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
        let mut value = redact_jwt_substrings(value).replace(&self.api_key, "<redacted>");
        let state = self.state.lock().expect("API-key credential state lock");
        for token in &state.known_tokens {
            value = value.replace(token, "<redacted>");
        }
        value
    }

    fn error(&self, status: &str, detail: &str) -> ApiKeyCredentialError {
        let status = self.redact(status);
        let detail = self.redact(detail);
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
            let notified = shared.refresh_requested.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
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
                    () = &mut notified => {},
                }
            }
            let result = shared
                .exchange()
                .await
                .and_then(|cached| shared.validate_exchange_result(cached));
            let failed_error = result.as_ref().err().cloned();
            {
                let mut state = shared.state.lock().expect("API-key credential state lock");
                match result {
                    Ok(cached) => {
                        if !state.known_tokens.contains(&cached.value) {
                            state.known_tokens.push(cached.value.clone());
                            if state.known_tokens.len() > 2 {
                                state.known_tokens.remove(0);
                            }
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
            if let Some(error) = failed_error {
                tracing::warn!(error = %error, "API-key token refresh failed");
                tokio::time::sleep(FAILED_REFRESH_DELAY).await;
            }
        }
    }
}

fn redact_jwt_substrings(value: &str) -> String {
    let bytes = value.as_bytes();
    let mut redacted = String::with_capacity(value.len());
    let mut index = 0;
    let mut unchanged_start = 0;
    while index < bytes.len() {
        if index > 0 && is_base64url(bytes[index - 1]) {
            index += 1;
            continue;
        }
        let first_end = base64url_segment_end(bytes, index);
        if first_end - index < 10 || bytes.get(first_end) != Some(&b'.') {
            index += 1;
            continue;
        }
        let second_start = first_end + 1;
        let second_end = base64url_segment_end(bytes, second_start);
        if second_end - second_start < 10 || bytes.get(second_end) != Some(&b'.') {
            index += 1;
            continue;
        }
        let third_start = second_end + 1;
        let third_end = base64url_segment_end(bytes, third_start);
        if third_end - third_start < 10 {
            index += 1;
            continue;
        }
        redacted.push_str(&value[unchanged_start..index]);
        redacted.push_str("<redacted>");
        index = third_end;
        unchanged_start = index;
    }
    redacted.push_str(&value[unchanged_start..]);
    redacted
}

fn base64url_segment_end(bytes: &[u8], start: usize) -> usize {
    let mut end = start;
    while end < bytes.len() && is_base64url(bytes[end]) {
        end += 1;
    }
    end
}

fn is_base64url(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_')
}

#[cfg(test)]
#[path = "api_key_credential_tests.rs"]
mod tests;
