// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! JSON Web Key Set parsing and the rate-limited key cache of the Go Server's JWT authenticator.

use std::collections::BTreeMap;
use std::collections::btree_map::Entry;
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Duration;

use async_trait::async_trait;
use aws_lc_rs::signature::{
    self, EcdsaVerificationAlgorithm, RsaParameters, RsaPublicKeyComponents, UnparsedPublicKey,
};
use serde::Deserialize;
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio::time::Instant;

use super::json_web_token::{JwsAlgorithm, JwtRejection, decode_base64_url};

/// How often a key set is refreshed, as in the Go Server.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct KeySetRefreshPolicy {
    /// Minimum time between refreshes while every requested key is known.
    pub(crate) refresh_interval: Duration,
    /// Minimum time between refreshes caused by an unknown `kid`.
    pub(crate) unknown_key_refresh_interval: Duration,
    /// Bound on one refresh, including discovery; requests wait at most this long for keys.
    pub(crate) fetch_timeout: Duration,
}

impl KeySetRefreshPolicy {
    /// The Go Server's limits.
    pub(crate) const STANDARD: Self = Self {
        refresh_interval: Duration::from_secs(60),
        unknown_key_refresh_interval: Duration::from_secs(10),
        fetch_timeout: Duration::from_secs(5),
    };
}

/// Fetches the current key set of one issuer.
#[async_trait]
pub(crate) trait KeySetSource: Send + Sync + 'static {
    async fn fetch_key_set(&self) -> Result<KeySet, KeySetFetchError>;
}

/// Why a key set refresh failed; the previous keys stay in use.
#[derive(Debug, thiserror::Error)]
pub(crate) enum KeySetFetchError {
    #[error("{url} is not a valid request URL")]
    InvalidUrl { url: String },
    #[error("GET {url}: {detail}")]
    Request { url: String, detail: String },
    #[error("GET {url}: HTTP {status}")]
    UnexpectedStatus { url: String, status: u16 },
    #[error("GET {url}: the response is not valid JSON: {source}")]
    InvalidJson {
        url: String,
        #[source]
        source: serde_json::Error,
    },
    #[error("the OIDC discovery document names issuer {discovered:?}")]
    DiscoveryIssuerMismatch { discovered: String },
    #[error("the OIDC discovery document's jwks_uri {error}")]
    DiscoveredJwksUrl {
        #[source]
        error: super::EndpointUrlError,
    },
}

/// A public key usable for one key family.
#[derive(Debug, PartialEq, Eq)]
enum PublicKeyMaterial {
    Rsa {
        modulus: Vec<u8>,
        exponent: Vec<u8>,
    },
    EllipticCurve {
        curve: EllipticCurve,
        uncompressed_point: Vec<u8>,
    },
    Ed25519 {
        public_key: Vec<u8>,
    },
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum EllipticCurve {
    P256,
    P384,
    P521,
}

impl EllipticCurve {
    fn from_name(name: &str) -> Option<Self> {
        match name {
            "P-256" => Some(Self::P256),
            "P-384" => Some(Self::P384),
            "P-521" => Some(Self::P521),
            _ => None,
        }
    }

    fn coordinate_length(self) -> usize {
        match self {
            Self::P256 => 32,
            Self::P384 => 48,
            Self::P521 => 66,
        }
    }
}

/// A public JSON Web Key that verifies signatures.
#[derive(Debug, PartialEq, Eq)]
pub(crate) struct VerificationKey {
    material: PublicKeyMaterial,
    /// The key's `alg` member; when present, the token's `alg` must equal it.
    algorithm: Option<String>,
}

impl VerificationKey {
    /// Verifies `signature` over `message`; RSA keys need 2048 to 8192 bits.
    pub(crate) fn verify(
        &self,
        algorithm: JwsAlgorithm,
        message: &[u8],
        signature: &[u8],
    ) -> Result<(), JwtRejection> {
        if self
            .algorithm
            .as_deref()
            .is_some_and(|key_algorithm| key_algorithm != algorithm.name())
        {
            return Err(JwtRejection::KeyAlgorithmMismatch);
        }
        let verified = match &self.material {
            PublicKeyMaterial::Rsa { modulus, exponent } => {
                let parameters =
                    rsa_parameters(algorithm).ok_or(JwtRejection::KeyAlgorithmMismatch)?;
                RsaPublicKeyComponents {
                    n: modulus,
                    e: exponent,
                }
                .verify(parameters, message, signature)
            }
            PublicKeyMaterial::EllipticCurve {
                curve,
                uncompressed_point,
            } => {
                let verification =
                    ecdsa_algorithm(algorithm, *curve).ok_or(JwtRejection::KeyAlgorithmMismatch)?;
                UnparsedPublicKey::new(verification, uncompressed_point).verify(message, signature)
            }
            PublicKeyMaterial::Ed25519 { public_key } => {
                if algorithm != JwsAlgorithm::EdDsa {
                    return Err(JwtRejection::KeyAlgorithmMismatch);
                }
                UnparsedPublicKey::new(&signature::ED25519, public_key).verify(message, signature)
            }
        };
        verified.map_err(|_unspecified| JwtRejection::InvalidSignature)
    }
}

fn rsa_parameters(algorithm: JwsAlgorithm) -> Option<&'static RsaParameters> {
    match algorithm {
        JwsAlgorithm::Rs256 => Some(&signature::RSA_PKCS1_2048_8192_SHA256),
        JwsAlgorithm::Rs384 => Some(&signature::RSA_PKCS1_2048_8192_SHA384),
        JwsAlgorithm::Rs512 => Some(&signature::RSA_PKCS1_2048_8192_SHA512),
        JwsAlgorithm::Ps256 => Some(&signature::RSA_PSS_2048_8192_SHA256),
        JwsAlgorithm::Ps384 => Some(&signature::RSA_PSS_2048_8192_SHA384),
        JwsAlgorithm::Ps512 => Some(&signature::RSA_PSS_2048_8192_SHA512),
        JwsAlgorithm::Es256 | JwsAlgorithm::Es384 | JwsAlgorithm::Es512 | JwsAlgorithm::EdDsa => {
            None
        }
    }
}

fn ecdsa_algorithm(
    algorithm: JwsAlgorithm,
    curve: EllipticCurve,
) -> Option<&'static EcdsaVerificationAlgorithm> {
    match (algorithm, curve) {
        (JwsAlgorithm::Es256, EllipticCurve::P256) => Some(&signature::ECDSA_P256_SHA256_FIXED),
        (JwsAlgorithm::Es384, EllipticCurve::P384) => Some(&signature::ECDSA_P384_SHA384_FIXED),
        (JwsAlgorithm::Es512, EllipticCurve::P521) => Some(&signature::ECDSA_P521_SHA512_FIXED),
        _ => None,
    }
}

/// What a key set holds under one `kid`.
#[derive(Debug)]
enum KeySetEntry {
    Usable(Arc<VerificationKey>),
    /// Several keys, a private key, or a key this server cannot use.
    Unusable,
}

/// What a lookup by `kid` found.
#[derive(Debug)]
pub(crate) enum KeyLookup {
    Usable(Arc<VerificationKey>),
    Unusable,
    Absent,
}

/// The keys of one key set document, by `kid`. Keys without a `kid` are ignored.
#[derive(Debug, Default)]
pub(crate) struct KeySet {
    entries: BTreeMap<String, KeySetEntry>,
}

#[derive(Deserialize)]
struct KeySetDocument {
    #[serde(default)]
    keys: Vec<serde_json::Value>,
}

#[derive(Deserialize)]
struct JsonWebKeyMembers {
    kty: String,
    #[serde(default)]
    alg: Option<String>,
    #[serde(default, rename = "use")]
    public_key_use: Option<String>,
    #[serde(default)]
    crv: Option<String>,
    #[serde(default)]
    n: Option<String>,
    #[serde(default)]
    e: Option<String>,
    #[serde(default)]
    x: Option<String>,
    #[serde(default)]
    y: Option<String>,
    #[serde(default)]
    d: Option<String>,
}

impl KeySet {
    /// Parses a key set document; malformed or unsupported keys are unusable rather than fatal.
    pub(crate) fn parse(document: &[u8]) -> Result<Self, serde_json::Error> {
        let document: KeySetDocument = serde_json::from_slice(document)?;
        let mut entries = BTreeMap::new();
        for value in document.keys {
            let Some(key_id) = value.get("kid").and_then(serde_json::Value::as_str) else {
                continue;
            };
            match entries.entry(key_id.to_owned()) {
                Entry::Occupied(mut repeated) => {
                    repeated.insert(KeySetEntry::Unusable);
                }
                Entry::Vacant(first) => {
                    let entry = match parse_verification_key(value) {
                        Some(key) => KeySetEntry::Usable(Arc::new(key)),
                        None => {
                            tracing::debug!(
                                key_id = %first.key(),
                                "a JWT key set entry is not a usable public signing key"
                            );
                            KeySetEntry::Unusable
                        }
                    };
                    first.insert(entry);
                }
            }
        }
        Ok(Self { entries })
    }

    pub(crate) fn lookup(&self, key_id: &str) -> KeyLookup {
        match self.entries.get(key_id) {
            Some(KeySetEntry::Usable(key)) => KeyLookup::Usable(Arc::clone(key)),
            Some(KeySetEntry::Unusable) => KeyLookup::Unusable,
            None => KeyLookup::Absent,
        }
    }
}

/// Returns `None` for private, encryption, malformed, or unsupported keys.
fn parse_verification_key(value: serde_json::Value) -> Option<VerificationKey> {
    let members: JsonWebKeyMembers = serde_json::from_value(value).ok()?;
    if members.d.is_some()
        || members
            .public_key_use
            .as_deref()
            .is_some_and(|public_key_use| public_key_use != "sig")
    {
        return None;
    }
    let material = match members.kty.as_str() {
        "RSA" => PublicKeyMaterial::Rsa {
            modulus: big_endian_integer(&members.n?)?,
            exponent: big_endian_integer(&members.e?)?,
        },
        "EC" => {
            let curve = EllipticCurve::from_name(members.crv.as_deref()?)?;
            let x = decode_base64_url(&members.x?)?;
            let y = decode_base64_url(&members.y?)?;
            if x.len() != curve.coordinate_length() || y.len() != curve.coordinate_length() {
                return None;
            }
            let mut uncompressed_point = Vec::with_capacity(1 + x.len() + y.len());
            uncompressed_point.push(0x04);
            uncompressed_point.extend_from_slice(&x);
            uncompressed_point.extend_from_slice(&y);
            PublicKeyMaterial::EllipticCurve {
                curve,
                uncompressed_point,
            }
        }
        "OKP" if members.crv.as_deref() == Some("Ed25519") => {
            let public_key = decode_base64_url(&members.x?)?;
            if public_key.len() != signature::ED25519_PUBLIC_KEY_LEN {
                return None;
            }
            PublicKeyMaterial::Ed25519 { public_key }
        }
        _ => return None,
    };
    Some(VerificationKey {
        material,
        algorithm: members.alg,
    })
}

/// Decodes an RSA parameter, dropping the leading zero bytes some issuers emit.
fn big_endian_integer(text: &str) -> Option<Vec<u8>> {
    let bytes = decode_base64_url(text)?;
    let first_significant = bytes.iter().position(|byte| *byte != 0)?;
    Some(bytes[first_significant..].to_vec())
}

/// Caches one issuer's keys and refreshes them on a task it owns.
///
/// A due refresh starts when a request looks up a key: once a minute, or after ten seconds for an
/// unknown `kid`. A request for an unknown `kid` waits for the running refresh, at most the fetch
/// timeout; a request for a known key never waits. A failed refresh keeps the previous keys.
pub(crate) struct JsonWebKeySetCache {
    shared: Arc<KeySetCacheShared>,
}

struct KeySetCacheShared {
    issuer: String,
    source: Arc<dyn KeySetSource>,
    policy: KeySetRefreshPolicy,
    state: Mutex<KeySetCacheState>,
    /// Counts finished refreshes so waiting requests wake up.
    refresh_completions: watch::Sender<u64>,
}

struct KeySetCacheState {
    keys: Arc<KeySet>,
    last_refresh_attempt: Option<Instant>,
    is_refreshing: bool,
    refresh: Option<JoinHandle<()>>,
}

impl JsonWebKeySetCache {
    /// Creates an empty cache; the first lookup fetches the keys.
    pub(crate) fn new(
        issuer: String,
        source: Arc<dyn KeySetSource>,
        policy: KeySetRefreshPolicy,
    ) -> Self {
        let (refresh_completions, _initial_receiver) = watch::channel(0);
        Self {
            shared: Arc::new(KeySetCacheShared {
                issuer,
                source,
                policy,
                state: Mutex::new(KeySetCacheState {
                    keys: Arc::new(KeySet::default()),
                    last_refresh_attempt: None,
                    is_refreshing: false,
                    refresh: None,
                }),
                refresh_completions,
            }),
        }
    }

    /// Returns the usable key named `key_id`, refreshing the key set when a refresh is due.
    pub(crate) async fn verification_key(
        &self,
        key_id: &str,
    ) -> Result<Arc<VerificationKey>, JwtRejection> {
        let mut completions = {
            let mut state = self.shared.lock_state();
            let lookup = state.keys.lookup(key_id);
            let is_absent = matches!(lookup, KeyLookup::Absent);
            if !state.is_refreshing && self.shared.is_refresh_due(&state, is_absent) {
                self.start_refresh(&mut state);
            }
            match lookup {
                KeyLookup::Usable(key) => return Ok(key),
                KeyLookup::Unusable => return Err(JwtRejection::UnusableKey),
                KeyLookup::Absent if !state.is_refreshing => {
                    return Err(JwtRejection::UnknownKeyId);
                }
                KeyLookup::Absent => self.shared.refresh_completions.subscribe(),
            }
        };
        match tokio::time::timeout(self.shared.policy.fetch_timeout, completions.changed()).await {
            Ok(Ok(())) => {}
            // The sender lives as long as the cache; a timeout leaves the key unknown.
            Ok(Err(_)) | Err(_) => return Err(JwtRejection::UnknownKeyId),
        }
        match self.shared.lock_state().keys.lookup(key_id) {
            KeyLookup::Usable(key) => Ok(key),
            KeyLookup::Unusable => Err(JwtRejection::UnusableKey),
            KeyLookup::Absent => Err(JwtRejection::UnknownKeyId),
        }
    }

    fn start_refresh(&self, state: &mut KeySetCacheState) {
        state.last_refresh_attempt = Some(Instant::now());
        state.is_refreshing = true;
        let shared = Arc::clone(&self.shared);
        let refresh = tokio::spawn(async move { shared.refresh().await });
        state.refresh = Some(refresh);
    }
}

impl Drop for JsonWebKeySetCache {
    fn drop(&mut self) {
        let mut state = match self.shared.state.lock() {
            Ok(state) => state,
            Err(poisoned) => poisoned.into_inner(),
        };
        if let Some(refresh) = state.refresh.take() {
            refresh.abort();
        }
    }
}

impl KeySetCacheShared {
    fn lock_state(&self) -> MutexGuard<'_, KeySetCacheState> {
        self.state
            .lock()
            .expect("key set cache lock is poisoned only if a holder panicked")
    }

    fn is_refresh_due(&self, state: &KeySetCacheState, is_key_absent: bool) -> bool {
        let Some(last_attempt) = state.last_refresh_attempt else {
            return true;
        };
        let since_last_attempt = Instant::now().saturating_duration_since(last_attempt);
        since_last_attempt >= self.policy.refresh_interval
            || (is_key_absent && since_last_attempt >= self.policy.unknown_key_refresh_interval)
    }

    async fn refresh(&self) {
        let fetched =
            tokio::time::timeout(self.policy.fetch_timeout, self.source.fetch_key_set()).await;
        {
            let mut state = self.lock_state();
            match fetched {
                Ok(Ok(keys)) => state.keys = Arc::new(keys),
                Ok(Err(error)) => tracing::error!(
                    issuer = %self.issuer,
                    %error,
                    "refreshing a JWT key set failed; keeping the previous keys"
                ),
                Err(_elapsed) => tracing::error!(
                    issuer = %self.issuer,
                    timeout_ms = self.policy.fetch_timeout.as_millis(),
                    "refreshing a JWT key set timed out; keeping the previous keys"
                ),
            }
            state.is_refreshing = false;
        }
        self.refresh_completions
            .send_modify(|completions| *completions = completions.wrapping_add(1));
    }
}
