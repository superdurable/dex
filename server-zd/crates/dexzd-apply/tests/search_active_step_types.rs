// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Active Step type counts and search documents, checked after every command against a full
//! row count.

use std::cell::Cell;
use std::collections::BTreeMap;

use dexzd_apply::read_model::get_search_document;
use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_time_travel_for_test,
};
use dexzd_keyspace::row_keys::{prefix_range, run_singleton_key};
use dexzd_keyspace::{
    ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, parse_run_key, run_table_prefix,
};
use dexzd_protocol::dex::{
    ActiveStepSearchMode, AttributeWrite, FlowConfig, FlowResetStepMethod, FlowResetType,
    FlowStartOptions, FlowTimeoutHandlerOptions, FlowTimeoutPolicy, InvokeExecuteMethodResponse,
    InvokeWaitForMethodResponse, ResetFlowRequest, SetAttributesRequest, StartFlowRequest,
    StepDecision, StepMovement, StepOptions, StopFlowRequest, StopType, TimerCondition,
    UpdateFlowConfigRequest, Value, WaitingCondition, WaitingConditionType, WorkerErrorResponse,
    WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    ActiveStepTypeCounts, AttributesSetCommand, FlowCommand, FlowConfigUpdateCommand,
    FlowStartCommand, FlowStopCommand, FlowTimeoutTimerId, RunRecord, RunSettings,
    StepAttemptFailedCommand, StepAttemptFailureKind, StepAttemptStartedCommand,
    StepExecutionPhase, StepExecutionRecord, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, TimeTravelCommand, TimerId, TimersFiredCommand,
    WaitConditionTimerId, command_reply, flow_command::Command, step_method_completed_command,
    timer_id,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const FLOW_ID: &str = "order-1";

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

/// Counts the Step execution rows that scans return.
struct StepScanCountingReader<'state> {
    state: &'state MemoryState,
    scanned_step_execution_rows: Cell<usize>,
}

impl FlowStateReader for StepScanCountingReader<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let rows = self.state.scan(column_family, start, end, limit)?;
        let step_rows = rows
            .iter()
            .filter(|(key, _)| {
                column_family == ColumnFamily::Flow
                    && parse_run_key(key)
                        .is_ok_and(|parsed| parsed.table == RunTable::StepExecution)
            })
            .count();
        self.scanned_step_execution_rows
            .set(self.scanned_step_execution_rows.get() + step_rows);
        Ok(rows)
    }
}

/// Active Step executions of one Step type: (with WaitFor, without WaitFor).
type ExpectedCounts = BTreeMap<String, (u64, u64)>;

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    run_id: RunId,
}

impl Harness {
    fn new() -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, FLOW_ID);
        Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000 * SECOND,
            run_id: RunId([9; 16]),
        }
    }

    fn start(
        &mut self,
        mode: Option<ActiveStepSearchMode>,
        step_options: StepOptions,
        timeout_policy: FlowTimeoutPolicy,
    ) {
        let start = self.start_command(mode, step_options, timeout_policy, settings());
        let outcome = self.apply(start);
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::StartFlow(_))
            ),
            "the run starts: {:?}",
            outcome.reply
        );
    }

    fn start_command(
        &self,
        mode: Option<ActiveStepSearchMode>,
        step_options: StepOptions,
        timeout_policy: FlowTimeoutPolicy,
        settings: RunSettings,
    ) -> Command {
        let is_handler = timeout_policy == FlowTimeoutPolicy::Handler;
        Command::FlowStart(FlowStartCommand {
            new_run_id: self.run_id.as_bytes().to_vec(),
            request: Some(StartFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_type: "OrderFlow".to_owned(),
                start_step_type: "ChargeStep".to_owned(),
                step_input: Some(string_value("input")),
                step_options: Some(step_options),
                flow_timeout_seconds: if is_handler { 30 } else { 0 },
                flow_timeout_policy: timeout_policy as i32,
                request_id: "start-1".to_owned(),
                flow_start_options: Some(FlowStartOptions {
                    flow_config_override: mode.map(|mode| FlowConfig {
                        active_step_search_mode: Some(mode as i32),
                        ..FlowConfig::default()
                    }),
                    timeout_handler_options: is_handler.then(FlowTimeoutHandlerOptions::default),
                    ..FlowStartOptions::default()
                }),
            }),
            settings: Some(settings),
            resolved_timeout_policy: if is_handler {
                timeout_policy
            } else {
                FlowTimeoutPolicy::Fail
            } as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..FlowStartCommand::default()
        })
    }

    /// Applies a command that may be rejected, and commits it when it applied.
    fn try_apply(&mut self, body: Command) -> ApplyOutcome {
        let command = self.command(body);
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("the command applies");
        self.state.commit(&outcome);
        outcome
    }

    /// Returns the run's state bytes that the run-state limit compares with its maximum.
    fn active_state_bytes(&self) -> u64 {
        let usage = self.run_record().storage.expect("the run counts storage");
        usage.state_row_bytes + usage.live_value_bytes
    }

    fn command(&mut self, body: Command) -> FlowCommand {
        self.now_micros += 1000;
        FlowCommand {
            flow_id: FLOW_ID.to_owned(),
            run_id: if matches!(body, Command::FlowStart(_)) {
                Vec::new()
            } else {
                self.run_id.as_bytes().to_vec()
            },
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: u128::try_from(self.now_micros)
                .expect("command time is positive")
                .to_be_bytes()
                .to_vec(),
            command: Some(body),
            ..FlowCommand::default()
        }
    }

    /// Applies a command, then requires the counts and the search document to match the rows.
    fn apply(&mut self, body: Command) -> ApplyOutcome {
        let command = self.command(body);
        let outcome = self.apply_command(command);
        self.assert_counts_match_step_executions();
        outcome
    }

    /// Applies a command and returns the Step execution rows its scans read.
    fn apply_counting_step_scans(&mut self, body: Command) -> usize {
        let command = self.command(body);
        let reader = StepScanCountingReader {
            state: &self.state,
            scanned_step_execution_rows: Cell::new(0),
        };
        let outcome =
            apply_flow_command(&self.context, &reader, &command).expect("the command applies");
        let scanned = reader.scanned_step_execution_rows.get();
        assert!(!outcome.writes.is_empty(), "applied: {:?}", outcome.reply);
        self.state.commit(&outcome);
        self.assert_counts_match_step_executions();
        scanned
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
            apply_flow_command(&self.context, &self.state, &command).expect("the command applies");
        let replay = apply_flow_command(&self.context, &self.state, &command)
            .expect("the command applies again");
        assert_eq!(outcome.writes, replay.writes, "apply is deterministic");
        assert!(!outcome.writes.is_empty(), "applied: {:?}", outcome.reply);
        self.state.commit(&outcome);
        outcome
    }

    fn complete_execute(&mut self, step_execution_id: &str, decision: StepDecision) {
        let start = start_attempt(step_execution_id, StepMethod::Execute);
        self.apply(start);
        self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation(step_execution_id, StepMethod::Execute)),
            attempt: 1,
            task_id: step_execution_id.as_bytes().to_vec(),
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(decision),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }));
    }

    fn complete_wait_for(&mut self, step_execution_id: &str, condition: Option<WaitingCondition>) {
        let start = start_attempt(step_execution_id, StepMethod::WaitFor);
        self.apply(start);
        self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation(step_execution_id, StepMethod::WaitFor)),
            attempt: 1,
            task_id: step_execution_id.as_bytes().to_vec(),
            response: Some(step_method_completed_command::Response::WaitFor(
                InvokeWaitForMethodResponse {
                    waiting_condition: condition,
                    ..InvokeWaitForMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }));
    }

    fn time_travel(&mut self, request: ResetFlowRequest) {
        let outcome = self.apply(Command::TimeTravel(TimeTravelCommand {
            request: Some(request),
            ..TimeTravelCommand::default()
        }));
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "time travel applies: {:?}",
            outcome.reply
        );
    }

    fn run_record(&self) -> RunRecord {
        let key = run_singleton_key(self.context.flow_hash, self.run_id, RunTable::RunRecord);
        RunRecord::decode(
            self.state
                .rows
                .get(&(ColumnFamily::Flow, key))
                .expect("the run record exists")
                .as_slice(),
        )
        .expect("the run record decodes")
    }

    /// Removes the counts from the stored run record, as a run recorded before them has none.
    fn clear_stored_counts(&mut self) {
        let key = run_singleton_key(self.context.flow_hash, self.run_id, RunTable::RunRecord);
        let mut record = self.run_record();
        record.search_active_step_type_counts = None;
        self.state
            .rows
            .insert((ColumnFamily::Flow, key), record.encode_to_vec());
    }

    fn step_executions(&self) -> Vec<StepExecutionRecord> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run_id, RunTable::StepExecution);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|(_, bytes)| StepExecutionRecord::decode(bytes.as_slice()).expect("Step decodes"))
            .collect()
    }

    fn step_execution_count(&self) -> usize {
        self.step_executions().len()
    }

    fn mode(&self) -> ActiveStepSearchMode {
        self.run_record()
            .flow_config
            .and_then(|config| config.active_step_search_mode)
            .and_then(|mode| ActiveStepSearchMode::try_from(mode).ok())
            .unwrap_or(ActiveStepSearchMode::Unspecified)
    }

    /// Counts the active Step executions of every row the way search selected them before
    /// the run record kept counts.
    fn recount(&self) -> ExpectedCounts {
        let mut counts = ExpectedCounts::new();
        for step in self.step_executions() {
            let is_active = matches!(
                StepExecutionPhase::try_from(step.phase),
                Ok(StepExecutionPhase::WaitFor
                    | StepExecutionPhase::Waiting
                    | StepExecutionPhase::Execute)
            );
            if step.is_timeout_handler || !is_active {
                continue;
            }
            let count = counts.entry(step.step_type).or_default();
            if step.options.is_some_and(|options| options.skip_wait_for) {
                count.1 += 1;
            } else {
                count.0 += 1;
            }
        }
        counts
    }

    fn stored_counts(&self) -> Option<ExpectedCounts> {
        self.run_record()
            .search_active_step_type_counts
            .map(|counts| {
                counts
                    .step_types
                    .into_iter()
                    .map(|(step_type, count)| {
                        (step_type, (count.with_wait_for, count.without_wait_for))
                    })
                    .collect()
            })
    }

    fn search_document_active_step_types(&self) -> Vec<String> {
        get_search_document(&self.state, self.context.flow_hash, self.run_id)
            .expect("the search document reads")
            .expect("the run has a search document")
            .active_step_types
    }

    fn assert_counts_match_step_executions(&self) {
        let mode = self.mode();
        let recount = self.recount();
        if mode == ActiveStepSearchMode::Disabled {
            assert_eq!(self.stored_counts(), None, "DISABLED keeps no counts");
        } else {
            assert_eq!(
                self.stored_counts().as_ref(),
                Some(&recount),
                "the counts match the Step executions"
            );
        }
        let expected: Vec<String> = recount
            .into_iter()
            .filter(|(_, (with_wait_for, without_wait_for))| match mode {
                ActiveStepSearchMode::Disabled => false,
                ActiveStepSearchMode::EnabledForAll => with_wait_for + without_wait_for > 0,
                ActiveStepSearchMode::Unspecified
                | ActiveStepSearchMode::EnabledForStepsWithWaitFor => *with_wait_for > 0,
            })
            .map(|(step_type, _)| step_type)
            .collect();
        assert_eq!(
            self.search_document_active_step_types(),
            expected,
            "the search document lists the Step types a full count selects"
        );
    }
}

fn settings() -> RunSettings {
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
        history_segment_target_bytes: 4 << 20,
        delete_expired_history_objects: false,
        superseded_value_grace_micros: 0,
    }
}

fn invocation(step_execution_id: &str, method: StepMethod) -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: step_execution_id.to_owned(),
        method: method as i32,
    }
}

fn start_attempt(step_execution_id: &str, method: StepMethod) -> Command {
    Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(invocation(step_execution_id, method)),
        attempt: 1,
        task_id: step_execution_id.as_bytes().to_vec(),
        pull_routing: None,
    })
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn skipping_wait_for() -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    }
}

fn movement(step_type: &str, options: StepOptions) -> StepMovement {
    StepMovement {
        step_type: step_type.to_owned(),
        step_input: Some(string_value(step_type)),
        step_options: Some(options),
        ..StepMovement::default()
    }
}

fn move_to(movements: Vec<StepMovement>) -> StepDecision {
    StepDecision {
        next_steps: movements,
        ..StepDecision::default()
    }
}

fn set_mode(mode: ActiveStepSearchMode) -> Command {
    Command::FlowConfigUpdate(FlowConfigUpdateCommand {
        request: Some(UpdateFlowConfigRequest {
            flow_config: Some(FlowConfig {
                active_step_search_mode: Some(mode as i32),
                ..FlowConfig::default()
            }),
            ..UpdateFlowConfigRequest::default()
        }),
    })
}

fn set_attribute(text: &str) -> Command {
    Command::AttributesSet(AttributesSetCommand {
        request: Some(SetAttributesRequest {
            attributes: vec![AttributeWrite {
                key: "status".to_owned(),
                value: Some(string_value(text)),
                ..AttributeWrite::default()
            }],
            ..SetAttributesRequest::default()
        }),
        ..AttributesSetCommand::default()
    })
}

/// Runs a chain of Steps that skip WaitFor, then moves to one Step with WaitFor and one without.
fn run_chain_then_fork(harness: &mut Harness, chain_length: u32) {
    let mut current = "ChargeStep-1".to_owned();
    for number in 1..=chain_length {
        harness.complete_execute(
            &current,
            move_to(vec![movement("ChainStep", skipping_wait_for())]),
        );
        current = format!("ChainStep-{number}");
    }
    harness.complete_execute(
        &current,
        move_to(vec![
            movement("ShipStep", StepOptions::default()),
            movement("NotifyStep", skipping_wait_for()),
        ]),
    );
}

#[test]
fn disabled_search_keeps_no_counts_and_scans_no_step_executions() {
    let mut harness = Harness::new();
    harness.start(
        Some(ActiveStepSearchMode::Disabled),
        StepOptions::default(),
        FlowTimeoutPolicy::Fail,
    );
    let start = start_attempt("ChargeStep-1", StepMethod::WaitFor);
    assert_eq!(harness.apply_counting_step_scans(start), 0);
    let completion = Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(invocation("ChargeStep-1", StepMethod::WaitFor)),
        attempt: 1,
        task_id: b"ChargeStep-1".to_vec(),
        response: Some(step_method_completed_command::Response::WaitFor(
            InvokeWaitForMethodResponse::default(),
        )),
        ..StepMethodCompletedCommand::default()
    });
    assert_eq!(harness.apply_counting_step_scans(completion), 0);
    let start = start_attempt("ChargeStep-1", StepMethod::Execute);
    assert_eq!(harness.apply_counting_step_scans(start), 0);
    let completion = Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(invocation("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        task_id: b"ChargeStep-1".to_vec(),
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                step_decision: Some(move_to(vec![
                    movement("ShipStep", StepOptions::default()),
                    movement("NotifyStep", skipping_wait_for()),
                ])),
                ..InvokeExecuteMethodResponse::default()
            },
        )),
        ..StepMethodCompletedCommand::default()
    });
    assert_eq!(harness.apply_counting_step_scans(completion), 0);
    assert_eq!(harness.apply_counting_step_scans(set_attribute("paid")), 0);
    assert_eq!(harness.run_record().search_active_step_type_counts, None);
    assert!(harness.search_document_active_step_types().is_empty());
}

#[test]
fn enabling_search_mid_run_counts_step_executions_once() {
    let mut harness = Harness::new();
    harness.start(
        Some(ActiveStepSearchMode::Disabled),
        skipping_wait_for(),
        FlowTimeoutPolicy::Fail,
    );
    run_chain_then_fork(&mut harness, 5);
    assert_eq!(harness.run_record().search_active_step_type_counts, None);
    let step_execution_count = harness.step_execution_count();

    let scanned = harness.apply_counting_step_scans(set_mode(ActiveStepSearchMode::EnabledForAll));
    assert_eq!(
        scanned, step_execution_count,
        "enabling counts every row once"
    );
    assert_eq!(
        harness.search_document_active_step_types(),
        ["NotifyStep", "ShipStep"]
    );

    let scanned = harness
        .apply_counting_step_scans(set_mode(ActiveStepSearchMode::EnabledForStepsWithWaitFor));
    assert_eq!(
        scanned, 0,
        "switching between enabled modes keeps the counts"
    );
    assert_eq!(harness.search_document_active_step_types(), ["ShipStep"]);

    let start = start_attempt("ShipStep-1", StepMethod::WaitFor);
    assert_eq!(harness.apply_counting_step_scans(start), 0);
    assert_eq!(
        harness.apply_counting_step_scans(set_attribute("shipping")),
        0
    );

    let scanned = harness.apply_counting_step_scans(set_mode(ActiveStepSearchMode::Disabled));
    assert_eq!(scanned, 0, "disabling reads no Step execution");
    assert_eq!(harness.run_record().search_active_step_type_counts, None);
    assert!(harness.search_document_active_step_types().is_empty());

    let scanned = harness.apply_counting_step_scans(set_mode(ActiveStepSearchMode::Unspecified));
    assert_eq!(scanned, step_execution_count, "enabling again counts again");
    assert_eq!(harness.search_document_active_step_types(), ["ShipStep"]);
    assert_eq!(
        harness.apply_counting_step_scans(set_attribute("shipped")),
        0
    );
}

#[test]
fn a_run_recorded_without_counts_counts_once_on_its_next_command() {
    let mut harness = Harness::new();
    harness.start(None, skipping_wait_for(), FlowTimeoutPolicy::Fail);
    run_chain_then_fork(&mut harness, 3);
    harness.clear_stored_counts();
    let step_execution_count = harness.step_execution_count();

    let start = start_attempt("ShipStep-1", StepMethod::WaitFor);
    assert_eq!(
        harness.apply_counting_step_scans(start),
        step_execution_count,
        "the first command counts every row"
    );
    assert_eq!(harness.apply_counting_step_scans(set_attribute("paid")), 0);
    assert_eq!(harness.search_document_active_step_types(), ["ShipStep"]);
}

#[test]
fn wait_for_waiting_and_execute_phases_count_as_active() {
    let mut harness = Harness::new();
    harness.start(None, StepOptions::default(), FlowTimeoutPolicy::Fail);
    harness.complete_wait_for(
        "ChargeStep-1",
        Some(WaitingCondition {
            waiting_condition_type: WaitingConditionType::AnyCompleted as i32,
            timer_conditions: vec![TimerCondition {
                condition_id: "deadline".to_owned(),
                duration_seconds: 60,
                firing_unix_timestamp_seconds: 0,
            }],
            ..WaitingCondition::default()
        }),
    );
    assert_eq!(harness.search_document_active_step_types(), ["ChargeStep"]);
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 1,
            kind: Some(timer_id::Kind::WaitCondition(WaitConditionTimerId {
                step_execution_id: "ChargeStep-1".to_owned(),
                timer_index: 0,
            })),
        }],
    }));
    harness.complete_execute(
        "ChargeStep-1",
        move_to(vec![movement("ShipStep", StepOptions::default())]),
    );
    assert_eq!(harness.search_document_active_step_types(), ["ShipStep"]);
}

#[test]
fn time_travel_counts_the_restored_step_executions() {
    let mut harness = Harness::new();
    harness.start(
        Some(ActiveStepSearchMode::EnabledForAll),
        skipping_wait_for(),
        FlowTimeoutPolicy::Fail,
    );
    harness.complete_execute(
        "ChargeStep-1",
        move_to(vec![
            movement("ShipStep", skipping_wait_for()),
            movement("PackStep", skipping_wait_for()),
        ]),
    );
    harness.complete_execute(
        "PackStep-1",
        move_to(vec![movement("LabelStep", StepOptions::default())]),
    );
    assert_eq!(
        harness.search_document_active_step_types(),
        ["LabelStep", "ShipStep"]
    );
    harness.time_travel(ResetFlowRequest {
        flow_id: FLOW_ID.to_owned(),
        reset_type: FlowResetType::StepExecutionId as i32,
        step_execution_id: "ChargeStep-1".to_owned(),
        step_method: FlowResetStepMethod::Execute as i32,
        reason: "test".to_owned(),
        skip_writes_reapply: true,
        ..ResetFlowRequest::default()
    });
    assert_eq!(harness.search_document_active_step_types(), ["ChargeStep"]);
    harness.complete_execute(
        "ChargeStep-1",
        move_to(vec![movement("PackStep", skipping_wait_for())]),
    );
    assert_eq!(harness.search_document_active_step_types(), ["PackStep"]);
}

#[test]
fn canceled_failed_and_closed_step_executions_leave_the_counts() {
    let mut harness = Harness::new();
    harness.start(
        Some(ActiveStepSearchMode::EnabledForAll),
        skipping_wait_for(),
        FlowTimeoutPolicy::Fail,
    );
    harness.complete_execute(
        "ChargeStep-1",
        move_to(vec![
            movement("ShipStep", skipping_wait_for()),
            movement("PackStep", skipping_wait_for()),
            movement("PackStep", StepOptions::default()),
        ]),
    );
    assert_eq!(
        harness.stored_counts(),
        Some(ExpectedCounts::from([
            ("PackStep".to_owned(), (1, 1)),
            ("ShipStep".to_owned(), (0, 1)),
        ]))
    );
    harness.complete_execute(
        "PackStep-1",
        StepDecision {
            cancel_step_types: vec!["PackStep".to_owned()],
            ..StepDecision::default()
        },
    );
    assert_eq!(harness.search_document_active_step_types(), ["ShipStep"]);
    let start = start_attempt("ShipStep-1", StepMethod::Execute);
    harness.apply(start);
    harness.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
        invocation: Some(invocation("ShipStep-1", StepMethod::Execute)),
        attempt: 1,
        kind: StepAttemptFailureKind::WorkerError as i32,
        worker_error: Some(WorkerErrorResponse {
            detail: "carrier unavailable".to_owned(),
            ..WorkerErrorResponse::default()
        }),
        is_non_retryable: true,
        ..StepAttemptFailedCommand::default()
    }));
    assert!(harness.search_document_active_step_types().is_empty());
    // The stored record decodes empty counts as present, so later commands keep adjusting them.
    assert_eq!(
        harness.run_record().search_active_step_type_counts,
        Some(ActiveStepTypeCounts::default())
    );
}

#[test]
fn a_flow_stop_ends_every_counted_step_execution() {
    let mut harness = Harness::new();
    harness.start(None, StepOptions::default(), FlowTimeoutPolicy::Fail);
    harness.complete_wait_for("ChargeStep-1", None);
    harness.complete_execute(
        "ChargeStep-1",
        move_to(vec![
            movement("ShipStep", StepOptions::default()),
            movement("PackStep", StepOptions::default()),
        ]),
    );
    assert_eq!(
        harness.search_document_active_step_types(),
        ["PackStep", "ShipStep"]
    );
    harness.apply(Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            stop_type: StopType::Terminate as i32,
            ..StopFlowRequest::default()
        }),
    }));
    assert_eq!(harness.stored_counts(), Some(ExpectedCounts::new()));
}

#[test]
fn the_flow_timeout_handler_is_never_counted() {
    let mut harness = Harness::new();
    harness.start(
        Some(ActiveStepSearchMode::EnabledForAll),
        skipping_wait_for(),
        FlowTimeoutPolicy::Handler,
    );
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 1,
            kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
        }],
    }));
    assert!(
        harness
            .step_executions()
            .iter()
            .any(|step| step.is_timeout_handler),
        "the timeout fired its handler"
    );
    assert_eq!(harness.search_document_active_step_types(), ["ChargeStep"]);
    harness.complete_execute("ChargeStep-1", StepDecision::default());
    assert_eq!(harness.stored_counts(), Some(ExpectedCounts::new()));
    assert_eq!(
        harness.apply_counting_step_scans(set_attribute("timed out")),
        0,
        "empty counts are kept, not counted again"
    );
}

/// Deterministic draws for the random command sequences (xorshift64*).
struct SeededDraws(u64);

impl SeededDraws {
    fn below(&mut self, bound: usize) -> usize {
        self.0 ^= self.0 >> 12;
        self.0 ^= self.0 << 25;
        self.0 ^= self.0 >> 27;
        let draw = self.0.wrapping_mul(0x2545_f491_4f6c_dd1d) >> 32;
        usize::try_from(draw).expect("a 32-bit draw fits in usize") % bound
    }

    fn is_drawn(&mut self, percent: usize) -> bool {
        self.below(100) < percent
    }
}

const RANDOM_STEP_TYPES: &[&str] = &["ShipStep", "PackStep", "NotifyStep"];
const RANDOM_MODES: &[ActiveStepSearchMode] = &[
    ActiveStepSearchMode::Disabled,
    ActiveStepSearchMode::Unspecified,
    ActiveStepSearchMode::EnabledForAll,
    ActiveStepSearchMode::EnabledForStepsWithWaitFor,
];

fn random_options(draws: &mut SeededDraws) -> StepOptions {
    if draws.is_drawn(50) {
        skipping_wait_for()
    } else {
        StepOptions::default()
    }
}

/// Moves to one or two random Steps, sometimes canceling a random Step type.
fn random_decision(draws: &mut SeededDraws) -> StepDecision {
    let next_steps = (0..=draws.below(2))
        .map(|_| {
            let step_type = RANDOM_STEP_TYPES[draws.below(RANDOM_STEP_TYPES.len())];
            movement(step_type, random_options(draws))
        })
        .collect();
    let cancel_step_types = if draws.is_drawn(15) {
        vec![RANDOM_STEP_TYPES[draws.below(RANDOM_STEP_TYPES.len())].to_owned()]
    } else {
        Vec::new()
    };
    StepDecision {
        next_steps,
        cancel_step_types,
        ..StepDecision::default()
    }
}

/// Applies one random command; every Step it completes moves on, so the run stays open.
fn apply_random_command(harness: &mut Harness, draws: &mut SeededDraws) {
    let steps = harness.step_executions();
    let draw = draws.below(100);
    if draw < 15 {
        let mode = RANDOM_MODES[draws.below(RANDOM_MODES.len())];
        harness.apply(set_mode(mode));
        return;
    }
    if draw < 20 {
        harness.apply(set_attribute("random"));
        return;
    }
    let completed: Vec<&StepExecutionRecord> = steps
        .iter()
        .filter(|step| step.phase == StepExecutionPhase::Completed as i32)
        .collect();
    if draw < 25 && !completed.is_empty() {
        let target = completed[draws.below(completed.len())];
        harness.time_travel(ResetFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            reset_type: FlowResetType::StepExecutionId as i32,
            step_execution_id: target.step_execution_id.clone(),
            step_method: FlowResetStepMethod::Execute as i32,
            reason: "random".to_owned(),
            skip_writes_reapply: draws.is_drawn(50),
            ..ResetFlowRequest::default()
        });
        return;
    }
    let active: Vec<&StepExecutionRecord> = steps
        .iter()
        .filter(|step| {
            step.phase == StepExecutionPhase::WaitFor as i32
                || step.phase == StepExecutionPhase::Execute as i32
        })
        .collect();
    let step = active[draws.below(active.len())];
    if step.phase == StepExecutionPhase::WaitFor as i32 {
        harness.complete_wait_for(&step.step_execution_id, None);
    } else {
        let decision = random_decision(draws);
        harness.complete_execute(&step.step_execution_id, decision);
    }
}

/// Mode switches, time travel, cancellation, and Step moves in seeded random order.
#[test]
fn random_command_sequences_keep_counts_equal_to_a_full_count() {
    for seed in 1..=24_u64 {
        let mut draws = SeededDraws(seed.wrapping_mul(0x9e37_79b9_7f4a_7c15) | 1);
        let mut harness = Harness::new();
        let mode = RANDOM_MODES[draws.below(RANDOM_MODES.len())];
        let options = random_options(&mut draws);
        harness.start(Some(mode), options, FlowTimeoutPolicy::Fail);
        for _ in 0..60 {
            apply_random_command(&mut harness, &mut draws);
        }
    }
}

/// gRPC code of a command rejected by the run-state limit.
const RESOURCE_EXHAUSTED: i32 = 8;
/// A limit no test state reaches, encoded in as many bytes as the measured limits.
const UNREACHED_MAX_RUN_STATE_BYTES: u64 = 16_383;

fn settings_with_max_run_state_bytes(max_run_state_bytes: u64) -> RunSettings {
    RunSettings {
        max_run_state_bytes,
        ..settings()
    }
}

fn is_resource_exhausted(outcome: &ApplyOutcome) -> bool {
    matches!(
        &outcome.reply.reply,
        Some(command_reply::Reply::Error(error)) if error.grpc_code == RESOURCE_EXHAUSTED
    )
}

/// Starts a run whose Step skips WaitFor, so DISABLED and UNSPECIFIED write the same document.
fn start_skipping_wait_for(
    harness: &mut Harness,
    mode: ActiveStepSearchMode,
    max_run_state_bytes: u64,
) -> ApplyOutcome {
    let start = harness.start_command(
        Some(mode),
        skipping_wait_for(),
        FlowTimeoutPolicy::Fail,
        settings_with_max_run_state_bytes(max_run_state_bytes),
    );
    harness.try_apply(start)
}

#[test]
fn an_enabled_search_start_fits_the_run_state_limit_of_a_disabled_one() {
    let mut disabled = Harness::new();
    start_skipping_wait_for(
        &mut disabled,
        ActiveStepSearchMode::Disabled,
        UNREACHED_MAX_RUN_STATE_BYTES,
    );
    let needed_bytes = disabled.active_state_bytes();
    assert!(needed_bytes >= 128, "the limit keeps its two-byte encoding");

    let mut at_limit = Harness::new();
    let outcome = start_skipping_wait_for(
        &mut at_limit,
        ActiveStepSearchMode::Unspecified,
        needed_bytes,
    );
    assert!(
        matches!(
            outcome.reply.reply,
            Some(command_reply::Reply::StartFlow(_))
        ),
        "a start at exactly the limit applies: {:?}",
        outcome.reply
    );
    assert_eq!(
        at_limit.stored_counts(),
        Some(ExpectedCounts::from([("ChargeStep".to_owned(), (0, 1))])),
        "the run keeps counts"
    );
    assert_eq!(at_limit.active_state_bytes(), needed_bytes);

    let mut over_limit = Harness::new();
    let outcome = start_skipping_wait_for(
        &mut over_limit,
        ActiveStepSearchMode::Unspecified,
        needed_bytes - 1,
    );
    assert!(is_resource_exhausted(&outcome), "{:?}", outcome.reply);
}

#[test]
fn counting_a_run_recorded_without_counts_never_pushes_it_over_its_limit() {
    let run_to_probe = |harness: &mut Harness, mode, max_run_state_bytes| {
        start_skipping_wait_for(harness, mode, max_run_state_bytes);
        let mut current = "ChargeStep-1".to_owned();
        for number in 1..=3 {
            harness.complete_execute(
                &current,
                move_to(vec![movement("ChainStep", skipping_wait_for())]),
            );
            current = format!("ChainStep-{number}");
        }
    };
    let mut disabled = Harness::new();
    run_to_probe(
        &mut disabled,
        ActiveStepSearchMode::Disabled,
        UNREACHED_MAX_RUN_STATE_BYTES,
    );
    disabled.apply(set_attribute("paid"));
    let needed_bytes = disabled.active_state_bytes();
    assert!(needed_bytes >= 128, "the limit keeps its two-byte encoding");

    let mut at_limit = Harness::new();
    run_to_probe(
        &mut at_limit,
        ActiveStepSearchMode::Unspecified,
        needed_bytes,
    );
    at_limit.clear_stored_counts();
    let step_execution_count = at_limit.step_execution_count();
    assert_eq!(
        at_limit.apply_counting_step_scans(set_attribute("paid")),
        step_execution_count,
        "the write at exactly the limit applies and counts every row"
    );
    assert_eq!(at_limit.active_state_bytes(), needed_bytes);

    let mut over_limit = Harness::new();
    run_to_probe(
        &mut over_limit,
        ActiveStepSearchMode::Unspecified,
        needed_bytes - 1,
    );
    over_limit.clear_stored_counts();
    let outcome = over_limit.try_apply(set_attribute("paid"));
    assert!(is_resource_exhausted(&outcome), "{:?}", outcome.reply);
}
