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
    ObjectStoreErrorKind, S3ApiDialect, S3Credentials, S3ObjectStore, S3ObjectStoreConfig,
    VersionDiscovery, WriteCondition, WriteOutcome,
};
use s3local::{Durability, EtagMode, Store, StoreConfig};
use tempfile::TempDir;

use super::set_s3local_fault;
use super::tencent_cos_dialect::{BUCKET, KEY_PREFIX, TencentCosEmulator};

const STEP_TIMEOUT: Duration = Duration::from_secs(10);
const LEASE_KEY: &str = "lease/node-a";

fn append_fault(key_prefix: &str, action: &str) -> String {
    format!(
        r#"{{"operation":"AppendObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/{key_prefix}","action":{action}}}"#
    )
}

const INTERRUPTED_APPEND: &str = r#"{"type":"interrupted_append"}"#;

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
        &append_fault(
            "inflight/",
            r#"{"type":"append_placeholder","milliseconds":1500}"#,
        ),
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
    tokio::time::sleep(Duration::from_millis(500)).await;
    assert_eq!(emulator.record("inflight/key").size, 0, "the placeholder");
    assert_eq!(store.head("inflight/key").await.expect("head"), None);
    assert_eq!(store.get("inflight/key").await.expect("get"), None);
    let listing = store.list("inflight/", None, 1000).await.expect("list");
    assert!(listing.objects.is_empty(), "{:?}", listing.objects);

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

/// A listing that repeats a page of empty objects ends the call instead of paging forever.
#[tokio::test]
async fn tencent_cos_listing_returns_when_a_stale_page_repeats_empty_objects() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    leave_residue(&emulator, &store, "stale/r0").await;
    leave_residue(&emulator, &store, "stale/r1").await;
    store
        .put(
            "stale/x",
            Bytes::from_static(b"x"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("a create succeeds");
    set_s3local_fault(
        emulator.address(),
        r#"{"operation":"ListObjectsV2","action":{"type":"stale_listing","ttl_milliseconds":600000}}"#,
    )
    .await;
    for _ in 0..3 {
        let listing = tokio::time::timeout(STEP_TIMEOUT, store.list("stale/", None, 1))
            .await
            .expect("the listing returns")
            .expect("the listing succeeds");
        assert!(
            listing.objects.iter().all(|object| object.size > 0),
            "{:?}",
            listing.objects
        );
        assert!(listing.next_start_after.is_some(), "the page is truncated");
    }
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
        &append_fault(
            &format!("{LEASE_KEY}.v/"),
            r#"{"type":"append_placeholder","milliseconds":1500}"#,
        ),
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
    tokio::time::sleep(Duration::from_millis(500)).await;
    assert_eq!(emulator.record(&version_object_key(2)).size, 0);
    let reader = versioned_store(&inner);
    let during = reader
        .get(LEASE_KEY)
        .await
        .expect("the lease reads during the renewal")
        .expect("the lease exists");
    assert_eq!(during.bytes, Bytes::from_static(b"v1"));
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
    tokio::time::sleep(Duration::from_millis(200)).await;
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
