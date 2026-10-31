// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! GetHistoryEvents paging over long and time-traveled histories, checked against a full scan.

use std::cell::Cell;
use std::collections::BTreeMap;

use dexzd_apply::read_model::get_history_events;
use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{
    attempt_record_key, history_record_key, prefix_range, run_named_key, run_singleton_key,
};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_protocol::dex::{
    ChannelMessage, DeleteChannelMessageRequest, FlowHistoryEvent, FlowResetType,
    FlowTimeoutPolicy, PublishToChannelRequest, ResetFlowRequest, StartFlowRequest, StepOptions,
    StopFlowRequest, StopType, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttemptRecord, ChannelMessageDeleteCommand, ChannelPublishCommand, CommandReply, FlowCommand,
    FlowStartCommand, FlowStopCommand, HistoryRecord, RunRecord, RunSettings,
    StepAttemptHeartbeatCommand, StepAttemptStartedCommand, StepExecutionRecord, StepInvocationKey,
    StepInvocationPhase, StepMethod, TimeTravelCommand, command_reply, flow_command::Command,
};
use prost::Message;

const SECOND: i64 = 1_000_000;

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
                StateWrite::Delete { column_family, key } => {
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

/// Counts the History column family rows that reads return.
struct CountingReader<'state> {
    state: &'state MemoryState,
    history_rows: Cell<usize>,
}

impl<'state> CountingReader<'state> {
    fn new(state: &'state MemoryState) -> Self {
        Self {
            state,
            history_rows: Cell::new(0),
        }
    }

    fn take_history_rows(&self) -> usize {
        self.history_rows.replace(0)
    }
}

impl FlowStateReader for CountingReader<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        let row = self.state.get(column_family, key)?;
        if column_family == ColumnFamily::History && row.is_some() {
            self.history_rows.set(self.history_rows.get() + 1);
        }
        Ok(row)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let rows = self.state.scan(column_family, start, end, limit)?;
        if column_family == ColumnFamily::History {
            self.history_rows.set(self.history_rows.get() + rows.len());
        }
        Ok(rows)
    }
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    run_id: RunId,
    now_micros: i64,
    next_message: u64,
}

impl Harness {
    fn new(settings: RunSettings) -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, "order-1");
        let mut harness = Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            run_id: RunId([9; 16]),
            now_micros: 1_800_000_000 * SECOND,
            next_message: 0,
        };
        let outcome = harness.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: harness.run_id.as_bytes().to_vec(),
            request: Some(StartFlowRequest {
                flow_id: "order-1".to_owned(),
                flow_type: "OrderFlow".to_owned(),
                start_step_type: "ChargeStep".to_owned(),
                step_input: Some(string_value("input")),
                step_options: Some(StepOptions {
                    skip_wait_for: true,
                    ..StepOptions::default()
                }),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            }),
            settings: Some(settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
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
        self.now_micros += 1000;
        let is_start = matches!(body, Command::FlowStart(_));
        let command = FlowCommand {
            flow_id: "order-1".to_owned(),
            run_id: if is_start {
                Vec::new()
            } else {
                self.run_id.as_bytes().to_vec()
            },
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            tenant_id: 0,
            command: Some(body),
            ..FlowCommand::default()
        };
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("the command applies");
        self.state.commit(&outcome);
        outcome
    }

    /// Publishes and deletes one message, one event each, then heartbeats `hole_rows` times
    /// without events.
    fn append_round(&mut self, hole_rows: usize) {
        self.next_message += 1;
        let message_id = format!("m{}", self.next_message);
        let published = self.apply(Command::ChannelPublish(ChannelPublishCommand {
            request: Some(PublishToChannelRequest {
                messages: vec![ChannelMessage {
                    channel_name: "orders".to_owned(),
                    value: Some(string_value(&message_id)),
                    message_id: message_id.clone(),
                }],
                ..PublishToChannelRequest::default()
            }),
        }));
        assert!(is_ok(&published.reply), "PublishToChannel applies");
        let deleted = self.apply(Command::ChannelMessageDelete(ChannelMessageDeleteCommand {
            request: Some(DeleteChannelMessageRequest {
                channel_name: "orders".to_owned(),
                message_id,
                ..DeleteChannelMessageRequest::default()
            }),
            ..ChannelMessageDeleteCommand::default()
        }));
        assert!(is_ok(&deleted.reply), "DeleteChannelMessage applies");
        for _ in 0..hole_rows {
            self.heartbeat();
        }
    }

    /// Heartbeats the running Execute attempt, starting it first when needed.
    fn heartbeat(&mut self) {
        let key = run_named_key(
            self.context.flow_hash,
            self.run_id,
            RunTable::StepExecution,
            "ChargeStep-1",
        );
        let bytes = self
            .state
            .rows
            .get(&(ColumnFamily::Flow, key))
            .expect("the Step execution exists");
        let step = StepExecutionRecord::decode(bytes.as_slice()).expect("the Step decodes");
        let invocation = step.invocation.expect("the Step has an invocation");
        let invocation_key = StepInvocationKey {
            step_execution_id: "ChargeStep-1".to_owned(),
            method: StepMethod::Execute as i32,
        };
        if invocation.phase != StepInvocationPhase::Running as i32 {
            self.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
                invocation: Some(invocation_key.clone()),
                attempt: invocation.attempt,
                task_id: vec![2; 16],
                pull_routing: None,
            }));
        }
        let heartbeat = self.apply(Command::StepAttemptHeartbeat(StepAttemptHeartbeatCommand {
            invocation: Some(invocation_key),
            attempt: invocation.attempt,
            checkpoint: None,
        }));
        assert!(is_ok(&heartbeat.reply), "the heartbeat applies");
    }

    /// Time travels to `timestamp_micros` of `run_attempt` and returns the new attempt.
    fn travel_to(&mut self, run_attempt: u32, timestamp_micros: i64) -> u32 {
        let outcome = self.apply(Command::TimeTravel(TimeTravelCommand {
            request: Some(ResetFlowRequest {
                flow_id: "order-1".to_owned(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                reason: "test".to_owned(),
                history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
                skip_writes_reapply: false,
                run_attempt: i32::try_from(run_attempt).expect("small attempt"),
                ..ResetFlowRequest::default()
            }),
        }));
        match &outcome.reply.reply {
            Some(command_reply::Reply::TimeTravel(reply)) => reply.run_attempt,
            other => panic!("time travel was rejected: {other:?}"),
        }
    }

    /// Removes History rows below `seq`, as history pruning does after the window moves.
    fn remove_history_before(&mut self, seq: u64) {
        let flow_hash = self.context.flow_hash;
        let run_id = self.run_id;
        let prefix = run_table_prefix(flow_hash, run_id, RunTable::HistoryRecord);
        let end = history_record_key(flow_hash, run_id, seq);
        self.state.rows.retain(|(column_family, key), _| {
            *column_family != ColumnFamily::History
                || !key.starts_with(&prefix)
                || key.as_slice() >= end.as_slice()
        });
    }

    fn stop(&mut self) {
        let outcome = self.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: StopType::Cancel as i32,
                ..StopFlowRequest::default()
            }),
        }));
        assert!(
            is_ok(&outcome.reply),
            "StopFlow applies: {:?}",
            outcome.reply
        );
    }

    fn history_records(&self) -> Vec<HistoryRecord> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run_id, RunTable::HistoryRecord);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::History, start)..(ColumnFamily::History, end))
            .map(|(_, bytes)| HistoryRecord::decode(bytes.as_slice()).expect("history decodes"))
            .collect()
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

    fn attempt_record(&self, attempt: u32) -> AttemptRecord {
        let key = attempt_record_key(self.context.flow_hash, self.run_id, attempt);
        let bytes = self
            .state
            .rows
            .get(&(ColumnFamily::Flow, key))
            .expect("the attempt record exists");
        AttemptRecord::decode(bytes.as_slice()).expect("the attempt record decodes")
    }

    /// The pre-seek algorithm over every record: lineage filter, sort, truncate.
    fn reference_page(
        &self,
        records: &[HistoryRecord],
        start_event_id: i64,
        page_size: i32,
        next_page_token: &[u8],
        run_attempt: u32,
    ) -> HistoryPage {
        let mut first_event_id = start_event_id.max(1);
        if !next_page_token.is_empty() {
            let token: [u8; 8] = next_page_token.try_into().expect("an 8-byte token");
            first_event_id = first_event_id.max(i64::from_be_bytes(token) + 1);
        }
        let mut attempt = if run_attempt == 0 {
            self.run_record().current_attempt
        } else {
            run_attempt
        };
        let mut lineage = BTreeMap::new();
        let mut cutoff = u64::MAX;
        while attempt != 0 {
            lineage.insert(attempt, cutoff);
            let record = self.attempt_record(attempt);
            cutoff = record.parent_seq;
            attempt = record.parent_attempt;
        }
        let mut events = Vec::new();
        for record in records {
            if !lineage
                .get(&record.attempt)
                .is_some_and(|cutoff| record.seq <= *cutoff)
            {
                continue;
            }
            for event in &record.events {
                if event.event_id >= first_event_id {
                    let mut event = event.clone();
                    if event.run_attempt == 0 {
                        event.run_attempt = i32::try_from(record.attempt).unwrap_or(i32::MAX);
                    }
                    events.push(event);
                }
            }
        }
        events.sort_by_key(|event| event.event_id);
        let size = if page_size == 0 {
            100
        } else {
            usize::try_from(page_size).expect("non-negative page size")
        };
        let has_more = events.len() > size;
        events.truncate(size);
        let next_internal_event_id = events
            .last()
            .map_or(first_event_id, |event| event.event_id + 1);
        let next_page_token = match events.last() {
            Some(event) if has_more => event.event_id.to_be_bytes().to_vec(),
            _ => Vec::new(),
        };
        HistoryPage {
            events,
            next_page_token,
            next_internal_event_id,
        }
    }

    /// Pages through one lineage, compares every page with the reference, and returns the
    /// largest number of History rows one page read.
    fn assert_pages_match_reference(
        &self,
        start_event_id: i64,
        page_size: i32,
        run_attempt: u32,
    ) -> PagingStats {
        let records = self.history_records();
        let reader = CountingReader::new(&self.state);
        let mut token = Vec::new();
        let mut stats = PagingStats::default();
        loop {
            let response = get_history_events(
                &reader,
                self.context.flow_hash,
                self.run_id,
                start_event_id,
                page_size,
                &token,
                i32::try_from(run_attempt).expect("small attempt"),
            )
            .expect("the history page reads");
            let rows = reader.take_history_rows();
            stats.max_rows_per_page = stats.max_rows_per_page.max(rows);
            stats.pages += 1;
            let actual = HistoryPage {
                events: response.events,
                next_page_token: response.next_page_token,
                next_internal_event_id: response.next_internal_event_id,
            };
            let expected =
                self.reference_page(&records, start_event_id, page_size, &token, run_attempt);
            assert_eq!(
                actual, expected,
                "page {} of attempt {run_attempt} with size {page_size} from event {start_event_id}",
                stats.pages
            );
            stats.events.extend(actual.events);
            if actual.next_page_token.is_empty() {
                break;
            }
            token = actual.next_page_token;
        }
        let everything = self.reference_page(&records, start_event_id, i32::MAX, &[], run_attempt);
        assert_eq!(
            stats.events, everything.events,
            "pages concatenate to the lineage"
        );
        stats
    }
}

#[derive(Debug, PartialEq)]
struct HistoryPage {
    events: Vec<FlowHistoryEvent>,
    next_page_token: Vec<u8>,
    next_internal_event_id: i64,
}

#[derive(Default)]
struct PagingStats {
    pages: usize,
    max_rows_per_page: usize,
    events: Vec<FlowHistoryEvent>,
}

fn settings(history_window_semantic_events: u32, checkpoint_max_commands: u32) -> RunSettings {
    RunSettings {
        history_window_semantic_events,
        history_prune_chunk_semantic_events: (history_window_semantic_events / 10).max(1),
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_min_written_bytes: 1 << 20,
        checkpoint_max_commands,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
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

/// Reads one event from each start and returns the most History rows one read used.
fn assert_single_event_reads_match_reference(harness: &Harness, start_event_ids: &[i64]) -> usize {
    let records = harness.history_records();
    let reader = CountingReader::new(&harness.state);
    let mut max_rows = 0;
    for start_event_id in start_event_ids {
        let response = get_history_events(
            &reader,
            harness.context.flow_hash,
            harness.run_id,
            *start_event_id,
            1,
            &[],
            0,
        )
        .expect("the history page reads");
        let expected = harness.reference_page(&records, *start_event_id, 1, &[], 0);
        assert_eq!(
            HistoryPage {
                events: response.events,
                next_page_token: response.next_page_token,
                next_internal_event_id: response.next_internal_event_id,
            },
            expected,
            "single-event page from event {start_event_id}"
        );
        max_rows = max_rows.max(reader.take_history_rows());
    }
    max_rows
}

#[test]
fn paging_a_long_history_reads_rows_proportional_to_the_page() {
    let mut harness = Harness::new(settings(1_000_000, 10_000));
    for round in 0..10_000 {
        harness.append_round(usize::from(round % 3 == 0));
    }
    harness.stop();
    let records = harness.history_records();
    let event_count: usize = records.iter().map(|record| record.events.len()).sum();
    assert!(
        event_count > 20_000,
        "the history holds {event_count} events"
    );
    assert!(records.iter().any(|record| record.events.is_empty()));
    assert!(records.iter().any(|record| record.events.len() > 1));

    let web_pages = harness.assert_pages_match_reference(0, 200, 0);
    assert_eq!(web_pages.pages, event_count.div_ceil(200));
    // A 200-event page spans about 234 rows; the seek adds a few rows per halving.
    assert!(
        web_pages.max_rows_per_page < 600,
        "a 200-event page read {} of {} History rows",
        web_pages.max_rows_per_page,
        records.len()
    );
    let single_event_rows = assert_single_event_reads_match_reference(
        &harness,
        &[
            1, 2, 9_999, 10_000, 15_001, 19_999, 20_001, 20_002, 20_003, 30_000,
        ],
    );
    assert!(
        single_event_rows < 100,
        "a one-event page read {single_event_rows} History rows"
    );

    for page_size in [0, 1_000] {
        harness.assert_pages_match_reference(0, page_size, 0);
    }
    harness.assert_pages_match_reference(12_345, 333, 0);
}

#[test]
fn paging_across_long_eventless_spans_reads_each_span_at_most_twice() {
    const SPAN_ROWS: usize = 600;
    let mut harness = Harness::new(settings(1_000_000, 10_000));
    for round in 0..2_000 {
        let hole_rows = if round % 250 == 0 {
            SPAN_ROWS
        } else {
            usize::from(round % 3 == 0)
        };
        harness.append_round(hole_rows);
    }
    harness.stop();
    let records = harness.history_records();

    let web_pages = harness.assert_pages_match_reference(0, 200, 0);
    // The seek and the page scan may each cross one eventless span.
    assert!(
        web_pages.max_rows_per_page < 2 * SPAN_ROWS + 600,
        "a 200-event page read {} of {} History rows",
        web_pages.max_rows_per_page,
        records.len()
    );
    let tail_rows =
        assert_single_event_reads_match_reference(&harness, &[harness.run_record().next_event_id]);
    assert_eq!(
        tail_rows, 0,
        "a read past the last event reads no History rows"
    );
    let near_spans: Vec<i64> = (0..8)
        .flat_map(|span| {
            let span_event_id = 2 + i64::from(span) * 500;
            [span_event_id - 1, span_event_id, span_event_id + 1]
        })
        .collect();
    let single_event_rows = assert_single_event_reads_match_reference(&harness, &near_spans);
    assert!(
        single_event_rows < 2 * SPAN_ROWS + 300,
        "a one-event page read {single_event_rows} History rows"
    );
    harness.assert_pages_match_reference(0, 1, 0);
}

#[test]
fn paging_after_the_oldest_rows_are_removed_starts_at_the_first_retained_event() {
    let mut harness = Harness::new(settings(1_000_000, 10_000));
    for round in 0..500 {
        harness.append_round(usize::from(round % 4 == 0));
    }
    let removed_before_seq = harness.history_records()[300].seq;
    harness.remove_history_before(removed_before_seq);
    let first_retained = harness.history_records()[0].events[0].event_id;
    assert!(first_retained > 200, "the oldest events are gone");
    for (start_event_id, page_size) in [(0, 7), (3, 50), (first_retained, 1), (999, 3)] {
        harness.assert_pages_match_reference(start_event_id, page_size, 0);
    }
}

#[test]
fn paging_each_time_travel_attempt_matches_its_lineage() {
    let mut harness = Harness::new(settings(1_000_000, 10_000));
    let mut round_times = BTreeMap::new();
    for round in 0..40 {
        harness.append_round(usize::from(round % 2 == 0));
        round_times.insert((1, round), harness.now_micros);
    }
    let second = harness.travel_to(1, round_times[&(1, 10)]);
    assert_eq!(second, 2);
    for round in 0..30 {
        harness.append_round(usize::from(round % 3 == 0));
        round_times.insert((2, round), harness.now_micros);
    }
    let third = harness.travel_to(1, round_times[&(1, 25)]);
    assert_eq!(third, 3);
    for round in 0..20 {
        harness.append_round(0);
        round_times.insert((3, round), harness.now_micros);
    }
    let fourth = harness.travel_to(2, round_times[&(2, 12)]);
    assert_eq!(fourth, 4);
    for _ in 0..15 {
        harness.append_round(1);
    }
    assert_eq!(harness.attempt_record(3).parent_attempt, 1);
    assert_eq!(harness.attempt_record(4).parent_attempt, 2);

    let records = harness.history_records();
    let attempts: Vec<u32> = records.iter().map(|record| record.attempt).collect();
    assert!(attempts.windows(2).all(|pair| pair[0] <= pair[1]));
    let mut last_event_id = 0;
    for event in records.iter().flat_map(|record| &record.events) {
        assert!(
            event.event_id > last_event_id,
            "event IDs increase with seq"
        );
        last_event_id = event.event_id;
    }
    assert!(
        records
            .iter()
            .any(|record| record.attempt > 1 && record.events.len() > 1),
        "a time travel record holds several events"
    );

    for run_attempt in 0..=4 {
        for page_size in [1, 2, 3, 7, 100] {
            let stats = harness.assert_pages_match_reference(0, page_size, run_attempt);
            let lineage_attempts: Vec<i32> =
                stats.events.iter().map(|event| event.run_attempt).collect();
            assert!(lineage_attempts.windows(2).all(|pair| pair[0] <= pair[1]));
        }
        let fork_start = harness.attempt_record(run_attempt.max(2)).start_seq;
        let fork_event_id = records
            .iter()
            .filter(|record| record.seq >= fork_start)
            .flat_map(|record| &record.events)
            .map(|event| event.event_id)
            .next()
            .unwrap_or(1);
        harness.assert_pages_match_reference(fork_event_id - 2, 4, run_attempt);
    }
}
