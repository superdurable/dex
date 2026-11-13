// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Retention over a listing that repeats an earlier listing, as on Tencent COS.

use std::collections::BTreeSet;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, InMemoryObjectStoreOptions,
    ListingConsistency, ObjectStore, ObjectStoreOperation, WriteCondition,
};
use dexzd_protocol::dexzd::v1::{SnapshotFile, SnapshotManifest};
use prost::Message;
use uuid::Uuid;

use super::super::{SnapshotCatalog, SnapshotRetention};
use super::test_fence;

/// A snapshot file and the manifest that uses it, hidden from listings cached before it.
struct HiddenManifest {
    object_store: Arc<InMemoryObjectStore>,
    file_key: String,
    manifest_key: String,
    manifest: SnapshotManifest,
}

/// Uploads a file, caches listings that show it without a manifest, then publishes the manifest.
async fn hidden_manifest() -> HiddenManifest {
    hidden_manifest_in(Arc::new(InMemoryObjectStore::with_stale_listings())).await
}

/// Builds [`hidden_manifest`] in `object_store`, which must serve stale listings.
async fn hidden_manifest_in(object_store: Arc<InMemoryObjectStore>) -> HiddenManifest {
    let lineage = Uuid::now_v7();
    let file_key = format!("snapshot/node-a/{lineage}/files/file");
    object_store
        .put(&file_key, Bytes::new(), WriteCondition::CreateOnly)
        .await
        .expect("upload file");
    for prefix in [
        "snapshot/node-a/".to_owned(),
        format!("snapshot/node-a/{lineage}/manifest/"),
    ] {
        object_store
            .list(&prefix, None, 1000)
            .await
            .expect("cache a listing without the manifest");
    }
    let manifest = SnapshotManifest {
        node_id: "node-a".to_owned(),
        lineage_id: lineage.as_bytes().to_vec(),
        snapshot_number: 1,
        files: vec![SnapshotFile {
            object_key: file_key.clone(),
            ..Default::default()
        }],
        ..Default::default()
    };
    let manifest_key = format!("snapshot/node-a/{lineage}/manifest/{:020}", 1);
    object_store
        .put(
            &manifest_key,
            Bytes::from(manifest.encode_to_vec()),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("publish manifest");
    HiddenManifest {
        object_store,
        file_key,
        manifest_key,
        manifest,
    }
}

fn retention(object_store: Arc<InMemoryObjectStore>) -> SnapshotRetention {
    SnapshotRetention {
        object_store,
        node_id: "node-a".to_owned(),
        retained_count: 3,
        deletion_delay: Duration::ZERO,
        listing_consistency: ListingConsistency::Eventual,
    }
}

#[tokio::test]
async fn a_manifest_this_process_published_keeps_its_files_while_listings_lack_it() {
    let hidden = hidden_manifest().await;
    let fence = test_fence(hidden.object_store.clone()).await;
    let retention = retention(hidden.object_store.clone());
    let mut catalog = SnapshotCatalog::default();
    catalog.insert(hidden.manifest_key.clone(), hidden.manifest.clone());
    for _ in 0..3 {
        retention
            .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
            .await
            .expect("retention runs");
    }
    assert!(
        hidden
            .object_store
            .get(&hidden.file_key)
            .await
            .expect("read file")
            .is_some(),
        "a stale listing must not delete the file of a published snapshot"
    );
    assert!(catalog.manifests.contains_key(&hidden.manifest_key));
}

#[tokio::test]
async fn a_referenced_manifest_that_listings_lack_keeps_its_files() {
    let hidden = hidden_manifest().await;
    let fence = test_fence(hidden.object_store.clone()).await;
    let retention = retention(hidden.object_store.clone());
    let referenced_keys = BTreeSet::from([hidden.manifest_key.clone()]);
    let mut catalog = SnapshotCatalog::default();
    for _ in 0..3 {
        retention
            .run_once(&mut catalog, &referenced_keys, None, &fence, i64::MAX)
            .await
            .expect("retention runs");
    }
    assert!(
        hidden
            .object_store
            .get(&hidden.file_key)
            .await
            .expect("read file")
            .is_some(),
        "a stale listing must not delete the file of a published snapshot"
    );
    assert_eq!(
        hidden
            .object_store
            .call_count(ObjectStoreOperation::Get, &hidden.manifest_key),
        1,
        "the referenced manifest is read once, then kept by its HEAD"
    );
}

#[tokio::test]
async fn a_manifest_deleted_elsewhere_leaves_the_catalog_once_a_head_misses_it() {
    let hidden = hidden_manifest().await;
    let fence = test_fence(hidden.object_store.clone()).await;
    let retention = retention(hidden.object_store.clone());
    let mut catalog = SnapshotCatalog::default();
    catalog.insert(hidden.manifest_key.clone(), hidden.manifest.clone());
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("retention runs");
    hidden
        .object_store
        .delete(&hidden.manifest_key)
        .await
        .expect("delete manifest");
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("retention runs");
    assert!(!catalog.manifests.contains_key(&hidden.manifest_key));
}

#[tokio::test]
async fn a_strong_listing_reads_no_manifest_that_listings_lack() {
    let hidden = hidden_manifest().await;
    let fence = test_fence(hidden.object_store.clone()).await;
    let retention = SnapshotRetention {
        listing_consistency: ListingConsistency::Strong,
        ..retention(hidden.object_store.clone())
    };
    let referenced_keys = BTreeSet::from([hidden.manifest_key.clone()]);
    let mut catalog = SnapshotCatalog::default();
    catalog.insert(hidden.manifest_key.clone(), hidden.manifest.clone());
    for _ in 0..3 {
        retention
            .run_once(&mut catalog, &referenced_keys, None, &fence, i64::MAX)
            .await
            .expect("retention runs");
    }
    for operation in [ObjectStoreOperation::Head, ObjectStoreOperation::Get] {
        assert_eq!(
            hidden
                .object_store
                .call_count(operation, &hidden.manifest_key),
            0,
            "{operation}"
        );
    }
    assert!(
        !catalog.manifests.contains_key(&hidden.manifest_key),
        "a strong listing that lacks a manifest still drops it"
    );
}

/// Fails listings of every manifest prefix as Transient once armed, as a replayed COS page does.
#[derive(Default)]
struct ManifestListingFailure {
    is_armed: std::sync::atomic::AtomicBool,
}

impl FaultInjector for ManifestListingFailure {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        let is_manifest_listing =
            operation == ObjectStoreOperation::List && key.ends_with("/manifest/");
        if is_manifest_listing && self.is_armed.load(std::sync::atomic::Ordering::SeqCst) {
            FaultAction::FailBeforeEffect
        } else {
            FaultAction::Proceed
        }
    }
}

#[tokio::test]
async fn a_lineage_whose_manifest_listing_fails_keeps_every_file() {
    let failure = Arc::new(ManifestListingFailure::default());
    let hidden = hidden_manifest_in(Arc::new(
        InMemoryObjectStore::with_options_and_stale_listings(
            InMemoryObjectStoreOptions {
                injector: Some(Arc::clone(&failure) as _),
                ..InMemoryObjectStoreOptions::default()
            },
            Duration::MAX,
        ),
    ))
    .await;
    failure
        .is_armed
        .store(true, std::sync::atomic::Ordering::SeqCst);
    let fence = test_fence(hidden.object_store.clone()).await;
    let retention = retention(hidden.object_store.clone());
    let mut catalog = SnapshotCatalog::default();
    for _ in 0..3 {
        retention
            .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
            .await
            .expect("an eventual retention decides without listing a lineage's manifests");
    }
    assert!(
        hidden
            .object_store
            .get(&hidden.file_key)
            .await
            .expect("read file")
            .is_some(),
        "a failed manifest listing must not delete a file that a later manifest may use"
    );
    assert_eq!(
        hidden
            .object_store
            .call_count(ObjectStoreOperation::Delete, &hidden.file_key),
        0
    );
}
