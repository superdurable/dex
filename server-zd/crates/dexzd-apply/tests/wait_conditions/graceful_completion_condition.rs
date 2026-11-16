// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A graceful completion request completes the graceful completion condition and wakes only its waiters.

use super::*;

use dexzd_apply::ApplyEffect;
use dexzd_protocol::dex::{GracefulCompletionCondition, GracefulCompletionResult};

impl Harness {
    fn stop_gracefully(&mut self) -> ApplyOutcome {
        let outcome = self.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: StopType::GracefulComplete as i32,
                ..StopFlowRequest::default()
            }),
        }));
        assert!(is_ok(&outcome.reply), "graceful stop: {:?}", outcome.reply);
        outcome
    }

    fn graceful_results(&self, step_execution_id: &str) -> Vec<GracefulCompletionResult> {
        self.step(step_execution_id)
            .condition_results
            .expect("the wait completed")
            .graceful_completion_results
    }

    fn run_status(&self) -> FlowStatus {
        FlowStatus::try_from(self.run_record().status).expect("known status")
    }
}

fn with_graceful(mut waiting: WaitingCondition, condition_id: &str) -> WaitingCondition {
    waiting
        .graceful_completion_conditions
        .push(GracefulCompletionCondition {
            condition_id: condition_id.to_owned(),
        });
    waiting
}

fn graceful_result(condition_id: &str, condition_status: i32) -> GracefulCompletionResult {
    GracefulCompletionResult {
        condition_id: condition_id.to_owned(),
        condition_status,
    }
}

fn dead_end() -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: CloseDecisionType::DeadEnd as i32,
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    }
}

/// `WaitStep-2` waits on the `other` channel, so the Flow keeps an active Step.
fn wait_on_other_channel(harness: &mut Harness) {
    harness.wait_on(
        "WaitStep-2",
        condition(
            WaitingConditionType::AnyCompleted,
            Vec::new(),
            vec![channel("", "other", None, None)],
        ),
        Vec::new(),
    );
}

#[test]
fn a_graceful_stop_wakes_only_the_steps_that_wait_on_the_condition() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    harness.wait_on(
        "WaitStep-1",
        with_graceful(
            condition(
                WaitingConditionType::AnyCompleted,
                Vec::new(),
                vec![channel("", "orders", None, None)],
            ),
            "",
        ),
        Vec::new(),
    );
    wait_on_other_channel(&mut harness);
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Waiting
    );

    harness.stop_gracefully();

    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Execute
    );
    assert_eq!(
        harness.graceful_results("WaitStep-1"),
        vec![graceful_result("", COMPLETED)]
    );
    assert_eq!(
        harness.consumed_texts("WaitStep-1"),
        vec![(WAITING, Vec::new())]
    );
    assert_eq!(
        phase(&harness.step("WaitStep-2")),
        StepExecutionPhase::Waiting
    );
    assert_eq!(harness.run_status(), FlowStatus::Running);

    harness.start_attempt("WaitStep-1", StepMethod::Execute, 1);
    let completed = harness.complete_execute("WaitStep-1", dead_end());
    let results = execute_inputs(&completed)[0]
        .condition_results
        .clone()
        .expect("Execute after WaitFor has results");
    assert_eq!(
        results.graceful_completion_results,
        vec![graceful_result("", COMPLETED)]
    );
    assert_eq!(harness.run_status(), FlowStatus::Running);

    harness.publish("other", &["m1"]);
    harness.start_attempt("WaitStep-2", StepMethod::Execute, 1);
    harness.complete_execute("WaitStep-2", dead_end());
    assert_eq!(harness.run_status(), FlowStatus::Completed);
}

#[test]
fn a_graceful_complete_decision_wakes_a_sibling_that_waits_on_the_condition() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    harness.wait_on(
        "WaitStep-1",
        with_graceful(
            condition(WaitingConditionType::AllCompleted, Vec::new(), Vec::new()),
            "",
        ),
        Vec::new(),
    );
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Waiting
    );
    harness.wait_on("WaitStep-2", WaitingCondition::default(), Vec::new());
    harness.start_attempt("WaitStep-2", StepMethod::Execute, 1);

    harness.complete_execute(
        "WaitStep-2",
        StepDecision {
            close_decision: Some(CloseDecision {
                close_decision_type: CloseDecisionType::GracefulComplete as i32,
                ..CloseDecision::default()
            }),
            ..StepDecision::default()
        },
    );

    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Execute
    );
    assert_eq!(harness.run_status(), FlowStatus::Running);
    harness.start_attempt("WaitStep-1", StepMethod::Execute, 1);
    harness.complete_execute("WaitStep-1", dead_end());
    assert_eq!(harness.run_status(), FlowStatus::Completed);
}

#[test]
fn a_wait_registered_after_the_request_completes_at_once() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    wait_on_other_channel(&mut harness);
    harness.stop_gracefully();
    assert_eq!(harness.run_status(), FlowStatus::Running);

    harness.wait_on(
        "WaitStep-1",
        with_graceful(
            condition(WaitingConditionType::AnyCompleted, Vec::new(), Vec::new()),
            "graceful",
        ),
        Vec::new(),
    );

    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Execute
    );
    assert_eq!(
        harness.graceful_results("WaitStep-1"),
        vec![graceful_result("graceful", COMPLETED)]
    );
}

#[test]
fn any_of_prefers_a_pending_message_over_the_request() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    wait_on_other_channel(&mut harness);
    harness.publish("orders", &["m1"]);
    harness.stop_gracefully();

    harness.wait_on(
        "WaitStep-1",
        with_graceful(
            condition(
                WaitingConditionType::AnyCompleted,
                Vec::new(),
                vec![channel("", "orders", None, None)],
            ),
            "",
        ),
        Vec::new(),
    );

    assert_eq!(
        harness.consumed_texts("WaitStep-1"),
        vec![(COMPLETED, texts(&["m1"]))]
    );
    // Like a fired timer that did not win, the condition still reports completed.
    assert_eq!(
        harness.graceful_results("WaitStep-1"),
        vec![graceful_result("", COMPLETED)]
    );
}

#[test]
fn all_of_waits_for_the_other_condition_too() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    wait_on_other_channel(&mut harness);
    harness.wait_on(
        "WaitStep-1",
        with_graceful(
            condition(
                WaitingConditionType::AllCompleted,
                Vec::new(),
                vec![channel("", "orders", None, None)],
            ),
            "",
        ),
        Vec::new(),
    );

    harness.stop_gracefully();
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Waiting
    );

    harness.publish("orders", &["m1"]);
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Execute
    );
    assert_eq!(
        harness.consumed_texts("WaitStep-1"),
        vec![(COMPLETED, texts(&["m1"]))]
    );
    assert_eq!(
        harness.graceful_results("WaitStep-1"),
        vec![graceful_result("", COMPLETED)]
    );
}

#[test]
fn any_combination_resolves_the_condition_by_its_id() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    wait_on_other_channel(&mut harness);
    let mut waiting = with_graceful(
        condition(
            WaitingConditionType::AnyCombinationCompleted,
            vec![timer("deadline", 60)],
            vec![channel("order", "orders", None, None)],
        ),
        "graceful",
    );
    waiting.condition_combinations = vec![
        ConditionCombination {
            condition_ids: vec!["deadline".to_owned(), "graceful".to_owned()],
        },
        ConditionCombination {
            condition_ids: vec!["order".to_owned()],
        },
    ];
    harness.wait_on("WaitStep-1", waiting, Vec::new());

    harness.stop_gracefully();
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Waiting
    );

    harness.skip("WaitStep-1", "deadline", None);
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Execute
    );
    assert_eq!(
        harness.graceful_results("WaitStep-1"),
        vec![graceful_result("graceful", COMPLETED)]
    );
    assert_eq!(
        harness.consumed_texts("WaitStep-1"),
        vec![(WAITING, Vec::new())]
    );
}

#[test]
fn a_repeated_request_does_not_dispatch_a_woken_step_again() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    wait_on_other_channel(&mut harness);
    harness.wait_on(
        "WaitStep-1",
        with_graceful(
            condition(WaitingConditionType::AnyCompleted, Vec::new(), Vec::new()),
            "",
        ),
        Vec::new(),
    );
    let first = harness.stop_gracefully();
    assert_eq!(pending_executes(&first), vec!["WaitStep-1".to_owned()]);
    let woken = harness.step("WaitStep-1");

    let repeated = harness.stop_gracefully();
    assert!(pending_executes(&repeated).is_empty());
    assert_eq!(harness.step("WaitStep-1"), woken);

    harness.start_attempt("WaitStep-1", StepMethod::Execute, 1);
    let executing = harness.step("WaitStep-1");
    let repeated = harness.stop_gracefully();
    assert!(pending_executes(&repeated).is_empty());
    assert_eq!(harness.step("WaitStep-1"), executing);
}

#[test]
fn waiters_on_the_condition_wake_in_started_waiting_order() {
    let mut harness = Harness::new();
    harness.start_two_waiting_steps();
    let graceful_only = || {
        with_graceful(
            condition(WaitingConditionType::AnyCompleted, Vec::new(), Vec::new()),
            "",
        )
    };
    harness.wait_on("WaitStep-2", graceful_only(), Vec::new());
    harness.wait_on("WaitStep-1", graceful_only(), Vec::new());

    let stopped = harness.stop_gracefully();

    assert_eq!(
        pending_executes(&stopped),
        vec!["WaitStep-2".to_owned(), "WaitStep-1".to_owned()]
    );
}

/// Step execution IDs whose Execute became pending, in effect order.
fn pending_executes(outcome: &ApplyOutcome) -> Vec<String> {
    outcome
        .effects
        .iter()
        .filter_map(|effect| match effect {
            ApplyEffect::InvocationPending { invocation, .. }
                if invocation.method == StepMethod::Execute as i32 =>
            {
                Some(invocation.step_execution_id.clone())
            }
            _ => None,
        })
        .collect()
}

#[test]
fn a_rejected_graceful_stop_wakes_nobody() {
    let mut harness = Harness::new();
    harness.start_waiting_flow(None);
    harness.wait_on(
        "WaitStep-1",
        with_graceful(
            condition(WaitingConditionType::AnyCompleted, Vec::new(), Vec::new()),
            "",
        ),
        Vec::new(),
    );
    harness.apply(Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            stop_type: StopType::GracefulComplete as i32,
            output: Some(Value {
                kind: Some(value::Kind::StringValue("x".repeat(17 << 20))),
            }),
            ..StopFlowRequest::default()
        }),
    }));
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Waiting
    );
    assert!(!harness.run_record().is_graceful_completion_requested);
}
