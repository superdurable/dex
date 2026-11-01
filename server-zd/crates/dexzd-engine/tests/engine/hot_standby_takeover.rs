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
    CommitUrgency, EngineCommandAndEffectHooks, EngineNode, EngineNodeConfig,
    EngineNodeDependencies, EngineServices, EngineStartError, HotStandby, HotStandbyState,
    HotStandbyTakeover, LocalStoreConfig, SystemEngineIdSource,
};
use dexzd_log::{StreamIdentity, decode_batch};
use dexzd_object_store::{ObjectStore, ObjectStoreOperation};
use dexzd_storage::{LocalReadView, RocksDbLocalStore, RocksDbStoreOptions};
use tokio::sync::watch;
use tokio::time::Instant;

use crate::harness::{CountingApplier, RecordingCommandAndEffectHooks, TestCluster, TestNode};
use crate::hot_standby::{
    FIRST_FLOW, LOG_PREFIX, ProcessObjectStore, applied_flows, commit_to_both_flows,
    fast_standby_config, first_log_seq, last_log_position, rocksdb_store, set_aside_copies,
    start_standby, wait_until_caught_up,
};

/// Longest wait for a takeover to read the held lease twice.
const LEASE_READ_DEADLINE: Duration = Duration::from_secs(30);

/// A takeover's own object storage view, applier, and result.
struct Takeover {
    result: Result<HotStandbyTakeover, EngineStartError>,
    object_store: Arc<ProcessObjectStore>,
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
        let recording_hooks = Arc::new(RecordingCommandAndEffectHooks::default());
        let factory_recording_hooks = recording_hooks.clone();
        let result = EngineNode::take_over_from_hot_standby_until_cancelled(
            &config,
            EngineNodeDependencies {
                metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
                object_store: task_object_store.clone(),
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
            object_store: task_object_store,
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
    let (_, takeover) = take_over(cluster, config, standby, cancellation);
    let Takeover {
        result,
        object_store,
        applier,
        recording_hooks,
    } = takeover.await;
    let node = match result.expect("the takeover succeeds") {
        HotStandbyTakeover::Serving(node) => node,
        HotStandbyTakeover::Cancelled(_) => panic!("an uncancelled takeover serves"),
        _ => panic!("unexpected takeover outcome"),
    };
    let test_node = TestNode {
        node,
        applier,
        recording_hooks,
        hasher: cluster.hasher.clone(),
        lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
    };
    (test_node, object_store)
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
async fn a_takeover_from_a_warm_store_reads_almost_none_of_the_log() {
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
    let takeover_epoch = recorded_epoch(&cluster).await;
    for (_, epoch) in &positions {
        assert!(
            0 < *epoch && *epoch < takeover_epoch,
            "{epoch} vs {takeover_epoch}"
        );
    }
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
    assert_eq!(
        standby.standby.status().state,
        HotStandbyState::Bootstrapping
    );
    serving.node.shutdown().await.expect("lease released");

    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert_eq!(node.committed_count(FIRST_FLOW).await, 5);
    assert_eq!(
        applied_flows(&node.applier),
        vec![FIRST_FLOW.to_owned(); 2],
        "the start replays the commands after the snapshot"
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
        vec![FIRST_FLOW.to_owned(); 4],
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
