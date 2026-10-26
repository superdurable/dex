// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attribute Sync projector tests: the real apply marks and acknowledges entries, and a fake
//! Attribute Store records writes.

use std::collections::{BTreeMap, BTreeSet, VecDeque};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_apply::{ApplyEffect, FlowStateReader};
use dexzd_cluster_metadata::LeaseStatus;
use dexzd_keyspace::row_keys::{attribute_sync_outbox_key, run_named_key};
use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionId, RunId, RunTable};
use dexzd_metrics::{AttributeSyncFailureKind, NodeMetrics};
use dexzd_protocol::dex::{
    AttributeStoreNames, AttributeSyncConfig as WriteSyncConfig, AttributeWrite, CloseDecision,
    CloseDecisionType, FlowConfig, FlowResetType, FlowStartOptions, FlowStatus, IdReusePolicy,
    InvokeExecuteMethodResponse, ResetFlowRequest, SetAttributesRequest, StepDecision, StepOptions,
    Value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, AttributeSyncOutboxRecord, AttributesSetCommand, FlowCommand,
    StepInvocationKey, StepMethod, StepMethodCompletedCommand, TimeTravelCommand, flow_command,
    step_method_completed_command,
};
use futures::future::BoxFuture;
use prost::Message;
use tokio::sync::{Notify, watch};
use tokio::time::Instant;

use super::{EffectHarness, is_error_reply, settings, start_request, string_value};
use crate::attribute_sync::{
    AttributeSyncConfig, AttributeSyncDependencies, AttributeSyncFence, AttributeSyncItem,
    AttributeSyncProjector, AttributeSyncProjectorHandle, AttributeSyncRetryPolicy,
    AttributeSyncWaker, AttributeSyncWriteError, AttributeSyncWriter,
};
use crate::effects::{EffectRuntime, EffectsConfig};
use crate::runtime::CommitUrgency;

const STORE: &str = "orders";
/// The fence token of the first node; a restarted node takes the next one.
const NODE_EPOCH: u64 = 7;

/// One write the fake store accepted or refused.
#[derive(Debug, Clone, PartialEq)]
struct RecordedWrite {
    store_name: String,
    flow_id: String,
    items: Vec<AttributeSyncItem>,
    fence: AttributeSyncFence,
    is_committed: bool,
    finished_at: Instant,
}

/// An Attribute Store that records writes, fails on request, and can hold writes open.
#[derive(Default)]
struct FakeAttributeStore {
    writes: Mutex<Vec<RecordedWrite>>,
    queued_failures: Mutex<VecDeque<AttributeSyncWriteError>>,
    persistent_failure: Mutex<Option<AttributeSyncWriteError>>,
    queued_store_failures: Mutex<BTreeMap<String, VecDeque<AttributeSyncWriteError>>>,
    persistent_store_failures: Mutex<BTreeMap<String, AttributeSyncWriteError>>,
    persistent_flow_failures: Mutex<BTreeMap<String, AttributeSyncWriteError>>,
    /// Scripted outcomes of a Flow's next writes; `None` commits.
    queued_flow_outcomes: Mutex<BTreeMap<String, VecDeque<Option<AttributeSyncWriteError>>>>,
    slow_flows: Mutex<BTreeMap<String, SlowWrites>>,
    is_holding_writes: AtomicBool,
    /// Held writes allowed to finish while `is_holding_writes` stays set.
    released_write_permits: AtomicUsize,
    released: Notify,
    write_started: Notify,
    abandoned_writes: AtomicUsize,
}

/// Per-item write latency of one Flow, enforced against the writer's transaction timeout.
#[derive(Clone, Copy)]
struct SlowWrites {
    item_latency: Duration,
    transaction_timeout: Duration,
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

    fn attempts_of_store(&self, store_name: &str) -> Vec<RecordedWrite> {
        self.writes
            .lock()
            .expect("lock")
            .iter()
            .filter(|write| write.store_name == store_name)
            .cloned()
            .collect()
    }

    fn attempt_count_of_flow(&self, flow_id: &str) -> usize {
        self.writes
            .lock()
            .expect("lock")
            .iter()
            .filter(|write| write.flow_id == flow_id)
            .count()
    }

    fn committed_writes_of_flow(&self, flow_id: &str) -> Vec<RecordedWrite> {
        self.committed_writes()
            .into_iter()
            .filter(|write| write.flow_id == flow_id)
            .collect()
    }

    /// Returns the stored values of `flow_id` after every committed write, as a table would hold them.
    fn table_row(&self, flow_id: &str) -> BTreeMap<String, Value> {
        let mut row = BTreeMap::new();
        for write in self.committed_writes_of_flow(flow_id) {
            for item in write.items {
                if let Some(value) = item.value {
                    row.insert(item.key, value);
                } else {
                    row.remove(&item.key);
                }
            }
        }
        row
    }

    /// Lets one held write finish while later writes stay held.
    fn release_one_write(&self) {
        self.released_write_permits.fetch_add(1, Ordering::SeqCst);
        self.released.notify_waiters();
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

    /// Makes each item of `flow_id`'s writes take `item_latency`, timing out like the real writer.
    fn slow_down_flow(&self, flow_id: &str, item_latency: Duration) {
        self.slow_flows.lock().expect("lock").insert(
            flow_id.to_owned(),
            SlowWrites {
                item_latency,
                transaction_timeout: sync_config().transaction_timeout,
            },
        );
    }

    /// Returns the item count of every write attempt of `flow_id` and whether it committed.
    fn attempt_sizes_of_flow(&self, flow_id: &str) -> Vec<(usize, bool)> {
        self.writes
            .lock()
            .expect("lock")
            .iter()
            .filter(|write| write.flow_id == flow_id)
            .map(|write| (write.items.len(), write.is_committed))
            .collect()
    }
}

/// The error the Postgres writer reports when a transaction outlasts its timeout.
fn transaction_timeout_error() -> AttributeSyncWriteError {
    AttributeSyncWriteError::TimedOut {
        detail: "Attribute Store \"orders\" transaction timed out after 2s".to_owned(),
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
                if !self.is_holding_writes.load(Ordering::SeqCst)
                    || self
                        .released_write_permits
                        .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |permits| {
                            permits.checked_sub(1)
                        })
                        .is_ok()
                {
                    break;
                }
                released.await;
            }
            let slow_writes = self.slow_flows.lock().expect("lock").get(flow_id).copied();
            let mut timeout_failure = None;
            if let Some(slow_writes) = slow_writes {
                let item_count = u32::try_from(items.len()).expect("batches are small");
                let write_duration = slow_writes.item_latency * item_count;
                if write_duration > slow_writes.transaction_timeout {
                    tokio::time::sleep(slow_writes.transaction_timeout).await;
                    timeout_failure = Some(transaction_timeout_error());
                } else {
                    tokio::time::sleep(write_duration).await;
                }
            }
            let scripted_outcome = self
                .queued_flow_outcomes
                .lock()
                .expect("lock")
                .get_mut(flow_id)
                .and_then(VecDeque::pop_front);
            let failure = timeout_failure
                .or_else(|| scripted_outcome.flatten())
                .or_else(|| self.queued_failures.lock().expect("lock").pop_front())
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
                })
                .or_else(|| {
                    self.persistent_flow_failures
                        .lock()
                        .expect("lock")
                        .get(flow_id)
                        .cloned()
                });
            self.writes.lock().expect("lock").push(RecordedWrite {
                store_name: store_name.to_owned(),
                flow_id: flow_id.to_owned(),
                items: items.to_vec(),
                fence: fence.clone(),
                is_committed: failure.is_none(),
                finished_at: Instant::now(),
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
    node_epoch: u64,
}

impl ProjectorFixture {
    fn start(config: AttributeSyncConfig) -> Self {
        Self::start_with_lease(config, Duration::from_secs(365 * 24 * 3600))
    }

    fn start_with_lease(config: AttributeSyncConfig, lease_validity: Duration) -> Self {
        let harness = EffectHarness::new();
        let waker = harness.attribute_sync_waker.clone();
        Self::start_on(harness, config, lease_validity, waker)
    }

    /// Starts a projector on `harness` that hears wake-ups only through `waker`.
    fn start_on(
        harness: EffectHarness,
        config: AttributeSyncConfig,
        lease_validity: Duration,
        waker: AttributeSyncWaker,
    ) -> Self {
        Self::start_node(
            harness,
            config,
            lease_validity,
            waker,
            Arc::new(FakeAttributeStore::default()),
            NODE_EPOCH,
        )
    }

    /// Starts a projector on `harness` that writes to `store` with the fence of `node_epoch`.
    fn start_node(
        harness: EffectHarness,
        config: AttributeSyncConfig,
        lease_validity: Duration,
        waker: AttributeSyncWaker,
        store: Arc<FakeAttributeStore>,
        node_epoch: u64,
    ) -> Self {
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
                fence: AttributeSyncFence::for_node_owner([1; 16], "node-a", node_epoch),
                lease_status,
                waker,
            },
            std::future::pending(),
        );
        Self {
            harness,
            store,
            lease,
            metrics,
            projector: Some(projector),
            node_epoch,
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
        start_synced_flow(&self.harness, flow_id, store_names, writes)
    }

    /// Returns the committed seq of a pending entry, or `None` once it was acknowledged.
    fn pending_written_seq(
        &self,
        flow_id: &str,
        run_id: RunId,
        attribute_key: &str,
    ) -> Option<u64> {
        let run = self.harness.run_location(flow_id, run_id);
        let key = attribute_sync_outbox_key(run.flow_hash, run.run_id, STORE, attribute_key);
        let bytes = self
            .harness
            .runtime
            .snapshot()
            .get(ColumnFamily::Flow, &key)
            .expect("memory reads never fail")?;
        Some(
            AttributeSyncOutboxRecord::decode(bytes.as_slice())
                .expect("the outbox record decodes")
                .written_seq,
        )
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

    fn acks(&self) -> Vec<AttributeSyncAckCommand> {
        self.acks_with_flow_ids()
            .into_iter()
            .map(|(_, ack)| ack)
            .collect()
    }

    fn acks_of_flow(&self, flow_id: &str) -> Vec<AttributeSyncAckCommand> {
        self.acks_with_flow_ids()
            .into_iter()
            .filter(|(ack_flow_id, _)| ack_flow_id == flow_id)
            .map(|(_, ack)| ack)
            .collect()
    }

    fn acks_with_flow_ids(&self) -> Vec<(String, AttributeSyncAckCommand)> {
        self.harness
            .runtime
            .commands_matching(|command| {
                matches!(command, flow_command::Command::AttributeSyncAck(_))
            })
            .into_iter()
            .filter_map(|recorded| match recorded.command.command {
                Some(flow_command::Command::AttributeSyncAck(ack)) => {
                    Some((recorded.command.flow_id, ack))
                }
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

    /// Restarts on the next epoch over the same committed state; only the startup scan sees pending entries.
    async fn restart_after_crash(mut self, config: AttributeSyncConfig) -> Self {
        let projector = self.projector.take().expect("the projector runs");
        assert!(projector.shutdown(Duration::from_secs(1)).await);
        let durable = self.harness.durable.clone();
        self.harness.shutdown().await;
        Self::start_node(
            EffectHarness::with_config(durable, EffectsConfig::default()),
            config,
            Duration::from_secs(365 * 24 * 3600),
            AttributeSyncWaker::new(),
            self.store,
            self.node_epoch + 1,
        )
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

fn start_synced_flow(
    harness: &EffectHarness,
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
    harness.start_flow(request, settings())
}

/// Returns the first `{prefix}{n}` Flow ID whose partition satisfies `is_wanted`.
fn flow_id_in_partition(
    harness: &EffectHarness,
    prefix: &str,
    is_wanted: impl Fn(PartitionId) -> bool,
) -> String {
    (0..10_000)
        .map(|n| format!("{prefix}{n}"))
        .find(|flow_id| is_wanted(harness.partition_of(flow_id)))
        .expect("some Flow ID hashes to the wanted partition")
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
    assert_a_reused_flow_id_keeps_run_order(AttributeSyncWriteError::Retryable {
        detail: "database is down".to_owned(),
    })
    .await;
}

#[tokio::test(start_paused = true)]
async fn a_run_scoped_failure_still_holds_back_newer_runs_of_the_same_flow_id() {
    assert_a_reused_flow_id_keeps_run_order(AttributeSyncWriteError::RetryableForRun {
        detail: "value too long for type character varying(8)".to_owned(),
    })
    .await;
}

async fn assert_a_reused_flow_id_keeps_run_order(failure: AttributeSyncWriteError) {
    let fixture = ProjectorFixture::start(sync_config());
    fixture.store.fail_until_cleared(failure);
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

#[tokio::test(start_paused = true)]
async fn a_due_store_retry_does_not_wait_for_another_stores_longer_backoff() {
    let mut config = sync_config();
    config.store_names = vec!["alpha".to_owned(), "beta".to_owned()];
    config.retry_policy.maximum_interval = Duration::from_secs(10);
    let fixture = ProjectorFixture::start(config);
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
        "beta-order",
        &["beta"],
        vec![synced("Status", Some(string_value("new")))],
    );
    fixture
        .wait_until("beta's backoff grows past a second", |fixture| {
            fixture.store.attempts_of_store("beta").len() >= 5
        })
        .await;
    fixture
        .store
        .queued_store_failures
        .lock()
        .expect("lock")
        .insert(
            "alpha".to_owned(),
            VecDeque::from([AttributeSyncWriteError::Retryable {
                detail: "connection reset".to_owned(),
            }]),
        );
    let started_at = Instant::now();
    fixture.start_flow_with_stores(
        "alpha-order",
        &["alpha"],
        vec![synced("Status", Some(string_value("new")))],
    );
    fixture
        .wait_until("alpha's retry commits", |fixture| {
            !fixture
                .store
                .committed_writes_of_flow("alpha-order")
                .is_empty()
        })
        .await;
    assert!(
        started_at.elapsed() < Duration::from_millis(500),
        "alpha retries after its own 100 ms backoff, not beta's: {:?}",
        started_at.elapsed()
    );
    assert_eq!(fixture.store.attempts_of_store("alpha").len(), 2);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_poisoned_run_backs_off_alone_and_is_dropped_after_the_total_duration() {
    let mut config = sync_config();
    config.retry_policy.total_duration = Duration::from_secs(5);
    let fixture = ProjectorFixture::start(config);
    let poisoned = flow_id_in_partition(&fixture.harness, "poisoned-", |partition| partition.0 < 8);
    let poisoned_partition = fixture.harness.partition_of(&poisoned);
    // Flows of one partition are written in Flow ID order, so this one comes after the poisoned run.
    let same_partition = flow_id_in_partition(&fixture.harness, "zz-healthy-", |partition| {
        partition == poisoned_partition
    });
    let higher_partition = flow_id_in_partition(&fixture.harness, "healthy-", |partition| {
        partition > poisoned_partition
    });
    fixture
        .store
        .persistent_flow_failures
        .lock()
        .expect("lock")
        .insert(
            poisoned.clone(),
            AttributeSyncWriteError::RetryableForRun {
                detail: "value too long for type character varying(8)".to_owned(),
            },
        );
    fixture.start_flow(
        &poisoned,
        vec![synced("Status", Some(string_value("much too long")))],
    );
    fixture
        .wait_until(
            "the poisoned run's backoff reaches maximumInterval",
            |fixture| fixture.store.attempt_count_of_flow(&poisoned) >= 5,
        )
        .await;

    let started_at = Instant::now();
    let same_partition_run = fixture.start_flow(
        &same_partition,
        vec![synced("Status", Some(string_value("new")))],
    );
    let higher_partition_run = fixture.start_flow(
        &higher_partition,
        vec![synced("Status", Some(string_value("new")))],
    );
    fixture
        .wait_until("both healthy runs are acknowledged", |fixture| {
            !fixture.acks_of_flow(&same_partition).is_empty()
                && !fixture.acks_of_flow(&higher_partition).is_empty()
        })
        .await;
    assert!(
        started_at.elapsed() < Duration::from_millis(100),
        "healthy runs wait for no backoff: {:?}",
        started_at.elapsed()
    );

    let changed_at = Instant::now();
    fixture.set_attributes(
        &same_partition,
        same_partition_run,
        vec![synced("Status", Some(string_value("paid")))],
    );
    fixture.set_attributes(
        &higher_partition,
        higher_partition_run,
        vec![synced("Status", Some(string_value("paid")))],
    );
    fixture
        .wait_until("the healthy changes are written", |fixture| {
            fixture
                .store
                .committed_writes_of_flow(&same_partition)
                .len()
                == 2
                && fixture
                    .store
                    .committed_writes_of_flow(&higher_partition)
                    .len()
                    == 2
        })
        .await;
    assert!(
        changed_at.elapsed() < Duration::from_millis(100),
        "later changes wait for no backoff either: {:?}",
        changed_at.elapsed()
    );
    assert!(fixture.acks_of_flow(&poisoned).is_empty());

    fixture
        .wait_until(
            "the poisoned entry is dropped after totalDuration",
            |fixture| {
                fixture
                    .acks_of_flow(&poisoned)
                    .iter()
                    .any(|ack| ack.is_dropped)
                    && fixture.pending_run_count() == 0
            },
        )
        .await;
    assert_eq!(fixture.dropped_effect_count(), 1);
    assert!(fixture.store.committed_writes_of_flow(&poisoned).is_empty());
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_store_scoped_failure_backs_off_every_run_of_that_store_but_not_another_store() {
    let mut config = sync_config();
    config.store_names = vec!["alpha".to_owned(), "beta".to_owned()];
    let fixture = ProjectorFixture::start(config);
    let first_alpha = flow_id_in_partition(&fixture.harness, "alpha-order-", |partition| {
        partition.0 < 8
    });
    let first_alpha_partition = fixture.harness.partition_of(&first_alpha);
    let second_alpha = flow_id_in_partition(&fixture.harness, "alpha-order-", |partition| {
        partition > first_alpha_partition
    });
    let beta = flow_id_in_partition(&fixture.harness, "beta-order-", |partition| {
        partition > first_alpha_partition
    });
    fixture
        .store
        .persistent_store_failures
        .lock()
        .expect("lock")
        .insert(
            "alpha".to_owned(),
            AttributeSyncWriteError::Retryable {
                detail: "connection refused".to_owned(),
            },
        );
    let started_at = Instant::now();
    for flow_id in [&first_alpha, &second_alpha] {
        fixture.start_flow_with_stores(
            flow_id,
            &["alpha"],
            vec![synced("Status", Some(string_value("new")))],
        );
    }
    fixture.start_flow_with_stores(
        &beta,
        &["beta"],
        vec![synced("Status", Some(string_value("new")))],
    );
    fixture
        .wait_until("the beta run is acknowledged", |fixture| {
            !fixture.acks_of_flow(&beta).is_empty()
        })
        .await;
    assert!(
        started_at.elapsed() < Duration::from_millis(100),
        "another store waits for no backoff: {:?}",
        started_at.elapsed()
    );
    tokio::time::sleep(Duration::from_secs(3)).await;
    fixture.harness.settle().await;
    let alpha_attempts = fixture.store.attempts_of_store("alpha");
    assert!(alpha_attempts.len() >= 4, "{alpha_attempts:?}");
    for attempts in alpha_attempts.windows(2) {
        assert!(
            attempts[1].finished_at - attempts[0].finished_at >= Duration::from_millis(100),
            "every alpha run waits for the one store backoff: {alpha_attempts:?}"
        );
    }

    fixture
        .store
        .persistent_store_failures
        .lock()
        .expect("lock")
        .clear();
    fixture
        .wait_until("both alpha runs sync once the store recovers", |fixture| {
            !fixture.acks_of_flow(&first_alpha).is_empty()
                && !fixture.acks_of_flow(&second_alpha).is_empty()
                && fixture.pending_run_count() == 0
        })
        .await;
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn entries_committed_before_the_projector_starts_are_synced_by_the_startup_scan() {
    let harness = EffectHarness::new();
    let flow_ids: BTreeSet<String> = (0..4).map(|n| format!("order-before-start-{n}")).collect();
    for flow_id in &flow_ids {
        start_synced_flow(
            &harness,
            flow_id,
            &[STORE],
            vec![synced("Status", Some(string_value("new")))],
        );
    }
    harness.settle().await;
    // A fresh waker never heard these marks, so only the startup scan can find them.
    let fixture = ProjectorFixture::start_on(
        harness,
        sync_config(),
        Duration::from_secs(365 * 24 * 3600),
        AttributeSyncWaker::new(),
    );
    fixture
        .wait_until("every entry is written and acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let written_flow_ids: BTreeSet<String> = fixture
        .store
        .committed_writes()
        .into_iter()
        .map(|write| write.flow_id)
        .collect();
    assert_eq!(written_flow_ids, flow_ids);
    assert_eq!(fixture.acks().len(), flow_ids.len());
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_unsynced_write_after_a_pending_synced_write_does_not_reach_the_store() {
    let harness = EffectHarness::new();
    let run_id = start_synced_flow(
        &harness,
        "unsynced-after-pending",
        &[STORE],
        vec![synced("Status", Some(string_value("opened")))],
    );
    let flow = EffectHarness::flow("unsynced-after-pending");
    let command = FlowCommand {
        flow_id: "unsynced-after-pending".to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: "unsynced-after-pending".to_owned(),
                run_id: run_id.to_string(),
                attributes: vec![unsynced("Status", string_value("quiet"))],
                ..SetAttributesRequest::default()
            }),
            ..AttributesSetCommand::default()
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .runtime
        .commit(&flow, command, CommitUrgency::Prompt)
        .expect("unsynced write commits");
    assert!(!is_error_reply(&reply));
    harness.settle().await;

    let fixture = ProjectorFixture::start_on(
        harness,
        sync_config(),
        Duration::from_secs(365 * 24 * 3600),
        AttributeSyncWaker::new(),
    );
    fixture
        .wait_until("the pending entry is acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let writes = fixture.store.committed_writes();
    assert_eq!(writes.len(), 1, "{writes:?}");
    assert_eq!(
        writes[0].items,
        vec![item("Status", Some(string_value("opened")))],
    );
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn time_travel_to_an_unsynced_value_deletes_the_store_row() {
    const FLOW_ID: &str = "reset-to-unsynced";
    let harness = EffectHarness::new();
    let run_id = start_synced_flow(
        &harness,
        FLOW_ID,
        &[STORE],
        vec![unsynced("Status", string_value("draft"))],
    );
    let flow = EffectHarness::flow(FLOW_ID);
    let set_command = FlowCommand {
        flow_id: FLOW_ID.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: FLOW_ID.to_owned(),
                run_id: run_id.to_string(),
                attributes: vec![synced("Status", Some(string_value("final")))],
                ..SetAttributesRequest::default()
            }),
            ..AttributesSetCommand::default()
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .runtime
        .commit(&flow, set_command, CommitUrgency::Prompt)
        .expect("synced write commits");
    assert!(!is_error_reply(&reply));
    let ack_command = FlowCommand {
        flow_id: FLOW_ID.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::AttributeSyncAck(
            AttributeSyncAckCommand {
                store_name: STORE.to_owned(),
                acked_seq: u64::MAX,
                attribute_keys: Vec::new(),
                is_dropped: false,
            },
        )),
        ..FlowCommand::default()
    };
    let reply = harness
        .runtime
        .commit(&flow, ack_command, CommitUrgency::Prompt)
        .expect("sync ack commits");
    assert!(!is_error_reply(&reply));
    let reset_command = FlowCommand {
        flow_id: FLOW_ID.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::TimeTravel(TimeTravelCommand {
            request: Some(ResetFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                reset_type: FlowResetType::Beginning as i32,
                reason: "test".to_owned(),
                skip_writes_reapply: true,
                ..ResetFlowRequest::default()
            }),
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .runtime
        .commit(&flow, reset_command, CommitUrgency::Prompt)
        .expect("reset commits");
    assert!(!is_error_reply(&reply));
    harness.settle().await;

    let fixture = ProjectorFixture::start_on(
        harness,
        sync_config(),
        Duration::from_secs(365 * 24 * 3600),
        AttributeSyncWaker::new(),
    );
    fixture
        .wait_until("the reset entry is acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let writes = fixture.store.committed_writes();
    assert_eq!(writes.len(), 1, "{writes:?}");
    assert_eq!(writes[0].items, vec![item("Status", None)]);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn time_travel_refreshes_a_pending_entry_when_the_restored_attribute_is_missing() {
    assert_pending_reset_store_row(Vec::new(), "reset-pending-missing", true, None).await;
}

#[tokio::test(start_paused = true)]
async fn time_travel_refreshes_a_pending_entry_when_the_restored_attribute_is_unsynced() {
    assert_pending_reset_store_row(
        vec![unsynced("Status", string_value("draft"))],
        "reset-pending-unsynced",
        true,
        None,
    )
    .await;
}

#[tokio::test(start_paused = true)]
async fn time_travel_reapplies_a_synced_snapshot_before_a_later_unsynced_write() {
    assert_pending_reset_store_row(
        Vec::new(),
        "reset-reapply-pending",
        false,
        Some(string_value("opened")),
    )
    .await;
}

async fn assert_pending_reset_store_row(
    initial_attributes: Vec<AttributeWrite>,
    flow_id: &str,
    skip_writes_reapply: bool,
    expected_value: Option<Value>,
) {
    let harness = EffectHarness::new();
    let run_id = start_synced_flow(&harness, flow_id, &[STORE], initial_attributes);
    let flow = EffectHarness::flow(flow_id);
    for (value, sync_enabled) in [("opened", true), ("quiet", false)] {
        let write = if sync_enabled {
            synced("Status", Some(string_value(value)))
        } else {
            unsynced("Status", string_value(value))
        };
        let reply = harness
            .runtime
            .commit(
                &flow,
                FlowCommand {
                    flow_id: flow_id.to_owned(),
                    run_id: run_id.as_bytes().to_vec(),
                    command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
                        request: Some(SetAttributesRequest {
                            flow_id: flow_id.to_owned(),
                            run_id: run_id.to_string(),
                            attributes: vec![write],
                            ..SetAttributesRequest::default()
                        }),
                        ..AttributesSetCommand::default()
                    })),
                    ..FlowCommand::default()
                },
                CommitUrgency::Prompt,
            )
            .expect("Attribute write commits");
        assert!(!is_error_reply(&reply));
    }
    let reply = harness
        .runtime
        .commit(
            &flow,
            FlowCommand {
                flow_id: flow_id.to_owned(),
                run_id: run_id.as_bytes().to_vec(),
                command: Some(flow_command::Command::TimeTravel(TimeTravelCommand {
                    request: Some(ResetFlowRequest {
                        flow_id: flow_id.to_owned(),
                        reset_type: FlowResetType::Beginning as i32,
                        reason: "test".to_owned(),
                        skip_writes_reapply,
                        ..ResetFlowRequest::default()
                    }),
                })),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("reset commits");
    assert!(!is_error_reply(&reply));
    harness.settle().await;
    let fixture = ProjectorFixture::start_on(
        harness,
        sync_config(),
        Duration::from_secs(365 * 24 * 3600),
        AttributeSyncWaker::new(),
    );
    fixture
        .wait_until("the reset entry is acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let writes = fixture.store.committed_writes();
    assert_eq!(writes.len(), 1, "{writes:?}");
    assert_eq!(writes[0].items, vec![item("Status", expected_value)]);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn legacy_pending_entries_read_only_synced_current_values() {
    const FLOW_ID: &str = "legacy-pending-entries";
    let harness = EffectHarness::new();
    let run_id = start_synced_flow(
        &harness,
        FLOW_ID,
        &[STORE],
        vec![
            synced("Synced", Some(string_value("ready"))),
            synced("Unsynced", Some(string_value("old"))),
            synced("Missing", Some(string_value("old"))),
        ],
    );
    let reply = harness
        .runtime
        .commit(
            &EffectHarness::flow(FLOW_ID),
            FlowCommand {
                flow_id: FLOW_ID.to_owned(),
                run_id: run_id.as_bytes().to_vec(),
                command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
                    request: Some(SetAttributesRequest {
                        flow_id: FLOW_ID.to_owned(),
                        run_id: run_id.to_string(),
                        attributes: vec![unsynced("Unsynced", string_value("private"))],
                        ..SetAttributesRequest::default()
                    }),
                    ..AttributesSetCommand::default()
                })),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("unsynced write commits");
    assert!(!is_error_reply(&reply));
    let run = harness.run_location(FLOW_ID, run_id);
    {
        let mut state = harness.runtime.state.lock().expect("lock");
        state.rows.remove(&(
            ColumnFamily::Flow,
            run_named_key(run.flow_hash, run.run_id, RunTable::Attribute, "Missing"),
        ));
        for attribute_key in ["Synced", "Unsynced", "Missing"] {
            let key = attribute_sync_outbox_key(run.flow_hash, run.run_id, STORE, attribute_key);
            let bytes = state
                .rows
                .get_mut(&(ColumnFamily::Flow, key))
                .expect("pending entry exists");
            let mut entry =
                AttributeSyncOutboxRecord::decode(bytes.as_slice()).expect("pending entry decodes");
            entry.sync_item = None;
            *bytes = entry.encode_to_vec();
        }
    }
    harness.settle().await;
    let fixture = ProjectorFixture::start_on(
        harness,
        sync_config(),
        Duration::from_secs(365 * 24 * 3600),
        AttributeSyncWaker::new(),
    );
    fixture
        .wait_until("legacy entries are acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let writes = fixture.store.committed_writes();
    assert_eq!(writes.len(), 1, "{writes:?}");
    assert_eq!(
        writes[0].items,
        vec![
            item("Missing", None),
            item("Synced", Some(string_value("ready")))
        ],
    );
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_crash_after_the_store_commit_and_before_the_ack_commits_rewrites_the_entries_on_restart()
{
    assert_a_crash_before_the_ack_commits_delivers_the_latest_values(false).await;
}

#[tokio::test(start_paused = true)]
async fn a_crash_before_the_ack_commits_writes_values_committed_after_the_store_read_on_restart() {
    assert_a_crash_before_the_ack_commits_delivers_the_latest_values(true).await;
}

/// After the store commit and before the ack commits, a restart rewrites the latest values.
async fn assert_a_crash_before_the_ack_commits_delivers_the_latest_values(has_newer_write: bool) {
    const FLOW_ID: &str = "order-crash";
    let fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .is_holding_writes
        .store(true, Ordering::SeqCst);
    let write_started = fixture.store.write_started.notified();
    let run_id = fixture.start_flow(
        FLOW_ID,
        vec![
            synced("Status", Some(string_value("new"))),
            synced("Members/account-1", Some(string_value("owner"))),
        ],
    );
    write_started.await;
    let first_items = vec![
        item("Members/account-1", Some(string_value("owner"))),
        item("Status", Some(string_value("new"))),
    ];
    let latest_items = if has_newer_write {
        fixture.set_attributes(
            FLOW_ID,
            run_id,
            vec![
                synced("Status", Some(string_value("paid"))),
                synced("Members/account-1", None),
            ],
        );
        vec![
            item("Members/account-1", None),
            item("Status", Some(string_value("paid"))),
        ]
    } else {
        first_items.clone()
    };
    let pending_seq = fixture
        .pending_written_seq(FLOW_ID, run_id, "Status")
        .expect("the entry is pending");
    // From here every proposal of this node fails, so its ack cannot commit before the crash.
    fixture
        .harness
        .runtime
        .unavailable_proposals
        .store(usize::MAX, Ordering::SeqCst);
    fixture.store.release_writes();
    fixture
        .wait_until(
            "the store commits and a proposal of this node fails",
            |fixture| {
                !fixture.store.committed_writes().is_empty()
                    && fixture
                        .harness
                        .runtime
                        .unavailable_proposals
                        .load(Ordering::SeqCst)
                        < usize::MAX
            },
        )
        .await;
    let committed_before_crash = fixture.store.committed_writes();
    assert_eq!(committed_before_crash.len(), 1);
    assert_eq!(committed_before_crash[0].items, first_items);
    assert!(
        fixture.acks().is_empty(),
        "no ack committed before the crash"
    );
    assert_eq!(
        fixture.pending_written_seq(FLOW_ID, run_id, "Status"),
        Some(pending_seq)
    );

    let fixture = fixture.restart_after_crash(sync_config()).await;
    fixture
        .wait_until(
            "the restarted node writes and acknowledges the entries",
            |fixture| fixture.pending_run_count() == 0,
        )
        .await;
    let writes = fixture.store.committed_writes();
    assert_eq!(writes.len(), 2, "{writes:?}");
    assert_eq!(writes[1].items, latest_items);
    assert_eq!(writes[1].fence.token, NODE_EPOCH + 1);
    let expected_row: BTreeMap<String, Value> = latest_items
        .into_iter()
        .filter_map(|item| Some((item.key, item.value?)))
        .collect();
    assert_eq!(fixture.store.table_row(FLOW_ID), expected_row);
    let acks = fixture.acks();
    assert_eq!(acks.len(), 1, "{acks:?}");
    assert!(!acks[0].is_dropped);
    assert!(acks[0].acked_seq >= pending_seq, "{acks:?}");
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_change_committed_during_an_in_flight_write_stays_pending_and_is_written_next() {
    let fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .is_holding_writes
        .store(true, Ordering::SeqCst);
    let first_write_started = fixture.store.write_started.notified();
    let run_id = fixture.start_flow(
        "order-12",
        vec![synced("Status", Some(string_value("new")))],
    );
    first_write_started.await;
    fixture.set_attributes(
        "order-12",
        run_id,
        vec![synced("Status", Some(string_value("paid")))],
    );
    let changed_seq = fixture
        .pending_written_seq("order-12", run_id, "Status")
        .expect("the change is pending");
    let second_write_started = fixture.store.write_started.notified();
    fixture.store.release_one_write();
    second_write_started.await;
    let acks = fixture.acks();
    assert_eq!(acks.len(), 1, "the first write is acknowledged: {acks:?}");
    assert!(
        acks[0].acked_seq < changed_seq,
        "the ack covers only the value read before the change: {acks:?}"
    );
    assert_eq!(
        fixture.pending_written_seq("order-12", run_id, "Status"),
        Some(changed_seq),
        "the ack leaves the newer change pending"
    );

    fixture.store.release_writes();
    fixture
        .wait_until("the newer value is written and acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(
        fixture
            .store
            .committed_writes()
            .into_iter()
            .map(|write| write.items)
            .collect::<Vec<_>>(),
        vec![
            vec![item("Status", Some(string_value("new")))],
            vec![item("Status", Some(string_value("paid")))],
        ]
    );
    assert_eq!(fixture.acks().len(), 2);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn acknowledgements_across_every_partition_commit_well_under_two_seconds() {
    let fixture = ProjectorFixture::start(sync_config());
    // Each commit waits like a group commit window plus a slow object store write.
    let commit_latency = Duration::from_millis(150);
    *fixture.harness.runtime.proposal_delay.lock().expect("lock") = commit_latency;
    let partition_count =
        usize::try_from(fixture.harness.hasher.partition_count()).expect("fits usize");
    let mut flows_per_partition: BTreeMap<PartitionId, usize> = BTreeMap::new();
    let flow_ids: Vec<String> = (0..10_000)
        .map(|n| format!("latency-order-{n}"))
        .filter(|flow_id| {
            let count = flows_per_partition
                .entry(fixture.harness.partition_of(flow_id))
                .or_default();
            *count += 1;
            *count <= 4
        })
        .take(4 * partition_count)
        .collect();
    assert_eq!(flows_per_partition.len(), partition_count);
    assert_eq!(flow_ids.len(), 4 * partition_count);

    let marked_at = Instant::now();
    let run_ids: Vec<RunId> = flow_ids
        .iter()
        .map(|flow_id| {
            fixture.start_flow(flow_id, vec![synced("Status", Some(string_value("new")))])
        })
        .collect();
    fixture
        .wait_until("every start is acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let startup_scan_latency = marked_at.elapsed();

    let changed_at = Instant::now();
    for (flow_id, run_id) in flow_ids.iter().zip(&run_ids) {
        fixture.set_attributes(
            flow_id,
            *run_id,
            vec![synced("Status", Some(string_value("paid")))],
        );
    }
    fixture
        .wait_until("every change is acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    let wake_up_latency = changed_at.elapsed();

    assert_eq!(fixture.acks().len(), 2 * flow_ids.len());
    // Committing one partition's acks at a time would take 16 × 150 ms = 2.4 s per round.
    for latency in [startup_scan_latency, wake_up_latency] {
        assert!(
            latency < Duration::from_secs(1),
            "mark-to-ack latency stays well under the 2 s target: {latency:?}"
        );
    }
    fixture.shutdown().await;
}

fn member_writes(keys: &[String]) -> Vec<AttributeWrite> {
    keys.iter()
        .map(|key| synced(key, Some(string_value("member"))))
        .collect()
}

fn member_keys(count: usize) -> Vec<String> {
    (0..count)
        .map(|n| format!("Members/account-{n:03}"))
        .collect()
}

fn committed_keys_of_flow(fixture: &ProjectorFixture, flow_id: &str) -> Vec<String> {
    fixture
        .store
        .committed_writes_of_flow(flow_id)
        .into_iter()
        .flat_map(|write| write.items)
        .map(|item| item.key)
        .collect()
}

#[tokio::test(start_paused = true)]
async fn a_timed_out_large_run_splits_its_transactions_while_healthy_runs_sync_promptly() {
    let fixture = ProjectorFixture::start(sync_config());
    let large = flow_id_in_partition(&fixture.harness, "large-", |partition| partition.0 < 8);
    let large_partition = fixture.harness.partition_of(&large);
    // Flows of one partition are written in Flow ID order, so this one comes after the large run.
    let same_partition = flow_id_in_partition(&fixture.harness, "zz-healthy-", |partition| {
        partition == large_partition
    });
    let higher_partition = flow_id_in_partition(&fixture.harness, "healthy-", |partition| {
        partition > large_partition
    });
    // 100 instances take 3 s, beyond the 2 s transaction timeout; 50 take 1.5 s.
    fixture
        .store
        .slow_down_flow(&large, Duration::from_millis(30));
    let keys = member_keys(100);
    fixture.start_flow(&large, member_writes(&keys));
    fixture
        .wait_until("the large run's first transaction timed out", |fixture| {
            fixture.store.attempt_count_of_flow(&large) == 1
        })
        .await;

    let started_at = Instant::now();
    for flow_id in [&same_partition, &higher_partition] {
        fixture.start_flow(flow_id, vec![synced("Status", Some(string_value("new")))]);
    }
    fixture
        .wait_until("both healthy runs are acknowledged", |fixture| {
            !fixture.acks_of_flow(&same_partition).is_empty()
                && !fixture.acks_of_flow(&higher_partition).is_empty()
        })
        .await;
    assert!(
        started_at.elapsed() < Duration::from_millis(500),
        "healthy runs wait for neither the timed-out run nor a store backoff: {:?}",
        started_at.elapsed()
    );

    fixture
        .wait_until("the large run is written and acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(
        fixture.store.attempt_sizes_of_flow(&large),
        vec![(100, false), (50, true), (50, true)],
        "the retry halves the transaction size"
    );
    assert_eq!(committed_keys_of_flow(&fixture, &large), keys);
    let large_acks = fixture.acks_of_flow(&large);
    assert_eq!(large_acks.len(), 1, "{large_acks:?}");
    assert!(!large_acks[0].is_dropped);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_partially_written_run_acknowledges_only_its_committed_transactions() {
    let fixture = ProjectorFixture::start(sync_config());
    let keys = member_keys(150);
    fixture
        .store
        .queued_flow_outcomes
        .lock()
        .expect("lock")
        .insert(
            "order-13".to_owned(),
            VecDeque::from([None, Some(transaction_timeout_error())]),
        );
    fixture.start_flow("order-13", member_writes(&keys));
    fixture
        .wait_until("every entry is written and acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(
        fixture.store.attempt_sizes_of_flow("order-13"),
        vec![(100, true), (50, false), (25, true), (25, true)],
        "only the unwritten entries are retried, in halved transactions"
    );
    let acks = fixture.acks_of_flow("order-13");
    assert_eq!(acks.len(), 2, "{acks:?}");
    assert_eq!(
        acks[0].attribute_keys,
        keys[..100],
        "the first ack covers only the committed transaction"
    );
    assert!(!acks[0].is_dropped);
    assert!(
        acks[1].attribute_keys.is_empty(),
        "the last ack covers every remaining entry"
    );
    assert_eq!(committed_keys_of_flow(&fixture, "order-13"), keys);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_timed_out_run_keeps_its_plain_attributes_in_one_transaction() {
    let fixture = ProjectorFixture::start(sync_config());
    // 63 items take 2.52 s, beyond the 2 s transaction timeout; 32 take 1.28 s.
    fixture
        .store
        .slow_down_flow("order-14", Duration::from_millis(40));
    let plain_keys = ["Address", "Status", "Zip"];
    let instance_keys = member_keys(60);
    let mut writes = member_writes(&instance_keys);
    writes.extend(
        plain_keys
            .iter()
            .map(|key| synced(key, Some(string_value("new")))),
    );
    fixture.start_flow("order-14", writes);
    fixture
        .wait_until("every entry is written and acknowledged", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    assert_eq!(
        fixture.store.attempt_sizes_of_flow("order-14"),
        vec![(63, false), (32, true), (30, true), (1, true)],
        "the halved statement limit counts the main-row upsert as one statement"
    );
    let writes_with_plain_keys: Vec<Vec<String>> = fixture
        .store
        .committed_writes_of_flow("order-14")
        .into_iter()
        .map(|write| {
            write
                .items
                .into_iter()
                .map(|item| item.key)
                .filter(|key| !key.contains('/'))
                .collect::<Vec<_>>()
        })
        .filter(|plain_keys_of_write| !plain_keys_of_write.is_empty())
        .collect();
    assert_eq!(
        writes_with_plain_keys,
        vec![plain_keys.map(str::to_owned).to_vec()],
        "one transaction updates every main-row column"
    );
    let mut committed_keys = committed_keys_of_flow(&fixture, "order-14");
    committed_keys.sort();
    let mut every_key: Vec<String> = plain_keys.map(str::to_owned).to_vec();
    every_key.extend(instance_keys);
    every_key.sort();
    assert_eq!(committed_keys, every_key);
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn timeouts_of_three_runs_without_a_commit_back_off_the_whole_store() {
    let fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .persistent_store_failures
        .lock()
        .expect("lock")
        .insert(STORE.to_owned(), transaction_timeout_error());
    let flow_ids: Vec<String> = (0..6).map(|n| format!("stalled-order-{n}")).collect();
    for flow_id in &flow_ids {
        fixture.start_flow(flow_id, vec![synced("Status", Some(string_value("new")))]);
    }
    tokio::time::sleep(Duration::from_secs(3)).await;
    fixture.harness.settle().await;
    let attempts = fixture.store.attempts_of_store(STORE);
    assert!(attempts.len() >= 5, "{attempts:?}");
    let first_flow_ids: BTreeSet<&str> = attempts[..3]
        .iter()
        .map(|attempt| attempt.flow_id.as_str())
        .collect();
    assert_eq!(
        first_flow_ids.len(),
        3,
        "three distinct runs time out before the store backs off: {attempts:?}"
    );
    for pair in attempts[2..].windows(2) {
        assert!(
            pair[1].finished_at - pair[0].finished_at >= Duration::from_millis(100),
            "later attempts wait for the store backoff: {attempts:?}"
        );
    }

    fixture
        .store
        .persistent_store_failures
        .lock()
        .expect("lock")
        .clear();
    fixture
        .wait_until("every run syncs once writes stop timing out", |fixture| {
            fixture.pending_run_count() == 0
        })
        .await;
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn the_retry_after_a_store_backoff_starts_with_other_runs_than_the_failed_one() {
    let fixture = ProjectorFixture::start(sync_config());
    let failing = flow_id_in_partition(&fixture.harness, "failing-", |partition| partition.0 < 8);
    let failing_partition = fixture.harness.partition_of(&failing);
    // Flows of one partition are written in Flow ID order, so this one comes after the failing run.
    let same_partition = flow_id_in_partition(&fixture.harness, "zz-healthy-", |partition| {
        partition == failing_partition
    });
    let higher_partition = flow_id_in_partition(&fixture.harness, "healthy-", |partition| {
        partition > failing_partition
    });
    fixture
        .store
        .persistent_flow_failures
        .lock()
        .expect("lock")
        .insert(
            failing.clone(),
            AttributeSyncWriteError::Retryable {
                detail: "connection reset".to_owned(),
            },
        );
    fixture.start_flow(&failing, vec![synced("Status", Some(string_value("new")))]);
    fixture
        .wait_until("the failing run's first write failed", |fixture| {
            fixture.store.attempt_count_of_flow(&failing) == 1
        })
        .await;

    let started_at = Instant::now();
    for flow_id in [&same_partition, &higher_partition] {
        fixture.start_flow(flow_id, vec![synced("Status", Some(string_value("new")))]);
    }
    fixture
        .wait_until("both healthy runs are acknowledged", |fixture| {
            !fixture.acks_of_flow(&same_partition).is_empty()
                && !fixture.acks_of_flow(&higher_partition).is_empty()
        })
        .await;
    assert!(
        started_at.elapsed() < Duration::from_millis(300),
        "healthy runs wait for one store backoff, not for the failing run: {:?}",
        started_at.elapsed()
    );
    assert!(fixture.acks_of_flow(&failing).is_empty());
    fixture.shutdown().await;
}

/// Starts a Flow whose first Step skips WaitFor, so its Execute closes the Flow.
fn start_closing_flow(
    harness: &EffectHarness,
    flow_id: &str,
    store_names: &[&str],
    writes: Vec<AttributeWrite>,
) -> RunId {
    let mut request = start_request(flow_id);
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    });
    request.flow_start_options = Some(FlowStartOptions {
        attributes: writes,
        flow_config_override: Some(FlowConfig {
            attribute_store_names: Some(AttributeStoreNames {
                names: store_names.iter().map(|name| (*name).to_owned()).collect(),
            }),
            ..FlowConfig::default()
        }),
        ..FlowStartOptions::default()
    });
    harness.start_flow(request, settings())
}

/// Completes the next dispatched Execute with a ForceComplete decision.
async fn force_complete_next_execute(harness: &EffectHarness) {
    let execute = harness.dispatcher.next_dispatch().await;
    execute.complete_execute(InvokeExecuteMethodResponse {
        step_decision: Some(StepDecision {
            close_decision: Some(CloseDecision {
                close_decision_type: CloseDecisionType::ForceComplete as i32,
                ..CloseDecision::default()
            }),
            ..StepDecision::default()
        }),
        ..InvokeExecuteMethodResponse::default()
    });
}

#[tokio::test(start_paused = true)]
async fn a_completed_flow_stays_running_until_the_projector_acknowledges_then_closes() {
    let fixture = ProjectorFixture::start(sync_config());
    fixture
        .store
        .is_holding_writes
        .store(true, Ordering::SeqCst);
    let run_id = start_closing_flow(
        &fixture.harness,
        "order-close",
        &[STORE],
        vec![synced("Status", Some(string_value("new")))],
    );
    force_complete_next_execute(&fixture.harness).await;
    fixture
        .wait_until("the completed Flow waits for the outbox", |fixture| {
            fixture
                .harness
                .run_record("order-close", run_id)
                .pending_close
                .is_some()
        })
        .await;
    tokio::time::sleep(Duration::from_millis(500)).await;
    fixture.harness.settle().await;
    let record = fixture.harness.run_record("order-close", run_id);
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert!(fixture.acks().is_empty(), "the write is still open");

    fixture.store.release_writes();
    fixture
        .wait_until("the ack finalizes the close", |fixture| {
            fixture.harness.run_record("order-close", run_id).status == FlowStatus::Completed as i32
        })
        .await;
    assert_eq!(
        fixture.store.committed_writes_of_flow("order-close").len(),
        1
    );
    assert_eq!(fixture.acks_of_flow("order-close").len(), 1);
    assert_eq!(
        fixture
            .harness
            .run_record("order-close", run_id)
            .pending_close,
        None
    );
    fixture.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn entries_of_an_unconfigured_store_are_dropped_so_the_close_finalizes() {
    for configured_store_names in [vec![STORE.to_owned()], Vec::new()] {
        let fixture = ProjectorFixture::start(AttributeSyncConfig {
            store_names: configured_store_names.clone(),
            ..sync_config()
        });
        let run_id = start_closing_flow(
            &fixture.harness,
            "order-retired",
            &["retired"],
            vec![synced("Status", Some(string_value("new")))],
        );
        force_complete_next_execute(&fixture.harness).await;
        fixture
            .wait_until("the dropped entries finalize the close", |fixture| {
                fixture.harness.run_record("order-retired", run_id).status
                    == FlowStatus::Completed as i32
            })
            .await;
        let acks = fixture.acks_of_flow("order-retired");
        assert_eq!(acks.len(), 1, "{configured_store_names:?}");
        assert_eq!(acks[0].store_name, "retired");
        assert!(acks[0].is_dropped);
        assert_eq!(fixture.dropped_effect_count(), 1);
        assert_eq!(fixture.pending_run_count(), 0);
        assert_eq!(
            fixture.store.attempt_count(),
            0,
            "nothing is written to a store the configuration does not name"
        );
        fixture.shutdown().await;
    }
}

/// Commits the Step's ForceComplete with the stamp of an admitted transactional RPC in flight.
fn commit_held_close(harness: &EffectHarness, flow_id: &str, run_id: RunId) {
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        is_transactional_rpc_in_flight: true,
        command: Some(flow_command::Command::StepMethodCompleted(
            StepMethodCompletedCommand {
                invocation: Some(StepInvocationKey {
                    step_execution_id: "ChargeStep-1".to_owned(),
                    method: StepMethod::Execute as i32,
                }),
                attempt: 1,
                response: Some(step_method_completed_command::Response::Execute(
                    InvokeExecuteMethodResponse {
                        step_decision: Some(StepDecision {
                            close_decision: Some(CloseDecision {
                                close_decision_type: CloseDecisionType::ForceComplete as i32,
                                ..CloseDecision::default()
                            }),
                            ..StepDecision::default()
                        }),
                        ..InvokeExecuteMethodResponse::default()
                    },
                )),
                app_worker_id: "worker-a".to_owned(),
                ..StepMethodCompletedCommand::default()
            },
        )),
        ..FlowCommand::default()
    };
    let reply = harness
        .runtime
        .commit(
            &EffectHarness::flow(flow_id),
            command,
            CommitUrgency::Prompt,
        )
        .expect("the held close commits");
    assert!(!is_error_reply(&reply), "the held close applies: {reply:?}");
}

async fn wait_for_status(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    status: FlowStatus,
) {
    for _ in 0..2_000 {
        harness.settle().await;
        if harness.run_record(flow_id, run_id).status == status as i32 {
            return;
        }
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    panic!("timed out waiting for status {status:?}");
}

#[tokio::test(start_paused = true)]
async fn a_close_held_by_transactional_rpcs_finalizes_once_they_end() {
    let harness = EffectHarness::new();
    let run_id = start_closing_flow(&harness, "held-close", &[], Vec::new());
    let execute = harness.dispatcher.next_dispatch().await;
    harness.runtime.transactional_rpcs.send_replace(1);
    commit_held_close(&harness, "held-close", run_id);
    tokio::time::sleep(Duration::from_millis(500)).await;
    harness.settle().await;
    let held = harness.run_record("held-close", run_id);
    assert_eq!(held.status, FlowStatus::Running as i32);
    assert!(held.pending_close.is_some());

    harness.runtime.transactional_rpcs.send_replace(0);
    wait_for_status(&harness, "held-close", run_id, FlowStatus::Completed).await;
    drop(execute);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_new_owner_finalizes_a_close_that_rpcs_of_the_old_owner_held() {
    let harness = EffectHarness::new();
    let run_id = start_closing_flow(&harness, "held-close", &[], Vec::new());
    let execute = harness.dispatcher.next_dispatch().await;
    harness.runtime.transactional_rpcs.send_replace(1);
    commit_held_close(&harness, "held-close", run_id);
    harness.settle().await;
    drop(execute);
    let durable = harness.durable.clone();
    harness.shutdown().await;

    let restarted = EffectHarness::with_config(durable, EffectsConfig::default());
    assert!(
        restarted
            .run_record("held-close", run_id)
            .pending_close
            .is_some()
    );
    restarted.rebuild_all_partitions().await;
    wait_for_status(&restarted, "held-close", run_id, FlowStatus::Completed).await;
    restarted.shutdown().await;
}
