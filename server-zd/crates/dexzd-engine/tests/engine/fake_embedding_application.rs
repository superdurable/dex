// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A test-only application that embeds server-zd and commits its own extension records.

use std::sync::atomic::{AtomicBool, AtomicI64, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use bytes::Bytes;
use dexzd_apply::{FlowStateReader, StateWrite};
use dexzd_cluster_metadata::EmbeddingApplication;
use dexzd_engine::{
    CommandCommitter, CommitUrgency, CommittedRecordHandling, CommittedRecordSink,
    EngineCommandAndEffectHooks, EngineLocalStore, EngineNode, EngineNodeConfig,
    EngineNodeDependencies, EngineServices, EngineStartError, ExtensionApplied, ExtensionApplyHalt,
    ExtensionCommitTicket, ExtensionRecordApplier, ExtensionRecordSink, ExtensionStamp,
    ExtensionSubmitError, HotStandby, HotStandbyConfig, HotStandbyDependencies, LocalStoreConfig,
    PartitionEffectRebuildError, PartitionEffectRebuilder, PublishedExtensionRecord,
    SystemEngineIdSource,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionHasher, PartitionId, TenantId};
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dexzd::v1::LogPosition;
use dexzd_storage::LocalReadView;
use futures::future::BoxFuture;
use tokio::task::JoinHandle;
use tokio::time::Instant;

use crate::harness::TestCluster;

/// The first key tag reserved for embedding applications.
const APPLICATION_KEY_TAG: u8 = 0x80;

/// Longest wait for a condition that the engine reaches on its own.
const CONVERGENCE_DEADLINE: Duration = Duration::from_secs(30);

/// What the fake applier does with one record.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum FakeOperation {
    /// Increments the named counter.
    Increment,
    /// Increments the counter; the sink then submits an increment of `<name>-follow-up`.
    IncrementAndFollowUp,
    /// Halts the partition.
    Halt,
    /// Writes a different value on every apply.
    Nondeterministic,
}

impl FakeOperation {
    fn encode(self) -> u8 {
        match self {
            Self::Increment => 0,
            Self::IncrementAndFollowUp => 1,
            Self::Halt => 2,
            Self::Nondeterministic => 3,
        }
    }

    fn decode(byte: u8) -> Self {
        match byte {
            0 => Self::Increment,
            1 => Self::IncrementAndFollowUp,
            2 => Self::Halt,
            3 => Self::Nondeterministic,
            _ => panic!("the fake application wrote operation {byte}"),
        }
    }
}

/// The fake application's outcome: the counter after the record.
pub(crate) struct FakeOutcome {
    pub(crate) name: String,
    pub(crate) operation: FakeOperation,
    pub(crate) counter: u64,
    /// Set by the sink, so a submitter can tell the sink saw the record first.
    pub(crate) is_seen_by_sink: AtomicBool,
    _live: LiveOutcome,
}

/// Counts outcomes not yet dropped.
struct LiveOutcome(Arc<AtomicI64>);

impl LiveOutcome {
    fn new(live: &Arc<AtomicI64>) -> Self {
        live.fetch_add(1, Ordering::SeqCst);
        Self(live.clone())
    }
}

impl Drop for LiveOutcome {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}

/// Applies `[operation] ++ name` payloads to a counter row and a history row with a reserved tag.
#[derive(Default)]
pub(crate) struct FakeExtensionApplier {
    pub(crate) live_outcomes: Arc<AtomicI64>,
    nondeterministic_values: AtomicU64,
}

pub(crate) fn counter_key(routing_hash: FlowHash, name: &str) -> Vec<u8> {
    let mut key = routing_hash.0.to_be_bytes().to_vec();
    key.push(APPLICATION_KEY_TAG);
    key.extend_from_slice(name.as_bytes());
    key
}

impl ExtensionRecordApplier for FakeExtensionApplier {
    fn apply_record(
        &self,
        _partition: PartitionId,
        stamp: &ExtensionStamp,
        payload: &[u8],
        state: &dyn FlowStateReader,
    ) -> Result<ExtensionApplied, ExtensionApplyHalt> {
        let (operation, name) = payload
            .split_first()
            .expect("a fake payload has an operation");
        let operation = FakeOperation::decode(*operation);
        let name = String::from_utf8(name.to_vec()).expect("fake names are UTF-8");
        if operation == FakeOperation::Halt {
            return Err(ExtensionApplyHalt {
                detail: "the test asked apply to halt".to_owned(),
            });
        }
        let key = counter_key(stamp.routing_hash, &name);
        let current = state
            .get(ColumnFamily::Flow, &key)
            .map_err(|error| ExtensionApplyHalt {
                detail: error.to_string(),
            })?
            .map_or(0, |bytes| decode_counter(&bytes));
        let counter = if operation == FakeOperation::Nondeterministic {
            self.nondeterministic_values.fetch_add(1, Ordering::SeqCst)
        } else {
            current + 1
        };
        let mut history_key = key.clone();
        history_key.extend_from_slice(&counter.to_be_bytes());
        Ok(ExtensionApplied {
            writes: vec![
                StateWrite::Put {
                    column_family: ColumnFamily::Flow,
                    key,
                    value: counter.to_be_bytes().to_vec(),
                },
                StateWrite::Put {
                    column_family: ColumnFamily::History,
                    key: history_key,
                    value: Vec::new(),
                },
            ],
            outcome: Box::new(FakeOutcome {
                name,
                operation,
                counter,
                is_seen_by_sink: AtomicBool::new(false),
                _live: LiveOutcome::new(&self.live_outcomes),
            }),
        })
    }
}

pub(crate) fn decode_counter(bytes: &[u8]) -> u64 {
    u64::from_be_bytes(bytes.try_into().expect("a counter has eight bytes"))
}

/// What the sink saw of one published record.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct SeenRecord {
    pub(crate) command_id: uuid::Uuid,
    pub(crate) name: String,
    pub(crate) counter: u64,
    pub(crate) has_waiter: bool,
    pub(crate) is_replayed: bool,
}

/// Records every published frame and submits follow-ups from separate tasks.
pub(crate) struct RecordingExtensionSink {
    committer: CommandCommitter,
    hasher: PartitionHasher,
    runtime: tokio::runtime::Handle,
    seen: Mutex<Vec<SeenRecord>>,
    follow_up_tasks: Mutex<Vec<JoinHandle<()>>>,
    pub(crate) follow_up_counters: Arc<Mutex<Vec<u64>>>,
}

impl ExtensionRecordSink for RecordingExtensionSink {
    fn publish_frame(
        &self,
        _partition: PartitionId,
        _position: &LogPosition,
        is_replayed: bool,
        records: &[PublishedExtensionRecord],
    ) {
        for record in records {
            let outcome = record
                .outcome
                .downcast_ref::<FakeOutcome>()
                .expect("the fake applier returns fake outcomes");
            outcome.is_seen_by_sink.store(true, Ordering::SeqCst);
            self.seen.lock().expect("seen lock").push(SeenRecord {
                command_id: record.stamp.command_id,
                name: outcome.name.clone(),
                counter: outcome.counter,
                has_waiter: record.has_waiter,
                is_replayed,
            });
            if outcome.operation == FakeOperation::IncrementAndFollowUp && !is_replayed {
                self.spawn_follow_up(format!("{}-follow-up", outcome.name));
            }
        }
    }
}

impl RecordingExtensionSink {
    pub(crate) fn seen(&self) -> Vec<SeenRecord> {
        self.seen.lock().expect("seen lock").clone()
    }

    /// Submits from another task: the applier lane waits for this sink to return.
    fn spawn_follow_up(&self, name: String) {
        let committer = self.committer.clone();
        let routing_hash = self.hasher.flow_hash(TenantId::DEFAULT, &name);
        let counters = self.follow_up_counters.clone();
        let task = self.runtime.spawn(async move {
            let (_, ticket) = committer
                .submit_extension_record(
                    routing_hash,
                    payload(FakeOperation::Increment, &name),
                    CommitUrgency::Prompt,
                )
                .expect("the follow-up is submitted");
            let committed = ticket.wait().await.expect("the follow-up commits");
            let outcome = committed
                .outcome
                .downcast::<FakeOutcome>()
                .expect("the follow-up returns a fake outcome");
            counters.lock().expect("counter lock").push(outcome.counter);
        });
        self.follow_up_tasks
            .lock()
            .expect("follow-up task lock")
            .push(task);
    }
}

/// Records rebuilt partitions; the fake application has no effects to rebuild.
#[derive(Default)]
pub(crate) struct RecordingRebuilder {
    pub(crate) rebuilt: Mutex<Vec<PartitionId>>,
}

impl PartitionEffectRebuilder for RecordingRebuilder {
    fn rebuild_partition(
        &self,
        partition: PartitionId,
    ) -> BoxFuture<'_, Result<usize, PartitionEffectRebuildError>> {
        self.rebuilt.lock().expect("rebuilt lock").push(partition);
        Box::pin(async { Ok(0) })
    }
}

pub(crate) fn payload(operation: FakeOperation, name: &str) -> Bytes {
    let mut payload = vec![operation.encode()];
    payload.extend_from_slice(name.as_bytes());
    Bytes::from(payload)
}

/// The node configuration of the fake application's cluster.
pub(crate) fn fake_application_config(
    cluster: &TestCluster,
    local_store: LocalStoreConfig,
) -> EngineNodeConfig {
    let mut config = cluster.config(local_store);
    config.cluster.embedding_application = Some(EmbeddingApplication {
        name: "test-embedding-application".to_owned(),
        key_layout_version: 0x8000_0001,
        supported_versions: 1..=1,
        current_version: 1,
    });
    config
}

/// A serving node of the fake application.
pub(crate) struct FakeApplicationNode {
    pub(crate) node: EngineNode,
    pub(crate) applier: Arc<FakeExtensionApplier>,
    pub(crate) sink: Arc<RecordingExtensionSink>,
    pub(crate) hasher: PartitionHasher,
}

/// The sink and hasher the hooks factory creates.
type FactoryParts = Arc<Mutex<Option<(Arc<RecordingExtensionSink>, PartitionHasher)>>>;

/// Returns dependencies and a hooks factory that apply and publish the fake application's records.
fn fake_application_parts(
    cluster: &TestCluster,
    object_store: Arc<dyn ObjectStore>,
    applier: &Arc<FakeExtensionApplier>,
) -> (
    EngineNodeDependencies,
    impl FnOnce(EngineServices) -> EngineCommandAndEffectHooks,
    FactoryParts,
) {
    let dependencies = EngineNodeDependencies {
        metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
        object_store,
        clock: cluster.clock.clone(),
        applier: CommittedRecordHandling::ExtensionRecord(applier.clone()),
        id_source: Arc::new(SystemEngineIdSource),
        lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
    };
    let parts: FactoryParts = Arc::default();
    let factory_parts = parts.clone();
    let factory = move |services: EngineServices| {
        let sink = Arc::new(RecordingExtensionSink {
            committer: services.committer.clone(),
            hasher: services.partition_hasher.clone(),
            runtime: tokio::runtime::Handle::current(),
            seen: Mutex::default(),
            follow_up_tasks: Mutex::default(),
            follow_up_counters: Arc::default(),
        });
        *factory_parts.lock().expect("factory parts lock") =
            Some((sink.clone(), services.partition_hasher));
        EngineCommandAndEffectHooks {
            committed_commands: CommittedRecordSink::ExtensionRecord(sink),
            rebuilder: Arc::new(RecordingRebuilder::default()),
        }
    };
    (dependencies, factory, parts)
}

fn into_fake_application_node(
    node: EngineNode,
    applier: Arc<FakeExtensionApplier>,
    parts: &FactoryParts,
) -> FakeApplicationNode {
    let (sink, hasher) = parts
        .lock()
        .expect("factory parts lock")
        .take()
        .expect("a serving node ran its hooks factory");
    FakeApplicationNode {
        node,
        applier,
        sink,
        hasher,
    }
}

/// Starts a node of the fake application on `object_store`.
pub(crate) async fn start_fake_application_on(
    cluster: &TestCluster,
    config: &EngineNodeConfig,
    object_store: Arc<dyn ObjectStore>,
) -> Result<FakeApplicationNode, EngineStartError> {
    let applier = Arc::new(FakeExtensionApplier::default());
    let (dependencies, factory, parts) = fake_application_parts(cluster, object_store, &applier);
    let node = EngineNode::start(config, dependencies, factory).await?;
    Ok(into_fake_application_node(node, applier, &parts))
}

/// Starts a node of the fake application on the cluster's object store.
pub(crate) async fn start_fake_application(
    cluster: &TestCluster,
    config: &EngineNodeConfig,
) -> Result<FakeApplicationNode, EngineStartError> {
    start_fake_application_on(cluster, config, cluster.object_store.clone()).await
}

/// Takes over from `standby` as a node of the fake application.
pub(crate) async fn take_over_as_fake_application(
    cluster: &TestCluster,
    config: &EngineNodeConfig,
    standby: HotStandby,
) -> FakeApplicationNode {
    let applier = Arc::new(FakeExtensionApplier::default());
    let (dependencies, factory, parts) =
        fake_application_parts(cluster, cluster.object_store.clone(), &applier);
    let (_cancel, mut cancellation) = tokio::sync::watch::channel(false);
    let takeover = EngineNode::take_over_from_hot_standby_until_cancelled(
        config,
        dependencies,
        factory,
        standby,
        &mut cancellation,
    )
    .await
    .expect("the takeover succeeds");
    let dexzd_engine::HotStandbyTakeover::Serving(node) = takeover else {
        panic!("an uncancelled takeover serves");
    };
    into_fake_application_node(node, applier, &parts)
}

/// Starts a hot standby that applies the fake application's records.
pub(crate) fn start_fake_application_standby(
    cluster: &TestCluster,
    config: EngineNodeConfig,
    standby_config: HotStandbyConfig,
) -> Result<HotStandby, EngineStartError> {
    HotStandby::start(
        config,
        standby_config,
        HotStandbyDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: cluster.object_store.clone(),
            clock: cluster.clock.clone(),
            applier: CommittedRecordHandling::ExtensionRecord(Arc::new(
                FakeExtensionApplier::default(),
            )),
            id_source: Arc::new(SystemEngineIdSource),
            restart_jitter_seed: 7,
        },
    )
}

impl FakeApplicationNode {
    pub(crate) fn routing_hash(&self, name: &str) -> FlowHash {
        self.hasher.flow_hash(TenantId::DEFAULT, name)
    }

    pub(crate) fn submit(
        &self,
        operation: FakeOperation,
        name: &str,
        urgency: CommitUrgency,
    ) -> Result<(ExtensionStamp, ExtensionCommitTicket), ExtensionSubmitError> {
        self.node.committer().submit_extension_record(
            self.routing_hash(name),
            payload(operation, name),
            urgency,
        )
    }

    /// Submits an increment, waits for it, and returns its outcome's counter.
    pub(crate) async fn increment(&self, name: &str) -> u64 {
        let (_, ticket) = self
            .submit(FakeOperation::Increment, name, CommitUrgency::Prompt)
            .expect("the record is submitted");
        let committed = ticket.wait().await.expect("the record commits");
        committed
            .outcome
            .downcast::<FakeOutcome>()
            .expect("the fake applier returns fake outcomes")
            .counter
    }

    /// Returns the committed counter of `name`.
    pub(crate) async fn counter(&self, name: &str) -> u64 {
        let view = self
            .node
            .committer()
            .committed_view()
            .expect("committed view opens");
        let key = counter_key(self.routing_hash(name), name);
        tokio::task::spawn_blocking(move || {
            LocalReadView::get(&view, ColumnFamily::Flow, &key)
                .expect("committed read succeeds")
                .map_or(0, |bytes| decode_counter(&bytes))
        })
        .await
        .expect("read task completes")
    }

    pub(crate) fn live_outcomes(&self) -> i64 {
        self.applier.live_outcomes.load(Ordering::SeqCst)
    }
}

/// Returns the counter of `name` in `store`, as a hot standby applied it.
pub(crate) fn stored_counter(
    store: &EngineLocalStore,
    hasher: &PartitionHasher,
    name: &str,
) -> u64 {
    let key = counter_key(hasher.flow_hash(TenantId::DEFAULT, name), name);
    store
        .get(ColumnFamily::Flow, &key)
        .expect("local read succeeds")
        .map_or(0, |bytes| decode_counter(&bytes))
}

/// Waits until `is_reached` holds, polling with paused time.
pub(crate) async fn wait_until(description: &str, mut is_reached: impl FnMut() -> bool) {
    let deadline = Instant::now() + CONVERGENCE_DEADLINE;
    while !is_reached() {
        assert!(Instant::now() < deadline, "{description}");
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}
