// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Object-store conformance of `AliyunOssObjectStore` over an in-memory create-only store.
//!
//! Plain keys run the shared conformance cases that need no replacement. Mutable object keys
//! run every case, including replacement, through the versioned layout.

#[expect(
    dead_code,
    reason = "these tests use only part of the shared create-only store"
)]
#[path = "aliyun_oss/create_only_store.rs"]
mod create_only_store;

use std::sync::Arc;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, ByteRange, InMemoryObjectStore, InMemoryObjectStoreOptions,
    InstrumentedObjectStore, MANIFEST_OBJECT_KEY, MUTABLE_OBJECT_KEY_PREFIXES, MUTABLE_OBJECT_KEYS,
    ObjectStore, ObjectStoreErrorKind, WriteCondition, WriteOutcome, is_mutable_object_key,
    node_lease_object_key, startup_self_check,
};

use create_only_store::{CreateOnlyStore, etag_generation, version_key};

const MUTABLE_KEYS: [&str; 4] = [
    "manifest",
    "registry/attribute-indexes",
    "lease/node-a",
    "probe/1-2",
];

#[tokio::test(start_paused = true)]
async fn plain_keys_keep_create_only_conformance_and_reject_replacement() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    assert!(store.get("missing").await.expect("get").is_none());
    assert!(store.head("missing").await.expect("head").is_none());
    let missing_range = ByteRange {
        start: 0,
        end_inclusive: 2,
    };
    assert!(
        store
            .get_range("missing", missing_range)
            .await
            .expect("get_range")
            .is_none()
    );
    let created = store
        .put(
            "objects/a",
            Bytes::from_static(b"abcdef"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create");
    let WriteOutcome::Written { etag } = created else {
        panic!("the initial create must succeed");
    };
    assert_eq!(
        store
            .put(
                "objects/a",
                Bytes::from_static(b"wrong"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("duplicate create"),
        WriteOutcome::PreconditionFailed
    );
    let replacement = store
        .put(
            "objects/a",
            Bytes::from_static(b"next"),
            WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() },
        )
        .await
        .expect_err("a plain key rejects replacement");
    assert_eq!(replacement.kind, ObjectStoreErrorKind::Permanent);
    assert_eq!(replacement.key, "objects/a");
    let content = store
        .get("objects/a")
        .await
        .expect("get")
        .expect("the object exists");
    assert_eq!(content.bytes, Bytes::from_static(b"abcdef"));
    assert_eq!(content.etag, etag);
    let range = store
        .get_range(
            "objects/a",
            ByteRange {
                start: 2,
                end_inclusive: 100,
            },
        )
        .await
        .expect("get_range")
        .expect("the object exists");
    assert_eq!(range.bytes, Bytes::from_static(b"cdef"));
    assert_eq!(range.object_size, 6);
    for key in ["objects/b", "objects/c"] {
        store
            .put(key, Bytes::from_static(b"x"), WriteCondition::Unconditional)
            .await
            .expect("an unconditional plain write passes through");
    }
    let first = store.list("objects/", None, 2).await.expect("list");
    assert_eq!(
        first
            .objects
            .iter()
            .map(|object| object.key.as_str())
            .collect::<Vec<_>>(),
        ["objects/a", "objects/b"]
    );
    let second = store
        .list("objects/", first.next_start_after.as_deref(), 2)
        .await
        .expect("list");
    assert_eq!(second.objects.len(), 1);
    assert!(second.next_start_after.is_none());
    let directory = tempfile::tempdir().expect("a temporary directory");
    let input = directory.path().join("input");
    let output = directory.path().join("output");
    tokio::fs::write(&input, b"from-file")
        .await
        .expect("write input");
    store
        .put_file("file", &input, WriteCondition::CreateOnly)
        .await
        .expect("put_file");
    assert!(
        store
            .put_file(
                "file",
                &input,
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "\"absent\"".to_owned()
                }
            )
            .await
            .is_err()
    );
    assert_eq!(
        store
            .get_to_file("file", &output)
            .await
            .expect("get_to_file"),
        Some(9)
    );
    store
        .delete_many(&[
            "objects/a".to_owned(),
            "objects/b".to_owned(),
            "absent".to_owned(),
        ])
        .await
        .expect("delete_many");
    store.delete("objects/c").await.expect("delete");
    assert!(
        store
            .list("objects/", None, 10)
            .await
            .expect("list")
            .objects
            .is_empty()
    );
    let too_many = store
        .delete_many(&vec!["unused".to_owned(); 1001])
        .await
        .expect_err("more than 1000 keys are rejected");
    assert_eq!(too_many.kind, ObjectStoreErrorKind::Permanent);
    assert_eq!(physical.physical_keys("").await, ["file"]);
}

#[tokio::test(start_paused = true)]
async fn mutable_keys_pass_the_full_conformance_set() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    for key in MUTABLE_KEYS {
        check_mutable_key(&store, key).await;
    }
    let directory = tempfile::tempdir().expect("a temporary directory");
    let input = directory.path().join("input");
    let output = directory.path().join("output");
    tokio::fs::write(&input, b"from-file")
        .await
        .expect("write input");
    let created = store
        .put_file(MANIFEST_OBJECT_KEY, &input, WriteCondition::CreateOnly)
        .await
        .expect("put_file");
    let WriteOutcome::Written { etag } = created else {
        panic!("a file create on a deleted key succeeds");
    };
    assert_eq!(
        store
            .get_to_file(MANIFEST_OBJECT_KEY, &output)
            .await
            .expect("get_to_file"),
        Some(9)
    );
    assert_eq!(
        tokio::fs::read(&output).await.expect("read output"),
        b"from-file"
    );
    assert!(
        store
            .get_to_file("lease/absent", &directory.path().join("absent"))
            .await
            .expect("get_to_file")
            .is_none()
    );
    assert!(!directory.path().join("absent").exists());
    let large = Bytes::from(vec![42; 6 * 1024 * 1024]);
    let replaced = store
        .put(
            MANIFEST_OBJECT_KEY,
            large.clone(),
            WriteCondition::ReplaceIfEtagMatches { etag },
        )
        .await
        .expect("a large replacement");
    assert!(matches!(replaced, WriteOutcome::Written { .. }));
    let content = store
        .get(MANIFEST_OBJECT_KEY)
        .await
        .expect("get")
        .expect("exists");
    assert_eq!(content.bytes, large);
    startup_self_check(&store)
        .await
        .expect("the startup self check passes through the adapter");
    assert!(physical.physical_keys("probe/").await.is_empty());
}

/// Runs the shared conformance cases, including replacement, on one mutable key.
async fn check_mutable_key(store: &AliyunOssObjectStore, key: &str) {
    assert!(store.get(key).await.expect("get").is_none());
    assert!(store.head(key).await.expect("head").is_none());
    let range = ByteRange {
        start: 2,
        end_inclusive: 100,
    };
    assert!(store.get_range(key, range).await.expect("range").is_none());
    for etag in [
        "\"absent\"",
        "wrong",
        "\"g1-00000000000000000000000000000000\"",
    ] {
        assert_eq!(
            store
                .put(
                    key,
                    Bytes::from_static(b"body"),
                    WriteCondition::ReplaceIfEtagMatches {
                        etag: etag.to_owned()
                    }
                )
                .await
                .expect("a missing-key replacement reports the condition"),
            WriteOutcome::PreconditionFailed
        );
    }
    let unconditional = store
        .put(key, Bytes::from_static(b"x"), WriteCondition::Unconditional)
        .await
        .expect_err("a mutable key rejects unconditional writes");
    assert_eq!(unconditional.kind, ObjectStoreErrorKind::Permanent);
    let WriteOutcome::Written { etag } = store
        .put(
            key,
            Bytes::from_static(b"abcdef"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create")
    else {
        panic!("the initial create must succeed");
    };
    assert_eq!(etag_generation(&etag), 1);
    assert_eq!(
        store
            .put(
                key,
                Bytes::from_static(b"again"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("duplicate create"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .put(
                key,
                Bytes::from_static(b"wrong"),
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "wrong".to_owned()
                }
            )
            .await
            .expect("a wrong replacement"),
        WriteOutcome::PreconditionFailed
    );
    let content = store.get(key).await.expect("get").expect("exists");
    assert_eq!(content.bytes, Bytes::from_static(b"abcdef"));
    assert_eq!(content.etag, etag);
    assert_eq!(content.object_size, 6);
    let sliced = store
        .get_range(key, range)
        .await
        .expect("range")
        .expect("exists");
    assert_eq!(sliced.bytes, Bytes::from_static(b"cdef"));
    assert_eq!(sliced.object_size, 6);
    assert_eq!(sliced.etag, etag);
    let beyond = store
        .get_range(
            key,
            ByteRange {
                start: 100,
                end_inclusive: 200,
            },
        )
        .await
        .expect("range")
        .expect("exists");
    assert!(beyond.bytes.is_empty());
    assert_eq!(beyond.object_size, 6);
    let metadata = store.head(key).await.expect("head").expect("exists");
    assert_eq!(metadata.key, key);
    assert_eq!(metadata.size, 6);
    assert_eq!(metadata.etag, etag);
    assert!(metadata.last_modified_unix_micros.is_some());
    let WriteOutcome::Written { etag: next_etag } = store
        .put(
            key,
            Bytes::from_static(b"next"),
            WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() },
        )
        .await
        .expect("replace")
    else {
        panic!("a replacement with the current ETag must succeed");
    };
    assert_eq!(etag_generation(&next_etag), 2);
    assert_eq!(
        store
            .put(
                key,
                Bytes::from_static(b"stale"),
                WriteCondition::ReplaceIfEtagMatches { etag }
            )
            .await
            .expect("a stale replacement"),
        WriteOutcome::PreconditionFailed
    );
    let metadata = store.head(key).await.expect("head").expect("exists");
    assert_eq!(metadata.size, 4);
    assert_eq!(metadata.etag, next_etag);
    let listed = store.list(key, None, 10).await.expect("list");
    assert_eq!(listed.objects, [metadata]);
    store.delete(key).await.expect("delete");
    assert!(store.get(key).await.expect("get").is_none());
    assert!(store.head(key).await.expect("head").is_none());
    assert!(
        store
            .list(key, None, 10)
            .await
            .expect("list")
            .objects
            .is_empty()
    );
    store.delete(key).await.expect("a second delete succeeds");
    assert_eq!(
        store
            .put(
                key,
                Bytes::from_static(b"after"),
                WriteCondition::ReplaceIfEtagMatches { etag: next_etag }
            )
            .await
            .expect("a replacement after delete"),
        WriteOutcome::PreconditionFailed
    );
    let recreated = store
        .put(
            key,
            Bytes::from_static(b"again"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create after delete");
    let WriteOutcome::Written { etag } = recreated else {
        panic!("a create after delete must succeed");
    };
    // Probe keys are purged; every other key keeps its tombstone and continues its generations.
    let expected_generation = if key.starts_with("probe/") { 1 } else { 4 };
    assert_eq!(etag_generation(&etag), expected_generation);
    store
        .delete_many(&[key.to_owned(), "absent".to_owned()])
        .await
        .expect("delete_many");
    assert!(store.get(key).await.expect("get").is_none());
}

#[tokio::test(start_paused = true)]
async fn listings_collapse_versions_into_logical_keys_in_logical_order() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    for key in [
        "manifest",
        "lease/node-a",
        "lease/node-a-b",
        "lease/node-ab",
        "lease/node-gone",
    ] {
        store
            .put(key, Bytes::from_static(b"v1"), WriteCondition::CreateOnly)
            .await
            .expect("create");
    }
    let current = store
        .get("lease/node-a")
        .await
        .expect("get")
        .expect("exists");
    store
        .put(
            "lease/node-a",
            Bytes::from_static(b"v2"),
            WriteCondition::ReplaceIfEtagMatches { etag: current.etag },
        )
        .await
        .expect("replace");
    store.delete("lease/node-gone").await.expect("delete");
    for key in ["manifest-x", "manifest/x", "manifesto"] {
        store
            .put(
                key,
                Bytes::from_static(b"plain"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("create plain");
    }
    let expected = [
        "lease/node-a",
        "lease/node-a-b",
        "lease/node-ab",
        "manifest",
        "manifest-x",
        "manifest/x",
        "manifesto",
    ];
    let mut listed = Vec::new();
    let mut start_after = None;
    loop {
        let page = store
            .list("", start_after.as_deref(), 2)
            .await
            .expect("list");
        listed.extend(page.objects.into_iter().map(|object| object.key));
        match page.next_start_after {
            Some(next) => start_after = Some(next),
            None => break,
        }
    }
    assert_eq!(listed, expected);
    let leases = store.list("lease/", None, 10).await.expect("list");
    assert_eq!(leases.objects.len(), 3);
    assert_eq!(leases.objects[0].size, 2);
    assert_eq!(etag_generation(&leases.objects[0].etag), 2);
    assert!(leases.next_start_after.is_none());
    assert!(
        store
            .list("manifest.", None, 10)
            .await
            .expect("list")
            .objects
            .is_empty()
    );
    let exact = store.list("manifest", None, 10).await.expect("list");
    assert_eq!(
        exact
            .objects
            .iter()
            .map(|object| object.key.as_str())
            .collect::<Vec<_>>(),
        ["manifest", "manifest-x", "manifest/x", "manifesto"]
    );
    assert_eq!(
        physical.generations("lease/node-gone").await,
        [2, 1],
        "a delete writes a tombstone version"
    );
}

#[tokio::test(start_paused = true)]
async fn keys_that_would_make_the_layout_ambiguous_are_rejected() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(physical.inner());
    let reserved = version_key(MANIFEST_OBJECT_KEY, 1);
    for key in [
        reserved.as_str(),
        "registry/attribute-indexes.v/1",
        "lease/a.b",
    ] {
        let put = store
            .put(key, Bytes::from_static(b"x"), WriteCondition::CreateOnly)
            .await
            .expect_err("the key is rejected");
        assert_eq!(put.kind, ObjectStoreErrorKind::Permanent);
        assert_eq!(put.key, key);
        let get = store.get(key).await.expect_err("the key is rejected");
        assert_eq!(get.kind, ObjectStoreErrorKind::Permanent);
        let delete = store
            .delete_many(&["plain".to_owned(), key.to_owned()])
            .await
            .expect_err("the batch is rejected");
        assert_eq!(delete.kind, ObjectStoreErrorKind::Permanent);
    }
    assert!(physical.physical_keys("").await.is_empty());
}

#[tokio::test(start_paused = true)]
async fn only_stores_with_replacement_everywhere_report_the_capability() {
    let physical = CreateOnlyStore::new();
    let adapter: Arc<dyn ObjectStore> = Arc::new(AliyunOssObjectStore::new(physical.inner()));
    assert!(!adapter.supports_replace_if_etag_matches_on_every_key());
    let instrumented =
        InstrumentedObjectStore::new(Arc::clone(&adapter), NodeMetrics::unregistered());
    assert!(!instrumented.supports_replace_if_etag_matches_on_every_key());
    assert!(
        !physical
            .store
            .supports_replace_if_etag_matches_on_every_key()
    );
    assert!(InMemoryObjectStore::new().supports_replace_if_etag_matches_on_every_key());
    let unrestricted = InMemoryObjectStore::with_options(InMemoryObjectStoreOptions::default());
    assert!(unrestricted.supports_replace_if_etag_matches_on_every_key());
}

#[test]
fn mutable_object_keys_never_contain_the_version_separator() {
    for key in MUTABLE_OBJECT_KEYS
        .iter()
        .chain(MUTABLE_OBJECT_KEY_PREFIXES.iter())
    {
        assert!(!key.contains('.'), "{key} contains '.'");
        assert!(is_mutable_object_key(key));
    }
    assert!(is_mutable_object_key(&node_lease_object_key("node-a")));
    assert!(!is_mutable_object_key("manifest/x"));
    assert!(!is_mutable_object_key("snapshot/node-a/manifest/1"));
}
