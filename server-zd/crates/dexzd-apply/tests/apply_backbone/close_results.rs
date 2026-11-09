// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Only a COMPLETED Flow keeps the outputs its Steps collected.

use super::*;
use dexzd_apply::read_model::get_flow_state;
use dexzd_protocol::dex::{FlowClosedHistoryEvent, FlowErrorType};

/// Larger than the harness inline threshold, so the output is a value-table value.
fn graceful_output() -> Value {
    string_value(&"o".repeat(500))
}

fn graceful_complete_with_output() -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: CloseDecisionType::GracefulComplete as i32,
            close_input: Some(graceful_output()),
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    }
}

fn force_fail(message: &str) -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: CloseDecisionType::ForceFail as i32,
            close_input: Some(string_value(message)),
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    }
}

fn flow_closed_event(outcome: &ApplyOutcome) -> FlowClosedHistoryEvent {
    history_events(outcome)
        .into_iter()
        .find_map(|payload| match payload {
            flow_history_event::Payload::FlowClosed(closed) => Some(closed),
            _ => None,
        })
        .expect("the command closed the Flow")
}

impl Harness {
    /// Starts a Flow whose ShipStep-1 requests graceful completion while PackStep-1 is active.
    ///
    /// Returns the value ID of the collected output.
    fn collect_graceful_output_with_pack_step_active(&mut self) -> ValueId {
        let mut request = start_request("request-1");
        request.step_options = Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        });
        self.start(request);
        self.collect_graceful_output_after_start()
    }

    /// Runs ChargeStep-1 into ShipStep-1 and PackStep-1; ShipStep-1 completes gracefully with
    /// an output.
    ///
    /// Returns the value ID of the collected output.
    fn collect_graceful_output_after_start(&mut self) -> ValueId {
        self.start_attempt("ChargeStep-1", StepMethod::Execute, 1);
        let skip = Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        });
        let branches = StepDecision {
            next_steps: ["ShipStep", "PackStep"]
                .into_iter()
                .map(|step_type| StepMovement {
                    step_type: step_type.to_owned(),
                    step_options: skip.clone(),
                    ..StepMovement::default()
                })
                .collect(),
            ..StepDecision::default()
        };
        self.complete_execute("ChargeStep-1", 1, branches);
        self.start_attempt("ShipStep-1", StepMethod::Execute, 1);
        self.complete_execute("ShipStep-1", 1, graceful_complete_with_output());
        let record = self.run_record();
        assert_eq!(record.status, FlowStatus::Running as i32);
        let close = record
            .close
            .expect("graceful completion collected the output");
        assert_eq!(close.results.len(), 1);
        let value_id = table_value_id(close.results[0].completed_step_output.as_ref());
        assert_eq!(self.holder_count(value_id), 1);
        value_id
    }

    /// Asserts that the closed run kept no output and released the collected value.
    fn assert_no_outputs_after_close(
        &self,
        closed: &ApplyOutcome,
        status: FlowStatus,
        value_id: ValueId,
    ) {
        let record = self.run_record();
        assert_eq!(record.status, status as i32);
        let close = record.close.expect("the run is closed");
        assert!(close.results.is_empty(), "{status:?} keeps no output");
        let event = flow_closed_event(closed);
        assert_eq!(event.flow_status, status as i32);
        assert!(event.results.is_empty(), "FlowClosed carries no output");
        let state = get_flow_state(&self.state, self.context.flow_hash, self.harness_run_id())
            .expect("GetFlowState reads the closed run");
        assert!(
            state.completed_steps.is_empty(),
            "GetFlowState shows no output"
        );
        assert_eq!(self.holder_count(value_id), 0, "the output is released");
        let garbage_seq = self.value_birth(value_id).garbage_seq;
        assert!(garbage_seq > 0, "the released output is superseded");
        assert_eq!(self.garbage_seqs(value_id), vec![garbage_seq]);
    }
}

#[test]
fn force_fail_after_a_graceful_output_closes_failed_without_outputs() {
    let mut harness = Harness::new("order-1");
    let value_id = harness.collect_graceful_output_with_pack_step_active();
    harness.start_attempt("PackStep-1", StepMethod::Execute, 1);

    let closed = harness.complete_execute("PackStep-1", 1, force_fail("packing failed"));

    harness.assert_no_outputs_after_close(&closed, FlowStatus::Failed, value_id);
    let close = harness.run_record().close.expect("the run is closed");
    assert_eq!(
        close.error_type,
        FlowErrorType::StepDecisionFailingFlow as i32
    );
    assert_eq!(close.error_message, "packing failed");
}

#[test]
fn stop_flow_after_a_graceful_output_closes_without_outputs() {
    for (stop_type, status) in [
        (StopType::Cancel, FlowStatus::Canceled),
        (StopType::Fail, FlowStatus::Failed),
        (StopType::Terminate, FlowStatus::Terminated),
    ] {
        let mut harness = Harness::new("order-1");
        let value_id = harness.collect_graceful_output_with_pack_step_active();

        let closed = harness.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: stop_type as i32,
                ..StopFlowRequest::default()
            }),
        }));

        harness.assert_no_outputs_after_close(&closed, status, value_id);
    }
}

#[test]
fn a_flow_timeout_after_a_graceful_output_closes_without_outputs() {
    for (timeout_policy, status) in [
        (FlowTimeoutPolicy::Fail, FlowStatus::Failed),
        (FlowTimeoutPolicy::Cancel, FlowStatus::Canceled),
    ] {
        let mut harness = Harness::new("order-1");
        let mut request = start_request("request-1");
        request.flow_timeout_seconds = 30;
        request.step_options = Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        });
        harness.start_with_timeout_policy(request, timeout_policy);
        let value_id = harness.collect_graceful_output_after_start();

        let closed = harness.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: 1,
                kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
            }],
        }));

        harness.assert_no_outputs_after_close(&closed, status, value_id);
        if timeout_policy == FlowTimeoutPolicy::Fail {
            let close = harness.run_record().close.expect("the run is closed");
            assert_eq!(close.error_type, FlowErrorType::FlowTimeout as i32);
        }
    }
}

#[test]
fn a_completed_flow_keeps_its_graceful_output() {
    let mut harness = Harness::new("order-1");
    let value_id = harness.collect_graceful_output_with_pack_step_active();
    harness.start_attempt("PackStep-1", StepMethod::Execute, 1);

    let closed = harness.complete_execute("PackStep-1", 1, StepDecision::default());

    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Completed as i32);
    let close = record.close.expect("the run is closed");
    assert_eq!(close.results.len(), 1);
    assert_eq!(close.results[0].completed_step_execution_id, "ShipStep-1");
    assert_eq!(flow_closed_event(&closed).results.len(), 1);
    let state = get_flow_state(
        &harness.state,
        harness.context.flow_hash,
        harness.harness_run_id(),
    )
    .expect("GetFlowState reads the closed run");
    assert_eq!(state.completed_steps.len(), 1);
    assert_eq!(harness.holder_count(value_id), 1, "the output stays held");
}

#[test]
fn time_travel_before_a_failing_close_holds_the_graceful_output_again() {
    let mut harness = Harness::new("order-1");
    let value_id = harness.collect_graceful_output_with_pack_step_active();
    let collected_at_micros = harness.now_micros;
    harness.start_attempt("PackStep-1", StepMethod::Execute, 1);
    let closed = harness.complete_execute("PackStep-1", 1, force_fail("packing failed"));
    harness.assert_no_outputs_after_close(&closed, FlowStatus::Failed, value_id);

    let traveled = harness.apply(Command::TimeTravel(TimeTravelCommand {
        staging_id: Vec::new(),
        computed_through_seq: 0,
        target_run_attempt: 0,
        target_run_seq: 0,
        should_restore_running_as_pending: false,
        request: Some(ResetFlowRequest {
            flow_id: "order-1".to_owned(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            history_event_time: (i128::from(collected_at_micros) * 1000).to_string(),
            run_attempt: 1,
            reason: "test".to_owned(),
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        }),
    }));

    assert!(
        matches!(
            traveled.reply.reply,
            Some(command_reply::Reply::TimeTravel(_))
        ),
        "{:?}",
        traveled.reply
    );
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    let close = record
        .close
        .expect("the restored run keeps its collected output");
    assert_eq!(close.results.len(), 1);
    assert_eq!(
        table_value_id(close.results[0].completed_step_output.as_ref()),
        value_id
    );
    assert_eq!(
        harness.holder_count(value_id),
        1,
        "the output is held again"
    );
    assert!(harness.garbage_seqs(value_id).is_empty());
}
