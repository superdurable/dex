// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Snapshot retention of a node whose object storage lists eventually, as Tencent COS does.

use std::collections::BTreeSet;
use std::time::Duration;

use dexzd_engine::{BlockingWorkPlacement, EngineNodeConfig, LocalStoreConfig};
use dexzd_object_store::{ListingConsistency, ObjectStore};

use crate::harness::TestCluster;

const DELETION_DELAY: Duration = Duration::from_secs(60 * 60);

fn eventual_config(cluster: &TestCluster) -> EngineNodeConfig {
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.snapshots.listing_consistency = ListingConsistency::Eventual;
    config.snapshots.deletion_delay = DELETION_DELAY;
    config.snapshots.retained_count = 1;
    config.snapshots.interval = Duration::from_secs(24 * 60 * 60);
    config.snapshots.retry_delay = Duration::from_secs(24 * 60 * 60);
    config.snapshots.log_threshold_bytes = u64::MAX;
    // Paused time would advance while the blocking pool uploads, so uploads take no test time.
    config.blocking_work = BlockingWorkPlacement::CallingTaskForTest;
    config
}

async fn snapshot_objects(cluster: &TestCluster) -> BTreeSet<String> {
    cluster
        .object_store
        .list("snapshot/", None, 1000)
        .await
        .expect("list snapshot objects")
        .objects
        .into_iter()
        .map(|object| object.key)
        .collect()
}

fn manifest_prefix_of(manifest_key: &str) -> String {
    let (lineage_prefix, _) = manifest_key
        .split_once("/manifest/")
        .expect("a manifest key has a manifest segment");
    format!("{lineage_prefix}/manifest/")
}

async fn pointed_manifest_key(cluster: &TestCluster) -> String {
    cluster.cluster_manifest().await.partitions[0]
        .snapshot
        .as_ref()
        .expect("every partition points to a snapshot")
        .manifest_key
        .clone()
}

/// A restarted owner replaces the former pointer and fails one manifest write before the next.
#[tokio::test(start_paused = true)]
async fn eventual_retention_keeps_a_replaced_startup_pointer_and_a_failed_write_for_the_delay() {
    let started = tokio::time::Instant::now();
    let mut cluster = TestCluster::new();
    let config = eventual_config(&cluster);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt("eventual-flow", 1).await;
    first.node.take_snapshot().await.expect("snapshot succeeds");
    first.node.shutdown().await.expect("lease released");
    let former_objects = snapshot_objects(&cluster).await;

    let second = cluster.start_with(&config).await.expect("node restores");
    second.commit_prompt("eventual-flow", 1).await;
    second
        .node
        .take_snapshot()
        .await
        .expect("snapshot succeeds");
    let replaced_objects: BTreeSet<String> = snapshot_objects(&cluster)
        .await
        .difference(&former_objects)
        .cloned()
        .collect();
    cluster
        .faults
        .fail_puts_under(&manifest_prefix_of(&pointed_manifest_key(&cluster).await));
    second.commit_prompt("eventual-flow", 1).await;
    second
        .node
        .take_snapshot()
        .await
        .expect_err("the manifest write fails");
    cluster.faults.clear();
    let before_failure: BTreeSet<String> =
        former_objects.union(&replaced_objects).cloned().collect();
    let failed_write_files: BTreeSet<String> = snapshot_objects(&cluster)
        .await
        .difference(&before_failure)
        .cloned()
        .collect();
    assert!(
        !failed_write_files.is_empty(),
        "the failed upload wrote files"
    );
    second.commit_prompt("eventual-flow", 1).await;
    second
        .node
        .take_snapshot()
        .await
        .expect("snapshot succeeds");
    assert!(
        started.elapsed() < DELETION_DELAY / 10,
        "{:?}",
        started.elapsed()
    );

    // Protection lasts the delay; candidacy then lasts it again, so nothing goes before twice the delay.
    tokio::time::sleep(DELETION_DELAY + DELETION_DELAY / 2).await;
    let objects = snapshot_objects(&cluster).await;
    for key in former_objects
        .iter()
        .chain(&replaced_objects)
        .chain(&failed_write_files)
    {
        assert!(objects.contains(key), "{key} went before twice the delay");
    }

    tokio::time::sleep(DELETION_DELAY).await;
    let objects = snapshot_objects(&cluster).await;
    for key in former_objects
        .iter()
        .chain(&replaced_objects)
        .chain(&failed_write_files)
    {
        assert!(!objects.contains(key), "{key} stayed past twice the delay");
    }
    let pointed_key = pointed_manifest_key(&cluster).await;
    assert!(objects.contains(&pointed_key), "{objects:?}");
    assert!(
        objects.iter().any(|key| key.contains("/files/")),
        "the pointed snapshot keeps its file: {objects:?}"
    );
    second.node.shutdown().await.expect("lease released");
}
