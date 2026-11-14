// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deleting uploaded history locally (plan section 5.5): records, checkpoints, and metadata after
//! the acknowledgment, superseded values after their grace period, the storage totals, and where
//! LoadBlobs finds an offloaded value. Also reviving an offloaded value before a command that
//! references it (plan section 5.11), and the HistoryLocalCleanup command that finishes local
//! deletion and window pruning for runs that stop receiving commands.

#[path = "support/prune_cursors.rs"]
mod prune_cursors;

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::read_model::{
    OffloadedValueLocation, SegmentedHistoryPage, get_history_events_from_segments, load_blobs,
    locate_offloaded_value, read_history_segment_content,
};
use dexzd_apply::values::NoOffloadedValues;
use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyOutcome, FlowStateReader, LoadedHistorySegments,
    SegmentCheckpoint, SegmentedTimeTravelComputation, StateReadError, StateRow, StateWrite,
    TimeTravelComputation, TimeTravelComputationRequest, apply_flow_command,
    compute_time_travel_from_segments,
};
use dexzd_history_segment::{HistorySegmentContent, encode_history_segment};
use dexzd_keyspace::row_keys::{
    attempt_record_key, history_local_cleanup_pending_index_key, prefix_range, run_named_key,
    run_singleton_key, value_row_key,
};
use dexzd_keyspace::{
    ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, ValueId, ValueReference,
    parse_run_key, run_prefix, run_table_prefix,
};
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowResetStepMethod, FlowResetType, FlowTimeoutPolicy,
    InvokeExecuteMethodResponse, LoadBlobRequestEntry, ResetFlowRequest, SetAttributesRequest,
    StartFlowRequest, StepDecision, StepMovement, StepOptions, StopFlowRequest, StopType,
    UpdateFlowConfigRequest, Value, WorkerTarget, flow_history_event, value,
};
use dexzd_protocol::dexzd::v1::{
    AttemptRecord, AttributeRecord, AttributesSetCommand, CommandReply, FlowCommand,
    FlowConfigUpdateCommand, FlowStartCommand, FlowStopCommand, HistoryLocalCleanupCommand,
    HistoryRecord, HistoryRowMetadata, HistorySegmentRecord, HistorySegmentState,
    HistorySegmentUploadedCommand, RetentionTimerId, RunPurgeCommand, RunRecord, RunSettings,
    RunStorageUsage, SetHistoryOffloadModeCommand, StepAttemptHeartbeatCommand,
    StepAttemptStartedCommand, StepExecutionRecord, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, TimeTravelStagedChunk, TimerId, TimersFiredCommand,
    ValueBirthRecord, ValueGarbageRecord, ValueRecord, ValueReviveCommand, command_reply,
    flow_command::Command, step_attempt_heartbeat_command, step_method_completed_command,
    stored_value, timer_id,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const START_STEP: &str = "ChargeStep";
const START_STEP_EXECUTION: &str = "ChargeStep-1";
/// The grace period of superseded values, by command time.
const GRACE: i64 = 10 * 60 * SECOND;
/// Command time a run goes without commands before its pending local cleanup comes due.
const QUIET: i64 = 60 * SECOND;
/// Command time between cleanups that wait for offload to turn on again.
const OFFLOAD_RECHECK: i64 = 60 * 60 * SECOND;

#[derive(Default, Clone)]
struct MemoryState {
    rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
}

impl FlowStateReader for MemoryState {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(self.rows.get(&(column_family, key.to_vec())).cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(self
            .rows
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect())
    }
}

impl MemoryState {
    fn commit(&mut self, outcome: &ApplyOutcome) {
        for write in &outcome.writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    self.rows
                        .insert((*column_family, key.clone()), value.clone());
                }
                StateWrite::Delete { column_family, key }
                | StateWrite::SingleDelete { column_family, key } => {
                    self.rows.remove(&(*column_family, key.clone()));
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => {
                    self.rows.retain(|(family, key), _| {
                        !(family == column_family && key >= start && key < end)
                    });
                }
            }
        }
    }
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    /// Command time between two commands.
    step_micros: i64,
    flow_id: String,
    run_id: Vec<u8>,
    request_count: u32,
    /// Content of each acknowledged segment by first seq, as its uploaded object holds it.
    uploaded: BTreeMap<u64, (HistorySegmentRecord, HistorySegmentContent)>,
    /// Every command applied, in log order.
    log: Vec<FlowCommand>,
}

impl Harness {
    /// A run whose partition offloads history from its start.
    fn offloading(settings: RunSettings) -> Self {
        let mut harness = Self::new();
        harness.set_offload_mode(true);
        harness.start(settings);
        harness
    }

    /// A run whose partition never offloads history.
    fn not_offloading(settings: RunSettings) -> Self {
        let mut harness = Self::new();
        harness.start(settings);
        harness
    }

    fn new() -> Self {
        let flow_id = "local-deletion-flow";
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, flow_id);
        Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000 * SECOND,
            step_micros: 1000,
            flow_id: flow_id.to_owned(),
            run_id: Vec::new(),
            request_count: 0,
            uploaded: BTreeMap::new(),
            log: Vec::new(),
        }
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        self.now_micros += self.step_micros;
        let is_run_command = !matches!(
            body,
            Command::FlowStart(_) | Command::SetHistoryOffloadMode(_)
        );
        let command = FlowCommand {
            flow_id: self.flow_id.clone(),
            run_id: if is_run_command {
                self.run_id.clone()
            } else {
                Vec::new()
            },
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            command: Some(body),
            ..FlowCommand::default()
        };
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        self.state.commit(&outcome);
        if let Ok(run_id) = RunId::from_slice(&self.run_id) {
            prune_cursors::assert_prune_cursors_skip_no_rows(
                &self.state.rows,
                self.context.flow_hash,
                run_id,
            );
        }
        self.log.push(command);
        outcome
    }

    fn advance(&mut self, micros: i64) {
        self.now_micros += micros;
    }

    /// Moves the command clock so the next command applies at exactly `micros`.
    fn advance_to(&mut self, micros: i64) {
        assert!(micros > self.now_micros, "command time only moves forward");
        self.now_micros = micros - self.step_micros;
    }

    fn set_offload_mode(&mut self, is_enabled: bool) {
        let outcome = self.apply(Command::SetHistoryOffloadMode(
            SetHistoryOffloadModeCommand {
                enabled: is_enabled,
                partition: u32::from(self.context.partition.0),
            },
        ));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
    }

    fn start(&mut self, settings: RunSettings) {
        self.start_with_request(
            settings,
            StartFlowRequest {
                flow_id: self.flow_id.clone(),
                flow_type: "LocalDeletionFlow".to_owned(),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            },
        );
    }

    /// Starts a run whose start Step's Execute is pending at once.
    fn start_with_step(&mut self, settings: RunSettings) {
        self.start_with_request(
            settings,
            StartFlowRequest {
                flow_id: self.flow_id.clone(),
                flow_type: "LocalDeletionFlow".to_owned(),
                start_step_type: START_STEP.to_owned(),
                step_options: Some(StepOptions {
                    skip_wait_for: true,
                    ..StepOptions::default()
                }),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            },
        );
    }

    fn start_with_request(&mut self, settings: RunSettings, request: StartFlowRequest) {
        let outcome = self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(request),
            settings: Some(settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget {
                is_pull_mode: true,
                ..WorkerTarget::default()
            }),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
            panic!("the run starts: {:?}", outcome.reply);
        };
        self.run_id = reply.run_id.clone();
    }

    fn set_attribute(&mut self, key: &str, text: String) -> ApplyOutcome {
        self.request_count += 1;
        let request_id = format!("write-{}", self.request_count);
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: key.to_owned(),
                    value: Some(Value {
                        kind: Some(value::Kind::StringValue(text)),
                    }),
                    ..AttributeWrite::default()
                }],
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        outcome
    }

    /// Applies a bookkeeping command that records neither a semantic point nor an event, as a
    /// Step heartbeat does.
    fn bookkeep(&mut self) -> ApplyOutcome {
        let outcome = self.apply(Command::FlowConfigUpdate(FlowConfigUpdateCommand {
            request: Some(UpdateFlowConfigRequest {
                flow_id: self.flow_id.clone(),
                flow_config: Some(FlowConfig::default()),
                ..UpdateFlowConfigRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        outcome
    }

    /// Acknowledges every SEALED segment, oldest first, keeping the content of its object.
    /// Returns the outcome of each acknowledgment.
    fn ack_all(&mut self) -> Vec<ApplyOutcome> {
        let mut outcomes = Vec::new();
        loop {
            let uploaded_through_seq = self.run_record().history_uploaded_through_seq;
            let Some(segment) = self
                .segments()
                .into_iter()
                .find(|segment| segment.last_seq > uploaded_through_seq)
            else {
                return outcomes;
            };
            let content = read_history_segment_content(
                &self.state,
                self.context.flow_hash,
                self.run(),
                segment.first_seq,
            )
            .expect("the segment content reads")
            .expect("the segment exists");
            let encoded = encode_history_segment(&content).expect("the content encodes");
            let outcome = self.apply(Command::HistorySegmentUploaded(
                HistorySegmentUploadedCommand {
                    first_seq: segment.first_seq,
                    last_seq: segment.last_seq,
                    content_checksum: encoded.content_checksum.to_vec(),
                    object_size_bytes: encoded.bytes.len() as u64,
                },
            ));
            assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
            self.uploaded.insert(segment.first_seq, (segment, content));
            outcomes.push(outcome);
        }
    }

    /// Applies the HistoryLocalCleanup command that the owner proposes once cleanup is due.
    fn clean_up(&mut self) -> ApplyOutcome {
        self.apply(Command::HistoryLocalCleanup(HistoryLocalCleanupCommand {}))
    }

    /// Applies due cleanups, as the owner does, until none is pending. Returns how many applied.
    fn clean_up_until_done(&mut self) -> usize {
        let mut cleanup_count = 0;
        while let Some(due_micros) = self.cleanup_due() {
            assert!(cleanup_count < 100, "local cleanup never finishes");
            if due_micros > self.now_micros + self.step_micros {
                self.advance_to(due_micros);
            }
            let outcome = self.clean_up();
            assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
            cleanup_count += 1;
        }
        cleanup_count
    }

    fn stop(&mut self) {
        let outcome = self.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: self.flow_id.clone(),
                stop_type: StopType::Cancel as i32,
                ..StopFlowRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
    }

    fn fire_retention(&mut self) -> ApplyOutcome {
        self.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: 0,
                kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
            }],
        }))
    }

    fn cleanup_due(&self) -> Option<i64> {
        self.run_record().history_local_cleanup_due_micros
    }

    fn has_cleanup_index_row(&self) -> bool {
        let key = history_local_cleanup_pending_index_key(
            self.context.partition,
            self.context.flow_hash,
            self.run(),
        );
        self.state
            .get(ColumnFamily::PartitionIndex, &key)
            .expect("memory reads never fail")
            .is_some()
    }

    /// Returns the seq of the checkpoint that window pruning keeps: the newest one at or before
    /// the window start.
    fn window_keep_from_seq(&self) -> u64 {
        let record = self.run_record();
        self.attempt_record(record.current_attempt)
            .checkpoint_seqs
            .into_iter()
            .filter(|seq| *seq <= record.window_start_seq)
            .max()
            .expect("a checkpoint precedes the window start")
    }

    fn run(&self) -> RunId {
        RunId::from_slice(&self.run_id).expect("the harness has a run")
    }

    fn get<RowMessage: Message + Default>(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Option<RowMessage> {
        self.state
            .rows
            .get(&(column_family, key.to_vec()))
            .map(|bytes| RowMessage::decode(bytes.as_slice()).expect("row decodes"))
    }

    fn run_record(&self) -> RunRecord {
        let key = run_singleton_key(self.context.flow_hash, self.run(), RunTable::RunRecord);
        self.get(ColumnFamily::Flow, &key)
            .expect("the run record exists")
    }

    fn storage(&self) -> RunStorageUsage {
        self.run_record().storage.expect("the run counts storage")
    }

    fn attempt_record(&self, attempt: u32) -> AttemptRecord {
        self.get(
            ColumnFamily::Flow,
            &attempt_record_key(self.context.flow_hash, self.run(), attempt),
        )
        .expect("the attempt record exists")
    }

    fn table_rows<RowMessage: Message + Default>(
        &self,
        column_family: ColumnFamily,
        table: RunTable,
    ) -> Vec<(Vec<u8>, RowMessage)> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), table);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((column_family, start)..(column_family, end))
            .map(|((_, key), value)| {
                (
                    key[prefix.len()..].to_vec(),
                    RowMessage::decode(value.as_slice()).expect("row decodes"),
                )
            })
            .collect()
    }

    fn segments(&self) -> Vec<HistorySegmentRecord> {
        self.table_rows::<HistorySegmentRecord>(ColumnFamily::Flow, RunTable::HistorySegment)
            .into_iter()
            .map(|(_, segment)| segment)
            .collect()
    }

    /// Seqs of the history records stored locally.
    fn stored_record_seqs(&self) -> BTreeSet<u64> {
        self.table_rows::<HistoryRecord>(ColumnFamily::History, RunTable::HistoryRecord)
            .into_iter()
            .map(|(suffix, _)| seq_of(&suffix))
            .collect()
    }

    /// (seq, attempt) of the checkpoints stored locally.
    fn stored_checkpoints(&self) -> BTreeSet<(u64, u32)> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), RunTable::Checkpoint);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::History, start)..(ColumnFamily::History, end))
            .map(|((_, key), _)| {
                let suffix = &key[prefix.len()..];
                let attempt = u32::from_be_bytes(suffix[..4].try_into().expect("attempt"));
                (seq_of(&suffix[4..]), attempt)
            })
            .collect()
    }

    /// History metadata by (seq, table byte, attempt).
    fn metadata(&self) -> BTreeMap<(u64, u8, u32), HistoryRowMetadata> {
        self.table_rows::<HistoryRowMetadata>(ColumnFamily::Flow, RunTable::HistoryRowMetadata)
            .into_iter()
            .map(|(suffix, metadata)| {
                let attempt = u32::from_be_bytes(suffix[9..].try_into().expect("attempt"));
                ((seq_of(&suffix[..8]), suffix[8], attempt), metadata)
            })
            .collect()
    }

    fn attribute_value_id(&self, key: &str) -> ValueId {
        let row_key = run_named_key(self.context.flow_hash, self.run(), RunTable::Attribute, key);
        let record: AttributeRecord = self
            .get(ColumnFamily::Flow, &row_key)
            .expect("the Attribute exists");
        match record.value.and_then(|stored| stored.kind) {
            Some(stored_value::Kind::TableValue(reference)) => {
                let value_id = reference.value_id.expect("table values have an ID");
                ValueId {
                    run_seq: value_id.run_seq,
                    index_in_command: value_id.index_in_command,
                }
            }
            other => panic!("Attribute {key} is not a table value: {other:?}"),
        }
    }

    fn value_row(&self, value_id: ValueId) -> Option<Vec<u8>> {
        self.state
            .rows
            .get(&(
                ColumnFamily::Value,
                value_row_key(
                    self.context.flow_hash,
                    self.run(),
                    RunTable::Value,
                    value_id,
                ),
            ))
            .cloned()
    }

    fn birth(&self, value_id: ValueId) -> Option<ValueBirthRecord> {
        self.get(
            ColumnFamily::Flow,
            &value_row_key(
                self.context.flow_hash,
                self.run(),
                RunTable::ValueBirth,
                value_id,
            ),
        )
    }

    /// Garbage rows by value: superseded seq and record.
    fn garbage(&self) -> BTreeMap<ValueId, (u64, ValueGarbageRecord)> {
        self.table_rows::<ValueGarbageRecord>(ColumnFamily::Flow, RunTable::ValueGarbage)
            .into_iter()
            .map(|(suffix, record)| {
                let value_id = ValueId::from_key_suffix(&suffix[8..]).expect("value ID");
                (value_id, (seq_of(&suffix[..8]), record))
            })
            .collect()
    }

    /// Writes a large `profile` twice and lets offload delete the first value locally. Returns
    /// that value and its deleted row.
    fn offload_first_profile(&mut self) -> (ValueId, Vec<u8>) {
        self.set_attribute("profile", large_text("first", 300));
        let first = self.attribute_value_id("profile");
        let first_row = self.value_row(first).expect("the first value is stored");
        self.set_attribute("profile", large_text("second", 300));
        self.ack_all();
        self.advance(GRACE + SECOND);
        self.bookkeep();
        assert!(
            matches!(self.locate(first), OffloadedValueLocation::Uploaded(_)),
            "the first profile is offloaded"
        );
        (first, first_row)
    }

    /// Returns the value an uploaded object holds for `value_id`.
    fn uploaded_value(&self, value_id: ValueId) -> Value {
        let row = self
            .uploaded
            .values()
            .flat_map(|(_, content)| &content.values)
            .find(|row| row.value_id == value_id)
            .expect("an uploaded object holds the value");
        ValueRecord::decode(row.row.as_slice())
            .expect("the value row decodes")
            .value
            .expect("the value row holds a value")
    }

    fn revive(&mut self, value_id: ValueId, value: Option<Value>) -> ApplyOutcome {
        self.apply(Command::ValueRevive(ValueReviveCommand {
            value_run_seq: value_id.run_seq,
            value_index_in_command: value_id.index_in_command,
            value,
        }))
    }

    fn reference(&self, value_id: ValueId) -> Value {
        Value {
            kind: Some(value::Kind::InternalBlobIdForStringValue(
                ValueReference {
                    run_id: self.run(),
                    value_id,
                }
                .to_string(),
            )),
        }
    }

    /// Writes Attribute `key` as a reference to `value_id`, as a Worker that echoes it would.
    fn set_reference(&mut self, key: &str, value_id: ValueId) -> ApplyOutcome {
        self.request_count += 1;
        let request_id = format!("reference-{}", self.request_count);
        let reference = self.reference(value_id);
        self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: key.to_owned(),
                    value: Some(reference),
                    ..AttributeWrite::default()
                }],
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }))
    }

    fn locate(&self, value_id: ValueId) -> OffloadedValueLocation {
        locate_offloaded_value(&self.state, self.context.flow_hash, self.run(), value_id)
            .expect("the location reads")
    }

    /// Recounts the run's storage from its rows, as the accounting rules state them.
    fn recount_storage(&self) -> RunStorageUsage {
        let prefix = run_prefix(self.context.flow_hash, self.run());
        let mut usage = RunStorageUsage::default();
        let mut birth_bytes = 0;
        for ((column_family, key), value) in &self.state.rows {
            if !key.starts_with(&prefix) {
                continue;
            }
            let row_bytes = (key.len() + value.len()) as u64;
            let table = parse_run_key(key).expect("valid run key").table;
            match (column_family, table) {
                (ColumnFamily::Flow, RunTable::HistoryRowMetadata) => {
                    let metadata =
                        HistoryRowMetadata::decode(value.as_slice()).expect("metadata decodes");
                    usage.history_bytes += row_bytes + metadata.row_size_bytes;
                }
                (ColumnFamily::Flow, RunTable::HistorySegment | RunTable::HistorySegmentValue) => {
                    usage.history_bytes += row_bytes;
                }
                (ColumnFamily::Flow, RunTable::ValueBirth) => {
                    let birth = ValueBirthRecord::decode(value.as_slice()).expect("birth decodes");
                    if birth.is_offloaded {
                        usage.history_bytes += row_bytes;
                    } else {
                        usage.state_row_bytes += row_bytes;
                        birth_bytes += birth.size_bytes;
                    }
                }
                (ColumnFamily::Flow, RunTable::ValueGarbage) => {
                    usage.state_row_bytes += row_bytes;
                    usage.superseded_value_bytes += ValueGarbageRecord::decode(value.as_slice())
                        .expect("garbage decodes")
                        .size_bytes;
                }
                (ColumnFamily::Flow, RunTable::RunRecord) => {
                    usage.state_row_bytes += row_bytes - run_record_search_cache_bytes(value);
                }
                (ColumnFamily::Flow, _) => usage.state_row_bytes += row_bytes,
                _ => {}
            }
        }
        usage.live_value_bytes = birth_bytes - usage.superseded_value_bytes;
        usage
    }

    /// Asserts the recorded totals equal the recount, apart from attempt state.
    fn assert_storage_recounts(&self) {
        let recorded = self.storage();
        let recount = self.recount_storage();
        assert_eq!(
            (
                recorded.state_row_bytes,
                recorded.live_value_bytes,
                recorded.superseded_value_bytes,
                recorded.history_bytes
            ),
            (
                recount.state_row_bytes,
                recount.live_value_bytes,
                recount.superseded_value_bytes,
                recount.history_bytes
            ),
            "recorded {recorded:?}, recount {recount:?}"
        );
    }

    /// Returns the history uploaded segments hold, loaded as an owner fetches it.
    fn loaded_segments(&self) -> LoadedHistorySegments {
        self.loaded_segments_matching(|_| true)
    }

    /// Returns the history of the uploaded segments that `is_loaded` accepts.
    fn loaded_segments_matching(
        &self,
        is_loaded: impl Fn(&HistorySegmentRecord) -> bool,
    ) -> LoadedHistorySegments {
        let mut loaded = LoadedHistorySegments::default();
        for (segment, content) in self.uploaded.values() {
            if !is_loaded(segment) {
                continue;
            }
            let records: Vec<HistoryRecord> = content
                .records
                .iter()
                .map(|row| HistoryRecord::decode(row.row.as_slice()).expect("record decodes"))
                .collect();
            loaded
                .insert(segment, records.into())
                .expect("uploaded records are in order");
            let checkpoints: Vec<SegmentCheckpoint> = content
                .checkpoints
                .iter()
                .map(|row| SegmentCheckpoint {
                    attempt: row.attempt,
                    seq: row.seq,
                    record: dexzd_protocol::dexzd::v1::CheckpointRecord::decode(row.row.as_slice())
                        .expect("checkpoint decodes"),
                })
                .collect();
            loaded
                .insert_checkpoints(segment, checkpoints.into())
                .expect("uploaded checkpoints are in order");
        }
        loaded
    }

    /// Computes `request` with the production time travel over `state` and every uploaded
    /// segment, and returns the staged chunks without their target.
    fn staged_result(
        &self,
        state: &MemoryState,
        request: &ResetFlowRequest,
    ) -> Vec<TimeTravelStagedChunk> {
        let computation = compute_time_travel_from_segments(
            &self.context,
            state,
            &self.loaded_segments(),
            &NoOffloadedValues,
            &TimeTravelComputationRequest {
                run_id: self.run(),
                reset: request,
                now_micros: self.now_micros,
                replay_memory_budget_bytes: u64::MAX,
            },
        )
        .expect("the computation runs");
        let SegmentedTimeTravelComputation::Computed(TimeTravelComputation::Computed(computed)) =
            computation
        else {
            panic!("the computation has every segment and succeeds: {computation:?}");
        };
        computed
            .stage_commands(b"staging")
            .expect("the result stages")
            .into_iter()
            .map(|command| {
                let mut chunk = TimeTravelStagedChunk::decode(command.chunk.as_slice())
                    .expect("the chunk decodes");
                chunk.target = None;
                chunk
            })
            .collect()
    }

    /// Computes `request` with the production time travel over the committed state and every
    /// uploaded segment.
    fn time_travel_computation(&self, request: &ResetFlowRequest) -> TimeTravelComputation {
        let computation = compute_time_travel_from_segments(
            &self.context,
            &self.state,
            &self.loaded_segments(),
            &NoOffloadedValues,
            &TimeTravelComputationRequest {
                run_id: self.run(),
                reset: request,
                now_micros: self.now_micros,
                replay_memory_budget_bytes: u64::MAX,
            },
        )
        .expect("the computation runs");
        let SegmentedTimeTravelComputation::Computed(computed) = computation else {
            panic!("the computation has every segment: {computation:?}");
        };
        computed
    }

    /// Event IDs of the first page, read as the engine does: uploaded seqs from their objects.
    fn history_event_ids(&self) -> Vec<i64> {
        let mut loaded = LoadedHistorySegments::default();
        for (segment, content) in self.uploaded.values() {
            let records: Vec<HistoryRecord> = content
                .records
                .iter()
                .map(|row| HistoryRecord::decode(row.row.as_slice()).expect("record decodes"))
                .collect();
            loaded
                .insert(segment, records.into())
                .expect("uploaded records are in order");
        }
        match get_history_events_from_segments(
            &self.state,
            self.context.flow_hash,
            self.run(),
            0,
            1000,
            &[],
            0,
            usize::MAX,
            &loaded,
            false,
        )
        .expect("history reads")
        {
            SegmentedHistoryPage::Page(page) => {
                page.events.iter().map(|event| event.event_id).collect()
            }
            SegmentedHistoryPage::NeedsSegments(needed) => {
                panic!("every uploaded segment is loaded, yet the read needs {needed:?}")
            }
        }
    }
}

fn settings(segment_target_bytes: u64) -> RunSettings {
    RunSettings {
        history_window_events: 10_000,
        history_prune_chunk_events: 1000,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 30,
        checkpoint_command_threshold: 4,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: segment_target_bytes,
        delete_expired_history_objects: false,
        superseded_value_grace_micros: 0,
        offload_history: None,
    }
}

/// Bytes of a run record's active Step type counts, which run storage never counts.
fn run_record_search_cache_bytes(value: &[u8]) -> u64 {
    let mut record = RunRecord::decode(value).expect("run record decodes");
    let full_size = record.encoded_len();
    record.search_active_step_type_counts = None;
    (full_size - record.encoded_len()) as u64
}

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

fn seq_of(bytes: &[u8]) -> u64 {
    u64::from_be_bytes(bytes[..8].try_into().expect("eight seq bytes"))
}

fn large_text(tag: &str, length: usize) -> String {
    let mut text = format!("{tag}:");
    while text.len() < length {
        text.push('x');
    }
    text
}

fn error_message(reply: &CommandReply) -> Option<(i32, &str)> {
    match &reply.reply {
        Some(command_reply::Reply::Error(error)) => Some((error.grpc_code, error.message.as_str())),
        _ => None,
    }
}

fn sealed_effect_count(outcome: &ApplyOutcome) -> usize {
    outcome
        .effects
        .iter()
        .filter(|effect| matches!(effect, ApplyEffect::HistorySegmentSealed { .. }))
        .count()
}

#[test]
fn an_acknowledgment_deletes_uploaded_rows_but_keeps_genesis_and_window_metadata() {
    let mut harness = Harness::offloading(settings(1));
    for round in 0..12 {
        harness.set_attribute("profile", format!("small-{round}"));
    }
    let metadata_before = harness.metadata();
    let checkpoints_before = harness.stored_checkpoints();
    let checkpoint_seqs_before = harness.attempt_record(1).checkpoint_seqs;
    let history_bytes_before = harness.storage().history_bytes;
    let event_ids_before = harness.history_event_ids();
    assert!(checkpoints_before.len() >= 3, "{checkpoints_before:?}");

    harness.ack_all();
    let record = harness.run_record();
    let uploaded_through_seq = record.history_uploaded_through_seq;
    assert_eq!(uploaded_through_seq, record.next_run_seq - 1);
    assert_eq!(
        record.history_local_rows_deleted_through_seq,
        uploaded_through_seq
    );
    assert_eq!(
        harness.stored_record_seqs(),
        BTreeSet::from([1]),
        "only genesis keeps its record"
    );
    assert_eq!(
        harness.stored_checkpoints(),
        BTreeSet::from([(1, 1)]),
        "only genesis keeps its checkpoint"
    );
    // Semantic records and checkpoints keep their metadata, without a row size, for the window.
    let metadata_after = harness.metadata();
    assert_eq!(
        metadata_after.keys().collect::<Vec<_>>(),
        metadata_before.keys().collect::<Vec<_>>()
    );
    for (identity, before) in &metadata_before {
        let after = &metadata_after[identity];
        let expected_size = if identity.0 == 1 {
            before.row_size_bytes
        } else {
            0
        };
        assert_eq!(
            after,
            &HistoryRowMetadata {
                row_size_bytes: expected_size,
                ..before.clone()
            },
            "{identity:?}"
        );
    }
    assert_eq!(
        harness.attempt_record(1).checkpoint_seqs,
        checkpoint_seqs_before,
        "checkpoint seqs stay listed until the window passes them"
    );
    assert!(harness.storage().history_bytes < history_bytes_before);
    harness.assert_storage_recounts();
    assert_eq!(
        harness.history_event_ids(),
        event_ids_before,
        "reads take the deleted rows from the objects"
    );
}

#[test]
fn records_without_events_lose_their_metadata_so_bookkeeping_keeps_metadata_bounded() {
    let mut harness = Harness::offloading(RunSettings {
        checkpoint_command_threshold: 100_000,
        ..settings(1)
    });
    harness.set_attribute("profile", "start".to_owned());
    let event_ids_before = harness.history_event_ids();
    for round in 0..300 {
        harness.bookkeep();
        if round % 10 == 9 {
            harness.ack_all();
        }
    }
    harness.ack_all();
    let record = harness.run_record();
    assert_eq!(
        record.history_local_rows_deleted_through_seq,
        record.next_run_seq - 1
    );
    let record_metadata: Vec<u64> = harness
        .metadata()
        .keys()
        .filter(|(_, table, _)| *table == RunTable::HistoryRecord as u8)
        .map(|(seq, _, _)| *seq)
        .collect();
    assert_eq!(
        record_metadata,
        [1, 2],
        "only genesis and the semantic record keep metadata after 300 bookkeeping records"
    );
    assert_eq!(harness.stored_record_seqs(), BTreeSet::from([1]));
    assert_eq!(harness.history_event_ids(), event_ids_before);
    harness.assert_storage_recounts();
}

#[test]
fn superseded_values_offload_only_after_their_grace_period() {
    let mut harness = Harness::offloading(settings(1));
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    let first_row = harness.value_row(first).expect("the first value is stored");
    harness.set_attribute("profile", large_text("second", 300));
    let second = harness.attribute_value_id("profile");
    let (superseded_seq, garbage) = harness.garbage()[&first];
    let superseded_at = garbage.superseded_at_micros;
    assert_eq!(
        superseded_at, harness.now_micros,
        "stamped with its command time"
    );
    let before = harness.storage();

    harness.ack_all();
    assert!(
        harness.run_record().history_uploaded_through_seq >= superseded_seq,
        "the segment that superseded the value is uploaded"
    );
    assert_eq!(harness.value_row(first), Some(first_row.clone()));
    assert!(!harness.birth(first).expect("birth").is_offloaded);
    assert_eq!(harness.locate(first), OffloadedValueLocation::Absent);

    // One second before the grace period ends, a command still keeps it.
    harness.advance(GRACE - (harness.now_micros - superseded_at) - SECOND - 1000);
    harness.bookkeep();
    assert_eq!(harness.value_row(first), Some(first_row.clone()));
    harness.ack_all();
    assert!(harness.garbage().contains_key(&first));

    harness.advance(2 * SECOND);
    harness.bookkeep();
    assert_eq!(harness.value_row(first), None, "the value row is offloaded");
    assert!(!harness.garbage().contains_key(&first));
    let birth = harness
        .birth(first)
        .expect("the birth record stays to find the value");
    assert!(birth.is_offloaded);
    assert_eq!(birth.garbage_seq, superseded_seq);
    assert!(harness.run_record().history_values_offloaded_through_seq >= superseded_seq);
    assert!(
        harness.value_row(second).is_some(),
        "the live value stays local"
    );

    let OffloadedValueLocation::Uploaded(segment) = harness.locate(first) else {
        panic!("the offloaded value is in an uploaded segment");
    };
    assert!((segment.first_seq..=segment.last_seq).contains(&superseded_seq));
    let (_, content) = &harness.uploaded[&segment.first_seq];
    let stored = content
        .values
        .iter()
        .find(|row| row.value_id == first)
        .expect("the object holds the value");
    assert_eq!(stored.row, first_row, "the object holds the deleted row");

    // The offloaded value no longer counts as superseded, and live bytes stay the same.
    let after = harness.storage();
    assert_eq!(after.live_value_bytes, before.live_value_bytes);
    assert_eq!(
        after.superseded_value_bytes,
        before.superseded_value_bytes - garbage.size_bytes
    );
    harness.assert_storage_recounts();
}

#[test]
fn values_and_rows_of_commands_applied_while_offload_was_off_stay_local() {
    let mut harness = Harness::offloading(settings(1));
    harness.set_attribute("profile", large_text("on-1", 300));
    let on_value = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("on-2", 300));
    let gap_value = harness.attribute_value_id("profile");
    harness.ack_all();

    harness.set_offload_mode(false);
    let gap_first_seq = harness.run_record().next_run_seq;
    harness.set_attribute("profile", large_text("off-1", 300));
    let inside_gap_value = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("off-2", 300));
    let after_gap_value = harness.attribute_value_id("profile");
    let gap_last_seq = harness.run_record().next_run_seq - 1;
    assert_eq!(harness.garbage()[&gap_value].0, gap_first_seq);
    assert_eq!(harness.garbage()[&inside_gap_value].0, gap_last_seq);

    harness.set_offload_mode(true);
    harness.set_attribute("profile", large_text("again-1", 300));
    harness.set_attribute("profile", large_text("again-2", 300));
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.bookkeep();
    harness.ack_all();
    let record = harness.run_record();
    assert!(record.history_uploaded_through_seq > gap_last_seq);
    assert!(record.history_local_rows_deleted_through_seq > gap_last_seq);
    assert!(record.history_values_offloaded_through_seq > gap_last_seq);

    assert!(harness.birth(on_value).expect("birth").is_offloaded);
    for value_id in [gap_value, inside_gap_value] {
        let birth = harness
            .birth(value_id)
            .expect("the window kept the gap value");
        assert!(
            !birth.is_offloaded,
            "a value superseded in the gap is never offloaded"
        );
        assert!(harness.value_row(value_id).is_some());
        assert!(harness.garbage().contains_key(&value_id));
    }
    assert!(
        harness.birth(after_gap_value).expect("birth").is_offloaded,
        "a value superseded by the first command after the gap is offloaded"
    );
    let stored = harness.stored_record_seqs();
    for seq in gap_first_seq..=gap_last_seq {
        assert!(stored.contains(&seq), "gap record {seq} stays local");
    }
    assert!(
        harness
            .segments()
            .iter()
            .all(|segment| segment.last_seq < gap_first_seq || segment.first_seq > gap_last_seq)
    );
    harness.assert_storage_recounts();
}

#[test]
fn deletion_is_bounded_per_command_and_later_commands_finish_it() {
    // Bookkeeping records take about 100 bytes, so one segment holds more than 4096 of them.
    let mut harness = Harness::offloading(RunSettings {
        checkpoint_command_threshold: 100_000,
        ..settings(800_000)
    });
    let mut sealed_at_command = None;
    for command in 0..12_000 {
        if sealed_effect_count(&harness.bookkeep()) > 0 {
            sealed_at_command = Some(command);
            break;
        }
    }
    let sealed_at_command = sealed_at_command.expect("the open segment sealed");
    assert!(
        sealed_at_command > 4096,
        "one segment holds {sealed_at_command} records"
    );
    harness.ack_all();
    let record = harness.run_record();
    let deleted_through_seq = record.history_local_rows_deleted_through_seq;
    assert!(
        deleted_through_seq > 1 && deleted_through_seq < record.history_uploaded_through_seq,
        "the acknowledgment deleted part of the segment: through {deleted_through_seq} of {}",
        record.history_uploaded_through_seq
    );
    let stored = harness.stored_record_seqs();
    assert!(
        stored
            .iter()
            .all(|seq| *seq == 1 || *seq > deleted_through_seq)
    );
    assert!(
        stored
            .iter()
            .any(|seq| *seq > 1 && *seq <= record.history_uploaded_through_seq),
        "uploaded records remain for later commands"
    );
    assert_eq!(
        harness.cleanup_due(),
        Some(harness.now_micros + QUIET),
        "unfinished deletion comes due once the run is quiet"
    );

    harness.bookkeep();
    let record = harness.run_record();
    assert!(record.history_local_rows_deleted_through_seq >= record.history_uploaded_through_seq);
    assert_eq!(harness.cleanup_due(), None, "the next command finished it");
    assert!(!harness.has_cleanup_index_row());
    assert!(
        harness
            .stored_record_seqs()
            .iter()
            .all(|seq| *seq == 1 || *seq > record.history_uploaded_through_seq)
    );
    harness.assert_storage_recounts();
}

#[test]
fn the_superseded_byte_cap_moves_the_window_only_while_offload_is_off() {
    let capped = RunSettings {
        max_retained_superseded_bytes: 1000,
        ..settings(1)
    };
    let mut offloading = Harness::offloading(capped);
    let mut local = Harness::not_offloading(capped);
    for round in 0..20 {
        offloading.set_attribute("profile", large_text(&format!("v{round}"), 500));
        local.set_attribute("profile", large_text(&format!("v{round}"), 500));
    }
    assert!(
        local.run_record().window_start_seq > 2,
        "the cap moves the window while offload is off"
    );
    let record = offloading.run_record();
    assert!(record.storage.expect("storage").superseded_value_bytes > 1000);
    assert!(
        record.window_start_seq <= 2,
        "the cap moves nothing while offload is on: window starts at {}",
        record.window_start_seq
    );
}

#[test]
fn turning_offload_off_stops_local_deletion_and_offloaded_values_stay_located() {
    let mut harness = Harness::offloading(settings(1));
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("second", 300));
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.bookkeep();
    assert!(harness.birth(first).expect("birth").is_offloaded);
    harness.set_attribute("profile", large_text("third", 300));
    harness.set_attribute("profile", large_text("fourth", 300));

    harness.set_offload_mode(false);
    let deleted_through_seq = harness.run_record().history_local_rows_deleted_through_seq;
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.bookkeep();
    let record = harness.run_record();
    assert!(record.history_uploaded_through_seq > deleted_through_seq);
    assert_eq!(
        record.history_local_rows_deleted_through_seq, deleted_through_seq,
        "nothing more is deleted while offload is off"
    );
    let stored = harness.stored_record_seqs();
    for seq in deleted_through_seq + 1..=record.history_uploaded_through_seq {
        assert!(stored.contains(&seq), "record {seq} stays local");
    }
    assert!(matches!(
        harness.locate(first),
        OffloadedValueLocation::Uploaded(_)
    ));
    harness.assert_storage_recounts();
}

#[test]
fn a_value_superseded_before_the_window_start_reads_as_absent() {
    let mut harness = Harness::offloading(RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..settings(1)
    });
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("second", 300));
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.bookkeep();
    assert!(matches!(
        harness.locate(first),
        OffloadedValueLocation::Uploaded(_)
    ));
    let superseded_seq = harness.birth(first).expect("birth").garbage_seq;
    for round in 0..12 {
        harness.set_attribute("other", format!("small-{round}"));
        harness.ack_all();
    }
    assert!(harness.run_record().window_start_seq > superseded_seq);
    let holding_segment = harness
        .segments()
        .into_iter()
        .find(|segment| (segment.first_seq..=segment.last_seq).contains(&superseded_seq))
        .expect("the segment that holds the value keeps its catalog row");
    assert_eq!(
        holding_segment.state,
        HistorySegmentState::Expired as i32,
        "window pruning passed the segment"
    );
    assert_eq!(
        harness.birth(first),
        None,
        "the segment's expiry deleted the birth record only its object held"
    );
    assert_eq!(
        harness.locate(first),
        OffloadedValueLocation::Absent,
        "the window passed the value"
    );
}

#[test]
fn local_storage_stays_flat_over_a_thousand_rewrites_of_a_large_attribute() {
    const VALUE_BYTES: usize = 32 << 10;
    let mut harness = Harness::offloading(RunSettings {
        max_run_state_bytes: 1 << 20,
        checkpoint_command_threshold: 1000,
        ..settings(1 << 20)
    });
    harness.step_micros = 10 * SECOND;
    let mut max_superseded_bytes = 0;
    for round in 0..1000 {
        harness.set_attribute(
            "profile",
            large_text(&format!("round-{round}"), VALUE_BYTES),
        );
        harness.ack_all();
        max_superseded_bytes = max_superseded_bytes.max(harness.storage().superseded_value_bytes);
    }
    let storage = harness.storage();
    let value_bytes = VALUE_BYTES as u64;
    assert!(
        storage.live_value_bytes >= value_bytes && storage.live_value_bytes < 2 * value_bytes,
        "one live value: {storage:?}"
    );
    // Values within their grace period and in the open segment stay local; nothing else does.
    let grace_commands = (GRACE / harness.step_micros) as u64 + 40;
    assert!(
        max_superseded_bytes < grace_commands * (value_bytes + 100),
        "superseded values stay bounded: {max_superseded_bytes}"
    );
    let value_rows = harness
        .table_rows::<dexzd_protocol::dexzd::v1::ValueRecord>(ColumnFamily::Value, RunTable::Value)
        .len() as u64;
    assert!(
        value_rows <= grace_commands,
        "{value_rows} local value rows"
    );
    let record = harness.run_record();
    let stored = harness.stored_record_seqs();
    assert!(
        stored
            .iter()
            .all(|seq| *seq == 1 || *seq > record.history_uploaded_through_seq),
        "only genesis and the open segment keep records"
    );
    assert!(
        harness.metadata().len() <= 1001 + 2,
        "metadata stays within the window"
    );
    harness.assert_storage_recounts();
}

// Offload deletes the metadata of records without events; the window start must not move past
// the kept checkpoint, or a replay from it skips such records.
#[test]
fn a_replay_from_the_kept_checkpoint_reads_records_whose_metadata_offload_deleted() {
    let mut harness = Harness::new();
    harness.set_offload_mode(true);
    harness.start_with_step(RunSettings {
        history_window_events: 3,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 7,
        ..settings(1)
    });
    for round in 0..6 {
        harness.set_attribute("before", format!("small-{round}"));
    }
    let invocation = StepInvocationKey {
        step_execution_id: START_STEP_EXECUTION.to_owned(),
        method: StepMethod::Execute as i32,
    };
    let started = harness.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(invocation.clone()),
        attempt: 1,
        task_id: vec![7; 16],
        pull_routing: None,
    }));
    assert!(is_ok(&started.reply), "{:?}", started.reply);
    let checkpoint_seq = harness.run_record().next_run_seq - 1;
    let heartbeat = harness.apply(Command::StepAttemptHeartbeat(StepAttemptHeartbeatCommand {
        invocation: Some(invocation),
        attempt: 1,
        checkpoint: Some(step_attempt_heartbeat_command::Checkpoint::Value(Value {
            kind: Some(value::Kind::StringValue("progress".to_owned())),
        })),
        task_id: vec![7; 16],
    }));
    assert!(is_ok(&heartbeat.reply), "{:?}", heartbeat.reply);
    for round in 0..4 {
        harness.set_attribute("after", format!("small-{round}"));
    }
    let record = harness.run_record();
    let window_start_seq = record.window_start_seq;
    let checkpoint_seqs = harness.attempt_record(1).checkpoint_seqs;
    let kept_checkpoint_seq = checkpoint_seqs
        .iter()
        .copied()
        .filter(|seq| *seq <= window_start_seq)
        .max();
    assert!(window_start_seq > checkpoint_seq + 1, "{record:?}");
    assert_eq!(
        kept_checkpoint_seq,
        Some(checkpoint_seq),
        "the window rebuilds from the checkpoint of the eventless start: {checkpoint_seqs:?}"
    );
    let target_time =
        harness.metadata()[&(window_start_seq, RunTable::HistoryRecord as u8, 0)].timestamp_micros;
    let request = ResetFlowRequest {
        flow_id: harness.flow_id.clone(),
        reset_type: FlowResetType::HistoryEventTime as i32,
        reason: "frontier".to_owned(),
        skip_writes_reapply: true,
        history_event_time: (i128::from(target_time) * 1000).to_string(),
        ..ResetFlowRequest::default()
    };
    let expected = harness.staged_result(&harness.state.clone(), &request);

    harness.ack_all();
    harness.bookkeep();
    harness.ack_all();
    let record = harness.run_record();
    assert_eq!(record.history_window_pruned_through_seq, checkpoint_seq - 1);
    let metadata = harness.metadata();
    for seq in [checkpoint_seq, checkpoint_seq + 1] {
        assert!(
            !metadata.contains_key(&(seq, RunTable::HistoryRecord as u8, 0)),
            "offload deleted the metadata of eventless record {seq}"
        );
    }
    assert!(
        harness
            .stored_record_seqs()
            .iter()
            .all(|seq| *seq == 1 || *seq > record.history_uploaded_through_seq)
    );
    let actual = harness.staged_result(&harness.state.clone(), &request);
    assert_eq!(
        actual, expected,
        "the replay reads the eventless records after the checkpoint"
    );

    // Reads show every event from the kept checkpoint on, and none of the pruned ones.
    let mut expected_event_ids = BTreeSet::from([1]);
    for (_, content) in harness.uploaded.values() {
        for row in &content.records {
            let record = HistoryRecord::decode(row.row.as_slice()).expect("record decodes");
            if record.seq >= checkpoint_seq {
                expected_event_ids.extend(record.events.iter().map(|event| event.event_id));
            }
        }
    }
    let event_ids: BTreeSet<i64> = harness.history_event_ids().into_iter().collect();
    assert_eq!(event_ids, expected_event_ids);
}

#[test]
fn a_revive_restores_an_offloaded_value_so_a_reference_to_it_commits() {
    let mut harness = Harness::offloading(settings(1));
    let (first, first_row) = harness.offload_first_profile();
    let superseded_seq = harness.birth(first).expect("birth").garbage_seq;

    // Without a revive, the reference names no value the run stores, as outside the window.
    let saved = harness.state.clone();
    let rejected = harness.set_reference("copy", first);
    let (code, message) = error_message(&rejected.reply).expect("the reference is rejected");
    assert_eq!(code, 3, "{message}");
    assert!(message.contains("does not name a value"), "{message}");
    harness.state = saved;

    let before = harness.storage();
    let record_before = harness.run_record();
    let records_before = harness.stored_record_seqs();
    let value = harness.uploaded_value(first);
    let revive = harness.revive(first, Some(value));
    assert!(is_ok(&revive.reply), "{:?}", revive.reply);
    assert_eq!(
        harness.value_row(first),
        Some(first_row),
        "the row is restored"
    );
    let birth = harness.birth(first).expect("birth");
    assert!(!birth.is_offloaded);
    assert_eq!(
        birth.garbage_seq, superseded_seq,
        "it stays superseded where it was"
    );
    let (garbage_seq, garbage) = harness.garbage()[&first];
    assert_eq!(garbage_seq, superseded_seq);
    assert_eq!(
        garbage.superseded_at_micros, harness.now_micros,
        "a new grace period starts"
    );
    assert_eq!(harness.locate(first), OffloadedValueLocation::Absent);
    // The revive records no history and takes no run seq.
    let record_after = harness.run_record();
    assert_eq!(record_after.next_run_seq, record_before.next_run_seq);
    assert_eq!(record_after.next_event_id, record_before.next_event_id);
    assert_eq!(harness.stored_record_seqs(), records_before);
    // The revived value is superseded again, so live bytes stay the same.
    let after_revive = harness.storage();
    assert_eq!(after_revive.live_value_bytes, before.live_value_bytes);
    assert_eq!(
        after_revive.superseded_value_bytes,
        before.superseded_value_bytes + garbage.size_bytes
    );
    harness.assert_storage_recounts();

    let copied = harness.set_reference("copy", first);
    assert!(is_ok(&copied.reply), "{:?}", copied.reply);
    assert_eq!(harness.attribute_value_id("copy"), first);
    assert_eq!(
        harness.birth(first).expect("birth").garbage_seq,
        0,
        "held again"
    );
    assert!(!harness.garbage().contains_key(&first));
    let after_copy = harness.storage();
    assert_eq!(
        after_copy.live_value_bytes,
        before.live_value_bytes + garbage.size_bytes
    );
    assert_eq!(
        after_copy.superseded_value_bytes,
        before.superseded_value_bytes
    );
    harness.assert_storage_recounts();

    // A second revive of the now local value changes nothing.
    let value = harness.uploaded_value(first);
    let repeated = harness.revive(first, Some(value));
    assert!(is_ok(&repeated.reply), "{:?}", repeated.reply);
    assert!(repeated.writes.is_empty());
}

#[test]
fn a_step_result_that_passes_an_offloaded_value_to_its_next_step_commits_after_a_revive() {
    let mut harness = Harness::new();
    harness.set_offload_mode(true);
    harness.start_with_step(settings(1));
    let invocation = StepInvocationKey {
        step_execution_id: START_STEP_EXECUTION.to_owned(),
        method: StepMethod::Execute as i32,
    };
    let started = harness.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(invocation.clone()),
        attempt: 1,
        task_id: vec![7; 16],
        pull_routing: None,
    }));
    assert!(is_ok(&started.reply), "{:?}", started.reply);
    let (first, first_row) = harness.offload_first_profile();
    let result = Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(invocation),
        attempt: 1,
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                step_decision: Some(StepDecision {
                    next_steps: vec![StepMovement {
                        step_type: "ReportStep".to_owned(),
                        step_input: Some(harness.reference(first)),
                        ..StepMovement::default()
                    }],
                    ..StepDecision::default()
                }),
                ..InvokeExecuteMethodResponse::default()
            },
        )),
        task_id: vec![7; 16],
        ..StepMethodCompletedCommand::default()
    });
    let has_next_step = |harness: &Harness| {
        harness
            .table_rows::<StepExecutionRecord>(ColumnFamily::Flow, RunTable::StepExecution)
            .iter()
            .any(|(_, step)| step.step_execution_id == "ReportStep-1")
    };

    // Without a revive, the result fails the attempt, which is not retried.
    let saved = harness.state.clone();
    harness.apply(result.clone());
    assert!(!has_next_step(&harness));
    assert!(harness.birth(first).expect("birth").is_offloaded);
    harness.state = saved;

    let value = harness.uploaded_value(first);
    let revive = harness.revive(first, Some(value));
    assert!(is_ok(&revive.reply), "{:?}", revive.reply);
    let completed = harness.apply(result);
    assert!(is_ok(&completed.reply), "{:?}", completed.reply);
    assert!(has_next_step(&harness), "the next Step holds the value");
    assert_eq!(harness.value_row(first), Some(first_row));
    assert_eq!(harness.birth(first).expect("birth").garbage_seq, 0);
    harness.assert_storage_recounts();
}

#[test]
fn a_revive_of_a_local_value_or_of_one_the_window_passed_changes_nothing() {
    let mut harness = Harness::offloading(RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        // Only the start checkpoint exists, so pruning never passes the value's segment: it
        // stays uploaded with the birth record offloaded while the window start moves past it.
        checkpoint_command_threshold: 1000,
        ..settings(1)
    });
    let live_text = large_text("live", 300);
    harness.set_attribute("live", live_text.clone());
    let live = harness.attribute_value_id("live");
    let local = harness.revive(
        live,
        Some(Value {
            kind: Some(value::Kind::StringValue(live_text)),
        }),
    );
    assert!(is_ok(&local.reply), "{:?}", local.reply);
    assert!(local.writes.is_empty(), "a local value needs no revive");

    let unknown = ValueId {
        run_seq: 9_999,
        index_in_command: 0,
    };
    let missing = harness.revive(
        unknown,
        Some(Value {
            kind: Some(value::Kind::StringValue("x".repeat(200))),
        }),
    );
    assert!(is_ok(&missing.reply), "{:?}", missing.reply);
    assert!(
        missing.writes.is_empty(),
        "a value without a birth record stays unknown"
    );

    let (first, _) = harness.offload_first_profile();
    let value = harness.uploaded_value(first);
    let superseded_seq = harness.birth(first).expect("birth").garbage_seq;
    for round in 0..12 {
        harness.set_attribute("other", format!("small-{round}"));
        harness.ack_all();
    }
    assert!(harness.run_record().window_start_seq > superseded_seq);
    assert!(harness.birth(first).is_some_and(|birth| birth.is_offloaded));
    let outside = harness.revive(first, Some(value));
    assert!(is_ok(&outside.reply), "{:?}", outside.reply);
    assert!(outside.writes.is_empty(), "the window passed the value");
    let rejected = harness.set_reference("copy", first);
    let (code, message) = error_message(&rejected.reply).expect("the reference is rejected");
    assert_eq!(code, 3, "{message}");
}

#[test]
fn a_revive_with_a_wrong_value_is_rejected_and_one_for_a_purging_run_is_not_found() {
    let mut harness = Harness::offloading(settings(1));
    let (first, _) = harness.offload_first_profile();
    let value = harness.uploaded_value(first);

    let wrong_size = harness.revive(
        first,
        Some(Value {
            kind: Some(value::Kind::StringValue("short".to_owned())),
        }),
    );
    let (code, message) = error_message(&wrong_size.reply).expect("a wrong size is rejected");
    assert_eq!(code, 3, "{message}");
    assert!(wrong_size.writes.is_empty());
    let without_value = harness.revive(first, None);
    let (code, message) = error_message(&without_value.reply).expect("no value is rejected");
    assert_eq!(code, 3, "{message}");
    assert!(harness.birth(first).expect("birth").is_offloaded);

    let mut record = harness.run_record();
    record.is_purging = true;
    harness.state.rows.insert(
        (
            ColumnFamily::Flow,
            run_singleton_key(
                harness.context.flow_hash,
                harness.run(),
                RunTable::RunRecord,
            ),
        ),
        record.encode_to_vec(),
    );
    let purging = harness.revive(first, Some(value));
    let (code, message) = error_message(&purging.reply).expect("a purging run is not found");
    assert_eq!(code, 5, "{message}");
    assert!(purging.writes.is_empty());
}

#[test]
fn replaying_the_log_with_its_revives_reaches_the_same_state() {
    let mut harness = Harness::offloading(settings(1));
    let (first, _) = harness.offload_first_profile();
    let value = harness.uploaded_value(first);
    let revive = harness.revive(first, Some(value));
    assert!(is_ok(&revive.reply), "{:?}", revive.reply);
    let copied = harness.set_reference("copy", first);
    assert!(is_ok(&copied.reply), "{:?}", copied.reply);
    harness.set_attribute("copy", large_text("third", 300));

    // A hot standby applies the same log to its own store.
    let mut standby = MemoryState::default();
    for command in &harness.log {
        let outcome =
            apply_flow_command(&harness.context, &standby, command).expect("replay applies");
        standby.commit(&outcome);
    }
    assert_eq!(standby.rows, harness.state.rows);
}

#[test]
fn while_offloading_every_visible_event_is_time_travelable_and_its_values_load() {
    // Checkpoints every 37 commands leave records between the kept checkpoint and W.
    let mut harness = Harness::offloading(RunSettings {
        history_window_events: 100,
        history_prune_chunk_events: 10,
        checkpoint_command_threshold: 37,
        ..settings(4096)
    });
    for round in 0..300 {
        harness.set_attribute("profile", large_text(&format!("round {round}"), 300));
        if round % 25 == 24 {
            harness.ack_all();
            harness.advance(GRACE + SECOND);
        }
    }
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.bookkeep();
    let record = harness.run_record();
    assert!(
        record.history_window_pruned_through_seq + 1 < record.window_start_seq,
        "records before W rebuild it: {record:?}"
    );
    assert!(
        harness.stored_record_seqs().len() < 100,
        "offload deleted uploaded records locally"
    );

    let SegmentedHistoryPage::Page(page) = get_history_events_from_segments(
        &harness.state,
        harness.context.flow_hash,
        harness.run(),
        0,
        1000,
        &[],
        0,
        usize::MAX,
        &harness.loaded_segments(),
        false,
    )
    .expect("history reads") else {
        panic!("every uploaded segment is loaded");
    };
    let window_start_last_event_id = harness.metadata()
        [&(record.window_start_seq, RunTable::HistoryRecord as u8, 0)]
        .last_assigned_event_id;
    // Every Attribute write records one event, so W's record holds only its last event.
    assert_eq!(page.events[0].event_id, 1, "FlowStart stays visible");
    assert_eq!(page.events[1].event_id, window_start_last_event_id);
    let range = page
        .pruned_event_id_range
        .expect("the window pruned events");
    assert_eq!(range.end_event_id, window_start_last_event_id - 1);

    // Reads never need a segment that lies wholly after genesis and before W.
    let is_hidden = |segment: &HistorySegmentRecord| {
        segment.first_seq > 1 && segment.last_seq < record.window_start_seq
    };
    assert!(
        harness
            .uploaded
            .values()
            .any(|(segment, _)| is_hidden(segment)),
        "a segment lies wholly before W"
    );
    let visible_segments = harness.loaded_segments_matching(|segment| !is_hidden(segment));
    for start_event_id in [
        0,
        2,
        window_start_last_event_id - 1,
        window_start_last_event_id,
    ] {
        let read = get_history_events_from_segments(
            &harness.state,
            harness.context.flow_hash,
            harness.run(),
            start_event_id,
            1000,
            &[],
            0,
            usize::MAX,
            &visible_segments,
            false,
        )
        .expect("history reads");
        let SegmentedHistoryPage::Page(visible_page) = read else {
            panic!("a read from event {start_event_id} needs a segment before W: {read:?}");
        };
        let first_after_flow_start = visible_page
            .events
            .iter()
            .map(|event| event.event_id)
            .find(|event_id| *event_id > 1);
        assert_eq!(
            first_after_flow_start,
            Some(start_event_id.max(window_start_last_event_id)),
            "a read from event {start_event_id}"
        );
    }

    let mut references = Vec::new();
    for event in &page.events[1..] {
        let Some(flow_history_event::Payload::RpcExecutionCompleted(completed)) = &event.payload
        else {
            continue;
        };
        for write in &completed.upsert_attributes {
            let Some(value::Kind::InternalBlobIdForStringValue(reference)) =
                write.value.as_ref().and_then(|value| value.kind.as_ref())
            else {
                panic!("a large Attribute is a reference in history");
            };
            references.push((event.event_id, reference.clone()));
        }
    }
    assert!(references.len() > 50, "visible events reference values");
    let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
    let entries: Vec<LoadBlobRequestEntry> = references
        .iter()
        .map(|(_, reference)| LoadBlobRequestEntry {
            flow_id: harness.flow_id.clone(),
            blob_value: Some(Value {
                kind: Some(value::Kind::InternalBlobIdForStringValue(reference.clone())),
            }),
        })
        .collect();
    let local =
        load_blobs(&harness.state, &hasher, TenantId::DEFAULT, &entries).expect("LoadBlobs reads");
    let mut uploaded_count = 0;
    for (event_id, reference) in &references {
        if local.values.contains_key(reference) {
            continue;
        }
        let value_id = reference
            .parse::<ValueReference>()
            .expect("a valid reference")
            .value_id;
        assert!(
            matches!(
                harness.locate(value_id),
                OffloadedValueLocation::Uploaded(_)
            ),
            "the value of visible event {event_id} is local or uploaded"
        );
        harness.uploaded_value(value_id);
        uploaded_count += 1;
    }
    assert!(uploaded_count > 0, "some visible values come from objects");

    let earliest_time = page.events[1].event_time.expect("events have a time");
    let earliest_micros = earliest_time.seconds * SECOND + i64::from(earliest_time.nanos) / 1000;
    let request = ResetFlowRequest {
        flow_id: harness.flow_id.clone(),
        reset_type: FlowResetType::HistoryEventTime as i32,
        reason: "earliest visible".to_owned(),
        skip_writes_reapply: true,
        history_event_time: (i128::from(earliest_micros) * 1000).to_string(),
        ..ResetFlowRequest::default()
    };
    assert!(
        !harness
            .staged_result(&harness.state.clone(), &request)
            .is_empty(),
        "travel to the earliest visible event replays from the uploaded segments"
    );
}

#[test]
fn while_offloading_steps_created_in_the_window_are_time_travelable() {
    // Checkpoints every 37 commands leave records between the kept checkpoint and W.
    let mut harness = Harness::new();
    harness.set_offload_mode(true);
    harness.start_with_step(RunSettings {
        history_window_events: 60,
        history_prune_chunk_events: 6,
        checkpoint_command_threshold: 37,
        ..settings(4096)
    });
    // Each Step passes its successor an input above the inline threshold, so history references it.
    for index in 1..=150_u8 {
        let invocation = StepInvocationKey {
            step_execution_id: format!("{START_STEP}-{index}"),
            method: StepMethod::Execute as i32,
        };
        let started = harness.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(invocation.clone()),
            attempt: 1,
            task_id: vec![index; 16],
            pull_routing: None,
        }));
        assert!(is_ok(&started.reply), "{:?}", started.reply);
        let completed = harness.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(StepDecision {
                        next_steps: vec![StepMovement {
                            step_type: START_STEP.to_owned(),
                            step_input: Some(Value {
                                kind: Some(value::Kind::StringValue(large_text(
                                    &format!("input {index}"),
                                    300,
                                ))),
                            }),
                            step_options: Some(StepOptions {
                                skip_wait_for: true,
                                ..StepOptions::default()
                            }),
                            ..StepMovement::default()
                        }],
                        ..StepDecision::default()
                    }),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            task_id: vec![index; 16],
            ..StepMethodCompletedCommand::default()
        }));
        assert!(is_ok(&completed.reply), "{:?}", completed.reply);
        if index % 25 == 0 {
            harness.ack_all();
            harness.advance(GRACE + SECOND);
        }
    }
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.bookkeep();
    let record = harness.run_record();
    assert!(
        record.history_window_pruned_through_seq + 1 < record.window_start_seq,
        "records before W rebuild it: {record:?}"
    );
    assert!(
        harness.stored_record_seqs().len() < 100,
        "offload deleted uploaded records locally"
    );

    let SegmentedHistoryPage::Page(page) = get_history_events_from_segments(
        &harness.state,
        harness.context.flow_hash,
        harness.run(),
        0,
        1000,
        &[],
        0,
        usize::MAX,
        &harness.loaded_segments(),
        false,
    )
    .expect("history reads") else {
        panic!("every uploaded segment is loaded");
    };
    let window_start_last_event_id = harness.metadata()
        [&(record.window_start_seq, RunTable::HistoryRecord as u8, 0)]
        .last_assigned_event_id;
    // Every Step completion records one event, so W's record holds only its last event.
    assert_eq!(page.events[1].event_id, window_start_last_event_id);
    let Some(flow_history_event::Payload::StepExecuteCompleted(completed_at_window_start)) =
        &page.events[1].payload
    else {
        panic!("W completes a Step: {:?}", page.events[1]);
    };
    let step_completed_at_window_start = completed_at_window_start
        .context
        .as_ref()
        .expect("the event names its Step")
        .step_execution_id
        .clone();

    let step_reset = |step_execution_id: String| ResetFlowRequest {
        flow_id: harness.flow_id.clone(),
        reset_type: FlowResetType::StepExecutionId as i32,
        reason: "earliest visible Step".to_owned(),
        skip_writes_reapply: true,
        step_execution_id,
        step_method: FlowResetStepMethod::Execute as i32,
        ..ResetFlowRequest::default()
    };
    // The Step that W completes was pending only before W, so no window point restores it.
    let TimeTravelComputation::Rejected(rejected) =
        harness.time_travel_computation(&step_reset(step_completed_at_window_start))
    else {
        panic!("the Step completed at W has no pending point in the window");
    };
    let Some(command_reply::Reply::Error(error)) = rejected.reply else {
        panic!("the rejection is an error: {rejected:?}");
    };
    assert_eq!(error.grpc_code, 9, "FAILED_PRECONDITION");

    let earliest_created_step = (record.window_start_seq..record.next_run_seq)
        .filter_map(|seq| {
            harness
                .metadata()
                .get(&(seq, RunTable::HistoryRecord as u8, 0))
                .cloned()
        })
        .find_map(|metadata| {
            metadata
                .pending_step_methods
                .into_iter()
                .find(|pending| pending.is_step_created)
        })
        .expect("a visible record creates a Step")
        .step_execution_id;
    assert!(
        !harness
            .staged_result(&harness.state.clone(), &step_reset(earliest_created_step))
            .is_empty(),
        "travel to the earliest Step created in the window replays from the uploaded segments"
    );
}

#[test]
fn an_idle_run_offloads_what_its_last_command_superseded_once_its_cleanup_is_due() {
    // The settings predate the grace period field, so the run keeps 10 minutes.
    let mut harness = Harness::offloading(settings(1));
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("second", 300));
    let superseded_at = harness.garbage()[&first].1.superseded_at_micros;
    assert_eq!(harness.cleanup_due(), None, "nothing is uploaded yet");

    let acknowledgments = harness.ack_all();
    assert_eq!(harness.cleanup_due(), Some(superseded_at + GRACE));
    assert!(harness.has_cleanup_index_row());
    let scheduled: Vec<_> = acknowledgments
        .iter()
        .flat_map(|outcome| &outcome.effects)
        .filter(|effect| matches!(effect, ApplyEffect::HistoryLocalCleanupScheduled { .. }))
        .collect();
    assert_eq!(
        scheduled.len(),
        1,
        "the row's creation asks the owner to schedule it"
    );
    assert!(harness.value_row(first).is_some());

    harness.advance_to(superseded_at + GRACE - SECOND);
    let early = harness.clean_up();
    assert!(is_ok(&early.reply), "{:?}", early.reply);
    assert!(
        early.writes.is_empty(),
        "a cleanup before its due time writes nothing"
    );

    let record_before = harness.run_record();
    harness.advance_to(superseded_at + GRACE);
    let cleanup = harness.clean_up();
    assert!(is_ok(&cleanup.reply), "{:?}", cleanup.reply);
    assert_eq!(harness.value_row(first), None, "the value row is offloaded");
    assert!(harness.birth(first).expect("birth").is_offloaded);
    let record = harness.run_record();
    assert_eq!(record.history_local_cleanup_due_micros, None);
    assert!(!harness.has_cleanup_index_row());
    assert_eq!(
        record.next_run_seq, record_before.next_run_seq,
        "no run seq"
    );
    assert_eq!(
        record.next_event_id, record_before.next_event_id,
        "no event"
    );
    assert!(
        cleanup.writes.iter().all(|write| !matches!(
            write,
            StateWrite::Put {
                column_family: ColumnFamily::History,
                ..
            }
        )),
        "a cleanup records no history"
    );
    harness.assert_storage_recounts();

    let repeated = harness.clean_up();
    assert!(is_ok(&repeated.reply) && repeated.writes.is_empty());
}

#[test]
fn an_idle_run_resumes_its_cleanup_once_offload_turns_on_again() {
    let mut harness = Harness::offloading(settings(1));
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("second", 300));
    let superseded_at = harness.garbage()[&first].1.superseded_at_micros;
    harness.ack_all();
    harness.set_offload_mode(false);

    harness.advance_to(superseded_at + GRACE);
    let paused = harness.clean_up();
    assert!(is_ok(&paused.reply), "{:?}", paused.reply);
    assert!(harness.value_row(first).is_some(), "offload is off");
    assert_eq!(
        harness.cleanup_due(),
        Some(harness.now_micros + OFFLOAD_RECHECK),
        "the run keeps its cleanup pending while offload is off"
    );
    assert!(harness.has_cleanup_index_row());

    harness.set_offload_mode(true);
    let cleanup_count = harness.clean_up_until_done();
    assert_eq!(cleanup_count, 1, "the next recheck resumes the cleanup");
    assert_eq!(harness.value_row(first), None, "the value row is offloaded");
    assert!(harness.birth(first).expect("birth").is_offloaded);
    assert!(!harness.has_cleanup_index_row());
    harness.assert_storage_recounts();
}

#[test]
fn window_pruning_while_offload_is_off_ends_the_wait_for_uploaded_history() {
    let mut harness = Harness::offloading(RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..settings(1)
    });
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("second", 300));
    harness.ack_all();
    let uploaded_through_seq = harness.run_record().history_uploaded_through_seq;
    harness.set_offload_mode(false);
    assert!(
        harness.has_cleanup_index_row(),
        "the value waits for its grace period"
    );

    for round in 0..40 {
        let record = harness.run_record();
        if record.window_start_seq > uploaded_through_seq
            && record.history_window_pruned_through_seq >= uploaded_through_seq
        {
            break;
        }
        harness.set_attribute("note", format!("note {round}"));
    }
    let record = harness.run_record();
    assert!(record.window_start_seq > uploaded_through_seq);
    assert!(
        record.history_values_offloaded_through_seq < uploaded_through_seq,
        "offload never deleted the value"
    );
    assert_eq!(harness.value_row(first), None, "window pruning deleted it");
    assert_eq!(record.history_local_cleanup_due_micros, None);
    assert!(!harness.has_cleanup_index_row());

    harness.advance(2 * OFFLOAD_RECHECK);
    let late = harness.clean_up();
    assert!(is_ok(&late.reply), "{:?}", late.reply);
    assert!(late.writes.is_empty(), "no cleanup is due any more");
    harness.assert_storage_recounts();
}

#[test]
fn a_closed_run_offloads_every_superseded_value_through_cleanups_a_quiet_period_apart() {
    let mut harness = Harness::offloading(settings(1));
    for round in 0..4 {
        harness.set_attribute("profile", large_text(&format!("v{round}"), 300));
    }
    harness.stop();
    harness.ack_all();
    let garbage = harness.garbage();
    assert_eq!(garbage.len(), 3);
    let first_superseded_at = garbage
        .values()
        .map(|(_, row)| row.superseded_at_micros)
        .min()
        .expect("superseded values");
    assert_eq!(harness.cleanup_due(), Some(first_superseded_at + GRACE));

    let cleanup_count = harness.clean_up_until_done();
    assert!(
        cleanup_count <= 2,
        "values superseded close together leave in at most two cleanups, not {cleanup_count}"
    );
    assert!(
        harness.garbage().is_empty(),
        "every superseded value is offloaded"
    );
    assert!(!harness.has_cleanup_index_row());
    harness.assert_storage_recounts();

    // A hot standby that applies the same log reaches the same state.
    let mut standby = MemoryState::default();
    for command in &harness.log {
        let outcome =
            apply_flow_command(&harness.context, &standby, command).expect("replay applies");
        standby.commit(&outcome);
    }
    assert_eq!(standby.rows, harness.state.rows);
}

#[test]
fn a_closed_run_finishes_the_window_pruning_its_upload_allowed_through_cleanups() {
    // The run's only segment seals when it closes, so pruning waits for that upload.
    let mut harness = Harness::offloading(RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..settings(64 << 20)
    });
    for round in 0..300 {
        harness.set_attribute("note", format!("note {round}"));
    }
    harness.stop();
    assert_eq!(harness.run_record().history_uploaded_through_seq, 0);
    let metadata_before = harness.metadata().len();
    assert!(
        metadata_before > 400,
        "the window kept {metadata_before} rows"
    );

    harness.ack_all();
    let keep_from_seq = harness.window_keep_from_seq();
    let passed_rows = |harness: &Harness| {
        harness
            .metadata()
            .keys()
            .filter(|(seq, _, _)| *seq > 1 && *seq < keep_from_seq)
            .count()
    };
    assert!(
        passed_rows(&harness) > 0,
        "one acknowledgment prunes a bounded amount"
    );
    assert_eq!(harness.cleanup_due(), Some(harness.now_micros + QUIET));

    let cleanup_count = harness.clean_up_until_done();
    assert!(
        cleanup_count >= 2,
        "a cleanup continues unfinished pruning at once"
    );
    assert_eq!(
        passed_rows(&harness),
        0,
        "pruning reached the kept checkpoint"
    );
    assert!(
        harness.run_record().history_window_pruned_through_seq >= keep_from_seq - 1,
        "readers see the pruned range through the kept checkpoint"
    );
    assert!(!harness.has_cleanup_index_row());
    harness.assert_storage_recounts();
}

#[test]
fn window_pruning_that_a_run_left_unfinished_completes_through_cleanups_without_offload() {
    let mut harness = Harness::not_offloading(RunSettings {
        history_window_events: 10,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 300,
        ..settings(4 << 20)
    });
    let mut round = 0;
    while harness.cleanup_due().is_none() {
        round += 1;
        assert!(round < 1000, "the window never left pruning unfinished");
        harness.set_attribute("note", format!("note {round}"));
    }
    assert_eq!(
        harness.cleanup_due(),
        Some(harness.now_micros + QUIET),
        "unfinished pruning comes due once the run is quiet"
    );
    assert!(harness.has_cleanup_index_row());
    let keep_from_seq = harness.window_keep_from_seq();
    assert!(
        harness
            .stored_record_seqs()
            .iter()
            .any(|seq| *seq > 1 && *seq < keep_from_seq),
        "records before the kept checkpoint remain"
    );

    let cleanup_count = harness.clean_up_until_done();
    assert!(
        cleanup_count >= 2,
        "a cleanup continues unfinished pruning at once"
    );
    assert!(
        harness
            .stored_record_seqs()
            .iter()
            .all(|seq| *seq == 1 || *seq >= keep_from_seq),
        "pruning reached the kept checkpoint"
    );
    assert!(!harness.has_cleanup_index_row());
    harness.assert_storage_recounts();
}

#[test]
fn a_busy_run_keeps_its_cleanup_one_quiet_period_after_each_command() {
    let mut harness = Harness::offloading(RunSettings {
        superseded_value_grace_micros: 2 * 60 * SECOND,
        ..settings(1)
    });
    harness.step_micros = 10 * SECOND;
    harness.set_attribute("profile", large_text("v0", 300));
    let first = harness.attribute_value_id("profile");
    for round in 1..40 {
        harness.set_attribute("profile", large_text(&format!("v{round}"), 300));
        harness.ack_all();
        if let Some(due_micros) = harness.cleanup_due() {
            assert!(
                due_micros >= harness.now_micros + QUIET,
                "round {round}: cleanup is due within a quiet period of the last command"
            );
        }
    }
    assert!(
        harness.birth(first).expect("birth").is_offloaded,
        "the run's own commands offloaded values whose grace period passed"
    );
}

#[test]
fn retention_removes_the_cleanup_row_and_a_cleanup_of_a_purging_run_is_not_found() {
    let mut harness = Harness::offloading(RunSettings {
        retention_after_close_micros: 60 * SECOND,
        ..settings(1)
    });
    harness.set_attribute("profile", large_text("first", 300));
    harness.set_attribute("profile", large_text("second", 300));
    harness.stop();
    harness.ack_all();
    assert!(harness.has_cleanup_index_row());

    harness.advance(60 * SECOND);
    let fired = harness.fire_retention();
    assert!(is_ok(&fired.reply), "{:?}", fired.reply);
    let record = harness.run_record();
    assert!(record.is_purging);
    assert_eq!(record.history_local_cleanup_due_micros, None);
    assert!(!harness.has_cleanup_index_row());

    let cleanup = harness.clean_up();
    assert_eq!(error_message(&cleanup.reply).map(|(code, _)| code), Some(5));
    assert!(cleanup.writes.is_empty());
    let purge = harness.apply(Command::RunPurge(RunPurgeCommand {}));
    assert!(is_ok(&purge.reply), "{:?}", purge.reply);
    assert!(!harness.has_cleanup_index_row());
}

#[test]
fn each_run_offloads_after_the_grace_period_it_recorded_at_start() {
    let mut harness = Harness::offloading(RunSettings {
        superseded_value_grace_micros: 30 * SECOND,
        ..settings(1)
    });
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("second", 300));
    let superseded_at = harness.garbage()[&first].1.superseded_at_micros;
    harness.ack_all();
    assert_eq!(
        harness.cleanup_due(),
        Some(harness.now_micros + QUIET),
        "a grace period shorter than the quiet period still waits for the quiet period"
    );

    harness.advance_to(superseded_at + 30 * SECOND - SECOND);
    harness.bookkeep();
    assert!(
        harness.value_row(first).is_some(),
        "within the recorded period"
    );
    harness.advance_to(superseded_at + 30 * SECOND);
    harness.bookkeep();
    assert_eq!(
        harness.value_row(first),
        None,
        "offloaded after the recorded 30 seconds"
    );
    harness.assert_storage_recounts();
}

// Turning offload on stops pruning below the prune cursors, which must not move back.
#[test]
fn prune_cursors_wait_at_the_uploaded_seq_and_never_move_back() {
    let mut harness = Harness::not_offloading(RunSettings {
        history_window_events: 50,
        history_prune_chunk_events: 5,
        checkpoint_command_threshold: 7,
        ..settings(4 << 10)
    });
    for round in 0..200 {
        harness.set_attribute("profile", large_text(&format!("off-{round}"), 300));
    }
    let before_offload = harness.run_record();
    assert!(before_offload.history_metadata_pruned_before_seq > 100);
    assert!(before_offload.value_garbage_pruned_before_seq > 100);

    harness.set_offload_mode(true);
    for round in 0..200 {
        harness.set_attribute("profile", large_text(&format!("on-{round}"), 300));
    }
    let unacknowledged = harness.run_record();
    assert_eq!(unacknowledged.history_uploaded_through_seq, 0);
    assert!(unacknowledged.window_start_seq > before_offload.window_start_seq + 150);
    assert_eq!(
        (
            unacknowledged.history_metadata_pruned_before_seq,
            unacknowledged.value_garbage_pruned_before_seq
        ),
        (
            before_offload.history_metadata_pruned_before_seq,
            before_offload.value_garbage_pruned_before_seq
        ),
        "nothing uploaded, so nothing more is pruned"
    );

    let mut previous = unacknowledged.clone();
    for round in 0..300 {
        if round % 25 == 0 {
            harness.ack_all();
        }
        harness.set_attribute("profile", large_text(&format!("acked-{round}"), 300));
        let record = harness.run_record();
        assert!(
            record.history_metadata_pruned_before_seq
                >= previous.history_metadata_pruned_before_seq
        );
        assert!(record.value_garbage_pruned_before_seq >= previous.value_garbage_pruned_before_seq);
        let first_unuploaded_seq = record
            .history_uploaded_through_seq
            .max(before_offload.history_metadata_pruned_before_seq)
            + 1;
        assert!(record.history_metadata_pruned_before_seq <= first_unuploaded_seq);
        assert!(record.value_garbage_pruned_before_seq <= first_unuploaded_seq);
        previous = record;
    }
    assert!(
        previous.history_metadata_pruned_before_seq > unacknowledged.window_start_seq,
        "pruning resumed once uploads passed the window start"
    );
    assert!(previous.value_garbage_pruned_before_seq > unacknowledged.window_start_seq);
    harness.assert_storage_recounts();
}
