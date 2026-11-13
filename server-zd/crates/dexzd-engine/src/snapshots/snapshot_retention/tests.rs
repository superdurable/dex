// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_clock::SystemWallClock;
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ListingConsistency, ObjectContent, ObjectListing,
    ObjectMetadata, ObjectStore, ObjectStoreOperation, ObjectStoreResult, WriteCondition,
    WriteOutcome,
};
use dexzd_protocol::dexzd::v1::{NodeLease, SnapshotFile, SnapshotManifest};
use prost::Message;
use uuid::Uuid;

use crate::commit::{CommitState, NodeStopReason};
use crate::snapshots::SnapshotError;
use crate::snapshots::ownership_fence::OwnershipFence;

use super::{CANDIDATES_PER_RUN, SnapshotCatalog, SnapshotRetention};

mod forward_skip_tests;
mod stale_listing_tests;

struct MetadataWithoutTimestamp {
    inner: Arc<InMemoryObjectStore>,
}

struct ManifestPublishingStore {
    inner: Arc<InMemoryObjectStore>,
    pending_manifest: Mutex<Option<(String, Bytes)>>,
}

#[async_trait]
impl ObjectStore for ManifestPublishingStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put_file(key, path, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.inner.head(key).await
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.inner.delete(key).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        self.inner.delete_many(keys).await
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        let listing = self.inner.list(prefix, start_after, max_keys).await?;
        if prefix == "snapshot/node-a/" {
            let pending_manifest = self
                .pending_manifest
                .lock()
                .expect("manifest publication lock")
                .take();
            if let Some((key, body)) = pending_manifest {
                let outcome = self
                    .inner
                    .put(&key, body, WriteCondition::CreateOnly)
                    .await?;
                assert!(matches!(outcome, WriteOutcome::Written { .. }));
            }
        }
        Ok(listing)
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        self.inner.supports_replace_if_etag_matches_on_every_key()
    }
}

#[async_trait]
impl ObjectStore for MetadataWithoutTimestamp {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put_file(key, path, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        Ok(self.inner.head(key).await?.map(|mut metadata| {
            metadata.etag = "stable".to_owned();
            metadata.last_modified_unix_micros = None;
            metadata
        }))
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.inner.delete(key).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        self.inner.delete_many(keys).await
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        let mut listing = self.inner.list(prefix, start_after, max_keys).await?;
        for metadata in &mut listing.objects {
            metadata.etag = "stable".to_owned();
            metadata.last_modified_unix_micros = None;
        }
        Ok(listing)
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        self.inner.supports_replace_if_etag_matches_on_every_key()
    }
}

async fn test_fence(object_store: Arc<dyn ObjectStore>) -> OwnershipFence {
    let lease = NodeLease {
        node_id: "node-a".to_owned(),
        epoch: 1,
        expires_at_unix_micros: i64::MAX,
        ..Default::default()
    };
    object_store
        .put(
            "lease/node-a",
            Bytes::from(lease.encode_to_vec()),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("store test lease");
    let state = Arc::new(CommitState::new(
        1,
        dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
    ));
    assert!(state.start_serving());
    OwnershipFence {
        object_store,
        clock: Arc::new(SystemWallClock),
        state,
        node_id: "node-a".to_owned(),
        epoch: 1,
    }
}

#[tokio::test]
async fn missing_head_timestamp_resets_rewritten_key_eligibility() {
    let inner = Arc::new(InMemoryObjectStore::new());
    let object_store = Arc::new(MetadataWithoutTimestamp {
        inner: inner.clone(),
    });
    let orphan_key = "snapshot/node-a/lineage/files/orphan";
    inner
        .put(
            orphan_key,
            Bytes::from_static(b"first"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("upload first version");
    let fence = test_fence(object_store.clone()).await;
    let retention = SnapshotRetention {
        object_store,
        node_id: "node-a".to_owned(),
        retained_count: 3,
        deletion_delay: Duration::from_micros(1),
        listing_consistency: ListingConsistency::Strong,
    };
    let mut catalog = SnapshotCatalog::default();
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX / 2)
        .await
        .expect("mark first version");
    let current_etag = inner
        .head(orphan_key)
        .await
        .expect("head first version")
        .expect("first version exists")
        .etag;
    inner
        .put(
            orphan_key,
            Bytes::from_static(b"second"),
            WriteCondition::ReplaceIfEtagMatches { etag: current_etag },
        )
        .await
        .expect("rewrite key");
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("inspect rewritten version");
    assert!(
        inner
            .get(orphan_key)
            .await
            .expect("read rewritten key")
            .is_some()
    );
    assert_eq!(
        catalog.orphan_first_seen[orphan_key].first_seen_unix_micros,
        i64::MAX
    );
}

#[tokio::test]
async fn orphan_sweep_limits_head_and_delete_work_per_pass() {
    let object_store = Arc::new(InMemoryObjectStore::new());
    for index in 0..(CANDIDATES_PER_RUN + 1) {
        let key = format!("snapshot/node-a/lineage/files/orphan-{index:03}");
        object_store
            .put(&key, Bytes::new(), WriteCondition::CreateOnly)
            .await
            .expect("upload orphan");
    }
    let fence = test_fence(object_store.clone()).await;
    let retention = SnapshotRetention {
        object_store: object_store.clone(),
        node_id: "node-a".to_owned(),
        retained_count: 3,
        deletion_delay: Duration::ZERO,
        listing_consistency: ListingConsistency::Strong,
    };
    let mut catalog = SnapshotCatalog::default();
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("mark orphans");
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("first bounded sweep");
    assert_eq!(
        object_store.call_count(ObjectStoreOperation::Head, "snapshot/node-a/"),
        CANDIDATES_PER_RUN as u64
    );
    assert_eq!(object_store.call_count(ObjectStoreOperation::Delete, ""), 1);
    assert!(
        object_store
            .get("snapshot/node-a/lineage/files/orphan-064")
            .await
            .expect("read remaining orphan")
            .is_some()
    );
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("resume sweep");
    assert_eq!(
        object_store.call_count(ObjectStoreOperation::Head, "snapshot/node-a/"),
        (CANDIDATES_PER_RUN + 1) as u64
    );
    assert!(
        object_store
            .get("snapshot/node-a/lineage/files/orphan-064")
            .await
            .expect("read final orphan")
            .is_none()
    );
}

#[tokio::test]
async fn orphan_requires_a_later_manifest_scan_before_deletion() {
    let object_store = Arc::new(InMemoryObjectStore::new());
    let orphan_key = "snapshot/node-a/lineage/files/orphan";
    object_store
        .put(orphan_key, Bytes::new(), WriteCondition::CreateOnly)
        .await
        .expect("upload orphan");
    let fence = test_fence(object_store.clone()).await;
    let retention = SnapshotRetention {
        object_store: object_store.clone(),
        node_id: "node-a".to_owned(),
        retained_count: 3,
        deletion_delay: Duration::ZERO,
        listing_consistency: ListingConsistency::Strong,
    };
    let mut catalog = SnapshotCatalog::default();
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("mark orphan");
    assert_eq!(object_store.call_count(ObjectStoreOperation::Delete, ""), 0);
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("sweep orphan");
    assert!(
        object_store
            .get(orphan_key)
            .await
            .expect("read orphan")
            .is_none()
    );
}

#[tokio::test]
async fn manifest_published_after_mark_protects_its_file_on_sweep() {
    let object_store = Arc::new(InMemoryObjectStore::new());
    let lineage = Uuid::now_v7();
    let file_key = format!("snapshot/node-a/{lineage}/files/file");
    object_store
        .put(&file_key, Bytes::new(), WriteCondition::CreateOnly)
        .await
        .expect("upload file");
    let fence = test_fence(object_store.clone()).await;
    let retention = SnapshotRetention {
        object_store: object_store.clone(),
        node_id: "node-a".to_owned(),
        retained_count: 3,
        deletion_delay: Duration::ZERO,
        listing_consistency: ListingConsistency::Strong,
    };
    let mut catalog = SnapshotCatalog::default();
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("mark file while manifest is unpublished");
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
        .expect("publish delayed manifest");
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("sweep after a fresh manifest scan");
    assert!(
        object_store
            .get(&file_key)
            .await
            .expect("read file")
            .is_some()
    );
    assert_eq!(
        object_store.call_count(ObjectStoreOperation::Delete, &file_key),
        0
    );
}

#[tokio::test]
async fn manifest_published_after_deleting_pass_list_protects_its_file() {
    let inner = Arc::new(InMemoryObjectStore::new());
    let object_store = Arc::new(ManifestPublishingStore {
        inner: inner.clone(),
        pending_manifest: Mutex::new(None),
    });
    let lineage = Uuid::now_v7();
    let file_key = format!("snapshot/node-a/{lineage}/files/file");
    inner
        .put(&file_key, Bytes::new(), WriteCondition::CreateOnly)
        .await
        .expect("upload file");
    let fence = test_fence(object_store.clone()).await;
    let retention = SnapshotRetention {
        object_store: object_store.clone(),
        node_id: "node-a".to_owned(),
        retained_count: 3,
        deletion_delay: Duration::ZERO,
        listing_consistency: ListingConsistency::Strong,
    };
    let mut catalog = SnapshotCatalog::default();
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("mark file");
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
    *object_store
        .pending_manifest
        .lock()
        .expect("manifest publication lock") =
        Some((manifest_key.clone(), Bytes::from(manifest.encode_to_vec())));
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("sweep after manifest publication during listing");
    assert!(
        inner
            .get(&file_key)
            .await
            .expect("read referenced file")
            .is_some()
    );
    assert!(catalog.manifests.contains_key(&manifest_key));
    assert_eq!(inner.call_count(ObjectStoreOperation::Delete, ""), 0);
}

#[tokio::test]
async fn fenced_retention_does_not_delete_an_orphan_file() {
    let object_store = Arc::new(InMemoryObjectStore::new());
    let orphan_key = "snapshot/node-a/lineage/files/orphan";
    object_store
        .put(orphan_key, Bytes::new(), WriteCondition::CreateOnly)
        .await
        .expect("upload orphan");
    let fence = test_fence(object_store.clone()).await;
    let retention = SnapshotRetention {
        object_store: object_store.clone(),
        node_id: "node-a".to_owned(),
        retained_count: 3,
        deletion_delay: Duration::ZERO,
        listing_consistency: ListingConsistency::Strong,
    };
    let mut catalog = SnapshotCatalog::default();
    retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await
        .expect("mark orphan");
    fence.state.stop(NodeStopReason::Fenced);
    let result = retention
        .run_once(&mut catalog, &BTreeSet::new(), None, &fence, i64::MAX)
        .await;
    assert!(matches!(result, Err(SnapshotError::NodeStopped)));
    assert!(
        object_store
            .get(orphan_key)
            .await
            .expect("read orphan")
            .is_some()
    );
    assert_eq!(object_store.call_count(ObjectStoreOperation::Delete, ""), 0);
}
