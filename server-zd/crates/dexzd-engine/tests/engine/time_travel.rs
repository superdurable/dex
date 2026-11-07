// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! ResetFlow through a registry over a node with real apply (plan section 6.3): the owner
//! computes the result, stages it, and commits the TimeTravel, and the debug replay check
//! compares every restored point with the hash recorded when that point was applied.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use bytes::Bytes;
use dexzd_apply::read_model::read_history_segment_content;
use dexzd_apply::values::OffloadedValues;
use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyError, FlowStateReader, LoadedHistorySegments,
    SegmentedTimeTravelComputation, TimeTravelComputationRequest,
    compute_time_travel_from_segments,
};
use dexzd_engine::{
    CommandCommitter, CommitUrgency, CommittedCommandSink, CommittedFlowCommand,
    DeterministicCommandApplier, EngineCommandAndEffectHooks, EngineNode, EngineNodeDependencies,
    EngineServices, FlowAddress, FlowCoordinationConfig, FlowCoordinationEffectSink,
    FlowCoordinationError, FlowCoordinationHardDeleteUploads, FlowCoordinatorDependencies,
    FlowCoordinatorRegistry, HardDeleteReadError, HardDeleteUploadError, HardDeleteUploadOutcome,
    HardDeleteValueReader, HardDeleteValueUploader, LoadedOffloadedValues, LocalStoreConfig,
    RpcInvokeError, RpcWorkerInvoker, SegmentedTimeTravelHistory, StepQueueStatus,
    SystemEngineIdSource, TimeTravelHistoryError, TimeTravelHistoryOpener, TimeTravelRunHistory,
    WorkerRpcInvocation, WorkerRpcResult, grpc_code,
};
use dexzd_history_segment::{encode_history_segment, history_segment_object_key};
use dexzd_keyspace::row_keys::{
    attempt_record_key, checkpoint_key, history_record_key, prefix_range, run_singleton_key,
    value_row_key,
};
use dexzd_keyspace::{
    ColumnFamily, PartitionHasher, PartitionId, RunId, RunTable, TenantId, ValueId, ValueReference,
    run_table_prefix,
};
use dexzd_object_store::{InMemoryObjectStore, ObjectStore, ObjectStoreOperation, WriteCondition};
use dexzd_protocol::dex::{
    ActionPermissionMapping, ActionPermissionMappings, AttributeWrite, FlowConfig,
    FlowResetStepMethod, FlowResetType, FlowStartOptions, LoadBlobRequestEntry, LoadBlobsRequest,
    ResetFlowRequest, SetAttributesRequest, StartFlowRequest, StepOptions, Value, WorkerTarget,
    value,
};
use dexzd_protocol::dexzd::v1::{
    AttemptRecord, AttributeRecord, AttributesSetCommand, FlowCommand, HardDeleteObjectReference,
    HistoryRecord, HistorySegmentRecord, HistorySegmentState, HistorySegmentUploadedCommand,
    LogPosition, RunRecord, RunSettings, StepAttemptHeartbeatCommand, StepAttemptStartedCommand,
    StepInvocationKey, StepMethod, ValueBirthRecord, ValueRecord, flow_command::Command,
    step_attempt_heartbeat_command, stored_value,
};
use dexzd_storage::{LocalStore, RestoredLocalStore, WriteBatch};
use futures::future::BoxFuture;
use prost::Message;
use tokio::sync::Notify;

use crate::harness::{RecordingCommandAndEffectHooks, TestCluster};

const FLOW_ID: &str = "travel-flow";
const START_STEP: &str = "ChargeStep";
const START_STEP_EXECUTION: &str = "ChargeStep-1";

/// A node with real apply and debug replay checks, and the registry that serves it.
struct TravelNode {
    node: EngineNode,
    registry: FlowCoordinatorRegistry,
    /// Registries beside `registry` over the same committer; each serializes only its own
    /// ResetFlows.
    other_registries: Vec<FlowCoordinatorRegistry>,
    store: Arc<RestoredLocalStore>,
    hasher: PartitionHasher,
    recording: Arc<RecordingCommandAndEffectHooks>,
    next_request: AtomicUsize,
    objects: Arc<InMemoryObjectStore>,
}

impl TravelNode {
    async fn start(
        cluster: &TestCluster,
        local_store: LocalStoreConfig,
        opener: Arc<dyn TimeTravelHistoryOpener>,
        flow_coordination: FlowCoordinationConfig,
    ) -> Self {
        Self::start_with_registries(
            cluster,
            local_store,
            vec![opener],
            flow_coordination,
            dexzd_engine::HistoryReadsConfig::default(),
        )
        .await
    }

    /// Starts a node whose history segment reads use `history_reads`.
    async fn start_with_history_reads(
        cluster: &TestCluster,
        local_store: LocalStoreConfig,
        opener: Arc<dyn TimeTravelHistoryOpener>,
        flow_coordination: FlowCoordinationConfig,
        history_reads: dexzd_engine::HistoryReadsConfig,
    ) -> Self {
        Self::start_with_registries(
            cluster,
            local_store,
            vec![opener],
            flow_coordination,
            history_reads,
        )
        .await
    }

    /// Starts a node with one registry per opener of `openers`; the first is `registry`.
    async fn start_with_registries(
        cluster: &TestCluster,
        local_store: LocalStoreConfig,
        openers: Vec<Arc<dyn TimeTravelHistoryOpener>>,
        flow_coordination: FlowCoordinationConfig,
        history_reads: dexzd_engine::HistoryReadsConfig,
    ) -> Self {
        let mut config = cluster.config(local_store);
        config.commit.debug_replay_checks = true;
        let captured = Arc::new(Mutex::new(None));
        let captured_for_factory = Arc::clone(&captured);
        let recording = Arc::new(RecordingCommandAndEffectHooks::default());
        let recording_for_factory = Arc::clone(&recording);
        let clock = cluster.clock.clone();
        let history_object_store = cluster.object_store.clone();
        let node = EngineNode::start(
            &config,
            EngineNodeDependencies {
                metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
                object_store: cluster.object_store.clone(),
                clock: cluster.clock.clone(),
                applier: Arc::new(DeterministicCommandApplier),
                id_source: Arc::new(SystemEngineIdSource),
                lease_validity_deadline: dexzd_cluster_metadata::LeaseValidityDeadline::disarmed(),
            },
            move |services: EngineServices| {
                let ports = Arc::new(UnusedPorts);
                let registries: Vec<FlowCoordinatorRegistry> = openers
                    .into_iter()
                    .map(|opener| {
                        FlowCoordinatorRegistry::new(
                            &flow_coordination,
                            FlowCoordinatorDependencies {
                                hasher: services.partition_hasher.clone(),
                                committer: Arc::new(services.committer.clone()),
                                applier: Arc::new(DeterministicCommandApplier),
                                effects: ports.clone(),
                                rpc_invoker: ports.clone(),
                                step_queue_status: ports.clone(),
                                hard_delete_reader: ports.clone(),
                                hard_delete_uploader: ports.clone(),
                                time_travel_history: opener,
                                clock: clock.clone(),
                                history_segment_reader: Arc::new(
                                    dexzd_engine::HistorySegmentReader::new(
                                        &history_reads,
                                        history_object_store.clone(),
                                        Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
                                        dexzd_engine::BlockingWorkPlacement::BlockingThreadPool,
                                    ),
                                ),
                                id_source: services.id_source.clone(),
                            },
                        )
                    })
                    .collect();
                *captured_for_factory.lock().expect("capture lock") = Some((
                    registries.clone(),
                    services.local_store,
                    services.partition_hasher,
                ));
                EngineCommandAndEffectHooks {
                    committed_commands: Arc::new(ForwardingSink {
                        registries,
                        recording: Arc::clone(&recording_for_factory),
                    }),
                    rebuilder: recording_for_factory,
                }
            },
        )
        .await
        .expect("the node starts");
        let (registries, store, hasher) = captured
            .lock()
            .expect("capture lock")
            .take()
            .expect("services captured");
        let mut registries = registries.into_iter();
        let registry = registries.next().expect("a node has at least one registry");
        Self {
            node,
            registry,
            other_registries: registries.collect(),
            store,
            hasher,
            recording,
            next_request: AtomicUsize::new(0),
            objects: cluster.object_store.clone(),
        }
    }

    fn context(&self) -> ApplyContext {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, FLOW_ID);
        ApplyContext {
            partition: self.hasher.partition_of(flow_hash),
            flow_hash,
        }
    }

    fn committer(&self) -> &CommandCommitter {
        self.node.committer()
    }

    async fn start_flow(&self, step_options: Option<StepOptions>) -> RunId {
        let response = self
            .registry
            .start_flow(
                TenantId::DEFAULT,
                StartFlowRequest {
                    flow_id: FLOW_ID.to_owned(),
                    flow_type: "TravelFlow".to_owned(),
                    start_step_type: START_STEP.to_owned(),
                    step_options,
                    request_id: "start".to_owned(),
                    flow_start_options: Some(FlowStartOptions {
                        flow_config_override: Some(FlowConfig {
                            worker_target: Some(WorkerTarget { is_pull_mode: true }),
                            ..FlowConfig::default()
                        }),
                        ..FlowStartOptions::default()
                    }),
                    ..StartFlowRequest::default()
                },
            )
            .await
            .expect("the Flow starts");
        response.run_id.parse().expect("the run ID parses")
    }

    async fn set_attribute(&self, key: &str, value: Value) {
        self.registry
            .set_attributes(
                TenantId::DEFAULT,
                SetAttributesRequest {
                    flow_id: FLOW_ID.to_owned(),
                    attributes: vec![AttributeWrite {
                        key: key.to_owned(),
                        value: Some(value),
                        ..AttributeWrite::default()
                    }],
                    request_id: format!("set-{}", self.next_request.fetch_add(1, Ordering::SeqCst)),
                    ..SetAttributesRequest::default()
                },
            )
            .await
            .expect("the Attributes are set");
    }

    async fn reset(&self, request: ResetFlowRequest) -> Result<u32, FlowCoordinationError> {
        self.registry
            .reset_flow(TenantId::DEFAULT, request)
            .await
            .map(|response| u32::try_from(response.run_attempt).expect("a positive attempt"))
    }

    /// Commits `body` for `run_id` straight to the log, as an effect does.
    async fn commit(&self, run_id: RunId, body: Command) {
        let (_, ticket) = self
            .committer()
            .submit(
                self.context(),
                FlowCommand {
                    flow_id: FLOW_ID.to_owned(),
                    run_id: run_id.as_bytes().to_vec(),
                    command: Some(body),
                    ..FlowCommand::default()
                },
                CommitUrgency::Prompt,
            )
            .expect("the command is submitted");
        ticket.wait().await.expect("the command commits");
    }

    fn view(&self) -> impl FlowStateReader {
        self.committer()
            .committed_view()
            .expect("the committed view opens")
    }

    fn run_record(&self, run_id: RunId) -> RunRecord {
        let key = run_singleton_key(self.context().flow_hash, run_id, RunTable::RunRecord);
        RunRecord::decode(
            self.view()
                .get(ColumnFamily::Flow, &key)
                .expect("the run record reads")
                .expect("the run record exists")
                .as_slice(),
        )
        .expect("the run record decodes")
    }

    fn history_record(&self, run_id: RunId, seq: u64) -> HistoryRecord {
        let key = history_record_key(self.context().flow_hash, run_id, seq);
        HistoryRecord::decode(
            self.view()
                .get(ColumnFamily::History, &key)
                .expect("the history record reads")
                .expect("the history record exists")
                .as_slice(),
        )
        .expect("the history record decodes")
    }

    fn attribute(&self, run_id: RunId, key: &str) -> Option<AttributeRecord> {
        let row_key = dexzd_keyspace::row_keys::run_named_key(
            self.context().flow_hash,
            run_id,
            RunTable::Attribute,
            key,
        );
        self.view()
            .get(ColumnFamily::Flow, &row_key)
            .expect("the Attribute reads")
            .map(|bytes| AttributeRecord::decode(bytes.as_slice()).expect("the Attribute decodes"))
    }

    /// Committed commands of the Flow whose body `matches`.
    fn committed_count(&self, matches: fn(&Command) -> bool) -> usize {
        self.recording
            .committed_commands()
            .iter()
            .filter(|committed| {
                committed.command.flow_id == FLOW_ID
                    && committed.command.command.as_ref().is_some_and(matches)
                    && !matches!(
                        committed.committed.outcome.reply.reply,
                        Some(dexzd_protocol::dexzd::v1::command_reply::Reply::Error(_))
                    )
            })
            .count()
    }

    /// Turns offload mode on for the Flow's partition.
    async fn enable_history_offload(&self) {
        let partition = self.context().partition;
        let (_, ticket) = self
            .committer()
            .submit(
                self.context(),
                FlowCommand {
                    flow_id: FLOW_ID.to_owned(),
                    command: Some(Command::SetHistoryOffloadMode(
                        dexzd_protocol::dexzd::v1::SetHistoryOffloadModeCommand {
                            enabled: true,
                            partition: u32::from(partition.0),
                        },
                    )),
                    ..FlowCommand::default()
                },
                CommitUrgency::Prompt,
            )
            .expect("the mode command is submitted");
        ticket.wait().await.expect("the mode commits");
    }

    /// Uploads and acknowledges every SEALED segment of the run as the uploader does, then
    /// deletes the local History records and checkpoints they cover, except genesis, as local
    /// deletion after upload does. Returns the run's uploaded-through seq.
    async fn offload_sealed_history(&self, run_id: RunId) -> u64 {
        let flow_hash = self.context().flow_hash;
        // FlowStart is the run's first seq.
        let genesis_seq = 1;
        for segment in self.segments(run_id) {
            if segment.state != HistorySegmentState::Sealed as i32 {
                continue;
            }
            let content =
                read_history_segment_content(&self.view(), flow_hash, run_id, segment.first_seq)
                    .expect("the segment content reads")
                    .expect("the segment exists");
            let encoded = encode_history_segment(&content).expect("the content encodes");
            let object_size_bytes = u64::try_from(encoded.bytes.len()).expect("small object");
            let key = history_segment_object_key(
                TenantId::DEFAULT,
                flow_hash,
                run_id,
                segment.first_seq,
                segment.last_seq,
            );
            self.objects
                .put(&key, Bytes::from(encoded.bytes), WriteCondition::CreateOnly)
                .await
                .expect("the object writes");
            self.commit(
                run_id,
                Command::HistorySegmentUploaded(HistorySegmentUploadedCommand {
                    first_seq: segment.first_seq,
                    last_seq: segment.last_seq,
                    content_checksum: encoded.content_checksum.to_vec(),
                    object_size_bytes,
                }),
            )
            .await;
            let mut batch = WriteBatch::new();
            for record in &content.records {
                if record.seq != genesis_seq {
                    batch.delete(
                        ColumnFamily::History,
                        history_record_key(flow_hash, run_id, record.seq),
                    );
                }
            }
            for checkpoint in &content.checkpoints {
                batch.delete(
                    ColumnFamily::History,
                    checkpoint_key(flow_hash, run_id, checkpoint.attempt, checkpoint.seq),
                );
            }
            self.store.write(batch).expect("the local rows are removed");
        }
        self.run_record(run_id).history_uploaded_through_seq
    }

    fn segments(&self, run_id: RunId) -> Vec<HistorySegmentRecord> {
        let prefix = run_table_prefix(self.context().flow_hash, run_id, RunTable::HistorySegment);
        let (start, end) = prefix_range(&prefix);
        self.view()
            .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
            .expect("the catalog scans")
            .into_iter()
            .map(|(_, value)| {
                HistorySegmentRecord::decode(value.as_slice()).expect("the catalog row decodes")
            })
            .collect()
    }

    /// Returns how many ranged reads of history segment objects object storage served.
    fn history_object_reads(&self) -> u64 {
        self.objects
            .call_count(ObjectStoreOperation::Get, "history/")
    }

    /// Removes a superseded value's value row, as offloading does, keeping its birth and
    /// garbage rows; returns the value.
    fn offload_superseded_value(&self, run_id: RunId, value_id: ValueId) -> Value {
        let flow_hash = self.context().flow_hash;
        let view = self.view();
        let birth_key = value_row_key(flow_hash, run_id, RunTable::ValueBirth, value_id);
        let birth = ValueBirthRecord::decode(
            view.get(ColumnFamily::Flow, &birth_key)
                .expect("the birth reads")
                .expect("the value has a birth row")
                .as_slice(),
        )
        .expect("the birth decodes");
        assert!(
            birth.garbage_seq > 0,
            "only a superseded value leaves the store"
        );
        let value_key = value_row_key(flow_hash, run_id, RunTable::Value, value_id);
        let value = ValueRecord::decode(
            view.get(ColumnFamily::Value, &value_key)
                .expect("the value reads")
                .expect("the value row exists")
                .as_slice(),
        )
        .expect("the value decodes")
        .value
        .expect("the row holds a value");
        let mut batch = WriteBatch::new();
        batch.delete(ColumnFamily::Value, value_key);
        self.store.write(batch).expect("the value is removed");
        value
    }

    /// Shuts the node down and releases the store, the registry, and the recorded commands.
    async fn shutdown(self) {
        let Self {
            node,
            registry,
            other_registries,
            store,
            recording,
            ..
        } = self;
        node.shutdown().await.expect("lease released");
        drop((registry, other_registries, store, recording));
    }

    fn assert_every_travel_verified(&self, expected: u64) {
        assert_eq!(
            self.node.time_travel_verification_counts(),
            (expected, 0),
            "every restored point matched its recorded hash"
        );
        assert!(
            self.recording.committed_commands().iter().all(|committed| {
                !matches!(
                    &committed.command.command,
                    Some(Command::TimeTravel(travel)) if travel.staging_id.is_empty()
                )
            }),
            "the owner proposes only TimeTravel commands that name a staged result"
        );
    }
}

/// Forwards committed commands to the registry, as the server does, and records them.
struct ForwardingSink {
    registries: Vec<FlowCoordinatorRegistry>,
    recording: Arc<RecordingCommandAndEffectHooks>,
}

impl CommittedCommandSink for ForwardingSink {
    fn apply_committed(&self, committed: CommittedFlowCommand) {
        self.recording.apply_committed(committed.clone());
        for registry in &self.registries {
            registry.apply_committed(committed.clone());
        }
    }

    fn finish_frame(&self, partition: PartitionId, position: &LogPosition) {
        self.recording.finish_frame(partition, position);
    }
}

/// Ports the registry needs for client writes and resets without Workers.
struct UnusedPorts;

impl FlowCoordinationEffectSink for UnusedPorts {
    fn handle(&self, _flow: &FlowAddress, _effects: &[ApplyEffect], _is_committed: bool) {}
}

impl StepQueueStatus for UnusedPorts {
    fn is_waiting_for_supporting_worker(
        &self,
        _tenant_id: u64,
        _flow_type: &str,
        _step_type: &str,
    ) -> bool {
        false
    }
}

impl RpcWorkerInvoker for UnusedPorts {
    fn invoke_worker_rpc(
        &self,
        _invocation: WorkerRpcInvocation,
    ) -> BoxFuture<'_, Result<WorkerRpcResult, RpcInvokeError>> {
        Box::pin(async { panic!("time travel tests call no Worker") })
    }
}

impl HardDeleteValueReader for UnusedPorts {
    fn read_hard_delete_value<'a>(
        &'a self,
        _flow: &'a FlowAddress,
        _run_id: RunId,
        _object: &'a HardDeleteObjectReference,
    ) -> BoxFuture<'a, Result<Option<Value>, HardDeleteReadError>> {
        Box::pin(async { panic!("time travel tests read no hard-delete object") })
    }
}

impl HardDeleteValueUploader for UnusedPorts {
    fn upload_hard_delete_values<'a>(
        &'a self,
        _flow: &'a FlowAddress,
        _run_id: RunId,
        _writes: &'a mut [AttributeWrite],
    ) -> BoxFuture<'a, Result<FlowCoordinationHardDeleteUploads, HardDeleteUploadError>> {
        Box::pin(async { panic!("time travel tests upload no hard-delete object") })
    }

    fn finish_hard_delete_uploads(
        &self,
        _uploads: FlowCoordinationHardDeleteUploads,
        _outcome: HardDeleteUploadOutcome,
    ) -> BoxFuture<'_, Result<(), HardDeleteUploadError>> {
        Box::pin(async { panic!("time travel tests upload no hard-delete object") })
    }
}

/// A test-controlled history opener: the first open can wait at a gate while the test changes
/// committed state, and every open serves the values the test removed from the local store
/// besides local rows and fetched segments, as an object storage source does.
#[derive(Default)]
struct ScriptedHistory {
    gate: Mutex<Option<(Arc<Notify>, Arc<Notify>)>>,
    opens: AtomicUsize,
    /// Current run attempt each open saw.
    seen_attempts: Mutex<Vec<u32>>,
    removed_values: Arc<Mutex<BTreeMap<ValueId, Value>>>,
}

impl ScriptedHistory {
    /// Makes the next open wait; returns the notification of the open and its release.
    fn gate_next_open(&self) -> (Arc<Notify>, Arc<Notify>) {
        let opened = Arc::new(Notify::new());
        let release = Arc::new(Notify::new());
        *self.gate.lock().expect("gate lock") = Some((Arc::clone(&opened), Arc::clone(&release)));
        (opened, release)
    }
}

impl TimeTravelHistoryOpener for ScriptedHistory {
    fn open_time_travel_history(
        &self,
        view: Box<dyn FlowStateReader + Send>,
        context: ApplyContext,
        run_id: RunId,
        segments: Arc<LoadedHistorySegments>,
        _values: Arc<LoadedOffloadedValues>,
    ) -> BoxFuture<'_, Result<Box<dyn TimeTravelRunHistory>, TimeTravelHistoryError>> {
        Box::pin(async move {
            self.opens.fetch_add(1, Ordering::SeqCst);
            let record_key = run_singleton_key(context.flow_hash, run_id, RunTable::RunRecord);
            let record = view
                .get(ColumnFamily::Flow, &record_key)
                .ok()
                .flatten()
                .and_then(|bytes| RunRecord::decode(bytes.as_slice()).ok())
                .unwrap_or_default();
            self.seen_attempts
                .lock()
                .expect("attempt lock")
                .push(record.current_attempt);
            let gate = self.gate.lock().expect("gate lock").take();
            if let Some((opened, release)) = gate {
                opened.notify_one();
                release.notified().await;
            }
            let history: Box<dyn TimeTravelRunHistory> = Box::new(ScriptedRunHistory {
                view,
                segments,
                removed_values: RemovedValues(
                    self.removed_values.lock().expect("value lock").clone(),
                ),
            });
            Ok(history)
        })
    }
}

struct ScriptedRunHistory {
    view: Box<dyn FlowStateReader + Send>,
    segments: Arc<LoadedHistorySegments>,
    removed_values: RemovedValues,
}

impl TimeTravelRunHistory for ScriptedRunHistory {
    fn compute_time_travel(
        &self,
        context: &ApplyContext,
        request: &TimeTravelComputationRequest<'_>,
    ) -> Result<SegmentedTimeTravelComputation, ApplyError> {
        compute_time_travel_from_segments(
            context,
            self.view.as_ref(),
            &self.segments,
            &self.removed_values,
            request,
        )
    }
}

struct RemovedValues(BTreeMap<ValueId, Value>);

impl OffloadedValues for RemovedValues {
    fn offloaded_value(
        &self,
        _run_id: RunId,
        value_id: ValueId,
    ) -> Result<Option<Value>, ApplyError> {
        Ok(self.0.get(&value_id).cloned())
    }
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn reset(reset_type: FlowResetType, is_reapplying: bool) -> ResetFlowRequest {
    ResetFlowRequest {
        flow_id: FLOW_ID.to_owned(),
        reset_type: reset_type as i32,
        reason: "engine time travel test".to_owned(),
        skip_writes_reapply: !is_reapplying,
        ..ResetFlowRequest::default()
    }
}

fn reset_to_time(timestamp_micros: i64) -> ResetFlowRequest {
    ResetFlowRequest {
        history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
        ..reset(FlowResetType::HistoryEventTime, false)
    }
}

fn is_time_travel(command: &Command) -> bool {
    matches!(command, Command::TimeTravel(_))
}

fn is_stage(command: &Command) -> bool {
    matches!(command, Command::TimeTravelStage(_))
}

fn table_value_id(record: &AttributeRecord) -> ValueId {
    let Some(stored_value::Kind::TableValue(reference)) = record
        .value
        .as_ref()
        .and_then(|stored| stored.kind.as_ref())
    else {
        panic!("expected a table value: {record:?}");
    };
    let value_id = reference.value_id.expect("table values have an ID");
    ValueId {
        run_seq: value_id.run_seq,
        index_in_command: value_id.index_in_command,
    }
}

/// Coordination whose runs seal a history segment with every command and write a checkpoint
/// every two commands, so offloading leaves little history local.
fn offloading_config() -> FlowCoordinationConfig {
    let config = FlowCoordinationConfig::default();
    FlowCoordinationConfig {
        run_settings: RunSettings {
            history_segment_target_bytes: 1,
            checkpoint_command_threshold: 2,
            ..config.run_settings
        },
        ..config
    }
}

fn segmented_opener() -> Arc<dyn TimeTravelHistoryOpener> {
    Arc::new(SegmentedTimeTravelHistory)
}

/// Every target type restores the point that apply first produced: the debug replay check
/// hashes the staged rows and compares them with the hash recorded at that point.
#[tokio::test(start_paused = true)]
async fn every_target_type_restores_the_point_its_first_apply_recorded() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let run_id = travel.start_flow(None).await;
    travel.set_attribute("status", string_value("paid")).await;
    let paid_seq = travel.run_record(run_id).next_run_seq - 1;
    travel
        .set_attribute("report", string_value(&"r".repeat(400)))
        .await;
    let paid_time = travel.history_record(run_id, paid_seq).timestamp_micros;
    let requests = [
        reset(FlowResetType::Beginning, true),
        reset_to_time(paid_time),
        ResetFlowRequest {
            step_type: START_STEP.to_owned(),
            ..reset(FlowResetType::StepType, true)
        },
        ResetFlowRequest {
            step_execution_id: START_STEP_EXECUTION.to_owned(),
            step_method: FlowResetStepMethod::WaitFor as i32,
            ..reset(FlowResetType::StepExecutionId, false)
        },
        ResetFlowRequest {
            run_attempt: 1,
            ..reset_to_time(paid_time)
        },
    ];
    for (index, request) in requests.into_iter().enumerate() {
        let attempt = travel.reset(request).await.expect("the reset applies");
        assert_eq!(attempt, u32::try_from(index).unwrap() + 2);
        travel.assert_every_travel_verified(u64::try_from(index).unwrap() + 1);
    }
    assert_eq!(travel.committed_count(is_time_travel), 5);
    assert_eq!(
        travel.committed_count(is_stage),
        5,
        "small results take one chunk"
    );
    assert!(travel.run_record(run_id).time_travel_staging.is_none());
    travel.shutdown().await;
}

/// With history offload enabled, history is still local in this release, so the same path
/// restores the same points.
#[tokio::test(start_paused = true)]
async fn time_travel_takes_the_same_path_while_the_partition_offloads_history() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let partition = travel.context().partition;
    let (_, ticket) = travel
        .committer()
        .submit(
            travel.context(),
            FlowCommand {
                flow_id: FLOW_ID.to_owned(),
                command: Some(Command::SetHistoryOffloadMode(
                    dexzd_protocol::dexzd::v1::SetHistoryOffloadModeCommand {
                        enabled: true,
                        partition: u32::from(partition.0),
                    },
                )),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("the mode command is submitted");
    ticket.wait().await.expect("the mode commits");
    let run_id = travel.start_flow(None).await;
    for index in 0..4 {
        travel
            .set_attribute("note", string_value(&format!("{index}").repeat(200)))
            .await;
    }
    assert_eq!(
        travel
            .reset(reset(FlowResetType::Beginning, true))
            .await
            .expect("the reset applies"),
        2
    );
    travel.assert_every_travel_verified(1);
    assert!(travel.attribute(run_id, "note").is_some());
    travel.shutdown().await;
}

/// A running Step heartbeats while the owner computes; the heartbeat lands after the
/// computed seq, so apply ignores it and the time travel needs one computation.
#[tokio::test(start_paused = true)]
async fn a_run_with_a_heartbeating_step_travels_in_one_computation() {
    let cluster = TestCluster::new();
    let history = Arc::new(ScriptedHistory::default());
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        history.clone(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let run_id = travel
        .start_flow(Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        }))
        .await;
    let invocation = StepInvocationKey {
        step_execution_id: START_STEP_EXECUTION.to_owned(),
        method: StepMethod::Execute as i32,
    };
    travel
        .commit(
            run_id,
            Command::StepAttemptStarted(StepAttemptStartedCommand {
                invocation: Some(invocation.clone()),
                attempt: 1,
                task_id: vec![7; 16],
                pull_routing: None,
            }),
        )
        .await;
    travel.set_attribute("progress", string_value("0")).await;
    let heartbeat = |round: u32| {
        Command::StepAttemptHeartbeat(StepAttemptHeartbeatCommand {
            invocation: Some(invocation.clone()),
            attempt: 1,
            checkpoint: Some(step_attempt_heartbeat_command::Checkpoint::Value(
                string_value(&format!("round {round}")),
            )),
            task_id: vec![7; 16],
        })
    };
    travel.commit(run_id, heartbeat(0)).await;
    let (opened, release) = history.gate_next_open();
    let resetting = travel.reset(reset(FlowResetType::Beginning, true));
    let heartbeats = async {
        opened.notified().await;
        for round in 1..4 {
            travel.commit(run_id, heartbeat(round)).await;
        }
        release.notify_one();
    };
    let (attempt, ()) = tokio::join!(resetting, heartbeats);
    assert_eq!(attempt.expect("the reset applies"), 2);
    assert_eq!(history.opens.load(Ordering::SeqCst), 1, "one computation");
    assert_eq!(travel.committed_count(is_time_travel), 1);
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// Input committed while the owner computes is reapplied from the local tail. Input that left
/// the local store by then makes apply reject the result; the owner computes again with the
/// input from its uploaded segment.
#[tokio::test(start_paused = true)]
async fn input_committed_during_the_computation_is_reapplied() {
    let cluster = TestCluster::new();
    let history = Arc::new(ScriptedHistory::default());
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        history.clone(),
        offloading_config(),
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    travel.set_attribute("early", string_value("1")).await;
    let (opened, release) = history.gate_next_open();
    let resetting = travel.reset(reset(FlowResetType::Beginning, true));
    let late_input = async {
        opened.notified().await;
        travel.set_attribute("late", string_value("2")).await;
        release.notify_one();
    };
    let (attempt, ()) = tokio::join!(resetting, late_input);
    assert_eq!(attempt.expect("the reset applies"), 2);
    assert_eq!(history.opens.load(Ordering::SeqCst), 1);
    assert!(travel.attribute(run_id, "early").is_some());
    assert!(
        travel.attribute(run_id, "late").is_some(),
        "the late input is reapplied"
    );
    let fork = travel.history_record(run_id, travel.run_record(run_id).next_run_seq - 1);
    assert_eq!(fork.reapplied_inputs.len(), 2);

    let (opened, release) = history.gate_next_open();
    let resetting = travel.reset(reset(FlowResetType::Beginning, true));
    let offloaded_input = async {
        opened.notified().await;
        travel.set_attribute("offloaded", string_value("3")).await;
        let seq = travel.run_record(run_id).next_run_seq - 1;
        assert!(
            travel.offload_sealed_history(run_id).await >= seq,
            "the input's segment is uploaded and its local record deleted"
        );
        release.notify_one();
    };
    let (attempt, ()) = tokio::join!(resetting, offloaded_input);
    assert_eq!(attempt.expect("the reset applies"), 3);
    assert!(
        history.opens.load(Ordering::SeqCst) > 3,
        "apply rejected the first result, and the owner computed again over segments it fetched"
    );
    assert!(travel.history_object_reads() > 0);
    assert!(
        travel.attribute(run_id, "offloaded").is_some(),
        "the input read from object storage is reapplied"
    );
    travel.assert_every_travel_verified(2);
    travel.shutdown().await;
}

/// Every target type restores the point its first apply recorded when the target's history and
/// checkpoints exist only in uploaded segments, and when the target is local after uploaded
/// history.
#[tokio::test(start_paused = true)]
async fn targets_whose_history_exists_only_in_uploaded_segments_restore_their_recorded_points() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        offloading_config(),
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    travel.set_attribute("status", string_value("paid")).await;
    let paid_seq = travel.run_record(run_id).next_run_seq - 1;
    let paid_time = travel.history_record(run_id, paid_seq).timestamp_micros;
    for index in 0..4 {
        travel
            .set_attribute("report", string_value(&format!("{index}").repeat(400)))
            .await;
    }
    let requests = [
        reset(FlowResetType::Beginning, true),
        reset_to_time(paid_time),
        ResetFlowRequest {
            step_type: START_STEP.to_owned(),
            ..reset(FlowResetType::StepType, true)
        },
        ResetFlowRequest {
            step_execution_id: START_STEP_EXECUTION.to_owned(),
            step_method: FlowResetStepMethod::WaitFor as i32,
            ..reset(FlowResetType::StepExecutionId, false)
        },
        ResetFlowRequest {
            run_attempt: 1,
            ..reset_to_time(paid_time)
        },
    ];
    let mut expected_verified = 0;
    for request in requests {
        let uploaded_through_seq = travel.offload_sealed_history(run_id).await;
        assert_eq!(
            uploaded_through_seq,
            travel.run_record(run_id).next_run_seq - 1,
            "every record is uploaded and only genesis stays local"
        );
        let reads_before = travel.history_object_reads();
        let attempt = travel.reset(request).await.expect("the reset applies");
        expected_verified += 1;
        assert_eq!(attempt, expected_verified + 1);
        travel.assert_every_travel_verified(u64::from(expected_verified));
        assert!(
            travel.history_object_reads() > reads_before,
            "the computation read uploaded segments"
        );
    }

    // A local target after uploaded history.
    travel.offload_sealed_history(run_id).await;
    travel.set_attribute("tail", string_value("local")).await;
    let tail_seq = travel.run_record(run_id).next_run_seq - 1;
    let tail_time = travel.history_record(run_id, tail_seq).timestamp_micros;
    travel
        .set_attribute("after-tail", string_value("local"))
        .await;
    let attempt = travel
        .reset(reset_to_time(tail_time))
        .await
        .expect("the reset applies");
    assert_eq!(attempt, expected_verified + 2);
    travel.assert_every_travel_verified(u64::from(expected_verified) + 1);
    assert!(travel.attribute(run_id, "tail").is_some());
    assert!(travel.attribute(run_id, "after-tail").is_none());
    travel.shutdown().await;
}

/// A value that left the local store after the computation: apply rejects the result naming
/// it, and the owner computes again and stages the value from object storage.
#[tokio::test(start_paused = true)]
async fn a_value_offloaded_during_the_computation_is_staged_on_the_next_one() {
    let cluster = TestCluster::new();
    let history = Arc::new(ScriptedHistory::default());
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        history.clone(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let run_id = travel.start_flow(None).await;
    travel
        .set_attribute("report", string_value(&"o".repeat(400)))
        .await;
    let old_value = table_value_id(&travel.attribute(run_id, "report").expect("the report"));
    for index in 0..3 {
        travel
            .set_attribute("note", string_value(&index.to_string()))
            .await;
    }
    let target_seq = travel.run_record(run_id).next_run_seq - 1;
    let target_time = travel.history_record(run_id, target_seq).timestamp_micros;
    travel
        .set_attribute("report", string_value(&"n".repeat(400)))
        .await;
    let (opened, release) = history.gate_next_open();
    let resetting = travel.reset(reset_to_time(target_time));
    let offload = async {
        opened.notified().await;
        let value = travel.offload_superseded_value(run_id, old_value);
        history
            .removed_values
            .lock()
            .expect("value lock")
            .insert(old_value, value);
        release.notify_one();
    };
    let (attempt, ()) = tokio::join!(resetting, offload);
    assert_eq!(attempt.expect("the reset applies"), 2);
    assert_eq!(history.opens.load(Ordering::SeqCst), 2);
    let restored = travel
        .attribute(run_id, "report")
        .expect("the report is restored");
    assert_eq!(table_value_id(&restored), old_value);
    let value_key = value_row_key(
        travel.context().flow_hash,
        run_id,
        RunTable::Value,
        old_value,
    );
    assert!(
        travel
            .view()
            .get(ColumnFamily::Value, &value_key)
            .expect("the value reads")
            .is_some(),
        "the staged value is stored again"
    );
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// A value that reapplied input references left the local store after the computation: apply
/// rejects the result naming it without halting, and the owner computes again and stages the
/// value from object storage.
#[tokio::test(start_paused = true)]
async fn a_value_reapplied_input_references_is_staged_after_it_leaves_the_store() {
    let cluster = TestCluster::new();
    let history = Arc::new(ScriptedHistory::default());
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        history.clone(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let run_id = travel.start_flow(None).await;
    travel
        .set_attribute("report", string_value(&"o".repeat(400)))
        .await;
    let old_value = table_value_id(&travel.attribute(run_id, "report").expect("the report"));
    let reference = ValueReference {
        run_id,
        value_id: old_value,
    }
    .to_string();
    // Request validation admits no reference, so the input is committed as apply accepts it.
    travel
        .commit(
            run_id,
            Command::AttributesSet(AttributesSetCommand {
                request: Some(SetAttributesRequest {
                    flow_id: FLOW_ID.to_owned(),
                    attributes: vec![AttributeWrite {
                        key: "copy".to_owned(),
                        value: Some(Value {
                            kind: Some(value::Kind::InternalBlobIdForStringValue(reference)),
                        }),
                        ..AttributeWrite::default()
                    }],
                    request_id: "copy-report".to_owned(),
                    ..SetAttributesRequest::default()
                }),
                hard_delete_objects: Vec::new(),
                request_fingerprint: b"copy-report".to_vec(),
            }),
        )
        .await;
    assert_eq!(
        table_value_id(&travel.attribute(run_id, "copy").expect("the copy")),
        old_value
    );
    travel.set_attribute("report", string_value("new")).await;
    travel.set_attribute("copy", string_value("new")).await;
    let (opened, release) = history.gate_next_open();
    let resetting = travel.reset(reset(FlowResetType::Beginning, true));
    let offload = async {
        opened.notified().await;
        let value = travel.offload_superseded_value(run_id, old_value);
        history
            .removed_values
            .lock()
            .expect("value lock")
            .insert(old_value, value);
        release.notify_one();
    };
    let (attempt, ()) = tokio::join!(resetting, offload);
    assert_eq!(attempt.expect("the reset applies"), 2);
    assert_eq!(
        history.opens.load(Ordering::SeqCst),
        2,
        "apply rejected the first result and the owner computed again"
    );
    let value_key = value_row_key(
        travel.context().flow_hash,
        run_id,
        RunTable::Value,
        old_value,
    );
    assert!(
        travel
            .view()
            .get(ColumnFamily::Value, &value_key)
            .expect("the value reads")
            .is_some(),
        "the staged value is stored again"
    );
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// A replay that needs more memory than configured fails with RESOURCE_EXHAUSTED, which names
/// the configuration key.
#[tokio::test(start_paused = true)]
async fn a_replay_over_the_memory_budget_fails_explicitly() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig {
            time_travel_replay_memory_bytes: 2048,
            ..FlowCoordinationConfig::default()
        },
    )
    .await;
    let run_id = travel.start_flow(None).await;
    travel
        .set_attribute("report", string_value(&"m".repeat(4096)))
        .await;
    let target_seq = travel.run_record(run_id).next_run_seq - 1;
    let target_time = travel.history_record(run_id, target_seq).timestamp_micros;
    let error = travel
        .reset(reset_to_time(target_time))
        .await
        .expect_err("the replay exceeds 2 KiB");
    assert_eq!(error.grpc_code(), grpc_code::RESOURCE_EXHAUSTED, "{error}");
    assert!(
        error
            .to_string()
            .contains("history.timeTravelReplayMemoryBytes"),
        "{error}"
    );
    assert_eq!(travel.committed_count(is_stage), 0, "nothing is staged");
    travel.shutdown().await;
}

/// Uploaded history larger than the replay memory budget fails the ResetFlow with
/// RESOURCE_EXHAUSTED once the fetched part exceeds it, before every segment is read.
#[tokio::test(start_paused = true)]
async fn uploaded_history_over_the_memory_budget_fails_before_every_segment_is_read() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig {
            time_travel_replay_memory_bytes: 8 << 10,
            ..offloading_config()
        },
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    for index in 0..200 {
        travel
            .set_attribute("note", string_value(&format!("{index:03}").repeat(100)))
            .await;
    }
    travel.offload_sealed_history(run_id).await;
    let segment_count = u64::try_from(travel.segments(run_id).len()).expect("few segments");
    assert!(segment_count > 200);
    let reads_before = travel.history_object_reads();
    let error = travel
        .reset(reset(FlowResetType::Beginning, true))
        .await
        .expect_err("the uploaded history exceeds 8 KiB");
    assert_eq!(error.grpc_code(), grpc_code::RESOURCE_EXHAUSTED, "{error}");
    assert!(
        error
            .to_string()
            .contains("history.timeTravelReplayMemoryBytes"),
        "{error}"
    );
    let reads = travel.history_object_reads() - reads_before;
    assert!(
        reads < segment_count,
        "{reads} object reads for {segment_count} segments of two parts each"
    );
    assert_eq!(travel.committed_count(is_stage), 0, "nothing is staged");
    assert_eq!(travel.run_record(run_id).current_attempt, 1);
    travel.shutdown().await;
}

/// Two ResetFlows of one run at once run one after the other: the second computes on the
/// first one's attempt, so neither is rejected.
#[tokio::test(start_paused = true)]
async fn concurrent_resets_of_one_run_are_serialized() {
    let cluster = TestCluster::new();
    let history = Arc::new(ScriptedHistory::default());
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        history.clone(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let run_id = travel.start_flow(None).await;
    travel.set_attribute("status", string_value("paid")).await;
    let (first, second) = tokio::join!(
        travel.reset(reset(FlowResetType::Beginning, true)),
        travel.reset(reset(FlowResetType::Beginning, false)),
    );
    let mut attempts = vec![
        first.expect("the first reset applies"),
        second.expect("the second reset applies"),
    ];
    attempts.sort_unstable();
    assert_eq!(attempts, vec![2, 3]);
    assert_eq!(
        *history.seen_attempts.lock().expect("attempt lock"),
        vec![1, 2],
        "the second computation started after the first TimeTravel committed"
    );
    assert_eq!(travel.run_record(run_id).current_attempt, 3);
    travel.assert_every_travel_verified(2);
    travel.shutdown().await;
}

/// Two registries do not serialize their ResetFlows; the one computed at the older attempt is
/// rejected, then computes again.
#[tokio::test(start_paused = true)]
async fn competing_resets_from_two_registries_compute_again_after_the_attempt_changes() {
    let cluster = TestCluster::new();
    let competing = Arc::new(ScriptedHistory::default());
    let travel = TravelNode::start_with_registries(
        &cluster,
        LocalStoreConfig::InMemory,
        vec![segmented_opener(), competing.clone()],
        FlowCoordinationConfig::default(),
        dexzd_engine::HistoryReadsConfig::default(),
    )
    .await;
    let run_id = travel.start_flow(None).await;
    travel.set_attribute("status", string_value("paid")).await;
    let (opened, release) = competing.gate_next_open();
    let competing_reset = travel.other_registries[0]
        .reset_flow(TenantId::DEFAULT, reset(FlowResetType::Beginning, true));
    let first_reset = async {
        opened.notified().await;
        let attempt = travel.reset(reset(FlowResetType::Beginning, false)).await;
        release.notify_one();
        attempt
    };
    let (competing_outcome, first_outcome) = tokio::join!(competing_reset, first_reset);
    assert_eq!(first_outcome.expect("the first reset applies"), 2);
    assert_eq!(
        competing_outcome
            .expect("the competing reset computes again and applies")
            .run_attempt,
        3
    );
    assert_eq!(
        *competing.seen_attempts.lock().expect("attempt lock"),
        vec![1, 2],
        "the competing reset computed again on the first reset's attempt"
    );
    assert_eq!(travel.run_record(run_id).current_attempt, 3);
    travel.assert_every_travel_verified(2);
    travel.shutdown().await;
}

/// A Beginning reset replays a small checkpoint, but its large reapplied writes exceed the
/// budget, so it stages nothing.
#[tokio::test(start_paused = true)]
async fn a_beginning_reset_that_holds_more_input_than_its_budget_fails_explicitly() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig {
            time_travel_replay_memory_bytes: 256 << 10,
            ..FlowCoordinationConfig::default()
        },
    )
    .await;
    let run_id = travel.start_flow(None).await;
    for index in 0..8 {
        travel
            .set_attribute(
                &format!("document-{index}"),
                string_value(&"d".repeat(64 << 10)),
            )
            .await;
    }
    let error = travel
        .reset(reset(FlowResetType::Beginning, true))
        .await
        .expect_err("eight 64 KiB writes do not fit in a 256 KiB budget");
    assert_eq!(error.grpc_code(), grpc_code::RESOURCE_EXHAUSTED, "{error}");
    let message = error.to_string();
    assert!(
        message.contains("external input")
            && message.contains("history.timeTravelReplayMemoryBytes"),
        "{message}"
    );
    assert_eq!(travel.committed_count(is_stage), 0, "nothing is staged");
    let attempt = travel
        .reset(reset(FlowResetType::Beginning, false))
        .await
        .expect("a reset that reapplies no writes fits");
    assert_eq!(attempt, 2);
    assert_eq!(travel.run_record(run_id).current_attempt, 2);
    travel.shutdown().await;
}

/// Input larger than one log record is staged in several chunks through the proposal path.
#[tokio::test(start_paused = true)]
async fn a_result_larger_than_a_log_record_is_staged_in_several_commands() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let run_id = travel.start_flow(None).await;
    for (index, fill) in ['a', 'b', 'c'].into_iter().enumerate() {
        travel
            .set_attribute(
                &format!("document-{index}"),
                string_value(&fill.to_string().repeat(12 << 20)),
            )
            .await;
    }
    assert_eq!(
        travel
            .reset(reset(FlowResetType::Beginning, true))
            .await
            .expect("the reset applies"),
        2
    );
    assert_eq!(travel.committed_count(is_stage), 4);
    for index in 0..3 {
        assert!(
            travel
                .attribute(run_id, &format!("document-{index}"))
                .is_some()
        );
    }
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// Computes `request` on the committed view the way the owner does and commits its Stage
/// commands, without the TimeTravel; returns the TimeTravel command that names them.
async fn stage_without_travel(
    travel: &TravelNode,
    run_id: RunId,
    request: ResetFlowRequest,
) -> FlowCommand {
    let unstaged = FlowCommand {
        flow_id: FLOW_ID.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command_id: vec![3; 16],
        command: Some(Command::TimeTravel(
            dexzd_protocol::dexzd::v1::TimeTravelCommand {
                request: Some(request),
                should_restore_running_as_pending: true,
                ..dexzd_protocol::dexzd::v1::TimeTravelCommand::default()
            },
        )),
        ..FlowCommand::default()
    };
    let mut commands =
        dexzd_apply::stage_time_travel_for_test(&travel.context(), &travel.view(), &unstaged)
            .expect("the result computes")
            .expect("the request is accepted")
            .commands;
    let time_travel = commands.pop().expect("staging ends with the TimeTravel");
    for stage in commands {
        let (_, ticket) = travel
            .committer()
            .submit(travel.context(), stage, CommitUrgency::Prompt)
            .expect("the stage is submitted");
        let committed = ticket.wait().await.expect("the stage commits");
        assert!(matches!(
            committed.outcome.reply.reply,
            Some(dexzd_protocol::dexzd::v1::command_reply::Reply::Ok(_))
        ));
    }
    time_travel
}

/// A hot standby replays Stage and TimeTravel commands to the serving store's state, including
/// a staging that no TimeTravel used yet.
#[tokio::test(start_paused = true)]
async fn a_hot_standby_replays_staged_time_travel_to_the_serving_state() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let serving_directory = directory.path().join("serving");
    let standby_directory = directory.path().join("standby");
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        crate::hot_standby::rocksdb_store(&serving_directory),
        segmented_opener(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let mut standby_config = cluster.config(crate::hot_standby::rocksdb_store(&standby_directory));
    standby_config.commit.debug_replay_checks = true;
    let standby = dexzd_engine::HotStandby::start(
        standby_config,
        crate::hot_standby::fast_standby_config(),
        dexzd_engine::HotStandbyDependencies {
            metrics: Arc::new(dexzd_metrics::NodeMetrics::unregistered()),
            object_store: Arc::new(crate::hot_standby::ProcessObjectStore::new(
                cluster.object_store.clone(),
            )),
            clock: cluster.clock.clone(),
            applier: Arc::new(DeterministicCommandApplier),
            id_source: Arc::new(SystemEngineIdSource),
            restart_jitter_seed: 7,
        },
    )
    .expect("the hot standby starts");
    let run_id = travel.start_flow(None).await;
    travel.set_attribute("status", string_value("paid")).await;
    travel
        .set_attribute("report", string_value(&"r".repeat(400)))
        .await;
    assert_eq!(
        travel
            .reset(reset(FlowResetType::Beginning, true))
            .await
            .expect("the reset applies"),
        2
    );
    travel
        .set_attribute("status", string_value("refunded"))
        .await;
    stage_without_travel(&travel, run_id, reset(FlowResetType::Beginning, false)).await;
    assert!(travel.run_record(run_id).time_travel_staging.is_some());
    crate::hot_standby::wait_until_caught_up(&cluster, &standby).await;
    travel.shutdown().await;
    standby.shutdown().await;
    crate::hot_standby::assert_same_stores(&serving_directory, &standby_directory);
}

/// A snapshot taken between the Stage commands and their TimeTravel restores the staging,
/// and the TimeTravel then applies on the restored node.
#[tokio::test(start_paused = true)]
async fn a_restored_snapshot_keeps_the_staging_for_its_time_travel() {
    let cluster = TestCluster::new();
    let first = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let run_id = first.start_flow(None).await;
    first.set_attribute("status", string_value("paid")).await;
    let time_travel =
        stage_without_travel(&first, run_id, reset(FlowResetType::Beginning, true)).await;
    let staging = first
        .run_record(run_id)
        .time_travel_staging
        .expect("the run has a staging");
    assert!(staging.is_complete);
    first
        .node
        .take_snapshot()
        .await
        .expect("the snapshot is taken");
    first.shutdown().await;

    let second = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig::default(),
    )
    .await;
    assert_eq!(second.run_record(run_id).time_travel_staging, Some(staging));
    let (_, ticket) = second
        .committer()
        .submit(second.context(), time_travel, CommitUrgency::Prompt)
        .expect("the TimeTravel is submitted");
    let committed = ticket.wait().await.expect("the TimeTravel commits");
    assert!(
        matches!(
            committed.outcome.reply.reply,
            Some(dexzd_protocol::dexzd::v1::command_reply::Reply::TimeTravel(
                _
            ))
        ),
        "{:?}",
        committed.outcome.reply
    );
    assert!(second.run_record(run_id).time_travel_staging.is_none());
    assert!(second.attribute(run_id, "status").is_some());
    assert_eq!(
        second.node.time_travel_verification_counts(),
        (0, 1),
        "the target predates the restored snapshot, so it has no recorded hash"
    );
    second.shutdown().await;
}

/// A value that offload deleted locally after its grace period comes from its uploaded segment:
/// time travel to a point that held it reads it from object storage and stores it again.
#[tokio::test(start_paused = true)]
async fn a_value_offloaded_after_its_grace_period_is_staged_from_object_storage() {
    const GRACE_MICROS: i64 = 10 * 60 * 1_000_000;
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        offloading_config(),
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    travel
        .set_attribute("report", string_value(&"o".repeat(400)))
        .await;
    let old_value = table_value_id(&travel.attribute(run_id, "report").expect("the report"));
    travel.set_attribute("note", string_value("0")).await;
    let target_seq = travel.run_record(run_id).next_run_seq - 1;
    let target_time = travel.history_record(run_id, target_seq).timestamp_micros;
    travel
        .set_attribute("report", string_value(&"n".repeat(400)))
        .await;
    travel.offload_sealed_history(run_id).await;
    cluster.clock.shift(GRACE_MICROS + 1_000_000);
    travel
        .set_attribute("note", string_value("after the grace period"))
        .await;
    travel.offload_sealed_history(run_id).await;

    let flow_hash = travel.context().flow_hash;
    let birth_key = value_row_key(flow_hash, run_id, RunTable::ValueBirth, old_value);
    let value_key = value_row_key(flow_hash, run_id, RunTable::Value, old_value);
    let birth = |view: &dyn FlowStateReader| {
        ValueBirthRecord::decode(
            view.get(ColumnFamily::Flow, &birth_key)
                .expect("the birth reads")
                .expect("the birth row stays")
                .as_slice(),
        )
        .expect("the birth decodes")
    };
    assert!(
        birth(&travel.view()).is_offloaded,
        "the old report is offloaded"
    );
    assert!(
        travel
            .view()
            .get(ColumnFamily::Value, &value_key)
            .expect("the value reads")
            .is_none()
    );

    let reads_before = travel.history_object_reads();
    let attempt = travel
        .reset(reset_to_time(target_time))
        .await
        .expect("the reset applies");
    assert_eq!(attempt, 2);
    assert!(
        travel.history_object_reads() > reads_before,
        "the computation read the value from object storage"
    );
    let restored = travel
        .attribute(run_id, "report")
        .expect("the report is restored");
    assert_eq!(table_value_id(&restored), old_value);
    assert!(
        travel
            .view()
            .get(ColumnFamily::Value, &value_key)
            .expect("the value reads")
            .is_some(),
        "the staged value is stored again"
    );
    assert!(!birth(&travel.view()).is_offloaded);
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// What [`offload_a_referenced_report`] built.
struct ReferencedReport {
    value: ValueId,
    /// Time of the command after the report, which wrote a checkpoint.
    checkpointed_time: i64,
    /// Time of the command that copied the report by reference.
    copied_time: i64,
}

/// Writes a large report, a note whose command writes a checkpoint, and a copy that references
/// the report by value reference. Supersedes both, then uploads and lets the grace period pass,
/// so offload deletes the report locally.
async fn offload_a_referenced_report(
    cluster: &TestCluster,
    travel: &TravelNode,
    run_id: RunId,
) -> ReferencedReport {
    const GRACE_MICROS: i64 = 10 * 60 * 1_000_000;
    travel
        .set_attribute("report", string_value(&"o".repeat(400)))
        .await;
    let value = table_value_id(&travel.attribute(run_id, "report").expect("the report"));
    travel.set_attribute("note", string_value("0")).await;
    let checkpointed_seq = travel.run_record(run_id).next_run_seq - 1;
    let reference = ValueReference {
        run_id,
        value_id: value,
    }
    .to_string();
    // Request validation admits no reference, so the input is committed as apply accepts it.
    travel
        .commit(
            run_id,
            Command::AttributesSet(AttributesSetCommand {
                request: Some(SetAttributesRequest {
                    flow_id: FLOW_ID.to_owned(),
                    attributes: vec![AttributeWrite {
                        key: "copy".to_owned(),
                        value: Some(Value {
                            kind: Some(value::Kind::InternalBlobIdForStringValue(reference)),
                        }),
                        ..AttributeWrite::default()
                    }],
                    request_id: "copy-report".to_owned(),
                    ..SetAttributesRequest::default()
                }),
                hard_delete_objects: Vec::new(),
                request_fingerprint: b"copy-report".to_vec(),
            }),
        )
        .await;
    let copied_seq = travel.run_record(run_id).next_run_seq - 1;
    let checkpointed_time = travel
        .history_record(run_id, checkpointed_seq)
        .timestamp_micros;
    let copied_time = travel.history_record(run_id, copied_seq).timestamp_micros;
    let checkpoint_seqs = attempt_checkpoint_seqs(travel, run_id);
    assert!(
        checkpoint_seqs.contains(&checkpointed_seq) && !checkpoint_seqs.contains(&copied_seq),
        "the replay to the copy starts at the checkpoint before it: {checkpoint_seqs:?}"
    );
    travel.set_attribute("report", string_value("new")).await;
    travel.set_attribute("copy", string_value("new")).await;
    travel.offload_sealed_history(run_id).await;
    cluster.clock.shift(GRACE_MICROS + 1_000_000);
    travel
        .set_attribute("note", string_value("after the grace period"))
        .await;
    travel.offload_sealed_history(run_id).await;
    assert!(
        value_birth(travel, run_id, value).is_offloaded,
        "the report is offloaded"
    );
    ReferencedReport {
        value,
        checkpointed_time,
        copied_time,
    }
}

fn attempt_checkpoint_seqs(travel: &TravelNode, run_id: RunId) -> Vec<u64> {
    let key = attempt_record_key(travel.context().flow_hash, run_id, 1);
    AttemptRecord::decode(
        travel
            .view()
            .get(ColumnFamily::Flow, &key)
            .expect("the attempt record reads")
            .expect("attempt 1 exists")
            .as_slice(),
    )
    .expect("the attempt record decodes")
    .checkpoint_seqs
}

fn value_birth(travel: &TravelNode, run_id: RunId, value_id: ValueId) -> ValueBirthRecord {
    let key = value_row_key(
        travel.context().flow_hash,
        run_id,
        RunTable::ValueBirth,
        value_id,
    );
    ValueBirthRecord::decode(
        travel
            .view()
            .get(ColumnFamily::Flow, &key)
            .expect("the birth reads")
            .expect("the birth row stays")
            .as_slice(),
    )
    .expect("the birth decodes")
}

fn is_value_stored(travel: &TravelNode, run_id: RunId, value_id: ValueId) -> bool {
    let key = value_row_key(
        travel.context().flow_hash,
        run_id,
        RunTable::Value,
        value_id,
    );
    travel
        .view()
        .get(ColumnFamily::Value, &key)
        .expect("the value reads")
        .is_some()
}

/// A replayed command that references an offloaded value stops the computation, which reports
/// the value; the owner fetches it from object storage and the next computation restores it.
#[tokio::test(start_paused = true)]
async fn an_offloaded_value_that_the_replay_reads_is_fetched_from_object_storage() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        offloading_config(),
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    let report = offload_a_referenced_report(&cluster, &travel, run_id).await;
    assert!(!is_value_stored(&travel, run_id, report.value));

    let reads_before = travel.history_object_reads();
    let attempt = travel
        .reset(reset_to_time(report.copied_time))
        .await
        .expect("the reset applies");
    assert_eq!(attempt, 2);
    assert!(travel.history_object_reads() > reads_before);
    for key in ["report", "copy"] {
        let restored = travel
            .attribute(run_id, key)
            .expect("the Attribute is restored");
        assert_eq!(table_value_id(&restored), report.value, "{key}");
    }
    assert!(is_value_stored(&travel, run_id, report.value));
    assert!(!value_birth(&travel, run_id, report.value).is_offloaded);
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// Reapplied input that references an offloaded value: the computation reports the value, and
/// the owner fetches it from object storage before staging it with the input.
#[tokio::test(start_paused = true)]
async fn an_offloaded_value_that_reapplied_input_references_is_fetched_from_object_storage() {
    let cluster = TestCluster::new();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        offloading_config(),
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    let report = offload_a_referenced_report(&cluster, &travel, run_id).await;

    let attempt = travel
        .reset(ResetFlowRequest {
            history_event_time: (i128::from(report.checkpointed_time) * 1000).to_string(),
            ..reset(FlowResetType::HistoryEventTime, true)
        })
        .await
        .expect("the reset applies");
    assert_eq!(attempt, 2);
    // The reapplied copy held the report again, and the reapplied rewrites superseded it.
    assert!(is_value_stored(&travel, run_id, report.value));
    let birth = value_birth(&travel, run_id, report.value);
    assert!(!birth.is_offloaded && birth.garbage_seq > 0, "{birth:?}");
    for key in ["report", "copy"] {
        let attribute = travel.attribute(run_id, key).expect("the Attribute exists");
        assert_ne!(
            attribute.value.and_then(|stored| stored.kind),
            None,
            "{key} holds the reapplied rewrite"
        );
    }
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// Replayed permission mappings that each read a different offloaded Attribute value stop the
/// replay at every one, so a computation discovers them one round at a time, more of them than
/// any fixed number of rounds; each round fetches the value the replay stopped at.
#[tokio::test(start_paused = true)]
async fn a_replay_that_discovers_many_offloaded_values_one_by_one_finishes() {
    const GRACE_MICROS: i64 = 10 * 60 * 1_000_000;
    const CHAIN: usize = 20;
    let cluster = TestCluster::new();
    let config = offloading_config();
    let travel = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig {
            run_settings: RunSettings {
                checkpoint_command_threshold: 60,
                ..config.run_settings
            },
            ..config
        },
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    let mut roles = Vec::with_capacity(CHAIN);
    for index in 0..CHAIN {
        let key = format!("role-{index}");
        let text = format!("{index}-").repeat(200);
        travel.set_attribute(&key, string_value(&text)).await;
        let value_id = table_value_id(&travel.attribute(run_id, &key).expect("the role"));
        roles.push((key, text, value_id));
    }
    // Pad to the next checkpoint, so the replay to the mappings starts before every mapping.
    while !attempt_checkpoint_seqs(&travel, run_id)
        .contains(&(travel.run_record(run_id).next_run_seq - 1))
    {
        travel.set_attribute("pad", string_value("pad")).await;
    }
    let checkpoint_seq = travel.run_record(run_id).next_run_seq - 1;
    for (index, (key, text, _)) in roles.iter().enumerate() {
        travel
            .commit(
                run_id,
                Command::AttributesSet(AttributesSetCommand {
                    request: Some(SetAttributesRequest {
                        flow_id: FLOW_ID.to_owned(),
                        attributes: vec![AttributeWrite {
                            key: "trigger".to_owned(),
                            value: Some(string_value(&index.to_string())),
                            ..AttributeWrite::default()
                        }],
                        request_id: format!("grant-{index}"),
                        action_permission_mappings: Some(ActionPermissionMappings {
                            mappings: vec![ActionPermissionMapping {
                                attribute_key: key.clone(),
                                equal_values: vec![string_value(text)],
                                required_permission: format!("permission-{index}"),
                            }],
                        }),
                        ..SetAttributesRequest::default()
                    }),
                    hard_delete_objects: Vec::new(),
                    request_fingerprint: format!("grant-{index}").into_bytes(),
                }),
            )
            .await;
    }
    let target_seq = travel.run_record(run_id).next_run_seq - 1;
    let target_time = travel.history_record(run_id, target_seq).timestamp_micros;
    assert!(
        attempt_checkpoint_seqs(&travel, run_id)
            .iter()
            .all(|seq| *seq <= checkpoint_seq || *seq > target_seq),
        "no checkpoint between the mappings"
    );
    for (key, _, _) in &roles {
        travel.set_attribute(key, string_value("new")).await;
    }
    travel.offload_sealed_history(run_id).await;
    cluster.clock.shift(GRACE_MICROS + 1_000_000);
    travel
        .set_attribute("note", string_value("after the grace period"))
        .await;
    travel.offload_sealed_history(run_id).await;
    for (_, _, value_id) in &roles {
        assert!(value_birth(&travel, run_id, *value_id).is_offloaded);
    }

    let attempt = travel
        .reset(reset_to_time(target_time))
        .await
        .expect("the reset applies");
    assert_eq!(attempt, 2);
    for (key, _, value_id) in &roles {
        let restored = travel.attribute(run_id, key).expect("the role is restored");
        assert_eq!(table_value_id(&restored), *value_id, "{key}");
        assert!(is_value_stored(&travel, run_id, *value_id));
    }
    travel.assert_every_travel_verified(1);
    travel.shutdown().await;
}

/// An offloaded value that the disk read cache serves counts against the replay memory budget,
/// as one read from object storage does. The replay reads one segment whose records the
/// computation does not hold, so only the fetched data exceeds the budget.
#[tokio::test(start_paused = true)]
async fn a_cached_offloaded_value_counts_against_the_replay_memory_budget() {
    const GRACE_MICROS: i64 = 10 * 60 * 1_000_000;
    let cache = tempfile::tempdir().expect("temporary directory");
    let cluster = TestCluster::new();
    let config = offloading_config();
    let travel = TravelNode::start_with_history_reads(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        FlowCoordinationConfig {
            time_travel_replay_memory_bytes: 512 << 10,
            run_settings: RunSettings {
                history_segment_target_bytes: 512 << 10,
                ..config.run_settings
            },
            ..config
        },
        dexzd_engine::HistoryReadsConfig {
            read_cache_directory: Some(cache.path().join("history-read-cache")),
            ..dexzd_engine::HistoryReadsConfig::default()
        },
    )
    .await;
    travel.enable_history_offload().await;
    let run_id = travel.start_flow(None).await;
    // Live to the end, these stay local; only their records go to the segment.
    for index in 0..2 {
        travel
            .set_attribute(
                &format!("filler-{index}"),
                string_value(&format!("{index}").repeat(32 << 10)),
            )
            .await;
    }
    travel
        .set_attribute("report", string_value(&"o".repeat(128 << 10)))
        .await;
    let old_value = table_value_id(&travel.attribute(run_id, "report").expect("the report"));
    while !attempt_checkpoint_seqs(&travel, run_id)
        .contains(&(travel.run_record(run_id).next_run_seq - 1))
    {
        travel.set_attribute("pad", string_value("pad")).await;
    }
    travel.set_attribute("note", string_value("0")).await;
    let target_seq = travel.run_record(run_id).next_run_seq - 1;
    let target_time = travel.history_record(run_id, target_seq).timestamp_micros;
    travel.set_attribute("report", string_value("new")).await;
    travel
        .set_attribute("tail", string_value(&"t".repeat(256 << 10)))
        .await;
    travel.offload_sealed_history(run_id).await;
    cluster.clock.shift(GRACE_MICROS + 1_000_000);
    travel
        .set_attribute("note", string_value("after the grace period"))
        .await;
    travel.offload_sealed_history(run_id).await;
    assert!(value_birth(&travel, run_id, old_value).is_offloaded);
    let segments = travel.segments(run_id);
    assert!(
        segments
            .iter()
            .any(|segment| segment.first_seq <= 2 && segment.last_seq >= target_seq),
        "one segment holds the fillers and the target: {segments:?}"
    );
    // LoadBlobs reads the value from its segment and caches it under the key time travel reads.
    let reference = ValueReference {
        run_id,
        value_id: old_value,
    }
    .to_string();
    let loaded = travel
        .registry
        .load_blobs(
            TenantId::DEFAULT,
            LoadBlobsRequest {
                entries: vec![LoadBlobRequestEntry {
                    flow_id: FLOW_ID.to_owned(),
                    blob_value: Some(Value {
                        kind: Some(value::Kind::InternalBlobIdForStringValue(reference.clone())),
                    }),
                }],
            },
        )
        .await
        .expect("LoadBlobs reads the offloaded value");
    assert!(loaded.values.contains_key(&reference), "{loaded:?}");

    let error = travel
        .reset(reset_to_time(target_time))
        .await
        .expect_err("the fetched records and the cached value exceed the budget");
    assert_eq!(error.grpc_code(), grpc_code::RESOURCE_EXHAUSTED, "{error}");
    assert!(
        error
            .to_string()
            .contains("the history it reads from object storage"),
        "the cached value counts with the fetched records: {error}"
    );
    assert_eq!(travel.committed_count(is_stage), 0, "nothing is staged");
    travel.shutdown().await;
}
