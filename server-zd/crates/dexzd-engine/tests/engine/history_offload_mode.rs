// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The history offload mode survives snapshot restore, log replay, and hot standby following.

use std::path::Path;
use std::sync::Arc;

use dexzd_apply::FlowStateReader;
use dexzd_engine::{
    DeterministicCommandApplier, EngineNode, HotStandby, HotStandbyDependencies, LocalStoreConfig,
    SystemEngineIdSource,
};
use dexzd_keyspace::row_keys::history_offload_mode_key;
use dexzd_keyspace::{ColumnFamily, PartitionHasher, PartitionId};
use dexzd_protocol::dexzd::v1::{
    HistoryOffloadModeRecord, SetHistoryOffloadModeCommand, flow_command::Command,
};
use dexzd_storage::{LocalReadView, RocksDbLocalStore, RocksDbStoreOptions};
use prost::Message;

use crate::harness::{PARTITION_COUNT, TestCluster};
use crate::hot_standby::{
    ProcessObjectStore, assert_same_stores, fast_standby_config, rocksdb_store,
    wait_until_caught_up,
};
use crate::search_outbox::submit_partition_command;
use crate::snapshots::start_deterministic_node;

#[tokio::test(start_paused = true)]
async fn history_offload_modes_recover_exactly_from_a_snapshot_and_the_log() {
    let cluster = TestCluster::new();
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.commit.debug_replay_checks = true;
    let (first, _, hasher, _) = start_deterministic_node(&cluster, &config).await;
    set_every_mode(&first, &hasher, true).await;
    first.take_snapshot().await.expect("snapshot succeeds");
    set_mode(&first, &hasher, PartitionId(0), false).await;
    set_mode(&first, &hasher, PartitionId(1), true).await;
    let continuous = first
        .committer()
        .committed_view()
        .expect("continuous state view");
    assert_eq!(enabled_modes(&continuous), expected_modes(&[0]));
    drop(first);

    let (second, _, _, _) = start_deterministic_node(&cluster, &config).await;
    let recovered = second
        .committer()
        .committed_view()
        .expect("recovered state view");
    dexzd_storage::compare_store_views(
        &continuous,
        &recovered,
        &[
            dexzd_storage::CanonicalizedStoreField::NodeLineageId,
            dexzd_storage::CanonicalizedStoreField::NodeLastSnapshotNumber,
            dexzd_storage::CanonicalizedStoreField::PartitionApplied,
            dexzd_storage::CanonicalizedStoreField::PartitionAppliedEpoch,
        ],
    )
    .unwrap_or_else(|error| panic!("recovery differs from live apply: {error}"));
    assert_eq!(enabled_modes(&recovered), expected_modes(&[0]));
    second.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restores_and_replays_history_offload_modes() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let cluster = TestCluster::new();
    let (serving, _, hasher, _) =
        start_deterministic_node(&cluster, &cluster.config(rocksdb_store(&serving_directory)))
            .await;
    set_every_mode(&serving, &hasher, true).await;
    serving.take_snapshot().await.expect("snapshot succeeds");
    let standby = start_applying_standby(&cluster, &standby_directory);
    set_mode(&serving, &hasher, PartitionId(0), false).await;
    set_mode(&serving, &hasher, PartitionId(1), true).await;
    set_mode(&serving, &hasher, PartitionId(1), false).await;
    wait_until_caught_up(&cluster, &standby).await;

    serving.shutdown().await.expect("lease released");
    standby.shutdown().await;
    assert_same_stores(&serving_directory, &standby_directory);
    let standby_store = RocksDbLocalStore::open(&standby_directory, RocksDbStoreOptions::default())
        .expect("the standby store opens");
    assert_eq!(
        stored_enabled_modes(&standby_store),
        expected_modes(&[0, 1])
    );
}

/// Starts a hot standby that applies commands with the real state machine.
fn start_applying_standby(cluster: &TestCluster, directory: &Path) -> HotStandby {
    HotStandby::start(
        cluster.config(rocksdb_store(directory)),
        fast_standby_config(),
        HotStandbyDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: Arc::new(ProcessObjectStore::new(cluster.object_store.clone())),
            clock: cluster.clock.clone(),
            applier: Arc::new(DeterministicCommandApplier),
            id_source: Arc::new(SystemEngineIdSource),
            restart_jitter_seed: 7,
        },
    )
    .expect("the hot standby starts")
}

async fn set_every_mode(node: &EngineNode, hasher: &PartitionHasher, is_enabled: bool) {
    for partition in 0..PARTITION_COUNT {
        let partition = PartitionId(u16::try_from(partition).expect("few partitions"));
        set_mode(node, hasher, partition, is_enabled).await;
    }
}

async fn set_mode(
    node: &EngineNode,
    hasher: &PartitionHasher,
    partition: PartitionId,
    is_enabled: bool,
) {
    submit_partition_command(
        node,
        hasher,
        partition,
        Command::SetHistoryOffloadMode(SetHistoryOffloadModeCommand {
            enabled: is_enabled,
            partition: u32::from(partition.0),
        }),
    )
    .await;
}

/// Returns whether each partition of `view` stores an enabled mode.
fn enabled_modes(view: &dyn FlowStateReader) -> Vec<bool> {
    enabled_modes_from(|key| {
        view.get(ColumnFamily::PartitionIndex, key)
            .expect("read the mode")
    })
}

/// Returns whether each partition of the closed `store` stores an enabled mode.
fn stored_enabled_modes(store: &RocksDbLocalStore) -> Vec<bool> {
    enabled_modes_from(|key| {
        store
            .get(ColumnFamily::PartitionIndex, key)
            .expect("read the mode")
    })
}

/// Returns whether each partition stores an enabled mode; an absent record means disabled.
fn enabled_modes_from(read_row: impl Fn(&[u8]) -> Option<Vec<u8>>) -> Vec<bool> {
    (0..PARTITION_COUNT)
        .map(|partition| {
            let partition = PartitionId(u16::try_from(partition).expect("few partitions"));
            read_row(&history_offload_mode_key(partition))
                .map(|bytes| {
                    HistoryOffloadModeRecord::decode(bytes.as_slice())
                        .expect("the mode decodes")
                        .enabled
                })
                .unwrap_or(false)
        })
        .collect()
}

fn expected_modes(disabled_partitions: &[u32]) -> Vec<bool> {
    (0..PARTITION_COUNT)
        .map(|partition| !disabled_partitions.contains(&partition))
        .collect()
}
