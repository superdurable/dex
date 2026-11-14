// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Prune cursors bound the rows each command's window and garbage pruning visits.
//!
//! Pruning from the cursors deletes exactly the rows that full scans delete.

#[path = "support/prune_cursors.rs"]
mod prune_cursors;

use std::cell::Cell;
use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::read_model::{get_history_events, is_history_event_available};
use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_time_travel_for_test,
};
use dexzd_keyspace::row_keys::{prefix_range, run_singleton_key};
use dexzd_keyspace::{
    ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, ValueId, run_table_prefix,
};
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowResetType, FlowTimeoutPolicy, HistoryEventIdRange,
    InvokeRpcRequest, InvokeWorkerRpcResponse, ResetFlowRequest, SetAttributesRequest,
    StartFlowRequest, StepOptions, UpdateFlowConfigRequest, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, CommandReply, FlowCommand, FlowConfigUpdateCommand, FlowStartCommand,
    HistoryRecord, RpcCommittedCommand, RunRecord, RunSettings, TimeTravelCommand, command_reply,
    flow_command::Command, stored_value,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const FLOW_ID: &str = "prune-flow";
/// Rows one command's window pruning or garbage pruning deletes at most.
const ROWS_PER_PRUNE: u64 = 128;

/// Rows a scan returned or skipped as deleted, by table, during one command.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
struct ScanVisits {
    history_metadata: u64,
    value_garbage: u64,
}

/// Stored rows plus the keys deleted since their last write, which scans step over like tombstones.
struct TombstoneState {
    rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
    deleted: BTreeSet<(ColumnFamily, Vec<u8>)>,
    history_metadata_prefix: Vec<u8>,
    value_garbage_prefix: Vec<u8>,
    visits: Cell<ScanVisits>,
}

impl FlowStateReader for TombstoneState {
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
        let returned: Vec<StateRow> = self
            .rows
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect();
        // A scan that filled its limit stops at its last row; otherwise it reached `end`.
        let visited_end = match returned.last() {
            Some((last_key, _)) if returned.len() == limit => last_key.clone(),
            _ => end.to_vec(),
        };
        let skipped = self
            .deleted
            .range((column_family, start.to_vec())..(column_family, visited_end))
            .count();
        let visited = u64::try_from(returned.len() + skipped).expect("small row counts");
        let mut visits = self.visits.get();
        if column_family == ColumnFamily::Flow && start.starts_with(&self.history_metadata_prefix) {
            visits.history_metadata += visited;
        } else if column_family == ColumnFamily::Flow
            && start.starts_with(&self.value_garbage_prefix)
        {
            visits.value_garbage += visited;
        }
        self.visits.set(visits);
        Ok(returned)
    }
}

impl TombstoneState {
    fn commit(&mut self, outcome: &ApplyOutcome) {
        for write in &outcome.writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    let row_key = (*column_family, key.clone());
                    self.deleted.remove(&row_key);
                    self.rows.insert(row_key, value.clone());
                }
                StateWrite::Delete { column_family, key }
                | StateWrite::SingleDelete { column_family, key } => {
                    let row_key = (*column_family, key.clone());
                    self.rows.remove(&row_key);
                    self.deleted.insert(row_key);
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => {
                    let removed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(row_key, _)| row_key.clone())
                        .collect();
                    for row_key in removed {
                        self.rows.remove(&row_key);
                        self.deleted.insert(row_key);
                    }
                }
            }
        }
    }
}

struct Harness {
    state: TombstoneState,
    context: ApplyContext,
    run_id: RunId,
    now_micros: i64,
    next_request: u64,
}

impl Harness {
    /// Starts a run whose first Step waits in WaitFor, so Attribute writes age its history.
    fn start(settings: RunSettings) -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, FLOW_ID);
        let run_id = RunId([9; 16]);
        let mut harness = Self {
            state: TombstoneState {
                rows: BTreeMap::new(),
                deleted: BTreeSet::new(),
                history_metadata_prefix: run_table_prefix(
                    flow_hash,
                    run_id,
                    RunTable::HistoryRowMetadata,
                ),
                value_garbage_prefix: run_table_prefix(flow_hash, run_id, RunTable::ValueGarbage),
                visits: Cell::new(ScanVisits::default()),
            },
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            run_id,
            now_micros: 1_800_000_000 * SECOND,
            next_request: 0,
        };
        let outcome = harness.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: run_id.as_bytes().to_vec(),
            request: Some(StartFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_type: "PruneFlow".to_owned(),
                start_step_type: "ReviewStep".to_owned(),
                step_input: Some(string_value("input")),
                step_options: Some(StepOptions::default()),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            }),
            settings: Some(settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget {
                is_pull_mode: true,
                ..WorkerTarget::default()
            }),
            effective_flow_config: Some(FlowConfig {
                worker_target: Some(WorkerTarget {
                    is_pull_mode: true,
                    ..WorkerTarget::default()
                }),
                ..FlowConfig::default()
            }),
            ..FlowStartCommand::default()
        }));
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::StartFlow(_))
            ),
            "FlowStart applies: {:?}",
            outcome.reply
        );
        harness
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        self.now_micros += SECOND;
        let is_start = matches!(body, Command::FlowStart(_));
        let command = FlowCommand {
            flow_id: FLOW_ID.to_owned(),
            run_id: if is_start {
                Vec::new()
            } else {
                self.run_id.as_bytes().to_vec()
            },
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: self.now_micros.to_be_bytes().to_vec(),
            tenant_id: 0,
            command: Some(body),
            ..FlowCommand::default()
        };
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
        self.state.visits.set(ScanVisits::default());
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("the command applies");
        self.state.commit(&outcome);
        prune_cursors::assert_prune_cursors_skip_no_rows(
            &self.state.rows,
            self.context.flow_hash,
            self.run_id,
        );
        outcome
    }

    /// Writes an Attribute larger than the inline threshold, superseding the previous value.
    ///
    /// Returns the visits of the command's scans.
    fn set_large_attribute(&mut self, request_id: Option<&str>) -> ScanVisits {
        self.next_request += 1;
        let request_id = request_id.map(str::to_owned).unwrap_or_default();
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: "status".to_owned(),
                    value: Some(string_value(&format!(
                        "v{}{}",
                        self.next_request,
                        "x".repeat(200)
                    ))),
                    ..AttributeWrite::default()
                }],
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }));
        assert!(is_ok(&outcome.reply), "SetAttributes applies");
        self.state.visits.get()
    }

    /// Commits an RPC whose output is larger than the inline threshold and returns its value.
    fn rpc_with_large_output(&mut self, request_id: &str) -> ValueId {
        let outcome = self.apply(Command::RpcCommitted(RpcCommittedCommand {
            request: Some(InvokeRpcRequest {
                flow_id: FLOW_ID.to_owned(),
                rpc_name: "updateReview".to_owned(),
                request_id: request_id.to_owned(),
                ..InvokeRpcRequest::default()
            }),
            response: Some(InvokeWorkerRpcResponse {
                output: Some(string_value(&"r".repeat(500))),
                ..InvokeWorkerRpcResponse::default()
            }),
            app_worker_id: "worker-rpc".to_owned(),
            request_fingerprint: request_id.as_bytes().to_vec(),
            ..RpcCommittedCommand::default()
        }));
        let Some(command_reply::Reply::Rpc(reply)) = &outcome.reply.reply else {
            panic!("the RPC commits: {:?}", outcome.reply);
        };
        let Some(stored_value::Kind::TableValue(reference)) = reply
            .output
            .as_ref()
            .and_then(|output| output.kind.as_ref())
        else {
            panic!("a large RPC output is a table value");
        };
        let value_id = reference.value_id.expect("a table value has an ID");
        ValueId {
            run_seq: value_id.run_seq,
            index_in_command: value_id.index_in_command,
        }
    }

    fn update_window(&mut self, history_window_events: i32) {
        let outcome = self.apply(Command::FlowConfigUpdate(FlowConfigUpdateCommand {
            request: Some(UpdateFlowConfigRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_config: Some(FlowConfig {
                    history_window_events: Some(history_window_events),
                    ..FlowConfig::default()
                }),
                ..UpdateFlowConfigRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "UpdateFlowConfig applies");
    }

    fn time_travel(&mut self, request: ResetFlowRequest) -> CommandReply {
        self.apply(Command::TimeTravel(TimeTravelCommand {
            staging_id: Vec::new(),
            computed_through_seq: 0,
            target_run_attempt: 0,
            target_run_seq: 0,
            should_restore_running_as_pending: false,
            request: Some(request),
        }))
        .reply
    }

    fn run_record(&self) -> RunRecord {
        RunRecord::decode(self.run_record_bytes().as_slice()).expect("the run record decodes")
    }

    fn run_record_bytes(&self) -> Vec<u8> {
        self.state
            .rows
            .get(&(ColumnFamily::Flow, self.run_record_key()))
            .expect("the run record exists")
            .clone()
    }

    fn run_record_key(&self) -> Vec<u8> {
        run_singleton_key(self.context.flow_hash, self.run_id, RunTable::RunRecord)
    }

    /// Clears both cursors, as in a run recorded before they existed.
    fn forget_prune_cursors(&mut self) {
        let mut record = self.run_record();
        record.history_metadata_pruned_before_seq = 0;
        record.value_garbage_pruned_before_seq = 0;
        let key = self.run_record_key();
        self.state
            .rows
            .insert((ColumnFamily::Flow, key), record.encode_to_vec());
    }

    /// Returns the History tables whose metadata rows the run holds at `seq`.
    fn metadata_tables_at(&self, seq: u64) -> Vec<RunTable> {
        let prefix = &self.state.history_metadata_prefix;
        let mut start = prefix.clone();
        start.extend_from_slice(&seq.to_be_bytes());
        let mut end = prefix.clone();
        end.extend_from_slice(&(seq + 1).to_be_bytes());
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|((_, key), _)| {
                RunTable::from_byte(key[prefix.len() + 8]).expect("a valid History table byte")
            })
            .collect()
    }

    fn has_value_row(&self, value_id: ValueId) -> bool {
        let key = dexzd_keyspace::row_keys::value_row_key(
            self.context.flow_hash,
            self.run_id,
            RunTable::Value,
            value_id,
        );
        self.state.rows.contains_key(&(ColumnFamily::Value, key))
    }

    fn history_records(&self) -> BTreeMap<u64, HistoryRecord> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run_id, RunTable::HistoryRecord);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::History, start)..(ColumnFamily::History, end))
            .map(|(_, bytes)| {
                let record = HistoryRecord::decode(bytes.as_slice()).expect("history decodes");
                (record.seq, record)
            })
            .collect()
    }

    /// Asserts that history shows FlowStart and then W onward, and that time travel reaches W.
    fn assert_history_reads_and_travels(&mut self) {
        let record = self.run_record();
        let response = get_history_events(
            &self.state,
            self.context.flow_hash,
            self.run_id,
            0,
            0,
            &[],
            0,
            usize::MAX,
        )
        .expect("history reads");
        let records = self.history_records();
        let window_start = &records[&record.window_start_seq];
        let window_start_event_id = window_start.events[0].event_id;
        assert_eq!(response.events[0].event_id, 1, "FlowStart stays visible");
        assert_eq!(response.events[1].event_id, window_start_event_id);
        assert_eq!(
            response.pruned_event_id_range,
            Some(HistoryEventIdRange {
                start_event_id: 2,
                end_event_id: window_start_event_id - 1,
            })
        );
        let available =
            is_history_event_available(&self.state, self.context.flow_hash, self.run_id, 2)
                .expect("availability reads");
        assert_eq!(available.available_internal_event_id, window_start_event_id);
        let reply = self.time_travel(reset_to_time(window_start.timestamp_micros));
        assert!(
            matches!(reply.reply, Some(command_reply::Reply::TimeTravel(_))),
            "travel to the earliest visible event applies: {reply:?}"
        );
    }
}

/// Returns settings whose window prunes a tenth at a time, with a checkpoint every 37 commands.
fn settings(history_window_events: u32, request_dedup_capacity: u32) -> RunSettings {
    RunSettings {
        history_window_events,
        history_prune_chunk_events: history_window_events / 10,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 30,
        checkpoint_command_threshold: 37,
        request_dedup_capacity,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: 4 << 20,
        delete_expired_history_objects: false,
        superseded_value_grace_micros: 0,
        offload_history: None,
    }
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

fn reset(reset_type: FlowResetType) -> ResetFlowRequest {
    ResetFlowRequest {
        flow_id: FLOW_ID.to_owned(),
        reset_type: reset_type as i32,
        reason: "test".to_owned(),
        skip_writes_reapply: true,
        ..ResetFlowRequest::default()
    }
}

fn reset_to_time(timestamp_micros: i64) -> ResetFlowRequest {
    ResetFlowRequest {
        history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
        ..reset(FlowResetType::HistoryEventTime)
    }
}

/// Returns the largest visits of `commands` Attribute writes, by table.
fn max_visits_over(harness: &mut Harness, commands: usize) -> ScanVisits {
    let mut max_visits = ScanVisits::default();
    for _ in 0..commands {
        let visits = harness.set_large_attribute(None);
        max_visits.history_metadata = max_visits.history_metadata.max(visits.history_metadata);
        max_visits.value_garbage = max_visits.value_garbage.max(visits.value_garbage);
    }
    max_visits
}

/// Applies command `index` of the workload that both runs of the differential test receive.
///
/// Returns the value of the RPC reply that command 0 stores.
fn apply_differential_command(harness: &mut Harness, index: usize) -> Option<ValueId> {
    match index {
        0 => return Some(harness.rpc_with_large_output("rpc-1")),
        // The second request ID evicts the RPC reply and releases its value.
        400 => {
            harness.set_large_attribute(Some("set-evicting"));
        }
        500 => harness.update_window(30),
        600 => harness.update_window(400),
        1100 => {
            let window_start_seq = harness.run_record().window_start_seq;
            let window_start_micros = harness.history_records()[&window_start_seq].timestamp_micros;
            let reply = harness.time_travel(reset_to_time(window_start_micros));
            assert!(
                matches!(reply.reply, Some(command_reply::Reply::TimeTravel(_))),
                "travel within the window applies: {reply:?}"
            );
        }
        1600 => {
            let reply = harness.time_travel(reset(FlowResetType::Beginning));
            assert!(
                matches!(reply.reply, Some(command_reply::Reply::TimeTravel(_))),
                "travel to the beginning applies: {reply:?}"
            );
        }
        _ => {
            harness.set_large_attribute(None);
        }
    }
    None
}

/// Returns the run's rows with both cursors and the RunRecord's own size cleared from its record,
/// so runs that differ only in their cursors compare equal.
fn rows_without_cursors(harness: &Harness) -> BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>> {
    let mut rows = harness.state.rows.clone();
    let mut record = harness.run_record();
    record.history_metadata_pruned_before_seq = 0;
    record.value_garbage_pruned_before_seq = 0;
    // The cursors are part of the RunRecord row, which state row bytes count.
    if let Some(storage) = record.storage.as_mut() {
        storage.state_row_bytes = 0;
    }
    rows.insert(
        (ColumnFamily::Flow, harness.run_record_key()),
        record.encode_to_vec(),
    );
    rows
}

#[test]
fn prune_scans_visit_a_bounded_number_of_rows_as_the_run_ages() {
    // A window of 200 events prunes 20 at a time, so deleted rows pile up behind it.
    let mut harness = Harness::start(settings(200, 1000));
    for _ in 0..1000 {
        harness.set_large_attribute(None);
    }
    let young = max_visits_over(&mut harness, 200);
    for _ in 0..19_000 {
        harness.set_large_attribute(None);
    }
    let old = max_visits_over(&mut harness, 200);
    println!("max scan visits per command: at 1k commands {young:?}, at 20k commands {old:?}");
    let deleted_metadata = harness
        .state
        .deleted
        .iter()
        .filter(|(_, key)| key.starts_with(&harness.state.history_metadata_prefix))
        .count();
    assert!(
        deleted_metadata > 19_000,
        "pruning left {deleted_metadata} deleted metadata rows behind the window"
    );
    // Window pruning and the window advance each visit at most one page of metadata.
    for visits in [young, old] {
        assert!(
            visits.history_metadata <= 3 * ROWS_PER_PRUNE,
            "metadata visits stay bounded: {visits:?}"
        );
        assert!(
            visits.value_garbage <= ROWS_PER_PRUNE,
            "garbage visits stay bounded: {visits:?}"
        );
    }
    assert!(
        old.history_metadata <= young.history_metadata + ROWS_PER_PRUNE / 4
            && old.value_garbage <= young.value_garbage + ROWS_PER_PRUNE / 4,
        "an old run visits no more rows than a young one: {young:?} then {old:?}"
    );
}

#[test]
fn a_run_records_no_cursor_until_its_window_passes_the_first_checkpoint() {
    let mut harness = Harness::start(settings(200, 1000));
    for _ in 0..200 {
        harness.set_large_attribute(None);
    }
    let record = harness.run_record();
    assert_eq!(record.window_start_seq, 1, "the window has not moved");
    assert_eq!(record.history_metadata_pruned_before_seq, 0);
    assert_eq!(record.value_garbage_pruned_before_seq, 0);
    while harness.run_record().history_metadata_pruned_before_seq == 0 {
        harness.set_large_attribute(None);
        assert!(harness.run_record().next_run_seq < 400, "pruning starts");
    }
    let record = harness.run_record();
    assert!(record.history_metadata_pruned_before_seq > 2);
    assert!(record.value_garbage_pruned_before_seq > 1);
    let records = harness.history_records();
    assert!(records.contains_key(&1), "pruning keeps the genesis record");
}

#[test]
fn a_run_without_cursors_prunes_the_same_rows_as_one_with_them() {
    // Clearing the cursors before each command makes every scan start where it did before them.
    let mut with_cursors = Harness::start(settings(100, 1));
    let mut without_cursors = Harness::start(settings(100, 1));
    let mut evicted_value = None;
    for index in 0..2000 {
        without_cursors.forget_prune_cursors();
        let with_value = apply_differential_command(&mut with_cursors, index);
        let without_value = apply_differential_command(&mut without_cursors, index);
        assert_eq!(with_value, without_value);
        evicted_value = evicted_value.or(with_value);
        assert_eq!(
            rows_without_cursors(&with_cursors),
            rows_without_cursors(&without_cursors),
            "both runs hold the same rows after command {index}"
        );
    }
    // The eviction released the reply at its own seq, which the window passed later.
    let value_id = evicted_value.expect("the RPC reply is a table value");
    assert!(
        !with_cursors.has_value_row(value_id),
        "the evicted reply is collected"
    );
    let record = with_cursors.run_record();
    assert_eq!(record.current_attempt, 3);
    // The grown window of 400 events trails the 2000 commands; checkpoints come every 37.
    assert!(record.history_metadata_pruned_before_seq > record.window_start_seq - 37);
    assert_eq!(
        record.value_garbage_pruned_before_seq,
        record.window_start_seq
    );
    assert!(record.window_start_seq > 1500);
}

#[test]
fn a_full_page_ending_between_the_rows_of_a_seq_keeps_that_seq_for_the_next_scan() {
    // Checkpoints every 42 commands make row 128 after genesis the record of a checkpoint seq.
    let mut harness = Harness::start(RunSettings {
        checkpoint_command_threshold: 42,
        ..settings(1000, 1000)
    });
    for _ in 0..400 {
        harness.set_large_attribute(None);
    }
    assert_eq!(harness.run_record().history_metadata_pruned_before_seq, 0);
    // The shrunk window moves W hundreds of seqs at once, so pruning fills whole pages.
    harness.update_window(10);
    harness.set_large_attribute(None);
    let split_seq = harness.run_record().history_metadata_pruned_before_seq;
    assert_eq!(
        harness.metadata_tables_at(split_seq),
        vec![RunTable::Checkpoint],
        "the full page deleted the record at {split_seq} but not its checkpoint"
    );
    harness.set_large_attribute(None);
    assert!(
        harness.metadata_tables_at(split_seq).is_empty(),
        "the next scan starts at {split_seq} and deletes the checkpoint"
    );
    assert!(harness.run_record().history_metadata_pruned_before_seq > split_seq);
}

#[test]
fn shrinking_and_growing_the_window_keep_the_cursors_behind_the_window() {
    let mut harness = Harness::start(settings(100, 1000));
    for _ in 0..300 {
        harness.set_large_attribute(None);
    }
    let before_shrink = harness.run_record();
    harness.update_window(10);
    for _ in 0..50 {
        harness.set_large_attribute(None);
    }
    let after_shrink = harness.run_record();
    assert!(after_shrink.window_start_seq > before_shrink.window_start_seq + 50);
    assert!(
        after_shrink.history_metadata_pruned_before_seq
            > before_shrink.history_metadata_pruned_before_seq
    );
    assert!(after_shrink.history_metadata_pruned_before_seq <= after_shrink.window_start_seq);
    assert!(after_shrink.value_garbage_pruned_before_seq <= after_shrink.window_start_seq);
    harness.update_window(1000);
    for _ in 0..200 {
        harness.set_large_attribute(None);
    }
    let after_grow = harness.run_record();
    assert_eq!(
        after_grow.window_start_seq, after_shrink.window_start_seq,
        "a grown window stops moving"
    );
    assert_eq!(
        after_grow.history_metadata_pruned_before_seq,
        after_shrink.history_metadata_pruned_before_seq
    );
    assert_eq!(
        after_grow.value_garbage_pruned_before_seq,
        after_shrink.value_garbage_pruned_before_seq
    );
    harness.assert_history_reads_and_travels();
}

#[test]
fn history_reads_and_time_travel_work_after_many_prunes_across_attempts() {
    let mut harness = Harness::start(settings(100, 1000));
    for _ in 0..2000 {
        harness.set_large_attribute(None);
    }
    harness.assert_history_reads_and_travels();
    assert_eq!(harness.run_record().current_attempt, 2);
    // The window moves into attempt 2, whose checkpoints now rebuild it.
    for _ in 0..500 {
        harness.set_large_attribute(None);
    }
    let record = harness.run_record();
    let window_start = &harness.history_records()[&record.window_start_seq];
    assert_eq!(window_start.attempt, 2);
    harness.assert_history_reads_and_travels();
    assert_eq!(harness.run_record().current_attempt, 3);
}

#[test]
fn a_run_recorded_before_the_cursors_starts_them_at_its_next_command() {
    let mut harness = Harness::start(settings(100, 1000));
    for _ in 0..500 {
        harness.set_large_attribute(None);
    }
    harness.forget_prune_cursors();
    // The first command scans from genesis, as before, and records how far it pruned.
    let first = harness.set_large_attribute(None);
    let record = harness.run_record();
    assert!(record.history_metadata_pruned_before_seq > 300);
    assert!(record.value_garbage_pruned_before_seq > 300);
    assert!(
        first.history_metadata > 300,
        "the first scan steps over every deleted row"
    );
    let next = harness.set_large_attribute(None);
    assert!(next.history_metadata <= 3 * ROWS_PER_PRUNE, "{next:?}");
    harness.assert_history_reads_and_travels();
}
