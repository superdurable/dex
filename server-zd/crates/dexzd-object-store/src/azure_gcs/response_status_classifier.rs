// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::error::Error as StdError;
use std::fmt;

use async_trait::async_trait;
use object_store::ClientOptions;
use object_store::client::{
    HttpClient, HttpConnector, HttpError, HttpErrorKind, HttpRequest, HttpResponse, HttpService,
    ReqwestConnector,
};

use super::AzureGcsCloud;

/// Longest response body excerpt kept in an error detail.
const MAX_BODY_EXCERPT_CHARS: usize = 512;

/// A failed response whose status `object_store` would otherwise hide in a crate-private error.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct UnexpectedResponseStatus {
    /// HTTP status code of the response.
    pub(crate) status: u16,
    /// Service error code from `x-ms-error-code`, or empty.
    pub(crate) service_code: String,
    /// What made the response unexpected, with a body excerpt when one was read.
    pub(crate) detail: String,
}

impl UnexpectedResponseStatus {
    /// Returns true for a copy that the service accepted but has not finished.
    pub(crate) fn is_pending_copy(&self) -> bool {
        (200..300).contains(&self.status)
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

/// Wraps the reqwest connector of `object_store` with [`ResponseStatusClassifyingService`].
#[derive(Debug)]
pub(crate) struct ResponseStatusClassifyingConnector {
    cloud: AzureGcsCloud,
}

impl ResponseStatusClassifyingConnector {
    pub(crate) fn new(cloud: AzureGcsCloud) -> Self {
        Self { cloud }
    }
}

impl HttpConnector for ResponseStatusClassifyingConnector {
    fn connect(&self, options: &ClientOptions) -> object_store::Result<HttpClient> {
        let inner = ReqwestConnector::default().connect(options)?;
        Ok(HttpClient::new(ResponseStatusClassifyingService {
            inner,
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
        let service_code = response
            .headers()
            .get("x-ms-error-code")
            .and_then(|value| value.to_str().ok())
            .unwrap_or("")
            .to_owned();
        if (200..300).contains(&status) {
            return match response
                .headers()
                .get("x-ms-copy-status")
                .map(|value| value.to_str().unwrap_or(""))
            {
                Some(copy_status) if copy_status != "success" => Err(HttpError::new(
                    HttpErrorKind::Unknown,
                    UnexpectedResponseStatus {
                        status,
                        service_code,
                        detail: format!("copy did not complete synchronously: {copy_status}"),
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
        (_, 300..=399 | 401 | 403 | 412) => FailedStatusHandling::PassThrough,
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
