// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Empty Tencent COS objects of appends in flight or interrupted read as absent.

use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, ByteRange, MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION, ObjectStore,
    ObjectStoreError, ObjectStoreErrorKind, S3ApiDialect, S3Credentials, S3ObjectStore,
    S3ObjectStoreConfig, TENCENT_COS_STALE_LISTING_BOUND, VersionDiscovery, WriteCondition,
    WriteOutcome,
};
use s3local::{Durability, EtagMode, Store, StoreConfig};
use tempfile::TempDir;

use super::tencent_cos_dialect::{BUCKET, KEY_PREFIX, TencentCosEmulator};
use super::{clear_s3local_faults, s3local_request_log, set_s3local_fault};

const STEP_TIMEOUT: Duration = Duration::from_secs(10);
const LEASE_KEY: &str = "lease/node-a";

fn append_fault(key_prefix: &str, action: &str) -> String {
    format!(
        r#"{{"operation":"AppendObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/{key_prefix}","action":{action}}}"#
    )
}

const INTERRUPTED_APPEND: &str = r#"{"type":"interrupted_append"}"#;

/// Holds each append as a placeholder until the test deletes the faults.
const HELD_APPEND_PLACEHOLDER: &str = r#"{"type":"append_placeholder","milliseconds":600000}"#;

/// Creates `key` with an append that the emulator interrupts, leaving an empty object.
async fn leave_residue(emulator: &TencentCosEmulator, store: &dyn ObjectStore, key: &str) {
    set_s3local_fault(emulator.address(), &append_fault(key, INTERRUPTED_APPEND)).await;
    let error = store
        .put(key, Bytes::from_static(b"lost"), WriteCondition::CreateOnly)
        .await
        .expect_err("an interrupted append fails");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown, "{error}");
    set_s3local_fault(emulator.address(), "").await;
    assert_eq!(emulator.record(key).size, 0, "{key} holds an empty object");
}

fn listed_keys(listing: &dexzd_object_store::ObjectListing) -> Vec<&str> {
    listing
        .objects
        .iter()
        .map(|object| object.key.as_str())
        .collect()
}

#[tokio::test]
async fn tencent_cos_reads_an_interrupted_create_as_absent_and_creates_over_it() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    leave_residue(&emulator, &store, "residue/a").await;
    leave_residue(&emulator, &store, "residue/c").await;
    store
        .put(
            "residue/b",
            Bytes::from_static(b"b"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("a create succeeds");

    assert_eq!(store.head("residue/a").await.expect("head succeeds"), None);
    assert_eq!(store.get("residue/a").await.expect("get succeeds"), None);
    let header = ByteRange {
        start: 0,
        end_inclusive: 37,
    };
    assert_eq!(
        store
            .get_range("residue/a", header)
            .await
            .expect("a range read succeeds"),
        None
    );
    let directory = TempDir::new().expect("the download directory is created");
    let download = directory.path().join("residue");
    assert_eq!(
        store
            .get_to_file("residue/a", &download)
            .await
            .expect("a download succeeds"),
        None
    );
    assert!(
        !download.exists(),
        "no file is created for an absent object"
    );

    let listing = store.list("residue/", None, 1000).await.expect("list");
    assert_eq!(listed_keys(&listing), ["residue/b"]);
    assert_eq!(listing.next_start_after, None);
    // A one-key page skips the empty object that sorts first.
    let first = store.list("residue/", None, 1).await.expect("list");
    assert_eq!(listed_keys(&first), ["residue/b"]);
    let rest = store
        .list("residue/", first.next_start_after.as_deref(), 1)
        .await
        .expect("list");
    assert!(rest.objects.is_empty(), "{:?}", rest.objects);
    assert_eq!(rest.next_start_after, None);

    assert!(matches!(
        store
            .put(
                "residue/a",
                Bytes::from_static(b"created"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("a create over the empty object succeeds"),
        WriteOutcome::Written { .. }
    ));
    assert_eq!(
        store
            .put(
                "residue/a",
                Bytes::from_static(b"again"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("a second create reaches the store"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .get("residue/a")
            .await
            .expect("get succeeds")
            .expect("the object exists")
            .bytes,
        Bytes::from_static(b"created")
    );
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_reads_an_append_in_flight_as_absent_and_commits_one_winner() {
    let emulator = TencentCosEmulator::start().await;
    let store = Arc::new(emulator.object_store().await);
    set_s3local_fault(
        emulator.address(),
        &append_fault("inflight/", HELD_APPEND_PLACEHOLDER),
    )
    .await;
    let mut creators = tokio::task::JoinSet::new();
    for body in ["first", "second"] {
        let store = Arc::clone(&store);
        creators.spawn(async move {
            let outcome = store
                .put(
                    "inflight/key",
                    Bytes::from(body),
                    WriteCondition::CreateOnly,
                )
                .await
                .expect("a create reaches a definite outcome");
            (body, outcome)
        });
    }
    emulator.wait_for_append_placeholder("inflight/key").await;
    assert_eq!(store.head("inflight/key").await.expect("head"), None);
    assert_eq!(store.get("inflight/key").await.expect("get"), None);
    let listing = store.list("inflight/", None, 1000).await.expect("list");
    assert!(listing.objects.is_empty(), "{:?}", listing.objects);
    clear_s3local_faults(emulator.address()).await;

    let mut winners = Vec::new();
    while let Some(joined) = creators.join_next().await {
        let (body, outcome) = joined.expect("a creator task completes");
        if let WriteOutcome::Written { .. } = outcome {
            winners.push(body);
        }
    }
    assert_eq!(winners.len(), 1, "{winners:?}");
    assert_eq!(
        store
            .get("inflight/key")
            .await
            .expect("get succeeds")
            .expect("the winner's object exists")
            .bytes,
        Bytes::from(winners[0])
    );
    emulator.shutdown().await;
}

const STALE_LISTING: &str =
    r#"{"operation":"ListObjectsV2","action":{"type":"stale_listing","ttl_milliseconds":600000}}"#;

/// A listing cache shorter than [`TENCENT_COS_STALE_LISTING_BOUND`].
const SHORT_STALE_LISTING: &str =
    r#"{"operation":"ListObjectsV2","action":{"type":"stale_listing","ttl_milliseconds":1500}}"#;

/// A short retry window, so tests reach the Transient error quickly.
const SHORT_RETRY_WINDOW: Duration = Duration::from_millis(500);

/// Pages `prefix` as callers do and checks that every cursor strictly advances.
async fn list_all_pages(store: &dyn ObjectStore, prefix: &str, max_keys: u32) -> Vec<String> {
    try_list_all_pages(store, prefix, max_keys)
        .await
        .expect("the listing succeeds")
}

/// Pages `prefix` as callers do and returns the first listing error.
async fn try_list_all_pages(
    store: &dyn ObjectStore,
    prefix: &str,
    max_keys: u32,
) -> Result<Vec<String>, ObjectStoreError> {
    let mut keys = Vec::new();
    let mut start_after: Option<String> = None;
    for _ in 0..16 {
        let listing = tokio::time::timeout(
            STEP_TIMEOUT,
            store.list(prefix, start_after.as_deref(), max_keys),
        )
        .await
        .expect("the listing returns")?;
        assert!(listing.objects.len() <= max_keys as usize, "{listing:?}");
        for object in &listing.objects {
            assert!(object.size > 0, "{listing:?}");
            assert!(
                start_after
                    .as_deref()
                    .is_none_or(|after| object.key.as_str() > after),
                "{object:?} is not after {start_after:?}"
            );
            assert!(
                keys.last().is_none_or(|last: &String| object.key > *last),
                "{object:?} repeats a key"
            );
            keys.push(object.key.clone());
        }
        let Some(next) = listing.next_start_after else {
            return Ok(keys);
        };
        assert!(
            start_after
                .as_deref()
                .is_none_or(|after| next.as_str() > after),
            "the cursor {next:?} does not advance past {start_after:?}"
        );
        start_after = Some(next);
    }
    panic!("the listing of {prefix} did not end; collected {keys:?}");
}

/// Returns how many ListObjectsV2 requests the emulator has served.
async fn list_request_count(emulator: &TencentCosEmulator) -> usize {
    s3local_request_log(emulator.address())
        .await
        .matches("\"operation\":\"ListObjectsV2\"")
        .count()
}

/// Lists `prefix` once and checks that it fails as Transient within a bounded number of requests.
async fn assert_stale_listing_fails_transient(
    emulator: &TencentCosEmulator,
    store: &dyn ObjectStore,
    prefix: &str,
    start_after: Option<&str>,
    max_keys: u32,
) {
    let requests_before = list_request_count(emulator).await;
    let started = tokio::time::Instant::now();
    let error = tokio::time::timeout(STEP_TIMEOUT, store.list(prefix, start_after, max_keys))
        .await
        .expect("the listing returns")
        .expect_err("a truncated page that keeps ending at its cursor fails the listing");
    assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
    assert!(started.elapsed() >= SHORT_RETRY_WINDOW, "{error}");
    let requests = list_request_count(emulator).await - requests_before;
    assert!(
        (3..=8).contains(&requests),
        "the replayed page is requested again with backoff, not in a busy loop: {requests} requests"
    );
}

/// A stale truncated page that replays a residue at the cursor fails instead of ending early.
#[tokio::test]
async fn tencent_cos_listing_fails_transient_when_a_stale_page_replays_only_empty_objects() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator
        .object_store_with_stale_listing_retry_window(SHORT_RETRY_WINDOW)
        .await;
    leave_residue(&emulator, &store, "stale/r0").await;
    leave_residue(&emulator, &store, "stale/r1").await;
    for key in ["stale/x", "stale/y"] {
        store
            .put(key, Bytes::from_static(b"x"), WriteCondition::CreateOnly)
            .await
            .expect("a create succeeds");
    }
    set_s3local_fault(emulator.address(), STALE_LISTING).await;
    // The first one-key page, holding only stale/r0, is replayed to every later listing.
    assert_stale_listing_fails_transient(&emulator, &store, "stale/", None, 1).await;
    assert_stale_listing_fails_transient(&emulator, &store, "stale/", Some("stale/r0"), 1).await;
    for max_keys in [1, 2, 1000] {
        let error = try_list_all_pages(&store, "stale/", max_keys)
            .await
            .expect_err("a stale listing never ends before stale/x");
        assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
    }
    set_s3local_fault(emulator.address(), "").await;
    for max_keys in [1, 2, 1000] {
        assert_eq!(
            list_all_pages(&store, "stale/", max_keys).await,
            ["stale/x", "stale/y"]
        );
    }
    emulator.shutdown().await;
}

/// A replayed page holding a residue and fewer than max-keys objects never hides later keys.
#[tokio::test]
async fn tencent_cos_listing_never_ends_at_a_replayed_page_with_a_residue() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator
        .object_store_with_stale_listing_retry_window(SHORT_RETRY_WINDOW)
        .await;
    store
        .put(
            "lineage/a",
            Bytes::from_static(b"a"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("a create succeeds");
    leave_residue(&emulator, &store, "lineage/b").await;
    store
        .put(
            "lineage/c",
            Bytes::from_static(b"c"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("a create succeeds");
    set_s3local_fault(emulator.address(), STALE_LISTING).await;
    // Caches the truncated page [lineage/a, lineage/b]; lineage/c is on a later page.
    assert_stale_listing_fails_transient(&emulator, &store, "lineage/", None, 2).await;
    set_s3local_fault(emulator.address(), "").await;
    assert_eq!(
        list_all_pages(&store, "lineage/", 2).await,
        ["lineage/a", "lineage/c"]
    );
    emulator.shutdown().await;
}

/// A replayed page returns only keys after `start_after` and at most `max_keys` of them.
#[tokio::test]
async fn tencent_cos_listing_filters_a_replayed_page_to_keys_after_start_after() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator
        .object_store_with_stale_listing_retry_window(SHORT_RETRY_WINDOW)
        .await;
    for key in ["page/a", "page/b", "page/c"] {
        store
            .put(key, Bytes::from_static(b"v"), WriteCondition::CreateOnly)
            .await
            .expect("a create succeeds");
    }
    set_s3local_fault(emulator.address(), STALE_LISTING).await;
    // Caches the truncated page [page/a, page/b] for every later listing.
    let primed = store.list("page/", None, 2).await.expect("list");
    assert_eq!(listed_keys(&primed), ["page/a", "page/b"]);
    assert_eq!(primed.next_start_after.as_deref(), Some("page/b"));

    let after_a = store.list("page/", Some("page/a"), 1).await.expect("list");
    assert_eq!(listed_keys(&after_a), ["page/b"]);
    assert_eq!(after_a.next_start_after.as_deref(), Some("page/b"));
    let first_key = store.list("page/", None, 1).await.expect("list");
    assert_eq!(listed_keys(&first_key), ["page/a"]);
    assert_eq!(first_key.next_start_after.as_deref(), Some("page/a"));
    // The stale cache hides page/c, so a listing past page/b fails until the cache expires.
    assert_stale_listing_fails_transient(&emulator, &store, "page/", Some("page/b"), 1).await;
    for max_keys in [1, 2, 1000] {
        let error = try_list_all_pages(&store, "page/", max_keys)
            .await
            .expect_err("a stale listing never ends before page/c");
        assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
    }
    set_s3local_fault(emulator.address(), "").await;
    for max_keys in [1, 2, 1000] {
        assert_eq!(
            list_all_pages(&store, "page/", max_keys).await,
            ["page/a", "page/b", "page/c"]
        );
    }
    emulator.shutdown().await;
}

/// An untruncated replayed page still ends the listing, as an eventually consistent one may.
#[tokio::test]
async fn tencent_cos_listing_ends_at_an_untruncated_replayed_page() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator
        .object_store_with_stale_listing_retry_window(SHORT_RETRY_WINDOW)
        .await;
    for key in ["whole/a", "whole/b"] {
        store
            .put(key, Bytes::from_static(b"v"), WriteCondition::CreateOnly)
            .await
            .expect("a create succeeds");
    }
    set_s3local_fault(emulator.address(), STALE_LISTING).await;
    let primed = store.list("whole/", None, 1000).await.expect("list");
    assert_eq!(listed_keys(&primed), ["whole/a", "whole/b"]);
    assert_eq!(primed.next_start_after, None);
    store
        .put(
            "whole/c",
            Bytes::from_static(b"v"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("a create succeeds");
    let requests_before = list_request_count(&emulator).await;
    let after_b = store
        .list("whole/", Some("whole/b"), 1000)
        .await
        .expect("an untruncated replay ends the listing");
    assert!(after_b.objects.is_empty(), "{after_b:?}");
    assert_eq!(after_b.next_start_after, None);
    assert_eq!(list_request_count(&emulator).await - requests_before, 1);
    assert_eq!(
        list_all_pages(&store, "whole/", 1000).await,
        ["whole/a", "whole/b"]
    );
    emulator.shutdown().await;
}

/// A listing cache that expires within the retry window delays a listing but never fails it.
#[tokio::test]
async fn tencent_cos_listing_waits_out_a_listing_cache_shorter_than_its_bound() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    for key in ["short/a", "short/b", "short/c"] {
        store
            .put(key, Bytes::from_static(b"v"), WriteCondition::CreateOnly)
            .await
            .expect("a create succeeds");
    }
    set_s3local_fault(emulator.address(), SHORT_STALE_LISTING).await;
    // Caches the truncated page [short/a, short/b] for 1.5 seconds.
    let primed = store.list("short/", None, 2).await.expect("list");
    assert_eq!(listed_keys(&primed), ["short/a", "short/b"]);
    assert_eq!(
        list_all_pages(&store, "short/", 2).await,
        ["short/a", "short/b", "short/c"]
    );
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_head_probing_reads_past_an_interrupted_replacement() {
    let emulator = TencentCosEmulator::start().await;
    let inner: Arc<dyn ObjectStore> = Arc::new(emulator.object_store().await);
    let writer = versioned_store(&inner);
    let WriteOutcome::Written { etag: first_etag } = writer
        .put(
            LEASE_KEY,
            Bytes::from_static(b"v1"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the lease is created")
    else {
        panic!("the first lease version is written");
    };
    let WriteOutcome::Written { etag: second_etag } = writer
        .put(
            LEASE_KEY,
            Bytes::from_static(b"v2"),
            WriteCondition::ReplaceIfEtagMatches { etag: first_etag },
        )
        .await
        .expect("the lease is renewed")
    else {
        panic!("the second lease version is written");
    };
    set_s3local_fault(
        emulator.address(),
        &append_fault(&format!("{LEASE_KEY}.v/"), INTERRUPTED_APPEND),
    )
    .await;
    let interrupted = writer
        .put(
            LEASE_KEY,
            Bytes::from_static(b"lost"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: second_etag.clone(),
            },
        )
        .await
        .expect_err("the interrupted renewal fails");
    assert_eq!(
        interrupted.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{interrupted}"
    );
    set_s3local_fault(emulator.address(), "").await;
    assert_eq!(emulator.record(&version_object_key(3)).size, 0);

    // A node taking over the lease has no generation hint, so it probes from the start.
    let taker = versioned_store(&inner);
    for store in [&writer, &taker] {
        let newest = store
            .get(LEASE_KEY)
            .await
            .expect("the lease reads")
            .expect("the lease exists");
        assert_eq!(newest.bytes, Bytes::from_static(b"v2"));
        assert_eq!(newest.etag, second_etag);
    }
    assert!(matches!(
        taker
            .put(
                LEASE_KEY,
                Bytes::from_static(b"taken"),
                WriteCondition::ReplaceIfEtagMatches { etag: second_etag },
            )
            .await
            .expect("the takeover reaches the store"),
        WriteOutcome::Written { .. }
    ));
    let newest = writer
        .get(LEASE_KEY)
        .await
        .expect("the lease reads")
        .expect("the lease exists");
    assert_eq!(newest.bytes, Bytes::from_static(b"taken"));
    emulator.shutdown().await;
}

/// A takeover read without a hint lists a residue at the newest generation through a warm cache.
#[tokio::test]
async fn tencent_cos_head_probing_reads_past_a_residue_that_a_stale_listing_replays() {
    let emulator = TencentCosEmulator::start().await;
    let inner: Arc<dyn ObjectStore> = Arc::new(
        emulator
            .object_store_with_stale_listing_retry_window(SHORT_RETRY_WINDOW)
            .await,
    );
    let default_window_inner: Arc<dyn ObjectStore> = Arc::new(emulator.object_store().await);
    let writer = versioned_store(&inner);
    let WriteOutcome::Written { etag: first_etag } = writer
        .put(
            LEASE_KEY,
            Bytes::from_static(b"v1"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the lease is created")
    else {
        panic!("the first lease version is written");
    };
    let WriteOutcome::Written { etag: second_etag } = writer
        .put(
            LEASE_KEY,
            Bytes::from_static(b"v2"),
            WriteCondition::ReplaceIfEtagMatches { etag: first_etag },
        )
        .await
        .expect("the lease is renewed")
    else {
        panic!("the second lease version is written");
    };
    set_s3local_fault(
        emulator.address(),
        &append_fault(&format!("{LEASE_KEY}.v/"), INTERRUPTED_APPEND),
    )
    .await;
    writer
        .put(
            LEASE_KEY,
            Bytes::from_static(b"lost"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: second_etag.clone(),
            },
        )
        .await
        .expect_err("the interrupted renewal fails");
    set_s3local_fault(emulator.address(), "").await;
    assert_eq!(emulator.record(&version_object_key(3)).size, 0);

    // A cache that outlives the retry window fails the read as retryable, never with v1.
    set_s3local_fault(emulator.address(), STALE_LISTING).await;
    let error = versioned_store(&inner)
        .get(LEASE_KEY)
        .await
        .expect_err("a listing stuck on the residue fails the hintless read");
    assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");

    // A cache shorter than the bound only delays the read.
    set_s3local_fault(emulator.address(), SHORT_STALE_LISTING).await;
    let started = tokio::time::Instant::now();
    let newest = versioned_store(&default_window_inner)
        .get(LEASE_KEY)
        .await
        .expect("the lease reads once the cache expires")
        .expect("the lease exists");
    assert_eq!(newest.bytes, Bytes::from_static(b"v2"));
    assert_eq!(newest.etag, second_etag);
    assert!(started.elapsed() < TENCENT_COS_STALE_LISTING_BOUND);
    set_s3local_fault(emulator.address(), "").await;
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_head_probing_reads_past_a_replacement_in_flight() {
    let emulator = TencentCosEmulator::start().await;
    let inner: Arc<dyn ObjectStore> = Arc::new(emulator.object_store().await);
    let writer = Arc::new(versioned_store(&inner));
    let WriteOutcome::Written { etag } = writer
        .put(
            LEASE_KEY,
            Bytes::from_static(b"v1"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the lease is created")
    else {
        panic!("the first lease version is written");
    };
    set_s3local_fault(
        emulator.address(),
        &append_fault(&format!("{LEASE_KEY}.v/"), HELD_APPEND_PLACEHOLDER),
    )
    .await;
    let renewal = tokio::spawn({
        let writer = Arc::clone(&writer);
        async move {
            writer
                .put(
                    LEASE_KEY,
                    Bytes::from_static(b"v2"),
                    WriteCondition::ReplaceIfEtagMatches { etag },
                )
                .await
        }
    });
    emulator
        .wait_for_append_placeholder(&version_object_key(2))
        .await;
    let reader = versioned_store(&inner);
    let during = reader
        .get(LEASE_KEY)
        .await
        .expect("the lease reads during the renewal")
        .expect("the lease exists");
    assert_eq!(during.bytes, Bytes::from_static(b"v1"));
    clear_s3local_faults(emulator.address()).await;
    assert!(matches!(
        renewal
            .await
            .expect("the renewal task completes")
            .expect("the renewal reaches the store"),
        WriteOutcome::Written { .. }
    ));
    let after = reader
        .get(LEASE_KEY)
        .await
        .expect("the lease reads")
        .expect("the lease exists");
    assert_eq!(after.bytes, Bytes::from_static(b"v2"));
    emulator.shutdown().await;
}

/// Writes of empty bodies fail before any request, because they would read as absent.
#[tokio::test]
async fn tencent_cos_rejects_every_empty_write_and_other_dialects_keep_empty_objects() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    let directory = TempDir::new().expect("the file directory is created");
    let empty_file = directory.path().join("empty");
    tokio::fs::write(&empty_file, b"")
        .await
        .expect("the empty file is written");
    let empty_put = store
        .put("empty/plain", Bytes::new(), WriteCondition::Unconditional)
        .await
        .expect_err("an empty unconditional put is rejected");
    let empty_file_put = store
        .put_file("empty/file", &empty_file, WriteCondition::Unconditional)
        .await
        .expect_err("an empty unconditional file upload is rejected");
    for error in [&empty_put, &empty_file_put] {
        assert_eq!(error.kind, ObjectStoreErrorKind::Permanent, "{error}");
        assert!(
            error.detail.contains("reads an empty object as absent"),
            "{error}"
        );
    }
    emulator.shutdown().await;

    for (emulator_dialect, dialect) in [
        (
            s3local_server::S3ApiDialect::AmazonS3,
            S3ApiDialect::AmazonS3,
        ),
        (
            s3local_server::S3ApiDialect::AliyunOss,
            S3ApiDialect::AliyunOss,
        ),
    ] {
        let (_directory, server, store) = start_emulator(emulator_dialect, dialect).await;
        store
            .put("empty/plain", Bytes::new(), WriteCondition::Unconditional)
            .await
            .expect("an empty put succeeds");
        let metadata = store
            .head("empty/plain")
            .await
            .expect("head succeeds")
            .expect("the empty object exists");
        assert_eq!(metadata.size, 0, "{dialect:?}");
        let content = store
            .get("empty/plain")
            .await
            .expect("get succeeds")
            .expect("the empty object exists");
        assert!(content.bytes.is_empty(), "{dialect:?}");
        let listing = store.list("empty/", None, 1000).await.expect("list");
        assert_eq!(listed_keys(&listing), ["empty/plain"], "{dialect:?}");
        server.shutdown().await;
    }
}

fn versioned_store(inner: &Arc<dyn ObjectStore>) -> AliyunOssObjectStore {
    AliyunOssObjectStore::with_version_discovery(
        Arc::clone(inner),
        MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
        NodeMetrics::unregistered(),
        VersionDiscovery::HeadProbing,
    )
}

/// Returns the physical key of `generation` of [`LEASE_KEY`], as the versioned layer names it.
fn version_object_key(generation: u64) -> String {
    format!("{LEASE_KEY}.v/{:020}", u64::MAX - generation)
}

/// Starts s3local with `emulator_dialect` and returns a client of `dialect` for it.
async fn start_emulator(
    emulator_dialect: s3local_server::S3ApiDialect,
    dialect: S3ApiDialect,
) -> (TempDir, s3local_server::ServerHandle, S3ObjectStore) {
    let directory = TempDir::new().expect("the emulator directory is created");
    let store = Store::open(&StoreConfig {
        root_directory: directory.path().to_path_buf(),
        durability: Durability::Relaxed,
        etag_mode: EtagMode::Md5,
    })
    .expect("the emulator store opens");
    store
        .create_bucket(BUCKET, false)
        .expect("the emulator bucket is created");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the emulator binds");
    let server = s3local_server::start_with_config(
        Arc::new(store),
        listener,
        s3local_server::ServerConfig {
            api_dialect: emulator_dialect,
            ..s3local_server::ServerConfig::default()
        },
    )
    .expect("the emulator starts");
    let mut config = S3ObjectStoreConfig::new("us-east-1", BUCKET);
    config.endpoint = Some(format!("http://{}", server.local_address()));
    config.force_path_style = true;
    config.key_prefix = KEY_PREFIX.to_owned();
    config.credentials = S3Credentials::Static {
        access_key_id: "s3local".to_owned(),
        secret_access_key: "s3local".to_owned(),
        session_token: None,
    };
    config.api_dialect = dialect;
    let client = S3ObjectStore::new(config)
        .await
        .expect("the S3 client is configured");
    (directory, server, client)
}

/// A ranged read gets 416 from the placeholder, then its HEAD sees the committed append.
#[tokio::test]
async fn tencent_cos_range_read_rereads_an_append_that_commits_after_its_416() {
    let emulator = TencentCosEmulator::start().await;
    let store = Arc::new(emulator.object_store().await);
    let placeholder = append_fault(
        "racing/",
        r#"{"type":"append_placeholder","milliseconds":500}"#,
    );
    let slow_head = format!(
        r#"{{"operation":"HeadObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/racing/","action":{{"type":"delay","milliseconds":1500}}}}"#
    );
    set_s3local_fault(emulator.address(), &format!("{placeholder},{slow_head}")).await;
    let create = tokio::spawn({
        let store = Arc::clone(&store);
        async move {
            store
                .put(
                    "racing/key",
                    Bytes::from_static(b"header and body"),
                    WriteCondition::CreateOnly,
                )
                .await
        }
    });
    emulator.wait_for_append_placeholder("racing/key").await;
    let header = ByteRange {
        start: 0,
        end_inclusive: 5,
    };
    let content = store
        .get_range("racing/key", header)
        .await
        .expect("the range read succeeds")
        .expect("the append committed before the HEAD");
    assert_eq!(content.bytes, Bytes::from_static(b"header"));
    assert_eq!(content.object_size, 15);
    assert!(matches!(
        create
            .await
            .expect("the create task completes")
            .expect("the create reaches the store"),
        WriteOutcome::Written { .. }
    ));
    emulator.shutdown().await;
}

/// A range read that keeps getting 416 for a non-empty object fails rather than read no bytes.
#[tokio::test]
async fn tencent_cos_range_read_never_returns_empty_bytes_for_a_non_empty_object() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    store
        .put(
            "unsatisfiable/key",
            Bytes::from_static(b"header and body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("a create succeeds");
    set_s3local_fault(
        emulator.address(),
        &format!(
            r#"{{"operation":"GetObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/unsatisfiable/","action":{{"type":"error","status":416}}}}"#
        ),
    )
    .await;
    let header = ByteRange {
        start: 0,
        end_inclusive: 5,
    };
    let error = store
        .get_range("unsatisfiable/key", header)
        .await
        .expect_err("the range read does not return empty bytes");
    assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
    // A range that starts at the end of the object still reads as no bytes.
    let past_end = store
        .get_range(
            "unsatisfiable/key",
            ByteRange {
                start: 15,
                end_inclusive: 20,
            },
        )
        .await
        .expect("a range past the end succeeds")
        .expect("the object exists");
    assert!(past_end.bytes.is_empty());
    assert_eq!(past_end.object_size, 15);
    emulator.shutdown().await;
}
