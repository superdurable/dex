// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The time travel window counted in history events, with per-run window sizes.

use std::collections::BTreeMap;

use dexzd_apply::read_model::get_history_events;
use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{prefix_range, run_singleton_key};
use dexzd_keyspace::{
    ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, parse_run_key, run_table_prefix,
};
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowResetType, FlowTimeoutPolicy, ResetFlowRequest,
    SetAttributesRequest, StartFlowRequest, StepDurability, StepOptions, UpdateFlowConfigRequest,
    Value, WorkerErrorResponse, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, CommandReply, FlowCommand, FlowConfigUpdateCommand, FlowStartCommand,
    HistoryRecord, HistoryRowMetadata, RetryBackoffTimerId, RunRecord, RunSettings,
    StepAttemptFailedCommand, StepAttemptStartedCommand, StepInvocationKey, StepMethod,
    TimeTravelCommand, TimerId, TimersFiredCommand, command_reply, flow_command::Command, timer_id,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const FLOW_ID: &str = "order-1";
const FAILED_PRECONDITION: i32 = 9;

#[derive(Default)]
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
                    let removed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in removed {
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
    run_id: RunId,
    now_micros: i64,
    next_request: u64,
}

impl Harness {
    /// Starts a run whose first Step waits in WaitFor; `flow_config` is its effective FlowConfig.
    fn start(settings: RunSettings, flow_config: FlowConfig) -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, FLOW_ID);
        let mut harness = Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            run_id: RunId([9; 16]),
            now_micros: 1_800_000_000 * SECOND,
            next_request: 0,
        };
        let outcome = harness.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: harness.run_id.as_bytes().to_vec(),
            request: Some(StartFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_type: "OrderFlow".to_owned(),
                start_step_type: "ChargeStep".to_owned(),
                step_input: Some(string_value("input")),
                step_options: Some(StepOptions::default()),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            }),
            settings: Some(settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            effective_flow_config: Some(flow_config),
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
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("the command applies");
        self.state.commit(&outcome);
        outcome
    }

    /// Writes one small Attribute: an external input that records one history event.
    fn set_attribute(&mut self) {
        self.next_request += 1;
        let request_id = format!("set-{}", self.next_request);
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: "status".to_owned(),
                    value: Some(string_value(&format!("v{}", self.next_request))),
                    ..AttributeWrite::default()
                }],
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }));
        assert!(is_ok(&outcome.reply), "SetAttributes applies");
    }

    /// Starts and fails the WaitFor attempt, then fires its retry timer.
    fn fail_and_retry_wait_for(&mut self, attempt: u32) {
        let started = self.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(wait_for_key()),
            attempt,
            task_id: vec![u8::try_from(attempt).expect("small attempt"); 16],
            pull_routing: None,
        }));
        assert!(!started.writes.is_empty(), "attempt {attempt} starts");
        let failed = self.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
            invocation: Some(wait_for_key()),
            attempt,
            kind: 1,
            worker_error: Some(WorkerErrorResponse {
                detail: "declined".to_owned(),
                ..WorkerErrorResponse::default()
            }),
            ..StepAttemptFailedCommand::default()
        }));
        assert!(!failed.writes.is_empty(), "attempt {attempt} fails");
        let fired = self.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: self.run_record().current_attempt,
                kind: Some(timer_id::Kind::RetryBackoff(RetryBackoffTimerId {
                    invocation: Some(wait_for_key()),
                    next_attempt: attempt + 1,
                })),
            }],
        }));
        assert!(
            !fired.writes.is_empty(),
            "the retry timer of {attempt} fires"
        );
    }

    fn update_flow_config(&mut self, flow_config: FlowConfig) {
        let outcome = self.apply(Command::FlowConfigUpdate(FlowConfigUpdateCommand {
            request: Some(UpdateFlowConfigRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_config: Some(flow_config),
                ..UpdateFlowConfigRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "UpdateFlowConfig applies");
    }

    fn update_window(&mut self, history_window_events: i32) {
        self.update_flow_config(FlowConfig {
            history_window_events: Some(history_window_events),
            ..FlowConfig::default()
        });
    }

    fn time_travel(&mut self, request: ResetFlowRequest) -> CommandReply {
        self.apply(Command::TimeTravel(TimeTravelCommand {
            should_restore_running_as_pending: false,
            request: Some(request),
        }))
        .reply
    }

    fn run_record(&self) -> RunRecord {
        let key = run_singleton_key(self.context.flow_hash, self.run_id, RunTable::RunRecord);
        let bytes = self
            .state
            .rows
            .get(&(ColumnFamily::Flow, key))
            .expect("the run record exists");
        RunRecord::decode(bytes.as_slice()).expect("the run record decodes")
    }

    fn put_run_record(&mut self, record: &RunRecord) {
        let key = run_singleton_key(self.context.flow_hash, self.run_id, RunTable::RunRecord);
        self.state
            .rows
            .insert((ColumnFamily::Flow, key), record.encode_to_vec());
    }

    /// History records by seq, including those below the window that rebuild it.
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

    /// Metadata rows of HistoryRecord rows by seq.
    fn record_metadata(&self) -> BTreeMap<u64, HistoryRowMetadata> {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.run_id,
            RunTable::HistoryRowMetadata,
        );
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .filter_map(|((_, key), bytes)| {
                let suffix = &key[prefix.len()..];
                let seq = u64::from_be_bytes(suffix[..8].try_into().expect("eight seq bytes"));
                (suffix[8] == RunTable::HistoryRecord as u8).then(|| {
                    let metadata =
                        HistoryRowMetadata::decode(bytes.as_slice()).expect("metadata decodes");
                    (seq, metadata)
                })
            })
            .collect()
    }

    fn events_in_window(&self) -> u64 {
        let record = self.run_record();
        record.window_event_count.expect("the run counts events") - record.pruned_window_event_count
    }

    /// Checks the counters against the metadata: W's count before it is what left the window,
    /// and each record adds its events, or one when it is semantic without events.
    ///
    /// Rows written before the window counted events count one per semantic record.
    fn assert_counters_match_metadata(&self) {
        let record = self.run_record();
        let metadata = self.record_metadata();
        let window_start = metadata
            .get(&record.window_start_seq)
            .expect("W has metadata");
        assert_eq!(
            window_start
                .window_event_count_before
                .unwrap_or(record.pruned_semantic_event_count),
            record.pruned_window_event_count,
            "the events before W left the window"
        );
        let records = self.history_records();
        let last_seq = record.next_run_seq - 1;
        for (seq, row) in &metadata {
            let Some(count_before) = row.window_event_count_before else {
                continue;
            };
            let next_count = if *seq == last_seq {
                record.window_event_count.expect("the run counts events")
            } else {
                let Some(next) = metadata.get(&(seq + 1)) else {
                    continue;
                };
                next.window_event_count_before
                    .expect("rows after a counted row are counted")
            };
            let history = &records[seq];
            let expected_events = if history.events.is_empty() {
                u64::from(history.is_semantic)
            } else {
                u64::try_from(history.events.len()).expect("small event count")
            };
            assert_eq!(
                next_count - count_before,
                expected_events,
                "record {seq} counts its events"
            );
        }
    }

    /// Clears the window fields that runs recorded before the window counted events lack.
    fn forget_window_event_counts(&mut self) {
        let mut record = self.run_record();
        record.window_event_count = None;
        record.pruned_window_event_count = 0;
        self.put_run_record(&record);
        for ((column_family, key), bytes) in &mut self.state.rows {
            let is_metadata = *column_family == ColumnFamily::Flow
                && parse_run_key(key).is_ok_and(|parsed| {
                    parsed.run_id == self.run_id && parsed.table == RunTable::HistoryRowMetadata
                });
            if is_metadata {
                let mut metadata =
                    HistoryRowMetadata::decode(bytes.as_slice()).expect("metadata decodes");
                metadata.window_event_count_before = None;
                *bytes = metadata.encode_to_vec();
            }
        }
    }
}

fn settings(history_window_events: u32, history_prune_chunk_events: u32) -> RunSettings {
    RunSettings {
        history_window_events,
        history_prune_chunk_events,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 20,
        checkpoint_command_threshold: 4,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
    }
}

fn worker_target_config() -> FlowConfig {
    FlowConfig {
        worker_target: Some(WorkerTarget { is_pull_mode: true }),
        ..FlowConfig::default()
    }
}

fn window_config(history_window_events: i32) -> FlowConfig {
    FlowConfig {
        history_window_events: Some(history_window_events),
        ..worker_target_config()
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

fn error_code(reply: &CommandReply) -> Option<i32> {
    match &reply.reply {
        Some(command_reply::Reply::Error(error)) => Some(error.grpc_code),
        _ => None,
    }
}

fn wait_for_key() -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: "ChargeStep-1".to_owned(),
        method: StepMethod::WaitFor as i32,
    }
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

#[test]
fn the_window_grows_past_one_chunk_then_prunes_about_one_chunk_back_to_its_size() {
    // A window of 1000 events prunes min(100, 1000 / 10) = 100 events at once.
    let mut harness = Harness::start(
        RunSettings {
            checkpoint_command_threshold: 100,
            ..settings(1000, 100)
        },
        worker_target_config(),
    );
    // FlowStart records event 1; every Attribute write records one more.
    while harness.run_record().window_event_count < Some(1100) {
        harness.set_attribute();
    }
    assert_eq!(harness.run_record().pruned_window_event_count, 0);
    harness.set_attribute();
    let record = harness.run_record();
    assert_eq!(record.window_event_count, Some(1101));
    assert_eq!(record.pruned_window_event_count, 100);
    assert_eq!(harness.events_in_window(), 1001);
    // W is the first record whose events stayed: event 101 is the oldest one kept.
    let window_start = &harness.history_records()[&record.window_start_seq];
    assert_eq!(window_start.events[0].event_id, 101);
    harness.assert_counters_match_metadata();
    for _ in 0..99 {
        harness.set_attribute();
    }
    assert_eq!(harness.run_record().pruned_window_event_count, 100);
    harness.set_attribute();
    assert_eq!(harness.run_record().pruned_window_event_count, 200);
    assert_eq!(harness.events_in_window(), 1001);
    harness.assert_counters_match_metadata();
}

#[test]
fn the_default_window_prunes_about_one_thousand_events_after_eleven_thousand() {
    let mut harness = Harness::start(
        RunSettings {
            checkpoint_command_threshold: 1000,
            ..settings(10_000, 1000)
        },
        worker_target_config(),
    );
    // FlowStart records event 1; every Attribute write records one more.
    while harness.run_record().window_event_count < Some(11_000) {
        harness.set_attribute();
    }
    assert_eq!(harness.run_record().pruned_window_event_count, 0);
    harness.set_attribute();
    let record = harness.run_record();
    assert_eq!(record.window_event_count, Some(11_001));
    assert_eq!(record.pruned_window_event_count, 1000);
    assert_eq!(harness.events_in_window(), 10_001);
    let window_start = &harness.history_records()[&record.window_start_seq];
    assert_eq!(window_start.events[0].event_id, 1001);
    harness.assert_counters_match_metadata();
    for _ in 0..999 {
        harness.set_attribute();
    }
    assert_eq!(harness.run_record().pruned_window_event_count, 1000);
    harness.set_attribute();
    assert_eq!(harness.run_record().pruned_window_event_count, 2000);
    assert_eq!(harness.events_in_window(), 10_001);
    harness.assert_counters_match_metadata();
}

#[test]
fn semantic_commands_without_events_count_one_and_move_the_window() {
    let mut harness = Harness::start(settings(4, 1000), worker_target_config());
    let next_event_id = harness.run_record().next_event_id;
    for attempt in 1..=6 {
        harness.fail_and_retry_wait_for(attempt);
    }
    let records = harness.history_records();
    let failures: Vec<_> = records
        .values()
        .filter(|record| {
            matches!(
                record
                    .command
                    .as_ref()
                    .and_then(|command| command.command.as_ref()),
                Some(Command::StepAttemptFailed(_))
            )
        })
        .collect();
    assert!(!failures.is_empty());
    for failure in &failures {
        assert!(failure.is_semantic && failure.events.is_empty());
    }
    let record = harness.run_record();
    assert_eq!(
        record.next_event_id, next_event_id,
        "no command recorded an event"
    );
    assert!(record.window_start_seq > 1, "eventless commands moved W");
    assert!(record.pruned_window_event_count > 0);
    assert!(harness.events_in_window() <= 5);
    harness.assert_counters_match_metadata();
}

#[test]
fn a_run_window_set_at_start_overrides_the_server_window() {
    let mut harness = Harness::start(settings(10_000, 1000), window_config(20));
    // A window of 20 prunes 2 at a time, once it holds more than 22 events.
    for _ in 0..21 {
        harness.set_attribute();
    }
    assert_eq!(harness.events_in_window(), 22);
    assert_eq!(harness.run_record().pruned_window_event_count, 0);
    harness.set_attribute();
    assert_eq!(harness.run_record().pruned_window_event_count, 2);
    assert_eq!(harness.events_in_window(), 21);
    harness.assert_counters_match_metadata();
}

#[test]
fn update_flow_config_shrinks_the_window_at_the_next_pruning_and_grows_it_for_later_events() {
    let mut harness = Harness::start(settings(10_000, 1000), window_config(20));
    for _ in 0..21 {
        harness.set_attribute();
    }
    assert_eq!(harness.events_in_window(), 22);
    // Changing the configuration records no event, so pruning waits for the next one.
    harness.update_window(8);
    assert_eq!(harness.run_record().pruned_window_event_count, 0);
    harness.set_attribute();
    // A window of 8 prunes 1 at a time; the oversized window drops to 8 plus one chunk at once.
    assert_eq!(harness.events_in_window(), 9);
    harness.assert_counters_match_metadata();
    harness.set_attribute();
    assert_eq!(harness.events_in_window(), 9);
    // Another field leaves the window unchanged; the window cannot be cleared.
    harness.update_flow_config(FlowConfig {
        step_durability: Some(StepDurability::Async as i32),
        ..FlowConfig::default()
    });
    let flow_config = harness
        .run_record()
        .flow_config
        .expect("the run has a config");
    assert_eq!(flow_config.history_window_events, Some(8));
    assert_eq!(
        flow_config.step_durability,
        Some(StepDurability::Async as i32)
    );
    harness.update_window(30);
    let pruned = harness.run_record().pruned_window_event_count;
    let window_start_seq = harness.run_record().window_start_seq;
    // A window of 30 prunes 3 at a time once it holds more than 33; pruned events stay pruned.
    for _ in 0..24 {
        harness.set_attribute();
    }
    assert_eq!(harness.events_in_window(), 33);
    assert_eq!(harness.run_record().pruned_window_event_count, pruned);
    assert_eq!(harness.run_record().window_start_seq, window_start_seq);
    assert!(
        harness.history_records().keys().nth(1) > Some(&2),
        "records pruned before the window grew are not restored"
    );
    harness.set_attribute();
    assert_eq!(harness.run_record().pruned_window_event_count, pruned + 3);
    assert_eq!(harness.events_in_window(), 31);
    harness.assert_counters_match_metadata();
}

#[test]
fn a_small_window_prunes_a_tenth_of_itself_at_once() {
    for (settings, flow_config, chunk_events) in [
        (settings(30, 1000), worker_target_config(), 3),
        (settings(10_000, 1000), window_config(5), 1),
        (settings(200, 7), worker_target_config(), 7),
    ] {
        let window_events = u64::from(settings.history_window_events);
        let mut harness = Harness::start(settings, flow_config.clone());
        let window_events = flow_config
            .history_window_events
            .map_or(window_events, |events| {
                u64::try_from(events).expect("positive")
            });
        let mut previous_pruned = 0;
        for _ in 0..(window_events * 3) {
            harness.set_attribute();
            let pruned = harness.run_record().pruned_window_event_count;
            if pruned != previous_pruned {
                assert_eq!(
                    pruned - previous_pruned,
                    chunk_events,
                    "one chunk at a time"
                );
                assert_eq!(harness.events_in_window(), window_events + 1);
            }
            assert!(harness.events_in_window() <= window_events + chunk_events);
            previous_pruned = pruned;
        }
        assert!(previous_pruned > 0, "the window pruned");
        harness.assert_counters_match_metadata();
    }
}

#[test]
fn a_run_recorded_before_the_window_counted_events_counts_its_records_once() {
    let mut harness = Harness::start(settings(6, 1000), worker_target_config());
    for _ in 0..3 {
        harness.set_attribute();
    }
    let travel_time = harness.history_records()[&2].timestamp_micros;
    let reply = harness.time_travel(ResetFlowRequest {
        skip_writes_reapply: false,
        ..reset_to_time(travel_time)
    });
    assert!(
        matches!(reply.reply, Some(command_reply::Reply::TimeTravel(_))),
        "{reply:?}"
    );
    let travel_seq = harness.run_record().next_run_seq - 1;
    let travel_events = harness.history_records()[&travel_seq].events.len();
    assert!(
        travel_events > 1,
        "the reapplied writes give the travel {travel_events} events"
    );
    harness.forget_window_event_counts();
    let legacy = harness.run_record();
    assert_eq!(legacy.pruned_semantic_event_count, 0);
    harness.set_attribute();
    // The recorded window of 6 now counts events; older records count one per semantic record.
    let record = harness.run_record();
    assert_eq!(
        record.window_event_count,
        Some(legacy.semantic_event_count + 1)
    );
    assert_eq!(record.pruned_window_event_count, 0);
    let mut has_passed_travel = false;
    while !has_passed_travel {
        harness.set_attribute();
        let record = harness.run_record();
        assert!(harness.events_in_window() <= 7);
        let window_start = &harness.record_metadata()[&record.window_start_seq];
        match window_start.window_event_count_before {
            None => assert_eq!(
                record.pruned_window_event_count, record.pruned_semantic_event_count,
                "every older record before W counted once"
            ),
            Some(count_before) => assert_eq!(record.pruned_window_event_count, count_before),
        }
        has_passed_travel = record.window_start_seq > travel_seq;
    }
    for _ in 0..20 {
        harness.set_attribute();
    }
    assert!(harness.events_in_window() <= 7);
    harness.assert_counters_match_metadata();
}

#[test]
fn time_travel_reaches_only_the_window_and_always_the_beginning() {
    let mut harness = Harness::start(settings(6, 1000), worker_target_config());
    for _ in 0..12 {
        harness.set_attribute();
    }
    let window_start_seq = harness.run_record().window_start_seq;
    assert!(window_start_seq > 3);
    let records = harness.history_records();
    let window_start_time = records[&window_start_seq].timestamp_micros;
    // Seq 2 was pruned; its time is one second after FlowStart.
    let pruned_time = records[&1].timestamp_micros + SECOND;
    let rejected = harness.time_travel(reset_to_time(pruned_time));
    assert_eq!(error_code(&rejected), Some(FAILED_PRECONDITION));
    let reply = harness.time_travel(reset_to_time(window_start_time));
    let Some(command_reply::Reply::TimeTravel(traveled)) = reply.reply else {
        panic!("travel to W applies: {reply:?}");
    };
    assert_eq!(traveled.run_attempt, 2);
    let reply = harness.time_travel(reset(FlowResetType::Beginning));
    let Some(command_reply::Reply::TimeTravel(traveled)) = reply.reply else {
        panic!("travel to the beginning applies: {reply:?}");
    };
    assert_eq!(traveled.run_attempt, 3);
}

#[test]
fn metadata_pruning_deletes_old_metadata_and_maintains_the_pruned_event_range() {
    let mut harness = Harness::start(settings(6, 1000), worker_target_config());
    for _ in 0..20 {
        harness.set_attribute();
        let records = harness.history_records();
        let metadata = harness.record_metadata();
        assert_eq!(
            metadata.keys().collect::<Vec<_>>(),
            records.keys().collect::<Vec<_>>(),
            "metadata rows go with the records they describe"
        );
    }
    let record = harness.run_record();
    let records = harness.history_records();
    let (first_kept_seq, first_kept) = records
        .iter()
        .nth(1)
        .expect("a record after FlowStart is kept");
    assert!(*first_kept_seq > 2, "the window pruned records");
    let first_kept_event_id = first_kept.events[0].event_id;
    assert_eq!(record.max_pruned_event_id, first_kept_event_id - 1);
    let response = get_history_events(
        &harness.state,
        harness.context.flow_hash,
        harness.run_id,
        0,
        100,
        &[],
        0,
    )
    .expect("history reads");
    let range = response
        .pruned_event_id_range
        .expect("the window pruned events");
    assert_eq!(range.start_event_id, 2);
    assert_eq!(range.end_event_id, record.max_pruned_event_id);
    assert_eq!(response.events[0].event_id, 1);
    assert_eq!(response.events[1].event_id, first_kept_event_id);
}
