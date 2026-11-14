// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What eventually consistent retention keeps, and when it deletes the rest, on paused time.

use std::collections::BTreeSet;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_object_store::{InMemoryObjectStore, ListingConsistency, ObjectStore, WriteCondition};
use dexzd_protocol::dexzd::v1::{SnapshotFile, SnapshotManifest};
use prost::Message;
use uuid::Uuid;

use super::super::{SnapshotCatalog, SnapshotRetention};
use super::test_fence;
use crate::snapshots::ownership_fence::OwnershipFence;

const DELETION_DELAY: Duration = Duration::from_secs(60 * 60);
const SECOND: Duration = Duration::from_secs(1);
/// A wall-clock reading that every manifest of these tests is far older than.
const FAR_FUTURE_UNIX_MICROS: i64 = i64::MAX / 2;

fn retention(object_store: Arc<InMemoryObjectStore>) -> SnapshotRetention {
    SnapshotRetention {
        object_store,
        node_id: "node-a".to_owned(),
        retained_count: 1,
        deletion_delay: DELETION_DELAY,
        listing_consistency: ListingConsistency::Eventual,
    }
}

fn file_key(lineage: Uuid, name: &str) -> String {
    format!("snapshot/node-a/{lineage}/files/{name}")
}

fn manifest_key(lineage: Uuid, snapshot_number: u64) -> String {
    format!("snapshot/node-a/{lineage}/manifest/{snapshot_number:020}")
}

fn manifest(
    lineage: Uuid,
    snapshot_number: u64,
    created_at_unix_micros: i64,
    file_keys: &[&str],
) -> SnapshotManifest {
    SnapshotManifest {
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
    }
}

async fn upload(object_store: &InMemoryObjectStore, key: &str, body: Bytes) {
    object_store
        .put(key, body, WriteCondition::CreateOnly)
        .await
        .expect("upload object");
}

async fn publish(object_store: &InMemoryObjectStore, manifest: &SnapshotManifest) -> String {
    let lineage = Uuid::from_slice(&manifest.lineage_id).expect("manifest lineage is a UUID");
    let key = manifest_key(lineage, manifest.snapshot_number);
    upload(object_store, &key, Bytes::from(manifest.encode_to_vec())).await;
    key
}

async fn exists(object_store: &InMemoryObjectStore, key: &str) -> bool {
    object_store.get(key).await.expect("read object").is_some()
}

/// Caches the node listing, which later runs then receive whatever is written afterwards.
async fn cache_node_listing(object_store: &InMemoryObjectStore) {
    object_store
        .list("snapshot/node-a/", None, 1000)
        .await
        .expect("cache the node listing");
}

async fn run(
    retention: &SnapshotRetention,
    catalog: &mut SnapshotCatalog,
    pointed_key: &str,
    fence: &OwnershipFence,
) {
    retention
        .run_once(
            catalog,
            &BTreeSet::from([pointed_key.to_owned()]),
            fence,
            FAR_FUTURE_UNIX_MICROS,
        )
        .await
        .expect("retention runs");
}

/// This process uploaded manifests 1 and 2; each lists its own file.
struct TwoOwnManifests {
    object_store: Arc<InMemoryObjectStore>,
    first_key: String,
    first_file: String,
    second_key: String,
    second_file: String,
}

async fn two_own_manifests(catalog: &mut SnapshotCatalog) -> TwoOwnManifests {
    let object_store = Arc::new(InMemoryObjectStore::with_stale_listings());
    let lineage = Uuid::now_v7();
    let first_file = file_key(lineage, "first");
    let second_file = file_key(lineage, "second");
    let mut keys = Vec::new();
    for (snapshot_number, file) in [(1, &first_file), (2, &second_file)] {
        upload(&object_store, file, Bytes::from_static(b"file")).await;
        let manifest = manifest(lineage, snapshot_number, 0, &[file]);
        let key = publish(&object_store, &manifest).await;
        catalog.insert_own(key.clone(), manifest);
        keys.push(key);
    }
    let [first_key, second_key] = <[String; 2]>::try_from(keys).expect("two manifests");
    TwoOwnManifests {
        object_store,
        first_key,
        first_file,
        second_key,
        second_file,
    }
}

#[tokio::test(start_paused = true)]
async fn a_replaced_pointer_keeps_its_files_for_the_delay_after_the_move() {
    let mut catalog = SnapshotCatalog::default();
    let manifests = two_own_manifests(&mut catalog).await;
    let object_store = manifests.object_store.clone();
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store.clone());
    run(&retention, &mut catalog, &manifests.first_key, &fence).await;
    // The owner moves the pointer to manifest 2 while a hot standby still restores manifest 1.
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    tokio::time::advance(DELETION_DELAY - SECOND).await;
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    assert!(exists(&object_store, &manifests.first_key).await);
    assert!(
        exists(&object_store, &manifests.first_file).await,
        "manifest 1 stopped being pointed less than the delay ago"
    );
    tokio::time::advance(2 * SECOND).await;
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    assert!(
        exists(&object_store, &manifests.first_file).await,
        "manifest 1 became a candidate only when its pointer protection ended"
    );
    tokio::time::advance(DELETION_DELAY).await;
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    assert!(!exists(&object_store, &manifests.first_key).await);
    assert!(!exists(&object_store, &manifests.first_file).await);
    assert!(exists(&object_store, &manifests.second_file).await);
}

#[tokio::test(start_paused = true)]
async fn a_replaced_pointer_that_listings_hide_keeps_its_files_for_the_delay() {
    let object_store = Arc::new(InMemoryObjectStore::with_stale_listings());
    let former_lineage = Uuid::now_v7();
    let former_file = file_key(former_lineage, "former");
    upload(&object_store, &former_file, Bytes::from_static(b"former")).await;
    cache_node_listing(&object_store).await;
    let former = manifest(former_lineage, 1, 0, &[&former_file]);
    let former_key = publish(&object_store, &former).await;
    let lineage = Uuid::now_v7();
    let current = manifest(lineage, 1, 0, &[]);
    let current_key = publish(&object_store, &current).await;
    let mut catalog = SnapshotCatalog::default();
    catalog.insert_own(current_key.clone(), current);
    // The first snapshot after a takeover replaced the former owner's pointer.
    catalog.record_pointed_manifests([former_key.clone()]);
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store.clone());
    run(&retention, &mut catalog, &current_key, &fence).await;
    tokio::time::advance(DELETION_DELAY - SECOND).await;
    run(&retention, &mut catalog, &current_key, &fence).await;
    assert!(
        exists(&object_store, &former_file).await,
        "the replaced pointer stays protected while listings hide its manifest"
    );
    tokio::time::advance(2 * SECOND).await;
    run(&retention, &mut catalog, &current_key, &fence).await;
    assert!(
        exists(&object_store, &former_file).await,
        "the file became a candidate only when its pointer protection ended"
    );
    tokio::time::advance(DELETION_DELAY).await;
    run(&retention, &mut catalog, &current_key, &fence).await;
    assert!(!exists(&object_store, &former_file).await);
    assert_eq!(
        object_store.call_count(dexzd_object_store::ObjectStoreOperation::Get, &former_key),
        1,
        "the replaced pointer is read by key once"
    );
}

#[tokio::test(start_paused = true)]
async fn a_failed_manifest_write_that_lands_later_unlisted_keeps_its_files() {
    let mut catalog = SnapshotCatalog::default();
    let manifests = two_own_manifests(&mut catalog).await;
    let object_store = manifests.object_store.clone();
    let lineage = Uuid::from_slice(&catalog.manifests[&manifests.first_key].lineage_id)
        .expect("manifest lineage is a UUID");
    let landing_file = file_key(lineage, "landing");
    upload(&object_store, &landing_file, Bytes::from_static(b"landing")).await;
    cache_node_listing(&object_store).await;
    let landing = manifest(lineage, 3, 0, &[&landing_file]);
    catalog.record_failed_manifest_upload(
        manifest_key(lineage, 3),
        &landing,
        tokio::time::Instant::now(),
    );
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store.clone());
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    tokio::time::advance(DELETION_DELAY - SECOND).await;
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    assert!(exists(&object_store, &landing_file).await);
    // The write lands after its upload reported a failure; cached listings never show it.
    let landing_key = publish(&object_store, &landing).await;
    for _ in 0..3 {
        tokio::time::advance(DELETION_DELAY).await;
        run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    }
    assert!(catalog.manifests.contains_key(&landing_key));
    assert!(
        exists(&object_store, &landing_file).await,
        "the landed manifest is this process's newest snapshot"
    );
    assert!(!exists(&object_store, &manifests.first_file).await);
    assert!(
        exists(&object_store, &manifests.second_file).await,
        "pointed"
    );
}

#[tokio::test(start_paused = true)]
async fn a_failed_manifest_write_that_never_lands_keeps_its_files_for_the_delay() {
    let mut catalog = SnapshotCatalog::default();
    let manifests = two_own_manifests(&mut catalog).await;
    let object_store = manifests.object_store.clone();
    let lineage = Uuid::now_v7();
    let orphan_file = file_key(lineage, "orphan");
    upload(&object_store, &orphan_file, Bytes::from_static(b"orphan")).await;
    catalog.record_failed_manifest_upload(
        manifest_key(lineage, 1),
        &manifest(lineage, 1, 0, &[&orphan_file]),
        tokio::time::Instant::now(),
    );
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store.clone());
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    tokio::time::advance(DELETION_DELAY - SECOND).await;
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    assert!(exists(&object_store, &orphan_file).await);
    tokio::time::advance(2 * SECOND).await;
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    assert!(
        exists(&object_store, &orphan_file).await,
        "the file became a candidate only now"
    );
    tokio::time::advance(DELETION_DELAY).await;
    run(&retention, &mut catalog, &manifests.second_key, &fence).await;
    assert!(!exists(&object_store, &orphan_file).await);
}

/// Two former lineages whose creators' clocks were far ahead and far behind this one's.
struct SkewedLineages {
    object_store: Arc<InMemoryObjectStore>,
    pointed_key: String,
    objects: Vec<String>,
}

async fn skewed_lineages() -> SkewedLineages {
    let object_store = Arc::new(InMemoryObjectStore::with_stale_listings());
    let mut objects = Vec::new();
    for (created_at_unix_micros, is_listed) in [(i64::MAX, true), (i64::MIN, false)] {
        let lineage = Uuid::now_v7();
        let file = file_key(lineage, "file");
        upload(&object_store, &file, Bytes::from_static(b"file")).await;
        if !is_listed {
            cache_node_listing(&object_store).await;
        }
        let former = manifest(lineage, 1, created_at_unix_micros, &[&file]);
        objects.push(publish(&object_store, &former).await);
        objects.push(file);
    }
    let pointed = manifest(Uuid::now_v7(), 1, 0, &[]);
    let pointed_key = publish(&object_store, &pointed).await;
    SkewedLineages {
        object_store,
        pointed_key,
        objects,
    }
}

#[tokio::test(start_paused = true)]
async fn skewed_creator_clocks_delete_former_lineages_only_after_the_delay() {
    let lineages = skewed_lineages().await;
    let object_store = lineages.object_store.clone();
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store.clone());
    let mut catalog = SnapshotCatalog::default();
    run(&retention, &mut catalog, &lineages.pointed_key, &fence).await;
    tokio::time::advance(DELETION_DELAY - SECOND).await;
    run(&retention, &mut catalog, &lineages.pointed_key, &fence).await;
    for key in &lineages.objects {
        assert!(
            exists(&object_store, key).await,
            "{key} is a fresh candidate"
        );
    }
    tokio::time::advance(2 * SECOND).await;
    run(&retention, &mut catalog, &lineages.pointed_key, &fence).await;
    for key in &lineages.objects {
        let is_hidden_manifest = key.contains("/manifest/") && !catalog.manifests.contains_key(key);
        if !is_hidden_manifest {
            assert!(
                !exists(&object_store, key).await,
                "{key} stayed a candidate"
            );
        }
    }
}

#[tokio::test(start_paused = true)]
async fn a_restart_restarts_every_candidate_timer() {
    let lineages = skewed_lineages().await;
    let object_store = lineages.object_store.clone();
    let fence = test_fence(object_store.clone()).await;
    let retention = retention(object_store.clone());
    let mut catalog = SnapshotCatalog::default();
    run(&retention, &mut catalog, &lineages.pointed_key, &fence).await;
    tokio::time::advance(DELETION_DELAY - SECOND).await;
    let mut restarted = SnapshotCatalog::default();
    tokio::time::advance(2 * SECOND).await;
    run(&retention, &mut restarted, &lineages.pointed_key, &fence).await;
    for key in &lineages.objects {
        assert!(
            exists(&object_store, key).await,
            "{key} became a candidate of the restarted process only now"
        );
    }
    tokio::time::advance(DELETION_DELAY).await;
    run(&retention, &mut restarted, &lineages.pointed_key, &fence).await;
    assert!(!exists(&object_store, &lineages.objects[1]).await);
}
