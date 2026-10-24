// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attribute Sync projector tests: the real apply marks and acknowledges entries, and a fake
//! Attribute Store records writes.

use std::collections::{BTreeMap, VecDeque};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_apply::ApplyEffect;
use dexzd_cluster_metadata::LeaseStatus;
use dexzd_keyspace::{ColumnFamily, IndexKind, RunId};
use dexzd_metrics::{AttributeSyncFailureKind, NodeMetrics};
use dexzd_protocol::dex::{
    AttributeStoreNames, AttributeSyncConfig as WriteSyncConfig, AttributeWrite, FlowConfig,
    FlowStartOptions, IdReusePolicy, SetAttributesRequest, Value,
};
use dexzd_protocol::dexzd::v1::{AttributesSetCommand, FlowCommand, flow_command};
use futures::future::BoxFuture;
use tokio::sync::{Notify, watch};
use tokio::time::Instant;

use super::{EffectHarness, is_error_reply, settings, start_request, string_value};
use crate::attribute_sync::{
    AttributeSyncConfig, AttributeSyncDependencies, AttributeSyncFence, AttributeSyncItem,
    AttributeSyncProjector, AttributeSyncProjectorHandle, AttributeSyncRetryPolicy,
    AttributeSyncWriteError, AttributeSyncWriter,
};
use crate::effects::EffectRuntime;
use crate::runtime::CommitUrgency;

const STORE: &str = "orders";

/// One write the fake store accepted or refused.
#[derive(Debug, Clone, PartialEq)]
struct RecordedWrite {
    store_name: String,
    flow_id: String,
    items: Vec<AttributeSyncItem>,
    fence: AttributeSyncFence,
    is_committed: bool,
}

/// An Attribute Store that records writes, fails on request, and can hold writes open.
#[derive(Default)]
struct FakeAttributeStore {
    writes: Mutex<Vec<RecordedWrite>>,
    queued_failures: Mutex<VecDeque<AttributeSyncWriteError>>,
    persistent_failure: Mutex<Option<AttributeSyncWriteError>>,
    queued_store_failures: Mutex<BTreeMap<String, VecDeque<AttributeSyncWriteError>>>,
    persistent_store_failures: Mutex<BTreeMap<String, AttributeSyncWriteError>>,
    is_holding_writes: AtomicBool,
    released: Notify,
    write_started: Notify,
    abandoned_writes: AtomicUsize,
}

/// Counts a write whose future was dropped before it finished.
struct InFlightWrite<'store> {
    store: &'store FakeAttributeStore,
    is_finished: bool,
}

impl Drop for InFlightWrite<'_> {
    fn drop(&mut self) {
        if !self.is_finished {
            self.store.abandoned_writes.fetch_add(1, Ordering::SeqCst);
        }
    }
}

impl FakeAttributeStore {
    fn committed_writes(&self) -> Vec<RecordedWrite> {
        self.writes
            .lock()
            .expect("lock")
            .iter()
            .filter(|write| write.is_committed)
            .cloned()
            .collect()
    }

    fn attempt_count(&self) -> usize {
        self.writes.lock().expect("lock").len()
    }

    fn fail_until_cleared(&self, error: AttributeSyncWriteError) {
        *self.persistent_failure.lock().expect("lock") = Some(error);
    }

    fn clear_failure(&self) {
        *self.persistent_failure.lock().expect("lock") = None;
    }

    fn release_writes(&self) {
        self.is_holding_writes.store(false, Ordering::SeqCst);
        self.released.notify_waiters();
    }
}

impl AttributeSyncWriter for FakeAttributeStore {
    fn write_flow_attributes<'a>(
        &'a self,
        store_name: &'a str,
        flow_id: &'a str,
        items: &'a [AttributeSyncItem],
        fence: &'a AttributeSyncFence,
    ) -> BoxFuture<'a, Result<(), AttributeSyncWriteError>> {
        Box::pin(async move {
            let mut in_flight = InFlightWrite {
                store: self,
                is_finished: false,
            };
            self.write_started.notify_waiters();
            loop {
                let released = self.released.notified();
                if !self.is_holding_writes.load(Ordering::SeqCst) {
                    break;
                }
                released.await;
            }
            let failure = self
                .queued_failures
                .lock()
                .expect("lock")
                .pop_front()
                .or_else(|| self.persistent_failure.lock().expect("lock").clone())
                .or_else(|| {
                    self.queued_store_failures
                        .lock()
                        .expect("lock")
                        .get_mut(store_name)
                        .and_then(VecDeque::pop_front)
                })
                .or_else(|| {
                    self.persistent_store_failures
                        .lock()
                        .expect("lock")
                        .get(store_name)
                        .cloned()
                });
            self.writes.lock().expect("lock").push(RecordedWrite {
                store_name: store_name.to_owned(),
                flow_id: flow_id.to_owned(),
                items: items.to_vec(),
                fence: fence.clone(),
                is_committed: failure.is_none(),
            });
            in_flight.is_finished = true;
            failure.map_or(Ok(()), Err)
        })
    }
}

/// A running projector with its fake store and controls.
struct ProjectorFixture {
    harness: EffectHarness,
    store: Arc<FakeAttributeStore>,
    lease: watch::Sender<LeaseStatus>,
    metrics: Arc<NodeMetrics>,
    projector: Option<AttributeSyncProjectorHandle>,
}

impl ProjectorFixture {
    fn start(config: AttributeSyncConfig) -> Self {
        Self::start_with_lease(config, Duration::from_secs(365 * 24 * 3600))
    }

    fn start_with_lease(config: AttributeSyncConfig, lease_validity: Duration) -> Self {
        let harness = EffectHarness::new();
        let store = Arc::new(FakeAttributeStore::default());
        let (lease, lease_status) =
            watch::channel(LeaseStatus::ValidUntil(Instant::now() + lease_validity));
        let metrics = Arc::new(NodeMetrics::unregistered());
        let projector = AttributeSyncProjector::start(
            &config,
            AttributeSyncDependencies {
                writer: Arc::clone(&store) as _,
                runtime: Arc::clone(&harness.runtime) as Arc<dyn EffectRuntime>,
                partition_hasher: harness.hasher.clone(),
                clock: Arc::clone(&harness.clock) as _,
                metrics: Arc::clone(&metrics),
                fence: AttributeSyncFence::for_node_owner([1; 16], "node-a", 7),
                lease_status,
                waker: harness.attribute_sync_waker.clone(),
            },
            std::future::pending(),
        );
        Self {
            harness,
            store,
            lease,
            metrics,
            projector: Some(projector),
        }
    }

    fn start_flow(&self, flow_id: &str, writes: Vec<AttributeWrite>) -> RunId {
        self.start_flow_with_stores(flow_id, &[STORE], writes)
    }

    fn start_flow_with_stores(
        &self,
        flow_id: &str,
        store_names: &[&str],
        writes: Vec<AttributeWrite>,
    ) -> RunId {
        let mut request = start_request(flow_id);
        request.flow_start_options = Some(FlowStartOptions {
            id_reuse_policy: IdReusePolicy::AllowTerminateIfRunning as i32,
            attributes: writes,
            flow_config_override: Some(FlowConfig {
                attribute_store_names: Some(AttributeStoreNames {
                    names: store_names.iter().map(|name| (*name).to_owned()).collect(),
                }),
                ..FlowConfig::default()
            }),
            ..FlowStartOptions::default()
        });
        self.harness.start_flow(request, settings())
    }

    fn set_attributes(&self, flow_id: &str, run_id: RunId, writes: Vec<AttributeWrite>) {
        let command = FlowCommand {
            flow_id: flow_id.to_owned(),
            run_id: run_id.as_bytes().to_vec(),
            command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
                request: Some(SetAttributesRequest {
                    flow_id: flow_id.to_owned(),
                    run_id: run_id.to_string(),
                    attributes: writes,
                    ..SetAttributesRequest::default()
                }),
                ..AttributesSetCommand::default()
            })),
            ..FlowCommand::default()
        };
        let reply = self
            .harness
            .runtime
            .commit(
                &EffectHarness::flow(flow_id),
                command,
                CommitUrgency::Prompt,
            )
            .expect("SetAttributes commits");
        assert!(!is_error_reply(&reply), "SetAttributes failed: {reply:?}");
    }

    /// Returns the committed pending index keys of every store and partition.
    fn pending_run_count(&self) -> usize {
        self.harness
            .runtime
            .snapshot()
            .rows
            .keys()
            .filter(|(column_family, key)| {
                *column_family == ColumnFamily::PartitionIndex
                    && key.get(2) == Some(&(IndexKind::AttributeSyncPending as u8))
            })
            .count()
    }

    fn acks(&self) -> Vec<dexzd_protocol::dexzd::v1::AttributeSyncAckCommand> {
        self.harness
            .runtime
            .commands_matching(|command| {
                matches!(command, flow_command::Command::AttributeSyncAck(_))
            })
            .into_iter()
            .filter_map(|recorded| match recorded.command.command {
                Some(flow_command::Command::AttributeSyncAck(ack)) => Some(ack),
                _ => None,
            })
            .collect()
    }

    fn dropped_effect_count(&self) -> usize {
        self.harness
            .runtime
            .published_effects
            .lock()
            .expect("lock")
            .iter()
            .filter(|effect| matches!(effect, ApplyEffect::AttributeSyncDropped { .. }))
            .count()
    }

    /// Advances paused time in small steps until `condition` holds.
    async fn wait_until(&self, description: &str, condition: impl Fn(&Self) -> bool) {
        for _ in 0..2_000 {
            self.harness.settle().await;
            if condition(self) {
                return;
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
        panic!("timed out waiting until {description}");
    }

    async fn shutdown(mut self) {
        if let Some(projector) = self.projector.take() {
            assert!(projector.shutdown(Duration::from_secs(1)).await);
        }
        self.harness.shutdown().await;
    }
}

fn sync_config() -> AttributeSyncConfig {
    AttributeSyncConfig {
        store_names: vec![STORE.to_owned()],
        batch_size: 100,
        retry_policy: AttributeSyncRetryPolicy {
            initial_interval: Duration::from_millis(100),
            maximum_interval: Duration::from_secs(1),
            backoff_coefficient: 2.0,
            maximum_attempts: 0,
            total_duration: Duration::from_secs(3600),
        },
        transaction_timeout: Duration::from_secs(2),
        lease_margin: Duration::from_secs(1),
    }
}

fn synced(key: &str, value: Option<Value>) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value,
        sync_config: Some(WriteSyncConfig { enabled: true }),
        ..AttributeWrite::default()
    }
}

fn unsynced(key: &str, value: Value) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value: Some(value),
        ..AttributeWrite::default()
    }
}

fn item(key: &str, value: Option<Value>) -> AttributeSyncItem {
    AttributeSyncItem {
        key: key.to_owned(),
        value,
    }
}

#[tokio::test(start_paused = true)]
async fn writes_complete_latest_values_with_the_node_fence_then_acknowledges() {
    let fixture = ProjectorFixture::start(sync_config());
    let large_text = "x".repeat(4096);
    let run_id = fixture.start_flow(
        "order-1",
        vec![
            synced("Status", Some(string_value("new"))),
            synced("Notes", Some(string_value(&large_text))),
            synced("Members/account-7", Some(string_value("owner"))),
            unsynced("Internal", string_value("hidden")),
        ],
    );
    fixture
        .wait_until("the pending entries are acknowledged", |fixture| {
            fixture.pending_run_count() == 0 && !fixture.acks().is_empty()
        })
        .await;
    let writes = fixture.store.committed_writes();
    assert_eq!(writes.len(), 1, "{writes:?}");
    assert_eq!(writes[0].store_name, STORE);
    assert_eq!(writes[0].flow_id, "order-1");
    assert_eq!(
        writes[0].items,
        vec![
            item("Members/account-7", Some(string_value("owner"))),
            item("Notes", Some(string_value(&large_text))),
            item("Status", Some(string_value("new"))),
        ]
    );
    assert_eq!(
        writes[0].fence,
        AttributeSyncFence {
            cluster_id: "01010101-0101-0101-0101-010101010101".to_owned(),
            scope: "node:node-a".to_owned(),
            token: 7,
        }
    );
    let acks = fixture.acks();
    assert_eq!(acks.len(), 1);
    assert!(!acks[0].is_dropped);
    assert!(acks[0].attribute_keys.is_empty());

    fixture.set_attributes(
        "order-1",
        run_id,
        vec![
            synced("Status", Some(string_value("paid"))),
            synced("Members/account-7", None),
        ],
    );
    fixture
        .wait_until(
            "the second write is acknowledged and rescanned",
            |fixture| {
                fixture.store.committed_writes().len() == 2
                    && fixture.pending_run_count() == 0
                    && fixture.metrics.attribute_sync_backlog_entries.get() == 0
            },
        )
        .await;
    assert_eq!(
        fixture.store.committed_writes()[1].items,
        vec![
            item("Members/account-7", None),
            item("Status", Some(string_value("paid"))),
        ]
    );
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn acknowledges_only_after_the_write_commits() {
    let fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .is_holding_writes
        .store(true, Ordering::SeqCst);
    let write_started = fixture.store.write_started.notified();
    fixture.start_flow("order-2", vec![synced("Status", Some(string_value("new")))]);
    write_started.await;
    tokio::time::sleep(Duration::from_millis(500)).await;
    fixture.harness.settle().await;
    assert!(fixture.acks().is_empty(), "no ack while the write is open");
    assert_eq!(fixture.pending_run_count(), 1);
    fixture.store.release_writes();
    fixture
        .wait_until("the ack commits after the write", |fixture| {
            fixture.acks().len() == 1 && fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(fixture.store.committed_writes().len(), 1);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn retries_with_backoff_and_drops_entries_after_the_total_duration() {
    let mut config = sync_config();
    config.retry_policy.total_duration = Duration::from_secs(5);
    let fixture = ProjectorFixture::start(config);
    fixture
        .store
        .fail_until_cleared(AttributeSyncWriteError::Retryable {
            detail: "connection refused".to_owned(),
        });
    let run_id = fixture.start_flow("order-3", vec![synced("Status", Some(string_value("new")))]);
    fixture
        .wait_until("several attempts failed", |fixture| {
            fixture.store.attempt_count() >= 4
        })
        .await;
    assert!(
        fixture.acks().is_empty(),
        "failed writes are not acknowledged"
    );
    assert!(
        fixture
            .metrics
            .attribute_sync_write_failures
            .count(AttributeSyncFailureKind::Retryable)
            >= 4
    );
    let attempts_before_cap = fixture.store.attempt_count();
    tokio::time::sleep(Duration::from_millis(100)).await;
    fixture.harness.settle().await;
    assert!(
        fixture.store.attempt_count() <= attempts_before_cap + 1,
        "backoff spaces the attempts"
    );
    fixture
        .wait_until("the entry is dropped after totalDuration", |fixture| {
            fixture.acks().iter().any(|ack| ack.is_dropped) && fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(fixture.dropped_effect_count(), 1);
    assert!(fixture.store.committed_writes().is_empty());

    fixture.store.clear_failure();
    fixture.set_attributes(
        "order-3",
        run_id,
        vec![synced("Status", Some(string_value("paid")))],
    );
    fixture
        .wait_until("a later write syncs normally", |fixture| {
            fixture.store.committed_writes().len() == 1 && fixture.pending_run_count() == 0
        })
        .await;
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_rejected_write_is_dropped_without_retrying() {
    let fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .queued_failures
        .lock()
        .expect("lock")
        .push_back(AttributeSyncWriteError::Rejected {
            detail: "Flow ID does not match rowKey".to_owned(),
        });
    fixture.start_flow("order-4", vec![synced("Status", Some(string_value("new")))]);
    fixture
        .wait_until("the rejected entry is dropped", |fixture| {
            fixture.acks().iter().any(|ack| ack.is_dropped) && fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(fixture.store.attempt_count(), 1);
    assert_eq!(
        fixture
            .metrics
            .attribute_sync_write_failures
            .count(AttributeSyncFailureKind::NonRetryable),
        1
    );
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn maximum_attempts_drops_the_entries_of_a_failing_run() {
    let mut config = sync_config();
    config.retry_policy.maximum_attempts = 3;
    let fixture = ProjectorFixture::start(config);
    fixture
        .store
        .fail_until_cleared(AttributeSyncWriteError::Retryable {
            detail: "connection reset".to_owned(),
        });
    fixture.start_flow("order-5", vec![synced("Status", Some(string_value("new")))]);
    fixture
        .wait_until("the entry is dropped after three attempts", |fixture| {
            fixture.acks().iter().any(|ack| ack.is_dropped) && fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(fixture.store.attempt_count(), 3);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_reused_flow_id_writes_the_older_run_first_and_skips_keys_the_newer_run_wrote() {
    let fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .fail_until_cleared(AttributeSyncWriteError::Retryable {
            detail: "database is down".to_owned(),
        });
    fixture.start_flow(
        "order-6",
        vec![
            synced("Status", Some(string_value("old"))),
            synced("Owner", Some(string_value("alice"))),
        ],
    );
    fixture.start_flow("order-6", vec![synced("Status", Some(string_value("new")))]);
    fixture
        .wait_until("both runs are pending and failing", |fixture| {
            fixture.store.attempt_count() >= 2 && fixture.pending_run_count() == 2
        })
        .await;
    let failed_writes = fixture.store.writes.lock().expect("lock").clone();
    assert!(
        failed_writes
            .iter()
            .all(|write| write.items == vec![item("Owner", Some(string_value("alice")))]),
        "the newer run waits while the older run fails: {failed_writes:?}"
    );
    fixture.store.clear_failure();
    fixture
        .wait_until("both runs are acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let writes = fixture.store.committed_writes();
    assert_eq!(
        writes
            .iter()
            .map(|write| write.items.clone())
            .collect::<Vec<_>>(),
        vec![
            vec![item("Owner", Some(string_value("alice")))],
            vec![item("Status", Some(string_value("new")))],
        ],
        "the older run's Status is superseded and its other keys land first"
    );
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn writes_wait_until_the_lease_has_room_for_a_whole_transaction() {
    let fixture = ProjectorFixture::start_with_lease(sync_config(), Duration::from_millis(2_500));
    fixture.start_flow("order-7", vec![synced("Status", Some(string_value("new")))]);
    fixture
        .wait_until("the backlog is observed", |fixture| {
            fixture.metrics.attribute_sync_backlog_entries.get() == 1
        })
        .await;
    assert_eq!(
        fixture.store.attempt_count(),
        0,
        "2.5 s of lease cannot fit a 2 s transaction plus a 1 s margin"
    );
    fixture.lease.send_replace(LeaseStatus::ValidUntil(
        Instant::now() + Duration::from_secs(60),
    ));
    fixture
        .wait_until("the renewed lease lets the write start", |fixture| {
            fixture.store.committed_writes().len() == 1 && fixture.pending_run_count() == 0
        })
        .await;
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn shutdown_abandons_an_in_flight_write_before_returning() {
    let mut fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .is_holding_writes
        .store(true, Ordering::SeqCst);
    let write_started = fixture.store.write_started.notified();
    fixture.start_flow("order-8", vec![synced("Status", Some(string_value("new")))]);
    write_started.await;
    let projector = fixture.projector.take().expect("the projector runs");
    assert!(projector.shutdown(Duration::from_secs(1)).await);
    assert_eq!(fixture.store.abandoned_writes.load(Ordering::SeqCst), 1);
    assert!(fixture.acks().is_empty());
    assert_eq!(
        fixture.pending_run_count(),
        1,
        "entries stay for the next owner"
    );
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn lease_loss_abandons_the_write_and_stops_the_projector() {
    let mut fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .is_holding_writes
        .store(true, Ordering::SeqCst);
    let write_started = fixture.store.write_started.notified();
    fixture.start_flow("order-9", vec![synced("Status", Some(string_value("new")))]);
    write_started.await;
    fixture
        .lease
        .send_replace(LeaseStatus::Lost("renewal precondition failed".to_owned()));
    fixture
        .wait_until("the in-flight write is abandoned", |fixture| {
            fixture.store.abandoned_writes.load(Ordering::SeqCst) == 1
        })
        .await;
    let projector = fixture.projector.take().expect("the projector ran");
    assert!(projector.has_stopped(), "the projector stopped by itself");
    assert!(fixture.acks().is_empty());
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_fenced_write_stops_the_projector_without_acknowledging() {
    let mut fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .queued_failures
        .lock()
        .expect("lock")
        .push_back(AttributeSyncWriteError::Fenced {
            detail: "a newer token is recorded".to_owned(),
        });
    let run_id = fixture.start_flow(
        "order-10",
        vec![synced("Status", Some(string_value("new")))],
    );
    fixture
        .wait_until("the fenced write is counted", |fixture| {
            fixture.metrics.attribute_sync_fenced_writes.get() == 1
        })
        .await;
    fixture.set_attributes(
        "order-10",
        run_id,
        vec![synced("Status", Some(string_value("paid")))],
    );
    tokio::time::sleep(Duration::from_secs(5)).await;
    fixture.harness.settle().await;
    assert_eq!(
        fixture.store.attempt_count(),
        1,
        "a fenced projector writes nothing more"
    );
    assert!(fixture.acks().is_empty());
    let projector = fixture.projector.take().expect("the projector ran");
    assert!(projector.has_stopped());
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_elapsed_backoff_of_one_store_does_not_spin_another_stores_retries() {
    let mut config = sync_config();
    config.store_names = vec!["alpha".to_owned(), "beta".to_owned()];
    let fixture = ProjectorFixture::start(config);
    fixture
        .store
        .queued_store_failures
        .lock()
        .expect("lock")
        .insert(
            "alpha".to_owned(),
            VecDeque::from([
                AttributeSyncWriteError::Retryable {
                    detail: "connection reset".to_owned(),
                },
                AttributeSyncWriteError::Rejected {
                    detail: "Flow ID does not match rowKey".to_owned(),
                },
            ]),
        );
    fixture
        .store
        .persistent_store_failures
        .lock()
        .expect("lock")
        .insert(
            "beta".to_owned(),
            AttributeSyncWriteError::Retryable {
                detail: "database is down".to_owned(),
            },
        );
    fixture.start_flow_with_stores(
        "order-11",
        &["alpha", "beta"],
        vec![synced("Status", Some(string_value("new")))],
    );
    fixture
        .wait_until("alpha's entry is dropped", |fixture| {
            fixture
                .acks()
                .iter()
                .any(|ack| ack.is_dropped && ack.store_name == "alpha")
        })
        .await;
    let reads_before = fixture
        .harness
        .runtime
        .committed_reads
        .load(Ordering::SeqCst);
    tokio::time::sleep(Duration::from_secs(5)).await;
    fixture.harness.settle().await;
    let reads = fixture
        .harness
        .runtime
        .committed_reads
        .load(Ordering::SeqCst)
        - reads_before;
    assert!(
        reads < 40,
        "beta retries follow its backoff instead of alpha's elapsed one: {reads} reads"
    );
    assert!(fixture.acks().iter().all(|ack| ack.store_name == "alpha"));
    fixture.shutdown().await;
}
