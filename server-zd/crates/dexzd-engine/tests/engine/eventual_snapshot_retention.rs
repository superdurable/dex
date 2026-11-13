// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Snapshot retention of a node whose object storage lists eventually, as Tencent COS does.

use std::collections::{BTreeMap, BTreeSet};
use std::time::Duration;

use dexzd_engine::{EngineNodeConfig, LocalStoreConfig};
use dexzd_object_store::{ListingConsistency, ObjectStore};
use tokio::time::Instant;

use crate::harness::TestCluster;

const DELETION_DELAY: Duration = Duration::from_secs(60 * 60);
/// Virtual time between two reads of the snapshot objects while retention waits.
const CHECK_STEP: Duration = Duration::from_secs(5 * 60);
/// How far the paused clock may pass the test's own sleeps before a read.
const CLOCK_SLACK: Duration = Duration::from_secs(1);

fn eventual_config(cluster: &TestCluster) -> EngineNodeConfig {
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.snapshots.listing_consistency = ListingConsistency::Eventual;
    config.snapshots.deletion_delay = DELETION_DELAY;
    config.snapshots.retained_count = 1;
    config.snapshots.interval = Duration::from_secs(24 * 60 * 60);
    config.snapshots.retry_delay = Duration::from_secs(24 * 60 * 60);
    config.snapshots.log_threshold_bytes = u64::MAX;
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

fn snapshot_number_of(manifest_key: &str) -> u64 {
    let (_, snapshot_number) = manifest_key
        .split_once("/manifest/")
        .expect("a manifest key has a manifest segment");
    snapshot_number
        .parse()
        .expect("a manifest key ends in its snapshot number")
}

async fn pointed_manifest_key(cluster: &TestCluster) -> String {
    cluster.cluster_manifest().await.partitions[0]
        .snapshot
        .as_ref()
        .expect("every partition points to a snapshot")
        .manifest_key
        .clone()
}

/// Objects whose protection ended no earlier than `protected_until_at_least`.
struct ProtectedGroup {
    name: &'static str,
    keys: BTreeSet<String>,
    protected_until_at_least: Instant,
}

/// The paused clock of the test, checked against the sleeps the test planned.
struct VirtualSchedule {
    started: Instant,
    planned_elapsed: Duration,
}

impl VirtualSchedule {
    async fn sleep(&mut self, duration: Duration) {
        tokio::time::sleep(duration).await;
        self.planned_elapsed += duration;
    }

    /// Fails when the paused clock advanced past the planned sleeps on its own.
    fn assert_on_schedule(&self, context: &str) {
        let elapsed = self.started.elapsed();
        assert!(
            elapsed >= self.planned_elapsed && elapsed - self.planned_elapsed <= CLOCK_SLACK,
            "{context}: virtual elapsed {elapsed:?}, planned {:?}",
            self.planned_elapsed
        );
    }
}

/// Lists the snapshot objects and confirms each tracked key with a HEAD.
async fn read_snapshot_objects(
    cluster: &TestCluster,
    tracked_keys: impl Iterator<Item = &String>,
) -> BTreeSet<String> {
    let listed = snapshot_objects(cluster).await;
    for key in tracked_keys {
        let is_headed = cluster
            .object_store
            .head(key)
            .await
            .expect("head a snapshot object")
            .is_some();
        assert_eq!(
            listed.contains(key),
            is_headed,
            "the test's LIST and HEAD disagree on {key}"
        );
    }
    listed
}

/// A restarted owner replaces the former pointer and fails one manifest write before the next.
#[tokio::test(start_paused = true)]
async fn eventual_retention_keeps_a_replaced_startup_pointer_and_a_failed_write_for_the_delay() {
    let started = Instant::now();
    let mut cluster = TestCluster::new();
    let config = eventual_config(&cluster);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt("eventual-flow", 1).await;
    first.node.take_snapshot().await.expect("snapshot succeeds");
    first.node.shutdown().await.expect("lease released");
    let former_objects = snapshot_objects(&cluster).await;

    let second = cluster.start_with(&config).await.expect("node restores");
    second.commit_prompt("eventual-flow", 1).await;
    let former_pointer_replaced_after = Instant::now();
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
    let replaced_pointer = pointed_manifest_key(&cluster).await;
    cluster
        .faults
        .fail_puts_under(&manifest_prefix_of(&replaced_pointer));
    second.commit_prompt("eventual-flow", 1).await;
    let failing_snapshot_started = Instant::now();
    second
        .node
        .take_snapshot()
        .await
        .expect_err("the manifest write fails");
    let failure_reported = Instant::now();
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
    assert!(
        failed_write_files.iter().all(|key| key.contains("/files/")),
        "the failed manifest write never landed: {failed_write_files:?}"
    );
    second.commit_prompt("eventual-flow", 1).await;
    let replaced_pointer_moved_after = Instant::now();
    second
        .node
        .take_snapshot()
        .await
        .expect("snapshot succeeds");
    let newest_pointer = pointed_manifest_key(&cluster).await;
    // A reused number would let the next upload overwrite the failed write's record.
    assert_eq!(
        snapshot_number_of(&newest_pointer),
        snapshot_number_of(&replaced_pointer) + 2,
        "the failed write and the next snapshot have distinct manifest keys"
    );
    let setup_elapsed = started.elapsed();
    assert!(setup_elapsed < DELETION_DELAY / 10, "{setup_elapsed:?}");

    let groups = [
        ProtectedGroup {
            name: "former pointer",
            keys: former_objects,
            protected_until_at_least: former_pointer_replaced_after + DELETION_DELAY,
        },
        ProtectedGroup {
            name: "replaced pointer",
            keys: replaced_objects,
            protected_until_at_least: replaced_pointer_moved_after + DELETION_DELAY,
        },
        ProtectedGroup {
            name: "failed write",
            keys: failed_write_files,
            protected_until_at_least: failing_snapshot_started + DELETION_DELAY,
        },
    ];
    let tracked_keys: BTreeSet<String> = groups
        .iter()
        .flat_map(|group| group.keys.iter().cloned())
        .collect();
    let mut schedule = VirtualSchedule {
        started,
        planned_elapsed: setup_elapsed,
    };
    let mut first_missing_at: BTreeMap<String, Duration> = BTreeMap::new();
    // Protection lasts the delay; candidacy then lasts it again, so nothing goes before twice the delay.
    while schedule.planned_elapsed < setup_elapsed + DELETION_DELAY * 5 / 2 {
        schedule.sleep(CHECK_STEP).await;
        schedule.assert_on_schedule("before reading the snapshot objects");
        let objects = read_snapshot_objects(&cluster, tracked_keys.iter()).await;
        let read_at = Instant::now();
        schedule.assert_on_schedule("after reading the snapshot objects");
        for group in &groups {
            let candidate_since = group.protected_until_at_least;
            for key in &group.keys {
                if objects.contains(key) {
                    assert!(
                        !first_missing_at.contains_key(key),
                        "{key} came back after it was gone"
                    );
                    continue;
                }
                first_missing_at
                    .entry(key.clone())
                    .or_insert_with(|| read_at - started);
                assert!(
                    read_at >= candidate_since + DELETION_DELAY,
                    "{} object {key} went early: {:?} after the failure was reported, {:?} \
                     after the failing snapshot started, {:?} after its protection could end \
                     at the earliest",
                    group.name,
                    read_at.saturating_duration_since(failure_reported),
                    read_at.saturating_duration_since(failing_snapshot_started),
                    read_at.saturating_duration_since(candidate_since),
                );
            }
        }
    }

    let objects = snapshot_objects(&cluster).await;
    for key in &tracked_keys {
        assert!(
            !objects.contains(key),
            "{key} stayed past twice the delay; first missing at {first_missing_at:?}"
        );
    }
    let pointed_key = pointed_manifest_key(&cluster).await;
    assert!(objects.contains(&pointed_key), "{objects:?}");
    assert!(
        objects.iter().any(|key| key.contains("/files/")),
        "the pointed snapshot keeps its file: {objects:?}"
    );
    second.node.shutdown().await.expect("lease released");
}
