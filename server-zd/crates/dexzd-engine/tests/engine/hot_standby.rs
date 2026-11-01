// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_engine::{
    CommitUrgency, EngineNodeConfig, HotStandby, HotStandbyConfig, HotStandbyDependencies,
    HotStandbyState, HotStandbyStatus, LocalStoreConfig, SystemEngineIdSource,
};
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore,
    ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult,
    WriteCondition, WriteOutcome,
};
use dexzd_storage::{
    CanonicalizedStoreField, RocksDbLocalStore, RocksDbStoreOptions, compare_store_views,
};
use tokio::time::Instant;

use crate::harness::{CountingApplier, TestCluster, TestNode};

const LOG_PREFIX: &str = "log/node-a/";
const FIRST_FLOW: &str = "followed-first";
/// Longest wait for a standby to reach the end of the log.
const CATCH_UP_DEADLINE: Duration = Duration::from_secs(120);
const BEHIND_LAG_THRESHOLD: Duration = Duration::from_secs(5);

/// The standby's view of the cluster's object storage: counts writes and can fail log reads.
struct StandbyObjectStore {
    inner: Arc<InMemoryObjectStore>,
    writes: AtomicU64,
    is_failing_log_reads: AtomicBool,
}

impl StandbyObjectStore {
    fn new(inner: Arc<InMemoryObjectStore>) -> Self {
        Self {
            inner,
            writes: AtomicU64::new(0),
            is_failing_log_reads: AtomicBool::new(false),
        }
    }

    fn fail_log_reads(&self, is_failing: bool) {
        self.is_failing_log_reads
            .store(is_failing, Ordering::SeqCst);
    }

    fn writes(&self) -> u64 {
        self.writes.load(Ordering::SeqCst)
    }

    fn count_write(&self) {
        self.writes.fetch_add(1, Ordering::SeqCst);
    }

    fn check_read(&self, operation: ObjectStoreOperation, key: &str) -> ObjectStoreResult<()> {
        if key.starts_with("log/") && self.is_failing_log_reads.load(Ordering::SeqCst) {
            return Err(ObjectStoreError::new(
                operation,
                key,
                ObjectStoreErrorKind::Transient,
                "the test stalls the standby's log reads",
            ));
        }
        Ok(())
    }
}

#[async_trait]
impl ObjectStore for StandbyObjectStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.count_write();
        self.inner.put(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.count_write();
        self.inner.put_file(key, path, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.check_read(ObjectStoreOperation::Get, key)?;
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.check_read(ObjectStoreOperation::Get, key)?;
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.check_read(ObjectStoreOperation::Get, key)?;
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.check_read(ObjectStoreOperation::Head, key)?;
        self.inner.head(key).await
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.count_write();
        self.inner.delete(key).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        self.count_write();
        self.inner.delete_many(keys).await
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        self.check_read(ObjectStoreOperation::List, prefix)?;
        self.inner.list(prefix, start_after, max_keys).await
    }
}

/// A hot standby with its own object storage view and applier.
struct TestStandby {
    standby: HotStandby,
    object_store: Arc<StandbyObjectStore>,
    applier: Arc<CountingApplier>,
}

fn start_standby(
    cluster: &TestCluster,
    config: EngineNodeConfig,
    standby_config: HotStandbyConfig,
) -> TestStandby {
    let object_store = Arc::new(StandbyObjectStore::new(cluster.object_store.clone()));
    let applier = Arc::new(CountingApplier::default());
    let standby = HotStandby::start(
        config,
        standby_config,
        HotStandbyDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: object_store.clone(),
            clock: cluster.clock.clone(),
            applier: applier.clone(),
            id_source: Arc::new(SystemEngineIdSource),
        },
    )
    .expect("the hot standby starts");
    TestStandby {
        standby,
        object_store,
        applier,
    }
}

/// Returns the Flow IDs of the commands the standby applied, once each, in apply order.
///
/// Replay checks in debug builds apply every command twice.
fn applied_flows(applier: &CountingApplier) -> Vec<String> {
    let mut seen = std::collections::BTreeSet::new();
    applier
        .applied_commands()
        .into_iter()
        .filter(|command| seen.insert(command.command_id.clone()))
        .map(|command| command.flow_id)
        .collect()
}

fn fast_standby_config() -> HotStandbyConfig {
    HotStandbyConfig {
        initial_poll_backoff: Duration::from_millis(10),
        max_poll_backoff: Duration::from_millis(100),
        manifest_refresh_interval: Duration::from_millis(500),
        stalled_seq_deletion_check_after: Duration::from_secs(1),
        behind_lag_threshold: BEHIND_LAG_THRESHOLD,
    }
}

fn rocksdb_store(directory: &Path) -> LocalStoreConfig {
    LocalStoreConfig::RocksDb {
        directory: directory.to_owned(),
        options: RocksDbStoreOptions::default(),
    }
}

/// Returns the incarnation and seq of the newest log batch.
async fn last_log_position(cluster: &TestCluster) -> (u32, u64) {
    let listing = cluster
        .object_store
        .list(LOG_PREFIX, None, 1000)
        .await
        .expect("list log batches");
    assert!(listing.next_start_after.is_none());
    let last = listing.objects.last().expect("the log holds a batch");
    let mut parts = last.key[LOG_PREFIX.len()..].split('/');
    let incarnation = parts
        .next()
        .and_then(|part| part.parse().ok())
        .expect("a batch key names its incarnation");
    let seq = parts
        .next()
        .and_then(|part| part.parse().ok())
        .expect("a batch key names its seq");
    (incarnation, seq)
}

async fn first_log_seq(cluster: &TestCluster) -> u64 {
    let listing = cluster
        .object_store
        .list(LOG_PREFIX, None, 1)
        .await
        .expect("list log batches");
    let first = listing.objects.first().expect("the log holds a batch");
    first
        .key
        .rsplit('/')
        .next()
        .and_then(|seq| seq.parse().ok())
        .expect("a batch key ends with its seq")
}

/// Waits until the standby has read every batch of the log and found no next one.
async fn wait_until_caught_up(cluster: &TestCluster, standby: &HotStandby) {
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    loop {
        let (incarnation, last_seq) = last_log_position(cluster).await;
        let status = standby.status();
        assert_not_failed(&status);
        if status.state == HotStandbyState::Following
            && let Some(lag) = status.lag
            && lag.incarnation == incarnation
            && lag.next_seq == last_seq + 1
            && lag.duration.is_zero()
        {
            return;
        }
        assert!(
            Instant::now() < deadline,
            "the standby did not reach incarnation {incarnation} seq {last_seq}: {status:?}"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

fn assert_not_failed(status: &HotStandbyStatus) {
    assert_ne!(
        status.state,
        HotStandbyState::Failed,
        "the standby failed: {:?}",
        status.failure
    );
}

/// Requires the closed stores to hold the same state apart from fields each process writes itself.
fn assert_same_stores(serving_directory: &Path, standby_directory: &Path) {
    let serving = RocksDbLocalStore::open(serving_directory, RocksDbStoreOptions::default())
        .expect("the serving store opens");
    let standby = RocksDbLocalStore::open(standby_directory, RocksDbStoreOptions::default())
        .expect("the standby store opens");
    compare_store_views(
        &serving,
        &standby,
        &[
            CanonicalizedStoreField::NodeLineageId,
            CanonicalizedStoreField::NodeLastSnapshotNumber,
            CanonicalizedStoreField::PartitionApplied,
            CanonicalizedStoreField::PartitionAppliedEpoch,
        ],
    )
    .expect("the standby store matches the serving store");
}

async fn commit_to_both_flows(serving: &TestNode, second_flow: &str, count: usize) {
    serving.commit_prompt(FIRST_FLOW, count).await;
    serving.commit_prompt(second_flow, count).await;
}

/// Returns the set-aside copies of `directory`, which a restore from a snapshot leaves.
fn set_aside_copies(directory: &Path) -> Vec<PathBuf> {
    let name = directory
        .file_name()
        .and_then(|name| name.to_str())
        .expect("the store directory has a name");
    let prefix = format!("{name}.discarded-");
    std::fs::read_dir(
        directory
            .parent()
            .expect("the store directory has a parent"),
    )
    .expect("list the parent directory")
    .map(|entry| entry.expect("read a directory entry").path())
    .filter(|path| {
        path.file_name()
            .and_then(|name| name.to_str())
            .is_some_and(|name| name.starts_with(&prefix))
    })
    .collect()
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_store_matches_the_serving_store_after_following_from_seq_one() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(rocksdb_store(&serving_directory)))
        .await
        .expect("the serving node starts");
    let second_flow = serving.flow_in_other_partition(&[FIRST_FLOW]);
    commit_to_both_flows(&serving, &second_flow, 2).await;
    let standby = start_standby(
        &cluster,
        cluster.config(rocksdb_store(&standby_directory)),
        fast_standby_config(),
    );
    commit_to_both_flows(&serving, &second_flow, 3).await;
    wait_until_caught_up(&cluster, &standby.standby).await;

    serving.node.shutdown().await.expect("lease released");
    standby.standby.shutdown().await;
    assert_eq!(standby.object_store.writes(), 0);
    assert_same_stores(&serving_directory, &standby_directory);
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_store_matches_the_serving_store_after_restoring_a_snapshot() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(rocksdb_store(&serving_directory)))
        .await
        .expect("the serving node starts");
    let second_flow = serving.flow_in_other_partition(&[FIRST_FLOW]);
    commit_to_both_flows(&serving, &second_flow, 3).await;
    serving
        .node
        .take_snapshot()
        .await
        .expect("the snapshot succeeds");
    let standby = start_standby(
        &cluster,
        cluster.config(rocksdb_store(&standby_directory)),
        fast_standby_config(),
    );
    commit_to_both_flows(&serving, &second_flow, 4).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let applied = applied_flows(&standby.applier);
    assert!(
        applied
            .iter()
            .all(|flow| flow == FIRST_FLOW || *flow == second_flow),
        "{applied:?}"
    );
    assert_eq!(
        applied.len(),
        8,
        "the standby applies only the commands after the snapshot"
    );

    serving.node.shutdown().await.expect("lease released");
    standby.standby.shutdown().await;
    assert_same_stores(&serving_directory, &standby_directory);
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_never_writes_object_storage_while_the_node_snapshots_and_cleans_its_log() {
    let mut cluster = TestCluster::new();
    let mut serving_config = cluster.config(LocalStoreConfig::InMemory);
    serving_config.snapshots.deletion_delay = Duration::ZERO;
    let serving = cluster
        .start_with(&serving_config)
        .await
        .expect("the serving node starts");
    let standby = start_standby(
        &cluster,
        cluster.config(LocalStoreConfig::InMemory),
        fast_standby_config(),
    );
    let second_flow = serving.flow_in_other_partition(&[FIRST_FLOW]);
    for _ in 0..3 {
        commit_to_both_flows(&serving, &second_flow, 2).await;
        wait_until_caught_up(&cluster, &standby.standby).await;
        serving
            .node
            .take_snapshot()
            .await
            .expect("the snapshot succeeds");
        tokio::time::sleep(Duration::from_secs(2)).await;
        assert_not_failed(&standby.standby.status());
    }
    assert!(
        first_log_seq(&cluster).await > 1,
        "log cleanup deleted batches"
    );
    assert!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Put, "snapshot/")
            > 0
    );

    assert_eq!(standby.object_store.writes(), 0);
    assert_eq!(
        applied_flows(&standby.applier).len(),
        12,
        "the standby followed every command instead of restoring a snapshot"
    );
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_follows_the_log_into_a_new_incarnation() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let mut cluster = TestCluster::new();
    let first_config = cluster.config(rocksdb_store(&serving_directory));
    let first = cluster
        .start_with(&first_config)
        .await
        .expect("incarnation 1 starts");
    let mut standby_config = cluster.config(rocksdb_store(&standby_directory));
    standby_config.node.incarnation = 2;
    let standby = start_standby(&cluster, standby_config, fast_standby_config());
    let second_flow = first.flow_in_other_partition(&[FIRST_FLOW]);
    commit_to_both_flows(&first, &second_flow, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    first.node.shutdown().await.expect("lease released");

    let mut second_config = first_config;
    second_config.node.incarnation = 2;
    let second = cluster
        .start_with(&second_config)
        .await
        .expect("incarnation 2 starts");
    commit_to_both_flows(&second, &second_flow, 3).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let lag = standby.standby.status().lag.expect("the standby follows");
    assert_eq!(lag.incarnation, 2);

    second.node.shutdown().await.expect("lease released");
    standby.standby.shutdown().await;
    assert_eq!(standby.object_store.writes(), 0);
    assert_same_stores(&serving_directory, &standby_directory);
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_waits_at_a_log_hole_while_a_later_batch_exists() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby = start_standby(
        &cluster,
        cluster.config(LocalStoreConfig::InMemory),
        fast_standby_config(),
    );
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let (_, last_seq) = last_log_position(&cluster).await;
    let hole_seq = last_seq + 1;

    let held_flow = "held-in-the-hole";
    let landed_flow = serving.flow_in_other_partition(&[held_flow]);
    cluster
        .faults
        .delay_next_put_under(LOG_PREFIX, Duration::from_secs(5));
    let (_, held) = serving
        .submit(held_flow, CommitUrgency::Prompt)
        .expect("submit the held command");
    cluster.faults.wait_until_delayed_operation_entered().await;
    let (_, landed) = serving
        .submit(&landed_flow, CommitUrgency::Prompt)
        .expect("submit the landed command");
    let deadline = Instant::now() + Duration::from_secs(4);
    while last_log_position(&cluster).await.1 != hole_seq + 1 {
        assert!(Instant::now() < deadline, "the later batch did not land");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    tokio::time::sleep(Duration::from_secs(2)).await;
    let lag = standby.standby.status().lag.expect("the standby follows");
    assert_eq!(lag.next_seq, hole_seq, "the standby waits at the hole");
    assert_eq!(
        applied_flows(&standby.applier),
        vec![FIRST_FLOW.to_owned(); 2]
    );

    held.wait().await.expect("the held command commits");
    landed.wait().await.expect("the landed command commits");
    wait_until_caught_up(&cluster, &standby.standby).await;
    // The two Flows use different partitions, whose applier lanes run in either order.
    let mut filled = applied_flows(&standby.applier).split_off(2);
    filled.sort();
    let mut expected = vec![held_flow.to_owned(), landed_flow];
    expected.sort();
    assert_eq!(filled, expected);
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restores_the_newest_snapshot_when_the_manifest_shows_cleanup() {
    // Without the check at a stalled seq, only the manifest refresh can find the deletion.
    restore_after_log_cleanup(HotStandbyConfig {
        stalled_seq_deletion_check_after: Duration::from_secs(24 * 60 * 60),
        ..fast_standby_config()
    })
    .await;
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restores_the_newest_snapshot_when_its_next_seq_was_deleted() {
    // Without manifest refreshes, only the check at a stalled seq can find the deletion.
    restore_after_log_cleanup(HotStandbyConfig {
        manifest_refresh_interval: Duration::from_secs(24 * 60 * 60),
        ..fast_standby_config()
    })
    .await;
}

/// The standby stalls while log cleanup deletes batches it needs, then restores a snapshot.
async fn restore_after_log_cleanup(standby_config: HotStandbyConfig) {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let mut cluster = TestCluster::new();
    let mut serving_config = cluster.config(rocksdb_store(&serving_directory));
    serving_config.snapshots.deletion_delay = Duration::ZERO;
    let serving = cluster
        .start_with(&serving_config)
        .await
        .expect("the serving node starts");
    let second_flow = serving.flow_in_other_partition(&[FIRST_FLOW]);
    commit_to_both_flows(&serving, &second_flow, 2).await;
    serving
        .node
        .take_snapshot()
        .await
        .expect("the first snapshot succeeds");
    let standby = start_standby(
        &cluster,
        cluster.config(rocksdb_store(&standby_directory)),
        standby_config,
    );
    commit_to_both_flows(&serving, &second_flow, 1).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let stalled_at = standby
        .standby
        .status()
        .lag
        .expect("the standby follows")
        .next_seq;

    standby.object_store.fail_log_reads(true);
    commit_to_both_flows(&serving, &second_flow, 3).await;
    serving
        .node
        .take_snapshot()
        .await
        .expect("the second snapshot succeeds");
    assert!(
        first_log_seq(&cluster).await > stalled_at,
        "log cleanup deleted the batches the standby needs"
    );
    commit_to_both_flows(&serving, &second_flow, 1).await;
    tokio::time::sleep(BEHIND_LAG_THRESHOLD + Duration::from_secs(1)).await;
    let stalled = standby.standby.status();
    assert_ne!(stalled.state, HotStandbyState::Failed, "{stalled:?}");

    standby.object_store.fail_log_reads(false);
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_eq!(
        set_aside_copies(&standby_directory).len(),
        1,
        "the standby set its store aside and restored the newest snapshot"
    );
    assert_eq!(standby.object_store.writes(), 0);
    serving.node.shutdown().await.expect("lease released");
    standby.standby.shutdown().await;
    assert_same_stores(&serving_directory, &standby_directory);
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_reports_behind_only_while_it_cannot_confirm_the_log_end() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby = start_standby(
        &cluster,
        cluster.config(LocalStoreConfig::InMemory),
        fast_standby_config(),
    );
    serving.commit_prompt(FIRST_FLOW, 1).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    // Caught up on an idle log, the standby reports no lag however old its last batch is.
    tokio::time::sleep(BEHIND_LAG_THRESHOLD * 2).await;
    let idle = standby.standby.status();
    assert_eq!(idle.state, HotStandbyState::Following, "{idle:?}");

    standby.object_store.fail_log_reads(true);
    tokio::time::sleep(Duration::from_secs(1)).await;
    let briefly_failing = standby.standby.status();
    assert_eq!(
        briefly_failing.state,
        HotStandbyState::Following,
        "{briefly_failing:?}"
    );
    tokio::time::sleep(BEHIND_LAG_THRESHOLD * 2).await;
    let behind = standby.standby.status();
    assert_eq!(behind.state, HotStandbyState::Behind, "{behind:?}");

    standby.object_store.fail_log_reads(false);
    serving.commit_prompt(FIRST_FLOW, 1).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}
