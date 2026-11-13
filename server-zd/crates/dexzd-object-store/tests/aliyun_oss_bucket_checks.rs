// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Timeouts and cleanup failures of the Aliyun OSS create-only bucket check, and the version
//! discovery marker check.

use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssBucketCheckError, AliyunOssObjectStore, FaultAction, FaultInjector,
    HEAD_PROBING_MARKER_OBJECT_KEY, InMemoryObjectStore, InMemoryObjectStoreOptions,
    MANIFEST_OBJECT_KEY, ObjectStore, ObjectStoreOperation, STARTUP_SELF_CHECK_DEADLINE,
    VersionDiscovery, VersionDiscoveryCheckError, WriteCondition,
    check_aliyun_oss_create_only_writes, check_version_discovery_marker, startup_self_check,
};

/// Delays or fails the second probe write and, optionally, every delete.
struct ProbeFaults {
    puts: AtomicU64,
    second_put: FaultAction,
    delete: FaultAction,
}

impl FaultInjector for ProbeFaults {
    fn action(&self, operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        match operation {
            ObjectStoreOperation::Put if self.puts.fetch_add(1, Ordering::SeqCst) == 1 => {
                self.second_put
            }
            ObjectStoreOperation::Delete => self.delete,
            _ => FaultAction::Proceed,
        }
    }
}

fn store_with(second_put: FaultAction, delete: FaultAction) -> InMemoryObjectStore {
    InMemoryObjectStore::with_injector(Arc::new(ProbeFaults {
        puts: AtomicU64::new(0),
        second_put,
        delete,
    }))
}

async fn object_count(store: &InMemoryObjectStore) -> usize {
    store
        .list("", None, 1000)
        .await
        .expect("the store lists")
        .objects
        .len()
}

#[tokio::test(start_paused = true)]
async fn a_check_that_times_out_still_deletes_its_probe_object() {
    let store = store_with(
        FaultAction::Delay(STARTUP_SELF_CHECK_DEADLINE + Duration::from_secs(60)),
        FaultAction::Proceed,
    );
    let error = check_aliyun_oss_create_only_writes(&store)
        .await
        .expect_err("the second write outlasts the deadline");
    assert!(
        matches!(error, AliyunOssBucketCheckError::TimedOut { .. }),
        "{error:?}"
    );
    assert_eq!(object_count(&store).await, 0);
}

#[tokio::test(start_paused = true)]
async fn a_failed_check_reports_a_failed_cleanup_too() {
    let store = store_with(FaultAction::FailBeforeEffect, FaultAction::FailBeforeEffect);
    let error = check_aliyun_oss_create_only_writes(&store)
        .await
        .expect_err("the second write fails");
    let AliyunOssBucketCheckError::CheckAndCleanupFailed { check, cleanup } = &error else {
        panic!("both failures are reported: {error:?}");
    };
    assert!(
        matches!(**check, AliyunOssBucketCheckError::ObjectStore(_)),
        "{check:?}"
    );
    assert!(!cleanup.is_empty());
    assert!(
        error.to_string().contains("deleting the probe object"),
        "{error}"
    );
}

#[tokio::test(start_paused = true)]
async fn a_passed_check_reports_a_failed_cleanup() {
    let store = store_with(FaultAction::Proceed, FaultAction::FailBeforeEffect);
    let error = check_aliyun_oss_create_only_writes(&store)
        .await
        .expect_err("the probe delete fails");
    assert!(
        matches!(error, AliyunOssBucketCheckError::CleanupFailed { .. }),
        "{error:?}"
    );
}

fn create_only_store() -> Arc<InMemoryObjectStore> {
    Arc::new(InMemoryObjectStore::with_options(
        InMemoryObjectStoreOptions {
            rejects_replace_if_etag_matches: true,
            ..InMemoryObjectStoreOptions::default()
        },
    ))
}

async fn has_marker(store: &InMemoryObjectStore) -> bool {
    store
        .head(HEAD_PROBING_MARKER_OBJECT_KEY)
        .await
        .expect("head")
        .is_some()
}

#[tokio::test(start_paused = true)]
async fn head_probing_marks_an_empty_prefix_that_listing_then_refuses() {
    let store = create_only_store();
    check_version_discovery_marker(store.as_ref(), VersionDiscovery::HeadProbing)
        .await
        .expect("an empty prefix suits head probing");
    assert!(has_marker(&store).await);
    check_version_discovery_marker(store.as_ref(), VersionDiscovery::HeadProbing)
        .await
        .expect("a marked prefix suits head probing");
    let error = check_version_discovery_marker(store.as_ref(), VersionDiscovery::Listing)
        .await
        .expect_err("listing refuses a marked prefix");
    assert!(
        matches!(
            &error,
            VersionDiscoveryCheckError::HeadProbingPrefix { marker_key }
                if marker_key == HEAD_PROBING_MARKER_OBJECT_KEY
        ),
        "{error:?}"
    );
    assert!(
        error.to_string().contains("version-discovery/head"),
        "{error}"
    );
}

#[tokio::test(start_paused = true)]
async fn head_probing_refuses_versions_that_listing_wrote() {
    let store = create_only_store();
    check_version_discovery_marker(store.as_ref(), VersionDiscovery::Listing)
        .await
        .expect("an unmarked prefix suits listing");
    let listing = AliyunOssObjectStore::new(
        Arc::clone(&store) as Arc<dyn ObjectStore>,
        Duration::from_secs(60),
        NodeMetrics::unregistered(),
    );
    listing
        .put(
            MANIFEST_OBJECT_KEY,
            Bytes::from_static(b"manifest"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create");
    let error = check_version_discovery_marker(store.as_ref(), VersionDiscovery::HeadProbing)
        .await
        .expect_err("head probing refuses versions without the marker");
    assert!(
        matches!(
            &error,
            VersionDiscoveryCheckError::VersionsWithoutHeadProbingMarker { version_key, .. }
                if version_key.starts_with("manifest.v/")
        ),
        "{error:?}"
    );
    assert!(!has_marker(&store).await);
    check_version_discovery_marker(store.as_ref(), VersionDiscovery::Listing)
        .await
        .expect("listing keeps its prefix");
}

#[tokio::test(start_paused = true)]
async fn head_probing_accepts_plain_objects_and_leftover_startup_probes() {
    let store = create_only_store();
    store
        .put(
            "log/00000000000000000001",
            Bytes::from_static(b"batch"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("put");
    store
        .put(
            "probe/1-1.v/18446744073709551614",
            Bytes::from_static(b"left by a crashed check"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("put");
    check_version_discovery_marker(store.as_ref(), VersionDiscovery::HeadProbing)
        .await
        .expect("plain objects and probes leave a prefix unversioned");
    assert!(has_marker(&store).await);
    let head_probing = AliyunOssObjectStore::with_version_discovery(
        Arc::clone(&store) as Arc<dyn ObjectStore>,
        Duration::from_secs(60),
        NodeMetrics::unregistered(),
        VersionDiscovery::HeadProbing,
    );
    startup_self_check(&head_probing)
        .await
        .expect("the marker does not disturb versioned keys");
}
