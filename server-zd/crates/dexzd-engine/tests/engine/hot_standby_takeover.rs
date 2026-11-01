// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;
use std::sync::Arc;
use std::time::Duration;

use dexzd_engine::{
    CommitUrgency, EngineCommandAndEffectHooks, EngineLocalStore, EngineNode, EngineNodeConfig,
    EngineNodeDependencies, EngineServices, EngineStartError, HotStandby, HotStandbyConfig,
    HotStandbyState, HotStandbyTakeover, LocalStoreConfig, SystemEngineIdSource,
};
use dexzd_log::{StreamIdentity, decode_batch};
use dexzd_object_store::{ObjectStore, ObjectStoreOperation};
use dexzd_storage::{
    InMemoryLocalStore, LocalReadView, LocalStore, RocksDbLocalStore, RocksDbStoreOptions,
};
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio::time::Instant;

use crate::harness::{
    BLOCKING_FLOW, CountingApplier, RecordingCommandAndEffectHooks, TestCluster, TestNode,
};
use crate::hot_standby::{
    FIRST_FLOW, LOG_PREFIX, LateBatchEpoch, ProcessObjectStore, TestStandby, applied_flows,
    commit_to_both_flows, fast_standby_config, first_log_seq, last_log_position, rocksdb_store,
    set_aside_copies, start_standby, wait_until_caught_up,
    write_a_repeated_command_past_the_recorded_end,
};

/// Longest wait for a takeover to read the held lease twice.
const LEASE_READ_DEADLINE: Duration = Duration::from_secs(30);

/// A takeover's applier, hooks, and result.
struct Takeover {
    result: Result<HotStandbyTakeover, EngineStartError>,
    applier: Arc<CountingApplier>,
    recording_hooks: Arc<RecordingCommandAndEffectHooks>,
}

/// Returns a takeover that owns its inputs, so a task can run it while the test acts.
fn take_over(
    cluster: &TestCluster,
    config: EngineNodeConfig,
    standby: HotStandby,
    mut cancellation: watch::Receiver<bool>,
) -> (
    Arc<ProcessObjectStore>,
    impl Future<Output = Takeover> + Send + 'static,
) {
    let object_store = Arc::new(ProcessObjectStore::new(cluster.object_store.clone()));
    let clock = cluster.clock.clone();
    let task_object_store = object_store.clone();
    let takeover = async move {
        let applier = Arc::new(CountingApplier::default());
        // The serving node replays any gated command; only a test's standby holds one.
        applier.gate.open();
        let recording_hooks = Arc::new(RecordingCommandAndEffectHooks::default());
        let factory_recording_hooks = recording_hooks.clone();
        let result = EngineNode::take_over_from_hot_standby_until_cancelled(
            &config,
            EngineNodeDependencies {
                metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
                object_store: task_object_store,
                clock,
                applier: applier.clone(),
                id_source: Arc::new(SystemEngineIdSource),
                lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
            },
            move |_services: EngineServices| EngineCommandAndEffectHooks {
                committed_commands: factory_recording_hooks.clone(),
                rebuilder: factory_recording_hooks,
            },
            standby,
            &mut cancellation,
        )
        .await;
        Takeover {
            result,
            applier,
            recording_hooks,
        }
    };
    (object_store, takeover)
}

/// Takes over without cancellation and returns the serving node.
async fn take_over_to_serving(
    cluster: &TestCluster,
    config: EngineNodeConfig,
    standby: HotStandby,
) -> (TestNode, Arc<ProcessObjectStore>) {
    let (_cancel, cancellation) = watch::channel(false);
    let (object_store, takeover) = take_over(cluster, config, standby, cancellation);
    let test_node = serving_test_node(cluster, takeover.await);
    (test_node, object_store)
}

/// Returns the node that `takeover` serves; panics unless it serves.
fn serving_test_node(cluster: &TestCluster, takeover: Takeover) -> TestNode {
    let Takeover {
        result,
        applier,
        recording_hooks,
        ..
    } = takeover;
    let node = match result.expect("the takeover succeeds") {
        HotStandbyTakeover::Serving(node) => node,
        HotStandbyTakeover::Cancelled(_) => panic!("an uncancelled takeover serves"),
        _ => panic!("unexpected takeover outcome"),
    };
    TestNode {
        node,
        applier,
        recording_hooks,
        hasher: cluster.hasher.clone(),
        lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
    }
}

async fn recorded_epoch(cluster: &TestCluster) -> u64 {
    cluster.cluster_manifest().await.nodes["node-a"].epoch
}

/// Returns the applied position and epoch of every partition in a closed RocksDB store.
fn applied_positions(directory: &std::path::Path) -> Vec<(u64, u64)> {
    let store = RocksDbLocalStore::open(directory, RocksDbStoreOptions::default())
        .expect("the closed store opens");
    store
        .partition_metas()
        .expect("read partition metas")
        .into_iter()
        .map(|meta| {
            let applied = meta
                .applied
                .expect("every partition has an applied position");
            (applied.seq, meta.applied_epoch)
        })
        .collect()
}

#[tokio::test(start_paused = true)]
async fn a_takeover_after_idle_partitions_reads_little_and_outranks_every_followed_epoch() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let standby_directory = directory.path().join("standby");
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(rocksdb_store(&standby_directory));
    let first = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    // One busy Flow leaves the other partitions idle.
    serving.commit_prompt(FIRST_FLOW, 20).await;
    wait_until_caught_up(&cluster, &first.standby).await;
    first.standby.shutdown().await;
    let (_, last_seq) = last_log_position(&cluster).await;
    let serving_epoch = recorded_epoch(&cluster).await;
    let positions = applied_positions(&standby_directory);
    assert_eq!(positions.len(), 4, "every partition has a position");
    for (seq, epoch) in &positions {
        assert_eq!(*seq, last_seq, "idle partitions advance with the busy one");
        assert_eq!(*epoch, serving_epoch);
    }

    // A restarted standby continues from its own store.
    let second = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    wait_until_caught_up(&cluster, &second.standby).await;
    assert!(applied_flows(&second.applier).is_empty());
    serving.node.shutdown().await.expect("lease released");

    let (node, object_store) = take_over_to_serving(&cluster, standby_config, second.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    let takeover_epoch = recorded_epoch(&cluster).await;
    assert!(
        object_store.log_reads() <= 3,
        "the takeover read {} log objects or listings",
        object_store.log_reads()
    );
    assert!(
        applied_flows(&node.applier).is_empty(),
        "nothing is replayed"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 20);
    node.commit_prompt(FIRST_FLOW, 1).await;
    assert_eq!(node.committed_count(FIRST_FLOW).await, 21);
    node.node.shutdown().await.expect("lease released");
    // Recovery accepted the standby's positions and moved every partition into its own epoch.
    for (seq, epoch) in applied_positions(&standby_directory) {
        assert!(
            seq > last_seq,
            "seq {seq} is not past the standby's {last_seq}"
        );
        assert_eq!(epoch, takeover_epoch);
    }
}

#[tokio::test(start_paused = true)]
async fn a_takeover_serves_the_in_memory_store_that_the_standby_hands_over() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 20).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    serving.node.shutdown().await.expect("lease released");

    // An in-memory store exists only in the standby, so a cold start would replay every batch.
    let (node, object_store) =
        take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert!(
        object_store.log_reads() <= 3,
        "the takeover read {} log objects or listings",
        object_store.log_reads()
    );
    assert!(
        applied_flows(&node.applier).is_empty(),
        "nothing is replayed"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 20);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_writes_its_epoch_start_at_the_hole_the_standby_waits_at() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let (incarnation, last_seq) = last_log_position(&cluster).await;
    let hole_seq = last_seq + 1;
    let stream = StreamIdentity {
        cluster_id: cluster_id(&cluster).await,
        node_id: "node-a".to_owned(),
        incarnation,
    };

    // The batch at the hole never lands, while the next one does.
    let held_flow = "held-in-the-hole";
    let landed_flow = serving.flow_in_other_partition(&[held_flow]);
    cluster
        .faults
        .fail_puts_of_after(&stream.batch_key(hole_seq), 0);
    let (_, _held) = serving
        .submit(held_flow, CommitUrgency::Prompt)
        .expect("submit the held command");
    let (_, _landed) = serving
        .submit(&landed_flow, CommitUrgency::Prompt)
        .expect("submit the landed command");
    let deadline = Instant::now() + Duration::from_secs(5);
    while last_log_position(&cluster).await.1 != hole_seq + 1 {
        assert!(Instant::now() < deadline, "the later batch did not land");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    tokio::time::sleep(Duration::from_secs(1)).await;
    let lag = standby.standby.status().lag.expect("the standby follows");
    assert_eq!(lag.next_seq, hole_seq, "the standby waits at the hole");
    let serving_epoch = recorded_epoch(&cluster).await;
    serving.node.shutdown().await.expect("lease released");
    cluster.faults.clear();

    let standby_applier = standby.applier.clone();
    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    let takeover_epoch = recorded_epoch(&cluster).await;
    let at_hole = read_batch(&cluster, &stream, hole_seq).await;
    assert!(
        at_hole.header.epoch_start.is_some(),
        "EpochStart fills the hole"
    );
    assert_eq!(at_hole.header.epoch, takeover_epoch);
    let after_hole = read_batch(&cluster, &stream, hole_seq + 1).await;
    assert_eq!(
        after_hole.header.epoch, serving_epoch,
        "the batch after the hole stays as written, and void"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 2);
    assert_eq!(node.committed_count(held_flow).await, 0);
    assert_eq!(node.committed_count(&landed_flow).await, 0);
    assert_eq!(
        applied_flows(&standby_applier),
        vec![FIRST_FLOW.to_owned(); 2]
    );
    assert!(applied_flows(&node.applier).is_empty());
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_from_a_store_behind_log_cleanup_restores_the_newest_snapshot() {
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
    // The standby never notices the cleanup, so only the takeover's check can find the gap.
    let standby_config = cluster.config(rocksdb_store(&standby_directory));
    let standby = start_standby(
        &cluster,
        standby_config.clone(),
        dexzd_engine::HotStandbyConfig {
            manifest_refresh_interval: Duration::from_secs(24 * 60 * 60),
            stalled_seq_deletion_check_after: Duration::from_secs(48 * 60 * 60),
            ..fast_standby_config()
        },
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
    let first_retained_seq = first_log_seq(&cluster).await;
    assert!(
        first_retained_seq > stalled_at,
        "log cleanup deleted the batches the standby needs"
    );
    commit_to_both_flows(&serving, &second_flow, 1).await;
    serving.node.shutdown().await.expect("lease released");
    assert_eq!(set_aside_copies(&standby_directory).len(), 0);

    let (node, object_store) =
        take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(!node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 7);
    assert_eq!(node.committed_count(&second_flow).await, 7);
    assert_eq!(
        set_aside_copies(&standby_directory).len(),
        1,
        "the takeover set the stale store aside and restored the newest snapshot"
    );
    for key in object_store
        .written_keys()
        .iter()
        .filter(|key| key.starts_with(LOG_PREFIX))
    {
        let seq: u64 = key
            .rsplit('/')
            .next()
            .and_then(|seq| seq.parse().ok())
            .expect("a batch key ends with its seq");
        assert!(
            seq >= first_retained_seq,
            "the takeover wrote deleted key {key}"
        );
    }
    let rewritten: Vec<(String, usize)> = cluster
        .faults
        .count_operations_per_key(ObjectStoreOperation::Put, LOG_PREFIX)
        .into_iter()
        .filter(|(_, writes)| *writes > 1)
        .collect();
    assert!(rewritten.is_empty(), "{rewritten:?}");
    node.commit_prompt(FIRST_FLOW, 1).await;
    assert_eq!(node.committed_count(FIRST_FLOW).await, 8);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_from_a_standby_without_a_store_restores_the_newest_snapshot() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    serving.commit_prompt(FIRST_FLOW, 3).await;
    serving
        .node
        .take_snapshot()
        .await
        .expect("the snapshot succeeds");
    serving.commit_prompt(FIRST_FLOW, 2).await;
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    standby.object_store.fail_all_reads(true);
    tokio::time::sleep(Duration::from_secs(2)).await;
    let without_store = standby.standby.status();
    assert!(
        matches!(
            without_store.state,
            HotStandbyState::Bootstrapping | HotStandbyState::Restarting
        ),
        "{without_store:?}"
    );
    serving.node.shutdown().await.expect("lease released");

    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(!node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 5);
    assert_eq!(
        applied_flows(&node.applier),
        vec![FIRST_FLOW.to_owned(); 2],
        "the start replays the commands after the snapshot"
    );
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_opens_the_configured_store_when_the_standby_followed_into_another() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let standby_directory = directory.path().join("standby");
    let node_directory = directory.path().join("node");
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
    serving.commit_prompt(FIRST_FLOW, 3).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    serving.node.shutdown().await.expect("lease released");

    let (node, _) = take_over_to_serving(
        &cluster,
        cluster.config(rocksdb_store(&node_directory)),
        standby.standby,
    )
    .await;
    assert!(!node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 3);
    assert_eq!(
        applied_flows(&node.applier),
        vec![FIRST_FLOW.to_owned(); 3],
        "the configured store replays the log instead of serving the standby's store"
    );
    assert!(
        std::fs::read_dir(&node_directory)
            .expect("the configured store directory exists")
            .next()
            .is_some()
    );
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn cancelling_a_takeover_before_the_lease_returns_the_following_standby() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;

    let status_reader = standby.standby.status_reader();
    let (cancel, cancellation) = watch::channel(false);
    let (takeover_store, takeover) =
        take_over(&cluster, standby_config, standby.standby, cancellation);
    let takeover = tokio::spawn(takeover);
    let deadline = Instant::now() + LEASE_READ_DEADLINE;
    while takeover_store.lease_reads() < 2 {
        assert!(!takeover.is_finished(), "the takeover waits for the lease");
        assert!(
            Instant::now() < deadline,
            "the takeover did not read the held lease"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    // While the takeover waits for the lease, the reader still reports the following standby.
    let next_seq_before = status_reader
        .status()
        .lag
        .expect("the standby follows")
        .next_seq;
    serving.commit_prompt(FIRST_FLOW, 1).await;
    let deadline = Instant::now() + LEASE_READ_DEADLINE;
    while status_reader
        .status()
        .lag
        .expect("the standby follows")
        .next_seq
        == next_seq_before
    {
        assert!(
            Instant::now() < deadline,
            "the reader did not report the standby's progress"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(status_reader.status().state, HotStandbyState::Following);
    cancel
        .send(true)
        .expect("the takeover listens for cancellation");
    let returned = match takeover
        .await
        .expect("the takeover task completes")
        .result
        .expect("a cancelled takeover succeeds")
    {
        HotStandbyTakeover::Cancelled(standby) => standby,
        HotStandbyTakeover::Serving(_) => panic!("a cancelled takeover does not serve"),
        _ => panic!("unexpected takeover outcome"),
    };
    assert_eq!(
        takeover_store.writes(),
        0,
        "the cancelled takeover wrote nothing"
    );

    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &returned).await;
    assert_eq!(
        applied_flows(&standby.applier),
        vec![FIRST_FLOW.to_owned(); 5],
        "the returned standby keeps following"
    );
    returned.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

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

async fn read_batch(
    cluster: &TestCluster,
    stream: &StreamIdentity,
    seq: u64,
) -> dexzd_log::DecodedBatch {
    let content = cluster
        .object_store
        .get(&stream.batch_key(seq))
        .await
        .expect("read the batch")
        .expect("the batch exists");
    decode_batch(&content.bytes).expect("decode the batch")
}

/// How long a real-time test waits after the lease, so a takeover is inside the handover.
const HANDOVER_START_DELAY: Duration = Duration::from_millis(300);

/// Waits until the manifest records a node epoch above `epoch`, as a takeover's lease does.
async fn wait_for_epoch_above(cluster: &TestCluster, epoch: u64) {
    let deadline = Instant::now() + LEASE_READ_DEADLINE;
    while recorded_epoch(cluster).await <= epoch {
        assert!(Instant::now() < deadline, "no process took the lease");
        tokio::time::sleep(Duration::from_millis(1)).await;
    }
}

/// Returns a future that opens the gate of `applier` after `delay`, for a task to run while the
/// test awaits what the gate holds up.
fn open_gate_after(
    applier: &Arc<CountingApplier>,
    delay: Duration,
) -> impl Future<Output = ()> + Send + 'static {
    let applier = Arc::clone(applier);
    async move {
        tokio::time::sleep(delay).await;
        applier.gate.open();
    }
}

/// Starts a node whose applier passes gated commands.
async fn start_with_open_gate(
    cluster: &mut TestCluster,
    config: &EngineNodeConfig,
) -> Result<TestNode, EngineStartError> {
    let applier = Arc::new(CountingApplier::default());
    applier.gate.open();
    cluster
        .start(
            config,
            applier,
            Arc::new(RecordingCommandAndEffectHooks::default()),
        )
        .await
}

/// Opens a gate when dropped, so a failed assertion never leaves a lane blocked forever.
struct GateOpenedOnDrop(Arc<CountingApplier>);

impl Drop for GateOpenedOnDrop {
    fn drop(&mut self) {
        self.0.gate.open();
    }
}

/// Starts a serving node and a standby whose lane holds a gated command, then stops the serving
/// node so a takeover gets the lease at once. Gated tests run in real time: paused time does not
/// advance while the gated apply blocks.
///
/// Returns the standby and a guard that opens its gate when the test ends.
async fn standby_with_a_command_mid_apply(
    cluster: &mut TestCluster,
    standby_config: EngineNodeConfig,
) -> (TestStandby, GateOpenedOnDrop) {
    let serving = start_with_open_gate(cluster, &cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby = start_standby(cluster, standby_config, fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(cluster, &standby.standby).await;
    serving.commit_prompt(BLOCKING_FLOW, 1).await;
    let gate = GateOpenedOnDrop(standby.applier.clone());
    standby.applier.gate.wait_until_entered().await;
    serving.node.shutdown().await.expect("lease released");
    (standby, gate)
}

/// Starts a takeover in a task and cancels it once it is inside the handover.
///
/// Returns the task that opens the standby's gate two seconds after the cancellation.
async fn cancel_a_takeover_mid_handover(
    cluster: &TestCluster,
    standby_config: EngineNodeConfig,
    standby: TestStandby,
) -> JoinHandle<()> {
    let epoch = recorded_epoch(cluster).await;
    let (cancel, cancellation) = watch::channel(false);
    let (_, takeover) = take_over(cluster, standby_config, standby.standby, cancellation);
    let takeover = tokio::spawn(takeover);
    // The handover waits for the gated lane, so cancellation arrives in the middle of it.
    wait_for_epoch_above(cluster, epoch).await;
    tokio::time::sleep(HANDOVER_START_DELAY).await;
    let gate_opener = tokio::spawn(open_gate_after(&standby.applier, Duration::from_secs(2)));
    cancel
        .send(true)
        .expect("the takeover listens for cancellation");
    let cancelled = takeover.await.expect("the takeover task completes").result;
    assert!(
        matches!(cancelled, Err(EngineStartError::Stopped { .. })),
        "a takeover cancelled after the lease stops"
    );
    gate_opener
}

#[tokio::test]
async fn a_start_on_the_directory_succeeds_right_after_a_takeover_is_cancelled_mid_handover() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let mut cluster = TestCluster::new();
    let standby_config = cluster.config(rocksdb_store(&directory.path().join("standby")));
    let (standby, _gate) =
        standby_with_a_command_mid_apply(&mut cluster, standby_config.clone()).await;
    let gate_opener =
        cancel_a_takeover_mid_handover(&cluster, standby_config.clone(), standby).await;

    let started = tokio::time::timeout(
        Duration::from_secs(20),
        start_with_open_gate(&mut cluster, &standby_config),
    )
    .await
    .expect("the start finishes");
    let node = started.expect("a start opens the standby's directory at once");
    assert_eq!(node.committed_count(FIRST_FLOW).await, 2);
    assert_eq!(node.committed_count(BLOCKING_FLOW).await, 1);
    node.node.shutdown().await.expect("lease released");
    gate_opener.await.expect("the gate opener completes");
}

#[tokio::test]
async fn a_new_standby_on_the_directory_follows_right_after_a_takeover_is_cancelled_mid_handover() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let mut cluster = TestCluster::new();
    let standby_config = cluster.config(rocksdb_store(&directory.path().join("standby")));
    let (standby, _gate) =
        standby_with_a_command_mid_apply(&mut cluster, standby_config.clone()).await;
    let gate_opener =
        cancel_a_takeover_mid_handover(&cluster, standby_config.clone(), standby).await;

    let next = start_standby(&cluster, standby_config, fast_standby_config());
    next.applier.gate.open();
    wait_until_caught_up(&cluster, &next.standby).await;
    assert_eq!(
        next.standby.status().last_error,
        None,
        "the new standby opened the directory without a restart"
    );
    assert!(next.standby.shutdown().await, "the standby's store closed");
    gate_opener.await.expect("the gate opener completes");
}

#[tokio::test]
async fn a_dropped_standby_fences_a_lane_mid_apply_at_once() {
    let store = Arc::new(EngineLocalStore::Memory(InMemoryLocalStore::new()));
    let mut cluster = TestCluster::new();
    let standby_config = cluster.config(LocalStoreConfig::ExistingForTest(Arc::clone(&store)));
    let (standby, _gate) = standby_with_a_command_mid_apply(&mut cluster, standby_config).await;
    let metas_before = store.partition_metas().expect("read the partition metas");
    let standby_applier = standby.applier.clone();

    drop(standby);
    standby_applier.gate.open();
    tokio::time::sleep(Duration::from_secs(1)).await;
    assert_eq!(
        store.partition_metas().expect("read the partition metas"),
        metas_before,
        "the lane wrote after its standby was dropped"
    );
}

#[tokio::test]
async fn a_standby_lane_mid_apply_at_handover_writes_nothing_after_the_node_serves() {
    let store = Arc::new(EngineLocalStore::Memory(InMemoryLocalStore::new()));
    let mut cluster = TestCluster::new();
    let standby_config = cluster.config(LocalStoreConfig::ExistingForTest(Arc::clone(&store)));
    let (standby, _gate) =
        standby_with_a_command_mid_apply(&mut cluster, standby_config.clone()).await;
    let standby_applier = standby.applier.clone();

    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(
        applied_flows(&node.applier),
        vec![BLOCKING_FLOW.to_owned()],
        "the serving node replays the frame the standby never wrote"
    );
    node.commit_prompt(BLOCKING_FLOW, 1).await;
    let takeover_epoch = recorded_epoch(&cluster).await;
    let partition = node.context(BLOCKING_FLOW).partition;

    // The standby's lane resumes behind its stopped write fence.
    standby_applier.gate.open();
    tokio::time::sleep(Duration::from_secs(1)).await;
    assert_eq!(node.committed_count(BLOCKING_FLOW).await, 2);
    let meta = store
        .partition_meta(partition)
        .expect("read the partition meta")
        .expect("the partition has a meta");
    assert_eq!(
        meta.applied_epoch, takeover_epoch,
        "the standby wrote its frame's position"
    );
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test]
async fn a_takeover_opens_the_store_cold_after_a_standby_store_write_failed_in_the_handover() {
    let mut cluster = TestCluster::new();
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let (standby, _gate) =
        standby_with_a_command_mid_apply(&mut cluster, standby_config.clone()).await;
    let standby_applier = standby.applier.clone();
    let write_fence = standby.standby.write_fence_for_test();
    let epoch = recorded_epoch(&cluster).await;
    let (_cancel, cancellation) = watch::channel(false);
    let (_, takeover) = take_over(&cluster, standby_config, standby.standby, cancellation);
    let takeover = tokio::spawn(takeover);
    wait_for_epoch_above(&cluster, epoch).await;
    tokio::time::sleep(HANDOVER_START_DELAY).await;
    // A write fails after the handover cancelled following, so only the fence records it.
    write_fence.fail_for_test();
    standby_applier.gate.open();

    let takeover = takeover.await.expect("the takeover task completes");
    let node = serving_test_node(&cluster, takeover);
    assert!(
        !node.node.is_serving_hot_standby_store_for_test(),
        "a store behind a failed write fence is not handed over"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 2);
    assert_eq!(node.committed_count(BLOCKING_FLOW).await, 1);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test]
async fn a_takeover_waits_for_a_lingering_standby_lane_before_it_reopens_the_store() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let mut cluster = TestCluster::new();
    let standby_config = cluster.config(rocksdb_store(&directory.path().join("standby")));
    let (standby, _gate) =
        standby_with_a_command_mid_apply(&mut cluster, standby_config.clone()).await;
    let standby_applier = standby.applier.clone();
    let write_fence = standby.standby.write_fence_for_test();
    let epoch = recorded_epoch(&cluster).await;
    let (_cancel, cancellation) = watch::channel(false);
    let (_, takeover) = take_over(&cluster, standby_config, standby.standby, cancellation);
    let takeover = tokio::spawn(takeover);
    wait_for_epoch_above(&cluster, epoch).await;
    tokio::time::sleep(HANDOVER_START_DELAY).await;
    write_fence.fail_for_test();
    // The handover gives up on the lane within six seconds; the lane then still holds the store.
    let gate_opener = tokio::spawn(open_gate_after(&standby_applier, Duration::from_secs(8)));

    let takeover = takeover.await.expect("the takeover task completes");
    gate_opener.await.expect("the gate opener completes");
    let node = serving_test_node(&cluster, takeover);
    assert!(!node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 2);
    assert_eq!(node.committed_count(BLOCKING_FLOW).await, 1);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_for_another_node_fails_before_the_lease_and_returns_the_standby() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let epoch = recorded_epoch(&cluster).await;

    let mut other_node_config = standby_config;
    other_node_config.node.node_id = "node-b".to_owned();
    let (_cancel, cancellation) = watch::channel(false);
    let (_, takeover) = take_over(&cluster, other_node_config, standby.standby, cancellation);
    let returned = match takeover
        .await
        .result
        .expect("the takeover reports its outcome")
    {
        HotStandbyTakeover::FailedBeforeLease { error, standby } => {
            assert!(
                matches!(error, EngineStartError::InvalidConfig { .. }),
                "{error}"
            );
            standby
        }
        HotStandbyTakeover::Serving(_) => panic!("another node's takeover must not serve"),
        _ => panic!("unexpected takeover outcome"),
    };
    assert_eq!(recorded_epoch(&cluster).await, epoch, "no lease was taken");
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &returned).await;
    assert_eq!(applied_flows(&standby.applier).len(), 4);
    returned.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_does_not_reuse_a_store_whose_positions_name_another_node() {
    let store = Arc::new(EngineLocalStore::Memory(InMemoryLocalStore::new()));
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(LocalStoreConfig::ExistingForTest(Arc::clone(&store)));
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    serving.node.shutdown().await.expect("lease released");
    for mut meta in store.partition_metas().expect("read the partition metas") {
        if let Some(applied) = meta.applied.as_mut() {
            applied.node_id = "node-z".to_owned();
        }
        store
            .put_partition_meta(&meta)
            .expect("write the partition meta");
    }

    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(
        !node.node.is_serving_hot_standby_store_for_test(),
        "a store with another node's positions is not reused"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 2);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_sets_aside_a_standby_store_that_applied_a_batch_past_the_recorded_end() {
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
    // The standby rereads the manifest only after the takeover.
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
    let deadline = Instant::now() + LEASE_READ_DEADLINE;
    while standby.applier.applied_commands().len() == applied_before {
        assert!(
            Instant::now() < deadline,
            "the standby did not apply the late batch"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    second.node.shutdown().await.expect("lease released");

    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(!node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(
        set_aside_copies(&standby_directory).len(),
        1,
        "the takeover set the diverged store aside"
    );
    assert_eq!(
        node.committed_count(FIRST_FLOW).await,
        2,
        "the repeated command is not served"
    );
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_serves_a_standby_store_that_voided_a_late_batch_past_the_recorded_end() {
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
    // The fenced writer's in-flight batch lands in its old epoch, behind the fencing batch.
    let end_seq =
        write_a_repeated_command_past_the_recorded_end(&cluster, LateBatchEpoch::First).await;
    let deadline = Instant::now() + LEASE_READ_DEADLINE;
    while standby.standby.status().lag.map(|lag| lag.next_seq) != Some(end_seq + 2) {
        assert!(
            Instant::now() < deadline,
            "the standby did not read past the late batch: {:?}",
            standby.standby.status()
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    second.node.shutdown().await.expect("lease released");

    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert!(set_aside_copies(&standby_directory).is_empty());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 2);
    node.node.shutdown().await.expect("lease released");
}
