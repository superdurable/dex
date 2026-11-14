// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! WaitForStepCompletion answers for every way a Step and its Flow can end.

use std::collections::BTreeMap;

use dexzd_apply::read_model::{StepCompletionState, get_flow_summary, step_completion_state};
use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_time_travel_for_test,
};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, TenantId};
use dexzd_protocol::dex::{
    CloseDecision, CloseDecisionType, ExecuteMethodFailurePolicy, FlowResetType, FlowStartOptions,
    FlowStatus, FlowTimeoutHandlerOptions, FlowTimeoutPolicy, IdReusePolicy,
    InvokeExecuteMethodResponse, InvokeWaitForMethodResponse, ResetFlowRequest, StartFlowRequest,
    StepDecision, StepMovement, StepOptions, StopFlowRequest, StopType, Value, WorkerErrorResponse,
    WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowStartCommand, FlowStopCommand, FlowTimeoutTimerId, PendingCloseCheckCommand,
    RunSettings, StepAttemptFailedCommand, StepAttemptFailureKind, StepAttemptStartedCommand,
    StepInvocationKey, StepMethod, StepMethodCompletedCommand, TimeTravelCommand, TimerId,
    TimersFiredCommand, command_reply, flow_command::Command, step_method_completed_command,
    timer_id,
};

use StepCompletionState::{Completed, EndedWithoutCompletion, NotCompleted};

const FLOW_ID: &str = "order-1";
const FIRST_RUN: RunId = RunId([1; 16]);
const SECOND_RUN: RunId = RunId([2; 16]);

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

/// How the run's Flow timeout is configured.
#[derive(Debug, Clone, Copy)]
enum FlowTimeout {
    None,
    Policy(FlowTimeoutPolicy),
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    run_id: RunId,
    now_micros: i64,
    is_transactional_rpc_in_flight: bool,
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
            run_id: FIRST_RUN,
            now_micros: 1_800_000_000_000_000,
            is_transactional_rpc_in_flight: false,
        }
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        self.now_micros += 1000;
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
            tenant_id: 0,
            command: Some(body),
            is_transactional_rpc_in_flight: self.is_transactional_rpc_in_flight,
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
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("the command applies");
        let repeated = apply_flow_command(&self.context, &self.state, &command)
            .expect("the same command applies again");
        assert_eq!(outcome.writes, repeated.writes);
        self.state.commit(&outcome);
        outcome
    }

    fn start_flow(
        &mut self,
        run_id: RunId,
        timeout: FlowTimeout,
        id_reuse_policy: IdReusePolicy,
    ) -> ApplyOutcome {
        let (flow_timeout_seconds, resolved_timeout_policy) = match timeout {
            FlowTimeout::None => (0, FlowTimeoutPolicy::Fail),
            FlowTimeout::Policy(policy) => (30, policy),
        };
        let request = StartFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            flow_type: "OrderFlow".to_owned(),
            start_step_type: "ChargeStep".to_owned(),
            step_input: Some(string_value("input")),
            step_options: Some(skip_wait_for()),
            flow_timeout_seconds,
            request_id: format!("start-{}", run_id.as_bytes()[0]),
            flow_start_options: Some(FlowStartOptions {
                id_reuse_policy: id_reuse_policy as i32,
                timeout_handler_options: Some(FlowTimeoutHandlerOptions::default()),
                ..FlowStartOptions::default()
            }),
            ..StartFlowRequest::default()
        };
        self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: run_id.as_bytes().to_vec(),
            request: Some(request),
            settings: Some(RunSettings {
                wire_inline_threshold_bytes: 100,
                max_value_bytes: 16 << 20,
                retention_after_close_micros: 7 * 24 * 3600 * 1_000_000,
                request_dedup_capacity: 1000,
                max_run_state_bytes: 1 << 30,
                max_active_step_executions: 10_000,
                history_window_events: 10_000,
                history_prune_chunk_events: 1000,
                max_retained_superseded_bytes: 256 << 20,
                ..RunSettings::default()
            }),
            resolved_timeout_policy: resolved_timeout_policy as i32,
            worker_target: Some(WorkerTarget {
                is_pull_mode: true,
                ..WorkerTarget::default()
            }),
            ..FlowStartCommand::default()
        }))
    }

    /// Starts the common run: two Steps completed, ActiveStep-1 in WaitFor, DeciderStep-1 awaiting Execute.
    fn open_run(&mut self, timeout: FlowTimeout, decider_options: StepOptions) {
        self.start_flow(FIRST_RUN, timeout, IdReusePolicy::Unspecified);
        self.start_attempt("ChargeStep-1", StepMethod::Execute);
        self.complete_execute(
            "ChargeStep-1",
            StepDecision {
                next_steps: vec![
                    movement("DoneStep", skip_wait_for()),
                    movement("ActiveStep", StepOptions::default()),
                    movement("DeciderStep", decider_options),
                ],
                ..StepDecision::default()
            },
        );
        self.start_attempt("DoneStep-1", StepMethod::Execute);
        self.complete_execute("DoneStep-1", StepDecision::default());
        self.start_attempt("ActiveStep-1", StepMethod::WaitFor);
        self.assert_states(&[
            ("ChargeStep", 1, Completed),
            ("DoneStep", 1, Completed),
            ("ActiveStep", 1, NotCompleted),
            ("DeciderStep", 1, NotCompleted),
            ("ActiveStep", 2, NotCompleted),
            ("NeverStep", 1, NotCompleted),
        ]);
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

    fn complete_wait_for(&mut self, step_execution_id: &str) {
        let outcome = self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation_key(step_execution_id, StepMethod::WaitFor)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::WaitFor(
                InvokeWaitForMethodResponse::default(),
            )),
            ..StepMethodCompletedCommand::default()
        }));
        assert_not_rejected(&outcome, "the WaitFor completes");
    }

    fn complete_execute(&mut self, step_execution_id: &str, decision: StepDecision) {
        let outcome = self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation_key(step_execution_id, StepMethod::Execute)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(decision),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }));
        assert_not_rejected(&outcome, "the Execute completes");
    }

    /// Runs DeciderStep-1's Execute to the given decision.
    fn decide(&mut self, decision: StepDecision) {
        self.start_attempt("DeciderStep-1", StepMethod::Execute);
        self.complete_execute("DeciderStep-1", decision);
    }

    fn fail_execute_without_retry(&mut self, step_execution_id: &str) {
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

    fn fire_flow_timeout(&mut self) {
        let outcome = self.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: 1,
                kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
            }],
        }));
        assert_not_rejected(&outcome, "the Flow timeout fires");
    }

    /// Time travels to the first semantic point at or after `timestamp_micros`.
    fn travel_to(&mut self, timestamp_micros: i64) {
        let outcome = self.apply(Command::TimeTravel(TimeTravelCommand {
            request: Some(ResetFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                reason: "test".to_owned(),
                history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
                ..ResetFlowRequest::default()
            }),
            ..TimeTravelCommand::default()
        }));
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "time travel was rejected: {:?}",
            outcome.reply
        );
    }

    fn status(&self) -> FlowStatus {
        let summary = get_flow_summary(&self.state, self.context.flow_hash, self.run_id)
            .expect("the summary reads");
        FlowStatus::try_from(summary.flow_status).expect("a known status")
    }

    fn state_of(&self, step_type: &str, step_execution_number: u32) -> StepCompletionState {
        step_completion_state(
            &self.state,
            self.context.flow_hash,
            self.run_id,
            step_type,
            step_execution_number,
        )
        .expect("the Step completion state reads")
    }

    fn assert_states(&self, expected: &[(&str, u32, StepCompletionState)]) {
        for (step_type, number, state) in expected {
            assert_eq!(
                self.state_of(step_type, *number),
                *state,
                "{step_type}-{number} with Flow status {:?}",
                self.status()
            );
        }
    }

    /// Checks every answer after a close; `active_steps_answer` is for the two Steps it canceled.
    fn assert_closed(&self, status: FlowStatus, active_steps_answer: StepCompletionState) {
        assert_eq!(self.status(), status);
        self.assert_states(&[
            ("ChargeStep", 1, Completed),
            ("DoneStep", 1, Completed),
            ("ActiveStep", 1, active_steps_answer),
            ("DeciderStep", 1, active_steps_answer),
            ("ActiveStep", 2, EndedWithoutCompletion),
            ("NeverStep", 1, EndedWithoutCompletion),
        ]);
    }
}

fn assert_not_rejected(outcome: &ApplyOutcome, what: &str) {
    assert!(
        !matches!(outcome.reply.reply, Some(command_reply::Reply::Error(_))),
        "{what}: {:?}",
        outcome.reply
    );
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

fn movement(step_type: &str, step_options: StepOptions) -> StepMovement {
    StepMovement {
        step_type: step_type.to_owned(),
        step_options: Some(step_options),
        ..StepMovement::default()
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
fn terminate_ends_the_steps_it_cancels_without_completion() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.stop(StopType::Terminate);
    harness.assert_closed(FlowStatus::Terminated, EndedWithoutCompletion);
}

#[test]
fn cancel_counts_the_steps_it_cancels_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.stop(StopType::Cancel);
    harness.assert_closed(FlowStatus::Canceled, Completed);
}

#[test]
fn force_complete_counts_the_steps_it_cancels_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.decide(close_decision(CloseDecisionType::ForceComplete));
    harness.assert_closed(FlowStatus::Completed, Completed);
}

#[test]
fn force_complete_on_empty_channels_counts_the_steps_it_cancels_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.decide(close_decision(
        CloseDecisionType::ForceCompleteOnChannelsEmpty,
    ));
    harness.assert_closed(FlowStatus::Completed, Completed);
}

#[test]
fn force_fail_counts_the_steps_it_cancels_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.decide(close_decision(CloseDecisionType::ForceFail));
    harness.assert_closed(FlowStatus::Failed, Completed);
}

#[test]
fn graceful_completion_completes_every_step() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.decide(close_decision(CloseDecisionType::GracefulComplete));
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.complete_wait_for("ActiveStep-1");
    harness.start_attempt("ActiveStep-1", StepMethod::Execute);
    harness.complete_execute("ActiveStep-1", StepDecision::default());
    harness.assert_closed(FlowStatus::Completed, Completed);
}

#[test]
fn exhausted_retries_end_the_failed_step_without_completion() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.fail_execute_without_retry("DeciderStep-1");
    assert_eq!(harness.status(), FlowStatus::Failed);
    harness.assert_states(&[
        ("ChargeStep", 1, Completed),
        ("DoneStep", 1, Completed),
        ("ActiveStep", 1, Completed),
        ("DeciderStep", 1, EndedWithoutCompletion),
        ("ActiveStep", 2, EndedWithoutCompletion),
        ("NeverStep", 1, EndedWithoutCompletion),
    ]);
}

#[test]
fn a_failure_that_proceeds_keeps_the_failed_step_ended_without_completion() {
    let mut harness = Harness::new();
    harness.open_run(
        FlowTimeout::None,
        StepOptions {
            skip_wait_for: true,
            execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
            execute_failure_proceed_step_type: "RecoverStep".to_owned(),
            execute_failure_proceed_step_options: Some(Box::new(skip_wait_for())),
            ..StepOptions::default()
        },
    );
    harness.fail_execute_without_retry("DeciderStep-1");
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.assert_states(&[
        ("DeciderStep", 1, EndedWithoutCompletion),
        ("RecoverStep", 1, NotCompleted),
        ("ActiveStep", 1, NotCompleted),
    ]);
    harness.start_attempt("RecoverStep-1", StepMethod::Execute);
    harness.complete_execute("RecoverStep-1", StepDecision::default());
    harness.assert_states(&[
        ("DeciderStep", 1, EndedWithoutCompletion),
        ("RecoverStep", 1, Completed),
    ]);
    harness.stop(StopType::Cancel);
    harness.assert_states(&[
        ("ChargeStep", 1, Completed),
        ("DoneStep", 1, Completed),
        ("ActiveStep", 1, Completed),
        ("DeciderStep", 1, EndedWithoutCompletion),
        ("RecoverStep", 1, Completed),
        ("RecoverStep", 2, EndedWithoutCompletion),
    ]);
}

#[test]
fn fail_policy_flow_timeout_counts_the_steps_it_cancels_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(
        FlowTimeout::Policy(FlowTimeoutPolicy::Fail),
        skip_wait_for(),
    );
    harness.fire_flow_timeout();
    harness.assert_closed(FlowStatus::Failed, Completed);
}

#[test]
fn cancel_policy_flow_timeout_counts_the_steps_it_cancels_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(
        FlowTimeout::Policy(FlowTimeoutPolicy::Cancel),
        skip_wait_for(),
    );
    harness.fire_flow_timeout();
    harness.assert_closed(FlowStatus::Canceled, Completed);
}

#[test]
fn handler_policy_flow_timeout_keeps_the_steps_active_until_a_close() {
    let mut harness = Harness::new();
    harness.open_run(
        FlowTimeout::Policy(FlowTimeoutPolicy::Handler),
        skip_wait_for(),
    );
    harness.fire_flow_timeout();
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.start_attempt("sys:timeout_handler-1", StepMethod::Execute);
    harness.complete_execute("sys:timeout_handler-1", StepDecision::default());
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.assert_states(&[
        ("ActiveStep", 1, NotCompleted),
        ("DeciderStep", 1, NotCompleted),
    ]);
    harness.stop(StopType::Terminate);
    harness.assert_closed(FlowStatus::Terminated, EndedWithoutCompletion);
}

#[test]
fn id_reuse_termination_ends_the_steps_it_cancels_without_completion() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    let outcome = harness.start_flow(
        SECOND_RUN,
        FlowTimeout::None,
        IdReusePolicy::AllowTerminateIfRunning,
    );
    assert_not_rejected(&outcome, "the second run starts");
    harness.assert_closed(FlowStatus::Terminated, EndedWithoutCompletion);
    harness.run_id = SECOND_RUN;
    harness.assert_states(&[
        ("ChargeStep", 1, NotCompleted),
        ("ActiveStep", 1, NotCompleted),
    ]);
}

#[test]
fn a_decision_that_cancels_and_closes_counts_its_cancellations_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.decide(StepDecision {
        cancel_step_types: vec!["ActiveStep".to_owned()],
        ..close_decision(CloseDecisionType::ForceFail)
    });
    harness.assert_closed(FlowStatus::Failed, Completed);
}

#[test]
fn a_decision_that_only_cancels_counts_its_cancellations_as_completed_after_terminate() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.decide(StepDecision {
        cancel_step_types: vec!["ActiveStep".to_owned()],
        ..StepDecision::default()
    });
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.assert_states(&[("ActiveStep", 1, Completed), ("DeciderStep", 1, Completed)]);
    harness.stop(StopType::Terminate);
    harness.assert_closed(FlowStatus::Terminated, Completed);
}

#[test]
fn a_deferred_close_counts_its_cancellations_as_completed_even_after_terminate() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.is_transactional_rpc_in_flight = true;
    harness.decide(close_decision(CloseDecisionType::ForceComplete));
    harness.is_transactional_rpc_in_flight = false;
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.assert_states(&[("ActiveStep", 1, Completed), ("DeciderStep", 1, Completed)]);
    harness.stop(StopType::Terminate);
    harness.assert_closed(FlowStatus::Terminated, Completed);
}

#[test]
fn a_deferred_close_that_finalizes_counts_its_cancellations_as_completed() {
    let mut harness = Harness::new();
    harness.open_run(FlowTimeout::None, skip_wait_for());
    harness.is_transactional_rpc_in_flight = true;
    harness.decide(close_decision(CloseDecisionType::ForceComplete));
    harness.is_transactional_rpc_in_flight = false;
    let outcome = harness.apply(Command::PendingCloseCheck(PendingCloseCheckCommand {}));
    assert_not_rejected(&outcome, "the pending close check applies");
    harness.assert_closed(FlowStatus::Completed, Completed);
}

#[test]
fn time_travel_restores_the_answers_of_its_target_point() {
    let mut harness = Harness::new();
    harness.start_flow(FIRST_RUN, FlowTimeout::None, IdReusePolicy::Unspecified);
    harness.start_attempt("ChargeStep-1", StepMethod::Execute);
    harness.complete_execute(
        "ChargeStep-1",
        StepDecision {
            next_steps: vec![
                movement("DoneStep", skip_wait_for()),
                movement("ActiveStep", StepOptions::default()),
            ],
            ..StepDecision::default()
        },
    );
    let before_done_micros = harness.now_micros;
    harness.start_attempt("DoneStep-1", StepMethod::Execute);
    harness.complete_execute("DoneStep-1", StepDecision::default());
    let after_done_micros = harness.now_micros;
    harness.stop(StopType::Terminate);
    harness.assert_states(&[
        ("DoneStep", 1, Completed),
        ("ActiveStep", 1, EndedWithoutCompletion),
    ]);

    harness.travel_to(after_done_micros);
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.assert_states(&[
        ("ChargeStep", 1, Completed),
        ("DoneStep", 1, Completed),
        ("ActiveStep", 1, NotCompleted),
    ]);

    harness.travel_to(before_done_micros);
    assert_eq!(harness.status(), FlowStatus::Running);
    harness.assert_states(&[
        ("ChargeStep", 1, Completed),
        ("DoneStep", 1, NotCompleted),
        ("ActiveStep", 1, NotCompleted),
    ]);
    harness.start_attempt("DoneStep-1", StepMethod::Execute);
    harness.complete_execute("DoneStep-1", StepDecision::default());
    harness.stop(StopType::Cancel);
    harness.assert_states(&[("DoneStep", 1, Completed), ("ActiveStep", 1, Completed)]);
}
