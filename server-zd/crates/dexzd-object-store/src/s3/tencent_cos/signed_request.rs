// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SigV4-signed requests whose URI is sent exactly as signed, without dot-segment removal.

use std::sync::Arc;
use std::time::{Duration, SystemTime};

use aws_sdk_s3::config::{ProvideCredentials, SharedCredentialsProvider};
use aws_sigv4::http_request::{
    PayloadChecksumKind, PercentEncodingMode, SignableBody, SignableRequest, SigningSettings,
    UriPathNormalizationMode, sign,
};
use aws_sigv4::sign::v4;
use bytes::Bytes;
use http_body_util::combinators::UnsyncBoxBody;
use hyper::body::Incoming;
use hyper_rustls::{HttpsConnector, HttpsConnectorBuilder};
use hyper_util::client::legacy::Client;
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::rt::TokioExecutor;

use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult};

/// Body of a signed request.
pub(super) type SignedRequestBody = UnsyncBoxBody<Bytes, std::io::Error>;

/// Hex SHA-256 of an empty payload.
pub(super) const EMPTY_PAYLOAD_SHA256_HEX: &str =
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";

/// Signs requests with the store's credentials and sends them over HTTP/1.1 or HTTPS.
pub(super) struct SignedRequestSender {
    http_client: Client<HttpsConnector<HttpConnector>, SignedRequestBody>,
    /// `None` sends unsigned requests.
    credentials: Option<SharedCredentialsProvider>,
    region: String,
}

/// One request: the URI string is signed and sent byte for byte.
pub(super) struct SignedRequest<'a> {
    pub(super) method: http::Method,
    pub(super) uri: &'a str,
    /// Headers besides those that signing adds; all of them are signed.
    pub(super) headers: &'a [(&'a str, &'a str)],
    pub(super) payload_sha256_hex: &'a str,
    pub(super) body: SignedRequestBody,
}

impl SignedRequestSender {
    /// Builds the client, trusting the system root certificates for HTTPS.
    pub(super) fn new(
        credentials: Option<SharedCredentialsProvider>,
        region: String,
        connect_timeout: Duration,
    ) -> Result<Self, String> {
        let mut roots = rustls::RootCertStore::empty();
        let native = rustls_native_certs::load_native_certs();
        for error in &native.errors {
            tracing::warn!(%error, "a system root certificate source could not be loaded");
        }
        let (_added, _ignored) = roots.add_parsable_certificates(native.certs);
        let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
            rustls::crypto::aws_lc_rs::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .map_err(|error| format!("configuring TLS: {error}"))?
        .with_root_certificates(roots)
        .with_no_client_auth();
        let mut http = HttpConnector::new();
        http.enforce_http(false);
        http.set_connect_timeout(Some(connect_timeout));
        let connector = HttpsConnectorBuilder::new()
            .with_tls_config(tls)
            .https_or_http()
            .enable_http1()
            .wrap_connector(http);
        Ok(Self {
            http_client: Client::builder(TokioExecutor::new()).build(connector),
            credentials,
            region,
        })
    }

    /// Signs `request` for `key` and sends it.
    ///
    /// Returns the response, or an error: [`ObjectStoreErrorKind::Permanent`] for an invalid
    /// request, [`ObjectStoreErrorKind::Transient`] when credentials fail or the connection
    /// cannot be opened, and [`ObjectStoreErrorKind::OutcomeUnknown`] when the request may have
    /// been sent.
    pub(super) async fn send(
        &self,
        key: &str,
        request: SignedRequest<'_>,
    ) -> ObjectStoreResult<http::Response<Incoming>> {
        let failure = |kind, detail: String| {
            ObjectStoreError::new(ObjectStoreOperation::Put, key, kind, detail)
        };
        let signed_headers = self
            .signed_headers(
                key,
                &request.method,
                request.uri,
                request.headers,
                request.payload_sha256_hex,
            )
            .await?;
        let uri: http::Uri = request.uri.parse().map_err(|error| {
            failure(
                ObjectStoreErrorKind::Permanent,
                format!("invalid request URI: {error}"),
            )
        })?;
        let mut builder = http::Request::builder().method(request.method).uri(uri);
        for (name, value) in request.headers {
            builder = builder.header(*name, *value);
        }
        for (name, value) in &signed_headers {
            builder = builder.header(name.as_str(), value.as_str());
        }
        let http_request = builder
            .body(request.body)
            .map_err(|error| failure(ObjectStoreErrorKind::Permanent, error.to_string()))?;
        self.http_client
            .request(http_request)
            .await
            .map_err(|error| {
                // Only a failed connection proves that nothing was sent.
                let kind = if error.is_connect() {
                    ObjectStoreErrorKind::Transient
                } else {
                    ObjectStoreErrorKind::OutcomeUnknown
                };
                failure(kind, format!("sending the request: {error:?}"))
            })
    }

    /// Returns the SigV4 headers of a request, or none for anonymous credentials.
    async fn signed_headers(
        &self,
        key: &str,
        method: &http::Method,
        uri: &str,
        headers: &[(&str, &str)],
        payload_sha256_hex: &str,
    ) -> ObjectStoreResult<Vec<(String, String)>> {
        let Some(provider) = &self.credentials else {
            return Ok(Vec::new());
        };
        let signing_error = |kind, detail: String| {
            ObjectStoreError::new(ObjectStoreOperation::Put, key, kind, detail)
        };
        // Nothing was sent, so a credential failure may be retried.
        let credentials = provider.provide_credentials().await.map_err(|error| {
            signing_error(
                ObjectStoreErrorKind::Transient,
                format!("resolving credentials: {error}"),
            )
        })?;
        let identity = credentials.into();
        let mut settings = SigningSettings::default();
        settings.percent_encoding_mode = PercentEncodingMode::Single;
        settings.uri_path_normalization_mode = UriPathNormalizationMode::Disabled;
        settings.payload_checksum_kind = PayloadChecksumKind::XAmzSha256;
        let params = v4::SigningParams::builder()
            .identity(&identity)
            .region(&self.region)
            .name("s3")
            .time(SystemTime::now())
            .settings(settings)
            .build()
            .map_err(|error| signing_error(ObjectStoreErrorKind::Permanent, error.to_string()))?
            .into();
        let signable = SignableRequest::new(
            method.as_str(),
            uri,
            headers.iter().copied(),
            SignableBody::Precomputed(payload_sha256_hex.to_owned()),
        )
        .map_err(|error| signing_error(ObjectStoreErrorKind::Permanent, error.to_string()))?;
        let (instructions, _signature) = sign(signable, &params)
            .map_err(|error| signing_error(ObjectStoreErrorKind::Permanent, error.to_string()))?
            .into_parts();
        let (headers, _query) = instructions.into_parts();
        Ok(headers
            .into_iter()
            .map(|header| (header.name().to_owned(), header.value().to_owned()))
            .collect())
    }
}
