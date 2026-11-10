// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A child that does not complete reports no outputs to its parent.

use super::*;
use dexzd_protocol::dex::{FlowResult, StepMovement};

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn close_decision(close_decision_type: CloseDecisionType, close_input: Value) -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: close_decision_type as i32,
            close_input: Some(close_input),
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    }
}

impl Harness {
    fn run_child_execute(&mut self, step_execution_id: &str, decision: StepDecision) {
        let invocation = StepInvocationKey {
            step_execution_id: step_execution_id.to_owned(),
            method: StepMethod::Execute as i32,
        };
        assert_ok(&self.apply(
            CHILD_ID,
            &FIRST_CHILD_RUN,
            Command::StepAttemptStarted(StepAttemptStartedCommand {
                invocation: Some(invocation.clone()),
                attempt: 1,
                task_id: vec![8; 16],
                pull_routing: None,
            }),
        ));
        assert_ok(&self.apply(
            CHILD_ID,
            &FIRST_CHILD_RUN,
            Command::StepMethodCompleted(StepMethodCompletedCommand {
                invocation: Some(invocation),
                attempt: 1,
                response: Some(step_method_completed_command::Response::Execute(
                    InvokeExecuteMethodResponse {
                        step_decision: Some(decision),
                        ..InvokeExecuteMethodResponse::default()
                    },
                )),
                ..StepMethodCompletedCommand::default()
            }),
        ));
    }

    /// Starts the child; ShipStep-1 completes gracefully with an output while PackStep-1 runs.
    fn start_child_with_a_graceful_output(&mut self) {
        self.start_parent();
        let start = self.register_sub_flow_wait(SubFlowReusePolicy::Unspecified);
        assert_ok(&self.deliver_start_outbox(&start, FIRST_CHILD_RUN));
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
        self.run_child_execute("ChildStep-1", branches);
        self.run_child_execute(
            "ShipStep-1",
            close_decision(
                CloseDecisionType::GracefulComplete,
                string_value(&"o".repeat(500)),
            ),
        );
        let child = self.record(CHILD_ID, FIRST_CHILD_RUN);
        assert_eq!(child.status, FlowStatus::Running as i32);
        assert_eq!(child.close.expect("collected output").results.len(), 1);
    }

    fn child_report(&self) -> (OutboxRecord, FlowResult) {
        let outbox = self
            .outbox(CHILD_ID, FIRST_CHILD_RUN)
            .into_iter()
            .find(|record| {
                matches!(
                    record.payload,
                    Some(outbox_record::Payload::ChildCompleted(_))
                )
            })
            .expect("the child reported its close");
        let Some(outbox_record::Payload::ChildCompleted(completed)) = outbox.payload.clone() else {
            panic!("expected a child completion");
        };
        (outbox, completed.result.expect("the report has a result"))
    }

    fn child_result_seen_by_parent(&self) -> (i32, usize) {
        let step = self.step("parent", PARENT_RUN, "StartStep-1");
        let results = step.condition_results.expect("the wait completed");
        let result = &results.sub_flow_results[0];
        (result.flow_status, result.results.len())
    }
}

#[test]
fn a_child_failed_after_a_graceful_output_reports_no_outputs() {
    let mut harness = Harness::new();
    harness.start_child_with_a_graceful_output();

    harness.run_child_execute(
        "PackStep-1",
        close_decision(CloseDecisionType::ForceFail, string_value("packing failed")),
    );

    let child = harness.record(CHILD_ID, FIRST_CHILD_RUN);
    assert_eq!(child.status, FlowStatus::Failed as i32);
    let (report, result) = harness.child_report();
    assert_eq!(result.flow_status, FlowStatus::Failed as i32);
    assert!(
        result.results.is_empty(),
        "a failed child reports no outputs"
    );
    assert_eq!(result.error_message, "packing failed");
    assert_ok(&harness.deliver_completion(&report));
    assert_eq!(
        harness.child_result_seen_by_parent(),
        (FlowStatus::Failed as i32, 0)
    );
}

#[test]
fn a_child_completed_after_a_graceful_output_reports_it() {
    let mut harness = Harness::new();
    harness.start_child_with_a_graceful_output();

    harness.run_child_execute("PackStep-1", StepDecision::default());

    let (report, result) = harness.child_report();
    assert_eq!(result.flow_status, FlowStatus::Completed as i32);
    assert_eq!(result.results.len(), 1);
    assert_ok(&harness.deliver_completion(&report));
    assert_eq!(
        harness.child_result_seen_by_parent(),
        (FlowStatus::Completed as i32, 1)
    );
}
