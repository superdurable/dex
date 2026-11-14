// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Retention over listings that replay a page produced for a later cursor, as on Tencent COS.

use std::collections::BTreeSet;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ListingConsistency, ObjectContent, ObjectListing,
    ObjectMetadata, ObjectStore, ObjectStoreResult, WriteCondition, WriteOutcome,
};
use dexzd_protocol::dexzd::v1::{SnapshotFile, SnapshotManifest};
use prost::Message;
use uuid::Uuid;

use super::super::{
    EVENTUAL_LISTING_DURATION_PER_RUN, LISTING_PAGE_SIZE, LISTING_PAGES_PER_RUN, SnapshotCatalog,
    SnapshotRetention,
};
use super::test_fence;

const NODE_PREFIX: &str = "snapshot/node-a/";

fn retention(object_store: Arc<dyn ObjectStore>, retained_count: usize) -> SnapshotRetention {
    SnapshotRetention {
        object_store,
        node_id: "node-a".to_owned(),
        retained_count,
        deletion_delay: Duration::ZERO,
        listing_consistency: ListingConsistency::Eventual,
    }
}

fn file_key(lineage: Uuid, name: &str) -> String {
    format!("snapshot/node-a/{lineage}/files/{name}")
}

fn manifest_key(lineage: Uuid, snapshot_number: u64) -> String {
    format!("snapshot/node-a/{lineage}/manifest/{snapshot_number:020}")
}

async fn upload_files(object_store: &InMemoryObjectStore, keys: &[&str]) {
    for key in keys {
        object_store
            .put(key, Bytes::from_static(b"file"), WriteCondition::CreateOnly)
            .await
            .expect("upload snapshot file");
    }
}

/// Publishes manifest `snapshot_number` of `lineage`, created at `created_at_unix_micros`.
async fn publish_manifest(
    object_store: &InMemoryObjectStore,
    lineage: Uuid,
    snapshot_number: u64,
    created_at_unix_micros: i64,
    file_keys: &[&str],
) -> (String, SnapshotManifest) {
    let manifest = SnapshotManifest {
        node_id: "node-a".to_owned(),
        lineage_id: lineage.as_bytes().to_vec(),
        snapshot_number,
        created_at_unix_micros,
        files: file_keys
            .iter()
            .map(|key| SnapshotFile {
                object_key: (*key).to_owned(),
                ..Default::default()
            })
            .collect(),
        ..Default::default()
    };
    let key = manifest_key(lineage, snapshot_number);
    object_store
        .put(
            &key,
            Bytes::from(manifest.encode_to_vec()),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("publish manifest");
    (key, manifest)
}

/// Caches the listing of `prefix` after `start_after`, which later listings then receive.
async fn cache_listing(object_store: &InMemoryObjectStore, prefix: &str, start_after: &str) {
    object_store
        .list(prefix, Some(start_after), 1000)
        .await
        .expect("cache a listing for a later cursor");
}

async fn exists(object_store: &InMemoryObjectStore, key: &str) -> bool {
    object_store.get(key).await.expect("read object").is_some()
}

/// Listings never show lineage L's manifest 5. Obsolete manifest 1 shares file `only-1-and-5` with 5.
struct SkippedNewestManifest {
    object_store: Arc<InMemoryObjectStore>,
    lineage: Uuid,
    newest_key: String,
    file_of_newest: String,
    shared_file: String,
}

async fn skipped_newest_manifest() -> SkippedNewestManifest {
    let object_store = Arc::new(InMemoryObjectStore::with_stale_listings());
    let lineage = Uuid::now_v7();
    let file_of_newest = file_key(lineage, "only-1-and-5");
    let shared_file = file_key(lineage, "shared-1-to-4");
    upload_files(&object_store, &[&file_of_newest, &shared_file]).await;
    publish_manifest(
        &object_store,
        lineage,
        1,
        1,
        &[&file_of_newest, &shared_file],
    )
    .await;
    for snapshot_number in 2..=4 {
        publish_manifest(
            &object_store,
            lineage,
            snapshot_number,
            i64::try_from(snapshot_number).expect("small number"),
            &[&shared_file],
        )
        .await;
    }
    object_store
        .list(NODE_PREFIX, None, 1000)
        .await
        .expect("cache a node listing without manifest 5");
    let (newest_key, _) = publish_manifest(&object_store, lineage, 5, 5, &[&file_of_newest]).await;
    // The manifest listing of L receives a page produced after manifest 5: it lists nothing.
    cache_listing(
        &object_store,
        &format!("snapshot/node-a/{lineage}/manifest/"),
        &newest_key,
    )
    .await;
    SkippedNewestManifest {
        object_store,
        lineage,
        newest_key,
        file_of_newest,
        shared_file,
    }
}

#[tokio::test]
async fn a_forward_skipped_listing_keeps_the_files_of_an_unlisted_pointed_manifest() {
    let skipped = skipped_newest_manifest().await;
    let fence = test_fence(skipped.object_store.clone()).await;
    let retention = retention(skipped.object_store.clone(), 3);
    let referenced_keys = BTreeSet::from([skipped.newest_key.clone()]);
    let mut catalog = SnapshotCatalog::default();
    for _ in 0..3 {
        retention
            .run_once(&mut catalog, &referenced_keys, &fence, i64::MAX)
            .await
            .expect("retention runs");
    }
    for snapshot_number in 1..=4 {
        assert!(
            !exists(
                &skipped.object_store,
                &manifest_key(skipped.lineage, snapshot_number)
            )
            .await,
            "manifest {snapshot_number} is neither pointed nor this process's"
        );
    }
    assert!(exists(&skipped.object_store, &skipped.newest_key).await);
    assert!(
        exists(&skipped.object_store, &skipped.file_of_newest).await,
        "the pointed manifest, which no listing shows, still uses this file"
    );
    assert!(
        !exists(&skipped.object_store, &skipped.shared_file).await,
        "only unprotected manifests use this file"
    );
}

#[tokio::test]
async fn an_unrecorded_manifest_of_this_process_keeps_its_files() {
    let object_store = Arc::new(InMemoryObjectStore::with_stale_listings());
    let lineage = Uuid::now_v7();
    let file_of_unrecorded = file_key(lineage, "only-1-and-3");
    let file_of_first = file_key(lineage, "only-1");
    upload_files(&object_store, &[&file_of_unrecorded, &file_of_first]).await;
    let mut catalog = SnapshotCatalog::default();
    for (snapshot_number, files) in [
        (1, vec![file_of_unrecorded.as_str(), file_of_first.as_str()]),
        (2, vec![]),
    ] {
        let (key, manifest) =
            publish_manifest(&object_store, lineage, snapshot_number, 1, &files).await;
        catalog.insert_own(key, manifest);
    }
    object_store
        .list(NODE_PREFIX, None, 1000)
        .await
        .expect("cache a node listing without manifest 3");
    // Manifest 3's upload reported an unknown outcome, so this process never recorded it.
    let (unrecorded_key, unrecorded) =
        publish_manifest(&object_store, lineage, 3, 3, &[&file_of_unrecorded]).await;
    catalog.record_failed_manifest_upload(
        unrecorded_key.clone(),
        &unrecorded,
        tokio::time::Instant::now(),
    );
    cache_listing(
        &object_store,
        &format!("snapshot/node-a/{lineage}/manifest/"),
        &unrecorded_key,
    )
    .await;
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store.clone(), 1);
    for _ in 0..3 {
        retention
            .run_once(&mut catalog, &BTreeSet::new(), &fence, i64::MAX)
            .await
            .expect("retention runs");
    }
    assert!(catalog.manifests.contains_key(&unrecorded_key));
    assert!(
        exists(&object_store, &file_of_unrecorded).await,
        "manifest 3 is the newest snapshot and uses this file"
    );
    assert!(!exists(&object_store, &file_of_first).await);
    assert_eq!(
        object_store.call_count(
            dexzd_object_store::ObjectStoreOperation::Get,
            &unrecorded_key
        ),
        1,
        "a found manifest write is read once"
    );
}

/// Takes `listing_delay` for every listing, as a page that waits out replays does.
struct SlowListingStore {
    inner: Arc<InMemoryObjectStore>,
    listing_delay: Duration,
}

#[async_trait]
impl ObjectStore for SlowListingStore {
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
        tokio::time::sleep(self.listing_delay).await;
        self.inner.list(prefix, start_after, max_keys).await
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        self.inner.supports_replace_if_etag_matches_on_every_key()
    }
}

#[tokio::test(start_paused = true)]
async fn an_eventual_refresh_starts_no_page_after_its_listing_duration() {
    let inner = Arc::new(InMemoryObjectStore::new());
    let lineage = Uuid::now_v7();
    let page_count = LISTING_PAGES_PER_RUN + 1;
    for index in 0..(page_count * LISTING_PAGE_SIZE as usize) {
        inner
            .put(
                &file_key(lineage, &format!("{index:06}")),
                Bytes::from_static(b"file"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("upload snapshot file");
    }
    let listing_delay = Duration::from_secs(4);
    let object_store = Arc::new(SlowListingStore {
        inner: inner.clone(),
        listing_delay,
    });
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store, 3);
    let mut catalog = SnapshotCatalog::default();
    let mut runs = 0;
    loop {
        let started = tokio::time::Instant::now();
        retention
            .run_once(&mut catalog, &BTreeSet::new(), &fence, i64::MAX)
            .await
            .expect("retention runs");
        let elapsed = started.elapsed();
        assert!(
            elapsed <= EVENTUAL_LISTING_DURATION_PER_RUN + listing_delay,
            "one run listed for {elapsed:?}"
        );
        runs += 1;
        if catalog.completed_scan_number > 0 {
            break;
        }
        assert!(runs < page_count, "the listing resumes where it stopped");
    }
    assert!(runs > 1, "the listing took more than one run");
}
