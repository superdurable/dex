// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! HTTPS retrieval of JSON Web Key Sets, directly or through OIDC discovery.

use std::net::IpAddr;
use std::path::Path;
use std::sync::Arc;

use async_trait::async_trait;
use bytes::Bytes;
use http_body_util::{BodyExt, Empty, Limited};
use hyper::Uri;
use hyper::header::ACCEPT;
use hyper_rustls::{HttpsConnector, HttpsConnectorBuilder};
use hyper_util::client::legacy::Client;
use hyper_util::client::legacy::connect::HttpConnector;
use hyper_util::rt::TokioExecutor;
use rustls::RootCertStore;
use rustls::pki_types::CertificateDer;
use rustls::pki_types::pem::PemObject;
use serde::Deserialize;

use super::json_web_key_set::{KeySet, KeySetFetchError, KeySetSource};
use super::{AuthenticationSetupError, JwtAuthenticatorConfig};

/// Largest discovery or key set response read, as in the Go Server.
const MAX_KEY_SET_RESPONSE_BYTES: usize = 1 << 20;
const OPENID_CONFIGURATION_PATH: &str = "/.well-known/openid-configuration";

/// Why a JWT issuer or key set URL is not acceptable.
#[derive(Clone, Copy, Debug, PartialEq, Eq, thiserror::Error)]
pub enum EndpointUrlError {
    /// The value is not an absolute URL with a host.
    #[error("must be an absolute URL")]
    NotAbsolute,
    /// The URL uses plain HTTP on a host that is not loopback, or a scheme other than HTTP(S).
    #[error("must use https unless its host is loopback")]
    RequiresHttps,
    /// The URL uses a scheme other than HTTP or HTTPS.
    #[error("must use http or https")]
    UnsupportedScheme,
}

/// Checks a JWT issuer or key set URL.
///
/// An HTTPS URL is always accepted. Plain HTTP is accepted on `localhost` and loopback IP
/// addresses, and on every host when `allows_plain_http` is true, which only an explicitly
/// configured key set URL may request. The Go Server applies the same rule without the opt-in.
///
/// # Errors
///
/// Returns [`EndpointUrlError`] naming the violated rule.
pub fn check_endpoint_url(url: &str, allows_plain_http: bool) -> Result<(), EndpointUrlError> {
    let uri: Uri = url.parse().map_err(|_| EndpointUrlError::NotAbsolute)?;
    let host = uri
        .host()
        .filter(|host| !host.is_empty())
        .ok_or(EndpointUrlError::NotAbsolute)?;
    match uri.scheme_str() {
        Some("https") => Ok(()),
        Some("http") if allows_plain_http || is_loopback_host(host) => Ok(()),
        Some("http") => Err(EndpointUrlError::RequiresHttps),
        Some(_) if allows_plain_http => Err(EndpointUrlError::UnsupportedScheme),
        Some(_) => Err(EndpointUrlError::RequiresHttps),
        None => Err(EndpointUrlError::NotAbsolute),
    }
}

fn is_loopback_host(host: &str) -> bool {
    if host == "localhost" {
        return true;
    }
    let address = host
        .strip_prefix('[')
        .and_then(|bracketed| bracketed.strip_suffix(']'))
        .unwrap_or(host);
    address
        .parse::<IpAddr>()
        .is_ok_and(|address| address.is_loopback())
}

type KeySetHttpClient = Client<HttpsConnector<HttpConnector>, Empty<Bytes>>;

/// Fetches one issuer's key set over HTTP(S). Redirects are not followed.
pub(crate) struct KeySetFetcher {
    issuer: String,
    jwks_url: Option<String>,
    http_client: KeySetHttpClient,
}

impl KeySetFetcher {
    /// Builds the TLS client from the system roots plus `caFile`.
    pub(crate) fn new(config: &JwtAuthenticatorConfig) -> Result<Self, AuthenticationSetupError> {
        let roots = root_certificates(config.ca_file.as_deref())?;
        let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
            rustls::crypto::aws_lc_rs::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .map_err(|source| AuthenticationSetupError::Tls { source })?
        .with_root_certificates(roots)
        .with_no_client_auth();
        let connector = HttpsConnectorBuilder::new()
            .with_tls_config(tls)
            .https_or_http()
            .enable_http1()
            .build();
        Ok(Self {
            issuer: config.issuer.clone(),
            jwks_url: config.jwks_url.clone(),
            http_client: Client::builder(TokioExecutor::new()).build(connector),
        })
    }

    async fn discover_jwks_url(&self) -> Result<String, KeySetFetchError> {
        #[derive(Deserialize)]
        struct OpenIdConfiguration {
            #[serde(default)]
            issuer: String,
            #[serde(default)]
            jwks_uri: String,
        }
        let url = format!(
            "{}{OPENID_CONFIGURATION_PATH}",
            self.issuer.strip_suffix('/').unwrap_or(&self.issuer)
        );
        let body = self.get(&url).await?;
        let discovery: OpenIdConfiguration = serde_json::from_slice(&body)
            .map_err(|source| KeySetFetchError::InvalidJson { url, source })?;
        if discovery.issuer != self.issuer {
            return Err(KeySetFetchError::DiscoveryIssuerMismatch {
                discovered: discovery.issuer,
            });
        }
        check_endpoint_url(&discovery.jwks_uri, false)
            .map_err(|error| KeySetFetchError::DiscoveredJwksUrl { error })?;
        Ok(discovery.jwks_uri)
    }

    async fn get(&self, url: &str) -> Result<Bytes, KeySetFetchError> {
        let uri: Uri = url.parse().map_err(|_| KeySetFetchError::InvalidUrl {
            url: url.to_owned(),
        })?;
        let request = hyper::Request::get(uri)
            .header(ACCEPT, "application/json")
            .body(Empty::new())
            .map_err(|_| KeySetFetchError::InvalidUrl {
                url: url.to_owned(),
            })?;
        let response =
            self.http_client
                .request(request)
                .await
                .map_err(|error| KeySetFetchError::Request {
                    url: url.to_owned(),
                    detail: error.to_string(),
                })?;
        if response.status() != hyper::StatusCode::OK {
            return Err(KeySetFetchError::UnexpectedStatus {
                url: url.to_owned(),
                status: response.status().as_u16(),
            });
        }
        let body = Limited::new(response.into_body(), MAX_KEY_SET_RESPONSE_BYTES)
            .collect()
            .await
            .map_err(|error| KeySetFetchError::Request {
                url: url.to_owned(),
                detail: error.to_string(),
            })?;
        Ok(body.to_bytes())
    }
}

#[async_trait]
impl KeySetSource for KeySetFetcher {
    async fn fetch_key_set(&self) -> Result<KeySet, KeySetFetchError> {
        let jwks_url = match &self.jwks_url {
            Some(jwks_url) => jwks_url.clone(),
            None => self.discover_jwks_url().await?,
        };
        let body = self.get(&jwks_url).await?;
        KeySet::parse(&body).map_err(|source| KeySetFetchError::InvalidJson {
            url: jwks_url,
            source,
        })
    }
}

/// Returns the system roots plus every certificate in `ca_file`, which must hold at least one.
fn root_certificates(ca_file: Option<&Path>) -> Result<RootCertStore, AuthenticationSetupError> {
    let mut roots = RootCertStore::empty();
    let native = rustls_native_certs::load_native_certs();
    for error in &native.errors {
        tracing::warn!(%error, "a system root certificate source could not be loaded");
    }
    let (_native_added, _native_ignored) = roots.add_parsable_certificates(native.certs);
    if let Some(ca_file) = ca_file {
        let bundle = std::fs::read(ca_file).map_err(|source| AuthenticationSetupError::CaFile {
            path: ca_file.to_path_buf(),
            detail: source.to_string(),
        })?;
        let certificates = CertificateDer::pem_slice_iter(&bundle)
            .collect::<Result<Vec<_>, _>>()
            .map_err(|error| AuthenticationSetupError::CaFile {
                path: ca_file.to_path_buf(),
                detail: error.to_string(),
            })?;
        let (added, _ignored) = roots.add_parsable_certificates(certificates);
        if added == 0 {
            return Err(AuthenticationSetupError::CaFile {
                path: ca_file.to_path_buf(),
                detail: "the file holds no PEM certificate".to_owned(),
            });
        }
    }
    if roots.is_empty() {
        tracing::warn!("no root certificates are available; HTTPS key set requests will fail");
    }
    Ok(roots)
}
