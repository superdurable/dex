// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deletion of the StepExecution rows of Steps that completed or were canceled without a close.

use std::collections::BTreeMap;

use dexzd_apply::diagnostics::{current_semantic_point_hash, time_travel_target_hash};
use dexzd_apply::read_model::{StepCompletionState, step_completion_state};
use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_time_travel_for_test,
};
use dexzd_keyspace::row_keys::{
    attempt_record_key, checkpoint_key, prefix_range, run_named_key, run_singleton_key,
};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_protocol::dex::{
    CloseDecision, CloseDecisionType, ExecuteMethodFailurePolicy, FlowResetType, FlowStartOptions,
    FlowTimeoutHandlerOptions, FlowTimeoutPolicy, InvokeExecuteMethodResponse, InvokeRpcRequest,
    InvokeWorkerRpcResponse, ResetFlowRequest, StartFlowRequest, StepDecision, StepMovement,
    StepOptions, StopFlowRequest, StopType, Value, WorkerErrorResponse, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttemptRecord, CheckpointRecord, CheckpointRow, FlowCommand, FlowStartCommand, FlowStopCommand,
    FlowTimeoutTimerId, RpcCommittedCommand, RunRecord, RunSettings, StepAttemptFailedCommand,
    StepAttemptFailureKind, StepAttemptQueuedCommand, StepAttemptStartedCommand,
    StepExecutionPhase, StepExecutionRecord, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, TimeTravelCommand, TimerId, TimersFiredCommand, command_reply,
    flow_command::Command, step_method_completed_command, timer_id,
};
use prost::Message;

const FLOW_ID: &str = "order-1";
const RUN_ID: RunId = RunId([1; 16]);

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
    now_micros: i64,
    settings: RunSettings,
}

impl Harness {
    fn new() -> Self {
        Self::with_settings(RunSettings::default())
    }

    fn with_settings(overrides: RunSettings) -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, FLOW_ID);
        Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000_000_000,
            settings: RunSettings {
                wire_inline_threshold_bytes: 100,
                max_value_bytes: 16 << 20,
                retention_after_close_micros: 7 * 24 * 3600 * 1_000_000,
                request_dedup_capacity: 1000,
                max_run_state_bytes: 1 << 30,
                max_active_step_executions: 10_000,
                history_window_events: 10_000,
                history_prune_chunk_events: 1000,
                max_retained_superseded_bytes: 256 << 20,
                ..overrides
            },
        }
    }

    fn command(&mut self, body: Command) -> FlowCommand {
        self.now_micros += 1000;
        let is_start = matches!(body, Command::FlowStart(_));
        FlowCommand {
            command_id: self.now_micros.to_be_bytes().to_vec(),
            flow_id: FLOW_ID.to_owned(),
            run_id: if is_start {
                Vec::new()
            } else {
                RUN_ID.as_bytes().to_vec()
            },
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            tenant_id: 0,
            command: Some(body),
            ..FlowCommand::default()
        }
    }

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
            apply_flow_command(&self.context, &self.state, &command).expect("the command applies");
        let repeated = apply_flow_command(&self.context, &self.state, &command)
            .expect("the same command applies again");
        assert_eq!(outcome.writes, repeated.writes);
        self.state.commit(&outcome);
        outcome
    }

    fn start(&mut self, timeout_policy: Option<FlowTimeoutPolicy>) {
        let request = StartFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            flow_type: "OrderFlow".to_owned(),
            start_step_type: "ChargeStep".to_owned(),
            step_input: Some(string_value("input")),
            step_options: Some(skip_wait_for()),
            flow_timeout_seconds: if timeout_policy.is_some() { 30 } else { 0 },
            request_id: "start-1".to_owned(),
            flow_start_options: Some(FlowStartOptions {
                timeout_handler_options: Some(FlowTimeoutHandlerOptions::default()),
                ..FlowStartOptions::default()
            }),
            ..StartFlowRequest::default()
        };
        let outcome = self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: RUN_ID.as_bytes().to_vec(),
            request: Some(request),
            settings: Some(self.settings),
            resolved_timeout_policy: timeout_policy.unwrap_or(FlowTimeoutPolicy::Fail) as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..FlowStartCommand::default()
        }));
        assert_not_rejected(&outcome, "the run starts");
    }

    fn start_attempt(&mut self, step_execution_id: &str, method: StepMethod) {
        let outcome = self.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(invocation_key(step_execution_id, method)),
            attempt: 1,
            task_id: vec![2; 16],
            pull_routing: None,
        }));
        assert_not_rejected(&outcome, "the attempt starts");
    }

    fn execute_result(step_execution_id: &str, decision: StepDecision) -> Command {
        Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation_key(step_execution_id, StepMethod::Execute)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(decision),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        })
    }

    /// Starts and completes the Execute of `step_execution_id` with `decision`.
    fn execute(&mut self, step_execution_id: &str, decision: StepDecision) -> ApplyOutcome {
        self.start_attempt(step_execution_id, StepMethod::Execute);
        let outcome = self.apply(Self::execute_result(step_execution_id, decision));
        assert_not_rejected(&outcome, "the Execute completes");
        outcome
    }

    fn fail_execute(&mut self, step_execution_id: &str) {
        self.start_attempt(step_execution_id, StepMethod::Execute);
        let outcome = self.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
            invocation: Some(invocation_key(step_execution_id, StepMethod::Execute)),
            attempt: 1,
            kind: StepAttemptFailureKind::WorkerError as i32,
            worker_error: Some(WorkerErrorResponse {
                detail: "unrecoverable".to_owned(),
                ..WorkerErrorResponse::default()
            }),
            is_non_retryable: true,
            ..StepAttemptFailedCommand::default()
        }));
        assert_not_rejected(&outcome, "the failure applies");
    }

    fn stop(&mut self, stop_type: StopType) {
        let outcome = self.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: stop_type as i32,
                ..StopFlowRequest::default()
            }),
        }));
        assert_not_rejected(&outcome, "the stop applies");
    }

    fn time_travel_command(&mut self, timestamp_micros: i64) -> FlowCommand {
        self.command(Command::TimeTravel(TimeTravelCommand {
            request: Some(ResetFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                reason: "test".to_owned(),
                history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
                ..ResetFlowRequest::default()
            }),
            ..TimeTravelCommand::default()
        }))
    }

    fn travel_to(&mut self, timestamp_micros: i64) {
        let command = self.time_travel_command(timestamp_micros);
        let outcome = self.apply_command(command);
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "time travel was rejected: {:?}",
            outcome.reply
        );
    }

    fn step_key(&self, step_execution_id: &str) -> Vec<u8> {
        run_named_key(
            self.context.flow_hash,
            RUN_ID,
            RunTable::StepExecution,
            step_execution_id,
        )
    }

    fn step(&self, step_execution_id: &str) -> Option<StepExecutionRecord> {
        self.state
            .rows
            .get(&(ColumnFamily::Flow, self.step_key(step_execution_id)))
            .map(|bytes| StepExecutionRecord::decode(bytes.as_slice()).expect("valid Step row"))
    }

    /// Step execution IDs that have a row, in key order.
    fn step_ids(&self) -> Vec<String> {
        let prefix = run_table_prefix(self.context.flow_hash, RUN_ID, RunTable::StepExecution);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|((_, key), _)| {
                String::from_utf8(key[prefix.len()..].to_vec()).expect("Step IDs are UTF-8")
            })
            .collect()
    }

    fn run_record(&self) -> RunRecord {
        let key = run_singleton_key(self.context.flow_hash, RUN_ID, RunTable::RunRecord);
        RunRecord::decode(self.state.rows[&(ColumnFamily::Flow, key)].as_slice())
            .expect("valid run row")
    }

    fn completion(&self, step_type: &str, number: u32) -> StepCompletionState {
        step_completion_state(
            &self.state,
            self.context.flow_hash,
            RUN_ID,
            step_type,
            number,
        )
        .expect("the Step completion state reads")
    }

    /// The latest checkpoint of the current attempt and its encoded size.
    fn latest_checkpoint(&self) -> (CheckpointRecord, usize) {
        let attempt = self.run_record().current_attempt;
        let attempt_key = attempt_record_key(self.context.flow_hash, RUN_ID, attempt);
        let attempt_record =
            AttemptRecord::decode(self.state.rows[&(ColumnFamily::Flow, attempt_key)].as_slice())
                .expect("valid attempt row");
        let seq = *attempt_record
            .checkpoint_seqs
            .last()
            .expect("the attempt has a checkpoint");
        let key = checkpoint_key(self.context.flow_hash, RUN_ID, attempt, seq);
        let bytes = &self.state.rows[&(ColumnFamily::History, key)];
        (
            CheckpointRecord::decode(bytes.as_slice()).expect("valid checkpoint"),
            bytes.len(),
        )
    }

    /// The hash of the semantic point that the last command recorded.
    fn last_semantic_point_hash(&self, command: &FlowCommand) -> [u8; 16] {
        current_semantic_point_hash(&self.context, &self.state, RUN_ID, &command.command_id)
            .expect("the hash reads")
            .expect("the command recorded a semantic point")
            .state_hash
    }
}

fn checkpoint_step_ids(checkpoint: &CheckpointRecord) -> Vec<String> {
    checkpoint
        .rows
        .iter()
        .filter(|row| row.table == u32::from(RunTable::StepExecution as u8))
        .map(|row| String::from_utf8(row.key_suffix.clone()).expect("Step IDs are UTF-8"))
        .collect()
}

fn assert_not_rejected(outcome: &ApplyOutcome, what: &str) {
    assert!(
        !matches!(outcome.reply.reply, Some(command_reply::Reply::Error(_))),
        "{what}: {:?}",
        outcome.reply
    );
}

fn error_code(outcome: &ApplyOutcome) -> Option<i32> {
    match &outcome.reply.reply {
        Some(command_reply::Reply::Error(error)) => Some(error.grpc_code),
        _ => None,
    }
}

fn invocation_key(step_execution_id: &str, method: StepMethod) -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: step_execution_id.to_owned(),
        method: method as i32,
    }
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn skip_wait_for() -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    }
}

fn go_to(step_types: &[&str]) -> StepDecision {
    StepDecision {
        next_steps: step_types
            .iter()
            .map(|step_type| StepMovement {
                step_type: (*step_type).to_owned(),
                step_options: Some(skip_wait_for()),
                ..StepMovement::default()
            })
            .collect(),
        ..StepDecision::default()
    }
}

fn close_decision(close_decision_type: CloseDecisionType) -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: close_decision_type as i32,
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    }
}

#[test]
fn completed_and_plainly_canceled_steps_leave_no_row_and_answer_completed() {
    let mut harness = Harness::new();
    harness.start(None);
    harness.execute(
        "ChargeStep-1",
        go_to(&["ShipStep", "PackStep", "DeciderStep"]),
    );
    assert!(harness.step("ChargeStep-1").is_none());
    harness.execute(
        "DeciderStep-1",
        StepDecision {
            cancel_step_types: vec!["PackStep".to_owned()],
            ..StepDecision::default()
        },
    );
    assert_eq!(harness.step_ids(), ["ShipStep-1"]);
    for step_type in ["ChargeStep", "PackStep", "DeciderStep"] {
        assert_eq!(
            harness.completion(step_type, 1),
            StepCompletionState::Completed
        );
    }
    harness.stop(StopType::Terminate);
    assert_eq!(
        harness
            .step("ShipStep-1")
            .map(|step| step.is_canceled_by_flow_close),
        Some(Some(true)),
        "the termination keeps the row of the Step it canceled"
    );
    assert_eq!(
        harness.completion("PackStep", 1),
        StepCompletionState::Completed,
        "a Step canceled before the termination stays completed"
    );
    assert_eq!(
        harness.completion("ShipStep", 1),
        StepCompletionState::EndedWithoutCompletion
    );
}

#[test]
fn failed_and_close_canceled_steps_keep_their_rows() {
    let mut harness = Harness::new();
    harness.start(None);
    let recovering = StepOptions {
        execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
        execute_failure_proceed_step_type: "RecoverStep".to_owned(),
        execute_failure_proceed_step_options: Some(Box::new(skip_wait_for())),
        ..skip_wait_for()
    };
    harness.execute(
        "ChargeStep-1",
        StepDecision {
            next_steps: vec![
                StepMovement {
                    step_type: "RiskyStep".to_owned(),
                    step_options: Some(recovering),
                    ..StepMovement::default()
                },
                StepMovement {
                    step_type: "WaitingStep".to_owned(),
                    ..StepMovement::default()
                },
            ],
            ..StepDecision::default()
        },
    );
    harness.fail_execute("RiskyStep-1");
    let failed = harness
        .step("RiskyStep-1")
        .expect("a Failed Step keeps its row");
    assert_eq!(failed.phase, StepExecutionPhase::Failed as i32);
    harness.execute(
        "RecoverStep-1",
        close_decision(CloseDecisionType::ForceComplete),
    );
    assert_eq!(harness.step_ids(), ["RiskyStep-1", "WaitingStep-1"]);
    let canceled = harness
        .step("WaitingStep-1")
        .expect("a Step the close canceled keeps its row");
    assert_eq!(canceled.phase, StepExecutionPhase::Canceled as i32);
    assert_eq!(canceled.is_canceled_by_flow_close, Some(true));
}

#[test]
fn the_flow_timeout_handler_keeps_its_row_after_it_completes() {
    let mut harness = Harness::new();
    harness.start(Some(FlowTimeoutPolicy::Handler));
    let outcome = harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 1,
            kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
        }],
    }));
    assert_not_rejected(&outcome, "the Flow timeout fires");
    harness.execute("sys:timeout_handler-1", StepDecision::default());
    let handler = harness
        .step("sys:timeout_handler-1")
        .expect("the handler keeps its row");
    assert_eq!(handler.phase, StepExecutionPhase::Completed as i32);
}

#[test]
fn execution_numbers_continue_after_rows_are_deleted() {
    let mut harness = Harness::new();
    harness.start(None);
    harness.execute("ChargeStep-1", go_to(&["LoopStep"]));
    for number in 1..=5 {
        harness.execute(&format!("LoopStep-{number}"), go_to(&["LoopStep"]));
        assert_eq!(harness.step_ids(), [format!("LoopStep-{}", number + 1)]);
    }
    for number in 1..=5 {
        assert_eq!(
            harness.completion("LoopStep", number),
            StepCompletionState::Completed
        );
    }
    assert_eq!(
        harness.completion("LoopStep", 6),
        StepCompletionState::NotCompleted
    );
    assert_eq!(
        harness.completion("LoopStep", 7),
        StepCompletionState::NotCompleted
    );
}

#[test]
fn late_results_and_queue_entries_for_a_deleted_step_are_stale() {
    let mut harness = Harness::new();
    harness.start(None);
    harness.execute("ChargeStep-1", go_to(&["ShipStep"]));
    let late_result = harness.apply(Harness::execute_result(
        "ChargeStep-1",
        StepDecision::default(),
    ));
    assert!(
        late_result.writes.is_empty(),
        "a late result writes nothing"
    );
    let late_queue = harness.apply(Command::StepAttemptQueued(StepAttemptQueuedCommand {
        invocation: Some(invocation_key("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        ..StepAttemptQueuedCommand::default()
    }));
    assert!(late_queue.writes.is_empty());
    assert_eq!(
        error_code(&late_queue),
        Some(9),
        "the attempt is no longer pending"
    );
    let late_start = harness.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(invocation_key("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        task_id: vec![3; 16],
        pull_routing: None,
    }));
    assert!(late_start.writes.is_empty());
    assert_eq!(harness.step_ids(), ["ShipStep-1"]);
}

#[test]
fn checkpoints_hold_only_the_rows_that_remain() {
    let mut harness = Harness::with_settings(RunSettings {
        checkpoint_command_threshold: 1,
        ..RunSettings::default()
    });
    harness.start(None);
    harness.execute("ChargeStep-1", go_to(&["LoopStep", "ParkedStep"]));
    for number in 1..=20 {
        harness.execute(&format!("LoopStep-{number}"), go_to(&["LoopStep"]));
    }
    let (checkpoint, _) = harness.latest_checkpoint();
    assert_eq!(
        checkpoint_step_ids(&checkpoint),
        ["LoopStep-21", "ParkedStep-1"]
    );
}

#[test]
fn a_rebuild_deletes_the_rows_that_live_apply_deleted() {
    let mut harness = Harness::with_settings(RunSettings {
        checkpoint_row_write_bytes_threshold: 1 << 30,
        ..RunSettings::default()
    });
    harness.start(None);
    harness.execute(
        "ChargeStep-1",
        go_to(&["LoopStep", "ParkedStep", "DeciderStep"]),
    );
    harness.execute("LoopStep-1", go_to(&["LoopStep"]));
    harness.execute(
        "DeciderStep-1",
        StepDecision {
            cancel_step_types: vec!["ParkedStep".to_owned()],
            ..StepDecision::default()
        },
    );
    harness.start_attempt("LoopStep-2", StepMethod::Execute);
    let deciding = harness.command(Harness::execute_result("LoopStep-2", go_to(&["LoopStep"])));
    harness.apply_command(deciding.clone());
    let target_micros = harness.now_micros;
    let live_hash = harness.last_semantic_point_hash(&deciding);
    assert_eq!(harness.step_ids(), ["LoopStep-3"]);
    harness.execute("LoopStep-3", go_to(&["LoopStep"]));

    let travel = harness.time_travel_command(target_micros);
    let rebuilt = time_travel_target_hash(&harness.context, &harness.state, &travel)
        .expect("the rebuild runs")
        .expect("the target exists");
    assert_eq!(
        rebuilt.state_hash, live_hash,
        "replay from the start checkpoint deletes the same rows"
    );
    harness.apply_command(travel);
    assert_eq!(harness.step_ids(), ["LoopStep-3"]);
}

#[test]
fn time_travel_deletes_the_ended_rows_it_restores_from_older_checkpoints() {
    let mut harness = Harness::with_settings(RunSettings {
        checkpoint_command_threshold: 1,
        ..RunSettings::default()
    });
    harness.start(None);
    harness.start_attempt("ChargeStep-1", StepMethod::Execute);
    let deciding = harness.command(Harness::execute_result(
        "ChargeStep-1",
        go_to(&["ShipStep"]),
    ));
    harness.apply_command(deciding.clone());
    let target_micros = harness.now_micros;
    let live_hash = harness.last_semantic_point_hash(&deciding);
    // A binary from before this change kept the Completed row in state and checkpoint.
    let legacy = StepExecutionRecord {
        step_execution_id: "ChargeStep-1".to_owned(),
        step_type: "ChargeStep".to_owned(),
        step_number: 1,
        phase: StepExecutionPhase::Completed as i32,
        completed_seq: harness.run_record().next_run_seq - 1,
        ..StepExecutionRecord::default()
    };
    let (mut checkpoint, _) = harness.latest_checkpoint();
    checkpoint.rows.insert(
        0,
        CheckpointRow {
            table: u32::from(RunTable::StepExecution as u8),
            key_suffix: b"ChargeStep-1".to_vec(),
            value: legacy.encode_to_vec(),
        },
    );
    let checkpoint_key = checkpoint_key(
        harness.context.flow_hash,
        RUN_ID,
        checkpoint.attempt,
        checkpoint.seq,
    );
    harness.state.rows.insert(
        (ColumnFamily::History, checkpoint_key),
        checkpoint.encode_to_vec(),
    );
    harness.execute("ShipStep-1", go_to(&["ShipStep"]));

    // The rebuild and the staged result hold the older Completed row; live state never did.
    let travel = harness.time_travel_command(target_micros);
    let rebuilt = time_travel_target_hash(&harness.context, &harness.state, &travel)
        .expect("the rebuild runs")
        .expect("the target exists");
    assert_eq!(rebuilt.state_hash, live_hash);
    let mut staged = stage_time_travel_for_test(&harness.context, &harness.state, &travel)
        .expect("time travel computes")
        .expect("the request is valid")
        .commands;
    let staged_travel = staged.pop().expect("staging ends with the TimeTravel");
    for stage in staged {
        harness.apply_command(stage);
    }
    let staged_target = time_travel_target_hash(&harness.context, &harness.state, &staged_travel)
        .expect("the staged result reads")
        .expect("the staged target exists");
    assert_eq!(
        staged_target.state_hash, live_hash,
        "the debug check of a staged result an older owner computed matches live state"
    );
    harness.apply_command(staged_travel);
    assert_eq!(harness.run_record().current_attempt, 2);
    assert_eq!(harness.step_ids(), ["ShipStep-1"]);
    let (restored, _) = harness.latest_checkpoint();
    assert_eq!(checkpoint_step_ids(&restored), ["ShipStep-1"]);
    assert_eq!(
        harness.completion("ChargeStep", 1),
        StepCompletionState::Completed
    );
}

#[test]
fn a_reapplied_rpc_cancellation_deletes_the_row_and_keeps_its_event_links() {
    let mut harness = Harness::new();
    harness.start(None);
    harness.execute("ChargeStep-1", go_to(&["WaitingStep"]));
    let target_micros = harness.now_micros;
    let rpc = Command::RpcCommitted(RpcCommittedCommand {
        request: Some(InvokeRpcRequest {
            flow_id: FLOW_ID.to_owned(),
            rpc_name: "updateOrder".to_owned(),
            request_id: "rpc-1".to_owned(),
            ..InvokeRpcRequest::default()
        }),
        response: Some(InvokeWorkerRpcResponse {
            step_decision: Some(StepDecision {
                cancel_step_types: vec!["WaitingStep".to_owned()],
                ..go_to(&["FollowupStep"])
            }),
            ..InvokeWorkerRpcResponse::default()
        }),
        app_worker_id: "worker-rpc".to_owned(),
        request_fingerprint: vec![1],
        ..RpcCommittedCommand::default()
    });
    let outcome = harness.apply(rpc);
    assert_not_rejected(&outcome, "the RPC commits");
    assert_eq!(harness.step_ids(), ["FollowupStep-1"]);
    let live_followup = harness.step("FollowupStep-1").expect("FollowupStep-1 runs");
    assert!(live_followup.from_event_id > 0);

    harness.travel_to(target_micros);
    assert_eq!(
        harness.step_ids(),
        ["FollowupStep-1"],
        "the reapplied RPC cancels WaitingStep-1 again"
    );
    let reapplied_followup = harness.step("FollowupStep-1").expect("FollowupStep-1 runs");
    assert!(
        reapplied_followup.from_event_id > live_followup.from_event_id,
        "the TimeTravel resolves the reapplied RPC's link to its own event"
    );
    assert_eq!(
        harness.completion("WaitingStep", 1),
        StepCompletionState::Completed
    );
}
