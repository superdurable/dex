// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::{AtomicBool, Ordering};

use dexzd_object_store::{FaultAction, FaultInjector, InMemoryObjectStore, ObjectStoreOperation};
use dexzd_storage::{RocksDbLocalStore, RocksDbStoreOptions};
use tempfile::TempDir;

use super::*;

fn local_store(directory: &TempDir) -> Arc<dyn LocalStore> {
    Arc::new(RocksDbLocalStore::open(directory.path(), RocksDbStoreOptions::default()).unwrap())
}

fn repair(run_number: u8, priority: RepairPriority) -> ProjectionRepair {
    ProjectionRepair::new(
        TenantId(2),
        RunId([run_number; 16]),
        FlowHash(3),
        PartitionId(4),
        priority,
    )
}

#[tokio::test]
async fn pending_runs_survive_local_restart_and_new_owner_takeover() {
    let directory = TempDir::new().unwrap();
    let objects = Arc::new(InMemoryObjectStore::new());
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [1; 16], 1).unwrap();
    pending
        .record(&[
            repair(1, RepairPriority::Intermediate),
            repair(2, RepairPriority::Close),
            repair(3, RepairPriority::Start),
            repair(1, RepairPriority::Start),
        ])
        .unwrap();
    assert_eq!(pending.count(), 3);
    drop(pending);
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [1; 16], 1).unwrap();
    let batch = pending.take_batch(10).unwrap();
    assert_eq!(
        batch.iter().map(|repair| repair.run_id).collect::<Vec<_>>(),
        vec![RunId([1; 16]), RunId([3; 16]), RunId([2; 16])]
    );
    assert!(pending.has_unsynced(PartitionId(4)));
    assert_eq!(pending.sync_objects(1).await.unwrap(), 1);
    assert!(!pending.has_unsynced(PartitionId(4)));
    let next_directory = TempDir::new().unwrap();
    let mut successor =
        PendingRunStore::new(local_store(&next_directory), objects.clone(), [1; 16], 2).unwrap();
    successor.load_objects().await.unwrap();
    assert_eq!(successor.count(), 3);
    successor.sync_objects(1).await.unwrap();
    pending.record(&[repair(5, RepairPriority::Start)]).unwrap();
    assert!(matches!(
        pending.sync_objects(1).await,
        Err(PendingStoreError::Fenced { epoch: 2, .. })
    ));
    let repaired = successor.take_batch(10).unwrap();
    successor
        .record(&[repair(1, RepairPriority::Close)])
        .unwrap();
    successor.finish(&repaired).unwrap();
    assert_eq!(
        successor.count(),
        1,
        "a drop during an SQL repair remains pending"
    );
    successor
        .finish(&successor.take_batch(10).unwrap())
        .unwrap();
    assert_eq!(successor.count(), 0);
    assert_eq!(successor.sync_objects(1).await.unwrap(), 1);
    assert!(
        objects
            .get("search-pending/v1/partition-4")
            .await
            .unwrap()
            .is_none()
    );
}

struct PendingObjectOutage {
    is_unavailable: AtomicBool,
}

impl FaultInjector for PendingObjectOutage {
    fn action(&self, operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        if operation == ObjectStoreOperation::Put && self.is_unavailable.load(Ordering::Relaxed) {
            FaultAction::FailBeforeEffect
        } else {
            FaultAction::Proceed
        }
    }
}

#[tokio::test]
async fn object_outage_preserves_local_pending_and_keeps_checkpoint_guard_closed() {
    let directory = TempDir::new().unwrap();
    let outage = Arc::new(PendingObjectOutage {
        is_unavailable: AtomicBool::new(true),
    });
    let objects = Arc::new(InMemoryObjectStore::with_injector(outage.clone()));
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [1; 16], 1).unwrap();
    pending.record(&[repair(1, RepairPriority::Close)]).unwrap();
    assert!(pending.sync_objects(1).await.is_err());
    assert_eq!(pending.count(), 1);
    assert!(pending.has_unsynced(PartitionId(4)));
    outage.is_unavailable.store(false, Ordering::Relaxed);
    assert_eq!(pending.sync_objects(1).await.unwrap(), 1);
    assert!(!pending.has_unsynced(PartitionId(4)));
    let fresh_directory = TempDir::new().unwrap();
    let mut successor =
        PendingRunStore::new(local_store(&fresh_directory), objects, [1; 16], 2).unwrap();
    successor.load_objects().await.unwrap();
    assert_eq!(successor.take_batch(10).unwrap()[0].run_id, RunId([1; 16]));
}

#[tokio::test]
async fn unreadable_object_quarantines_only_its_partition_until_manual_rebuilding() {
    let directory = TempDir::new().unwrap();
    let objects = Arc::new(InMemoryObjectStore::new());
    objects
        .put(
            "search-pending/v1/partition-4",
            vec![255].into(),
            dexzd_object_store::WriteCondition::CreateOnly,
        )
        .await
        .unwrap();
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [1; 16], 1).unwrap();
    let mut valid = repair(2, RepairPriority::Start);
    valid.partition = PartitionId(5);
    pending.record(&[valid]).unwrap();
    pending.sync_objects(1).await.unwrap();
    let successor_directory = TempDir::new().unwrap();
    let mut successor = PendingRunStore::new(
        local_store(&successor_directory),
        objects.clone(),
        [1; 16],
        2,
    )
    .unwrap();
    successor.load_objects().await.unwrap();
    assert_eq!(successor.count(), 1);
    assert!(successor.has_unsynced(PartitionId(4)));
    let markers = successor.load_rebuild_markers().unwrap();
    assert!(markers[&PartitionId(4)].contains(&dexzd_metrics::ProjectionRebuildReason::Unreadable));
    assert_eq!(successor.sync_objects(10).await.unwrap(), 0);
    assert!(
        objects
            .get("search-pending/v1/partition-4")
            .await
            .unwrap()
            .is_some()
    );
    assert!(!successor.has_unsynced(PartitionId(5)));
    successor.finish_manual_rebuild(PartitionId(4));
    successor.store_rebuild_markers(&BTreeMap::new()).unwrap();
    assert_eq!(successor.sync_objects(10).await.unwrap(), 1);
    assert!(
        objects
            .get("search-pending/v1/partition-4")
            .await
            .unwrap()
            .is_none()
    );
    assert!(successor.load_rebuild_markers().unwrap().is_empty());
}

#[test]
fn a_quarantined_start_does_not_starve_other_partitions_pending_repairs() {
    let directory = TempDir::new().unwrap();
    let mut pending = PendingRunStore::new(
        local_store(&directory),
        Arc::new(InMemoryObjectStore::new()),
        [1; 16],
        1,
    )
    .unwrap();
    let mut close = repair(2, RepairPriority::Close);
    close.partition = PartitionId(5);
    pending
        .record(&[repair(1, RepairPriority::Start), close])
        .unwrap();
    let repairs = pending
        .take_batch_excluding(10, &BTreeSet::from([PartitionId(4)]))
        .unwrap();
    assert_eq!(repairs.len(), 1);
    assert_eq!(repairs[0].run_id, RunId([2; 16]));
    assert!(
        pending
            .take_batch_excluding(10, &BTreeSet::from([PartitionId(4), PartitionId(5)]))
            .unwrap()
            .is_empty()
    );
}

#[tokio::test]
async fn hot_partitions_cannot_starve_other_pending_object_syncs() {
    let directory = TempDir::new().unwrap();
    let objects = Arc::new(InMemoryObjectStore::new());
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [1; 16], 1).unwrap();
    let mut later_partition = repair(2, RepairPriority::Start);
    later_partition.partition = PartitionId(5);
    pending
        .record(&[repair(1, RepairPriority::Start), later_partition])
        .unwrap();
    pending.sync_objects(1).await.unwrap();
    assert!(
        objects
            .get("search-pending/v1/partition-4")
            .await
            .unwrap()
            .is_some()
    );
    pending.record(&[repair(1, RepairPriority::Close)]).unwrap();
    pending.sync_objects(1).await.unwrap();
    assert!(
        objects
            .get("search-pending/v1/partition-5")
            .await
            .unwrap()
            .is_some()
    );
    assert!(pending.has_unsynced(PartitionId(4)));
    assert!(!pending.has_unsynced(PartitionId(5)));
}

#[tokio::test]
async fn a_record_during_unlocked_sync_keeps_the_new_revision_dirty() {
    let directory = TempDir::new().unwrap();
    let objects = Arc::new(InMemoryObjectStore::new());
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [1; 16], 1).unwrap();
    pending.record(&[repair(1, RepairPriority::Start)]).unwrap();
    let snapshot = pending.snapshot_sync(1).unwrap().pop().unwrap();
    pending.record(&[repair(2, RepairPriority::Close)]).unwrap();
    pending.reconcile_sync(snapshot.execute().await).unwrap();
    assert!(pending.has_unsynced(PartitionId(4)));
    pending.sync_objects(1).await.unwrap();
    assert!(!pending.has_unsynced(PartitionId(4)));
    let next_directory = TempDir::new().unwrap();
    let mut successor =
        PendingRunStore::new(local_store(&next_directory), objects, [1; 16], 2).unwrap();
    successor.load_objects().await.unwrap();
    assert_eq!(successor.count(), 2);
}

#[tokio::test]
async fn rebuilt_markers_do_not_return_on_failback_and_unknown_keys_are_ignored() {
    let first_directory = TempDir::new().unwrap();
    let next_directory = TempDir::new().unwrap();
    let objects = Arc::new(InMemoryObjectStore::new());
    let mut first =
        PendingRunStore::new(local_store(&first_directory), objects.clone(), [1; 16], 1).unwrap();
    let markers = BTreeMap::from([(
        PartitionId(4),
        BTreeSet::from([dexzd_metrics::ProjectionRebuildReason::Divergence]),
    )]);
    first.store_rebuild_markers(&markers).unwrap();
    first.sync_objects(1).await.unwrap();
    let mut successor =
        PendingRunStore::new(local_store(&next_directory), objects.clone(), [1; 16], 2).unwrap();
    successor.load_objects().await.unwrap();
    assert_eq!(successor.load_rebuild_markers().unwrap(), markers);
    successor.store_rebuild_markers(&BTreeMap::new()).unwrap();
    successor.sync_objects(1).await.unwrap();
    objects
        .put(
            "search-pending/v2/partition-4",
            vec![255].into(),
            dexzd_object_store::WriteCondition::CreateOnly,
        )
        .await
        .unwrap();
    objects
        .put(
            "search-pending/v1/unrelated",
            vec![255].into(),
            dexzd_object_store::WriteCondition::CreateOnly,
        )
        .await
        .unwrap();
    drop(first);
    let mut returned =
        PendingRunStore::new(local_store(&first_directory), objects, [1; 16], 3).unwrap();
    returned.load_objects().await.unwrap();
    assert!(returned.load_rebuild_markers().unwrap().is_empty());
    assert_eq!(returned.unknown_objects, 2);
}

#[tokio::test]
async fn paged_pending_scans_preserve_maximum_partition_and_tenant_keys() {
    let directory = TempDir::new().unwrap();
    let objects = Arc::new(InMemoryObjectStore::new());
    let repairs: Vec<_> = (0..1025_u128)
        .map(|run_number| {
            ProjectionRepair::new(
                TenantId(u64::MAX),
                RunId(run_number.to_be_bytes()),
                FlowHash(3),
                PartitionId(u16::MAX),
                RepairPriority::Start,
            )
        })
        .collect();
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [255; 16], 1).unwrap();
    pending.record(&repairs).unwrap();
    drop(pending);
    let mut pending =
        PendingRunStore::new(local_store(&directory), objects.clone(), [255; 16], 1).unwrap();
    assert_eq!(pending.count(), 1025);
    pending.sync_objects(1).await.unwrap();
    let successor_directory = TempDir::new().unwrap();
    let mut successor =
        PendingRunStore::new(local_store(&successor_directory), objects, [255; 16], 2).unwrap();
    successor.load_objects().await.unwrap();
    let restored = successor.take_batch(2048).unwrap();
    assert_eq!(restored.len(), 1025);
    assert!(
        restored
            .iter()
            .all(|repair| repair.partition == PartitionId(u16::MAX)
                && repair.tenant_id == TenantId(u64::MAX))
    );
    assert_eq!(
        restored.last().unwrap().run_id,
        RunId(1024_u128.to_be_bytes())
    );
}
