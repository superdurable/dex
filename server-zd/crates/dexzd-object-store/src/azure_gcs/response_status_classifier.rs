// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::error::Error as StdError;
use std::fmt;
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

/// Timeouts of the HTTP clients that one connector builds.
#[derive(Debug, Clone, Copy)]
pub(crate) struct TransportTimeouts {
    pub(crate) connect: Duration,
    /// Longest wait for the next response bytes; `None` disables it.
    pub(crate) read_idle: Option<Duration>,
    /// Longest duration of one request, including its body; `None` disables it.
    pub(crate) total: Option<Duration>,
}

/// Builds reqwest clients wrapped in [`ResponseStatusClassifyingService`]; following redirects would repeat writes.
#[derive(Debug)]
pub(crate) struct ResponseStatusClassifyingConnector {
    cloud: AzureGcsCloud,
    timeouts: TransportTimeouts,
}

impl ResponseStatusClassifyingConnector {
    pub(crate) fn new(cloud: AzureGcsCloud, timeouts: TransportTimeouts) -> Self {
        Self { cloud, timeouts }
    }
}

impl HttpConnector for ResponseStatusClassifyingConnector {
    fn connect(&self, options: &ClientOptions) -> object_store::Result<HttpClient> {
        // Metadata credential endpoints use plain HTTP; object_store enables it in their options.
        let allow_http = options
            .get_config_value(&ClientConfigKey::AllowHttp)
            .is_some_and(|value| value == "true");
        let mut builder = reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .user_agent(USER_AGENT)
            .http1_only()
            .connect_timeout(self.timeouts.connect)
            .no_gzip()
            .no_brotli()
            .no_zstd()
            .no_deflate()
            .https_only(!allow_http);
        if let Some(read_idle) = self.timeouts.read_idle {
            builder = builder.read_timeout(read_idle);
        }
        if let Some(total) = self.timeouts.total {
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
        }))
    }
}

/// Keeps statuses that `object_store` types; others become status errors, retryable ones as `Request`.
#[derive(Debug)]
struct ResponseStatusClassifyingService {
    inner: HttpClient,
    cloud: AzureGcsCloud,
}

#[async_trait]
impl HttpService for ResponseStatusClassifyingService {
    async fn call(&self, request: HttpRequest) -> Result<HttpResponse, HttpError> {
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
        let handling = classify_failed_status(self.cloud, status, &service_code);
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
