// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Key set refresh rates and bounds on paused time.

use std::sync::Arc;
use std::time::Duration;

use tokio::task::JoinSet;
use tokio::time::Instant;

use super::json_web_key_set::{JsonWebKeySetCache, KeySetRefreshPolicy};
use super::json_web_token::{JwsAlgorithm, JwtRejection};
use super::token_signing_tests::{
    ScriptedKeySetSource, TEST_ISSUER, TestSigningKey, key_set_document,
};

const FIRST_KEY: &str = "first-key";
const SECOND_KEY: &str = "second-key";

fn cache(source: &Arc<ScriptedKeySetSource>) -> JsonWebKeySetCache {
    JsonWebKeySetCache::new(
        TEST_ISSUER.to_owned(),
        Arc::clone(source) as Arc<_>,
        KeySetRefreshPolicy::STANDARD,
    )
}

fn single_key_document(key_id: &str) -> serde_json::Value {
    key_set_document(&[TestSigningKey::generate(JwsAlgorithm::EdDsa).public_jwk(key_id)])
}

/// Lets the refresh task that a lookup started run to completion.
async fn settle() {
    tokio::time::sleep(Duration::from_millis(1)).await;
}

#[tokio::test(start_paused = true)]
async fn the_first_lookup_fetches_and_waits_for_the_keys() {
    let source = ScriptedKeySetSource::serving(single_key_document(FIRST_KEY));
    source.set_delay(Duration::from_secs(2));
    let cache = cache(&source);
    assert_eq!(source.fetch_count(), 0);
    let started = Instant::now();
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    assert_eq!(started.elapsed(), Duration::from_secs(2));
    assert_eq!(source.fetch_count(), 1);
}

#[tokio::test(start_paused = true)]
async fn unknown_key_refreshes_wait_ten_seconds_after_the_last_attempt() {
    let source = ScriptedKeySetSource::serving(single_key_document(FIRST_KEY));
    let cache = cache(&source);
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    let both_keys = key_set_document(&[
        TestSigningKey::generate(JwsAlgorithm::EdDsa).public_jwk(FIRST_KEY),
        TestSigningKey::generate(JwsAlgorithm::EdDsa).public_jwk(SECOND_KEY),
    ]);
    source.replace_document(both_keys);
    for _ in 0..5 {
        assert_eq!(
            cache.verification_key(SECOND_KEY).await.err(),
            Some(JwtRejection::UnknownKeyId)
        );
    }
    assert_eq!(source.fetch_count(), 1);
    tokio::time::advance(Duration::from_secs(9)).await;
    assert_eq!(
        cache.verification_key(SECOND_KEY).await.err(),
        Some(JwtRejection::UnknownKeyId)
    );
    assert_eq!(source.fetch_count(), 1);
    tokio::time::advance(Duration::from_secs(1)).await;
    assert!(cache.verification_key(SECOND_KEY).await.is_ok());
    assert_eq!(source.fetch_count(), 2);
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    assert_eq!(source.fetch_count(), 2);
}

#[tokio::test(start_paused = true)]
async fn known_keys_refresh_once_a_minute_without_waiting() {
    let source = ScriptedKeySetSource::serving(single_key_document(FIRST_KEY));
    let cache = cache(&source);
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    tokio::time::advance(Duration::from_secs(59)).await;
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    assert_eq!(source.fetch_count(), 1);

    source.replace_document(single_key_document(SECOND_KEY));
    source.set_delay(Duration::from_secs(3));
    tokio::time::advance(Duration::from_secs(1)).await;
    let started = Instant::now();
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    assert_eq!(started.elapsed(), Duration::ZERO);
    settle().await;
    assert_eq!(source.fetch_count(), 2);
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());

    tokio::time::advance(Duration::from_secs(3)).await;
    settle().await;
    assert_eq!(
        cache.verification_key(FIRST_KEY).await.err(),
        Some(JwtRejection::UnknownKeyId)
    );
    assert!(cache.verification_key(SECOND_KEY).await.is_ok());
    assert_eq!(source.fetch_count(), 2);
}

#[tokio::test(start_paused = true)]
async fn a_failed_refresh_keeps_the_previous_keys() {
    let source = ScriptedKeySetSource::serving(single_key_document(FIRST_KEY));
    let cache = cache(&source);
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    source.set_failing(true);
    tokio::time::advance(Duration::from_secs(60)).await;
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    settle().await;
    assert_eq!(source.fetch_count(), 2);
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    tokio::time::advance(Duration::from_secs(10)).await;
    assert_eq!(
        cache.verification_key(SECOND_KEY).await.err(),
        Some(JwtRejection::UnknownKeyId)
    );
    assert_eq!(source.fetch_count(), 3);
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
}

#[tokio::test(start_paused = true)]
async fn a_stalled_key_server_delays_requests_at_most_the_fetch_timeout() {
    let source = ScriptedKeySetSource::serving(single_key_document(FIRST_KEY));
    source.set_delay(Duration::from_secs(600));
    let cache = cache(&source);
    let started = Instant::now();
    assert_eq!(
        cache.verification_key(FIRST_KEY).await.err(),
        Some(JwtRejection::UnknownKeyId)
    );
    assert_eq!(
        started.elapsed(),
        KeySetRefreshPolicy::STANDARD.fetch_timeout
    );
    settle().await;
    let started = Instant::now();
    assert_eq!(
        cache.verification_key(FIRST_KEY).await.err(),
        Some(JwtRejection::UnknownKeyId)
    );
    assert_eq!(started.elapsed(), Duration::ZERO);
    assert_eq!(source.fetch_count(), 1);

    source.set_delay(Duration::ZERO);
    tokio::time::advance(Duration::from_secs(5)).await;
    assert!(cache.verification_key(FIRST_KEY).await.is_ok());
    assert_eq!(source.fetch_count(), 2);
}

#[tokio::test(start_paused = true)]
async fn concurrent_lookups_share_one_fetch() {
    let source = ScriptedKeySetSource::serving(single_key_document(FIRST_KEY));
    source.set_delay(Duration::from_secs(1));
    let cache = Arc::new(cache(&source));
    let mut lookups = JoinSet::new();
    for _ in 0..16 {
        let cache = Arc::clone(&cache);
        lookups.spawn(async move { cache.verification_key(FIRST_KEY).await.is_ok() });
    }
    for is_found in lookups.join_all().await {
        assert!(is_found);
    }
    assert_eq!(source.fetch_count(), 1);
}
