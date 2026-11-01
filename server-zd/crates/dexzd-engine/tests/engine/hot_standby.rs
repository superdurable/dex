// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_engine::{
    CommitUrgency, EngineNodeConfig, EngineStartError, HotStandby, HotStandbyConfig,
    HotStandbyDependencies, HotStandbyState, HotStandbyStatus, LocalStoreConfig,
    SystemEngineIdSource,
};
use dexzd_log::{BatchContent, BatchRecord, StreamIdentity, decode_batch, encode_batch};
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore,
    ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult,
    WriteCondition, WriteOutcome,
};
use dexzd_storage::{
    CanonicalizedStoreField, LocalReadView, RocksDbLocalStore, RocksDbStoreOptions,
    compare_store_views,
};
use tokio::time::Instant;

use crate::harness::{CountingApplier, TestCluster, TestNode};

pub(crate) const LOG_PREFIX: &str = "log/node-a/";
pub(crate) const FIRST_FLOW: &str = "followed-first";
/// Longest wait for a standby to reach the end of the log.
pub(crate) const CATCH_UP_DEADLINE: Duration = Duration::from_secs(120);
pub(crate) const BEHIND_LAG_THRESHOLD: Duration = Duration::from_secs(5);
const MANIFEST_KEY: &str = "manifest";

/// One process's view of the cluster's object storage: records its writes and reads, and can
/// fail or delay them.
pub(crate) struct ProcessObjectStore {
    inner: Arc<InMemoryObjectStore>,
    written_keys: Mutex<Vec<String>>,
    log_reads: AtomicU64,
    lease_reads: AtomicU64,
    manifest_reads: AtomicU64,
    /// Delays the next manifest read, which then makes later manifest reads fail.
    next_manifest_read_delay: Mutex<Option<Duration>>,
    delayed_manifest_reads: AtomicU64,
    is_failing_log_reads: AtomicBool,
    is_failing_manifest_reads: AtomicBool,
    is_failing_all_reads: AtomicBool,
    /// GETs of the cluster object, which every bootstrap reads first.
    cluster_reads: AtomicU64,
    read_faults: Mutex<Option<ReadFaults>>,
    /// Reads drawn so far, which seed each read's fault draw.
    fault_draws: AtomicU64,
    /// Holds reads of snapshot objects while `false`.
    snapshot_reads_open: tokio::sync::watch::Sender<bool>,
    held_snapshot_reads: AtomicU64,
}

/// Seeded faults on a process's reads.
#[derive(Debug, Clone, Copy)]
pub(crate) struct ReadFaults {
    /// Reads per thousand that fail at once, as an HTTP 5xx or 503 would.
    pub(crate) failures_per_mille: u64,
    /// Reads per thousand that fail after a long wait, as a timeout would.
    pub(crate) timeouts_per_mille: u64,
    /// Reads per thousand that succeed after a latency spike.
    pub(crate) slow_reads_per_mille: u64,
}

/// How long a timed-out read waits before it fails.
const READ_TIMEOUT: Duration = Duration::from_secs(5);
/// How long a slow read waits before it proceeds.
const READ_LATENCY_SPIKE: Duration = Duration::from_secs(2);

impl ProcessObjectStore {
    pub(crate) fn new(inner: Arc<InMemoryObjectStore>) -> Self {
        Self {
            inner,
            written_keys: Mutex::new(Vec::new()),
            log_reads: AtomicU64::new(0),
            lease_reads: AtomicU64::new(0),
            manifest_reads: AtomicU64::new(0),
            next_manifest_read_delay: Mutex::new(None),
            delayed_manifest_reads: AtomicU64::new(0),
            is_failing_log_reads: AtomicBool::new(false),
            is_failing_manifest_reads: AtomicBool::new(false),
            is_failing_all_reads: AtomicBool::new(false),
            cluster_reads: AtomicU64::new(0),
            read_faults: Mutex::new(None),
            fault_draws: AtomicU64::new(0),
            snapshot_reads_open: tokio::sync::watch::Sender::new(true),
            held_snapshot_reads: AtomicU64::new(0),
        }
    }

    /// Injects `faults` into every later read, or none with `None`.
    pub(crate) fn inject_read_faults(&self, faults: Option<ReadFaults>) {
        *self
            .read_faults
            .lock()
            .expect("read fault lock is not poisoned") = faults;
    }

    /// Returns how many times this process read the cluster object, once per bootstrap.
    pub(crate) fn cluster_reads(&self) -> u64 {
        self.cluster_reads.load(Ordering::SeqCst)
    }

    /// Holds every later read of a snapshot object while `is_holding`.
    pub(crate) fn hold_snapshot_reads(&self, is_holding: bool) {
        self.snapshot_reads_open.send_replace(!is_holding);
    }

    /// Returns how many snapshot reads waited while snapshot reads were held.
    pub(crate) fn held_snapshot_reads(&self) -> u64 {
        self.held_snapshot_reads.load(Ordering::SeqCst)
    }

    /// Waits while snapshot reads are held, then applies the injected read faults.
    async fn delay_or_fail_read(
        &self,
        operation: ObjectStoreOperation,
        key: &str,
    ) -> ObjectStoreResult<()> {
        if key.starts_with("snapshot/") && !*self.snapshot_reads_open.borrow() {
            self.held_snapshot_reads.fetch_add(1, Ordering::SeqCst);
            let mut open = self.snapshot_reads_open.subscribe();
            if open.wait_for(|is_open| *is_open).await.is_err() {
                return Err(read_failure(
                    operation,
                    key,
                    ObjectStoreErrorKind::Transient,
                ));
            }
        }
        let Some(faults) = *self
            .read_faults
            .lock()
            .expect("read fault lock is not poisoned")
        else {
            return Ok(());
        };
        let draw = xxhash_rust::xxh3::xxh3_64(
            &self
                .fault_draws
                .fetch_add(1, Ordering::SeqCst)
                .to_le_bytes(),
        ) % 1000;
        let failures = faults.failures_per_mille;
        let timeouts = failures + faults.timeouts_per_mille;
        let slow_reads = timeouts + faults.slow_reads_per_mille;
        if draw < failures {
            return Err(read_failure(
                operation,
                key,
                ObjectStoreErrorKind::Transient,
            ));
        }
        if draw < timeouts {
            tokio::time::sleep(READ_TIMEOUT).await;
            return Err(read_failure(
                operation,
                key,
                ObjectStoreErrorKind::OutcomeUnknown,
            ));
        }
        if draw < slow_reads {
            tokio::time::sleep(READ_LATENCY_SPIKE).await;
        }
        Ok(())
    }

    pub(crate) fn fail_log_reads(&self, is_failing: bool) {
        self.is_failing_log_reads
            .store(is_failing, Ordering::SeqCst);
    }

    pub(crate) fn fail_all_reads(&self, is_failing: bool) {
        self.is_failing_all_reads
            .store(is_failing, Ordering::SeqCst);
    }

    pub(crate) fn fail_manifest_reads(&self, is_failing: bool) {
        self.is_failing_manifest_reads
            .store(is_failing, Ordering::SeqCst);
    }

    /// Delays the next manifest read by `delay` and fails every manifest read after it.
    fn delay_next_manifest_read(&self, delay: Duration) {
        *self
            .next_manifest_read_delay
            .lock()
            .expect("manifest delay lock is not poisoned") = Some(delay);
    }

    fn delayed_manifest_reads(&self) -> u64 {
        self.delayed_manifest_reads.load(Ordering::SeqCst)
    }

    /// Returns how many writes and deletes this process sent.
    pub(crate) fn writes(&self) -> u64 {
        self.written_keys
            .lock()
            .expect("written key lock is not poisoned")
            .len() as u64
    }

    /// Returns the keys this process wrote or deleted, in call order.
    pub(crate) fn written_keys(&self) -> Vec<String> {
        self.written_keys
            .lock()
            .expect("written key lock is not poisoned")
            .clone()
    }

    /// Returns how many GETs and listings of log batches this process sent.
    pub(crate) fn log_reads(&self) -> u64 {
        self.log_reads.load(Ordering::SeqCst)
    }

    pub(crate) fn lease_reads(&self) -> u64 {
        self.lease_reads.load(Ordering::SeqCst)
    }

    fn manifest_reads(&self) -> u64 {
        self.manifest_reads.load(Ordering::SeqCst)
    }

    fn count_write(&self, key: &str) {
        self.written_keys
            .lock()
            .expect("written key lock is not poisoned")
            .push(key.to_owned());
    }

    fn check_read(&self, operation: ObjectStoreOperation, key: &str) -> ObjectStoreResult<()> {
        let is_manifest = key == MANIFEST_KEY;
        if is_manifest {
            self.manifest_reads.fetch_add(1, Ordering::SeqCst);
        }
        if key == "cluster" {
            self.cluster_reads.fetch_add(1, Ordering::SeqCst);
        }
        if key.starts_with("log/") && operation != ObjectStoreOperation::Head {
            self.log_reads.fetch_add(1, Ordering::SeqCst);
        }
        if key.starts_with("lease/") {
            self.lease_reads.fetch_add(1, Ordering::SeqCst);
        }
        let is_failing = self.is_failing_all_reads.load(Ordering::SeqCst)
            || (is_manifest && self.is_failing_manifest_reads.load(Ordering::SeqCst))
            || (key.starts_with("log/") && self.is_failing_log_reads.load(Ordering::SeqCst));
        if is_failing {
            return Err(read_failure(
                operation,
                key,
                ObjectStoreErrorKind::Transient,
            ));
        }
        Ok(())
    }
}

fn read_failure(
    operation: ObjectStoreOperation,
    key: &str,
    kind: ObjectStoreErrorKind,
) -> ObjectStoreError {
    ObjectStoreError::new(operation, key, kind, "the test fails this process's reads")
}

#[async_trait]
impl ObjectStore for ProcessObjectStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.count_write(key);
        self.inner.put(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.count_write(key);
        self.inner.put_file(key, path, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.check_read(ObjectStoreOperation::Get, key)?;
        self.delay_or_fail_read(ObjectStoreOperation::Get, key)
            .await?;
        let delay = if key == MANIFEST_KEY {
            self.next_manifest_read_delay
                .lock()
                .expect("manifest delay lock is not poisoned")
                .take()
        } else {
            None
        };
        if let Some(delay) = delay {
            self.fail_manifest_reads(true);
            self.delayed_manifest_reads.fetch_add(1, Ordering::SeqCst);
            tokio::time::sleep(delay).await;
        }
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.check_read(ObjectStoreOperation::Get, key)?;
        self.delay_or_fail_read(ObjectStoreOperation::Get, key)
            .await?;
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.check_read(ObjectStoreOperation::Get, key)?;
        self.delay_or_fail_read(ObjectStoreOperation::Get, key)
            .await?;
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.check_read(ObjectStoreOperation::Head, key)?;
        self.delay_or_fail_read(ObjectStoreOperation::Head, key)
            .await?;
        self.inner.head(key).await
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.count_write(key);
        self.inner.delete(key).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        for key in keys {
            self.count_write(key);
        }
        self.inner.delete_many(keys).await
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        self.check_read(ObjectStoreOperation::List, prefix)?;
        self.delay_or_fail_read(ObjectStoreOperation::List, prefix)
            .await?;
        self.inner.list(prefix, start_after, max_keys).await
    }
}

/// A hot standby with its own object storage view and applier.
pub(crate) struct TestStandby {
    pub(crate) standby: HotStandby,
    pub(crate) object_store: Arc<ProcessObjectStore>,
    pub(crate) applier: Arc<CountingApplier>,
}

pub(crate) fn start_standby(
    cluster: &TestCluster,
    config: EngineNodeConfig,
    standby_config: HotStandbyConfig,
) -> TestStandby {
    let object_store = Arc::new(ProcessObjectStore::new(cluster.object_store.clone()));
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
            restart_jitter_seed: 7,
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
pub(crate) fn applied_flows(applier: &CountingApplier) -> Vec<String> {
    let mut seen = std::collections::BTreeSet::new();
    applier
        .applied_commands()
        .into_iter()
        .filter(|command| seen.insert(command.command_id.clone()))
        .map(|command| command.flow_id)
        .collect()
}

pub(crate) fn fast_standby_config() -> HotStandbyConfig {
    HotStandbyConfig {
        initial_poll_backoff: Duration::from_millis(10),
        max_poll_backoff: Duration::from_millis(100),
        manifest_refresh_interval: Duration::from_millis(500),
        stalled_seq_deletion_check_after: Duration::from_secs(1),
        behind_lag_threshold: BEHIND_LAG_THRESHOLD,
    }
}

pub(crate) fn rocksdb_store(directory: &Path) -> LocalStoreConfig {
    LocalStoreConfig::RocksDb {
        directory: directory.to_owned(),
        options: RocksDbStoreOptions::default(),
    }
}

/// Returns the incarnation and seq of the newest log batch.
pub(crate) async fn last_log_position(cluster: &TestCluster) -> (u32, u64) {
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

pub(crate) async fn first_log_seq(cluster: &TestCluster) -> u64 {
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
pub(crate) async fn wait_until_caught_up(cluster: &TestCluster, standby: &HotStandby) {
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

/// Requires that no write reached object storage and that the read-only store rejected none.
pub(crate) fn assert_wrote_nothing(standby: &TestStandby) {
    assert_eq!(standby.object_store.writes(), 0);
    assert_eq!(standby.standby.rejected_writes_for_test(), 0);
}

pub(crate) fn assert_not_failed(status: &HotStandbyStatus) {
    assert_ne!(
        status.state,
        HotStandbyState::Failed,
        "the standby failed: {:?}",
        status.failure
    );
}

/// Requires the closed stores to hold the same state apart from fields each process writes itself.
pub(crate) fn assert_same_stores(serving_directory: &Path, standby_directory: &Path) {
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

pub(crate) async fn commit_to_both_flows(serving: &TestNode, second_flow: &str, count: usize) {
    serving.commit_prompt(FIRST_FLOW, count).await;
    serving.commit_prompt(second_flow, count).await;
}

/// Returns the set-aside copies of `directory`, which a restore from a snapshot leaves.
pub(crate) fn set_aside_copies(directory: &Path) -> Vec<PathBuf> {
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

    assert_wrote_nothing(&standby);
    serving.node.shutdown().await.expect("lease released");
    standby.standby.shutdown().await;
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

    assert_wrote_nothing(&standby);
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

    assert_wrote_nothing(&standby);
    second.node.shutdown().await.expect("lease released");
    standby.standby.shutdown().await;
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

/// How a test lets the standby find that log cleanup deleted batches it needs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum DeletionDetection {
    /// Only a manifest refresh can find it: the check at a stalled seq never runs.
    Manifest,
    /// Only the check at a stalled seq can find it: manifest reads fail from the stall on.
    StalledSeq,
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restores_the_newest_snapshot_when_the_manifest_shows_cleanup() {
    restore_after_log_cleanup(DeletionDetection::Manifest).await;
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restores_the_newest_snapshot_when_its_next_seq_was_deleted() {
    restore_after_log_cleanup(DeletionDetection::StalledSeq).await;
}

/// The standby stalls while log cleanup deletes batches it needs, then restores a snapshot.
async fn restore_after_log_cleanup(detection: DeletionDetection) {
    let standby_config = match detection {
        DeletionDetection::Manifest => HotStandbyConfig {
            stalled_seq_deletion_check_after: Duration::from_secs(24 * 60 * 60),
            ..fast_standby_config()
        },
        DeletionDetection::StalledSeq => fast_standby_config(),
    };
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
    if detection == DeletionDetection::StalledSeq {
        standby.object_store.fail_manifest_reads(true);
    }
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
    if detection == DeletionDetection::StalledSeq {
        // The restart rereads the manifest, which still fails, so the standby stops following.
        let deadline = Instant::now() + CATCH_UP_DEADLINE;
        while !matches!(
            standby.standby.status().state,
            HotStandbyState::Bootstrapping | HotStandbyState::Restarting
        ) {
            assert!(
                Instant::now() < deadline,
                "the standby did not find the deletion: {:?}",
                standby.standby.status()
            );
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
        standby.object_store.fail_manifest_reads(false);
    }
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_eq!(
        set_aside_copies(&standby_directory).len(),
        1,
        "the standby set its store aside and restored the newest snapshot"
    );
    assert_wrote_nothing(&standby);
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

/// Returns the ID of the cluster that the first node start created.
async fn cluster_id(cluster: &TestCluster) -> [u8; 16] {
    let content = cluster
        .object_store
        .get("cluster")
        .await
        .expect("read the cluster object")
        .expect("the cluster object exists");
    let cluster_object: dexzd_protocol::dexzd::v1::ClusterObject =
        prost::Message::decode(content.bytes).expect("decode the cluster object");
    cluster_object
        .cluster_id
        .as_slice()
        .try_into()
        .expect("a cluster ID has 16 bytes")
}

pub(crate) async fn stream_of(cluster: &TestCluster, incarnation: u32) -> StreamIdentity {
    StreamIdentity {
        cluster_id: cluster_id(cluster).await,
        node_id: "node-a".to_owned(),
        incarnation,
    }
}

/// Returns the last seq that the manifest records for the closed stream of `incarnation`.
async fn recorded_end_seq(cluster: &TestCluster, incarnation: u32) -> u64 {
    let manifest = cluster.cluster_manifest().await;
    let segment = manifest.partitions[0]
        .segments
        .iter()
        .find(|segment| segment.incarnation == incarnation)
        .expect("the manifest records the stream");
    assert!(!segment.is_open, "the stream is closed");
    segment.end_seq
}

pub(crate) async fn wait_for_status(
    standby: &HotStandby,
    description: &str,
    is_reached: impl Fn(&HotStandbyStatus) -> bool,
) -> HotStandbyStatus {
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    loop {
        let status = standby.status();
        if is_reached(&status) {
            return status;
        }
        assert!(Instant::now() < deadline, "{description}: {status:?}");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_reaches_the_next_incarnation_past_a_late_write_beyond_the_recorded_end() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let mut cluster = TestCluster::new();
    let first_config = cluster.config(rocksdb_store(&serving_directory));
    let first = cluster
        .start_with(&first_config)
        .await
        .expect("incarnation 1 starts");
    let mut standby_config = cluster.config(LocalStoreConfig::InMemory);
    standby_config.node.incarnation = 2;
    let standby = start_standby(
        &cluster,
        standby_config,
        HotStandbyConfig {
            manifest_refresh_interval: Duration::from_secs(10),
            stalled_seq_deletion_check_after: Duration::from_secs(20),
            ..fast_standby_config()
        },
    );
    first.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    // Right after a refresh, the next one is about 10 seconds away.
    let manifest_reads = standby.object_store.manifest_reads();
    while standby.object_store.manifest_reads() == manifest_reads {
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    first.node.shutdown().await.expect("lease released");
    let mut second_config = first_config;
    second_config.node.incarnation = 2;
    let second = cluster
        .start_with(&second_config)
        .await
        .expect("incarnation 2 starts");
    // A fenced writer's late PUT lands after the new incarnation listed and closed the stream.
    let end_seq = recorded_end_seq(&cluster, 1).await;
    let stream = stream_of(&cluster, 1).await;
    let late = encode_batch(&BatchContent {
        stream: stream.clone(),
        epoch: 1,
        seq: end_seq + 1,
        sealed_at_unix_micros: 0,
        records: vec![BatchRecord {
            partition: 0,
            bytes: Bytes::from_static(b"never acknowledged"),
        }],
        epoch_start_writer: None,
    })
    .expect("encode the late batch");
    cluster
        .object_store
        .put(
            &stream.batch_key(end_seq + 1),
            late,
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the late batch lands");
    tokio::time::sleep(Duration::from_secs(30)).await;

    let status = standby.standby.status();
    assert_not_failed(&status);
    assert_eq!(
        status.last_error, None,
        "the standby moved on without a restart"
    );
    assert_eq!(status.lag.expect("the standby follows").incarnation, 2);
    second.commit_prompt(FIRST_FLOW, 1).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_eq!(applied_flows(&standby.applier).len(), 3);
    assert_wrote_nothing(&standby);
    standby.standby.shutdown().await;
    second.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_sets_its_store_aside_after_applying_a_batch_past_the_recorded_end() {
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
    let standby = start_standby(
        &cluster,
        standby_config,
        HotStandbyConfig {
            manifest_refresh_interval: Duration::from_secs(10),
            stalled_seq_deletion_check_after: Duration::from_secs(20),
            ..fast_standby_config()
        },
    );
    first.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let manifest_reads = standby.object_store.manifest_reads();
    while standby.object_store.manifest_reads() == manifest_reads {
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    first.node.shutdown().await.expect("lease released");
    let mut second_config = first_config;
    second_config.node.incarnation = 2;
    let second = cluster
        .start_with(&second_config)
        .await
        .expect("incarnation 2 starts");
    let applied_before = standby.applier.applied_commands().len();
    write_a_repeated_command_past_the_recorded_end(&cluster, LateBatchEpoch::Fencing).await;
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    while standby.applier.applied_commands().len() == applied_before {
        assert!(
            Instant::now() < deadline,
            "the standby did not apply the late batch"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    while set_aside_copies(&standby_directory).is_empty() {
        assert!(
            Instant::now() < deadline,
            "the standby kept its diverged store"
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    }

    second.commit_prompt(FIRST_FLOW, 1).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_wrote_nothing(&standby);
    standby.standby.shutdown().await;
    second.node.shutdown().await.expect("lease released");
    assert_same_stores(&serving_directory, &standby_directory);
}

#[tokio::test(start_paused = true)]
async fn a_node_start_sets_aside_a_store_that_applied_a_batch_past_the_recorded_end() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let mut cluster = TestCluster::new();
    let first_config = cluster.config(rocksdb_store(&directory.path().join("serving")));
    let first = cluster
        .start_with(&first_config)
        .await
        .expect("incarnation 1 starts");
    let standby_directory = directory.path().join("standby");
    let mut standby_config = cluster.config(rocksdb_store(&standby_directory));
    standby_config.node.incarnation = 2;
    // The standby never rereads the manifest, so it keeps the diverged store.
    let standby = start_standby(
        &cluster,
        standby_config.clone(),
        HotStandbyConfig {
            manifest_refresh_interval: Duration::from_secs(600),
            stalled_seq_deletion_check_after: Duration::from_secs(1200),
            ..fast_standby_config()
        },
    );
    first.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    first.node.shutdown().await.expect("lease released");
    let mut second_config = first_config;
    second_config.node.incarnation = 2;
    let second = cluster
        .start_with(&second_config)
        .await
        .expect("incarnation 2 starts");
    let applied_before = standby.applier.applied_commands().len();
    write_a_repeated_command_past_the_recorded_end(&cluster, LateBatchEpoch::Fencing).await;
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    while standby.applier.applied_commands().len() == applied_before {
        assert!(
            Instant::now() < deadline,
            "the standby did not apply the late batch"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert!(
        standby.standby.shutdown().await,
        "the standby's store closed"
    );
    second.commit_prompt(FIRST_FLOW, 1).await;
    second
        .node
        .take_snapshot()
        .await
        .expect("the snapshot succeeds");
    second.commit_prompt(FIRST_FLOW, 1).await;
    second.node.shutdown().await.expect("lease released");

    let node = cluster
        .start_with(&standby_config)
        .await
        .expect("a node starts on the diverged store's directory");
    assert_eq!(
        set_aside_copies(&standby_directory).len(),
        1,
        "the start set the diverged store aside"
    );
    assert_eq!(
        node.committed_count(FIRST_FLOW).await,
        4,
        "every acknowledged write survives, and the repeated command does not"
    );
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_dropped_hot_standby_reports_stopped() {
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
    let reader = standby.standby.status_reader();
    drop(standby);
    assert_eq!(reader.status().state, HotStandbyState::Stopped);
    serving.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_node_start_reuses_a_store_whose_positions_end_exactly_at_the_recorded_end() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let node_directory = directory.path().join("node");
    let mut cluster = TestCluster::new();
    let first_config = cluster.config(rocksdb_store(&node_directory));
    let first = cluster
        .start_with(&first_config)
        .await
        .expect("incarnation 1 starts");
    first.commit_prompt(FIRST_FLOW, 3).await;
    first.node.shutdown().await.expect("lease released");
    // Incarnation 2 closes the first stream, replays it, and fails at its own first batch.
    let mut second_config = first_config;
    second_config.node.incarnation = 2;
    let second_stream = StreamIdentity {
        cluster_id: cluster_id(&cluster).await,
        node_id: "node-a".to_owned(),
        incarnation: 2,
    };
    cluster
        .faults
        .fail_puts_of_after(&second_stream.batch_key(1), 0);
    let failed = tokio::time::timeout(Duration::from_secs(300), cluster.start_with(&second_config))
        .await
        .expect("the failing start ends");
    assert!(failed.is_err(), "the start fails at its first batch");
    cluster.faults.clear();
    let end_seq = recorded_end_seq(&cluster, 1).await;
    {
        let store = RocksDbLocalStore::open(&node_directory, RocksDbStoreOptions::default())
            .expect("the closed store opens");
        for meta in store.partition_metas().expect("read the partition metas") {
            let applied = meta.applied.expect("every partition applied a batch");
            assert_eq!(
                (applied.incarnation, applied.seq),
                (1, end_seq),
                "partition {} ends at the recorded end",
                meta.partition
            );
        }
    }

    let log_reads = |cluster: &TestCluster| {
        cluster
            .faults
            .count_operations(ObjectStoreOperation::Get, |key| key.starts_with(LOG_PREFIX))
    };
    let reads_before = log_reads(&cluster);
    let node = cluster
        .start_with(&second_config)
        .await
        .expect("incarnation 2 starts from the store");
    assert!(
        set_aside_copies(&node_directory).is_empty(),
        "a store at the recorded end is reused"
    );
    let replayed_reads = log_reads(&cluster) - reads_before;
    assert!(
        replayed_reads <= 2,
        "the start read {replayed_reads} log batches"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 3);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_node_starts_from_a_hot_standby_store_with_little_replay() {
    start_from_a_hot_standby_store(false).await;
}

#[tokio::test(start_paused = true)]
async fn a_node_starts_from_a_hot_standby_store_that_followed_a_new_incarnation() {
    start_from_a_hot_standby_store(true).await;
}

/// The standby's closed store must let a start reuse it and replay only a few batches.
async fn start_from_a_hot_standby_store(is_changing_incarnation: bool) {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let mut cluster = TestCluster::new();
    let mut serving_config = cluster.config(rocksdb_store(&serving_directory));
    let mut serving = cluster
        .start_with(&serving_config)
        .await
        .expect("the serving node starts");
    let incarnation = if is_changing_incarnation { 2 } else { 1 };
    let mut standby_config = cluster.config(rocksdb_store(&standby_directory));
    standby_config.node.incarnation = incarnation;
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    let second_flow = serving.flow_in_other_partition(&[FIRST_FLOW]);
    commit_to_both_flows(&serving, &second_flow, 3).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    if is_changing_incarnation {
        serving.node.shutdown().await.expect("lease released");
        serving_config.node.incarnation = 2;
        serving = cluster
            .start_with(&serving_config)
            .await
            .expect("incarnation 2 starts");
        commit_to_both_flows(&serving, &second_flow, 2).await;
        wait_until_caught_up(&cluster, &standby.standby).await;
    }
    assert_wrote_nothing(&standby);
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");

    let log_reads = |cluster: &TestCluster| {
        cluster
            .faults
            .count_operations(ObjectStoreOperation::Get, |key| key.starts_with(LOG_PREFIX))
    };
    let reads_before = log_reads(&cluster);
    let node = cluster
        .start_with(&standby_config)
        .await
        .expect("a node starts from the standby's store");
    let replayed_reads = log_reads(&cluster) - reads_before;
    assert!(
        replayed_reads <= 1,
        "the start read {replayed_reads} log batches"
    );
    assert!(
        set_aside_copies(&standby_directory).is_empty(),
        "the start reused the standby's store"
    );
    let expected = if is_changing_incarnation { 5 } else { 3 };
    assert_eq!(node.committed_count(FIRST_FLOW).await, expected);
    assert_eq!(node.committed_count(&second_flow).await, expected);
    node.node.shutdown().await.expect("lease released");
}

/// Starts a standby whose first stream, closed in the manifest, lacks seq 4.
async fn follow_a_closed_stream_with_a_missing_batch(
    cluster: &mut TestCluster,
    standby_config: HotStandbyConfig,
) -> (TestStandby, TestNode) {
    let first_config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster
        .start_with(&first_config)
        .await
        .expect("incarnation 1 starts");
    first.commit_prompt(FIRST_FLOW, 6).await;
    first.node.shutdown().await.expect("lease released");
    let mut second_config = cluster.config(LocalStoreConfig::InMemory);
    second_config.node.incarnation = 2;
    let second = cluster
        .start_with(&second_config)
        .await
        .expect("incarnation 2 starts");
    let stream = stream_of(cluster, 1).await;
    cluster
        .object_store
        .delete(&stream.batch_key(4))
        .await
        .expect("delete seq 4 of the closed stream");
    let mut node_config = cluster.config(LocalStoreConfig::InMemory);
    node_config.node.incarnation = 2;
    let standby = start_standby(cluster, node_config, standby_config);
    (standby, second)
}

/// Pacing that rereads the manifest every 2 seconds and checks a stalled seq after 3.
fn hole_checking_standby_config() -> HotStandbyConfig {
    HotStandbyConfig {
        manifest_refresh_interval: Duration::from_secs(2),
        stalled_seq_deletion_check_after: Duration::from_secs(3),
        ..fast_standby_config()
    }
}

fn is_waiting_at_the_missing_batch(status: &HotStandbyStatus) -> bool {
    status
        .lag
        .is_some_and(|lag| lag.incarnation == 1 && lag.next_seq == 4)
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_keeps_waiting_at_a_closed_stream_hole_until_a_newer_manifest_records_it() {
    let mut cluster = TestCluster::new();
    let (standby, serving) =
        follow_a_closed_stream_with_a_missing_batch(&mut cluster, hole_checking_standby_config())
            .await;
    standby.object_store.fail_log_reads(true);
    wait_for_status(&standby.standby, "the standby follows", |status| {
        status.lag.is_some()
    })
    .await;
    // A refresh read that starts before the wait and arrives after it is not newer than the wait.
    standby
        .object_store
        .delay_next_manifest_read(Duration::from_millis(1_500));
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    while standby.object_store.delayed_manifest_reads() == 0 {
        assert!(
            Instant::now() < deadline,
            "the standby did not reread the manifest"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    standby.object_store.fail_log_reads(false);
    wait_for_status(
        &standby.standby,
        "the standby reaches the missing batch",
        is_waiting_at_the_missing_batch,
    )
    .await;
    tokio::time::sleep(Duration::from_secs(12)).await;
    let waiting = standby.standby.status();
    assert!(is_waiting_at_the_missing_batch(&waiting), "{waiting:?}");
    assert_eq!(waiting.last_error, None);
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restarts_when_a_newer_manifest_still_records_a_missing_closed_seq() {
    let mut cluster = TestCluster::new();
    let (standby, serving) =
        follow_a_closed_stream_with_a_missing_batch(&mut cluster, hole_checking_standby_config())
            .await;
    let restarted = wait_for_status(
        &standby.standby,
        "the standby reports the missing batch",
        |status| {
            status.last_error.as_deref().is_some_and(|error| {
                error.contains("closed log stream of incarnation 1 has no batch at seq 4")
            })
        },
    )
    .await;
    assert_not_failed(&restarted);
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_reports_behind_at_a_hole_that_stays_open() {
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
    let (incarnation, last_seq) = last_log_position(&cluster).await;
    let hole_seq = last_seq + 1;
    let stream = stream_of(&cluster, incarnation).await;
    cluster
        .faults
        .fail_puts_of_after(&stream.batch_key(hole_seq), 0);
    let held_flow = "held-in-the-hole";
    let landed_flow = serving.flow_in_other_partition(&[held_flow]);
    let (_, _held) = serving
        .submit(held_flow, CommitUrgency::Prompt)
        .expect("submit the held command");
    let (_, _landed) = serving
        .submit(&landed_flow, CommitUrgency::Prompt)
        .expect("submit the landed command");
    tokio::time::sleep(BEHIND_LAG_THRESHOLD + Duration::from_secs(2)).await;
    let status = standby.standby.status();
    assert_eq!(status.state, HotStandbyState::Behind, "{status:?}");
    let lag = status.lag.expect("the standby follows");
    assert_eq!(lag.next_seq, hole_seq);
    assert!(lag.batches >= 1, "{lag:?}");
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
    cluster.faults.clear();
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_backs_off_further_while_a_slow_error_recurs_without_progress() {
    let mut cluster = TestCluster::new();
    // Each period follows for about 90 seconds before the missing seq ends it.
    let (standby, serving) = follow_a_closed_stream_with_a_missing_batch(
        &mut cluster,
        HotStandbyConfig {
            manifest_refresh_interval: Duration::from_secs(2),
            stalled_seq_deletion_check_after: Duration::from_secs(90),
            ..fast_standby_config()
        },
    )
    .await;
    let mut restart_delays = Vec::new();
    for _ in 0..3 {
        let restarting = wait_for_slow_status(&standby.standby, "the standby restarts", |status| {
            status.state == HotStandbyState::Restarting && status.restart_in.is_some()
        })
        .await;
        assert_eq!(restarting.lag, None);
        assert!(restarting.last_error.is_some(), "{restarting:?}");
        restart_delays.push(restarting.restart_in.expect("the restart delay runs"));
        wait_for_slow_status(&standby.standby, "the standby follows again", |status| {
            status.lag.is_some()
        })
        .await;
        tokio::time::sleep(Duration::from_secs(70)).await;
        let following = standby.standby.status();
        assert!(
            following.last_error.is_some(),
            "following without progress kept the last error: {following:?}"
        );
    }
    // Restart delays double from 1 second, each within 20 percent.
    assert!(
        restart_delays[2] > restart_delays[0] * 2,
        "the restart delays grew: {restart_delays:?}"
    );
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

/// Waits like [`wait_for_status`], within a deadline that spans a slow following period.
async fn wait_for_slow_status(
    standby: &HotStandby,
    description: &str,
    is_reached: impl Fn(&HotStandbyStatus) -> bool,
) -> HotStandbyStatus {
    let deadline = Instant::now() + Duration::from_secs(600);
    loop {
        let status = standby.status();
        if is_reached(&status) {
            return status;
        }
        assert!(Instant::now() < deadline, "{description}: {status:?}");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_retries_a_store_directory_another_handle_holds_and_follows_once_it_closes() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let standby_directory = directory.path().join("standby");
    // A lingering handle, such as one of a node that just stepped down, holds the directory.
    let holder = RocksDbLocalStore::open(&standby_directory, RocksDbStoreOptions::default())
        .expect("the holder opens the directory");
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    serving.commit_prompt(FIRST_FLOW, 2).await;
    let standby = start_standby(
        &cluster,
        cluster.config(rocksdb_store(&standby_directory)),
        fast_standby_config(),
    );
    let retrying = wait_for_status(&standby.standby, "the standby retries", |status| {
        status.state == HotStandbyState::Restarting
    })
    .await;
    assert!(
        retrying
            .last_error
            .as_deref()
            .is_some_and(|error| error
                .contains("a store this process opened still holds the directory's lock")),
        "the lock probe, not a failed open, found this process's handle: {retrying:?}"
    );
    tokio::time::sleep(Duration::from_secs(10)).await;
    assert_not_failed(&standby.standby.status());

    drop(holder);
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_eq!(applied_flows(&standby.applier).len(), 2);
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

/// Environment variable that makes [`hold_a_store_directory_for_another_test`] hold a store.
const HELD_DIRECTORY_VARIABLE: &str = "DEXZD_TEST_HELD_STORE_DIRECTORY";

/// Holds the store in the directory that [`HELD_DIRECTORY_VARIABLE`] names until standard input
/// closes; another test runs it as a second process. Without the variable it does nothing.
#[test]
fn hold_a_store_directory_for_another_test() {
    let Some(directory) = std::env::var_os(HELD_DIRECTORY_VARIABLE) else {
        return;
    };
    let _store = RocksDbLocalStore::open(PathBuf::from(directory), RocksDbStoreOptions::default())
        .expect("the holding process opens the store");
    println!("held");
    std::io::stdout().flush().expect("flush the held line");
    let mut rest = String::new();
    std::io::stdin()
        .read_to_string(&mut rest)
        .expect("wait for standard input to close");
}

#[tokio::test]
async fn a_hot_standby_fails_on_a_directory_another_process_holds_and_leaves_its_log_alone() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let standby_directory = directory.path().join("standby");
    let mut holder = std::process::Command::new(std::env::current_exe().expect("test binary"))
        .args([
            "--exact",
            "hot_standby::hold_a_store_directory_for_another_test",
            "--nocapture",
        ])
        .env(HELD_DIRECTORY_VARIABLE, &standby_directory)
        .stdin(std::process::Stdio::piped())
        .stdout(std::process::Stdio::piped())
        .spawn()
        .expect("start the holding process");
    let mut held = String::new();
    let mut holder_output =
        std::io::BufReader::new(holder.stdout.take().expect("the holder's output"));
    while !held.contains("held") {
        held.clear();
        let read = std::io::BufRead::read_line(&mut holder_output, &mut held)
            .expect("read the holder's output");
        assert!(
            read > 0,
            "the holding process exited before it held the store"
        );
    }
    let files_before = directory_files(&standby_directory);
    let log_before = std::fs::read(standby_directory.join("LOG")).expect("read the holder's LOG");

    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby = start_standby(
        &cluster,
        cluster.config(rocksdb_store(&standby_directory)),
        fast_standby_config(),
    );
    let failed = wait_for_status(&standby.standby, "the standby fails", |status| {
        status.state == HotStandbyState::Failed
    })
    .await;
    assert!(
        failed.failure.as_deref().is_some_and(|error| matches!(
            error,
            EngineStartError::InvalidConfig { detail } if detail.contains("another process")
        )),
        "{failed:?}"
    );
    assert_eq!(
        directory_files(&standby_directory),
        files_before,
        "the standby renamed none of the holder's files"
    );
    assert_eq!(
        std::fs::read(standby_directory.join("LOG")).expect("read the holder's LOG"),
        log_before
    );
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
    drop(holder.stdin.take());
    holder.wait().expect("the holding process exits");
}

/// Returns the names of the files in `directory`.
fn directory_files(directory: &Path) -> Vec<String> {
    let mut files: Vec<String> = std::fs::read_dir(directory)
        .expect("list the directory")
        .map(|entry| {
            entry
                .expect("read a directory entry")
                .file_name()
                .to_string_lossy()
                .into_owned()
        })
        .collect();
    files.sort();
    files
}

#[tokio::test(start_paused = true)]
async fn a_standby_reopens_the_directory_of_a_stepped_down_node_once_its_store_closes() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let node_directory = directory.path().join("node");
    let mut cluster = TestCluster::new();
    let node_config = cluster.config(rocksdb_store(&node_directory));
    let serving = cluster
        .start_with(&node_config)
        .await
        .expect("the serving node starts");
    serving.commit_prompt(FIRST_FLOW, 2).await;
    let waiter = serving.node.local_store_close_waiter();
    serving.node.shutdown().await.expect("lease released");
    assert!(
        waiter.wait_until_closed(Duration::from_secs(5)).await,
        "the stepped-down node's store closed"
    );

    let standby = start_standby(&cluster, node_config, fast_standby_config());
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_eq!(
        standby.standby.status().last_error,
        None,
        "the standby opened the directory at once"
    );
    let standby_waiter = standby.standby.local_store_close_waiter();
    let lingering = standby
        .standby
        .local_store_for_test()
        .expect("the following standby has a store");
    assert!(
        !standby.standby.shutdown().await,
        "a lingering handle keeps the store open"
    );
    assert!(
        !standby_waiter
            .wait_until_closed(Duration::from_secs(1))
            .await
    );
    drop(lingering);
    assert!(standby_waiter.wait_until_closed(Duration::ZERO).await);
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_fails_when_the_cluster_needs_a_newer_behavior_version() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    serving.node.shutdown().await.expect("lease released");
    dexzd_cluster_metadata::update_manifest(
        cluster.object_store.as_ref(),
        cluster.clock.as_ref(),
        |manifest| {
            manifest
                .version_barrier
                .get_or_insert_default()
                .behavior_version = 99;
            Ok(dexzd_cluster_metadata::ManifestChange::Changed)
        },
    )
    .await
    .expect("a newer binary raises the barrier");
    let standby = start_standby(
        &cluster,
        cluster.config(LocalStoreConfig::InMemory),
        fast_standby_config(),
    );
    let failed = wait_for_status(&standby.standby, "the standby fails", |status| {
        status.state == HotStandbyState::Failed
    })
    .await;
    assert!(
        matches!(
            failed.failure.as_deref(),
            Some(EngineStartError::UnsupportedBehaviorVersion { barrier: 99, .. })
        ),
        "{failed:?}"
    );
    standby.standby.shutdown().await;
}

/// The epoch of a late batch past the recorded end of a stream.
pub(crate) enum LateBatchEpoch {
    /// The epoch of the batch that fences the stream's end, so a reader accepts it.
    Fencing,
    /// Epoch 1, which the fencing batch voids.
    First,
}

/// Writes a batch past the recorded end of incarnation 1's stream. It repeats the stream's last
/// command, so a store that applies it diverges from the log.
///
/// Returns the recorded end seq.
pub(crate) async fn write_a_repeated_command_past_the_recorded_end(
    cluster: &TestCluster,
    epoch: LateBatchEpoch,
) -> u64 {
    let end_seq = recorded_end_seq(cluster, 1).await;
    let stream = stream_of(cluster, 1).await;
    let mut last_command_seq = end_seq;
    let mut fencing_epoch = None;
    let last = loop {
        let batch = cluster
            .object_store
            .get(&stream.batch_key(last_command_seq))
            .await
            .expect("read a batch")
            .expect("the batch exists");
        let batch = decode_batch(&batch.bytes).expect("decode the batch");
        fencing_epoch.get_or_insert(batch.header.epoch);
        if !batch.records.is_empty() {
            break batch;
        }
        last_command_seq -= 1;
    };
    let late = encode_batch(&BatchContent {
        stream: stream.clone(),
        epoch: match epoch {
            LateBatchEpoch::Fencing => fencing_epoch.expect("the stream's end batch was read"),
            LateBatchEpoch::First => 1,
        },
        seq: end_seq + 1,
        sealed_at_unix_micros: last.header.sealed_at_unix_micros,
        records: last.records,
        epoch_start_writer: None,
    })
    .expect("encode the late batch");
    cluster
        .object_store
        .put(
            &stream.batch_key(end_seq + 1),
            late,
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the late batch lands");
    end_seq
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_clears_its_last_error_once_it_reaches_the_end_of_an_idle_log() {
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
    let probe = standby.standby.probe_for_test();
    for _ in 0..3 {
        probe.fail_store_writes_for_test();
        let restarting = wait_for_status(&standby.standby, "the standby restarts", |status| {
            status.state == HotStandbyState::Restarting && status.restart_in.is_some()
        })
        .await;
        assert!(restarting.last_error.is_some(), "{restarting:?}");
        // Reaching the end of the idle log resets the delay to about 1 second.
        let restart_in = restarting.restart_in.expect("the restart delay runs");
        assert!(
            restart_in <= Duration::from_millis(1_200),
            "the restart delay grew to {restart_in:?}"
        );
        wait_for_status(
            &standby.standby,
            "the standby clears its last error at the log end",
            |status| status.lag.is_some() && status.last_error.is_none(),
        )
        .await;
    }
    assert_wrote_nothing(&standby);
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}
