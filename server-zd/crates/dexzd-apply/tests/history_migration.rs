// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Migrating the history of runs that started before their partition offloaded history.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::read_model::{
    SegmentedHistoryPage, get_history_events_from_segments, read_history_segment_content,
};
use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyOutcome, FlowStateReader, LoadedHistorySegments,
    StateReadError, StateRow, StateWrite, apply_flow_command, apply_staged_time_travel_for_test,
    is_unstaged_time_travel_for_test, stage_time_travel_for_test,
};
use dexzd_history_segment::{
    HistorySegmentContent, decode_history_segment, encode_history_segment,
};
use dexzd_keyspace::row_keys::{
    history_record_key, history_row_metadata_key, history_segment_value_prefix,
    history_upload_pending_index_key, prefix_range, run_named_key, run_singleton_key,
};
use dexzd_keyspace::{
    ColumnFamily, IndexKind, PartitionHasher, RunId, RunTable, TenantId, ValueId, run_prefix,
    run_table_prefix,
};
use dexzd_protocol::dex::{
    AttributeWrite, ChannelMessage, FlowResetType, FlowTimeoutPolicy, GetHistoryEventsResponse,
    PublishToChannelRequest, ResetFlowRequest, SetAttributesRequest, StartFlowRequest,
    StopFlowRequest, StopType, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, AttributesSetCommand, ChannelPublishCommand, CommandReply, FlowCommand,
    FlowStartCommand, FlowStopCommand, HistoryRecord, HistoryRowMetadata, HistorySegmentRecord,
    HistorySegmentState, HistorySegmentUploadedCommand, HistorySegmentValueRecord,
    RetentionTimerId, RunPurgeCommand, RunRecord, RunSettings, SetHistoryOffloadModeCommand,
    TimeTravelCommand, TimerId, TimersFiredCommand, command_reply, flow_command::Command,
    stored_value, timer_id,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
/// Most commands one migration may take in these tests before it must have caught up.
const MAX_MIGRATION_COMMANDS: usize = 60;

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
    /// Content of each acknowledged segment by first seq, as its uploaded object holds it.
    uploaded: BTreeMap<u64, HistorySegmentContent>,
}

impl Harness {
    fn new(settings: RunSettings) -> Self {
        let flow_id = "migrating-flow";
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
            uploaded: BTreeMap::new(),
        }
    }

    /// A run that started while its partition did not offload, with `commands` commands since.
    fn legacy(settings: RunSettings, commands: usize) -> Self {
        let mut harness = Self::new(settings);
        harness.start();
        for round in 0..commands {
            harness.apply_round(&format!("legacy{round}"));
        }
        assert_eq!(harness.run_record().history_open_segment_first_seq, 0);
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
        self.apply_command(command)
    }

    /// Applies `command`; an unstaged TimeTravel is computed and staged first, as an owner does.
    fn apply_command(&mut self, command: FlowCommand) -> ApplyOutcome {
        if is_unstaged_time_travel_for_test(&command) {
            let staged = stage_time_travel_for_test(&self.context, &self.state, &command)
                .expect("time travel computes");
            return apply_staged_time_travel_for_test(staged, |staged_command| {
                self.apply_command(staged_command)
            });
        }
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        let again =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
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
                flow_type: "MigratingFlow".to_owned(),
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

    /// Replaces the `profile` Attribute when `tag` ends with an even digit and publishes to a
    /// Channel otherwise, so records alternate between superseding a value and adding an event.
    fn apply_round(&mut self, tag: &str) -> ApplyOutcome {
        let is_write = tag
            .bytes()
            .last()
            .is_some_and(|digit| (digit - b'0').is_multiple_of(2));
        if is_write {
            self.set_attribute("profile", large_text(tag, 300))
        } else {
            self.publish("updates", &large_text(tag, 200))
        }
    }

    fn set_attribute(&mut self, key: &str, text: String) -> ApplyOutcome {
        self.set_attributes(&[(key.to_owned(), text)])
    }

    fn set_attributes(&mut self, values: &[(String, String)]) -> ApplyOutcome {
        self.request_count += 1;
        let request_id = format!("write-{}", self.request_count);
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: values
                    .iter()
                    .map(|(key, text)| AttributeWrite {
                        key: key.clone(),
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

    fn publish(&mut self, channel_name: &str, text: &str) -> ApplyOutcome {
        self.request_count += 1;
        let outcome = self.apply(Command::ChannelPublish(ChannelPublishCommand {
            request: Some(PublishToChannelRequest {
                messages: vec![ChannelMessage {
                    channel_name: channel_name.to_owned(),
                    value: Some(string_value(text)),
                    message_id: format!("message-{}", self.request_count),
                }],
                ..PublishToChannelRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        outcome
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
        let timestamp_micros = self.record_metadata(seq).timestamp_micros;
        let outcome = self.apply(Command::TimeTravel(TimeTravelCommand {
            staging_id: Vec::new(),
            computed_through_seq: 0,
            target_run_attempt: 0,
            target_run_seq: 0,
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

    fn fire_retention(&mut self) -> ApplyOutcome {
        self.now_micros = self.run_record().retention_deadline_micros;
        self.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: 0,
                kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
            }],
        }))
    }

    /// Acknowledges the oldest SEALED segment with the checksum of its encoded content.
    fn acknowledge_oldest_sealed(&mut self) -> Option<HistorySegmentRecord> {
        let uploaded_through_seq = self.run_record().history_uploaded_through_seq;
        let segment = self
            .segments()
            .into_iter()
            .find(|segment| segment.last_seq > uploaded_through_seq)?;
        let content = self.segment_content(segment.first_seq);
        let encoded = encode_history_segment(&content).expect("the content encodes");
        self.uploaded.insert(segment.first_seq, content);
        let outcome = self.apply(Command::HistorySegmentUploaded(
            HistorySegmentUploadedCommand {
                first_seq: segment.first_seq,
                last_seq: segment.last_seq,
                content_checksum: encoded.content_checksum.to_vec(),
                object_size_bytes: encoded.bytes.len() as u64,
            },
        ));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        assert_eq!(
            self.run_record().history_uploaded_through_seq,
            segment.last_seq
        );
        Some(segment)
    }

    fn acknowledge_every_sealed(&mut self) {
        while self.acknowledge_oldest_sealed().is_some() {}
        assert!(!self.has_upload_pending_index_row());
    }

    /// Applies rounds until the run's migration caught up, requiring exactly one sealed segment
    /// per command. Returns the commands the migration took.
    fn migrate_to_completion(&mut self, tag: &str) -> usize {
        for command_count in 1..=MAX_MIGRATION_COMMANDS {
            let outcome = self.apply_round(&format!("{tag}{command_count}"));
            assert_eq!(
                sealed_effect_count(&outcome),
                1,
                "every command of a migrating run seals one segment"
            );
            assert_segment_chain(self);
            if self.run_record().history_migration_started_seq == 0 {
                return command_count;
            }
        }
        panic!("the migration did not catch up within {MAX_MIGRATION_COMMANDS} commands");
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

    fn record_metadata(&self, seq: u64) -> HistoryRowMetadata {
        self.get(
            ColumnFamily::Flow,
            &history_row_metadata_key(
                self.context.flow_hash,
                self.run(),
                seq,
                RunTable::HistoryRecord,
                0,
            ),
        )
        .unwrap_or_else(|| panic!("history record {seq} has metadata"))
    }

    /// Seqs of the history records stored locally, ascending.
    fn stored_record_seqs(&self) -> Vec<u64> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), RunTable::HistoryRecord);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::History, start)..(ColumnFamily::History, end))
            .map(|((_, key), _)| {
                u64::from_be_bytes(key[prefix.len()..].try_into().expect("eight-byte seq"))
            })
            .collect()
    }

    /// Event IDs of the history records that window pruning kept, from local rows or, once
    /// offload deleted them, from their uploaded segment.
    fn stored_event_ids(&self) -> BTreeSet<i64> {
        let metadata_prefix = run_table_prefix(
            self.context.flow_hash,
            self.run(),
            RunTable::HistoryRowMetadata,
        );
        let (start, end) = prefix_range(&metadata_prefix);
        let kept_record_seqs: BTreeSet<u64> = self
            .state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|((_, key), _)| &key[metadata_prefix.len()..])
            .filter(|suffix| suffix[8] == RunTable::HistoryRecord as u8)
            .map(|suffix| u64::from_be_bytes(suffix[..8].try_into().expect("eight-byte seq")))
            .collect();
        kept_record_seqs
            .into_iter()
            .flat_map(|seq| {
                let key = history_record_key(self.context.flow_hash, self.run(), seq);
                let record: HistoryRecord = self
                    .get(ColumnFamily::History, &key)
                    .or_else(|| self.uploaded_record(seq))
                    .unwrap_or_else(|| panic!("record {seq} is stored locally or uploaded"));
                record.events.into_iter().map(|event| event.event_id)
            })
            .collect()
    }

    /// The record at `seq` in the uploaded segment that covers it, if any.
    fn uploaded_record(&self, seq: u64) -> Option<HistoryRecord> {
        let (_, content) = self.uploaded.range(..=seq).next_back()?;
        let row = content.records.iter().find(|row| row.seq == seq)?;
        Some(HistoryRecord::decode(row.row.as_slice()).expect("the uploaded record decodes"))
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

    fn segment_content(&self, first_seq: u64) -> HistorySegmentContent {
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
        content
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

    /// Reads the first page as the engine does: uploaded seqs from their objects, the rest
    /// locally.
    fn history_page(&self) -> GetHistoryEventsResponse {
        let mut loaded = LoadedHistorySegments::default();
        for segment in self.segments() {
            if let Some(content) = self.uploaded.get(&segment.first_seq) {
                let records: Vec<HistoryRecord> = content
                    .records
                    .iter()
                    .map(|row| HistoryRecord::decode(row.row.as_slice()).expect("record decodes"))
                    .collect();
                loaded
                    .insert(&segment, records.into())
                    .expect("uploaded records are in order");
            }
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
            SegmentedHistoryPage::Page(page) => page,
            SegmentedHistoryPage::NeedsSegments(needed) => {
                panic!("every uploaded segment is loaded, yet the read needs {needed:?}")
            }
        }
    }

    /// Clears the event ID fields of every history record's metadata, as rows of binaries
    /// older than `last_assigned_event_id` store them.
    fn clear_metadata_event_ids(&mut self) {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.run(),
            RunTable::HistoryRowMetadata,
        );
        for ((column_family, key), value) in &mut self.state.rows {
            let is_record_metadata = *column_family == ColumnFamily::Flow
                && key.starts_with(&prefix)
                && key.get(prefix.len() + 8) == Some(&(RunTable::HistoryRecord as u8));
            if !is_record_metadata {
                continue;
            }
            let metadata = HistoryRowMetadata::decode(value.as_slice()).expect("row decodes");
            *value = HistoryRowMetadata {
                last_assigned_event_id: 0,
                max_event_id: 0,
                ..metadata
            }
            .encode_to_vec();
        }
    }
}

/// Settings whose window prunes before the migration: an eight-event window pruned one event at a
/// time, a checkpoint every two commands, and segments of about two records.
fn legacy_settings() -> RunSettings {
    RunSettings {
        history_window_events: 8,
        history_prune_chunk_events: 1,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 20,
        checkpoint_command_threshold: 2,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: 1_500,
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

fn sealed_effect_count(outcome: &ApplyOutcome) -> usize {
    outcome
        .effects
        .iter()
        .filter(|effect| matches!(effect, ApplyEffect::HistorySegmentSealed { .. }))
        .count()
}

fn requests_purge(outcome: &ApplyOutcome) -> bool {
    outcome
        .effects
        .iter()
        .any(|effect| matches!(effect, ApplyEffect::RunPurgeRequested { .. }))
}

/// Asserts that the segments are contiguous from seq 1 and event 1 up to the open segment, that
/// U names the last uploaded one, and that each lists every value superseded inside it.
fn assert_segment_chain(harness: &Harness) {
    let record = harness.run_record();
    let mut next_seq = 1;
    let mut next_event_id = 1;
    for segment in harness.segments() {
        assert_eq!(segment.first_seq, next_seq, "{segment:?}");
        assert!(segment.first_seq <= segment.last_seq, "{segment:?}");
        assert_eq!(segment.first_event_id, next_event_id, "{segment:?}");
        assert_segment_state(harness, &segment, &record);
        next_seq = segment.last_seq + 1;
        next_event_id = segment.last_event_id + 1;
    }
    assert_eq!(record.history_open_segment_first_seq, next_seq);
    assert_eq!(record.history_open_segment_first_event_id, next_event_id);
}

fn assert_segment_state(harness: &Harness, segment: &HistorySegmentRecord, record: &RunRecord) {
    let state = if segment.last_seq <= record.history_uploaded_through_seq {
        HistorySegmentState::Uploaded
    } else {
        HistorySegmentState::Sealed
    };
    assert_eq!(segment.state, state as i32, "{segment:?}");
    let values = harness.segment_values(segment.first_seq);
    assert_eq!(segment.superseded_value_count, values.len() as u64);
    assert!(
        values
            .values()
            .all(|row| (segment.first_seq..=segment.last_seq).contains(&row.superseded_seq))
    );
    if state == HistorySegmentState::Sealed {
        let content = harness.segment_content(segment.first_seq);
        let stored: Vec<u64> = harness
            .stored_record_seqs()
            .into_iter()
            .filter(|seq| (segment.first_seq..=segment.last_seq).contains(seq))
            .collect();
        let held: Vec<u64> = content.records.iter().map(|row| row.seq).collect();
        assert_eq!(held, stored, "a SEALED segment holds its stored records");
        assert_eq!(content.values.len(), values.len());
    }
}

/// Asserts that a history read returns exactly the stored events and reports every other event
/// ID since event 1 as pruned.
fn assert_history_reads_match_stored_events(harness: &Harness) {
    let page = harness.history_page();
    let returned: BTreeSet<i64> = page.events.iter().map(|event| event.event_id).collect();
    assert_eq!(returned, harness.stored_event_ids());
    let pruned = page
        .pruned_event_id_range
        .map_or(0..=0, |range| range.start_event_id..=range.end_event_id);
    let next_event_id = harness.run_record().next_event_id;
    for event_id in 1..next_event_id {
        assert_ne!(
            returned.contains(&event_id),
            pruned.contains(&event_id),
            "event {event_id} is returned or reported pruned, never both or neither"
        );
    }
}

/// Seqs of history records that window pruning deleted, between genesis and the newest one.
fn pruned_record_seqs(harness: &Harness) -> BTreeSet<u64> {
    let stored: BTreeSet<u64> = harness.stored_record_seqs().into_iter().collect();
    (1..=harness.last_seq())
        .filter(|seq| !stored.contains(seq))
        .collect()
}

#[test]
fn a_running_legacy_run_migrates_one_segment_per_command_until_caught_up() {
    let mut harness = Harness::legacy(legacy_settings(), 30);
    let pruned_before = pruned_record_seqs(&harness);
    assert!(
        !pruned_before.is_empty(),
        "the window pruned before offload"
    );
    assert!(!pruned_before.contains(&1), "FlowStart stays");
    let pruned_range = harness
        .history_page()
        .pruned_event_id_range
        .expect("pruned events are reported");
    assert_history_reads_match_stored_events(&harness);

    harness.set_offload_mode(true);
    let first = harness.apply_round("migrate0");
    let record = harness.run_record();
    assert_eq!(sealed_effect_count(&first), 1);
    assert_eq!(record.history_migration_started_seq, harness.last_seq());
    assert_eq!(record.history_open_segment_bytes, 0);
    assert_segment_chain(&harness);
    let commands = 1 + harness.migrate_to_completion("migrate");
    assert!(commands >= 3, "the legacy history spans several segments");

    let segments = harness.segments();
    let (last, migrated) = segments
        .split_last()
        .expect("the migration sealed segments");
    assert_eq!(
        last.last_seq,
        harness.last_seq(),
        "it ends at the catching-up command"
    );
    for segment in migrated {
        let content = harness.segment_content(segment.first_seq);
        assert!(content.records.len() >= 2, "{segment:?} holds two records");
        assert!(
            content
                .records
                .iter()
                .all(|row| !pruned_before.contains(&row.seq)),
            "pruned records stay holes"
        );
    }
    assert_eq!(
        harness.history_page().pruned_event_id_range,
        Some(pruned_range),
        "the migration deletes nothing, so earlier pruned events stay reported as pruned"
    );
    assert_history_reads_match_stored_events(&harness);

    let after = harness.apply_round("after0");
    let record = harness.run_record();
    assert_eq!(record.history_migration_started_seq, 0);
    assert_eq!(
        sealed_effect_count(&after),
        0,
        "the open segment follows its bytes"
    );
    assert!(record.history_open_segment_bytes > 0);

    for round in 1..12 {
        harness.acknowledge_every_sealed();
        harness.apply_round(&format!("after{round}"));
        assert_history_reads_match_stored_events(&harness);
    }
    let pruned_after = harness
        .history_page()
        .pruned_event_id_range
        .expect("pruning continues after uploads");
    assert!(pruned_after.end_event_id > pruned_range.end_event_id);
}

#[test]
fn a_run_started_while_offload_was_off_migrates_once_it_is_on() {
    let mut harness = Harness::new(legacy_settings());
    harness.set_offload_mode(true);
    harness.set_offload_mode(false);
    harness.start();
    for round in 0..20 {
        harness.apply_round(&format!("off{round}"));
    }
    assert_eq!(harness.run_record().history_open_segment_first_seq, 0);
    assert!(harness.segments().is_empty());

    harness.set_offload_mode(true);
    harness.migrate_to_completion("on");
    assert_eq!(harness.segments()[0].first_seq, 1);
    harness.acknowledge_every_sealed();
    assert_segment_chain(&harness);
    assert_history_reads_match_stored_events(&harness);
}

#[test]
fn a_closed_legacy_run_is_not_migrated_and_its_purge_deletes_its_history() {
    let mut harness = Harness::legacy(legacy_settings(), 12);
    harness.stop();
    harness.set_offload_mode(true);

    let fired = harness.fire_retention();
    assert_eq!(sealed_effect_count(&fired), 0);
    assert!(requests_purge(&fired), "nothing waits for an upload");
    let record = harness.run_record();
    assert_eq!(record.history_open_segment_first_seq, 0);
    assert_eq!(record.history_migration_started_seq, 0);
    assert!(harness.segments().is_empty());
    assert!(!harness.stored_record_seqs().is_empty());

    let purge = harness.apply(Command::RunPurge(RunPurgeCommand {}));
    assert!(is_ok(&purge.reply), "{:?}", purge.reply);
    assert_every_run_row_purged(&harness);
}

#[test]
fn a_run_that_closes_while_migrating_seals_once_more_then_keeps_the_rest_local() {
    let mut harness = Harness::legacy(legacy_settings(), 30);
    harness.set_offload_mode(true);
    harness.apply_round("migrate0");
    let stop = harness.stop();
    assert_eq!(
        sealed_effect_count(&stop),
        1,
        "the closing command still migrates"
    );
    let record = harness.run_record();
    assert_ne!(
        record.history_migration_started_seq, 0,
        "the history is too long to catch up by the close"
    );
    let unsealed_from = record.history_open_segment_first_seq;
    assert_segment_chain(&harness);

    let postponed = harness.fire_retention();
    assert_eq!(
        sealed_effect_count(&postponed),
        0,
        "a closed run stops migrating"
    );
    assert!(
        !requests_purge(&postponed),
        "retention waits for the uploads"
    );
    assert_eq!(
        harness.run_record().history_open_segment_first_seq,
        unsealed_from
    );
    assert!(
        harness
            .stored_record_seqs()
            .iter()
            .any(|seq| *seq >= unsealed_from),
        "the rest stays local"
    );

    harness.acknowledge_every_sealed();
    let fired = harness.fire_retention();
    assert_eq!(sealed_effect_count(&fired), 0);
    assert!(requests_purge(&fired));
    let purge = harness.apply(Command::RunPurge(RunPurgeCommand {}));
    assert!(is_ok(&purge.reply), "{:?}", purge.reply);
    assert_every_run_row_purged(&harness);
}

#[test]
fn time_travel_during_a_migration_restores_its_target_and_the_migration_continues() {
    let mut harness = Harness::legacy(legacy_settings(), 30);
    harness.set_offload_mode(true);
    harness.apply_round("migrate0");
    assert_ne!(harness.run_record().history_migration_started_seq, 0);
    let target_seq = harness.last_seq();
    assert_eq!(harness.attribute_value_id("profile").run_seq, target_seq);
    harness.apply_round("migrate1");
    harness.apply_round("migrate2");

    let travel = harness.travel_to_seq(target_seq);
    let travel_seq = harness.last_seq();
    assert_eq!(
        sealed_effect_count(&travel),
        1,
        "the time travel migrates too"
    );
    let record = harness.run_record();
    assert_eq!(record.current_attempt, 2);
    assert_eq!(harness.attribute_value_id("profile").run_seq, target_seq);
    assert_segment_chain(&harness);

    harness.migrate_to_completion("travelled");
    let segment = harness
        .segments()
        .into_iter()
        .find(|segment| (segment.first_seq..=segment.last_seq).contains(&travel_seq))
        .expect("a segment holds the time travel");
    let content = harness.segment_content(segment.first_seq);
    assert!(content.records.iter().any(|row| row.seq == travel_seq));
    assert!(
        content
            .checkpoints
            .iter()
            .any(|row| row.seq == travel_seq && row.attempt == 2),
        "the new attempt's start checkpoint is offloaded with its command"
    );
    harness.acknowledge_every_sealed();
    assert_segment_chain(&harness);
}

#[test]
fn migration_event_ranges_do_not_need_last_assigned_event_ids() {
    let mut intact = Harness::legacy(legacy_settings(), 30);
    let mut older = Harness::legacy(legacy_settings(), 30);
    older.clear_metadata_event_ids();
    for harness in [&mut intact, &mut older] {
        harness.set_offload_mode(true);
        harness.apply_round("migrate0");
        harness.migrate_to_completion("migrate");
    }
    let ranges = |harness: &Harness| {
        harness
            .segments()
            .into_iter()
            .map(|segment| {
                (
                    segment.first_seq,
                    segment.last_seq,
                    segment.first_event_id,
                    segment.last_event_id,
                )
            })
            .collect::<Vec<_>>()
    };
    assert!(ranges(&intact).len() >= 3);
    assert_eq!(ranges(&older), ranges(&intact));
}

#[test]
fn commands_larger_than_the_target_still_let_the_migration_catch_up() {
    let mut harness = Harness::legacy(
        RunSettings {
            history_window_events: 1_000,
            history_segment_target_bytes: 100,
            ..legacy_settings()
        },
        16,
    );
    harness.set_offload_mode(true);
    harness.apply_round("migrate0");
    let commands = 1 + harness.migrate_to_completion("migrate");
    assert!(commands <= 17, "each command seals at least two records");
    let segments = harness.segments();
    let (_, migrated) = segments.split_last().expect("segments");
    for segment in migrated {
        assert_eq!(
            harness.segment_content(segment.first_seq).records.len(),
            2,
            "{segment:?} ends at its second record"
        );
    }
}

#[test]
fn the_superseded_value_limit_cuts_migration_segments() {
    let keys: Vec<String> = (0..6_000).map(|index| format!("item-{index}")).collect();
    let mut harness = Harness::new(RunSettings {
        history_window_events: 1_000,
        history_segment_target_bytes: 1 << 30,
        ..legacy_settings()
    });
    harness.start();
    for round in 0..4 {
        let values: Vec<(String, String)> = keys
            .iter()
            .map(|key| (key.clone(), large_text(&format!("{key}-{round}"), 110)))
            .collect();
        harness.set_attributes(&values);
    }
    let superseding_seqs: Vec<u64> = (3..=harness.last_seq()).collect();
    harness.set_offload_mode(true);
    harness.apply_round("migrate0");
    harness.migrate_to_completion("migrate");

    let segments = harness.segments();
    assert!(segments.len() >= 3, "{segments:?}");
    let first = &segments[0];
    assert_eq!(
        first.last_seq, superseding_seqs[0],
        "the limit cuts after 6,000 values"
    );
    assert_eq!(first.superseded_value_count, 6_000);
    let second = &segments[1];
    assert_eq!(
        (second.first_seq, second.last_seq),
        (superseding_seqs[1], superseding_seqs[2]),
        "a segment ends at the seq that reaches the limit"
    );
    assert_eq!(second.superseded_value_count, 12_000);
    assert_segment_chain(&harness);
}

#[test]
fn a_legacy_seq_past_the_superseded_value_limit_migrates_in_one_step() {
    let keys: Vec<String> = (0..12_000).map(|index| format!("item-{index}")).collect();
    let mut harness = Harness::new(RunSettings {
        history_window_events: 1_000,
        history_segment_target_bytes: 1 << 30,
        ..legacy_settings()
    });
    harness.start();
    for round in 0..2 {
        let values: Vec<(String, String)> = keys
            .iter()
            .map(|key| (key.clone(), large_text(&format!("{key}-{round}"), 110)))
            .collect();
        harness.set_attributes(&values);
    }
    let superseding_seq = harness.last_seq();
    let superseded: BTreeSet<ValueId> = harness
        .table_rows::<dexzd_protocol::dexzd::v1::ValueGarbageRecord>(RunTable::ValueGarbage)
        .into_keys()
        .filter(|suffix| suffix[..8] == superseding_seq.to_be_bytes())
        .map(|suffix| ValueId::from_key_suffix(&suffix[8..]).expect("value ID suffix"))
        .collect();
    assert_eq!(superseded.len(), 12_000);

    harness.set_offload_mode(true);
    harness.apply_round("migrate0");
    assert!(
        harness
            .segments()
            .iter()
            .all(|segment| segment.last_seq < superseding_seq),
        "the first step stops before the seq past the limit"
    );
    let step = harness.apply_round("migrate1");
    assert_eq!(
        sealed_effect_count(&step),
        1,
        "one step seals the whole seq"
    );
    let segment = harness
        .segments()
        .into_iter()
        .find(|segment| (segment.first_seq..=segment.last_seq).contains(&superseding_seq))
        .expect("a segment holds the seq");
    assert_eq!(segment.first_seq, superseding_seq);
    let listed = harness.segment_values(segment.first_seq);
    let listed_at_seq: BTreeSet<ValueId> = listed
        .iter()
        .filter(|(_, row)| row.superseded_seq == superseding_seq)
        .map(|(value_id, _)| *value_id)
        .collect();
    assert_eq!(
        listed_at_seq, superseded,
        "every value of the seq is listed"
    );
    assert_eq!(segment.superseded_value_count, listed.len() as u64);
    assert_eq!(
        harness.segment_content(segment.first_seq).values.len(),
        listed.len()
    );
    harness.migrate_to_completion("migrate");
    assert_segment_chain(&harness);
}

#[test]
fn turning_offload_off_ends_a_migration_and_its_rest_becomes_a_gap() {
    let mut harness = Harness::legacy(legacy_settings(), 30);
    harness.set_offload_mode(true);
    harness.apply_round("migrate0");
    let record = harness.run_record();
    assert_ne!(record.history_migration_started_seq, 0);
    let gap_first_seq = record.history_open_segment_first_seq;
    harness.acknowledge_every_sealed();
    let migrated = harness.segments();

    harness.set_offload_mode(false);
    let off = harness.apply_round("off0");
    assert_eq!(sealed_effect_count(&off), 0);
    let record = harness.run_record();
    assert_eq!(
        record.history_migration_started_seq, 0,
        "the migration ended"
    );
    assert_eq!(
        record.history_open_segment_first_seq,
        harness.last_seq() + 1
    );
    for round in 1..14 {
        harness.apply_round(&format!("off{round}"));
    }
    let gap_last_seq = harness.last_seq();
    let gap_last_event_id = harness.run_record().next_event_id - 1;
    let gap = gap_first_seq..=gap_last_seq;
    assert!(
        pruned_record_seqs(&harness)
            .iter()
            .any(|seq| gap.contains(seq)),
        "pruning while offload is off deleted rows of the gap"
    );
    assert_history_reads_match_stored_events(&harness);

    harness.set_offload_mode(true);
    assert_gap_is_skipped(&mut harness, &migrated, &gap, gap_last_event_id);
}

#[test]
fn reenabling_offload_skips_the_gap_and_reads_report_its_pruned_events() {
    let mut harness = Harness::new(legacy_settings());
    harness.set_offload_mode(true);
    harness.start();
    for round in 0..12 {
        harness.apply_round(&format!("on{round}"));
        harness.acknowledge_every_sealed();
    }
    let sealed = harness.segments();
    assert!(!sealed.is_empty());
    let gap_first_seq = harness.run_record().history_open_segment_first_seq;

    harness.set_offload_mode(false);
    for round in 0..14 {
        harness.apply_round(&format!("off{round}"));
    }
    let gap_last_seq = harness.last_seq();
    let gap_last_event_id = harness.run_record().next_event_id - 1;
    let gap = gap_first_seq..=gap_last_seq;
    assert!(
        pruned_record_seqs(&harness)
            .iter()
            .any(|seq| gap.contains(seq)),
        "pruning while offload is off deleted rows of the gap"
    );

    harness.set_offload_mode(true);
    assert_gap_is_skipped(&mut harness, &sealed, &gap, gap_last_event_id);
}

/// With offload on again after the gap, seals segments until one follows the gap, then asserts
/// that no segment holds a gap seq and that reads stay consistent while the window passes it.
fn assert_gap_is_skipped(
    harness: &mut Harness,
    before_gap: &[HistorySegmentRecord],
    gap: &std::ops::RangeInclusive<u64>,
    gap_last_event_id: i64,
) {
    let mut round = 0;
    while harness.segments().len() == before_gap.len() {
        harness.apply_round(&format!("again{round}"));
        assert_history_reads_match_stored_events(harness);
        round += 1;
        assert!(round < 20, "a segment seals after offload is on again");
    }
    let segments = harness.segments();
    assert_eq!(&segments[..before_gap.len()], before_gap);
    let reopened = &segments[before_gap.len()];
    assert_eq!(reopened.first_seq, gap.end() + 1);
    assert_eq!(reopened.first_event_id, gap_last_event_id + 1);
    // Content stays inside a segment's seqs, so disjoint seqs keep gap rows out of every object.
    for segment in &segments {
        assert!(segment.last_seq < *gap.start() || segment.first_seq > *gap.end());
        assert!(
            harness
                .segment_values(segment.first_seq)
                .values()
                .all(|row| !gap.contains(&row.superseded_seq))
        );
    }
    let reopened_content = harness.segment_content(reopened.first_seq);
    assert!(
        reopened_content
            .records
            .iter()
            .all(|row| !gap.contains(&row.seq))
    );
    assert!(
        reopened_content
            .checkpoints
            .iter()
            .all(|row| !gap.contains(&row.seq))
    );
    assert!(
        harness
            .stored_record_seqs()
            .iter()
            .any(|seq| gap.contains(seq))
    );
    for round in 0..30 {
        harness.acknowledge_every_sealed();
        harness.apply_round(&format!("passing{round}"));
        assert_history_reads_match_stored_events(harness);
    }
    let record = harness.run_record();
    assert!(record.history_uploaded_through_seq > *gap.end());
    assert!(
        record.window_start_seq > *gap.end(),
        "the window passed the gap"
    );
    let pruned = harness
        .history_page()
        .pruned_event_id_range
        .expect("pruned events are reported");
    assert!(pruned.end_event_id >= gap_last_event_id);
    assert!(
        harness
            .stored_record_seqs()
            .iter()
            .all(|seq| *seq == 1 || !gap.contains(seq)),
        "the window rule pruned the gap"
    );
}

fn assert_every_run_row_purged(harness: &Harness) {
    assert!(
        harness
            .state
            .rows
            .keys()
            .all(
                |(column_family, key)| *column_family != ColumnFamily::PartitionIndex
                    || key.get(2) != Some(&(IndexKind::HistoryUploadPending as u8))
            )
    );
    let run_prefix = run_prefix(harness.context.flow_hash, harness.run());
    assert!(
        harness
            .state
            .rows
            .keys()
            .all(
                |(column_family, key)| *column_family == ColumnFamily::PartitionIndex
                    || !key.starts_with(&run_prefix)
            ),
        "the purge deleted every row of the run"
    );
}
