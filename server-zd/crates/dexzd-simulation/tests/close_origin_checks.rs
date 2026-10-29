// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hand-built command sequences that the AS5 close origin checks must accept.

use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{
    CloseDecision, CloseDecisionType, FlowResetType, FlowStatus, FlowTimeoutPolicy,
    InvokeExecuteMethodResponse, ResetFlowRequest, StartFlowRequest, StepDecision, StepOptions,
    Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowStartCommand, RunRecord, RunSettings, StepAttemptStartedCommand,
    StepInvocationKey, StepMethod, StepMethodCompletedCommand, TimeTravelCommand, command_reply,
    flow_command::Command, step_method_completed_command,
};
use dexzd_simulation::{ApplyChecker, RunSelection};

const SECOND: i64 = 1_000_000;
const FLOW_ID: &str = "close-origin-probe";
const RUN_ID: [u8; 16] = [5; 16];

struct Probe {
    checker: ApplyChecker,
    now_micros: i64,
    is_transactional_rpc_in_flight: bool,
}

impl Probe {
    fn new() -> Self {
        Self {
            checker: ApplyChecker::new(),
            now_micros: 1_800_000_000 * SECOND,
            is_transactional_rpc_in_flight: false,
        }
    }

    /// Applies `body` through every checker invariant; panics on a violation.
    fn apply(&mut self, body: Command) -> dexzd_apply::ApplyOutcome {
        self.now_micros += SECOND;
        let command = FlowCommand {
            flow_id: FLOW_ID.to_owned(),
            run_id: if matches!(body, Command::FlowStart(_)) {
                Vec::new()
            } else {
                RUN_ID.to_vec()
            },
            expected_run_attempt: 0,
            epoch: 1,
            behavior_version: dexzd_apply::BehaviorVersion::CURRENT.number(),
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            tenant_id: 0,
            is_transactional_rpc_in_flight: self.is_transactional_rpc_in_flight,
            command: Some(body),
        };
        self.checker
            .apply_and_check(&command)
            .unwrap_or_else(|violation| panic!("{violation}"))
    }

    fn run_record(&self) -> RunRecord {
        let view = self
            .checker
            .view(FLOW_ID, RunSelection::All)
            .expect("the state decodes");
        let run_id = RunId::from_slice(&RUN_ID).expect("a 16-byte run ID");
        view.runs
            .get(&run_id)
            .and_then(|run| run.record.clone())
            .expect("the run record exists")
    }
}

fn execute_key() -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: "ChargeStep-1".to_owned(),
        method: StepMethod::Execute as i32,
    }
}

fn start_without_attribute_stores() -> Command {
    Command::FlowStart(FlowStartCommand {
        new_run_id: RUN_ID.to_vec(),
        request: Some(StartFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            flow_type: "OrderFlow".to_owned(),
            start_step_type: "ChargeStep".to_owned(),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            flow_timeout_seconds: 600,
            request_id: "start-probe".to_owned(),
            ..StartFlowRequest::default()
        }),
        settings: Some(RunSettings {
            history_window_semantic_events: 100,
            history_prune_chunk_semantic_events: 10,
            max_retained_superseded_bytes: 256 << 20,
            retention_after_close_micros: 60 * SECOND,
            wire_inline_threshold_bytes: 100,
            max_value_bytes: 16 << 20,
            checkpoint_min_written_bytes: 1 << 20,
            checkpoint_max_commands: 10_000,
            request_dedup_capacity: 4,
            max_run_state_bytes: 1 << 30,
            max_active_step_executions: 10_000,
        }),
        resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
        worker_target: Some(WorkerTarget { is_pull_mode: true }),
        ..FlowStartCommand::default()
    })
}

fn force_complete() -> Command {
    Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(execute_key()),
        attempt: 1,
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                step_decision: Some(StepDecision {
                    close_decision: Some(CloseDecision {
                        close_decision_type: CloseDecisionType::ForceComplete as i32,
                        close_input: Some(Value {
                            kind: Some(value::Kind::StringValue("done".to_owned())),
                        }),
                        ..CloseDecision::default()
                    }),
                    ..StepDecision::default()
                }),
                ..InvokeExecuteMethodResponse::default()
            },
        )),
        app_worker_id: "worker-a".to_owned(),
        ..StepMethodCompletedCommand::default()
    })
}

/// A reset into a pending close finalizes it as a natural close that consulted the barrier.
#[test]
fn a_reset_that_finalizes_a_restored_pending_close_is_a_natural_close() {
    let mut probe = Probe::new();
    probe.apply(start_without_attribute_stores());
    probe.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(execute_key()),
        attempt: 1,
        task_id: vec![1; 16],
        pull_routing: None,
    }));
    probe.is_transactional_rpc_in_flight = true;
    probe.apply(force_complete());
    probe.is_transactional_rpc_in_flight = false;
    let held = probe.run_record();
    assert_eq!(held.status, FlowStatus::Running as i32);
    assert!(
        held.pending_close.is_some(),
        "the admitted RPC holds the close"
    );
    let held_time = probe.now_micros;

    let travel = probe.apply(Command::TimeTravel(TimeTravelCommand {
        request: Some(ResetFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            history_event_time: (i128::from(held_time) * 1000).to_string(),
            run_attempt: 1,
            reason: "inspect the held close".to_owned(),
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        }),
    }));
    assert!(
        matches!(
            travel.reply.reply,
            Some(command_reply::Reply::TimeTravel(_))
        ),
        "time travel applies: {:?}",
        travel.reply
    );
    let closed = probe.run_record();
    assert_eq!(closed.current_attempt, 2);
    assert_eq!(closed.status, FlowStatus::Completed as i32);
    assert_eq!(
        closed.close.and_then(|close| close.is_closed_at_once),
        Some(false)
    );
}
