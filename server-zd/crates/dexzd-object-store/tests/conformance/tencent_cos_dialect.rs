// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Conformance of the Tencent COS dialect against s3local's Tencent COS emulation.

use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_object_store::{
    MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES, ObjectStore, ObjectStoreErrorKind, S3Credentials,
    S3ObjectStore, S3ObjectStoreConfig, TENCENT_COS_STALE_LISTING_BOUND,
    TencentCosBucketCheckError, WriteCondition, WriteOutcome, check_tencent_cos_bucket_versioning,
    check_tencent_cos_create_only_writes, check_tencent_cos_holds_no_data_of_another_provider,
};
use s3local::{Durability, EtagMode, Store, StoreConfig};
use tempfile::TempDir;

use super::aliyun_oss_dialect::check_store_without_atomic_replacement;
use super::{s3local_request_log, set_s3local_fault};

pub(super) const BUCKET: &str = "tencent-cos-dialect";
pub(super) const KEY_PREFIX: &str = "dexzd/tencent";
const PART_SIZE: usize = 5 * 1024 * 1024;

pub(super) struct TencentCosEmulator {
    _directory: TempDir,
    store: Arc<Store>,
    server: s3local_server::ServerHandle,
}

impl TencentCosEmulator {
    /// Starts the emulator with strict signature checks, so AppendObject signing is verified.
    pub(super) async fn start() -> Self {
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
        let store = Arc::new(store);
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("the emulator binds");
        let server = s3local_server::start_with_config(
            Arc::clone(&store),
            listener,
            s3local_server::ServerConfig {
                api_dialect: s3local_server::S3ApiDialect::TencentCos,
                signature_verification: s3local_server::SignatureVerification::Strict,
                ..s3local_server::ServerConfig::default()
            },
        )
        .expect("the emulator starts");
        Self {
            _directory: directory,
            store,
            server,
        }
    }

    pub(super) fn address(&self) -> SocketAddr {
        self.server.local_address()
    }

    pub(super) async fn object_store(&self) -> S3ObjectStore {
        self.object_store_with_stale_listing_retry_window(TENCENT_COS_STALE_LISTING_BOUND)
            .await
    }

    pub(super) async fn object_store_with_stale_listing_retry_window(
        &self,
        stale_listing_retry_window: Duration,
    ) -> S3ObjectStore {
        let mut config = S3ObjectStoreConfig::new("us-east-1", BUCKET);
        config.stale_listing_retry_window = stale_listing_retry_window;
        config.endpoint = Some(format!("http://{}", self.address()));
        config.key_prefix = KEY_PREFIX.to_owned();
        config.credentials = S3Credentials::Static {
            access_key_id: "s3local".to_owned(),
            secret_access_key: "s3local".to_owned(),
            session_token: None,
        };
        config.multipart_threshold = PART_SIZE;
        config.multipart_part_size = PART_SIZE;
        config.api_dialect = dexzd_object_store::S3ApiDialect::TencentCos;
        S3ObjectStore::new(config)
            .await
            .expect("the Tencent COS dialect store is configured")
    }

    /// Returns the stored record of `key`, relative to the store prefix.
    pub(super) fn record(&self, key: &str) -> s3local::VersionRecord {
        self.store
            .head_object(
                BUCKET,
                &format!("{KEY_PREFIX}/{key}"),
                &s3local::ObjectReadRequest::default(),
            )
            .expect("the object is readable")
            .record
    }

    /// Waits until `key` holds the empty object of an append in flight, read past the client.
    pub(super) async fn wait_for_append_placeholder(&self, key: &str) {
        let full_key = format!("{KEY_PREFIX}/{key}");
        let has_placeholder = || match self.store.head_object(
            BUCKET,
            &full_key,
            &s3local::ObjectReadRequest::default(),
        ) {
            Ok(selection) => selection.record.size == 0,
            Err(s3local::StoreError::NoSuchKey { .. }) => false,
            Err(error) => panic!("the emulator reads {key}: {error}"),
        };
        tokio::time::timeout(Duration::from_secs(10), async {
            while !has_placeholder() {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .expect("the append creates its placeholder");
    }

    pub(super) async fn shutdown(self) {
        self.server.shutdown().await;
    }
}

#[tokio::test]
async fn tencent_cos_conformance() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    check_store_without_atomic_replacement(&store).await;
    // Create-only writes of every size are single appendable objects.
    for key in ["large", "large-file", "file"] {
        let record = emulator.record(key);
        assert!(record.is_appendable, "{key}");
        assert_eq!(record.parts_count, None, "{key}");
    }
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_concurrent_creators_have_exactly_one_winner() {
    let emulator = TencentCosEmulator::start().await;
    let store = Arc::new(emulator.object_store().await);
    for round in 0..10 {
        let key = format!("race/{round}");
        let mut creators = tokio::task::JoinSet::new();
        for creator in 0..32 {
            let store = Arc::clone(&store);
            let key = key.clone();
            creators.spawn(async move {
                let body = Bytes::from(format!("creator-{creator}"));
                let outcome = store
                    .put(&key, body.clone(), WriteCondition::CreateOnly)
                    .await
                    .expect("a concurrent create reaches a definite outcome");
                (body, outcome)
            });
        }
        let mut winners = Vec::new();
        while let Some(joined) = creators.join_next().await {
            let (body, outcome) = joined.expect("a creator task completes");
            match outcome {
                WriteOutcome::Written { etag } => winners.push((body, etag)),
                WriteOutcome::PreconditionFailed => {}
            }
        }
        assert_eq!(winners.len(), 1, "round {round} has exactly one winner");
        let content = store
            .get(&key)
            .await
            .expect("get succeeds")
            .expect("the winner's object exists");
        assert_eq!(content.bytes, winners[0].0);
        assert_eq!(content.etag, winners[0].1);
    }
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_rejects_create_only_bodies_that_append_cannot_create_exclusively() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    let directory = TempDir::new().expect("the file directory is created");
    let empty_file = directory.path().join("empty");
    tokio::fs::write(&empty_file, b"")
        .await
        .expect("the empty file is written");
    // A file larger than 5 GiB, sparse so that the test allocates no disk space.
    let oversized_file = directory.path().join("oversized");
    std::fs::File::create(&oversized_file)
        .and_then(|file| file.set_len(MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES + 1))
        .expect("the sparse file is created");
    let empty_bytes = store
        .put("empty/bytes", Bytes::new(), WriteCondition::CreateOnly)
        .await
        .expect_err("an empty create-only body is rejected");
    let empty_from_file = store
        .put_file("empty/file", &empty_file, WriteCondition::CreateOnly)
        .await
        .expect_err("an empty create-only file is rejected");
    for error in [&empty_bytes, &empty_from_file] {
        assert_eq!(error.kind, ObjectStoreErrorKind::Permanent, "{error}");
        assert!(error.detail.contains("empty appendable object"), "{error}");
    }
    let oversized = store
        .put_file("oversized", &oversized_file, WriteCondition::CreateOnly)
        .await
        .expect_err("a create-only file over 5 GiB is rejected");
    assert_eq!(oversized.kind, ObjectStoreErrorKind::Permanent);
    assert!(oversized.detail.contains("5 GiB"), "{oversized}");
    let log = s3local_request_log(emulator.address()).await;
    assert!(!log.contains("AppendObject"), "{log}");
    // Unconditional writes do not use AppendObject, but an empty object would read as absent.
    let empty_plain = store
        .put("empty/plain", Bytes::new(), WriteCondition::Unconditional)
        .await
        .expect_err("an empty unconditional put is rejected");
    assert_eq!(empty_plain.kind, ObjectStoreErrorKind::Permanent);
    let replace = store
        .put(
            "objects/a",
            Bytes::from_static(b"body"),
            WriteCondition::ReplaceIfEtagMatches {
                etag: "\"any\"".to_owned(),
            },
        )
        .await
        .expect_err("replacement is unsupported");
    assert_eq!(replace.kind, ObjectStoreErrorKind::Permanent);
    assert!(replace.detail.contains("Tencent COS"), "{replace}");
    assert!(!store.supports_replace_if_etag_matches_on_every_key());
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_large_create_only_file_is_one_append_and_unconditional_is_multipart() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    let directory = TempDir::new().expect("the file directory is created");
    let input = directory.path().join("snapshot");
    let body: Vec<u8> = (0..20 * 1024 * 1024)
        .map(|index: usize| (index % 251) as u8)
        .collect();
    tokio::fs::write(&input, &body)
        .await
        .expect("the 20 MiB file is written");
    for (key, condition) in [
        ("appended", WriteCondition::CreateOnly),
        ("multipart", WriteCondition::Unconditional),
    ] {
        assert!(matches!(
            store
                .put_file(key, &input, condition)
                .await
                .expect("the 20 MiB upload succeeds"),
            WriteOutcome::Written { .. }
        ));
        let output = directory.path().join(format!("{key}-downloaded"));
        assert_eq!(
            store
                .get_to_file(key, &output)
                .await
                .expect("the download succeeds"),
            Some(body.len() as u64)
        );
        assert!(
            tokio::fs::read(&output)
                .await
                .expect("the download is readable")
                == body,
            "{key}: the downloaded file equals the uploaded file"
        );
    }
    let appended = emulator.record("appended");
    assert!(appended.is_appendable);
    assert_eq!(appended.parts_count, None);
    let multipart = emulator.record("multipart");
    assert!(!multipart.is_appendable);
    assert_eq!(multipart.parts_count, Some(4));
    assert_eq!(
        store
            .put_file("appended", &input, WriteCondition::CreateOnly)
            .await
            .expect("a duplicate large create reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_conflicts_and_ambiguous_appends() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    let address = emulator.address();
    let fault = |prefix: &str, action: &str| {
        format!(
            r#"{{"operation":"AppendObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/{prefix}/","action":{action}}}"#
        )
    };

    set_s3local_fault(
        address,
        &fault("conflict", r#"{"type":"error","status":409}"#),
    )
    .await;
    let conflict = store
        .put(
            "conflict/key",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a 409 without an append code is not a failed condition");
    assert_eq!(conflict.kind, ObjectStoreErrorKind::Transient, "{conflict}");

    set_s3local_fault(
        address,
        &fault("failed", r#"{"type":"error","status":500}"#),
    )
    .await;
    let failed = store
        .put(
            "failed/key",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a server error on an append is ambiguous");
    assert_eq!(
        failed.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{failed}"
    );

    set_s3local_fault(
        address,
        &fault("committed", r#"{"type":"commit_then_drop"}"#),
    )
    .await;
    let dropped = store
        .put(
            "committed/key",
            Bytes::from_static(b"first"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a dropped append response is ambiguous");
    assert_eq!(
        dropped.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{dropped}"
    );
    set_s3local_fault(address, "").await;
    // A retry after an unknown outcome never reports a second write.
    assert_eq!(
        store
            .put(
                "committed/key",
                Bytes::from_static(b"second"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("the retry reaches the store"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .get("committed/key")
            .await
            .expect("get succeeds")
            .expect("the first append landed")
            .bytes,
        Bytes::from_static(b"first")
    );
    let log = s3local_request_log(address).await;
    assert_eq!(
        log.matches(&format!(
            "\"key\":\"{KEY_PREFIX}/conflict/key\",\"operation\":\"AppendObject\""
        ))
        .count(),
        1,
        "appends are never retried: {log}"
    );
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_bucket_checks_pass_on_a_new_bucket_and_leave_no_probe_object() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    check_tencent_cos_bucket_versioning(&store)
        .await
        .expect("an unversioned bucket passes");
    check_tencent_cos_holds_no_data_of_another_provider(&store)
        .await
        .expect("an empty prefix holds no other provider's data");
    check_tencent_cos_create_only_writes(&store)
        .await
        .expect("append creates are exclusive");
    let listing = store.list("", None, 1000).await.expect("list");
    assert!(listing.objects.is_empty(), "{:?}", listing.objects);
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_bucket_versioning_check_rejects_a_versioned_bucket() {
    for versioning in [
        s3local::VersioningState::Enabled,
        s3local::VersioningState::Suspended,
    ] {
        let emulator = TencentCosEmulator::start().await;
        emulator
            .store
            .put_bucket_versioning(BUCKET, versioning)
            .expect("the bucket versioning changes");
        let store = emulator.object_store().await;
        let error = check_tencent_cos_bucket_versioning(&store)
            .await
            .expect_err("a versioned bucket is rejected");
        assert!(
            matches!(
                &error,
                TencentCosBucketCheckError::VersioningConfigured { status }
                    if status == if versioning == s3local::VersioningState::Enabled {
                        "Enabled"
                    } else {
                        "Suspended"
                    }
            ),
            "{versioning:?}: {error:?}"
        );
        assert!(error.to_string().contains("versioning"), "{error}");
        emulator.shutdown().await;
    }
}

#[tokio::test]
async fn tencent_cos_provider_check_rejects_metadata_written_in_place() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    for key in [
        dexzd_object_store::MANIFEST_OBJECT_KEY,
        dexzd_object_store::ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY,
    ] {
        store
            .put(key, Bytes::from_static(b"s3"), WriteCondition::CreateOnly)
            .await
            .expect("the other provider's object is written");
        let error = check_tencent_cos_holds_no_data_of_another_provider(&store)
            .await
            .expect_err("an object at a mutable key came from another provider");
        assert!(
            matches!(
                &error,
                TencentCosBucketCheckError::WrittenByAnotherProvider { key: found } if found == key
            ),
            "{error:?}"
        );
        assert!(error.to_string().contains("tencentCOS"), "{error}");
        store.delete(key).await.expect("the object is deleted");
    }
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_appends_keep_dot_segments_double_slashes_and_percent_signs() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    let keys = [
        "dots/./same",
        "dots/../parent",
        "dots/tail/..",
        "dots/tail/.",
        "slashes//double",
        "percent/%2E%2E/100%",
    ];
    for key in keys {
        let body = Bytes::from(format!("body of {key}"));
        assert!(
            matches!(
                store
                    .put(key, body.clone(), WriteCondition::CreateOnly)
                    .await
                    .expect("the append succeeds"),
                WriteOutcome::Written { .. }
            ),
            "{key}"
        );
        assert!(emulator.record(key).is_appendable, "{key}");
        assert_eq!(
            store
                .get(key)
                .await
                .expect("get succeeds")
                .expect("the object exists at its own key")
                .bytes,
            body,
            "{key}"
        );
        assert_eq!(
            store
                .put(
                    key,
                    Bytes::from_static(b"again"),
                    WriteCondition::CreateOnly
                )
                .await
                .expect("a duplicate append reaches the store"),
            WriteOutcome::PreconditionFailed,
            "{key}"
        );
    }
    // A normalized path would have written these keys instead.
    for normalized in ["dots/same", "parent", "dots", "dots/tail", "percent/100%"] {
        assert!(
            store
                .head(normalized)
                .await
                .expect("head succeeds")
                .is_none(),
            "{normalized}"
        );
    }
    let listing = store.list("", None, 1000).await.expect("list succeeds");
    assert_eq!(listing.objects.len(), keys.len(), "{:?}", listing.objects);
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_a_server_error_after_the_append_commits_is_an_unknown_outcome() {
    let emulator = TencentCosEmulator::start().await;
    let store = emulator.object_store().await;
    set_s3local_fault(
        emulator.address(),
        &format!(
            r#"{{"operation":"AppendObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/slow/","action":{{"type":"commit_then_error","status":503}}}}"#
        ),
    )
    .await;
    let error = store
        .put(
            "slow/key",
            Bytes::from_static(b"first"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a 503 SlowDown may follow a committed append");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown, "{error}");
    set_s3local_fault(emulator.address(), "").await;
    // The caller reconciles by reading: the first body is there, and a retry writes nothing.
    assert_eq!(
        store
            .get("slow/key")
            .await
            .expect("get succeeds")
            .expect("the append committed")
            .bytes,
        Bytes::from_static(b"first")
    );
    assert_eq!(
        store
            .put(
                "slow/key",
                Bytes::from_static(b"second"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("the retry reaches the store"),
        WriteOutcome::PreconditionFailed
    );
    emulator.shutdown().await;
}

#[tokio::test]
async fn tencent_cos_an_append_whose_key_is_recreated_before_its_etag_read_is_unknown() {
    let emulator = TencentCosEmulator::start().await;
    let store = Arc::new(emulator.object_store().await);
    set_s3local_fault(
        emulator.address(),
        &format!(
            r#"{{"operation":"HeadObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/recreated/","action":{{"type":"delay","milliseconds":1500}}}}"#
        ),
    )
    .await;
    let first_store = Arc::clone(&store);
    let first = tokio::spawn(async move {
        first_store
            .put(
                "recreated/key",
                Bytes::from_static(b"first"),
                WriteCondition::CreateOnly,
            )
            .await
    });
    let deadline = tokio::time::Instant::now() + std::time::Duration::from_secs(5);
    while store
        .get("recreated/key")
        .await
        .expect("get succeeds")
        .is_none()
    {
        assert!(
            tokio::time::Instant::now() < deadline,
            "the first append landed"
        );
        tokio::time::sleep(std::time::Duration::from_millis(10)).await;
    }
    store
        .delete("recreated/key")
        .await
        .expect("the first object is deleted");
    let second_store = Arc::clone(&store);
    let second = tokio::spawn(async move {
        second_store
            .put(
                "recreated/key",
                Bytes::from_static(b"second"),
                WriteCondition::CreateOnly,
            )
            .await
    });
    let first = first.await.expect("the first writer completes");
    let error = first.expect_err("the ETag read found another writer's object");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown, "{error}");
    assert!(error.detail.contains("another append"), "{error}");
    let second = second
        .await
        .expect("the second writer completes")
        .expect("the second append reads its own ETag");
    let WriteOutcome::Written { etag } = second else {
        panic!("the second append wrote: {second:?}");
    };
    let content = store
        .get("recreated/key")
        .await
        .expect("get succeeds")
        .expect("the second object exists");
    assert_eq!(content.bytes, Bytes::from_static(b"second"));
    assert_eq!(content.etag, etag);
    emulator.shutdown().await;
}
