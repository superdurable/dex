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
    ObjectStore, ObjectStoreErrorKind, S3ApiDialect, S3Credentials, S3ObjectStore,
    S3ObjectStoreConfig, StartupSelfCheckFailure, VersionDiscovery, WriteCondition, WriteOutcome,
    is_mutable_object_key, node_lease_object_key, startup_self_check,
};
use s3local::{Durability, EtagMode, Store, StoreConfig};

use create_only_store::{
    CreateOnlyStore, TEST_MINIMUM_SUPERSEDED_AGE, etag_generation, version_key,
};

const MUTABLE_KEYS: [&str; 7] = [
    "manifest",
    "registry/attribute-indexes",
    "lease/node-a",
    "probe/1-2",
    "registry/flow-definitions/0/types/Order_Flow-v1.2",
    "registry/flow-definitions/0/types/ends.v",
    "registry/flow-definitions/0/types/v",
];

/// Prefix of the Flow definition records of the default tenant.
const FLOW_DEFINITION_TYPES_PREFIX: &str = "registry/flow-definitions/0/types/";

#[tokio::test(start_paused = true)]
async fn plain_keys_keep_create_only_conformance_and_reject_replacement() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
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
    check_full_conformance_set(VersionDiscovery::Listing).await;
}

#[tokio::test(start_paused = true)]
async fn mutable_keys_pass_the_full_conformance_set_with_head_probing() {
    check_full_conformance_set(VersionDiscovery::HeadProbing).await;
}

async fn check_full_conformance_set(version_discovery: VersionDiscovery) {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::with_version_discovery(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
        version_discovery,
    );
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
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
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
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    let reserved = version_key(MANIFEST_OBJECT_KEY, 1);
    for key in [
        reserved.as_str(),
        "registry/attribute-indexes.v/1",
        "manifest.v/x.v/00000000000000000001",
        "lease/a.v/b",
        "registry/flow-definitions/0/types/a.v/b",
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
    // Dots elsewhere keep the layout unambiguous.
    for key in [
        "lease/a.b",
        "registry/flow-definitions/0/types/a.v.v",
        "x/manifest.v/1",
    ] {
        let created = store
            .put(key, Bytes::from_static(b"x"), WriteCondition::CreateOnly)
            .await
            .expect("the key is accepted");
        assert!(matches!(created, WriteOutcome::Written { .. }), "{key}");
        let content = store.get(key).await.expect("get").expect("exists");
        assert_eq!(content.bytes, Bytes::from_static(b"x"));
    }
}

#[tokio::test(start_paused = true)]
async fn many_flow_definitions_list_in_logical_order_across_physical_pages() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    let mut expected = Vec::new();
    for index in 0..520 {
        let segment = match index % 5 {
            0 => format!("Order_Flow-v1.{index}"),
            1 => format!("flow{index}.v"),
            2 => format!("flow{index}%2Fpart"),
            3 => format!("flow{index}"),
            _ => format!("flow{index}-v"),
        };
        let key = format!("{FLOW_DEFINITION_TYPES_PREFIX}{segment}");
        let WriteOutcome::Written { etag } = store
            .put(
                &key,
                Bytes::from_static(b"first"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("create")
        else {
            panic!("the create of {key} must succeed");
        };
        store
            .put(
                &key,
                Bytes::from_static(b"second"),
                WriteCondition::ReplaceIfEtagMatches { etag },
            )
            .await
            .expect("replace");
        if index % 13 == 0 {
            store.delete(&key).await.expect("delete");
        } else {
            expected.push(key);
        }
    }
    expected.push(format!("{FLOW_DEFINITION_TYPES_PREFIX}v"));
    store
        .put(
            &format!("{FLOW_DEFINITION_TYPES_PREFIX}v"),
            Bytes::from_static(b"second"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create");
    expected.sort();
    assert!(
        physical
            .physical_keys(FLOW_DEFINITION_TYPES_PREFIX)
            .await
            .len()
            > 1000
    );
    let mut listed = Vec::new();
    let mut start_after = None;
    loop {
        let page = store
            .list(FLOW_DEFINITION_TYPES_PREFIX, start_after.as_deref(), 97)
            .await
            .expect("list");
        assert!(page.objects.len() <= 97);
        for object in &page.objects {
            assert_eq!(object.size, 6, "{} lists its newest version", object.key);
        }
        listed.extend(page.objects.into_iter().map(|object| object.key));
        match page.next_start_after {
            Some(next) => start_after = Some(next),
            None => break,
        }
    }
    assert_eq!(listed, expected);
}

#[tokio::test(start_paused = true)]
async fn only_stores_with_replacement_everywhere_report_the_capability() {
    let physical = CreateOnlyStore::new();
    let adapter: Arc<dyn ObjectStore> = Arc::new(AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    ));
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
        assert!(!key.contains(".v/"), "{key} contains \".v/\"");
        assert!(is_mutable_object_key(key));
    }
    assert!(is_mutable_object_key(&node_lease_object_key("node-a")));
    assert!(!is_mutable_object_key("manifest/x"));
    assert!(!is_mutable_object_key("snapshot/node-a/manifest/1"));
    assert!(is_mutable_object_key(
        "registry/flow-definitions/7/types/Order_Flow-v1.2"
    ));
}

/// Starts s3local in `emulated_dialect` and returns the S3 store that speaks to it in `dialect`.
async fn start_dialect_emulator(
    directory: &std::path::Path,
    etag_mode: EtagMode,
    emulated_dialect: s3local_server::S3ApiDialect,
    dialect: S3ApiDialect,
) -> (s3local_server::ServerHandle, S3ObjectStore) {
    let emulated = Store::open(&StoreConfig {
        root_directory: directory.to_path_buf(),
        durability: Durability::Relaxed,
        etag_mode,
    })
    .expect("the emulator store opens");
    emulated
        .create_bucket("versioned-cas", false)
        .expect("the emulator bucket is created");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the emulator binds");
    let server = s3local_server::start_with_config(
        Arc::new(emulated),
        listener,
        s3local_server::ServerConfig {
            api_dialect: emulated_dialect,
            ..s3local_server::ServerConfig::default()
        },
    )
    .expect("the emulator starts");
    let mut config = S3ObjectStoreConfig::new("us-east-1", "versioned-cas");
    config.endpoint = Some(format!("http://{}", server.local_address()));
    config.key_prefix = "dexzd/versioned".to_owned();
    config.force_path_style = true;
    config.credentials = S3Credentials::Static {
        access_key_id: "s3local".to_owned(),
        secret_access_key: "s3local".to_owned(),
        session_token: None,
    };
    config.multipart_threshold = 5 * 1024 * 1024;
    config.multipart_part_size = 5 * 1024 * 1024;
    config.api_dialect = dialect;
    let object_store = S3ObjectStore::new(config)
        .await
        .expect("the dialect store is configured");
    (server, object_store)
}

#[tokio::test]
async fn the_adapter_keeps_its_guarantees_over_the_s3_aliyun_oss_dialect() {
    check_adapter_guarantees_over_dialect(
        s3local_server::S3ApiDialect::AliyunOss,
        S3ApiDialect::AliyunOss,
        VersionDiscovery::Listing,
    )
    .await;
}

#[tokio::test]
async fn the_adapter_keeps_its_guarantees_over_the_s3_tencent_cos_dialect() {
    check_adapter_guarantees_over_dialect(
        s3local_server::S3ApiDialect::TencentCos,
        S3ApiDialect::TencentCos,
        VersionDiscovery::Listing,
    )
    .await;
}

#[tokio::test]
async fn head_probing_keeps_its_guarantees_over_the_s3_aliyun_oss_dialect() {
    check_adapter_guarantees_over_dialect(
        s3local_server::S3ApiDialect::AliyunOss,
        S3ApiDialect::AliyunOss,
        VersionDiscovery::HeadProbing,
    )
    .await;
}

#[tokio::test]
async fn head_probing_keeps_its_guarantees_over_the_s3_tencent_cos_dialect() {
    check_adapter_guarantees_over_dialect(
        s3local_server::S3ApiDialect::TencentCos,
        S3ApiDialect::TencentCos,
        VersionDiscovery::HeadProbing,
    )
    .await;
}

/// Over the COS dialect with stale listings, as on real COS, only head probing passes the check.
#[tokio::test]
async fn the_startup_self_check_over_stale_cos_listings_needs_head_probing() {
    let directory = tempfile::tempdir().expect("a temporary directory");
    let (server, s3_store) = start_dialect_emulator(
        directory.path(),
        EtagMode::Opaque,
        s3local_server::S3ApiDialect::TencentCos,
        S3ApiDialect::TencentCos,
    )
    .await;
    set_stale_listing_fault(server.local_address()).await;
    let inner: Arc<dyn ObjectStore> = Arc::new(s3_store);
    let listing = AliyunOssObjectStore::new(
        Arc::clone(&inner),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    let error = startup_self_check(&listing)
        .await
        .expect_err("listing discovery cannot confirm a create over a stale listing");
    assert_eq!(error.check, StartupSelfCheckFailure::InitialCreate);
    let source = error.source.expect("the create failed in the store");
    assert_eq!(source.kind, ObjectStoreErrorKind::OutcomeUnknown);
    assert!(
        source.detail.contains("shows no well-formed version"),
        "{source}"
    );
    let head_probing = AliyunOssObjectStore::with_version_discovery(
        Arc::clone(&inner),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
        VersionDiscovery::HeadProbing,
    );
    startup_self_check(&head_probing)
        .await
        .expect("head probing passes the startup self check over stale listings");
    let mut etag = match head_probing
        .put(
            MANIFEST_OBJECT_KEY,
            Bytes::from_static(b"one"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create")
    {
        WriteOutcome::Written { etag } => etag,
        WriteOutcome::PreconditionFailed => panic!("the manifest is absent"),
    };
    for body in ["two", "three", "four"] {
        // A new adapter has no hint, so it starts from the stale listing.
        let fresh = AliyunOssObjectStore::with_version_discovery(
            Arc::clone(&inner),
            TEST_MINIMUM_SUPERSEDED_AGE,
            NodeMetrics::unregistered(),
            VersionDiscovery::HeadProbing,
        );
        assert_eq!(
            fresh
                .get(MANIFEST_OBJECT_KEY)
                .await
                .expect("get")
                .expect("exists")
                .etag,
            etag
        );
        etag = match fresh
            .put(
                MANIFEST_OBJECT_KEY,
                Bytes::from_static(body.as_bytes()),
                WriteCondition::ReplaceIfEtagMatches { etag },
            )
            .await
            .expect("replace")
        {
            WriteOutcome::Written { etag } => etag,
            WriteOutcome::PreconditionFailed => panic!("the replacement uses the newest ETag"),
        };
    }
    assert_eq!(etag_generation(&etag), 4);
    server.shutdown().await;
}

async fn set_stale_listing_fault(address: std::net::SocketAddr) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let body = r#"{"rules":[{"operation":"ListObjectsV2","action":{"type":"stale_listing","ttl_milliseconds":600000}}]}"#;
    let mut connection = tokio::net::TcpStream::connect(address)
        .await
        .expect("the s3local admin endpoint is reachable");
    let request = format!(
        "PUT /_s3local/faults HTTP/1.1\r\nHost: {address}\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{body}",
        body.len()
    );
    connection
        .write_all(request.as_bytes())
        .await
        .expect("the fault request is sent");
    let mut response = Vec::new();
    connection
        .read_to_end(&mut response)
        .await
        .expect("the fault response is read");
    assert!(
        response.starts_with(b"HTTP/1.1 200"),
        "{}",
        String::from_utf8_lossy(&response)
    );
}

async fn check_adapter_guarantees_over_dialect(
    emulated_dialect: s3local_server::S3ApiDialect,
    dialect: S3ApiDialect,
    version_discovery: VersionDiscovery,
) {
    for etag_mode in [EtagMode::Md5, EtagMode::Opaque] {
        let directory = tempfile::tempdir().expect("a temporary directory");
        let (server, s3_store) =
            start_dialect_emulator(directory.path(), etag_mode, emulated_dialect, dialect).await;
        let inner: Arc<dyn ObjectStore> = Arc::new(s3_store);
        let store = AliyunOssObjectStore::with_version_discovery(
            Arc::clone(&inner),
            TEST_MINIMUM_SUPERSEDED_AGE,
            NodeMetrics::unregistered(),
            version_discovery,
        );
        for key in MUTABLE_KEYS {
            check_mutable_key(&store, key).await;
        }
        // Six MiB exceeds the multipart threshold, so these versions are multipart objects;
        // the Tencent COS dialect appends them in one request instead.
        let large = Bytes::from(vec![42; 6 * 1024 * 1024]);
        let WriteOutcome::Written { etag } = store
            .put(
                MANIFEST_OBJECT_KEY,
                large.clone(),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("a large create")
        else {
            panic!("a large create after a delete must succeed");
        };
        let larger = Bytes::from(vec![7; 6 * 1024 * 1024 + 1]);
        let replaced = store
            .put(
                MANIFEST_OBJECT_KEY,
                larger.clone(),
                WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() },
            )
            .await
            .expect("a large replacement");
        assert!(matches!(replaced, WriteOutcome::Written { .. }));
        assert_eq!(
            store
                .put(
                    MANIFEST_OBJECT_KEY,
                    large,
                    WriteCondition::ReplaceIfEtagMatches { etag }
                )
                .await
                .expect("a stale replacement"),
            WriteOutcome::PreconditionFailed
        );
        assert_eq!(
            store
                .get(MANIFEST_OBJECT_KEY)
                .await
                .expect("get")
                .expect("exists")
                .bytes,
            larger
        );
        // Bound reads rely on listing, whole reads, and range reads naming one object alike.
        let listed = inner
            .list("manifest.v/", None, 1)
            .await
            .expect("list")
            .objects
            .remove(0);
        let whole = inner.get(&listed.key).await.expect("get").expect("exists");
        let header = inner
            .get_range(
                &listed.key,
                ByteRange {
                    start: 0,
                    end_inclusive: 37,
                },
            )
            .await
            .expect("get_range")
            .expect("exists");
        assert_eq!(listed.etag, whole.etag, "{etag_mode:?}");
        assert_eq!(listed.etag, header.etag, "{etag_mode:?}");
        assert_eq!(listed.size, whole.object_size);
        assert_eq!(listed.size, header.object_size);
        startup_self_check(&store)
            .await
            .expect("the startup self check passes through the adapter");
        assert_eq!(
            store
                .remove_stale_startup_probes()
                .await
                .expect("the probe sweep reads store time"),
            0
        );
        assert!(
            inner
                .list("probe/", None, 10)
                .await
                .expect("list")
                .objects
                .is_empty()
        );
        server.shutdown().await;
    }
}
