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

use dexzd_clock::WallClock;
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_engine::{
    EngineNode, EngineNodeConfig, EngineNodeDependencies, EngineRuntimeHooks, EngineServices,
    EngineStartError, LocalStoreConfig, NodeStopReason, SystemEngineIdSource,
};
use dexzd_log::LogError;
use dexzd_object_store::{FaultAction, ObjectStore, ObjectStoreOperation};
use prost::Message;
use tokio::sync::watch;
use tokio::time::Instant;

use crate::harness::{CountingApplier, RecordingRuntime, TestCluster};
use crate::standby_takeover::start_node;

const FLOW_ID: &str = "recovered-flow";
const LEASE_KEY: &str = "lease/node-a";
/// Longer than the 14.5-second server shutdown budget and the 30-second S3 request timeout.
const SLOW_LEASE_WRITE: Duration = Duration::from_secs(60);

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
    let cluster_reads_before = cluster
        .object_store
        .call_count(ObjectStoreOperation::Get, "cluster");
    cluster.faults.fail_next(
        ObjectStoreOperation::Get,
        "cluster",
        FaultAction::FailBeforeEffect,
    );
    let first = cluster
        .start_with(&config)
        .await
        .expect("cluster read retried");
    assert!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Get, "cluster")
            >= cluster_reads_before + 2
    );
    first.node.shutdown().await.expect("lease released");
    for key in ["manifest", LEASE_KEY] {
        let reads_before = cluster
            .object_store
            .call_count(ObjectStoreOperation::Get, key);
        cluster.faults.fail_next(
            ObjectStoreOperation::Get,
            key,
            FaultAction::FailBeforeEffect,
        );
        let node = cluster
            .start_with(&config)
            .await
            .expect("metadata read retried");
        assert!(
            cluster
                .object_store
                .call_count(ObjectStoreOperation::Get, key)
                >= reads_before + 2
        );
        node.node.shutdown().await.expect("lease released");
    }
}

#[tokio::test(start_paused = true)]
async fn many_independent_short_read_failures_get_fresh_windows() {
    let mut cluster = TestCluster::new();
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.recovery.startup_read_retry_budget = Duration::from_millis(200);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 100).await;
    first.node.shutdown().await.expect("lease released");
    let reads_before = cluster
        .faults
        .count_operations_per_key(ObjectStoreOperation::Get, "log/");
    cluster
        .faults
        .fail_first_read_of_each_key_under(ObjectStoreOperation::Get, "log/");
    let restarted = cluster
        .start_with(&config)
        .await
        .expect("independent reads retry");
    assert_eq!(restarted.committed_count(FLOW_ID).await, 100);
    let retrying_keys = cluster
        .faults
        .count_operations_per_key(ObjectStoreOperation::Get, "log/")
        .iter()
        .filter(|(key, count)| **count >= reads_before.get(*key).copied().unwrap_or(0) + 2)
        .count();
    assert!(
        retrying_keys >= 100,
        "only {retrying_keys} log keys were retried"
    );
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn concurrent_reads_survive_a_one_second_brownout() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 80).await;
    first.node.shutdown().await.expect("lease released");
    let reads_before = cluster
        .faults
        .count_operations_per_key(ObjectStoreOperation::Get, "log/");
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "log/");
    let start = tokio::spawn(start_node(&cluster, config));
    for _ in 0..10_000 {
        let reads_in_flight = cluster
            .faults
            .count_operations_per_key(ObjectStoreOperation::Get, "log/")
            .iter()
            .filter(|(key, count)| **count > *reads_before.get(*key).unwrap_or(&0))
            .count();
        if reads_in_flight >= 64 {
            break;
        }
        tokio::task::yield_now().await;
    }
    let reads_in_flight = cluster
        .faults
        .count_operations_per_key(ObjectStoreOperation::Get, "log/")
        .iter()
        .filter(|(key, count)| **count > *reads_before.get(*key).unwrap_or(&0))
        .count();
    assert!(
        reads_in_flight >= 64,
        "only {reads_in_flight} reads started"
    );
    // Sleeping runs each read's full backoff, so 64 reads would drain a shared 30 s budget
    // within 0.5 s.
    tokio::time::sleep(Duration::from_secs(1)).await;
    cluster.faults.clear();
    let restarted = start
        .await
        .expect("start task completes")
        .expect("brownout recovers");
    assert_eq!(restarted.committed_count(FLOW_ID).await, 80);
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn permanent_store_error_fails_without_retry() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    cluster.faults.fail_next(
        ObjectStoreOperation::Get,
        "cluster",
        FaultAction::FailPermanently,
    );
    let error = cluster
        .start_with(&config)
        .await
        .err()
        .expect("permanent error fails startup");
    assert!(error.to_string().contains("injected permanent failure"));
    assert_eq!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Get, "cluster"),
        1
    );
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
    first.commit_prompt(FLOW_ID, 5).await;
    first.node.shutdown().await.expect("lease released");
    let log_keys_before: Vec<String> = cluster
        .object_store
        .list("log/", None, 1000)
        .await
        .expect("list log")
        .objects
        .into_iter()
        .map(|object| object.key)
        .collect();
    assert!(log_keys_before.len() >= 6);
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, &log_keys_before[3]);
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
    let applied_before_stop = applier.applied_commands().len();
    assert!(
        applied_before_stop > 0 && applied_before_stop < 5,
        "{applied_before_stop} commands applied"
    );
    tokio::task::yield_now().await;
    assert_eq!(applier.applied_commands().len(), applied_before_stop);
    let log_keys_after: Vec<String> = cluster
        .object_store
        .list("log/", None, 1000)
        .await
        .expect("list log")
        .objects
        .into_iter()
        .map(|object| object.key)
        .collect();
    assert_eq!(
        log_keys_after, log_keys_before,
        "stale epoch must not write EpochStart"
    );
}

#[tokio::test(start_paused = true)]
async fn shutdown_interrupts_a_retrying_lease_read_without_prior_retries() {
    let cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, LEASE_KEY);
    let (sender, cancellation) = watch::channel(false);
    let start = tokio::spawn(start_with_cancellation(&cluster, config, cancellation));
    cluster.faults.wait_until_faulted().await;
    sender.send(true).expect("start observes shutdown");
    let (result, _) = tokio::time::timeout(Duration::from_secs(1), start)
        .await
        .expect("lease acquisition stops promptly")
        .expect("start task completes");
    assert!(matches!(result, Ok(None)));
}

#[tokio::test(start_paused = true)]
async fn shutdown_during_acquisition_releases_a_written_lease() {
    let cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    cluster
        .faults
        .delay_next_put_under(LEASE_KEY, Duration::from_millis(1));
    let (sender, cancellation) = watch::channel(false);
    let start = tokio::spawn(start_with_cancellation(&cluster, config, cancellation));
    cluster.faults.wait_until_delayed_operation_entered().await;
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "manifest");
    tokio::time::advance(Duration::from_millis(1)).await;
    cluster.faults.wait_until_faulted().await;
    sender.send(true).expect("start observes shutdown");
    let (result, _) = tokio::time::timeout(Duration::from_secs(1), start)
        .await
        .expect("acquisition stops promptly")
        .expect("start task completes");
    assert!(matches!(result, Ok(None)));
    let lease = cluster
        .object_store
        .get(LEASE_KEY)
        .await
        .expect("read lease")
        .expect("lease exists");
    let lease = dexzd_protocol::dexzd::v1::NodeLease::decode(lease.bytes).expect("decode lease");
    assert_eq!(lease.expires_at_unix_micros, i64::MIN);
}

#[tokio::test(start_paused = true)]
async fn shutdown_interrupts_the_read_back_of_an_unknown_lease_write() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    cluster.faults.fail_next_then_every_read_under(
        ObjectStoreOperation::Put,
        LEASE_KEY,
        FaultAction::ApplyThenOutcomeUnknown,
        ObjectStoreOperation::Get,
        LEASE_KEY,
    );
    let lease_validity_deadline = LeaseValidityDeadline::disarmed();
    let (sender, cancellation) = watch::channel(false);
    let start = tokio::spawn(start_with_cancellation_and_deadline(
        &cluster,
        config.clone(),
        cancellation,
        lease_validity_deadline.clone(),
    ));
    cluster.faults.wait_until_faulted().await;
    let manifest_writes = cluster
        .object_store
        .call_count(ObjectStoreOperation::Put, "manifest");
    let latest_expiry = cluster.clock.now_unix_micros()
        + i64::try_from(config.lease.duration.as_micros()).expect("lease duration fits i64");
    sender.send(true).expect("start observes shutdown");
    let (result, _) = tokio::time::timeout(Duration::from_secs(1), start)
        .await
        .expect("the lease read-back stops promptly")
        .expect("start task completes");
    assert!(matches!(result, Ok(None)));
    assert!(lease_validity_deadline.armed_deadline().is_none());
    assert_eq!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Put, LEASE_KEY),
        1,
        "no renewal or release follows an unconfirmed write"
    );
    assert_eq!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Put, "manifest"),
        manifest_writes,
        "no epoch raise follows an unconfirmed write"
    );
    cluster.faults.clear();
    let lease = cluster
        .object_store
        .get(LEASE_KEY)
        .await
        .expect("read lease")
        .expect("the unconfirmed write landed");
    let lease = dexzd_protocol::dexzd::v1::NodeLease::decode(lease.bytes).expect("decode lease");
    assert!(lease.expires_at_unix_micros <= latest_expiry);
    let restart_started = Instant::now();
    let restarted = cluster
        .start_with(&config)
        .await
        .expect("a later start acquires the expired lease");
    assert!(
        restart_started.elapsed()
            <= config.lease.duration + config.lease.takeover_grace + Duration::from_secs(1)
    );
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn shutdown_interrupts_a_slow_lease_write() {
    let mut cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    cluster
        .faults
        .delay_next_put_under(LEASE_KEY, SLOW_LEASE_WRITE);
    let lease_validity_deadline = LeaseValidityDeadline::disarmed();
    let (sender, cancellation) = watch::channel(false);
    let start = tokio::spawn(start_with_cancellation_and_deadline(
        &cluster,
        config.clone(),
        cancellation,
        lease_validity_deadline.clone(),
    ));
    cluster.faults.wait_until_delayed_operation_entered().await;
    let manifest_writes = cluster
        .object_store
        .call_count(ObjectStoreOperation::Put, "manifest");
    sender.send(true).expect("start observes shutdown");
    let (result, _) = tokio::time::timeout(Duration::from_secs(1), start)
        .await
        .expect("the lease write stops promptly")
        .expect("start task completes");
    assert!(matches!(result, Ok(None)));
    assert!(lease_validity_deadline.armed_deadline().is_none());
    assert_eq!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Put, LEASE_KEY),
        1,
        "no release follows a cancelled write"
    );
    assert_eq!(
        cluster
            .object_store
            .call_count(ObjectStoreOperation::Put, "manifest"),
        manifest_writes,
        "no epoch raise follows a cancelled write"
    );
    tokio::time::sleep(SLOW_LEASE_WRITE).await;
    assert!(
        cluster
            .object_store
            .get(LEASE_KEY)
            .await
            .expect("read lease")
            .is_none(),
        "the dropped write never lands"
    );
    let restart_started = Instant::now();
    let restarted = cluster
        .start_with(&config)
        .await
        .expect("a later start acquires the lease");
    assert!(restart_started.elapsed() < config.lease.duration);
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn unknown_lease_write_is_retried_from_a_fresh_read_after_its_validity() {
    let cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let lease_duration = config.lease.duration;
    cluster.faults.fail_next_then_every_read_under(
        ObjectStoreOperation::Put,
        LEASE_KEY,
        FaultAction::ApplyThenOutcomeUnknown,
        ObjectStoreOperation::Get,
        LEASE_KEY,
    );
    let start = tokio::spawn(start_node(&cluster, config));
    cluster.faults.wait_until_faulted().await;
    tokio::time::sleep(lease_duration + Duration::from_secs(1)).await;
    cluster.faults.clear();
    // Confirming the first write now would fail, because its local validity has ended.
    let restarted = tokio::time::timeout(Duration::from_secs(10), start)
        .await
        .expect("acquisition finishes once reads recover")
        .expect("start task completes")
        .expect("a fresh read acquires the lease");
    restarted.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn acquisition_retry_cannot_outlive_lease_validity() {
    let cluster = TestCluster::new();
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.lease.duration = Duration::from_millis(300);
    cluster
        .faults
        .delay_next_put_under(LEASE_KEY, Duration::from_millis(1));
    let start = tokio::spawn(start_node(&cluster, config));
    cluster.faults.wait_until_delayed_operation_entered().await;
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "manifest");
    tokio::time::advance(Duration::from_millis(1)).await;
    cluster.faults.wait_until_faulted().await;
    let retry_started = Instant::now();
    let error = tokio::time::timeout(Duration::from_secs(1), start)
        .await
        .expect("acquisition stays within lease validity")
        .expect("start task completes")
        .err()
        .expect("manifest read fails");
    assert!(
        retry_started.elapsed() < Duration::from_millis(600),
        "{error}"
    );
}

#[tokio::test(start_paused = true)]
async fn failed_start_lease_release_is_reported() {
    let mut cluster = TestCluster::new();
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.recovery.startup_read_retry_budget = Duration::from_millis(100);
    let first = cluster.start_with(&config).await.expect("node starts");
    first.commit_prompt(FLOW_ID, 1).await;
    first.node.shutdown().await.expect("lease released");
    cluster
        .faults
        .fail_every_read_under(ObjectStoreOperation::Get, "log/");
    let start = tokio::spawn(start_node(&cluster, config));
    cluster.faults.wait_until_faulted().await;
    cluster.faults.fail_puts_under(LEASE_KEY);
    let error = start
        .await
        .expect("start task completes")
        .err()
        .expect("start fails");
    assert!(
        matches!(&error, EngineStartError::FailedStartLeaseRelease { .. }),
        "{error}"
    );
    assert!(
        error
            .to_string()
            .contains("releasing its lease also failed")
    );
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
    cancellation: watch::Receiver<bool>,
) -> impl Future<
    Output = (
        Result<Option<EngineNode>, EngineStartError>,
        Arc<CountingApplier>,
    ),
> + Send
+ 'static {
    start_with_cancellation_and_deadline(
        cluster,
        config,
        cancellation,
        LeaseValidityDeadline::disarmed(),
    )
}

fn start_with_cancellation_and_deadline(
    cluster: &TestCluster,
    config: EngineNodeConfig,
    mut cancellation: watch::Receiver<bool>,
    lease_validity_deadline: LeaseValidityDeadline,
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
                lease_validity_deadline,
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
