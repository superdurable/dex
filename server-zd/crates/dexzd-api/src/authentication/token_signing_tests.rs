// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Test signing keys, JWT minting, a manual wall clock, and a scripted key set source.

use std::sync::atomic::{AtomicBool, AtomicI64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use aws_lc_rs::rand::SystemRandom;
use aws_lc_rs::rsa::KeySize;
use aws_lc_rs::signature::{
    self, EcdsaKeyPair, EcdsaSigningAlgorithm, Ed25519KeyPair, KeyPair, RsaEncoding, RsaKeyPair,
};
use base64::Engine;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use dexzd_clock::WallClock;
use serde_json::{Value, json};

use super::json_web_key_set::{KeySet, KeySetFetchError, KeySetSource};
use super::json_web_token::JwsAlgorithm;

/// Issuer of the tokens minted by these helpers.
pub(super) const TEST_ISSUER: &str = "https://issuer.example.com";
/// Audience of the tokens minted by these helpers.
pub(super) const TEST_AUDIENCE: &str = "dex";
/// Wall-clock start of every manual clock, 2026-10-24T00:00:00Z.
pub(super) const TEST_START_UNIX_SECONDS: i64 = 1_792_800_000;

enum TestKeyPair {
    Rsa(RsaKeyPair),
    Ecdsa(EcdsaKeyPair),
    Ed25519(Ed25519KeyPair),
}

/// A private key that signs JWTs with one algorithm.
pub(super) struct TestSigningKey {
    algorithm: JwsAlgorithm,
    key_pair: TestKeyPair,
}

impl TestSigningKey {
    pub(super) fn generate(algorithm: JwsAlgorithm) -> Self {
        let key_pair = match algorithm {
            JwsAlgorithm::Rs256
            | JwsAlgorithm::Rs384
            | JwsAlgorithm::Rs512
            | JwsAlgorithm::Ps256
            | JwsAlgorithm::Ps384
            | JwsAlgorithm::Ps512 => TestKeyPair::Rsa(
                RsaKeyPair::generate(KeySize::Rsa2048).expect("an RSA test key is generated"),
            ),
            JwsAlgorithm::Es256 | JwsAlgorithm::Es384 | JwsAlgorithm::Es512 => TestKeyPair::Ecdsa(
                EcdsaKeyPair::generate(ecdsa_signing_algorithm(algorithm))
                    .expect("an ECDSA test key is generated"),
            ),
            JwsAlgorithm::EdDsa => TestKeyPair::Ed25519(
                Ed25519KeyPair::generate().expect("an Ed25519 test key is generated"),
            ),
        };
        Self {
            algorithm,
            key_pair,
        }
    }

    /// Returns the public JWK, with `alg` and `use: sig`, under `key_id`.
    pub(super) fn public_jwk(&self, key_id: &str) -> Value {
        let mut jwk = match &self.key_pair {
            TestKeyPair::Rsa(key_pair) => {
                let (modulus, exponent) = rsa_public_key_components(key_pair.public_key().as_ref());
                json!({"kty": "RSA", "n": encode(modulus), "e": encode(exponent)})
            }
            TestKeyPair::Ecdsa(key_pair) => {
                let point = key_pair.public_key().as_ref();
                let coordinate_length = (point.len() - 1) / 2;
                let curve = match self.algorithm {
                    JwsAlgorithm::Es256 => "P-256",
                    JwsAlgorithm::Es384 => "P-384",
                    _ => "P-521",
                };
                json!({
                    "kty": "EC",
                    "crv": curve,
                    "x": encode(&point[1..=coordinate_length]),
                    "y": encode(&point[1 + coordinate_length..]),
                })
            }
            TestKeyPair::Ed25519(key_pair) => {
                json!({"kty": "OKP", "crv": "Ed25519", "x": encode(key_pair.public_key().as_ref())})
            }
        };
        jwk["kid"] = json!(key_id);
        jwk["alg"] = json!(self.algorithm.name());
        jwk["use"] = json!("sig");
        jwk
    }

    /// Signs `claims` under a header with this key's `alg`, `typ: JWT`, and `kid`.
    pub(super) fn token(&self, key_id: &str, claims: &Value) -> String {
        self.token_with_header(
            &json!({"alg": self.algorithm.name(), "typ": "JWT", "kid": key_id}),
            claims,
        )
    }

    /// Signs `claims` under an arbitrary header with this key's algorithm.
    pub(super) fn token_with_header(&self, header: &Value, claims: &Value) -> String {
        let signing_input = format!(
            "{}.{}",
            encode(header.to_string().as_bytes()),
            encode(claims.to_string().as_bytes())
        );
        let signature = self.sign(signing_input.as_bytes());
        format!("{signing_input}.{}", encode(&signature))
    }

    fn sign(&self, message: &[u8]) -> Vec<u8> {
        let random = SystemRandom::new();
        match &self.key_pair {
            TestKeyPair::Rsa(key_pair) => {
                let mut signature = vec![0; key_pair.public_modulus_len()];
                key_pair
                    .sign(
                        rsa_encoding(self.algorithm),
                        &random,
                        message,
                        &mut signature,
                    )
                    .expect("RSA signing succeeds");
                signature
            }
            TestKeyPair::Ecdsa(key_pair) => key_pair
                .sign(&random, message)
                .expect("ECDSA signing succeeds")
                .as_ref()
                .to_vec(),
            TestKeyPair::Ed25519(key_pair) => key_pair.sign(message).as_ref().to_vec(),
        }
    }
}

/// Splits a DER `RSAPublicKey` (PKCS #1) into its modulus and public exponent.
fn rsa_public_key_components(der: &[u8]) -> (&[u8], &[u8]) {
    let (sequence, _) = der_element(der, 0x30);
    let (modulus, rest) = der_element(sequence, 0x02);
    let (exponent, _) = der_element(rest, 0x02);
    (strip_leading_zeros(modulus), strip_leading_zeros(exponent))
}

/// Returns the content of the element with `tag` at the start of `input`, and what follows it.
fn der_element(input: &[u8], tag: u8) -> (&[u8], &[u8]) {
    assert_eq!(input[0], tag, "unexpected DER tag");
    let (length, header_length) = if input[1] < 0x80 {
        (usize::from(input[1]), 2)
    } else {
        let length_bytes = usize::from(input[1] & 0x7f);
        let length = input[2..2 + length_bytes]
            .iter()
            .fold(0, |length, byte| length << 8 | usize::from(*byte));
        (length, 2 + length_bytes)
    };
    input[header_length..].split_at(length)
}

fn strip_leading_zeros(integer: &[u8]) -> &[u8] {
    let first_significant = integer
        .iter()
        .position(|byte| *byte != 0)
        .unwrap_or(integer.len());
    &integer[first_significant..]
}

fn ecdsa_signing_algorithm(algorithm: JwsAlgorithm) -> &'static EcdsaSigningAlgorithm {
    match algorithm {
        JwsAlgorithm::Es256 => &signature::ECDSA_P256_SHA256_FIXED_SIGNING,
        JwsAlgorithm::Es384 => &signature::ECDSA_P384_SHA384_FIXED_SIGNING,
        _ => &signature::ECDSA_P521_SHA512_FIXED_SIGNING,
    }
}

fn rsa_encoding(algorithm: JwsAlgorithm) -> &'static dyn RsaEncoding {
    match algorithm {
        JwsAlgorithm::Rs256 => &signature::RSA_PKCS1_SHA256,
        JwsAlgorithm::Rs384 => &signature::RSA_PKCS1_SHA384,
        JwsAlgorithm::Rs512 => &signature::RSA_PKCS1_SHA512,
        JwsAlgorithm::Ps256 => &signature::RSA_PSS_SHA256,
        JwsAlgorithm::Ps384 => &signature::RSA_PSS_SHA384,
        _ => &signature::RSA_PSS_SHA512,
    }
}

/// Encodes bytes as unpadded base64url.
pub(super) fn encode(bytes: &[u8]) -> String {
    URL_SAFE_NO_PAD.encode(bytes)
}

/// Claims accepted at the manual clock's start: one hour of validity for the test audience.
pub(super) fn valid_claims() -> Value {
    json!({
        "iss": TEST_ISSUER,
        "sub": "caller",
        "aud": TEST_AUDIENCE,
        "iat": TEST_START_UNIX_SECONDS,
        "exp": TEST_START_UNIX_SECONDS + 3600,
    })
}

/// A key set document listing `keys`.
pub(super) fn key_set_document(keys: &[Value]) -> Value {
    json!({ "keys": keys })
}

/// A wall clock that tests move by hand.
pub(super) struct ManualWallClock {
    now_unix_micros: AtomicI64,
}

impl ManualWallClock {
    pub(super) fn starting_at_test_start() -> Arc<Self> {
        Arc::new(Self {
            now_unix_micros: AtomicI64::new(TEST_START_UNIX_SECONDS * 1_000_000),
        })
    }

    pub(super) fn advance(&self, duration: Duration) {
        let micros = i64::try_from(duration.as_micros()).expect("test durations fit in i64");
        self.now_unix_micros.fetch_add(micros, Ordering::SeqCst);
    }
}

impl WallClock for ManualWallClock {
    fn now_unix_micros(&self) -> i64 {
        self.now_unix_micros.load(Ordering::SeqCst)
    }
}

/// A key set source that serves a replaceable document, counts fetches, and can fail or stall.
pub(super) struct ScriptedKeySetSource {
    document: Mutex<Value>,
    fetches: AtomicUsize,
    is_failing: AtomicBool,
    delay: Mutex<Duration>,
}

impl ScriptedKeySetSource {
    pub(super) fn serving(document: Value) -> Arc<Self> {
        Arc::new(Self {
            document: Mutex::new(document),
            fetches: AtomicUsize::new(0),
            is_failing: AtomicBool::new(false),
            delay: Mutex::new(Duration::ZERO),
        })
    }

    pub(super) fn replace_document(&self, document: Value) {
        *self.document.lock().expect("the test document lock") = document;
    }

    pub(super) fn set_failing(&self, is_failing: bool) {
        self.is_failing.store(is_failing, Ordering::SeqCst);
    }

    pub(super) fn set_delay(&self, delay: Duration) {
        *self.delay.lock().expect("the test delay lock") = delay;
    }

    pub(super) fn fetch_count(&self) -> usize {
        self.fetches.load(Ordering::SeqCst)
    }
}

#[async_trait]
impl KeySetSource for ScriptedKeySetSource {
    async fn fetch_key_set(&self) -> Result<KeySet, KeySetFetchError> {
        self.fetches.fetch_add(1, Ordering::SeqCst);
        let delay = *self.delay.lock().expect("the test delay lock");
        tokio::time::sleep(delay).await;
        if self.is_failing.load(Ordering::SeqCst) {
            return Err(KeySetFetchError::UnexpectedStatus {
                redacted_url: "https://issuer.example.com/keys".to_owned(),
                status: 503,
            });
        }
        let document = self
            .document
            .lock()
            .expect("the test document lock")
            .to_string();
        Ok(KeySet::parse(document.as_bytes()).expect("the test key set document parses"))
    }
}
