// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hot standby recovery from object storage faults, corrupt batches, and races with retention,
//! and takeovers from a standby in every state.

use std::time::Duration;

use bytes::Bytes;
use dexzd_engine::{HotStandbyConfig, HotStandbyState, HotStandbyStatus, LocalStoreConfig};
use dexzd_object_store::{ObjectStore, WriteCondition};
use tokio::time::Instant;

use crate::harness::TestCluster;
use crate::hot_standby::{
    BEHIND_LAG_THRESHOLD, CATCH_UP_DEADLINE, FIRST_FLOW, ReadFaults, applied_flows,
    assert_not_failed, assert_same_stores, assert_wrote_nothing, commit_to_both_flows,
    fast_standby_config, rocksdb_store, start_standby, stream_of, wait_for_status,
    wait_until_caught_up,
};
use crate::hot_standby_takeover::take_over_to_serving;

/// The longest restart delay, 60 seconds, plus its 20 percent jitter.
const LONGEST_RESTART_DELAY: Duration = Duration::from_secs(72);

/// Asserts that a standby in a transient outage is not failed and waits no longer than the
/// longest restart delay.
fn assert_retrying(status: &HotStandbyStatus) {
    assert_not_failed(status);
    if let Some(restart_in) = status.restart_in {
        assert!(restart_in <= LONGEST_RESTART_DELAY, "{status:?}");
    }
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_follows_through_read_faults_and_latency_spikes_without_restarting() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(rocksdb_store(&serving_directory)))
        .await
        .expect("the serving node starts");
    let second_flow = serving.flow_in_other_partition(&[FIRST_FLOW]);
    let standby = start_standby(
        &cluster,
        cluster.config(rocksdb_store(&standby_directory)),
        fast_standby_config(),
    );
    commit_to_both_flows(&serving, &second_flow, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;

    standby.object_store.inject_read_faults(Some(ReadFaults {
        failures: 150,
        timeouts: 50,
        slow_reads: 150,
    }));
    for _ in 0..20 {
        commit_to_both_flows(&serving, &second_flow, 1).await;
        tokio::time::sleep(Duration::from_millis(500)).await;
        assert_not_failed(&standby.standby.status());
    }
    standby.object_store.inject_read_faults(None);
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_eq!(
        standby.object_store.cluster_reads(),
        1,
        "the standby bootstrapped again under transient read faults"
    );
    assert_eq!(standby.standby.status().last_error, None);
    assert_wrote_nothing(&standby);
    assert!(standby.standby.shutdown().await);
    serving.node.shutdown().await.expect("lease released");
    assert_same_stores(&serving_directory, &standby_directory);
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_keeps_retrying_through_a_long_storage_outage_and_catches_up_when_it_heals() {
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

    // Ten minutes without object storage.
    standby.object_store.fail_all_reads(true);
    for _ in 0..60 {
        serving.commit_prompt(FIRST_FLOW, 1).await;
        tokio::time::sleep(Duration::from_secs(10)).await;
        assert_retrying(&standby.standby.status());
    }
    standby.object_store.fail_all_reads(false);
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_eq!(applied_flows(&standby.applier).len(), 62);
    wait_for_status(
        &standby.standby,
        "following after the outage clears the last error",
        |status| status.last_error.is_none(),
    )
    .await;
    assert_wrote_nothing(&standby);
    standby.standby.shutdown().await;
    serving.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_retries_its_bootstrap_through_a_long_storage_outage() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    serving.commit_prompt(FIRST_FLOW, 2).await;
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    // The driver has not run yet, so even its first read fails.
    standby.object_store.fail_all_reads(true);
    let mut has_backed_off = false;
    for _ in 0..60 {
        tokio::time::sleep(Duration::from_secs(10)).await;
        let status = standby.standby.status();
        assert_retrying(&status);
        assert!(
            matches!(
                status.state,
                HotStandbyState::Bootstrapping | HotStandbyState::Restarting
            ),
            "{status:?}"
        );
        has_backed_off |= status
            .restart_in
            .is_some_and(|restart_in| restart_in > Duration::from_secs(30));
    }
    assert!(has_backed_off, "the restart delay grew during the outage");
    standby.object_store.fail_all_reads(false);
    serving.commit_prompt(FIRST_FLOW, 1).await;
    // The longest restart delay is shorter than the catch-up deadline.
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_wrote_nothing(&standby);
    serving.node.shutdown().await.expect("lease released");
    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 3);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restarts_with_backoff_at_a_corrupt_batch_and_resumes_once_it_is_repaired() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    let corrupt_seq = standby
        .standby
        .status()
        .lag
        .expect("the standby follows")
        .next_seq;
    standby.object_store.fail_log_reads(true);
    serving.commit_prompt(FIRST_FLOW, 3).await;
    let key = stream_of(&cluster, 1).await.batch_key(corrupt_seq);
    let original = cluster
        .object_store
        .get(&key)
        .await
        .expect("read the batch")
        .expect("the batch exists")
        .bytes;
    cluster
        .object_store
        .put(
            &key,
            Bytes::from_static(b"corrupt batch"),
            WriteCondition::Unconditional,
        )
        .await
        .expect("corrupt the batch");
    standby.object_store.fail_log_reads(false);

    // A restart delay that jumps up starts the next restart; it counts down otherwise.
    let mut restart_delays: Vec<Duration> = Vec::new();
    let mut last_restart_in = Duration::ZERO;
    let deadline = Instant::now() + LONGEST_RESTART_DELAY + CATCH_UP_DEADLINE;
    while restart_delays.len() < 3 {
        let status = standby.standby.status();
        assert_retrying(&status);
        if let Some(restart_in) = status.restart_in {
            assert!(status.last_error.is_some(), "{status:?}");
            if restart_in > last_restart_in {
                restart_delays.push(restart_in);
            }
            last_restart_in = restart_in;
        }
        assert!(Instant::now() < deadline, "the standby did not restart");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert!(
        restart_delays.windows(2).all(|pair| pair[1] > pair[0]),
        "no progress past the corrupt batch, so the delay grows: {restart_delays:?}"
    );

    cluster
        .object_store
        .put(&key, original, WriteCondition::Unconditional)
        .await
        .expect("repair the batch");
    let deadline = Instant::now() + LONGEST_RESTART_DELAY + CATCH_UP_DEADLINE;
    while applied_flows(&standby.applier).len() < 5 {
        assert!(Instant::now() < deadline, "the standby did not resume");
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    wait_until_caught_up(&cluster, &standby.standby).await;
    serving.node.shutdown().await.expect("lease released");
    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 5);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_restores_the_newest_snapshot_when_retention_deletes_the_one_it_reads() {
    let mut cluster = TestCluster::new();
    let mut serving_config = cluster.config(LocalStoreConfig::InMemory);
    serving_config.snapshots.deletion_delay = Duration::ZERO;
    serving_config.snapshots.retained_count = 1;
    let serving = cluster
        .start_with(&serving_config)
        .await
        .expect("the serving node starts");
    serving.commit_prompt(FIRST_FLOW, 3).await;
    serving
        .node
        .take_snapshot()
        .await
        .expect("the first snapshot succeeds");
    let first_snapshot_keys = snapshot_keys(&cluster).await;

    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    standby.object_store.hold_snapshot_reads(true);
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    while standby.object_store.held_snapshot_reads() == 0 {
        assert!(Instant::now() < deadline, "the standby did not restore");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    serving.commit_prompt(FIRST_FLOW, 2).await;
    serving
        .node
        .take_snapshot()
        .await
        .expect("the second snapshot succeeds");
    let deadline = Instant::now() + CATCH_UP_DEADLINE;
    loop {
        let keys = snapshot_keys(&cluster).await;
        if first_snapshot_keys.iter().all(|key| !keys.contains(key)) {
            break;
        }
        assert!(
            Instant::now() < deadline,
            "retention kept the first snapshot"
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    standby.object_store.hold_snapshot_reads(false);

    wait_for_status(
        &standby.standby,
        "the interrupted restore restarts",
        |status| status.last_error.is_some(),
    )
    .await;
    let deadline = Instant::now() + LONGEST_RESTART_DELAY + CATCH_UP_DEADLINE;
    while standby.standby.status().lag.is_none() {
        let status = standby.standby.status();
        assert_retrying(&status);
        assert!(Instant::now() < deadline, "the standby did not recover");
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    wait_until_caught_up(&cluster, &standby.standby).await;
    assert_wrote_nothing(&standby);
    serving.node.shutdown().await.expect("lease released");
    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(node.committed_count(FIRST_FLOW).await, 5);
    node.node.shutdown().await.expect("lease released");
}

/// Returns every snapshot object key of node-a.
async fn snapshot_keys(cluster: &TestCluster) -> Vec<String> {
    let listing = cluster
        .object_store
        .list("snapshot/node-a/", None, 1000)
        .await
        .expect("list snapshot objects");
    assert!(listing.next_start_after.is_none());
    listing
        .objects
        .into_iter()
        .map(|object| object.key)
        .collect()
}

#[tokio::test(start_paused = true)]
async fn a_takeover_from_a_standby_behind_the_log_serves_its_store_after_a_longer_replay() {
    let mut cluster = TestCluster::new();
    let serving = cluster
        .start_with(&cluster.config(LocalStoreConfig::InMemory))
        .await
        .expect("the serving node starts");
    let standby_config = cluster.config(LocalStoreConfig::InMemory);
    let standby = start_standby(&cluster, standby_config.clone(), fast_standby_config());
    serving.commit_prompt(FIRST_FLOW, 2).await;
    wait_until_caught_up(&cluster, &standby.standby).await;
    standby.object_store.fail_log_reads(true);
    serving.commit_prompt(FIRST_FLOW, 5).await;
    tokio::time::sleep(BEHIND_LAG_THRESHOLD + Duration::from_secs(1)).await;
    wait_for_status(&standby.standby, "the standby is behind", |status| {
        status.state == HotStandbyState::Behind
    })
    .await;
    serving.node.shutdown().await.expect("lease released");

    let (node, _) = take_over_to_serving(&cluster, standby_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(
        applied_flows(&node.applier).len(),
        5,
        "the takeover replays what the standby missed"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 7);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_takeover_serves_a_standby_store_across_incarnation_changes_it_has_not_seen() {
    let mut cluster = TestCluster::new();
    let first_config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster
        .start_with(&first_config)
        .await
        .expect("incarnation 1 starts");
    let standby = start_standby(
        &cluster,
        cluster.config(LocalStoreConfig::InMemory),
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
    second.commit_prompt(FIRST_FLOW, 2).await;
    second.node.shutdown().await.expect("lease released");
    assert_eq!(
        standby
            .standby
            .status()
            .lag
            .expect("the standby follows")
            .incarnation,
        1,
        "the standby has not reread the manifest"
    );

    // The takeover starts a third incarnation from the standby's store.
    let mut takeover_config = second_config;
    takeover_config.node.incarnation = 3;
    let (node, _) = take_over_to_serving(&cluster, takeover_config, standby.standby).await;
    assert!(node.node.is_serving_hot_standby_store_for_test());
    assert_eq!(
        applied_flows(&node.applier).len(),
        2,
        "the takeover replays incarnation 2"
    );
    assert_eq!(node.committed_count(FIRST_FLOW).await, 4);
    node.commit_prompt(FIRST_FLOW, 1).await;
    assert_eq!(node.committed_count(FIRST_FLOW).await, 5);
    node.node.shutdown().await.expect("lease released");
}
