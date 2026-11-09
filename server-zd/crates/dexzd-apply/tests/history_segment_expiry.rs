// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Expired history segments (plan section 5.7): segments the window passed expire, their value
//! lists and the birth records only their objects held go, the delete switch keeps the delete
//! pending index, and recorded object deletions remove catalog rows oldest first. A run that
//! records its offload choice at start follows it whatever its partition's mode, ignores the
//! delete switch, and counts the rows of superseded values as history.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::read_model::{
    OffloadedValueLocation, SegmentedHistoryPage, get_history_events_from_segments,
    locate_offloaded_value, read_history_segment_content,
};
use dexzd_apply::values::OffloadedValues;
use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyError, ApplyOutcome, FlowStateReader, LoadedHistorySegments,
    SegmentCheckpoint, SegmentedTimeTravelComputation, StateReadError, StateRow, StateWrite,
    TimeTravelComputation, TimeTravelComputationRequest, apply_flow_command,
    apply_staged_time_travel_for_test, compute_time_travel_from_segments,
    stage_computed_time_travel_for_test,
};
use dexzd_history_segment::{HistorySegmentContent, encode_history_segment};
use dexzd_keyspace::row_keys::{
    history_object_delete_pending_index_key, prefix_range, run_named_key, run_singleton_key,
    value_row_key,
};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, ValueId};
use dexzd_keyspace::{parse_run_key, run_prefix, run_table_prefix};
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowResetType, FlowTimeoutPolicy, ResetFlowRequest,
    SetAttributesRequest, StartFlowRequest, StopFlowRequest, StopType, UpdateFlowConfigRequest,
    Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, AttributesSetCommand, CheckpointRecord, CommandReply, FlowCommand,
    FlowConfigUpdateCommand, FlowStartCommand, FlowStopCommand, HistoryRecord, HistoryRowMetadata,
    HistorySegmentObjectsDeletedCommand, HistorySegmentRecord, HistorySegmentState,
    HistorySegmentUploadedCommand, HistorySegmentValueRecord, RetentionTimerId, RunPurgeCommand,
    RunRecord, RunSettings, RunStorageUsage, SetHistoryOffloadModeCommand, TimeTravelCommand,
    TimerId, TimersFiredCommand, ValueBirthRecord, ValueGarbageRecord, ValueRecord, command_reply,
    flow_command::Command, stored_value, timer_id,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
/// The grace period of superseded values, by command time.
const GRACE: i64 = 10 * 60 * SECOND;

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

/// Values of uploaded segment objects, as an owner reads them for a time travel.
struct UploadedValues<'harness>(
    &'harness BTreeMap<u64, (HistorySegmentRecord, HistorySegmentContent)>,
);

impl OffloadedValues for UploadedValues<'_> {
    fn offloaded_value(
        &self,
        _run_id: RunId,
        value_id: ValueId,
    ) -> Result<Option<Value>, ApplyError> {
        Ok(self
            .0
            .values()
            .flat_map(|(_, content)| &content.values)
            .find(|row| row.value_id == value_id)
            .map(|row| {
                ValueRecord::decode(row.row.as_slice())
                    .expect("an uploaded value row decodes")
                    .value
                    .expect("an uploaded value row holds a value")
            }))
    }
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    flow_id: String,
    run_id: Vec<u8>,
    request_count: u32,
    /// Content of each acknowledged segment by first seq, as its uploaded object holds it.
    uploaded: BTreeMap<u64, (HistorySegmentRecord, HistorySegmentContent)>,
    /// Commands whose outcome emitted HistorySegmentsExpired.
    expiry_signals: usize,
}

impl Harness {
    /// A run whose partition offloads history from its start, with `flow_config` as its start
    /// override.
    fn offloading(settings: RunSettings, flow_config: FlowConfig) -> Self {
        Self::on_partition(settings, flow_config, true)
    }

    /// A run started with `settings` and the `flow_config` start override, on a partition that
    /// offloads history when `is_partition_offloading`.
    fn on_partition(
        settings: RunSettings,
        flow_config: FlowConfig,
        is_partition_offloading: bool,
    ) -> Self {
        let flow_id = "segment-expiry-flow";
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let mut harness = Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000 * SECOND,
            flow_id: flow_id.to_owned(),
            run_id: Vec::new(),
            request_count: 0,
            uploaded: BTreeMap::new(),
            expiry_signals: 0,
        };
        let mode = harness.apply(Command::SetHistoryOffloadMode(
            SetHistoryOffloadModeCommand {
                enabled: is_partition_offloading,
                partition: u32::from(harness.context.partition.0),
            },
        ));
        assert!(is_ok(&mode.reply), "{:?}", mode.reply);
        let outcome = harness.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(StartFlowRequest {
                flow_id: flow_id.to_owned(),
                flow_type: "SegmentExpiryFlow".to_owned(),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            }),
            settings: Some(settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            effective_flow_config: Some(flow_config),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
            panic!("the run starts: {:?}", outcome.reply);
        };
        harness.run_id = reply.run_id.clone();
        harness
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        self.now_micros += 1000;
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
        self.apply_command(&command)
    }

    fn apply_command(&mut self, command: &FlowCommand) -> ApplyOutcome {
        let outcome =
            apply_flow_command(&self.context, &self.state, command).expect("apply succeeds");
        self.state.commit(&outcome);
        if outcome
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::HistorySegmentsExpired { .. }))
        {
            self.expiry_signals += 1;
        }
        outcome
    }

    fn advance(&mut self, micros: i64) {
        self.now_micros += micros;
    }

    fn set_attributes(&mut self, writes: Vec<(String, String)>) -> ApplyOutcome {
        let outcome = self.try_set_attributes(writes);
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        outcome
    }

    fn try_set_attributes(&mut self, writes: Vec<(String, String)>) -> ApplyOutcome {
        self.request_count += 1;
        let request_id = format!("write-{}", self.request_count);
        self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: writes
                    .into_iter()
                    .map(|(key, text)| AttributeWrite {
                        key,
                        value: Some(Value {
                            kind: Some(value::Kind::StringValue(text)),
                        }),
                        ..AttributeWrite::default()
                    })
                    .collect(),
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }))
    }

    fn set_attribute(&mut self, key: &str, text: String) -> ApplyOutcome {
        self.set_attributes(vec![(key.to_owned(), text)])
    }

    fn update_flow_config(&mut self, flow_config: FlowConfig) -> ApplyOutcome {
        let outcome = self.apply(Command::FlowConfigUpdate(FlowConfigUpdateCommand {
            request: Some(UpdateFlowConfigRequest {
                flow_id: self.flow_id.clone(),
                flow_config: Some(flow_config),
                ..UpdateFlowConfigRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        outcome
    }

    fn set_delete_switch(&mut self, is_enabled: bool) -> ApplyOutcome {
        self.update_flow_config(FlowConfig {
            delete_expired_history_objects: Some(is_enabled),
            ..FlowConfig::default()
        })
    }

    /// Acknowledges every SEALED segment, oldest first, keeping the content of its object.
    fn ack_all(&mut self) {
        loop {
            let uploaded_through_seq = self.run_record().history_uploaded_through_seq;
            let Some(segment) = self
                .segments()
                .into_iter()
                .find(|segment| segment.last_seq > uploaded_through_seq)
            else {
                return;
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
        }
    }

    /// Applies small Attribute writes, acknowledging uploads after each, until `is_done`.
    fn write_until(&mut self, what: &str, is_done: impl Fn(&Self) -> bool) {
        for round in 0..200 {
            if is_done(self) {
                return;
            }
            self.set_attribute("other", format!("small-{round}"));
            self.ack_all();
        }
        panic!("{what} never happened");
    }

    fn record_deleted_objects(&mut self, first_seqs: Vec<u64>) -> ApplyOutcome {
        self.apply(Command::HistorySegmentObjectsDeleted(
            HistorySegmentObjectsDeletedCommand { first_seqs },
        ))
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

    fn segments(&self) -> Vec<HistorySegmentRecord> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), RunTable::HistorySegment);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|(_, value)| HistorySegmentRecord::decode(value.as_slice()).expect("decodes"))
            .collect()
    }

    fn segment_covering(&self, seq: u64) -> HistorySegmentRecord {
        self.segments()
            .into_iter()
            .find(|segment| (segment.first_seq..=segment.last_seq).contains(&seq))
            .unwrap_or_else(|| panic!("no catalog row covers seq {seq}"))
    }

    fn expired_segments(&self) -> Vec<HistorySegmentRecord> {
        self.segments()
            .into_iter()
            .filter(|segment| segment.state == HistorySegmentState::Expired as i32)
            .collect()
    }

    /// Value IDs that the segment starting at `first_seq` lists.
    fn segment_values(&self, first_seq: u64) -> BTreeSet<ValueId> {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.run(),
            RunTable::HistorySegmentValue,
        );
        let mut start = prefix.clone();
        start.extend_from_slice(&first_seq.to_be_bytes());
        let mut end = prefix.clone();
        end.extend_from_slice(&(first_seq + 1).to_be_bytes());
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|((_, key), value)| {
                HistorySegmentValueRecord::decode(value.as_slice()).expect("decodes");
                ValueId::from_key_suffix(&key[prefix.len() + 8..]).expect("value ID")
            })
            .collect()
    }

    fn is_delete_pending(&self) -> bool {
        self.state.rows.contains_key(&(
            ColumnFamily::PartitionIndex,
            history_object_delete_pending_index_key(
                self.context.partition,
                self.context.flow_hash,
                self.run(),
            ),
        ))
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

    fn value_row(&self, value_id: ValueId) -> Option<ValueRecord> {
        self.get(
            ColumnFamily::Value,
            &value_row_key(
                self.context.flow_hash,
                self.run(),
                RunTable::Value,
                value_id,
            ),
        )
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

    fn locate(&self, value_id: ValueId) -> OffloadedValueLocation {
        locate_offloaded_value(&self.state, self.context.flow_hash, self.run(), value_id)
            .expect("the location reads")
    }

    /// Recounts the run's storage from its rows, as the accounting rules state them.
    fn recount_storage(&self) -> RunStorageUsage {
        let prefix = run_prefix(self.context.flow_hash, self.run());
        let counts_superseded_rows_as_history = self
            .run_record()
            .settings
            .unwrap_or_default()
            .offload_history
            .is_some();
        let mut usage = RunStorageUsage::default();
        let mut birth_bytes = 0;
        for ((column_family, key), value) in &self.state.rows {
            if !key.starts_with(&prefix) || *column_family != ColumnFamily::Flow {
                continue;
            }
            let row_bytes = (key.len() + value.len()) as u64;
            match parse_run_key(key).expect("valid run key").table {
                RunTable::HistoryRowMetadata => {
                    let metadata =
                        HistoryRowMetadata::decode(value.as_slice()).expect("metadata decodes");
                    usage.history_bytes += row_bytes + metadata.row_size_bytes;
                }
                RunTable::HistorySegment | RunTable::HistorySegmentValue => {
                    usage.history_bytes += row_bytes;
                }
                RunTable::ValueBirth => {
                    let birth = ValueBirthRecord::decode(value.as_slice()).expect("birth decodes");
                    if birth.is_offloaded {
                        usage.history_bytes += row_bytes;
                    } else if counts_superseded_rows_as_history && birth.garbage_seq != 0 {
                        usage.history_bytes += row_bytes;
                        birth_bytes += birth.size_bytes;
                    } else {
                        usage.state_row_bytes += row_bytes;
                        birth_bytes += birth.size_bytes;
                    }
                }
                RunTable::ValueGarbage => {
                    if counts_superseded_rows_as_history {
                        usage.history_bytes += row_bytes;
                    } else {
                        usage.state_row_bytes += row_bytes;
                    }
                    usage.superseded_value_bytes += ValueGarbageRecord::decode(value.as_slice())
                        .expect("garbage decodes")
                        .size_bytes;
                }
                RunTable::RunRecord => {
                    usage.state_row_bytes += row_bytes - run_record_search_cache_bytes(value);
                }
                _ => usage.state_row_bytes += row_bytes,
            }
        }
        usage.live_value_bytes = birth_bytes - usage.superseded_value_bytes;
        usage
    }

    /// Asserts the recorded totals equal the recount, apart from attempt state.
    fn assert_storage_recounts(&self) {
        let recorded = self.run_record().storage.expect("the run counts storage");
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

    /// Returns the records and checkpoints of every acknowledged segment, expired ones included,
    /// as an owner could still fetch them while their objects exist.
    fn loaded_segments(&self) -> LoadedHistorySegments {
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
            let checkpoints: Vec<SegmentCheckpoint> = content
                .checkpoints
                .iter()
                .map(|row| SegmentCheckpoint {
                    attempt: row.attempt,
                    seq: row.seq,
                    record: CheckpointRecord::decode(row.row.as_slice())
                        .expect("checkpoint decodes"),
                })
                .collect();
            loaded
                .insert_checkpoints(segment, checkpoints.into())
                .expect("uploaded checkpoints are in order");
        }
        loaded
    }

    /// Reads the run's history from the start with `loaded` segments.
    fn read_history(&self, loaded: &LoadedHistorySegments) -> SegmentedHistoryPage {
        get_history_events_from_segments(
            &self.state,
            self.context.flow_hash,
            self.run(),
            0,
            1000,
            &[],
            0,
            usize::MAX,
            loaded,
            false,
        )
        .expect("history reads")
    }

    /// Event IDs of the first page, with every acknowledged segment loaded.
    fn history_event_ids(&self) -> Vec<i64> {
        match self.read_history(&self.loaded_segments()) {
            SegmentedHistoryPage::Page(page) => {
                page.events.iter().map(|event| event.event_id).collect()
            }
            SegmentedHistoryPage::NeedsSegments(needed) => {
                panic!("every acknowledged segment is loaded, yet the read needs {needed:?}")
            }
        }
    }

    /// Time travels to the end of the command at `seq` from uploaded segments and objects, as an
    /// owner computes and stages it, and applies the staged result.
    fn travel_to_seq(&mut self, seq: u64) {
        let metadata_key = dexzd_keyspace::row_keys::history_row_metadata_key(
            self.context.flow_hash,
            self.run(),
            seq,
            RunTable::HistoryRecord,
            0,
        );
        let metadata: HistoryRowMetadata = self
            .get(ColumnFamily::Flow, &metadata_key)
            .expect("the target keeps its metadata");
        self.now_micros += 1000;
        let request = ResetFlowRequest {
            flow_id: self.flow_id.clone(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            reason: "restore".to_owned(),
            skip_writes_reapply: true,
            history_event_time: (i128::from(metadata.timestamp_micros) * 1000).to_string(),
            ..ResetFlowRequest::default()
        };
        let command = FlowCommand {
            flow_id: self.flow_id.clone(),
            run_id: self.run_id.clone(),
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            command: Some(Command::TimeTravel(TimeTravelCommand {
                request: Some(request.clone()),
                ..TimeTravelCommand::default()
            })),
            ..FlowCommand::default()
        };
        let computation = compute_time_travel_from_segments(
            &self.context,
            &self.state,
            &self.loaded_segments(),
            &UploadedValues(&self.uploaded),
            &TimeTravelComputationRequest {
                run_id: self.run(),
                reset: &request,
                now_micros: self.now_micros,
                replay_memory_budget_bytes: u64::MAX,
            },
        )
        .expect("the computation runs");
        let SegmentedTimeTravelComputation::Computed(computation) = computation else {
            panic!("every uploaded segment is loaded: {computation:?}");
        };
        let staged =
            stage_computed_time_travel_for_test(&command, computation).expect("the result stages");
        let outcome =
            apply_staged_time_travel_for_test(staged, |command| self.apply_command(&command));
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "{:?}",
            outcome.reply
        );
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
        self.now_micros = self.run_record().retention_deadline_micros;
        self.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: 0,
                kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
            }],
        }))
    }
}

/// Every command seals its own segment, and a small window lets pruning pass old segments.
fn settings(is_deletion_enabled: bool) -> RunSettings {
    RunSettings {
        history_window_events: 6,
        history_prune_chunk_events: 1,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 60 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 30,
        checkpoint_command_threshold: 2,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: 1,
        delete_expired_history_objects: is_deletion_enabled,
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

/// A 32 KiB state budget, which the rows of a few hundred superseded values fill while they
/// count as state and wait for an upload.
fn small_budget_settings(offload_history: Option<bool>) -> RunSettings {
    RunSettings {
        history_window_events: 8,
        max_run_state_bytes: SMALL_RUN_STATE_BYTES,
        request_dedup_capacity: 16,
        history_segment_target_bytes: 64 << 10,
        offload_history,
        ..settings(true)
    }
}

const SMALL_RUN_STATE_BYTES: u64 = 32 << 10;
const RESOURCE_EXHAUSTED: i32 = 8;

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

fn active_bytes(usage: &RunStorageUsage) -> u64 {
    usage.state_row_bytes + usage.live_value_bytes
}

/// Rewrites `profile` with 200-byte values until the run rejects one, at most `limit` times;
/// returns how many it accepted and the rejection.
fn rewrite_until_rejected(harness: &mut Harness, limit: usize) -> (usize, Option<ApplyOutcome>) {
    for accepted in 0..limit {
        let text = large_text(&format!("rewrite-{accepted}"), 200);
        let outcome = harness.try_set_attributes(vec![("profile".to_owned(), text)]);
        if !is_ok(&outcome.reply) {
            return (accepted, Some(outcome));
        }
    }
    (limit, None)
}

fn large_text(tag: &str, length: usize) -> String {
    let mut text = format!("{tag}:");
    while text.len() < length {
        text.push('x');
    }
    text
}

/// Writes `profile` twice and lets its first value offload: returns that value and the seq that
/// superseded it.
fn offload_first_profile(harness: &mut Harness) -> (ValueId, u64) {
    harness.set_attribute("profile", large_text("first", 300));
    let first = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("second", 300));
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.set_attribute("other", "offload".to_owned());
    harness.ack_all();
    let birth = harness
        .birth(first)
        .expect("the first value keeps its birth");
    assert!(birth.is_offloaded, "{birth:?}");
    (first, birth.garbage_seq)
}

#[test]
fn a_live_value_outlives_expired_segments_and_expires_with_the_segment_that_lists_it() {
    let mut harness = Harness::offloading(settings(true), FlowConfig::default());
    harness.set_attribute("profile", large_text("profile", 300));
    let profile = harness.attribute_value_id("profile");
    let written_seq = profile.run_seq;

    // The window passes the segment that wrote the value; the live value stays local.
    harness.write_until("expiry of the segment that wrote the value", |harness| {
        harness.segment_covering(written_seq).state == HistorySegmentState::Expired as i32
    });
    harness.write_until("several expired segments", |harness| {
        harness.expired_segments().len() >= 4
    });
    let birth = harness
        .birth(profile)
        .expect("the live value keeps its birth");
    assert_eq!(birth.garbage_seq, 0, "{birth:?}");
    assert!(!birth.is_offloaded);
    assert!(harness.value_row(profile).is_some());
    assert!(
        harness
            .uploaded
            .values()
            .all(|(_, content)| content.values.iter().all(|row| row.value_id != profile)),
        "no object holds a live value"
    );

    // Superseding the value lists it in the segment of that command only.
    let superseding = harness.set_attribute("profile", large_text("replaced", 300));
    assert!(
        superseding
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::HistorySegmentSealed { .. }))
    );
    let superseded_seq = harness.run_record().next_run_seq - 1;
    let holding = harness.segment_covering(superseded_seq);
    assert_eq!(
        harness.segment_values(holding.first_seq),
        BTreeSet::from([profile])
    );
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.set_attribute("other", "offload".to_owned());
    harness.ack_all();
    assert!(
        harness
            .birth(profile)
            .is_some_and(|birth| birth.is_offloaded)
    );
    assert_eq!(
        harness.locate(profile),
        OffloadedValueLocation::Uploaded(harness.segment_covering(superseded_seq))
    );

    // The window passes that segment: it expires, its list and the birth record go.
    harness.write_until("expiry of the segment that lists the value", |harness| {
        harness.segment_covering(superseded_seq).state == HistorySegmentState::Expired as i32
    });
    let expired = harness.segment_covering(superseded_seq);
    let record = harness.run_record();
    assert!(expired.expired_at_micros > 0 && expired.expired_at_micros <= harness.now_micros);
    assert!(expired.last_seq <= record.history_expired_through_seq);
    assert!(record.history_expired_through_seq <= record.history_window_pruned_through_seq);
    assert!(harness.segment_values(expired.first_seq).is_empty());
    assert_eq!(harness.birth(profile), None);
    assert_eq!(harness.value_row(profile), None);
    assert_eq!(harness.locate(profile), OffloadedValueLocation::Absent);
    harness.assert_storage_recounts();
}

#[test]
fn with_deletion_off_expired_segments_keep_their_rows_and_reads_never_return_them() {
    let mut harness = Harness::offloading(settings(false), FlowConfig::default());
    let (first, superseded_seq) = offload_first_profile(&mut harness);
    harness.write_until("expiry of the segment that lists the value", |harness| {
        harness.segment_covering(superseded_seq).state == HistorySegmentState::Expired as i32
    });
    harness.write_until("several expired segments", |harness| {
        harness.expired_segments().len() >= 6
    });
    let record = harness.run_record();
    assert_eq!(record.history_objects_deleted_through_seq, 0);
    assert!(!harness.is_delete_pending());
    assert_eq!(
        harness.expiry_signals, 0,
        "no deletion is requested while the switch is off"
    );
    let expired = harness.expired_segments();
    let catalog = harness.segments();
    assert_eq!(catalog[0].first_seq, 1);
    assert_eq!(
        catalog[0].state,
        HistorySegmentState::Uploaded as i32,
        "the segment that holds genesis never expires"
    );
    assert_eq!(
        expired.first().map(|segment| segment.first_seq),
        catalog.get(1).map(|segment| segment.first_seq),
        "every catalog row stays, so the objects stay until the run is deleted"
    );

    // Reads with every object at hand, expired ones included, still show only the window.
    let event_ids = harness.history_event_ids();
    for segment in &expired {
        let expired_events = segment.first_event_id.max(2)..=segment.last_event_id;
        assert!(
            event_ids
                .iter()
                .all(|event_id| !expired_events.contains(event_id)),
            "a read returned an event of expired segment {segment:?}: {event_ids:?}"
        );
    }
    assert_eq!(
        event_ids.first(),
        Some(&1),
        "the first event stays readable"
    );

    // Reads without objects never ask for an expired segment.
    match harness.read_history(&LoadedHistorySegments::default()) {
        SegmentedHistoryPage::NeedsSegments(needed) => {
            assert!(!needed.is_empty());
            assert!(
                needed
                    .iter()
                    .all(|segment| segment.state == HistorySegmentState::Uploaded as i32),
                "{needed:?}"
            );
        }
        SegmentedHistoryPage::Page(page) => {
            panic!("uploaded history needs its objects: {page:?}")
        }
    }
    assert_eq!(harness.birth(first), None);
    assert_eq!(harness.locate(first), OffloadedValueLocation::Absent);
    harness.assert_storage_recounts();
}

#[test]
fn the_start_override_and_flow_config_updates_switch_deletion_per_run() {
    // The server default is off; the start override turns deletion on.
    let mut harness = Harness::offloading(
        settings(false),
        FlowConfig {
            delete_expired_history_objects: Some(true),
            ..FlowConfig::default()
        },
    );
    harness.write_until("an expired segment", |harness| {
        !harness.expired_segments().is_empty()
    });
    assert!(harness.is_delete_pending());
    assert!(harness.expiry_signals > 0);

    // Off: the index row goes and later expiries request nothing.
    let off = harness.set_delete_switch(false);
    assert!(!harness.is_delete_pending());
    assert!(
        !off.effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::HistorySegmentsExpired { .. }))
    );
    let signals = harness.expiry_signals;
    let expired_count = harness.expired_segments().len();
    harness.write_until("more expired segments", |harness| {
        harness.expired_segments().len() > expired_count + 2
    });
    assert!(!harness.is_delete_pending());
    assert_eq!(harness.expiry_signals, signals);

    // On again: the backlog of expired objects is requested at once.
    let on = harness.set_delete_switch(true);
    assert!(harness.is_delete_pending());
    assert!(
        on.effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::HistorySegmentsExpired { .. })),
        "turning deletion on deletes what already expired"
    );
    assert_eq!(
        harness
            .run_record()
            .flow_config
            .and_then(|config| config.delete_expired_history_objects),
        Some(true)
    );

    // A run that records the default on needs no override.
    let mut defaulted = Harness::offloading(settings(true), FlowConfig::default());
    defaulted.write_until("an expired segment", |harness| {
        !harness.expired_segments().is_empty()
    });
    assert!(defaulted.is_delete_pending());
    // An update that leaves the switch unset keeps it.
    defaulted.update_flow_config(FlowConfig {
        history_window_events: Some(50),
        ..FlowConfig::default()
    });
    assert!(defaulted.is_delete_pending());
}

#[test]
fn a_run_that_records_offload_offloads_and_deletes_expired_objects_on_any_partition() {
    let mut harness = Harness::on_partition(
        RunSettings {
            offload_history: Some(true),
            ..settings(false)
        },
        FlowConfig {
            delete_expired_history_objects: Some(false),
            ..FlowConfig::default()
        },
        false,
    );
    assert_ne!(harness.run_record().history_open_segment_first_seq, 0);
    harness.write_until("an expired segment", |harness| {
        !harness.expired_segments().is_empty()
    });
    assert!(
        harness.is_delete_pending(),
        "a run that records its offload choice ignores the delete switch"
    );
    assert!(harness.expiry_signals > 0);
    harness.set_delete_switch(false);
    assert!(harness.is_delete_pending());
    harness.assert_storage_recounts();
}

#[test]
fn a_run_that_records_no_offload_keeps_its_history_local_on_an_offloading_partition() {
    let mut harness = Harness::on_partition(
        RunSettings {
            offload_history: Some(false),
            history_window_events: 4,
            max_retained_superseded_bytes: 1,
            ..settings(true)
        },
        FlowConfig::default(),
        true,
    );
    assert_eq!(harness.run_record().history_open_segment_first_seq, 0);
    let first_profile = {
        harness.set_attribute("profile", large_text("first", 300));
        harness.attribute_value_id("profile")
    };
    for round in 0..40 {
        harness.set_attribute("profile", large_text(&format!("round-{round}"), 300));
        harness.set_attribute("other", format!("small-{round}"));
        harness.assert_storage_recounts();
    }
    let record = harness.run_record();
    assert_eq!(record.history_open_segment_first_seq, 0, "no segment state");
    assert_eq!(record.history_uploaded_through_seq, 0, "nothing uploads");
    assert!(harness.segments().is_empty());
    assert!(
        harness.birth(first_profile).is_none() && harness.value_row(first_profile).is_none(),
        "the window passed the first value, so it is deleted locally"
    );
    let window_rows = harness
        .state
        .rows
        .keys()
        .filter(|(family, key)| {
            *family == ColumnFamily::Flow
                && key.starts_with(&run_table_prefix(
                    harness.context.flow_hash,
                    harness.run(),
                    RunTable::HistoryRowMetadata,
                ))
        })
        .count();
    assert!(
        window_rows < 20,
        "the window prunes local history: {window_rows} rows"
    );
    let usage = record.storage.expect("the run counts storage");
    assert!(
        usage.superseded_value_bytes > 1,
        "no superseded byte cap shrinks the window: {usage:?}"
    );
}

#[test]
fn superseded_value_rows_never_fill_the_state_budget_of_a_run_that_records_offload() {
    // A run without a recorded choice counts the rows of superseded values as state until an
    // upload, so rewrites of one value fill its budget before a segment seals.
    let mut legacy = Harness::offloading(small_budget_settings(None), FlowConfig::default());
    let (legacy_accepted, rejection) = rewrite_until_rejected(&mut legacy, 1_000);
    let rejection = rejection.expect("superseded value rows fill the legacy budget");
    assert!(
        matches!(&rejection.reply.reply, Some(command_reply::Reply::Error(error))
            if error.grpc_code == RESOURCE_EXHAUSTED),
        "{:?}",
        rejection.reply
    );
    legacy.assert_storage_recounts();

    let mut offloaded =
        Harness::offloading(small_budget_settings(Some(true)), FlowConfig::default());
    let (accepted, rejection) = rewrite_until_rejected(&mut offloaded, 3 * legacy_accepted);
    assert!(
        rejection.is_none(),
        "{:?}",
        rejection.map(|outcome| outcome.reply)
    );
    assert_eq!(accepted, 3 * legacy_accepted);
    offloaded.assert_storage_recounts();
    let usage = offloaded
        .run_record()
        .storage
        .expect("the run counts storage");
    assert!(active_bytes(&usage) < SMALL_RUN_STATE_BYTES, "{usage:?}");
    assert!(
        !offloaded.segments().is_empty(),
        "segments sealed meanwhile"
    );

    // Uploads let pruning delete those rows, and history bytes shrink by exactly their size.
    offloaded.ack_all();
    offloaded.set_attribute("other", "after uploads".to_owned());
    offloaded.ack_all();
    offloaded.assert_storage_recounts();
    let uploaded = offloaded
        .run_record()
        .storage
        .expect("the run counts storage");
    assert!(uploaded.history_bytes < usage.history_bytes, "{uploaded:?}");

    // A run without offload deletes superseded values at the window, within the same budget.
    let mut local = Harness::offloading(small_budget_settings(Some(false)), FlowConfig::default());
    let (accepted, rejection) = rewrite_until_rejected(&mut local, 3 * legacy_accepted);
    assert!(
        rejection.is_none(),
        "{:?}",
        rejection.map(|outcome| outcome.reply)
    );
    assert_eq!(accepted, 3 * legacy_accepted);
    local.assert_storage_recounts();
}

#[test]
fn the_flow_config_choice_replaces_the_proposed_default_and_older_commands_record_none() {
    for (proposed, chosen, recorded) in [
        (Some(true), None, Some(true)),
        (Some(true), Some(false), Some(false)),
        (Some(false), Some(true), Some(true)),
        (None, Some(false), None),
    ] {
        let harness = Harness::on_partition(
            RunSettings {
                offload_history: proposed,
                ..settings(true)
            },
            FlowConfig {
                offload_history: chosen,
                ..FlowConfig::default()
            },
            true,
        );
        let record = harness.run_record();
        assert_eq!(
            record.settings.unwrap_or_default().offload_history,
            recorded,
            "proposed {proposed:?}, chosen {chosen:?}"
        );
        assert_eq!(
            record.history_open_segment_first_seq != 0,
            recorded.unwrap_or(true),
            "proposed {proposed:?}, chosen {chosen:?}"
        );
    }
}

#[test]
fn recorded_deletions_remove_only_the_oldest_expired_rows_in_order() {
    let mut harness = Harness::offloading(settings(true), FlowConfig::default());
    offload_first_profile(&mut harness);
    harness.write_until("three expired segments", |harness| {
        harness.expired_segments().len() >= 3
    });
    let expired = harness.expired_segments();
    let event_ids = harness.history_event_ids();
    let segments_before = harness.segments();

    // A record that skips the oldest EXPIRED row changes nothing.
    let skipped = harness.record_deleted_objects(vec![expired[1].first_seq]);
    assert!(is_ok(&skipped.reply), "{:?}", skipped.reply);
    assert!(skipped.writes.is_empty());
    assert_eq!(harness.segments(), segments_before);

    // The oldest rows go in order; a name that is not next ends the record.
    let deleted = harness.record_deleted_objects(vec![
        expired[0].first_seq,
        expired[1].first_seq,
        expired[0].first_seq,
    ]);
    assert!(is_ok(&deleted.reply), "{:?}", deleted.reply);
    let record = harness.run_record();
    assert_eq!(
        record.history_objects_deleted_through_seq,
        expired[1].last_seq
    );
    let remaining: Vec<u64> = harness
        .segments()
        .iter()
        .map(|segment| segment.first_seq)
        .collect();
    assert!(!remaining.contains(&expired[0].first_seq));
    assert!(!remaining.contains(&expired[1].first_seq));
    assert_eq!(
        remaining.get(..2),
        Some([1, expired[2].first_seq].as_slice()),
        "the genesis segment stays, then the oldest EXPIRED row left"
    );
    assert!(harness.is_delete_pending(), "EXPIRED rows remain");

    // A repeated record is stale and changes nothing.
    let repeated = harness.record_deleted_objects(vec![expired[0].first_seq]);
    assert!(is_ok(&repeated.reply), "{:?}", repeated.reply);
    assert!(repeated.writes.is_empty());

    let rest: Vec<u64> = harness
        .expired_segments()
        .iter()
        .map(|segment| segment.first_seq)
        .collect();
    let last = harness.record_deleted_objects(rest);
    assert!(is_ok(&last.reply), "{:?}", last.reply);
    assert!(harness.expired_segments().is_empty());
    assert!(
        !harness.is_delete_pending(),
        "no EXPIRED row keeps its object"
    );
    let record = harness.run_record();
    assert_eq!(
        record.history_objects_deleted_through_seq,
        record.history_expired_through_seq
    );
    assert_eq!(
        harness.history_event_ids(),
        event_ids,
        "deleted catalog rows change no read"
    );
    harness.assert_storage_recounts();

    // A later expiry asks for deletion again.
    let signals = harness.expiry_signals;
    harness.write_until("another expired segment", |harness| {
        !harness.expired_segments().is_empty()
    });
    assert!(harness.is_delete_pending());
    assert!(harness.expiry_signals > signals);
}

#[test]
fn expiry_keeps_the_birth_record_of_a_value_time_travel_restored() {
    let mut harness = Harness::offloading(
        RunSettings {
            history_window_events: 1000,
            ..settings(true)
        },
        FlowConfig::default(),
    );
    harness.set_attribute("profile", large_text("restored", 300));
    let restored = harness.attribute_value_id("profile");
    let target_seq = harness.run_record().next_run_seq - 1;
    harness.set_attributes(vec![
        ("profile".to_owned(), large_text("profile-2", 300)),
        ("notes".to_owned(), large_text("notes-1", 300)),
    ]);
    let notes = harness.attribute_value_id("notes");
    harness.set_attribute("notes", large_text("notes-2", 300));
    let notes_superseded_seq = harness.run_record().next_run_seq - 1;
    harness.ack_all();
    harness.advance(GRACE + SECOND);
    harness.set_attribute("other", "offload".to_owned());
    harness.ack_all();
    let restored_superseded_seq = harness
        .birth(restored)
        .filter(|birth| birth.is_offloaded)
        .expect("the profile value is offloaded")
        .garbage_seq;
    assert!(harness.birth(notes).is_some_and(|birth| birth.is_offloaded));

    // Time travel to before the rewrite restores the offloaded profile value locally.
    harness.travel_to_seq(target_seq);
    harness.ack_all();
    let birth = harness
        .birth(restored)
        .expect("the restored value has a birth");
    assert!(!birth.is_offloaded, "{birth:?}");
    assert!(harness.value_row(restored).is_some());
    assert_eq!(harness.attribute_value_id("profile"), restored);

    // The window passes both segments: only the notes value loses its birth record.
    harness.update_flow_config(FlowConfig {
        history_window_events: Some(2),
        ..FlowConfig::default()
    });
    harness.ack_all();
    harness.write_until("expiry of both listing segments", |harness| {
        [restored_superseded_seq, notes_superseded_seq]
            .iter()
            .all(|seq| harness.segment_covering(*seq).state == HistorySegmentState::Expired as i32)
    });
    assert_eq!(
        harness.birth(notes),
        None,
        "the notes value only its object held"
    );
    let birth = harness
        .birth(restored)
        .expect("the restored value keeps its birth");
    assert!(!birth.is_offloaded, "{birth:?}");
    let value = harness
        .value_row(restored)
        .and_then(|row| row.value)
        .and_then(|value| value.kind);
    assert_eq!(
        value,
        Some(value::Kind::StringValue(large_text("restored", 300)))
    );
    harness.assert_storage_recounts();
}

#[test]
fn expiry_is_bounded_by_listed_values_and_a_later_command_finishes_it() {
    const KEYS: usize = 6000;
    let mut harness = Harness::offloading(
        RunSettings {
            history_window_events: 1000,
            history_prune_chunk_events: 100,
            ..settings(true)
        },
        FlowConfig::default(),
    );
    let writes = |tag: &str| -> Vec<(String, String)> {
        (0..KEYS)
            .map(|index| {
                (
                    format!("key-{index}"),
                    large_text(&format!("{tag}-{index}"), 120),
                )
            })
            .collect()
    };
    harness.set_attributes(writes("first"));
    harness.set_attributes(writes("second"));
    let first_listing_seq = harness.run_record().next_run_seq - 1;
    harness.set_attributes(writes("third"));
    let second_listing_seq = harness.run_record().next_run_seq - 1;
    for seq in [first_listing_seq, second_listing_seq] {
        assert_eq!(
            harness.segment_covering(seq).superseded_value_count,
            KEYS as u64
        );
    }
    for round in 0..4 {
        harness.set_attribute("other", format!("small-{round}"));
    }
    harness.ack_all();

    // A smaller window lets the next pruning pass both listing segments in one command.
    harness.update_flow_config(FlowConfig {
        history_window_events: Some(1),
        ..FlowConfig::default()
    });
    harness.ack_all();
    harness.set_attribute("other", "shrink".to_owned());
    let record = harness.run_record();
    assert!(
        record.history_window_pruned_through_seq >= second_listing_seq,
        "one pruning passed both listing segments: {record:?}"
    );
    assert_eq!(
        harness.segment_covering(first_listing_seq).state,
        HistorySegmentState::Expired as i32
    );
    let deferred = harness.segment_covering(second_listing_seq);
    assert_eq!(
        deferred.state,
        HistorySegmentState::Uploaded as i32,
        "the second listing segment would exceed 10,000 value rows in one command"
    );
    assert_eq!(harness.segment_values(deferred.first_seq).len(), KEYS);
    assert!(record.history_expired_through_seq < deferred.first_seq);

    harness.set_attribute("other", "finish".to_owned());
    assert_eq!(
        harness.segment_covering(second_listing_seq).state,
        HistorySegmentState::Expired as i32
    );
    assert!(harness.segment_values(deferred.first_seq).is_empty());
    harness.assert_storage_recounts();
}

#[test]
fn a_purge_removes_the_delete_pending_index_row() {
    let mut harness = Harness::offloading(settings(true), FlowConfig::default());
    harness.write_until("an expired segment", |harness| {
        !harness.expired_segments().is_empty()
    });
    assert!(harness.is_delete_pending());
    harness.stop();
    harness.ack_all();
    let fired = harness.fire_retention();
    assert!(
        fired
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::RunPurgeRequested { .. })),
        "retention never waits for expired objects: {:?}",
        fired.effects
    );
    let purge = harness.apply(Command::RunPurge(RunPurgeCommand {}));
    assert!(is_ok(&purge.reply), "{:?}", purge.reply);
    assert!(!harness.is_delete_pending());
    let deleted = harness.record_deleted_objects(vec![1]);
    assert!(
        matches!(
            deleted.reply.reply,
            Some(command_reply::Reply::Error(ref error)) if error.grpc_code == 5
        ),
        "{:?}",
        deleted.reply
    );
}

#[test]
fn time_travel_computes_from_uploaded_segments_without_expired_ones() {
    let mut harness = Harness::offloading(settings(false), FlowConfig::default());
    offload_first_profile(&mut harness);
    harness.write_until("several expired segments", |harness| {
        harness.expired_segments().len() >= 4
    });
    let window_start_seq = harness.run_record().window_start_seq;
    let metadata_key = dexzd_keyspace::row_keys::history_row_metadata_key(
        harness.context.flow_hash,
        harness.run(),
        window_start_seq,
        RunTable::HistoryRecord,
        0,
    );
    let window_start: HistoryRowMetadata = harness
        .get(ColumnFamily::Flow, &metadata_key)
        .expect("the window start keeps its metadata");
    // Only objects of UPLOADED segments are at hand, as if every expired one were deleted.
    let catalog = harness.segments();
    let mut uploaded_only = LoadedHistorySegments::default();
    for (segment, content) in harness.uploaded.values() {
        let is_uploaded = catalog.iter().any(|row| {
            row.first_seq == segment.first_seq && row.state == HistorySegmentState::Uploaded as i32
        });
        if !is_uploaded {
            continue;
        }
        let records: Vec<HistoryRecord> = content
            .records
            .iter()
            .map(|row| HistoryRecord::decode(row.row.as_slice()).expect("record decodes"))
            .collect();
        uploaded_only
            .insert(segment, records.into())
            .expect("uploaded records are in order");
        let checkpoints: Vec<SegmentCheckpoint> = content
            .checkpoints
            .iter()
            .map(|row| SegmentCheckpoint {
                attempt: row.attempt,
                seq: row.seq,
                record: CheckpointRecord::decode(row.row.as_slice()).expect("checkpoint decodes"),
            })
            .collect();
        uploaded_only
            .insert_checkpoints(segment, checkpoints.into())
            .expect("uploaded checkpoints are in order");
    }
    let requests = [
        ResetFlowRequest {
            flow_id: harness.flow_id.clone(),
            reset_type: FlowResetType::Beginning as i32,
            reason: "beginning".to_owned(),
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        },
        ResetFlowRequest {
            flow_id: harness.flow_id.clone(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            reason: "window start".to_owned(),
            skip_writes_reapply: true,
            history_event_time: (i128::from(window_start.timestamp_micros) * 1000).to_string(),
            ..ResetFlowRequest::default()
        },
    ];
    for request in &requests {
        let computation = compute_time_travel_from_segments(
            &harness.context,
            &harness.state,
            &uploaded_only,
            &UploadedValues(&harness.uploaded),
            &TimeTravelComputationRequest {
                run_id: harness.run(),
                reset: request,
                now_micros: harness.now_micros,
                replay_memory_budget_bytes: u64::MAX,
            },
        )
        .expect("the computation runs");
        match computation {
            SegmentedTimeTravelComputation::Computed(TimeTravelComputation::Computed(_)) => {}
            other => panic!(
                "{:?} needs no expired segment, yet computed {other:?}",
                request.reset_type
            ),
        }
    }
}
