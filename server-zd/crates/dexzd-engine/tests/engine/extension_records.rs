// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Extension records of a test-only embedding application through apply, publish, and recovery.

use std::sync::Arc;
use std::time::Duration;

use dexzd_engine::{
    CommitError, CommitUrgency, CommittedRecordHandling, CommittedRecordSink,
    EngineCommandAndEffectHooks, EngineNode, EngineNodeDependencies, EngineServices,
    EngineStartError, ExtensionSubmitError, HotStandby, HotStandbyDependencies, HotStandbyState,
    LocalStoreConfig, SystemEngineIdSource,
};
use dexzd_object_store::{ObjectStore, node_lease_object_key};
use dexzd_protocol::dexzd::v1::NodeLease;
use prost::Message;

use crate::fake_embedding_application::{
    FakeExtensionApplier, FakeOperation, FakeOutcome, RecordingRebuilder, fake_application_config,
    start_fake_application, start_fake_application_standby, stored_counter,
    take_over_as_fake_application, wait_until,
};
use crate::harness::{CountingApplier, RecordingCommandAndEffectHooks, TestCluster};
use crate::hot_standby::{fast_standby_config, rocksdb_store, wait_for_status};

#[tokio::test(start_paused = true)]
async fn a_record_commits_after_the_sink_sees_it_and_returns_its_outcome() {
    let cluster = TestCluster::new();
    let config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    let node = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    let (stamp, ticket) = node
        .submit(FakeOperation::Increment, "alpha", CommitUrgency::Prompt)
        .expect("the record is submitted");
    let committed = ticket.wait().await.expect("the record commits");
    let seen = node.sink.seen();
    assert_eq!(seen.len(), 1, "{seen:?}");
    assert_eq!(seen[0].command_id, stamp.command_id);
    assert!(seen[0].has_waiter && !seen[0].is_replayed, "{seen:?}");
    assert_eq!(committed.stamp, stamp);
    let outcome = committed
        .outcome
        .downcast::<FakeOutcome>()
        .expect("the submitter downcasts its outcome");
    assert_eq!(outcome.counter, 1);
    assert!(
        outcome
            .is_seen_by_sink
            .load(std::sync::atomic::Ordering::SeqCst),
        "the sink saw the record before its submitter"
    );
    drop(outcome);
    assert_eq!(node.counter("alpha").await, 1);
    assert_eq!(node.increment("alpha").await, 2);
    assert_eq!(node.live_outcomes(), 0);
    assert!(
        matches!(
            node.node.committer().submit(
                dexzd_apply::ApplyContext {
                    partition: dexzd_keyspace::PartitionId(0),
                    flow_hash: node.routing_hash("alpha"),
                },
                crate::harness::flow_command("alpha"),
                CommitUrgency::Prompt,
            ),
            Err(CommitError::Unavailable { .. })
        ),
        "an extension node accepts no Flow command"
    );
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_sink_submits_a_follow_up_record_without_blocking_its_lane() {
    let cluster = TestCluster::new();
    let config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    let node = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    let (_, ticket) = node
        .submit(
            FakeOperation::IncrementAndFollowUp,
            "beta",
            CommitUrgency::Prompt,
        )
        .expect("the record is submitted");
    ticket.wait().await.expect("the record commits");
    let follow_ups = node.sink.follow_up_counters.clone();
    wait_until("the follow-up commits", || {
        follow_ups.lock().expect("counter lock").as_slice() == [1]
    })
    .await;
    assert_eq!(node.counter("beta-follow-up").await, 1);
    assert_eq!(node.increment("beta").await, 2);
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn recovery_replays_records_without_waiters_or_kept_outcomes() {
    let cluster = TestCluster::new();
    let config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    let first = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    for expected in 1..=3 {
        assert_eq!(first.increment("gamma").await, expected);
    }
    first.node.shutdown().await.expect("lease released");
    let second = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application restarts");
    let replayed = second.sink.seen();
    assert_eq!(replayed.len(), 3, "{replayed:?}");
    assert!(
        replayed
            .iter()
            .all(|record| record.is_replayed && !record.has_waiter),
        "{replayed:?}"
    );
    assert_eq!(second.live_outcomes(), 0, "replayed outcomes are dropped");
    assert_eq!(second.counter("gamma").await, 3);
    assert_eq!(second.increment("gamma").await, 4);
    let live = second.sink.seen().pop().expect("the new record is seen");
    assert!(live.has_waiter && !live.is_replayed, "{live:?}");
    second.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_restart_restores_the_snapshot_and_replays_only_later_records() {
    let cluster = TestCluster::new();
    let config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    let first = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    first.increment("delta").await;
    first.increment("delta").await;
    first.node.take_snapshot().await.expect("snapshot taken");
    let (stamp, ticket) = first
        .submit(FakeOperation::Increment, "delta", CommitUrgency::Prompt)
        .expect("the record is submitted");
    ticket.wait().await.expect("the record commits");
    first.node.shutdown().await.expect("lease released");
    let second = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application restarts");
    let replayed: Vec<_> = second
        .sink
        .seen()
        .into_iter()
        .map(|record| record.command_id)
        .collect();
    assert_eq!(replayed, [stamp.command_id]);
    assert_eq!(second.counter("delta").await, 3);
    second.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_follows_records_and_takes_over() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let cluster = TestCluster::new();
    let serving_config =
        fake_application_config(&cluster, rocksdb_store(&directory.path().join("serving")));
    let serving = start_fake_application(&cluster, &serving_config)
        .await
        .expect("the fake application starts");
    serving.increment("epsilon").await;
    let mut standby_config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    standby_config.node.incarnation = 2;
    let standby =
        start_fake_application_standby(&cluster, standby_config.clone(), fast_standby_config())
            .expect("the standby starts");
    serving.increment("epsilon").await;
    let hasher = serving.hasher.clone();
    wait_until("the standby applies both records", || {
        standby
            .local_store_for_test()
            .is_some_and(|store| stored_counter(&store, &hasher, "epsilon") == 2)
    })
    .await;
    serving.node.shutdown().await.expect("lease released");
    let taken_over = take_over_as_fake_application(&cluster, &standby_config, standby).await;
    assert_eq!(taken_over.counter("epsilon").await, 2);
    assert_eq!(taken_over.increment("epsilon").await, 3);
    taken_over.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_halted_partition_fails_waiting_records_at_once() {
    let cluster = TestCluster::new();
    let config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    let node = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    let (_, first) = node
        .submit(FakeOperation::Increment, "zeta", CommitUrgency::Batched)
        .expect("the record is submitted");
    let (_, halting) = node
        .submit(FakeOperation::Halt, "zeta", CommitUrgency::Batched)
        .expect("the record is submitted");
    for ticket in [first, halting] {
        let error = tokio::time::timeout(Duration::from_secs(5), ticket.wait())
            .await
            .expect("the halt fails the ticket at once")
            .expect_err("the halted frame fails");
        assert!(
            matches!(&error, CommitError::Unavailable { detail } if detail.contains("stopped applying")),
            "{error:?}"
        );
    }
    assert!(
        node.node.stop_reason().is_none(),
        "only the partition stopped"
    );
    assert_eq!(node.node.halted_partitions().len(), 1);
    assert_eq!(
        node.counter("zeta").await,
        0,
        "the halted frame wrote nothing"
    );
    assert_eq!(node.live_outcomes(), 0);
    assert!(matches!(
        node.submit(FakeOperation::Increment, "zeta", CommitUrgency::Prompt),
        Err(ExtensionSubmitError::Commit(
            CommitError::Unavailable { .. }
        ))
    ));
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_repeated_apply_with_other_writes_halts_the_partition() {
    let cluster = TestCluster::new();
    let mut config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    config.commit.debug_replay_checks = true;
    let node = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    let (_, ticket) = node
        .submit(
            FakeOperation::Nondeterministic,
            "eta",
            CommitUrgency::Prompt,
        )
        .expect("the record is submitted");
    let error = ticket.wait().await.expect_err("the double apply differs");
    assert!(
        matches!(&error, CommitError::Unavailable { detail } if detail.contains("returned different writes")),
        "{error:?}"
    );
    assert!(node.node.stop_reason().is_none());
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn an_oversized_record_is_rejected_before_stamping() {
    let cluster = TestCluster::new();
    let mut config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    config.commit.max_unapplied_bytes = 1024;
    let node = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    let result = node.submit(
        FakeOperation::Increment,
        &"x".repeat(2000),
        CommitUrgency::Prompt,
    );
    assert!(
        matches!(
            result,
            Err(ExtensionSubmitError::RecordTooLarge {
                limit_bytes: 1024,
                ..
            })
        ),
        "{result:?}"
    );
    assert!(node.sink.seen().is_empty());
    node.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_record_kind_that_contradicts_the_cluster_identity_fails_before_bootstrap() {
    let mut cluster = TestCluster::new();
    let application_config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    let flow_on_application = cluster.start_with(&application_config).await;
    assert!(
        matches!(
            flow_on_application,
            Err(EngineStartError::InvalidConfig { .. })
        ),
        "{:?}",
        flow_on_application.err()
    );
    let server_zd_config = cluster.config(LocalStoreConfig::InMemory);
    let extension_on_server_zd = start_fake_application(&cluster, &server_zd_config).await;
    assert!(
        matches!(
            extension_on_server_zd,
            Err(EngineStartError::InvalidConfig { .. })
        ),
        "{:?}",
        extension_on_server_zd.err()
    );
    let manifest = dexzd_cluster_metadata::read_manifest(cluster.object_store.as_ref())
        .await
        .expect("manifest read");
    assert!(
        manifest.is_none(),
        "no rejected start bootstrapped the cluster"
    );

    let flow_standby = HotStandby::start(
        application_config,
        fast_standby_config(),
        HotStandbyDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: cluster.object_store.clone(),
            clock: cluster.clock.clone(),
            applier: CommittedRecordHandling::FlowCommand(Arc::new(CountingApplier::default())),
            id_source: Arc::new(SystemEngineIdSource),
            restart_jitter_seed: 7,
        },
    );
    assert!(matches!(
        flow_standby,
        Err(EngineStartError::InvalidConfig { .. })
    ));
    let extension_standby =
        start_fake_application_standby(&cluster, server_zd_config, fast_standby_config());
    assert!(matches!(
        extension_standby,
        Err(EngineStartError::InvalidConfig { .. })
    ));
}

#[tokio::test(start_paused = true)]
async fn hooks_of_another_record_kind_fail_the_start_and_release_the_lease() {
    let cluster = TestCluster::new();
    let config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    let flow_hooks = Arc::new(RecordingCommandAndEffectHooks::default());
    let result = EngineNode::start(
        &config,
        EngineNodeDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: cluster.object_store.clone(),
            clock: cluster.clock.clone(),
            applier: CommittedRecordHandling::ExtensionRecord(Arc::new(
                FakeExtensionApplier::default(),
            )),
            id_source: Arc::new(SystemEngineIdSource),
            lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
        },
        move |_services: EngineServices| EngineCommandAndEffectHooks {
            committed_commands: CommittedRecordSink::FlowCommand(flow_hooks),
            rebuilder: Arc::new(RecordingRebuilder::default()),
        },
    )
    .await;
    assert!(
        matches!(&result, Err(EngineStartError::InvalidConfig { detail }) if detail.contains("record kind")),
        "{:?}",
        result.err()
    );
    let lease = cluster
        .object_store
        .get(&node_lease_object_key("node-a"))
        .await
        .expect("read node lease")
        .expect("the start took the lease");
    let lease = NodeLease::decode(lease.bytes).expect("decode lease");
    assert_eq!(
        lease.expires_at_unix_micros,
        i64::MIN,
        "the lease is released"
    );
}

#[tokio::test(start_paused = true)]
async fn replay_checks_start_a_node_and_a_standby_over_application_history_rows() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let cluster = TestCluster::new();
    let mut config =
        fake_application_config(&cluster, rocksdb_store(&directory.path().join("serving")));
    config.commit.debug_replay_checks = true;
    let first = start_fake_application(&cluster, &config)
        .await
        .expect("the fake application starts");
    first.increment("theta").await;
    first.increment("theta").await;
    first.node.take_snapshot().await.expect("snapshot taken");
    let close_waiter = first.node.local_store_close_waiter();
    first.node.shutdown().await.expect("lease released");
    // Its committer holds the store open.
    drop(first.sink);
    assert!(
        close_waiter
            .wait_until_closed(Duration::from_secs(30))
            .await
    );
    // The reopened store holds history rows whose keys are no server-zd run keys.
    let second = start_fake_application(&cluster, &config)
        .await
        .expect("replay checks skip server-zd diagnostics");
    assert_eq!(second.counter("theta").await, 2);
    let mut standby_config = fake_application_config(&cluster, LocalStoreConfig::InMemory);
    standby_config.node.incarnation = 2;
    standby_config.commit.debug_replay_checks = true;
    let standby = start_fake_application_standby(&cluster, standby_config, fast_standby_config())
        .expect("the standby starts");
    let status = wait_for_status(&standby, "the standby follows", |status| {
        status.state == HotStandbyState::Following || status.state == HotStandbyState::Failed
    })
    .await;
    assert_eq!(status.state, HotStandbyState::Following, "{status:?}");
    let hasher = second.hasher.clone();
    let store = standby
        .local_store_for_test()
        .expect("the standby has a store");
    assert_eq!(stored_counter(&store, &hasher, "theta"), 2);
    standby.shutdown().await;
    second.node.shutdown().await.expect("lease released");
}
