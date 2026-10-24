// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Public API authentication with JWTs and static bearer tokens, compatible with the Go Server.

mod authenticated_service;
mod json_web_key_set;
mod json_web_token;
mod key_set_fetcher;
mod static_token_file;
mod watched_file;

#[cfg(test)]
mod authenticated_service_tests;
#[cfg(test)]
mod jwt_verification_tests;
#[cfg(test)]
mod key_set_cache_tests;
#[cfg(test)]
mod key_set_fetcher_tests;
#[cfg(test)]
mod static_token_reload_tests;
#[cfg(test)]
mod token_signing_tests;

pub use authenticated_service::AuthenticatedService;
pub use key_set_fetcher::{EndpointUrlError, check_endpoint_url};

use std::path::PathBuf;
use std::sync::Arc;

use aws_lc_rs::digest;
use dexzd_clock::WallClock;

use json_web_key_set::{JsonWebKeySetCache, KeySetRefreshPolicy};
use json_web_token::{JwtRejection, JwtVerifier, UnverifiedJsonWebToken};
use key_set_fetcher::KeySetFetcher;
use static_token_file::{SHA256_LENGTH, StaticTokenHashes};
use watched_file::WatchedFile;

/// Configures one public API authenticator.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum CredentialAuthenticatorConfig {
    /// Verifies signed JSON Web Tokens of one issuer.
    Jwt(JwtAuthenticatorConfig),
    /// Accepts bearer tokens whose SHA-256 hashes a hot-reloaded YAML file lists.
    StaticTokens {
        /// File in the Go Server's format, `tokens: [{sha256: <64 hexadecimal characters>}]`.
        ///
        /// It must exist at startup. Changes are picked up within a second. A missing or empty
        /// file revokes every token; a malformed update keeps the last valid content.
        file: PathBuf,
    },
}

/// Verifies JWTs of one issuer against its JSON Web Key Set.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct JwtAuthenticatorConfig {
    /// Exact `iss` claim, an HTTPS URL or plain HTTP on loopback; OIDC discovery starts here.
    pub issuer: String,
    /// Accepted `aud` values; a token must name at least one.
    pub audiences: Vec<String>,
    /// Key set URL; `None` reads `jwks_uri` from the issuer's OpenID configuration.
    pub jwks_url: Option<String>,
    /// Permits a plain HTTP `jwks_url` on a host that is not loopback.
    pub allow_insecure_jwks_url: bool,
    /// PEM bundle added to the system roots for discovery and key set requests.
    pub ca_file: Option<PathBuf>,
}

/// Why the public API authenticator could not be built at startup.
#[derive(Debug, thiserror::Error)]
pub enum AuthenticationSetupError {
    /// A static token file is missing, unreadable, or malformed.
    #[error("static token file {}: {detail}", path.display())]
    StaticTokenFile {
        /// File path.
        path: PathBuf,
        /// What failed, without any token.
        detail: String,
    },
    /// A JWT issuer or key set URL is not acceptable.
    #[error("{field} {url:?} {error}")]
    EndpointUrl {
        /// `issuer` or `jwksURL`.
        field: &'static str,
        /// Rejected URL.
        url: String,
        /// Violated rule.
        #[source]
        error: EndpointUrlError,
    },
    /// The CA bundle could not be read or holds no certificate.
    #[error("caFile {}: {detail}", path.display())]
    CaFile {
        /// Bundle path.
        path: PathBuf,
        /// What failed.
        detail: String,
    },
    /// The TLS client for key set requests could not be built.
    #[error("building the key set TLS client: {source}")]
    Tls {
        /// TLS configuration error.
        #[source]
        source: rustls::Error,
    },
}

/// Why a credential was rejected; logged at debug level, never returned to callers.
#[derive(Debug, thiserror::Error)]
pub(crate) enum CredentialRejection {
    #[error("no authenticator recognizes the credential")]
    NotRecognized,
    #[error("JWT rejected: {0}")]
    Jwt(JwtRejection),
}

enum CredentialAuthenticator {
    StaticTokens(WatchedFile<StaticTokenHashes>),
    Jwt(JwtVerifier),
}

/// Verifies public API bearer credentials with the configured authenticators, in order.
///
/// Static token authenticators accept a credential whose SHA-256 hash they list. The first JWT
/// authenticator whose issuer equals the credential's unverified `iss` claim decides alone: a
/// failed check rejects the credential without trying later authenticators, as in the Go Server.
/// A JWT is accepted when its `alg` is RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384,
/// ES512, or EdDSA (Ed25519); its `kid` names exactly one public signing key whose `alg`, when
/// present, matches; the signature verifies; `iss` matches exactly; `aud` names an accepted
/// audience; and `exp`, which is required, `nbf`, and `iat` hold with a 30-second leeway.
pub struct PublicApiAuthenticator {
    authenticators: Vec<CredentialAuthenticator>,
}

impl PublicApiAuthenticator {
    /// Loads every static token file and prepares every JWT verifier.
    ///
    /// No key set is fetched yet: the first JWT from an issuer fetches its keys, so an unreachable
    /// issuer does not fail startup. With no configuration every credential is rejected; pass `None`
    /// to [`AuthenticatedService::new`] to disable authentication instead.
    ///
    /// # Errors
    ///
    /// Returns [`AuthenticationSetupError`] when a static token file is missing or malformed, a URL
    /// is not acceptable, or a CA bundle is unreadable or holds no certificate.
    pub fn new(
        configs: &[CredentialAuthenticatorConfig],
        clock: Arc<dyn WallClock>,
    ) -> Result<Self, AuthenticationSetupError> {
        Self::with_key_set_refresh_policy(configs, clock, KeySetRefreshPolicy::STANDARD)
    }

    fn with_key_set_refresh_policy(
        configs: &[CredentialAuthenticatorConfig],
        clock: Arc<dyn WallClock>,
        policy: KeySetRefreshPolicy,
    ) -> Result<Self, AuthenticationSetupError> {
        let mut authenticators = Vec::with_capacity(configs.len());
        for config in configs {
            authenticators.push(match config {
                CredentialAuthenticatorConfig::StaticTokens { file } => {
                    CredentialAuthenticator::StaticTokens(WatchedFile::open(file).map_err(
                        |error| AuthenticationSetupError::StaticTokenFile {
                            path: file.clone(),
                            detail: error.detail(),
                        },
                    )?)
                }
                CredentialAuthenticatorConfig::Jwt(jwt) => {
                    check_jwt_urls(jwt)?;
                    let fetcher = KeySetFetcher::new(jwt)?;
                    let key_set =
                        JsonWebKeySetCache::new(jwt.issuer.clone(), Arc::new(fetcher), policy);
                    CredentialAuthenticator::Jwt(JwtVerifier::new(
                        jwt.clone(),
                        key_set,
                        Arc::clone(&clock),
                    ))
                }
            });
        }
        Ok(Self { authenticators })
    }

    /// Accepts `credential` when an authenticator recognizes it.
    pub(crate) async fn authenticate(&self, credential: &str) -> Result<(), CredentialRejection> {
        let credential_hash = sha256(credential);
        let token = UnverifiedJsonWebToken::parse(credential);
        for authenticator in &self.authenticators {
            match authenticator {
                CredentialAuthenticator::StaticTokens(file) => {
                    if file.content().contains(&credential_hash) {
                        return Ok(());
                    }
                }
                CredentialAuthenticator::Jwt(verifier) => {
                    let Some(token) = &token else {
                        continue;
                    };
                    if token.issuer() != Some(verifier.issuer()) {
                        continue;
                    }
                    return verifier
                        .verify(token)
                        .await
                        .map_err(CredentialRejection::Jwt);
                }
            }
        }
        Err(CredentialRejection::NotRecognized)
    }
}

fn check_jwt_urls(config: &JwtAuthenticatorConfig) -> Result<(), AuthenticationSetupError> {
    check_endpoint_url(&config.issuer, false).map_err(|error| {
        AuthenticationSetupError::EndpointUrl {
            field: "issuer",
            url: config.issuer.clone(),
            error,
        }
    })?;
    if let Some(jwks_url) = &config.jwks_url {
        check_endpoint_url(jwks_url, config.allow_insecure_jwks_url).map_err(|error| {
            AuthenticationSetupError::EndpointUrl {
                field: "jwksURL",
                url: jwks_url.clone(),
                error,
            }
        })?;
    }
    Ok(())
}

fn sha256(credential: &str) -> [u8; SHA256_LENGTH] {
    digest::digest(&digest::SHA256, credential.as_bytes())
        .as_ref()
        .try_into()
        .expect("a SHA-256 digest is 32 bytes")
}
