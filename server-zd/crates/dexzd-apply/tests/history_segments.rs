// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History segments: sealing, upload acknowledgments, pruning only uploaded rows, and accounting.

use std::collections::BTreeMap;

use dexzd_apply::read_model::{oldest_sealed_history_segment, read_history_segment_content};
use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_history_segment::{decode_history_segment, encode_history_segment};
use dexzd_keyspace::row_keys::{
    history_record_key, history_segment_value_prefix, history_upload_pending_index_key,
    prefix_range, run_singleton_key, value_garbage_key,
};
use dexzd_keyspace::{
    ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, ValueId, parse_run_key,
    run_table_prefix,
};
use dexzd_protocol::dex::{
    AttributeWrite, FlowResetType, FlowTimeoutPolicy, ResetFlowRequest, SetAttributesRequest,
    StartFlowRequest, StopFlowRequest, StopType, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, AttributesSetCommand, CommandReply, FlowCommand, FlowStartCommand,
    FlowStopCommand, HistoryRowMetadata, HistorySegmentRecord, HistorySegmentState,
    HistorySegmentUploadedCommand, HistorySegmentValueRecord, RunRecord, RunSettings,
    SetHistoryOffloadModeCommand, TimeTravelCommand, ValueGarbageRecord, command_reply,
    flow_command::Command, stored_value,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
/// gRPC status codes the assertions compare.
const INVALID_ARGUMENT: i32 = 3;
const NOT_FOUND: i32 = 5;

#[derive(Default, Clone, PartialEq, Eq, Debug)]
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
                    let doomed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in doomed {
                        self.rows.remove(&key);
                    }
                }
            }
        }
    }
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    flow_id: String,
    run_id: Vec<u8>,
    settings: RunSettings,
    request_count: u32,
}

impl Harness {
    fn new(settings: RunSettings) -> Self {
        let flow_id = "history-flow";
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, flow_id);
        Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000 * SECOND,
            flow_id: flow_id.to_owned(),
            run_id: Vec::new(),
            settings,
            request_count: 0,
        }
    }

    /// A harness whose partition offloads history before the run starts.
    fn offloading(settings: RunSettings) -> Self {
        let mut harness = Self::new(settings);
        harness.set_offload_mode(true);
        harness.start();
        harness
    }

    fn command(&mut self, body: Command) -> FlowCommand {
        self.now_micros += 1000;
        FlowCommand {
            flow_id: self.flow_id.clone(),
            run_id: if matches!(
                body,
                Command::FlowStart(_) | Command::SetHistoryOffloadMode(_)
            ) {
                Vec::new()
            } else {
                self.run_id.clone()
            },
            expected_run_attempt: 0,
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            tenant_id: 0,
            is_transactional_rpc_in_flight: false,
            command: Some(body),
        }
    }

    /// Applies twice on the committed state, requires identical outcomes, and commits.
    fn apply(&mut self, body: Command) -> ApplyOutcome {
        let command = self.command(body);
        self.apply_command(&command)
    }

    fn apply_command(&mut self, command: &FlowCommand) -> ApplyOutcome {
        let outcome =
            apply_flow_command(&self.context, &self.state, command).expect("apply succeeds");
        let again =
            apply_flow_command(&self.context, &self.state, command).expect("apply succeeds");
        assert_eq!(outcome, again, "apply must be deterministic");
        self.state.commit(&outcome);
        outcome
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

    fn start(&mut self) -> ApplyOutcome {
        let outcome = self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(StartFlowRequest {
                flow_id: self.flow_id.clone(),
                flow_type: "HistoryFlow".to_owned(),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            }),
            settings: Some(self.settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
            panic!("the run starts: {:?}", outcome.reply);
        };
        self.run_id = reply.run_id.clone();
        outcome
    }

    /// Writes `values` in one SetAttributes command and requires it to apply.
    fn set_attributes(&mut self, values: &[(&str, String)]) -> ApplyOutcome {
        self.request_count += 1;
        let request_id = format!("write-{}", self.request_count);
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: values
                    .iter()
                    .map(|(key, text)| AttributeWrite {
                        key: (*key).to_owned(),
                        value: Some(string_value(text)),
                        ..AttributeWrite::default()
                    })
                    .collect(),
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        outcome
    }

    fn set_attribute(&mut self, key: &str, text: String) -> ApplyOutcome {
        self.set_attributes(&[(key, text)])
    }

    fn stop(&mut self) -> ApplyOutcome {
        let outcome = self.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: self.flow_id.clone(),
                stop_type: StopType::Cancel as i32,
                ..StopFlowRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        outcome
    }

    fn travel_to_seq(&mut self, seq: u64) -> ApplyOutcome {
        let timestamp_micros = self.history_record_timestamp(seq);
        let outcome = self.apply(Command::TimeTravel(TimeTravelCommand {
            should_restore_running_as_pending: false,
            request: Some(ResetFlowRequest {
                flow_id: self.flow_id.clone(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                reason: "test".to_owned(),
                skip_writes_reapply: true,
                history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
                ..ResetFlowRequest::default()
            }),
        }));
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "{:?}",
            outcome.reply
        );
        outcome
    }

    fn ack(&mut self, first_seq: u64, last_seq: u64) -> ApplyOutcome {
        self.apply(Command::HistorySegmentUploaded(
            HistorySegmentUploadedCommand {
                first_seq,
                last_seq,
                content_checksum: vec![0xc5; 16],
                object_size_bytes: 1234,
            },
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

    fn last_seq(&self) -> u64 {
        self.run_record().next_run_seq - 1
    }

    fn history_record_timestamp(&self, seq: u64) -> i64 {
        let key = history_record_key(self.context.flow_hash, self.run(), seq);
        let metadata: HistoryRowMetadata = self
            .get(
                ColumnFamily::Flow,
                &dexzd_keyspace::row_keys::history_row_metadata_key(
                    self.context.flow_hash,
                    self.run(),
                    seq,
                    RunTable::HistoryRecord,
                    0,
                ),
            )
            .unwrap_or_else(|| panic!("history record {seq} has metadata"));
        assert!(self.state.rows.contains_key(&(ColumnFamily::History, key)));
        metadata.timestamp_micros
    }

    fn segments(&self) -> Vec<HistorySegmentRecord> {
        self.table_rows::<HistorySegmentRecord>(RunTable::HistorySegment)
            .into_values()
            .collect()
    }

    fn segment_values(&self, first_seq: u64) -> BTreeMap<ValueId, HistorySegmentValueRecord> {
        let prefix = history_segment_value_prefix(self.context.flow_hash, self.run(), first_seq);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|((_, key), value)| {
                (
                    ValueId::from_key_suffix(&key[prefix.len()..]).expect("value ID suffix"),
                    HistorySegmentValueRecord::decode(value.as_slice()).expect("row decodes"),
                )
            })
            .collect()
    }

    fn table_rows<RowMessage: Message + Default>(
        &self,
        table: RunTable,
    ) -> BTreeMap<Vec<u8>, RowMessage> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), table);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|((_, key), value)| {
                (
                    key[prefix.len()..].to_vec(),
                    RowMessage::decode(value.as_slice()).expect("row decodes"),
                )
            })
            .collect()
    }

    fn has_upload_pending_index_row(&self) -> bool {
        self.state.rows.contains_key(&(
            ColumnFamily::PartitionIndex,
            history_upload_pending_index_key(
                self.context.partition,
                self.context.flow_hash,
                self.run(),
            ),
        ))
    }

    fn attribute_value_id(&self, key: &str) -> ValueId {
        let row_key = dexzd_keyspace::row_keys::run_named_key(
            self.context.flow_hash,
            self.run(),
            RunTable::Attribute,
            key,
        );
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

    fn encode_segment(&self, first_seq: u64) -> Vec<u8> {
        let content = read_history_segment_content(
            &self.state,
            self.context.flow_hash,
            self.run(),
            first_seq,
        )
        .expect("the segment content reads")
        .expect("the segment exists");
        let encoded = encode_history_segment(&content).expect("the content encodes");
        assert_eq!(
            decode_history_segment(&encoded.bytes).expect("the object decodes"),
            content
        );
        encoded.bytes
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
        checkpoint_row_write_bytes_threshold: 1 << 20,
        checkpoint_command_threshold: 10_000,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: segment_target_bytes,
    }
}

/// Settings with a three-point window pruned one point at a time and frequent checkpoints.
fn small_window_settings(segment_target_bytes: u64) -> RunSettings {
    RunSettings {
        history_window_events: 3,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..settings(segment_target_bytes)
    }
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

/// A string long enough to be stored as a table value.
fn large_text(tag: &str, length: usize) -> String {
    let mut text = format!("{tag}:");
    while text.len() < length {
        text.push_str(tag);
    }
    text
}

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

fn error_code(reply: &CommandReply) -> Option<i32> {
    match &reply.reply {
        Some(command_reply::Reply::Error(error)) => Some(error.grpc_code),
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

/// Bytes a command adds to the open segment, computed from its writes: its history record and
/// checkpoint rows, and the values it superseded.
fn segment_bytes_of(outcome: &ApplyOutcome, run_id: RunId) -> (u64, u64) {
    let mut bytes = 0_u64;
    let mut superseded = 0_u64;
    for write in &outcome.writes {
        let StateWrite::Put {
            column_family,
            key,
            value,
        } = write
        else {
            continue;
        };
        let Ok(parsed) = parse_run_key(key) else {
            continue;
        };
        if parsed.run_id != run_id {
            continue;
        }
        match (column_family, parsed.table) {
            (ColumnFamily::History, _) => bytes += (key.len() + value.len()) as u64,
            (ColumnFamily::Flow, RunTable::ValueGarbage) => {
                bytes += ValueGarbageRecord::decode(value.as_slice())
                    .expect("garbage rows decode")
                    .size_bytes;
                superseded += 1;
            }
            _ => {}
        }
    }
    (bytes, superseded)
}

/// Asserts that the segments are contiguous from seq 1 and from event 1, end right before the
/// open segment, and that U names the last uploaded one.
fn assert_segment_chain(harness: &Harness) {
    let record = harness.run_record();
    let mut next_seq = 1;
    let mut next_event_id = 1;
    for segment in harness.segments() {
        assert_eq!(segment.first_seq, next_seq, "{segment:?}");
        assert!(segment.first_seq <= segment.last_seq, "{segment:?}");
        assert_eq!(segment.first_event_id, next_event_id, "{segment:?}");
        let state = if segment.last_seq <= record.history_uploaded_through_seq {
            HistorySegmentState::Uploaded
        } else {
            HistorySegmentState::Sealed
        };
        assert_eq!(segment.state, state as i32, "{segment:?}");
        assert_eq!(
            segment.superseded_value_count,
            harness.segment_values(segment.first_seq).len() as u64
        );
        next_seq = segment.last_seq + 1;
        next_event_id = segment.last_event_id + 1;
    }
    assert_eq!(record.history_open_segment_first_seq, next_seq);
    assert_eq!(record.history_open_segment_first_event_id, next_event_id);
    let has_sealed = harness
        .segments()
        .iter()
        .any(|segment| segment.state == HistorySegmentState::Sealed as i32);
    assert_eq!(harness.has_upload_pending_index_row(), has_sealed);
}

#[test]
fn segments_seal_once_their_bytes_reach_the_target() {
    let target_bytes = 6_000;
    let mut harness = Harness::offloading(settings(target_bytes));
    let start_record = harness.run_record();
    assert_eq!(start_record.history_open_segment_first_seq, 1);
    let mut accumulated = start_record.history_open_segment_bytes;
    assert!(accumulated > 0, "the start command's record counts");
    let mut sealed_through = Vec::new();
    for round in 0..12 {
        let outcome = harness.set_attribute("profile", large_text(&format!("v{round}"), 1500));
        let (bytes, _) = segment_bytes_of(&outcome, harness.run());
        accumulated += bytes;
        let record = harness.run_record();
        if accumulated >= target_bytes {
            assert_eq!(sealed_effect_count(&outcome), 1, "round {round}");
            assert_eq!(record.history_open_segment_bytes, 0);
            assert_eq!(
                record.history_open_segment_first_seq,
                harness.last_seq() + 1
            );
            sealed_through.push(harness.last_seq());
            accumulated = 0;
        } else {
            assert_eq!(sealed_effect_count(&outcome), 0, "round {round}");
            assert_eq!(record.history_open_segment_bytes, accumulated);
        }
    }
    let segments = harness.segments();
    assert!(segments.len() >= 3, "{segments:?}");
    assert_eq!(
        segments
            .iter()
            .map(|segment| segment.last_seq)
            .collect::<Vec<_>>(),
        sealed_through
    );
    assert_segment_chain(&harness);
    let first = &segments[0];
    assert_eq!(
        first.sealed_at_micros,
        harness.history_record_timestamp(first.last_seq)
    );
    assert!(first.content_checksum.is_empty());
    for segment in &segments {
        // Every superseded value of the segment's range is listed, with its seq and size.
        let values = harness.segment_values(segment.first_seq);
        assert!(
            !values.is_empty(),
            "each segment superseded a profile value"
        );
        for (value_id, row) in &values {
            assert!((segment.first_seq..=segment.last_seq).contains(&row.superseded_seq));
            let garbage: ValueGarbageRecord = harness
                .get(
                    ColumnFamily::Flow,
                    &value_garbage_key(
                        harness.context.flow_hash,
                        harness.run(),
                        row.superseded_seq,
                        *value_id,
                    ),
                )
                .expect("the superseded value is still in table G");
            assert_eq!(garbage.size_bytes, row.size_bytes);
        }
        harness.encode_segment(segment.first_seq);
    }
}

#[test]
fn segments_seal_once_ten_thousand_values_were_superseded() {
    let mut harness = Harness::offloading(settings(256 << 20));
    let keys: Vec<String> = (0..10_000)
        .map(|index| format!("item-{index:05}"))
        .collect();
    let write_all = |harness: &mut Harness, version: &str, keys: &[String]| {
        let values: Vec<(&str, String)> = keys
            .iter()
            .map(|key| (key.as_str(), large_text(version, 110)))
            .collect();
        harness.set_attributes(&values)
    };
    let first = write_all(&mut harness, "a", &keys);
    assert_eq!(sealed_effect_count(&first), 0);
    let almost = write_all(&mut harness, "b", &keys[..9_999]);
    assert_eq!(sealed_effect_count(&almost), 0);
    assert_eq!(
        harness
            .run_record()
            .history_open_segment_superseded_value_count,
        9_999
    );
    let last = write_all(&mut harness, "c", &keys[9_999..]);
    assert_eq!(sealed_effect_count(&last), 1);
    let segments = harness.segments();
    assert_eq!(segments.len(), 1);
    assert_eq!(segments[0].superseded_value_count, 10_000);
    assert_eq!(harness.segment_values(1).len(), 10_000);
    assert!(
        harness.run_record().history_open_segment_bytes == 0
            && segments[0].last_seq == harness.last_seq()
    );
    assert_segment_chain(&harness);
}

#[test]
fn closing_a_run_seals_its_open_segment() {
    let mut harness = Harness::offloading(settings(256 << 20));
    harness.set_attribute("profile", large_text("first", 400));
    harness.set_attribute("profile", large_text("second", 400));
    assert!(harness.segments().is_empty());
    let stop = harness.stop();
    assert_eq!(sealed_effect_count(&stop), 1);
    let segments = harness.segments();
    assert_eq!(segments.len(), 1);
    let segment = &segments[0];
    assert_eq!(
        (segment.first_seq, segment.last_seq),
        (1, harness.last_seq())
    );
    assert_eq!(
        segment.last_event_id,
        harness.run_record().next_event_id - 1,
        "the FlowClosed event is the segment's last event"
    );
    assert_eq!(harness.segment_values(1).len(), 1);
    assert_segment_chain(&harness);
}

#[test]
fn reviving_a_sealed_value_keeps_the_segment_list_and_encoding() {
    let mut harness = Harness::offloading(settings(256 << 20));
    harness.set_attribute("profile", large_text("original", 500));
    let original_seq = harness.last_seq();
    let original = harness.attribute_value_id("profile");
    harness.set_attribute("profile", large_text("replacement", 500));
    let replaced_seq = harness.last_seq();
    let garbage_key = value_garbage_key(
        harness.context.flow_hash,
        harness.run(),
        replaced_seq,
        original,
    );
    assert!(
        harness
            .state
            .rows
            .contains_key(&(ColumnFamily::Flow, garbage_key.clone()))
    );
    harness.stop();
    let segment = harness.segments()[0].clone();
    let listed_before = harness.segment_values(segment.first_seq);
    assert!(listed_before.contains_key(&original));
    let encoded_before = harness.encode_segment(segment.first_seq);

    harness.travel_to_seq(original_seq);
    assert_eq!(harness.attribute_value_id("profile"), original);
    assert!(
        !harness
            .state
            .rows
            .contains_key(&(ColumnFamily::Flow, garbage_key)),
        "time travel revived the value"
    );
    assert_eq!(harness.segments()[0], segment);
    assert_eq!(harness.segment_values(segment.first_seq), listed_before);
    assert_eq!(harness.encode_segment(segment.first_seq), encoded_before);
}

/// Seals three segments, each at a closing or byte-sized command.
fn harness_with_three_sealed_segments() -> Harness {
    let mut harness = Harness::offloading(settings(4_000));
    for round in 0..40 {
        harness.set_attribute("profile", large_text(&format!("v{round}"), 1500));
        if harness.segments().len() == 3 {
            break;
        }
    }
    assert_eq!(harness.segments().len(), 3);
    harness
}

#[test]
fn acknowledgments_accept_only_the_oldest_sealed_segment() {
    let mut harness = harness_with_three_sealed_segments();
    let segments = harness.segments();
    let (first, second, third) = (&segments[0], &segments[1], &segments[2]);
    let record_before = harness.run_record();
    assert_eq!(
        oldest_sealed_history_segment(&harness.state, harness.context.flow_hash, harness.run())
            .expect("reads")
            .as_ref(),
        Some(first)
    );

    for (first_seq, last_seq) in [
        (second.first_seq, second.last_seq),
        (first.first_seq, first.last_seq + 1),
        (first.first_seq + 1, first.last_seq),
    ] {
        let outcome = harness.ack(first_seq, last_seq);
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        assert!(outcome.writes.is_empty(), "a stale ack writes nothing");
    }

    let applied = harness.ack(first.first_seq, first.last_seq);
    assert!(is_ok(&applied.reply));
    assert!(applied.effects.is_empty());
    let record = harness.run_record();
    assert_eq!(record.history_uploaded_through_seq, first.last_seq);
    assert_eq!(
        record.next_run_seq, record_before.next_run_seq,
        "no run seq"
    );
    assert_eq!(
        record.next_event_id, record_before.next_event_id,
        "no event"
    );
    assert!(
        applied.writes.iter().all(|write| !matches!(
            write,
            StateWrite::Put {
                column_family: ColumnFamily::History,
                ..
            }
        )),
        "an ack records no history"
    );
    let uploaded = &harness.segments()[0];
    assert_eq!(uploaded.state, HistorySegmentState::Uploaded as i32);
    assert_eq!(uploaded.content_checksum, vec![0xc5; 16]);
    assert_eq!(uploaded.object_size_bytes, 1234);
    assert!(harness.has_upload_pending_index_row());
    assert_segment_chain(&harness);

    let duplicate = harness.ack(first.first_seq, first.last_seq);
    assert!(is_ok(&duplicate.reply) && duplicate.writes.is_empty());

    harness.ack(second.first_seq, second.last_seq);
    assert!(harness.has_upload_pending_index_row());
    let last = harness.ack(third.first_seq, third.last_seq);
    assert!(!last.writes.is_empty());
    assert!(!harness.has_upload_pending_index_row());
    assert_eq!(
        harness.run_record().history_uploaded_through_seq,
        third.last_seq
    );
    assert!(
        oldest_sealed_history_segment(&harness.state, harness.context.flow_hash, harness.run())
            .expect("reads")
            .is_none()
    );
    let after_all = harness.ack(third.first_seq, third.last_seq);
    assert!(is_ok(&after_all.reply) && after_all.writes.is_empty());
    assert_segment_chain(&harness);
}

#[test]
fn malformed_and_misrouted_acknowledgments_are_rejected_without_writes() {
    let mut harness = harness_with_three_sealed_segments();
    let first = harness.segments()[0].clone();
    let short_checksum = harness.apply(Command::HistorySegmentUploaded(
        HistorySegmentUploadedCommand {
            first_seq: first.first_seq,
            last_seq: first.last_seq,
            content_checksum: vec![1; 15],
            object_size_bytes: 10,
        },
    ));
    assert_eq!(error_code(&short_checksum.reply), Some(INVALID_ARGUMENT));
    assert!(short_checksum.writes.is_empty());

    let empty_object = harness.apply(Command::HistorySegmentUploaded(
        HistorySegmentUploadedCommand {
            first_seq: first.first_seq,
            last_seq: first.last_seq,
            content_checksum: vec![1; 16],
            object_size_bytes: 0,
        },
    ));
    assert_eq!(error_code(&empty_object.reply), Some(INVALID_ARGUMENT));
    assert!(empty_object.writes.is_empty());
    assert_eq!(harness.run_record().history_uploaded_through_seq, 0);

    let mut unknown_run = harness.command(Command::HistorySegmentUploaded(
        HistorySegmentUploadedCommand {
            first_seq: first.first_seq,
            last_seq: first.last_seq,
            content_checksum: vec![1; 16],
            object_size_bytes: 10,
        },
    ));
    unknown_run.run_id = vec![8; 16];
    let outcome = harness.apply_command(&unknown_run);
    assert_eq!(error_code(&outcome.reply), Some(NOT_FOUND));
    assert!(outcome.writes.is_empty());
}

#[test]
fn acknowledgments_still_apply_after_time_travel() {
    let mut harness = harness_with_three_sealed_segments();
    let first = harness.segments()[0].clone();
    harness.travel_to_seq(2);
    assert_eq!(harness.run_record().current_attempt, 2);
    let ack = harness.command(Command::HistorySegmentUploaded(
        HistorySegmentUploadedCommand {
            first_seq: first.first_seq,
            last_seq: first.last_seq,
            content_checksum: vec![2; 16],
            object_size_bytes: 99,
        },
    ));
    assert_eq!(
        ack.expected_run_attempt, 0,
        "uploads are not bound to an attempt"
    );
    let outcome = harness.apply_command(&ack);
    assert!(is_ok(&outcome.reply) && !outcome.writes.is_empty());
    assert_eq!(
        harness.run_record().history_uploaded_through_seq,
        first.last_seq
    );
    assert_segment_chain(&harness);
}

#[test]
fn segment_rows_count_as_history_and_never_toward_run_state() {
    let mut harness = harness_with_three_sealed_segments();
    harness.stop();
    let usage = harness
        .run_record()
        .storage
        .expect("the run has storage usage");
    let prefix = dexzd_keyspace::run_prefix(harness.context.flow_hash, harness.run());
    let mut state_row_bytes = 0_u64;
    let mut history_bytes = 0_u64;
    let mut segment_row_bytes = 0_u64;
    for ((column_family, key), value) in &harness.state.rows {
        if *column_family != ColumnFamily::Flow || !key.starts_with(&prefix) {
            continue;
        }
        let row_bytes = (key.len() + value.len()) as u64;
        match parse_run_key(key).expect("run keys parse").table {
            RunTable::HistoryRowMetadata => {
                let metadata =
                    HistoryRowMetadata::decode(value.as_slice()).expect("metadata decodes");
                history_bytes += row_bytes + metadata.row_size_bytes;
            }
            RunTable::HistorySegment | RunTable::HistorySegmentValue => {
                history_bytes += row_bytes;
                segment_row_bytes += row_bytes;
            }
            _ => state_row_bytes += row_bytes,
        }
    }
    assert!(segment_row_bytes > 0);
    assert_eq!(usage.state_row_bytes, state_row_bytes);
    assert_eq!(usage.history_bytes, history_bytes);
}

#[test]
fn with_offload_off_nothing_is_sealed() {
    let mut harness = Harness::new(small_window_settings(1));
    harness.start();
    for round in 0..12 {
        let outcome = harness.set_attribute("profile", large_text(&format!("v{round}"), 300));
        assert_eq!(sealed_effect_count(&outcome), 0);
    }
    let stop = harness.stop();
    assert_eq!(sealed_effect_count(&stop), 0);
    let record = harness.run_record();
    assert_eq!(
        (
            record.history_open_segment_first_seq,
            record.history_open_segment_bytes,
            record.history_open_segment_superseded_value_count,
            record.history_uploaded_through_seq,
            record.history_open_segment_first_event_id,
        ),
        (0, 0, 0, 0, 0)
    );
    assert!(harness.segments().is_empty());
    assert!(
        harness
            .table_rows::<HistorySegmentValueRecord>(RunTable::HistorySegmentValue)
            .is_empty()
    );
    assert!(!harness.has_upload_pending_index_row());
    assert!(
        harness.history_record_count() < harness.last_seq() as usize,
        "window pruning deletes as before"
    );
}

#[test]
fn turning_offload_off_keeps_sealed_content_until_it_is_acknowledged() {
    let mut harness = Harness::offloading(small_window_settings(2_000));
    for round in 0..8 {
        harness.set_attribute("profile", large_text(&format!("on{round}"), 600));
    }
    let segments = harness.segments();
    assert!(segments.len() >= 2, "{segments:?}");
    let encodings: Vec<Vec<u8>> = segments
        .iter()
        .map(|segment| harness.encode_segment(segment.first_seq))
        .collect();
    harness.set_offload_mode(false);
    for round in 0..20 {
        let outcome = harness.set_attribute("profile", large_text(&format!("off{round}"), 600));
        assert_eq!(sealed_effect_count(&outcome), 0);
        assert_eq!(
            harness.run_record().history_open_segment_first_seq,
            harness.last_seq() + 1,
            "a command applied while offload is off belongs to no segment"
        );
    }
    assert!(
        harness.run_record().window_start_seq > 10,
        "the window still moves"
    );
    assert_eq!(
        harness.history_record_count(),
        harness.last_seq() as usize,
        "SEALED segments keep the fence, so nothing is pruned"
    );
    assert_eq!(
        harness
            .table_rows::<ValueGarbageRecord>(RunTable::ValueGarbage)
            .len(),
        harness.last_seq() as usize - 2,
        "every superseded value stays"
    );
    assert_eq!(harness.segments(), segments);
    for (segment, encoding) in segments.iter().zip(&encodings) {
        assert_eq!(&harness.encode_segment(segment.first_seq), encoding);
    }

    let first = segments[0].clone();
    harness.ack(first.first_seq, first.last_seq);
    for round in 0..4 {
        harness.set_attribute("profile", large_text(&format!("half{round}"), 600));
    }
    let remaining = harness.history_record_seqs();
    assert!(
        remaining.len() < harness.last_seq() as usize,
        "uploaded rows are pruned"
    );
    for seq in first.last_seq + 1..=harness.last_seq() {
        assert!(remaining.contains(&seq), "record {seq} is not uploaded yet");
    }
    for segment in &segments[1..] {
        let encoding = harness.encode_segment(segment.first_seq);
        let index = segments
            .iter()
            .position(|sealed| sealed == segment)
            .expect("the segment is listed");
        assert_eq!(encoding, encodings[index]);
    }

    for segment in &segments[1..] {
        harness.ack(segment.first_seq, segment.last_seq);
    }
    assert!(!harness.has_upload_pending_index_row());
    let before = harness.history_record_count();
    for round in 0..4 {
        harness.set_attribute("profile", large_text(&format!("drained{round}"), 600));
    }
    let after_last_segment = segments.last().expect("segments exist").last_seq + 1;
    assert!(
        harness.history_record_count() < before + 4,
        "with no SEALED segment left, pruning returns to the window rule"
    );
    assert!(
        !harness.history_record_seqs().contains(&after_last_segment),
        "rows of commands applied while offload was off are pruned like any other"
    );
}

#[test]
fn reenabling_offload_opens_a_fresh_segment_after_the_commands_applied_while_off() {
    let mut harness = Harness::offloading(settings(20_000));
    harness.set_attribute("profile", large_text("on0", 1500));
    harness.set_attribute("profile", large_text("on1", 1500));
    assert!(
        harness.run_record().history_open_segment_bytes > 0,
        "a partial segment is open"
    );
    let sealed_before = harness.segments();
    harness.set_offload_mode(false);
    for round in 0..3 {
        harness.set_attribute("profile", large_text(&format!("off{round}"), 1500));
    }
    let gap_last_seq = harness.last_seq();
    let gap_last_event_id = harness.run_record().next_event_id - 1;
    let record = harness.run_record();
    assert_eq!(record.history_open_segment_first_seq, gap_last_seq + 1);
    assert_eq!(
        record.history_open_segment_first_event_id,
        gap_last_event_id + 1
    );
    assert_eq!(record.history_open_segment_bytes, 0);
    assert_eq!(harness.segments(), sealed_before);

    harness.set_offload_mode(true);
    while harness.segments().len() == sealed_before.len() {
        let round = harness.last_seq();
        harness.set_attribute("profile", large_text(&format!("again{round}"), 1500));
    }
    let segments = harness.segments();
    let reopened = segments.last().expect("a segment sealed after re-enabling");
    assert_eq!(reopened.first_seq, gap_last_seq + 1);
    assert_eq!(reopened.first_event_id, gap_last_event_id + 1);
    let previous_last_seq = sealed_before.last().map_or(0, |segment| segment.last_seq);
    assert!(
        previous_last_seq < gap_last_seq,
        "the commands applied while off form a gap between segments"
    );
    for (value_id, row) in harness.segment_values(reopened.first_seq) {
        assert!(
            row.superseded_seq >= reopened.first_seq,
            "value {value_id:?} superseded in the gap is listed"
        );
    }
    let listed: Vec<u64> = segments
        .iter()
        .flat_map(|segment| harness.segment_values(segment.first_seq).into_values())
        .map(|row| row.superseded_seq)
        .collect();
    assert!(
        listed
            .iter()
            .all(|seq| *seq <= previous_last_seq || *seq > gap_last_seq),
        "values superseded while offload was off belong to no segment: {listed:?}"
    );
    harness.encode_segment(reopened.first_seq);
}

#[test]
fn runs_started_before_offload_keep_no_segment_state_and_keep_their_history() {
    let mut harness = Harness::new(small_window_settings(1));
    harness.start();
    harness.set_offload_mode(true);
    for round in 0..12 {
        let outcome = harness.set_attribute("profile", large_text(&format!("v{round}"), 300));
        assert_eq!(sealed_effect_count(&outcome), 0);
    }
    assert_eq!(harness.run_record().history_open_segment_first_seq, 0);
    assert!(harness.segments().is_empty());
    assert_eq!(
        harness.history_record_count(),
        harness.last_seq() as usize,
        "nothing is uploaded, so nothing is pruned"
    );
    assert!(
        harness.run_record().window_start_seq > 1,
        "the window still moves"
    );
}

#[test]
fn window_pruning_deletes_only_uploaded_rows() {
    let mut harness = Harness::offloading(small_window_settings(2_000));
    for round in 0..16 {
        harness.set_attribute("profile", large_text(&format!("v{round}"), 600));
    }
    let record = harness.run_record();
    assert!(record.window_start_seq > 5, "the window moved: {record:?}");
    assert_eq!(
        harness.history_record_count(),
        harness.last_seq() as usize,
        "no segment is uploaded, so every record stays"
    );
    let garbage_before = harness.table_rows::<ValueGarbageRecord>(RunTable::ValueGarbage);
    assert_eq!(garbage_before.len(), 15, "every superseded value stays");

    let segments = harness.segments();
    assert!(segments.len() >= 3);
    let uploaded = segments[1].clone();
    harness.ack(segments[0].first_seq, segments[0].last_seq);
    harness.ack(uploaded.first_seq, uploaded.last_seq);
    for round in 0..4 {
        harness.set_attribute("profile", large_text(&format!("w{round}"), 600));
    }
    let uploaded_through = harness.run_record().history_uploaded_through_seq;
    assert_eq!(uploaded_through, uploaded.last_seq);
    let remaining_records = harness.history_record_seqs();
    assert_eq!(
        remaining_records.first(),
        Some(&1),
        "the genesis record stays"
    );
    assert!(
        remaining_records.len() < harness.last_seq() as usize,
        "uploaded records are pruned"
    );
    for seq in uploaded_through + 1..=harness.last_seq() {
        assert!(
            remaining_records.contains(&seq),
            "record {seq} is not uploaded yet"
        );
    }
    let garbage_after = harness.table_rows::<ValueGarbageRecord>(RunTable::ValueGarbage);
    let garbage_seq =
        |suffix: &Vec<u8>| u64::from_be_bytes(suffix[..8].try_into().expect("eight seq bytes"));
    assert!(
        garbage_before
            .keys()
            .any(|suffix| garbage_seq(suffix) <= uploaded_through
                && !garbage_after.contains_key(suffix)),
        "uploaded values are pruned"
    );
    for suffix in garbage_before.keys() {
        let seq = garbage_seq(suffix);
        if seq > uploaded_through {
            assert!(
                garbage_after.contains_key(suffix),
                "value superseded at {seq} stays"
            );
        }
    }
    for segment in harness.segments() {
        if segment.state == HistorySegmentState::Sealed as i32 {
            harness.encode_segment(segment.first_seq);
        }
    }
    assert_segment_chain(&harness);
}

impl Harness {
    fn history_record_seqs(&self) -> Vec<u64> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), RunTable::HistoryRecord);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::History, start)..(ColumnFamily::History, end))
            .map(|((_, key), _)| {
                u64::from_be_bytes(key[prefix.len()..].try_into().expect("eight seq bytes"))
            })
            .collect()
    }

    fn history_record_count(&self) -> usize {
        self.history_record_seqs().len()
    }
}

#[test]
fn purging_a_run_deletes_its_upload_pending_index_row() {
    let mut harness = harness_with_three_sealed_segments();
    harness.stop();
    assert!(harness.has_upload_pending_index_row());
    let record = harness.run_record();
    let retention = record.retention_deadline_micros;
    assert!(retention > 0);
    harness.now_micros = retention;
    let fired = harness.apply(Command::TimersFired(
        dexzd_protocol::dexzd::v1::TimersFiredCommand {
            timers: vec![dexzd_protocol::dexzd::v1::TimerId {
                run_attempt: 0,
                kind: Some(dexzd_protocol::dexzd::v1::timer_id::Kind::Retention(
                    dexzd_protocol::dexzd::v1::RetentionTimerId {},
                )),
            }],
        },
    ));
    assert!(
        fired
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::RunPurgeRequested { .. })),
        "{:?}",
        fired.effects
    );
    let purge = harness.apply(Command::RunPurge(
        dexzd_protocol::dexzd::v1::RunPurgeCommand {},
    ));
    assert!(is_ok(&purge.reply), "{:?}", purge.reply);
    assert!(!harness.has_upload_pending_index_row());
    assert!(
        harness
            .state
            .rows
            .keys()
            .all(
                |(column_family, key)| *column_family != ColumnFamily::PartitionIndex
                    || key.get(2) != Some(&(dexzd_keyspace::IndexKind::HistoryUploadPending as u8))
            )
    );
    let run_prefix = dexzd_keyspace::run_prefix(harness.context.flow_hash, harness.run());
    assert!(
        harness
            .state
            .rows
            .keys()
            .all(
                |(column_family, key)| *column_family == ColumnFamily::PartitionIndex
                    || !key.starts_with(&run_prefix)
            ),
        "the purge deleted every row of the run, segment rows included"
    );
}
