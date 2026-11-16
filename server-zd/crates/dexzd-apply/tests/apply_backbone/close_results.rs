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

/// Larger than the harness inline threshold, so the StopFlow output is a value-table value.
fn stop_output() -> Value {
    string_value(&"s".repeat(500))
}

impl Harness {
    fn stop_flow(&mut self, stop_type: StopType, output: Option<Value>) -> ApplyOutcome {
        self.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: stop_type as i32,
                reason: "ignored by the complete stop types".to_owned(),
                output,
                ..StopFlowRequest::default()
            }),
        }))
    }

    /// Starts a Flow whose ChargeStep-1 skips its wait and has not run yet.
    fn start_with_charge_step_active(&mut self) {
        let mut request = start_request("request-1");
        request.step_options = Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        });
        self.start(request);
    }

    /// Starts a Flow whose ShipStep-1 requests graceful completion while PackStep-1 is active.
    ///
    /// Returns the value ID of the collected output.
    fn collect_graceful_output_with_pack_step_active(&mut self) -> ValueId {
        self.start_with_charge_step_active();
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

#[test]
fn a_force_complete_stop_cancels_active_steps_and_keeps_every_output() {
    let mut harness = Harness::new("order-1");
    harness.collect_graceful_output_with_pack_step_active();

    let closed = harness.stop_flow(StopType::ForceComplete, Some(stop_output()));

    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Completed as i32);
    let close = record.close.expect("the run is closed");
    assert_eq!(close.results.len(), 2);
    assert_eq!(close.results[0].completed_step_execution_id, "ShipStep-1");
    assert!(close.results[1].completed_step_type.is_empty());
    assert!(close.results[1].completed_step_execution_id.is_empty());
    assert!(
        close.history_message.is_none(),
        "the stop reason is not kept"
    );
    let stop_value_id = table_value_id(close.results[1].completed_step_output.as_ref());
    assert_eq!(harness.holder_count(stop_value_id), 1);
    assert_eq!(flow_closed_event(&closed).results.len(), 2);
    assert_eq!(
        harness.step_execution("PackStep-1").phase,
        StepExecutionPhase::Canceled as i32
    );
}

#[test]
fn a_graceful_complete_stop_waits_for_the_active_step_and_appends_each_output() {
    let mut harness = Harness::new("order-1");
    harness.start_with_charge_step_active();

    let requested = harness.stop_flow(StopType::GracefulComplete, Some(stop_output()));
    assert!(matches!(
        requested.reply.reply,
        Some(command_reply::Reply::Ok(_))
    ));
    harness.stop_flow(StopType::GracefulComplete, Some(string_value("again")));

    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert!(record.is_graceful_completion_requested);
    assert_eq!(
        record.close.expect("outputs are collected").results.len(),
        2
    );
    harness.start_attempt("ChargeStep-1", StepMethod::Execute, 1);
    let closed = harness.complete_execute("ChargeStep-1", 1, StepDecision::default());

    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Completed as i32);
    let results = record.close.expect("the run is closed").results;
    assert_eq!(results.len(), 2);
    assert!(
        results
            .iter()
            .all(|output| output.completed_step_type.is_empty())
    );
    assert_eq!(flow_closed_event(&closed).results.len(), 2);
}

#[test]
fn a_graceful_complete_stop_without_active_steps_completes_at_once() {
    let mut harness = Harness::new("order-1");
    harness.start_with_charge_step_active();
    harness.start_attempt("ChargeStep-1", StepMethod::Execute, 1);
    let dead_end = StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: CloseDecisionType::DeadEnd as i32,
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    };
    harness.complete_execute("ChargeStep-1", 1, dead_end);
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);

    let closed = harness.stop_flow(StopType::GracefulComplete, None);

    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Completed as i32);
    assert!(record.close.expect("the run is closed").results.is_empty());
    assert!(flow_closed_event(&closed).results.is_empty());
}

#[test]
fn a_later_stop_after_a_graceful_complete_stop_closes_without_outputs() {
    for (stop_type, status) in [
        (StopType::Cancel, FlowStatus::Canceled),
        (StopType::Terminate, FlowStatus::Terminated),
        (StopType::Fail, FlowStatus::Failed),
    ] {
        let mut harness = Harness::new("order-1");
        harness.start_with_charge_step_active();
        harness.stop_flow(StopType::GracefulComplete, Some(stop_output()));
        let record = harness.run_record();
        let close = record.close.expect("the output is collected");
        let value_id = table_value_id(close.results[0].completed_step_output.as_ref());

        let closed = harness.stop_flow(stop_type, None);

        harness.assert_no_outputs_after_close(&closed, status, value_id);
    }
}

#[test]
fn a_graceful_complete_stop_waits_for_the_steps_an_active_step_moves_to() {
    let mut harness = Harness::new("order-1");
    harness.start_with_charge_step_active();
    harness.stop_flow(StopType::GracefulComplete, Some(stop_output()));
    harness.start_attempt("ChargeStep-1", StepMethod::Execute, 1);
    let go_to_ship = StepDecision {
        next_steps: vec![StepMovement {
            step_type: "ShipStep".to_owned(),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            ..StepMovement::default()
        }],
        ..StepDecision::default()
    };

    harness.complete_execute("ChargeStep-1", 1, go_to_ship);

    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    harness.start_attempt("ShipStep-1", StepMethod::Execute, 1);
    let closed = harness.complete_execute("ShipStep-1", 1, StepDecision::default());
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Completed as i32);
    assert_eq!(record.close.expect("the run is closed").results.len(), 1);
    assert_eq!(flow_closed_event(&closed).results.len(), 1);
}

#[test]
fn a_graceful_complete_stop_output_over_the_run_state_limit_is_rejected_without_changes() {
    let mut harness = Harness::new("order-1");
    harness.start_with_charge_step_active();
    harness.set_max_run_state_bytes(1);
    let before = harness.state.rows.clone();

    let rejected = harness.stop_flow(StopType::GracefulComplete, Some(stop_output()));

    assert!(is_error(&rejected.reply, 8), "{:?}", rejected.reply);
    assert!(rejected.writes.is_empty());
    assert_eq!(harness.state.rows, before);
    let requested = harness.stop_flow(StopType::GracefulComplete, None);
    assert!(
        matches!(requested.reply.reply, Some(command_reply::Reply::Ok(_))),
        "a stop without output only sets the request flag: {:?}",
        requested.reply
    );
    assert!(harness.run_record().is_graceful_completion_requested);
}

#[test]
fn a_complete_stop_with_a_too_large_output_is_rejected_without_changes() {
    let mut harness = Harness::new("order-1");
    let mut run_settings = settings();
    run_settings.max_value_bytes = 100;
    harness.start_with_settings(start_request("request-1"), run_settings);

    for stop_type in [StopType::GracefulComplete, StopType::ForceComplete] {
        let rejected = harness.stop_flow(stop_type, Some(stop_output()));

        assert!(is_error(&rejected.reply, 3), "{stop_type:?}");
        assert!(rejected.writes.is_empty(), "{stop_type:?}");
        let record = harness.run_record();
        assert_eq!(record.status, FlowStatus::Running as i32);
        assert!(!record.is_graceful_completion_requested);
    }
}
