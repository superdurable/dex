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
    EngineNode, EngineNodeConfig, EngineNodeDependencies, EngineRuntimeHooks, EngineServices,
    EngineStartError, LocalStoreConfig, NodeStopReason, SystemEngineIdSource,
};
use dexzd_log::LogError;
use dexzd_object_store::{FaultAction, ObjectStore, ObjectStoreOperation};
use tokio::sync::watch;
use tokio::time::Instant;

use crate::harness::{CountingApplier, RecordingRuntime, TestCluster};
use crate::standby_takeover::start_node;

const FLOW_ID: &str = "recovered-flow";
const LEASE_KEY: &str = "lease/node-a";

#[tokio::test(start_paused = true)]
async fn transient_batch_read_replays_the_same_state() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 3).await;
    first.node.shutdown().await.expect("lease released");
    let batch_key = cluster
        .object_store
        .list("log/", None, 1000)
        .await
        .expect("list log")
        .objects
        .first()
        .expect("log batch exists")
        .key
        .clone();
    cluster.faults.fail_next(
        ObjectStoreOperation::Get,
        &batch_key,
        FaultAction::FailBeforeEffect,
    );
    let reads_before = cluster
        .object_store
        .call_count(ObjectStoreOperation::Get, &batch_key);
    let restarted = cluster
        .start_with(&config)
        .await
        .expect("start retries batch read");
    assert_eq!(restarted.committed_count(FLOW_ID).await, 3);
    assert!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Get, &batch_key)
            >= reads_before + 2
    );
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn transient_log_listing_cannot_look_like_an_empty_stream() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 1).await;
    first.node.shutdown().await.expect("lease released");
    let log_prefix = "log/node-a/0000000001/";
    cluster.faults.fail_next(
        ObjectStoreOperation::List,
        log_prefix,
        FaultAction::FailBeforeEffect,
    );
    let listings_before = cluster
        .object_store
        .call_count(ObjectStoreOperation::List, log_prefix);
    let restarted = cluster
        .start_with(&config)
        .await
        .expect("listing is retried");
    assert_eq!(restarted.committed_count(FLOW_ID).await, 1);
    assert!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::List, log_prefix)
            >= listings_before + 2
    );
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn transient_cluster_manifest_and_lease_reads_start() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    cluster.faults.fail_next(
        ObjectStoreOperation::Get,
        "cluster",
        FaultAction::FailBeforeEffect,
    );
    let first = cluster
        .start_with(&config)
        .await
        .expect("cluster read retried");
    first.node.shutdown().await.expect("lease released");
    for key in ["manifest", LEASE_KEY] {
        cluster.faults.fail_next(
            ObjectStoreOperation::Get,
            key,
            FaultAction::FailBeforeEffect,
        );
        let node = cluster
            .start_with(&config)
            .await
            .expect("metadata read retried");
        node.node.shutdown().await.expect("lease released");
    }
}

#[tokio::test(start_paused = true)]
async fn corrupt_batch_fails_without_retry() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 1).await;
    first.node.shutdown().await.expect("lease released");
    let batch_key = cluster
        .object_store
        .list("log/", None, 1000)
        .await
        .expect("list log")
        .objects
        .first()
        .expect("log batch exists")
        .key
        .clone();
    cluster.faults.fail_next(
        ObjectStoreOperation::Get,
        &batch_key,
        FaultAction::CorruptOnRead,
    );
    let reads_before = cluster
        .object_store
        .call_count(ObjectStoreOperation::Get, &batch_key);
    let error = cluster
        .start_with(&config)
        .await
        .err()
        .expect("corrupt batch fails");
    assert!(matches!(error, EngineStartError::Log(LogError::Corrupt(_))));
    assert_eq!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Get, &batch_key),
        reads_before + 1
    );
}

#[tokio::test(start_paused = true)]
async fn exhausted_read_budget_names_operation_key_and_last_error() {
    let mut cluster = TestCluster::new();
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.recovery.startup_read_retry_budget = Duration::from_millis(100);
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "cluster");
    let started = Instant::now();
    let error = cluster
        .start_with(&config)
        .await
        .err()
        .expect("budget exhausted");
    let elapsed = started.elapsed();
    assert!(elapsed >= Duration::from_millis(100), "elapsed {elapsed:?}");
    assert!(elapsed < Duration::from_millis(200), "elapsed {elapsed:?}");
    let detail = error.to_string();
    assert!(
        detail.contains("get") && detail.contains("cluster"),
        "{detail}"
    );
    assert!(
        detail.contains("last error") && detail.contains("injected failure"),
        "{detail}"
    );
}

#[tokio::test(start_paused = true)]
async fn slow_retrying_read_cannot_outlive_the_budget() {
    let mut cluster = TestCluster::new();
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.recovery.startup_read_retry_budget = Duration::from_millis(100);
    cluster.faults.fail_next(
        ObjectStoreOperation::Get,
        "cluster",
        FaultAction::FailBeforeEffect,
    );
    cluster
        .faults
        .delay_every_get_under("cluster", Duration::from_secs(1));
    let started = Instant::now();
    let error = cluster
        .start_with(&config)
        .await
        .err()
        .expect("slow retry exhausts budget");
    let elapsed = started.elapsed();
    assert!(elapsed >= Duration::from_millis(100), "elapsed {elapsed:?}");
    assert!(elapsed < Duration::from_millis(200), "elapsed {elapsed:?}");
    let detail = error.to_string();
    assert!(
        detail.contains("get") && detail.contains("cluster") && detail.contains("injected failure"),
        "{detail}"
    );
}

#[tokio::test(start_paused = true)]
async fn lease_loss_interrupts_startup_read_retry() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 1).await;
    first.node.shutdown().await.expect("lease released");
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "log/");
    let (_sender, cancellation) = watch::channel(false);
    let start = tokio::spawn(start_with_cancellation(&cluster, config, cancellation));
    cluster.faults.wait_until_faulted().await;
    cluster.expire_lease_of("node-a").await;
    let (result, applier) = tokio::time::timeout(Duration::from_secs(10), start)
        .await
        .expect("start stops before retry budget")
        .expect("start task completes");
    assert!(matches!(
        result,
        Err(EngineStartError::Stopped {
            reason: NodeStopReason::LeaseLost { .. }
        })
    ));
    assert!(applier.applied_commands().is_empty());
}

#[tokio::test(start_paused = true)]
async fn lease_renews_during_a_long_startup_read_retry() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 1).await;
    first.node.shutdown().await.expect("lease released");
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "log/");
    let start = tokio::spawn(start_node(&cluster, config));
    cluster.faults.wait_until_faulted().await;
    for _ in 0..12 {
        tokio::time::advance(Duration::from_secs(1)).await;
    }
    cluster.faults.clear();
    let restarted = start
        .await
        .expect("start task completes")
        .expect("lease stayed valid");
    assert_eq!(restarted.committed_count(FLOW_ID).await, 1);
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn shutdown_interrupts_startup_read_retry() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 1).await;
    first.node.shutdown().await.expect("lease released");
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "log/");
    let (sender, cancellation) = watch::channel(false);
    let start = tokio::spawn(start_with_cancellation(&cluster, config, cancellation));
    cluster.faults.wait_until_faulted().await;
    sender.send(true).expect("start observes shutdown");
    let (result, _) = tokio::time::timeout(Duration::from_secs(1), start)
        .await
        .expect("start stops promptly")
        .expect("start task completes");
    assert!(matches!(
        result,
        Err(EngineStartError::Stopped {
            reason: NodeStopReason::Shutdown
        })
    ));
}

#[tokio::test(start_paused = true)]
async fn unknown_lease_write_outcome_is_verified() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.node.shutdown().await.expect("lease released");
    cluster.faults.fail_next(
        ObjectStoreOperation::Put,
        LEASE_KEY,
        FaultAction::ApplyThenOutcomeUnknown,
    );
    let second = cluster
        .start_with(&config)
        .await
        .expect("lease write verified");
    second.node.shutdown().await.expect("lease released");
}

fn start_with_cancellation(
    cluster: &TestCluster,
    config: EngineNodeConfig,
    mut cancellation: watch::Receiver<bool>,
) -> impl Future<
    Output = (
        Result<Option<EngineNode>, EngineStartError>,
        Arc<CountingApplier>,
    ),
> + Send
+ 'static {
    let object_store = cluster.object_store.clone();
    let clock = cluster.clock.clone();
    async move {
        let applier = Arc::new(CountingApplier::default());
        let runtime = Arc::new(RecordingRuntime::default());
        let factory_runtime = runtime.clone();
        let result = EngineNode::start_until_cancelled(
            &config,
            EngineNodeDependencies {
                metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
                object_store,
                clock,
                applier: applier.clone(),
                id_source: Arc::new(SystemEngineIdSource),
                lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
            },
            move |_services: EngineServices| EngineRuntimeHooks {
                committed_commands: factory_runtime.clone(),
                rebuilder: factory_runtime,
            },
            &mut cancellation,
        )
        .await;
        (result, applier)
    }
}
