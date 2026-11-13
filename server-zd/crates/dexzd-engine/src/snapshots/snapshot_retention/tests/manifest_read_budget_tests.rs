// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! How eventually consistent retention spreads manifest reads over runs, on paused time.

use std::collections::BTreeSet;
use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ListingConsistency, ObjectContent, ObjectListing,
    ObjectMetadata, ObjectStore, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation,
    ObjectStoreResult, WriteCondition, WriteOutcome,
};
use dexzd_protocol::dexzd::v1::{SnapshotFile, SnapshotManifest};
use prost::Message;
use uuid::Uuid;

use super::super::{
    CANDIDATES_PER_RUN, EVENTUAL_MANIFEST_READS_PER_RUN, SnapshotCatalog, SnapshotRetention,
};
use super::test_fence;
use crate::snapshots::ownership_fence::OwnershipFence;

const DELETION_DELAY: Duration = Duration::from_secs(60 * 60);
/// A wall-clock reading that every manifest of these tests is far older than.
const FAR_FUTURE_UNIX_MICROS: i64 = i64::MAX / 2;
/// Unprotected manifests a former process left; reading them all takes three runs.
const FORMER_MANIFEST_COUNT: u64 = 2 * EVENTUAL_MANIFEST_READS_PER_RUN as u64 + 88;

/// Counts the GETs and HEADs that reach the inner store, and fails one chosen GET once.
struct ReadCountingStore {
    inner: Arc<InMemoryObjectStore>,
    reads: AtomicUsize,
    got_keys: Mutex<Vec<String>>,
    failing_get_key: Mutex<Option<String>>,
}

impl ReadCountingStore {
    fn new(inner: Arc<InMemoryObjectStore>) -> Self {
        Self {
            inner,
            reads: AtomicUsize::new(0),
            got_keys: Mutex::new(Vec::new()),
            failing_get_key: Mutex::new(None),
        }
    }

    fn take_read_count(&self) -> usize {
        self.reads.swap(0, Ordering::SeqCst)
    }

    fn take_got_keys(&self) -> Vec<String> {
        std::mem::take(&mut *self.got_keys.lock().expect("got keys lock"))
    }
}

#[async_trait]
impl ObjectStore for ReadCountingStore {
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
        self.reads.fetch_add(1, Ordering::SeqCst);
        self.got_keys
            .lock()
            .expect("got keys lock")
            .push(key.to_owned());
        let should_fail = {
            let mut failing_get_key = self.failing_get_key.lock().expect("failing key lock");
            let should_fail = failing_get_key.as_deref() == Some(key);
            if should_fail {
                *failing_get_key = None;
            }
            should_fail
        };
        if should_fail {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Get,
                key,
                ObjectStoreErrorKind::Transient,
                "injected read failure",
            ));
        }
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.reads.fetch_add(1, Ordering::SeqCst);
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.reads.fetch_add(1, Ordering::SeqCst);
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.reads.fetch_add(1, Ordering::SeqCst);
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
        self.inner.list(prefix, start_after, max_keys).await
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        self.inner.supports_replace_if_etag_matches_on_every_key()
    }
}

fn file_key(lineage: Uuid, name: &str) -> String {
    format!("snapshot/node-a/{lineage}/files/{name}")
}

/// Uploads a manifest of `lineage` that lists one file of its own and returns both keys.
async fn publish_with_file(
    object_store: &InMemoryObjectStore,
    lineage: Uuid,
    snapshot_number: u64,
) -> (String, String) {
    let file = file_key(lineage, &format!("file-{snapshot_number:020}"));
    object_store
        .put(
            &file,
            Bytes::from_static(b"file"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("upload file");
    let manifest = SnapshotManifest {
        node_id: "node-a".to_owned(),
        lineage_id: lineage.as_bytes().to_vec(),
        snapshot_number,
        files: vec![SnapshotFile {
            object_key: file.clone(),
            ..Default::default()
        }],
        ..Default::default()
    };
    let key = format!("snapshot/node-a/{lineage}/manifest/{snapshot_number:020}");
    object_store
        .put(
            &key,
            Bytes::from(manifest.encode_to_vec()),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("upload manifest");
    (key, file)
}

async fn exists(object_store: &InMemoryObjectStore, key: &str) -> bool {
    object_store.get(key).await.expect("read object").is_some()
}

/// Runs retention with `pointed_key` pointed and returns the GETs and HEADs it sent.
async fn run_counting_reads(
    retention: &SnapshotRetention,
    counting_store: &ReadCountingStore,
    catalog: &mut SnapshotCatalog,
    pointed_key: &str,
    fence: &OwnershipFence,
) -> usize {
    counting_store.take_read_count();
    retention
        .run_once(
            catalog,
            &BTreeSet::from([pointed_key.to_owned()]),
            fence,
            FAR_FUTURE_UNIX_MICROS,
        )
        .await
        .expect("retention runs");
    counting_store.take_read_count()
}

#[tokio::test(start_paused = true)]
async fn eventual_retention_bounds_manifest_reads_and_deletes_only_after_reading_them_all() {
    let inner = Arc::new(InMemoryObjectStore::new());
    let pointed_lineage = Uuid::now_v7();
    let (pointed_key, pointed_file) = publish_with_file(&inner, pointed_lineage, 1).await;
    let former_lineage = Uuid::now_v7();
    let mut former_objects = Vec::new();
    for snapshot_number in 1..=FORMER_MANIFEST_COUNT {
        former_objects.push(publish_with_file(&inner, former_lineage, snapshot_number).await);
    }
    let (newest_former_key, newest_former_file) =
        former_objects.last().cloned().expect("former manifests");
    let counting_store = Arc::new(ReadCountingStore::new(inner.clone()));
    let fence = test_fence(inner.clone()).await;
    let retention = SnapshotRetention {
        object_store: counting_store.clone(),
        node_id: "node-a".to_owned(),
        retained_count: 1,
        deletion_delay: DELETION_DELAY,
        listing_consistency: ListingConsistency::Eventual,
    };
    let mut catalog = SnapshotCatalog::default();

    // The pointed manifest and the former ones need 601 reads, so the third run reads the last.
    for run_number in 1..=2 {
        let reads = run_counting_reads(
            &retention,
            &counting_store,
            &mut catalog,
            &pointed_key,
            &fence,
        )
        .await;
        assert_eq!(
            reads, EVENTUAL_MANIFEST_READS_PER_RUN,
            "run {run_number} reads one budget of manifests"
        );
        tokio::time::advance(2 * DELETION_DELAY).await;
    }
    assert!(
        exists(&inner, &newest_former_file).await,
        "the unread manifest that lists it was listed twice the delay ago"
    );
    let reads = run_counting_reads(
        &retention,
        &counting_store,
        &mut catalog,
        &pointed_key,
        &fence,
    )
    .await;
    assert_eq!(
        reads,
        FORMER_MANIFEST_COUNT as usize + 1 - 2 * EVENTUAL_MANIFEST_READS_PER_RUN,
        "the third run reads the rest and checks no file candidate yet"
    );
    for (key, file) in &former_objects {
        assert!(
            exists(&inner, key).await,
            "{key} became a candidate just now"
        );
        assert!(
            exists(&inner, file).await,
            "{file} became a candidate just now"
        );
    }

    tokio::time::advance(DELETION_DELAY).await;
    let reads = run_counting_reads(
        &retention,
        &counting_store,
        &mut catalog,
        &pointed_key,
        &fence,
    )
    .await;
    assert!(
        reads <= CANDIDATES_PER_RUN,
        "the run after reading everything starts a listing and HEADs file candidates: {reads}"
    );
    let (oldest_former_key, oldest_former_file) = &former_objects[0];
    assert!(!exists(&inner, oldest_former_key).await);
    assert!(!exists(&inner, oldest_former_file).await);
    assert!(
        exists(&inner, &newest_former_file).await,
        "its manifest is beyond this run's candidates"
    );
    assert!(exists(&inner, &newest_former_key).await);
    assert!(exists(&inner, &pointed_key).await);
    assert!(exists(&inner, &pointed_file).await);
}

#[tokio::test(start_paused = true)]
async fn a_failed_queued_manifest_read_is_retried_before_any_deletion() {
    let inner = Arc::new(InMemoryObjectStore::new());
    let lineage = Uuid::now_v7();
    let (pointed_key, _) = publish_with_file(&inner, lineage, 4).await;
    let mut former_objects = Vec::new();
    for snapshot_number in 1..=3 {
        former_objects.push(publish_with_file(&inner, lineage, snapshot_number).await);
    }
    let (failing_key, failing_file) = former_objects[1].clone();
    let counting_store = Arc::new(ReadCountingStore::new(inner.clone()));
    let fence = test_fence(inner.clone()).await;
    let retention = SnapshotRetention {
        object_store: counting_store.clone(),
        node_id: "node-a".to_owned(),
        retained_count: 1,
        deletion_delay: DELETION_DELAY,
        listing_consistency: ListingConsistency::Eventual,
    };
    let mut catalog = SnapshotCatalog::default();
    *counting_store
        .failing_get_key
        .lock()
        .expect("failing key lock") = Some(failing_key.clone());
    retention
        .run_once(
            &mut catalog,
            &BTreeSet::from([pointed_key.clone()]),
            &fence,
            FAR_FUTURE_UNIX_MICROS,
        )
        .await
        .expect_err("the injected manifest read fails");
    counting_store.take_got_keys();

    run_counting_reads(
        &retention,
        &counting_store,
        &mut catalog,
        &pointed_key,
        &fence,
    )
    .await;
    assert!(
        counting_store.take_got_keys().contains(&failing_key),
        "the next run reads the manifest whose read failed"
    );
    tokio::time::advance(DELETION_DELAY).await;
    run_counting_reads(
        &retention,
        &counting_store,
        &mut catalog,
        &pointed_key,
        &fence,
    )
    .await;
    assert!(
        exists(&inner, &failing_file).await || !exists(&inner, &failing_key).await,
        "a file goes no earlier than the manifest that lists it"
    );
}
