// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#[cfg(feature = "test-util")]
use std::collections::BTreeSet;
use std::error::Error as StdError;
use std::fmt;
#[cfg(feature = "test-util")]
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use object_store::client::{
    HttpClient, HttpConnector, HttpError, HttpErrorKind, HttpRequest, HttpResponse, HttpService,
};
use object_store::{ClientConfigKey, ClientOptions};

use super::AzureGcsCloud;

/// Longest response body excerpt kept in an error detail.
const MAX_BODY_EXCERPT_CHARS: usize = 512;
const USER_AGENT: &str = "dexzd-object-store";

/// A failed response whose status `object_store` would otherwise hide in a crate-private error.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct UnexpectedResponseStatus {
    /// HTTP status code of the response.
    pub(crate) status: u16,
    /// Service error code from `x-ms-error-code`, or empty.
    pub(crate) service_code: String,
    /// Azure `x-ms-copy-status` of a blob whose copy did not succeed, or empty.
    pub(crate) copy_status: String,
    /// What made the response unexpected, with a body excerpt when one was read.
    pub(crate) detail: String,
}

impl UnexpectedResponseStatus {
    /// Returns true for an Azure copy that the service accepted but has not finished.
    pub(crate) fn is_pending_copy(&self) -> bool {
        self.copy_status == "pending"
    }

    /// Returns true for an Azure blob whose copy failed or was aborted.
    pub(crate) fn is_failed_copy(&self) -> bool {
        !self.copy_status.is_empty() && !self.is_pending_copy()
    }

    /// Returns true for a redirect, which these APIs never send to a correct endpoint.
    pub(crate) fn is_redirect(&self) -> bool {
        (300..400).contains(&self.status)
    }
}

impl fmt::Display for UnexpectedResponseStatus {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "HTTP {}", self.status)?;
        if !self.service_code.is_empty() {
            write!(formatter, " {}", self.service_code)?;
        }
        write!(formatter, ": {}", self.detail)
    }
}

impl StdError for UnexpectedResponseStatus {}

/// Timeouts of the HTTP clients that one connector builds, unless the client options override them.
#[derive(Debug, Clone, Copy)]
pub(crate) struct TransportTimeouts {
    pub(crate) connect: Duration,
    /// Longest wait for the next response bytes; `None` disables it.
    pub(crate) read_idle: Option<Duration>,
    /// Longest duration of one request, including its body; `None` disables it.
    pub(crate) total: Option<Duration>,
}

impl TransportTimeouts {
    /// Returns these timeouts with each one that `options` sets, such as a metadata endpoint's 1 s connect.
    fn overridden_by(self, options: &ClientOptions) -> object_store::Result<Self> {
        Ok(Self {
            connect: option_duration(options, ClientConfigKey::ConnectTimeout)?
                .unwrap_or(self.connect),
            read_idle: option_duration(options, ClientConfigKey::ReadTimeout)?.or(self.read_idle),
            total: option_duration(options, ClientConfigKey::Timeout)?.or(self.total),
        })
    }
}

/// Returns the duration that `options` sets for `key`, or `None` when it sets none.
fn option_duration(
    options: &ClientOptions,
    key: ClientConfigKey,
) -> object_store::Result<Option<Duration>> {
    options
        .get_config_value(&key)
        .map(|value| {
            humantime::parse_duration(&value).map_err(|error| object_store::Error::Generic {
                store: USER_AGENT,
                source: Box::new(error),
            })
        })
        .transpose()
}

/// The distinct `scheme://host:port` origins that a store's clients sent requests to.
///
/// Only test-util builds record them; other builds keep nothing.
#[derive(Debug, Clone, Default)]
pub(crate) struct RequestedOrigins(#[cfg(feature = "test-util")] Arc<Mutex<BTreeSet<String>>>);

impl RequestedOrigins {
    #[cfg_attr(
        not(feature = "test-util"),
        expect(unused_variables, reason = "only test-util builds record origins")
    )]
    fn record(&self, request: &HttpRequest) {
        #[cfg(feature = "test-util")]
        self.record_origin(request);
    }

    #[cfg(feature = "test-util")]
    fn record_origin(&self, request: &HttpRequest) {
        let uri = request.uri();
        let origin = format!(
            "{}://{}",
            uri.scheme_str().unwrap_or(""),
            uri.authority().map_or("", |authority| authority.as_str())
        );
        self.0
            .lock()
            .expect("the requested origins lock is never poisoned")
            .insert(origin);
    }

    /// Returns the recorded origins in ascending order.
    #[cfg(feature = "test-util")]
    pub(crate) fn origins(&self) -> Vec<String> {
        self.0
            .lock()
            .expect("the requested origins lock is never poisoned")
            .iter()
            .cloned()
            .collect()
    }
}

/// Builds reqwest clients wrapped in [`ResponseStatusClassifyingService`]; following redirects would repeat writes.
#[derive(Debug)]
pub(crate) struct ResponseStatusClassifyingConnector {
    cloud: AzureGcsCloud,
    timeouts: TransportTimeouts,
    requested_origins: RequestedOrigins,
}

impl ResponseStatusClassifyingConnector {
    pub(crate) fn new(
        cloud: AzureGcsCloud,
        timeouts: TransportTimeouts,
        requested_origins: RequestedOrigins,
    ) -> Self {
        Self {
            cloud,
            timeouts,
            requested_origins,
        }
    }
}

impl HttpConnector for ResponseStatusClassifyingConnector {
    fn connect(&self, options: &ClientOptions) -> object_store::Result<HttpClient> {
        // Metadata credential endpoints use plain HTTP; object_store enables it in their options.
        let allow_http = options
            .get_config_value(&ClientConfigKey::AllowHttp)
            .is_some_and(|value| value == "true");
        let timeouts = self.timeouts.overridden_by(options)?;
        let mut builder = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .user_agent(USER_AGENT)
            .http1_only()
            .connect_timeout(timeouts.connect)
            .no_gzip()
            .no_brotli()
            .no_zstd()
            .no_deflate()
            .https_only(!allow_http);
        if let Some(read_idle) = timeouts.read_idle {
            builder = builder.read_timeout(read_idle);
        }
        if let Some(total) = timeouts.total {
            builder = builder.timeout(total);
        }
        let client = builder
            .build()
            .map_err(|error| object_store::Error::Generic {
                store: USER_AGENT,
                source: Box::new(error),
            })?;
        Ok(HttpClient::new(ResponseStatusClassifyingService {
            inner: HttpClient::new(client),
            cloud: self.cloud,
            requested_origins: self.requested_origins.clone(),
        }))
    }
}

/// Keeps statuses that `object_store` types; others become status errors, retryable ones as `Request`.
#[derive(Debug)]
struct ResponseStatusClassifyingService {
    inner: HttpClient,
    cloud: AzureGcsCloud,
    requested_origins: RequestedOrigins,
}

#[async_trait]
impl HttpService for ResponseStatusClassifyingService {
    async fn call(&self, request: HttpRequest) -> Result<HttpResponse, HttpError> {
        // object_store reports failed POSTs, such as multipart completions, without their status.
        let is_post = request.method() == http::Method::POST;
        self.requested_origins.record(&request);
        let response = self.inner.execute(request).await?;
        let status = response.status().as_u16();
        let header = |name: &str| {
            response
                .headers()
                .get(name)
                .map(|value| value.to_str().unwrap_or("unreadable").to_owned())
        };
        let service_code = header("x-ms-error-code").unwrap_or_default();
        if (200..300).contains(&status) {
            return match header("x-ms-copy-status") {
                Some(copy_status) if copy_status != "success" => Err(HttpError::new(
                    HttpErrorKind::Unknown,
                    UnexpectedResponseStatus {
                        status,
                        service_code,
                        detail: format!("blob copy status is {copy_status}"),
                        copy_status,
                    },
                )),
                _ => Ok(response),
            };
        }
        let handling = if is_post {
            FailedStatusHandling::Classify
        } else {
            classify_failed_status(self.cloud, status, &service_code)
        };
        if handling == FailedStatusHandling::PassThrough {
            return Ok(response);
        }
        let (parts, body) = response.into_parts();
        let body = body.bytes().await?;
        let excerpt: String = String::from_utf8_lossy(&body)
            .chars()
            .take(MAX_BODY_EXCERPT_CHARS)
            .collect();
        if handling == FailedStatusHandling::PassThroughUnlessMissingBucket
            && !excerpt.contains("<Code>NoSuchBucket</Code>")
        {
            return Ok(HttpResponse::from_parts(parts, body.into()));
        }
        let kind = match status {
            408 | 429 | 500..=599 => HttpErrorKind::Request,
            _ => HttpErrorKind::Unknown,
        };
        Err(HttpError::new(
            kind,
            UnexpectedResponseStatus {
                status,
                service_code,
                copy_status: String::new(),
                detail: excerpt,
            },
        ))
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FailedStatusHandling {
    /// `object_store` maps the status to a typed error that the backend relies on.
    PassThrough,
    /// A GCS 404 is a missing object unless its body names a missing bucket.
    PassThroughUnlessMissingBucket,
    Classify,
}

fn classify_failed_status(
    cloud: AzureGcsCloud,
    status: u16,
    service_code: &str,
) -> FailedStatusHandling {
    match (cloud, status) {
        (_, 304 | 401 | 403 | 412) => FailedStatusHandling::PassThrough,
        (AzureGcsCloud::AzureBlob, 404) if service_code == "ContainerNotFound" => {
            FailedStatusHandling::Classify
        }
        (AzureGcsCloud::AzureBlob, 404) => FailedStatusHandling::PassThrough,
        (AzureGcsCloud::GoogleCloudStorage, 404) => {
            FailedStatusHandling::PassThroughUnlessMissingBucket
        }
        (AzureGcsCloud::AzureBlob, 409) if service_code == "BlobAlreadyExists" => {
            FailedStatusHandling::PassThrough
        }
        _ => FailedStatusHandling::Classify,
    }
}

/// Failure found in the source chain of an `object_store` error.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) enum HttpFailureCause {
    /// A response that [`ResponseStatusClassifyingService`] classified.
    Status(UnexpectedResponseStatus),
    /// The request failed before a response arrived.
    Transport(HttpErrorKind),
    /// No HTTP failure, such as a credential or response parsing error.
    NotHttp,
}

/// Walks the source chain of `error` to find its HTTP failure.
pub(crate) fn http_failure_cause(error: &(dyn StdError + 'static)) -> HttpFailureCause {
    let mut current = Some(error);
    while let Some(candidate) = current {
        if let Some(status) = candidate.downcast_ref::<UnexpectedResponseStatus>() {
            return HttpFailureCause::Status(status.clone());
        }
        if let Some(http_error) = candidate.downcast_ref::<HttpError>() {
            if let Some(status) = http_error
                .source()
                .and_then(|source| source.downcast_ref::<UnexpectedResponseStatus>())
            {
                return HttpFailureCause::Status(status.clone());
            }
            return HttpFailureCause::Transport(http_error.kind());
        }
        current = candidate.source();
    }
    HttpFailureCause::NotHttp
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn client_options_override_the_connector_timeouts() {
        let defaults = TransportTimeouts {
            connect: Duration::from_secs(30),
            read_idle: Some(Duration::from_secs(30)),
            total: None,
        };
        let metadata_options = ClientOptions::new()
            .with_timeout_disabled()
            .with_connect_timeout(Duration::from_secs(1));
        let timeouts = defaults
            .overridden_by(&metadata_options)
            .expect("the options parse");
        assert_eq!(timeouts.connect, Duration::from_secs(1));
        assert_eq!(timeouts.read_idle, Some(Duration::from_secs(30)));
        assert_eq!(timeouts.total, None);
        let write_options = ClientOptions::new()
            .with_connect_timeout(Duration::from_millis(1500))
            .with_timeout(Duration::from_secs(45));
        let timeouts = defaults
            .overridden_by(&write_options)
            .expect("the options parse");
        assert_eq!(timeouts.connect, Duration::from_millis(1500));
        assert_eq!(timeouts.total, Some(Duration::from_secs(45)));
    }
}
