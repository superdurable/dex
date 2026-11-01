// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Public API authentication with JWTs and static bearer tokens, compatible with the Go Server.

mod admin_authorization;
mod authenticated_service;
mod json_web_key_set;
mod json_web_token;
mod key_set_fetcher;
mod static_token_file;
mod watched_file;

#[cfg(test)]
mod admin_authentication_tests;
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

pub(crate) use admin_authorization::AdminCallerLogFields;
pub use authenticated_service::AuthenticatedService;
pub use key_set_fetcher::{EndpointUrlError, check_endpoint_url};

use std::path::PathBuf;
use std::sync::Arc;

use aws_lc_rs::digest;
use dexzd_clock::WallClock;

use json_web_key_set::{JsonWebKeySetCache, KeySetRefreshPolicy};
use json_web_token::{JwtRejection, JwtVerifier, UnverifiedJsonWebToken};
use key_set_fetcher::{KeySetFetcher, redact_url_credentials};
use static_token_file::{SHA256_LENGTH, StaticTokenHashes};
use watched_file::WatchedFile;

/// Configures one public API authenticator.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum CredentialAuthenticatorConfig {
    /// Verifies signed JSON Web Tokens of one issuer.
    Jwt(JwtAuthenticatorConfig),
    /// Accepts bearer tokens whose SHA-256 hashes a hot-reloaded YAML file lists.
    StaticTokens {
        /// Go Server format file of SHA-256 token hashes, reloaded on change; must exist at startup.
        file: PathBuf,
    },
}

/// Verifies JWTs of one issuer against its JSON Web Key Set.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct JwtAuthenticatorConfig {
    /// Exact `iss` claim, an HTTPS URL or plain HTTP on loopback; OIDC discovery starts here.
    /// No default; the caller supplies the issuer URL.
    pub issuer: String,
    /// Accepted `aud` values; a token must name at least one.
    /// No default; the caller supplies accepted audiences. Empty accepts no public API token, as
    /// for an issuer that only authorizes AdminService calls.
    pub audiences: Vec<String>,
    /// `aud` values that make a token of this issuer an AdminService credential.
    /// No default; empty means this issuer never authorizes AdminService calls. Empty entries are
    /// ignored. Never give admin audiences to an issuer that lets callers choose `aud`.
    pub admin_audiences: Vec<String>,
    /// Key set URL; `None` reads `jwks_uri` from the issuer's OpenID configuration.
    /// No default; the caller supplies a URL or `None` for discovery.
    pub jwks_url: Option<String>,
    /// Permits a plain HTTP `jwks_url` on a host that is not loopback.
    /// No default; the caller chooses whether plain HTTP is allowed.
    pub allow_insecure_jwks_url: bool,
    /// PEM bundle added to the system roots for discovery and key set requests.
    /// No default; the caller supplies a PEM path or `None` for system roots.
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
    #[error("{field} {redacted_url:?} {error}")]
    EndpointUrl {
        /// `issuer` or `jwksURL`.
        field: &'static str,
        /// Rejected URL without userinfo, query, or fragment, which may carry credentials.
        redacted_url: String,
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

/// Who presented an accepted credential, for audit logs.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum CredentialIdentity {
    /// A static token, which names nobody.
    StaticToken,
    /// A verified JWT.
    Jwt {
        /// Verified `iss` claim.
        issuer: String,
        /// `sub` claim when it is a string.
        subject: Option<String>,
    },
}

enum CredentialAuthenticator {
    StaticTokens(WatchedFile<StaticTokenHashes>),
    Jwt(JwtVerifier),
}

/// Tries each authenticator in order; a JWT authenticator matching the token's issuer decides alone.
pub struct PublicApiAuthenticator {
    authenticators: Vec<CredentialAuthenticator>,
}

impl PublicApiAuthenticator {
    /// Loads static token files and JWT verifiers; key sets are fetched lazily, so issuers may be down.
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
                    let key_set = JsonWebKeySetCache::new(
                        redact_url_credentials(&jwt.issuer),
                        Arc::new(fetcher),
                        policy,
                    );
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

    /// Returns an authenticator of the JWT issuers that configure admin audiences, accepting only those.
    ///
    /// It shares their key set caches. Static tokens and issuers without admin audiences are dropped,
    /// so they never authorize admin RPCs.
    pub(crate) fn admin_authenticator(&self) -> Self {
        Self {
            authenticators: self
                .authenticators
                .iter()
                .filter_map(|authenticator| match authenticator {
                    CredentialAuthenticator::Jwt(verifier) => verifier
                        .for_admin_audiences()
                        .map(CredentialAuthenticator::Jwt),
                    CredentialAuthenticator::StaticTokens(_) => None,
                })
                .collect(),
        }
    }

    /// Accepts `credential` when an authenticator recognizes it, and returns who presented it.
    pub(crate) async fn authenticate(
        &self,
        credential: &str,
    ) -> Result<CredentialIdentity, CredentialRejection> {
        let credential_hash = sha256(credential);
        let token = UnverifiedJsonWebToken::parse(credential);
        for authenticator in &self.authenticators {
            match authenticator {
                CredentialAuthenticator::StaticTokens(file) => {
                    if file.content().contains(&credential_hash) {
                        return Ok(CredentialIdentity::StaticToken);
                    }
                }
                CredentialAuthenticator::Jwt(verifier) => {
                    let Some(token) = &token else {
                        continue;
                    };
                    if token.issuer() != Some(verifier.issuer()) {
                        continue;
                    }
                    verifier
                        .verify(token)
                        .await
                        .map_err(CredentialRejection::Jwt)?;
                    return Ok(CredentialIdentity::Jwt {
                        issuer: verifier.issuer().to_owned(),
                        subject: token.subject().map(str::to_owned),
                    });
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
            redacted_url: redact_url_credentials(&config.issuer),
            error,
        }
    })?;
    if let Some(jwks_url) = &config.jwks_url {
        check_endpoint_url(jwks_url, config.allow_insecure_jwks_url).map_err(|error| {
            AuthenticationSetupError::EndpointUrl {
                field: "jwksURL",
                redacted_url: redact_url_credentials(jwks_url),
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
