// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Compact JWS parsing and the signature and claim checks of the Go Server's JWT authenticator.

use std::sync::Arc;
use std::time::Duration;

use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use dexzd_clock::WallClock;
use serde::Deserialize;

use super::JwtAuthenticatorConfig;
use super::json_web_key_set::JsonWebKeySetCache;

/// Clock skew tolerated for `exp`, `nbf`, and `iat`, as in the Go Server.
pub(crate) const JWT_CLOCK_SKEW_LEEWAY: Duration = Duration::from_secs(30);

/// The signature algorithms the Go Server accepts. Every other `alg`, including `none` and HMAC, is rejected.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum JwsAlgorithm {
    Rs256,
    Rs384,
    Rs512,
    Ps256,
    Ps384,
    Ps512,
    Es256,
    Es384,
    Es512,
    EdDsa,
}

impl JwsAlgorithm {
    pub(crate) const ALL: [Self; 10] = [
        Self::Rs256,
        Self::Rs384,
        Self::Rs512,
        Self::Ps256,
        Self::Ps384,
        Self::Ps512,
        Self::Es256,
        Self::Es384,
        Self::Es512,
        Self::EdDsa,
    ];

    /// Returns the algorithm a JOSE header names, or `None` for an unsupported one.
    pub(crate) fn from_name(name: &str) -> Option<Self> {
        Self::ALL
            .into_iter()
            .find(|algorithm| algorithm.name() == name)
    }

    /// Returns the JOSE `alg` value.
    pub(crate) fn name(self) -> &'static str {
        match self {
            Self::Rs256 => "RS256",
            Self::Rs384 => "RS384",
            Self::Rs512 => "RS512",
            Self::Ps256 => "PS256",
            Self::Ps384 => "PS384",
            Self::Ps512 => "PS512",
            Self::Es256 => "ES256",
            Self::Es384 => "ES384",
            Self::Es512 => "ES512",
            Self::EdDsa => "EdDSA",
        }
    }
}

/// Why a JWT from a configured issuer was rejected; logged at debug level, never returned to callers.
#[derive(Clone, Copy, Debug, PartialEq, Eq, thiserror::Error)]
pub(crate) enum JwtRejection {
    #[error("the alg header is not an accepted signature algorithm")]
    UnsupportedAlgorithm,
    #[error("the token declares critical header extensions")]
    CriticalHeader,
    #[error("the kid header is missing")]
    MissingKeyId,
    #[error("the key set has no key with the token's kid")]
    UnknownKeyId,
    #[error("the key set holds several, private, or unsupported keys under the token's kid")]
    UnusableKey,
    #[error("the key does not match the token's alg")]
    KeyAlgorithmMismatch,
    #[error("the signature is not valid")]
    InvalidSignature,
    #[error("the iss claim does not match the issuer")]
    IssuerMismatch,
    #[error("the exp claim is missing")]
    MissingExpiry,
    #[error("the aud claim names no accepted audience")]
    AudienceMismatch,
    #[error("the token has expired")]
    Expired,
    #[error("the token is not valid yet")]
    NotYetValid,
    #[error("the token was issued in the future")]
    IssuedInFuture,
}

#[derive(Debug, Deserialize)]
struct JoseHeader {
    alg: String,
    #[serde(default)]
    kid: Option<String>,
    #[serde(default)]
    crit: Option<serde_json::Value>,
}

#[derive(Debug, Deserialize)]
struct RegisteredClaims {
    #[serde(default)]
    iss: Option<String>,
    #[serde(default)]
    aud: Option<Audience>,
    #[serde(default)]
    exp: Option<f64>,
    #[serde(default)]
    nbf: Option<f64>,
    #[serde(default)]
    iat: Option<f64>,
}

#[derive(Debug, Deserialize)]
#[serde(untagged)]
enum Audience {
    One(String),
    Many(Vec<String>),
}

impl Audience {
    fn contains(&self, audience: &str) -> bool {
        match self {
            Self::One(value) => value == audience,
            Self::Many(values) => values.iter().any(|value| value == audience),
        }
    }
}

/// A compact JWS whose header and claims decoded, before any signature or claim check.
#[derive(Debug)]
pub(crate) struct UnverifiedJsonWebToken<'credential> {
    signing_input: &'credential str,
    signature: Vec<u8>,
    header: JoseHeader,
    claims: RegisteredClaims,
}

impl<'credential> UnverifiedJsonWebToken<'credential> {
    /// Decodes a compact JWS.
    ///
    /// Returns `None` for any credential that is not three base64url segments with a JSON header
    /// and JSON registered claims; only static tokens can match such a credential.
    pub(crate) fn parse(credential: &'credential str) -> Option<Self> {
        let mut segments = credential.split('.');
        let (Some(header), Some(claims), Some(signature), None) = (
            segments.next(),
            segments.next(),
            segments.next(),
            segments.next(),
        ) else {
            return None;
        };
        let signing_input = credential.get(..header.len() + 1 + claims.len())?;
        Some(Self {
            signing_input,
            signature: decode_base64_url(signature)?,
            header: serde_json::from_slice(&decode_base64_url(header)?).ok()?,
            claims: serde_json::from_slice(&decode_base64_url(claims)?).ok()?,
        })
    }

    /// Returns the unverified `iss` claim, which selects the JWT authenticator.
    pub(crate) fn issuer(&self) -> Option<&str> {
        self.claims.iss.as_deref()
    }

    /// Checks issuer, expiry presence, audience, and the validity period with the leeway, in the Go Server's order.
    fn validate_claims(
        &self,
        issuer: &str,
        audiences: &[String],
        now_unix_seconds: f64,
    ) -> Result<(), JwtRejection> {
        if self.claims.iss.as_deref() != Some(issuer) {
            return Err(JwtRejection::IssuerMismatch);
        }
        let expiry = self.claims.exp.ok_or(JwtRejection::MissingExpiry)?;
        let names_accepted_audience =
            self.claims.aud.as_ref().is_some_and(|audience| {
                audiences.iter().any(|accepted| audience.contains(accepted))
            });
        if !names_accepted_audience {
            return Err(JwtRejection::AudienceMismatch);
        }
        let leeway = JWT_CLOCK_SKEW_LEEWAY.as_secs_f64();
        // The Go Server truncates fractional NumericDate values to whole seconds.
        if self
            .claims
            .nbf
            .is_some_and(|not_before| now_unix_seconds + leeway < not_before.trunc())
        {
            return Err(JwtRejection::NotYetValid);
        }
        if now_unix_seconds - leeway > expiry.trunc() {
            return Err(JwtRejection::Expired);
        }
        if self
            .claims
            .iat
            .is_some_and(|issued_at| now_unix_seconds + leeway < issued_at.trunc())
        {
            return Err(JwtRejection::IssuedInFuture);
        }
        Ok(())
    }
}

/// Verifies JWTs of one issuer against its cached key set.
pub(crate) struct JwtVerifier {
    config: JwtAuthenticatorConfig,
    key_set: Arc<JsonWebKeySetCache>,
    clock: Arc<dyn WallClock>,
}

impl JwtVerifier {
    pub(crate) fn new(
        config: JwtAuthenticatorConfig,
        key_set: JsonWebKeySetCache,
        clock: Arc<dyn WallClock>,
    ) -> Self {
        Self {
            config,
            key_set: Arc::new(key_set),
            clock,
        }
    }

    pub(super) fn for_audiences(&self, audiences: &[String]) -> Self {
        let mut config = self.config.clone();
        config.audiences = audiences.to_vec();
        Self {
            config,
            key_set: self.key_set.clone(),
            clock: self.clock.clone(),
        }
    }

    /// Returns the exact issuer this verifier accepts.
    pub(crate) fn issuer(&self) -> &str {
        &self.config.issuer
    }

    /// Checks the algorithm, the key named by `kid`, the signature, and the claims.
    pub(crate) async fn verify(
        &self,
        token: &UnverifiedJsonWebToken<'_>,
    ) -> Result<(), JwtRejection> {
        let algorithm =
            JwsAlgorithm::from_name(&token.header.alg).ok_or(JwtRejection::UnsupportedAlgorithm)?;
        if token.header.crit.is_some() {
            return Err(JwtRejection::CriticalHeader);
        }
        let key_id = token
            .header
            .kid
            .as_deref()
            .filter(|key_id| !key_id.is_empty())
            .ok_or(JwtRejection::MissingKeyId)?;
        let key = self.key_set.verification_key(key_id).await?;
        key.verify(algorithm, token.signing_input.as_bytes(), &token.signature)?;
        let now_unix_seconds = self.clock.now_unix_micros() as f64 / 1_000_000.0;
        token.validate_claims(
            &self.config.issuer,
            &self.config.audiences,
            now_unix_seconds,
        )
    }
}

/// Decodes unpadded base64url, also accepting the trailing padding the Go Server strips.
pub(crate) fn decode_base64_url(text: &str) -> Option<Vec<u8>> {
    URL_SAFE_NO_PAD.decode(text.trim_end_matches('=')).ok()
}
