// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! JWT signature, header, and claim checks against an in-memory key set.

use std::sync::Arc;
use std::time::Duration;

use serde_json::{Value, json};

use super::json_web_key_set::{JsonWebKeySetCache, KeySetRefreshPolicy};
use super::json_web_token::{JwsAlgorithm, JwtRejection, JwtVerifier, UnverifiedJsonWebToken};
use super::token_signing_tests::{
    ManualWallClock, ScriptedKeySetSource, TEST_AUDIENCE, TEST_ISSUER, TEST_START_UNIX_SECONDS,
    TestSigningKey, encode, key_set_document, valid_claims,
};
use super::{CredentialAuthenticator, CredentialRejection, JwtAuthenticatorConfig};
use super::{PublicApiAuthenticator, static_token_file::StaticTokenHashes};

const KEY_ID: &str = "signing-key";

fn verifier(source: Arc<ScriptedKeySetSource>, clock: Arc<ManualWallClock>) -> JwtVerifier {
    JwtVerifier::new(
        JwtAuthenticatorConfig {
            issuer: TEST_ISSUER.to_owned(),
            audiences: vec!["another-audience".to_owned(), TEST_AUDIENCE.to_owned()],
            jwks_url: None,
            allow_insecure_jwks_url: false,
            ca_file: None,
        },
        JsonWebKeySetCache::new(
            TEST_ISSUER.to_owned(),
            source,
            KeySetRefreshPolicy::STANDARD,
        ),
        clock,
    )
}

fn verifier_trusting(keys: &[Value]) -> (JwtVerifier, Arc<ManualWallClock>) {
    let clock = ManualWallClock::starting_at_test_start();
    let source = ScriptedKeySetSource::serving(key_set_document(keys));
    (verifier(source, Arc::clone(&clock)), clock)
}

async fn verify(verifier: &JwtVerifier, token: &str) -> Result<(), JwtRejection> {
    let parsed = UnverifiedJsonWebToken::parse(token).expect("the test token is a compact JWS");
    verifier.verify(&parsed).await
}

fn claims_with(changes: &[(&str, Value)]) -> Value {
    let mut claims = valid_claims();
    for (name, value) in changes {
        if value.is_null() {
            claims
                .as_object_mut()
                .expect("the claims are an object")
                .remove(*name);
        } else {
            claims[*name] = value.clone();
        }
    }
    claims
}

#[tokio::test]
async fn every_go_server_algorithm_verifies_and_rejects_a_forged_signature() {
    for algorithm in JwsAlgorithm::ALL {
        let key = TestSigningKey::generate(algorithm);
        let (verifier, _clock) = verifier_trusting(&[key.public_jwk(KEY_ID)]);
        let token = key.token(KEY_ID, &valid_claims());
        assert_eq!(verify(&verifier, &token).await, Ok(()), "{algorithm:?}");

        let other_key = TestSigningKey::generate(algorithm);
        let forged = other_key.token(KEY_ID, &valid_claims());
        assert_eq!(
            verify(&verifier, &forged).await,
            Err(JwtRejection::InvalidSignature),
            "{algorithm:?}"
        );
        let (signing_input, _signature) = token
            .rsplit_once('.')
            .expect("a compact JWS has a signature segment");
        let tampered = format!(
            "{signing_input}.{}",
            encode(b"not the signature of this token")
        );
        assert_eq!(
            verify(&verifier, &tampered).await,
            Err(JwtRejection::InvalidSignature),
            "{algorithm:?}"
        );
    }
}

#[tokio::test]
async fn a_key_verifies_only_its_own_algorithm() {
    let rsa = TestSigningKey::generate(JwsAlgorithm::Rs256);
    let (verifier, _clock) = verifier_trusting(&[rsa.public_jwk(KEY_ID)]);
    let other_header = json!({"alg": "PS256", "kid": KEY_ID});
    assert_eq!(
        verify(
            &verifier,
            &rsa.token_with_header(&other_header, &valid_claims())
        )
        .await,
        Err(JwtRejection::KeyAlgorithmMismatch)
    );

    let p256 = TestSigningKey::generate(JwsAlgorithm::Es256);
    let mut jwk_without_alg = p256.public_jwk(KEY_ID);
    jwk_without_alg
        .as_object_mut()
        .expect("a JWK is an object")
        .remove("alg");
    let (verifier, _clock) = verifier_trusting(&[jwk_without_alg]);
    assert_eq!(
        verify(&verifier, &p256.token(KEY_ID, &valid_claims())).await,
        Ok(())
    );
    for algorithm in ["ES384", "ES512", "RS256", "EdDSA"] {
        let header = json!({"alg": algorithm, "kid": KEY_ID});
        assert_eq!(
            verify(&verifier, &p256.token_with_header(&header, &valid_claims())).await,
            Err(JwtRejection::KeyAlgorithmMismatch),
            "{algorithm}"
        );
    }
}

#[tokio::test]
async fn unsupported_headers_are_rejected() {
    let key = TestSigningKey::generate(JwsAlgorithm::EdDsa);
    let (verifier, _clock) = verifier_trusting(&[key.public_jwk(KEY_ID)]);
    let claims = encode(valid_claims().to_string().as_bytes());
    for algorithm in ["HS256", "HS512", "none", "RS1", "Ed25519"] {
        let header = encode(
            json!({"alg": algorithm, "kid": KEY_ID})
                .to_string()
                .as_bytes(),
        );
        let token = format!("{header}.{claims}.{}", encode(b"mac"));
        assert_eq!(
            verify(&verifier, &token).await,
            Err(JwtRejection::UnsupportedAlgorithm),
            "{algorithm}"
        );
    }
    let unsigned = format!(
        "{}.{claims}.",
        encode(json!({"alg": "none"}).to_string().as_bytes())
    );
    assert_eq!(
        verify(&verifier, &unsigned).await,
        Err(JwtRejection::UnsupportedAlgorithm)
    );
    let cases = [
        (json!({"alg": "EdDSA"}), JwtRejection::MissingKeyId),
        (
            json!({"alg": "EdDSA", "kid": ""}),
            JwtRejection::MissingKeyId,
        ),
        (
            json!({"alg": "EdDSA", "kid": KEY_ID, "crit": ["exp"], "exp": 1}),
            JwtRejection::CriticalHeader,
        ),
        (
            json!({"alg": "EdDSA", "kid": "another-key"}),
            JwtRejection::UnknownKeyId,
        ),
    ];
    for (header, rejection) in cases {
        assert_eq!(
            verify(&verifier, &key.token_with_header(&header, &valid_claims())).await,
            Err(rejection),
            "{header}"
        );
    }
}

#[tokio::test]
async fn claims_follow_the_go_server_rules() {
    let key = TestSigningKey::generate(JwsAlgorithm::EdDsa);
    let (verifier, clock) = verifier_trusting(&[key.public_jwk(KEY_ID)]);
    let start = TEST_START_UNIX_SECONDS;
    let accepted = [
        claims_with(&[("aud", json!(["unrelated", TEST_AUDIENCE]))]),
        claims_with(&[("aud", json!("another-audience"))]),
        claims_with(&[("exp", json!(start - 29)), ("iat", Value::Null)]),
        claims_with(&[("nbf", json!(start + 29))]),
        claims_with(&[("iat", json!(start + 29))]),
        claims_with(&[
            ("exp", json!(start as f64 + 60.75)),
            ("nbf", json!(start as f64 - 0.5)),
        ]),
        claims_with(&[("iat", Value::Null), ("sub", Value::Null)]),
    ];
    for claims in accepted {
        assert_eq!(
            verify(&verifier, &key.token(KEY_ID, &claims)).await,
            Ok(()),
            "{claims}"
        );
    }
    let rejected = [
        (
            claims_with(&[("iss", json!("https://other.example.com"))]),
            JwtRejection::IssuerMismatch,
        ),
        (
            claims_with(&[("iss", Value::Null)]),
            JwtRejection::IssuerMismatch,
        ),
        (
            claims_with(&[("exp", Value::Null)]),
            JwtRejection::MissingExpiry,
        ),
        (
            claims_with(&[("aud", json!("unrelated"))]),
            JwtRejection::AudienceMismatch,
        ),
        (
            claims_with(&[("aud", json!([]))]),
            JwtRejection::AudienceMismatch,
        ),
        (
            claims_with(&[("aud", Value::Null)]),
            JwtRejection::AudienceMismatch,
        ),
        (
            claims_with(&[("exp", json!(start - 31))]),
            JwtRejection::Expired,
        ),
        (
            claims_with(&[("nbf", json!(start + 31))]),
            JwtRejection::NotYetValid,
        ),
        (
            claims_with(&[("iat", json!(start + 31))]),
            JwtRejection::IssuedInFuture,
        ),
    ];
    for (claims, rejection) in rejected {
        assert_eq!(
            verify(&verifier, &key.token(KEY_ID, &claims)).await,
            Err(rejection),
            "{claims}"
        );
    }

    let token = key.token(KEY_ID, &claims_with(&[("exp", json!(start + 60))]));
    assert_eq!(verify(&verifier, &token).await, Ok(()));
    clock.advance(Duration::from_secs(90));
    assert_eq!(verify(&verifier, &token).await, Ok(()));
    clock.advance(Duration::from_secs(1));
    assert_eq!(verify(&verifier, &token).await, Err(JwtRejection::Expired));
}

#[tokio::test]
async fn ambiguous_private_and_unsupported_keys_reject_their_tokens() {
    let key = TestSigningKey::generate(JwsAlgorithm::EdDsa);
    let public_jwk = key.public_jwk(KEY_ID);
    let mut private_jwk = public_jwk.clone();
    private_jwk["d"] = json!(encode(&[7; 32]));
    let mut encryption_jwk = public_jwk.clone();
    encryption_jwk["use"] = json!("enc");
    let mut wrong_length_jwk = public_jwk.clone();
    wrong_length_jwk["x"] = json!(encode(&[1; 31]));
    let symmetric_jwk = json!({"kty": "oct", "kid": KEY_ID, "k": encode(b"secret")});
    let unknown_curve_jwk =
        json!({"kty": "OKP", "crv": "Ed448", "kid": KEY_ID, "x": encode(&[1; 57])});
    let documents = [
        vec![public_jwk.clone(), public_jwk.clone()],
        vec![private_jwk],
        vec![encryption_jwk],
        vec![wrong_length_jwk],
        vec![symmetric_jwk],
        vec![unknown_curve_jwk],
        vec![json!({"kid": KEY_ID})],
    ];
    for keys in documents {
        let (verifier, _clock) = verifier_trusting(&keys);
        assert_eq!(
            verify(&verifier, &key.token(KEY_ID, &valid_claims())).await,
            Err(JwtRejection::UnusableKey),
            "{keys:?}"
        );
    }
    let mut jwk_without_kid = public_jwk;
    jwk_without_kid
        .as_object_mut()
        .expect("a JWK is an object")
        .remove("kid");
    let (verifier, _clock) = verifier_trusting(&[jwk_without_kid]);
    assert_eq!(
        verify(&verifier, &key.token(KEY_ID, &valid_claims())).await,
        Err(JwtRejection::UnknownKeyId)
    );
}

#[tokio::test]
async fn authenticators_are_tried_in_order_and_an_issuer_match_decides() {
    let key = TestSigningKey::generate(JwsAlgorithm::Es512);
    let clock = ManualWallClock::starting_at_test_start();
    let source = ScriptedKeySetSource::serving(key_set_document(&[key.public_jwk(KEY_ID)]));
    let token_file = tempfile::NamedTempFile::new().expect("a temporary token file");
    let expired = key.token(
        KEY_ID,
        &claims_with(&[("exp", json!(TEST_START_UNIX_SECONDS - 3600))]),
    );
    let expired_hash = hex(&super::sha256(&expired));
    std::fs::write(
        token_file.path(),
        format!("tokens:\n  - sha256: {expired_hash}\n"),
    )
    .expect("the token file is written");
    let static_tokens =
        super::watched_file::WatchedFile::<StaticTokenHashes>::open(token_file.path())
            .expect("the token file loads");
    let authenticator = PublicApiAuthenticator {
        authenticators: vec![
            CredentialAuthenticator::Jwt(verifier(Arc::clone(&source), Arc::clone(&clock))),
            CredentialAuthenticator::StaticTokens(static_tokens),
        ],
    };
    assert!(
        authenticator
            .authenticate(&key.token(KEY_ID, &valid_claims()))
            .await
            .is_ok()
    );
    assert!(matches!(
        authenticator.authenticate(&expired).await,
        Err(CredentialRejection::Jwt(JwtRejection::Expired))
    ));
    let other_issuer = key.token(
        KEY_ID,
        &claims_with(&[("iss", json!("https://other.example.com"))]),
    );
    assert!(matches!(
        authenticator.authenticate(&other_issuer).await,
        Err(CredentialRejection::NotRecognized)
    ));
    assert!(matches!(
        authenticator.authenticate("not-a-token").await,
        Err(CredentialRejection::NotRecognized)
    ));
    assert_eq!(source.fetch_count(), 1);
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}
