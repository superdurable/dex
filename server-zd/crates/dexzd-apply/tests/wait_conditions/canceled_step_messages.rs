// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Messages a canceled Step's wait consumed before its Execute committed return to their
//! channels, and a commit that publishes and cancels never hands its messages to a Step it
//! cancels.

use super::*;

use dexzd_apply::ApplyEffect;
use dexzd_protocol::dex::{InvokeRpcRequest, InvokeWorkerRpcResponse};
use dexzd_protocol::dexzd::v1::{ChannelMessageRecord, RpcCommittedCommand};

const CHANNEL_MESSAGE_HOLDER: u8 = 0x03;
const CONDITION_RESULT_HOLDER: u8 = 0x06;

impl Harness {
    /// Starts a Flow whose first Step creates one Step of each type, each with a pending WaitFor.
    fn start_steps(&mut self, step_types: &[&str]) {
        let mut request = start_request();
        request.start_step_type = "PlanStep".to_owned();
        request.step_options = Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        });
        self.start(request);
        self.start_attempt("PlanStep-1", StepMethod::Execute, 1);
        self.complete_execute(
            "PlanStep-1",
            StepDecision {
                next_steps: step_types
                    .iter()
                    .map(|step_type| movement(step_type))
                    .collect(),
                ..StepDecision::default()
            },
        );
    }

    /// Runs the Step's empty WaitFor and starts its Execute.
    fn run_to_execute(&mut self, step_execution_id: &str) {
        self.wait_on(step_execution_id, WaitingCondition::default(), Vec::new());
        self.start_attempt(step_execution_id, StepMethod::Execute, 1);
    }

    fn complete_execute_publishing(
        &mut self,
        step_execution_id: &str,
        decision: StepDecision,
        publications: Vec<ChannelMessage>,
    ) -> ApplyOutcome {
        let outcome = self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation_key(step_execution_id, StepMethod::Execute)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(decision),
                    publish_to_channel: publications,
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }));
        assert!(
            is_ok(&outcome.reply),
            "Execute completion: {:?}",
            outcome.reply
        );
        outcome
    }

    fn commit_rpc(
        &mut self,
        decision: StepDecision,
        publications: Vec<ChannelMessage>,
    ) -> ApplyOutcome {
        let outcome = self.apply(Command::RpcCommitted(RpcCommittedCommand {
            request: Some(InvokeRpcRequest {
                flow_id: self.flow_id.clone(),
                rpc_name: "replaceWorker".to_owned(),
                request_id: "rpc-1".to_owned(),
                ..InvokeRpcRequest::default()
            }),
            response: Some(InvokeWorkerRpcResponse {
                step_decision: Some(decision),
                publish_to_channel: publications,
                ..InvokeWorkerRpcResponse::default()
            }),
            request_fingerprint: vec![1],
            ..RpcCommittedCommand::default()
        }));
        assert!(
            matches!(outcome.reply.reply, Some(command_reply::Reply::Rpc(_))),
            "RPC commit: {:?}",
            outcome.reply
        );
        outcome
    }

    /// Pending message texts of one channel in FIFO order.
    fn pending_texts(&self, channel_name: &str) -> Vec<String> {
        self.table_rows(RunTable::ChannelMessage)
            .into_iter()
            .map(|(_, bytes)| {
                ChannelMessageRecord::decode(bytes.as_slice()).expect("the message decodes")
            })
            .filter(|record| record.channel_name == channel_name)
            .map(|record| record.message_id.trim_start_matches("id-").to_owned())
            .collect()
    }

    fn counts(&self, channel_name: &str) -> (u64, u64) {
        let meta = self.channel_meta(channel_name);
        (meta.pending_count, meta.claimed_count)
    }

    fn is_canceled(&self, step_execution_id: &str) -> bool {
        self.get::<StepExecutionRecord>(RunTable::StepExecution, step_execution_id)
            .is_none_or(|step| phase(&step) == StepExecutionPhase::Canceled)
    }
}

fn movement(step_type: &str) -> StepMovement {
    StepMovement {
        step_type: step_type.to_owned(),
        ..StepMovement::default()
    }
}

fn exactly_one(channel_name: &str) -> WaitingCondition {
    condition(
        WaitingConditionType::AnyCompleted,
        Vec::new(),
        vec![channel("c", channel_name, None, None)],
    )
}

fn cancel(step_type: &str) -> StepDecision {
    StepDecision {
        cancel_step_types: vec![step_type.to_owned()],
        ..StepDecision::default()
    }
}

fn close_decision(close_decision_type: CloseDecisionType, channel_names: &[&str]) -> CloseDecision {
    CloseDecision {
        close_decision_type: close_decision_type as i32,
        conditional_channel_names: channel_names
            .iter()
            .map(|name| (*name).to_owned())
            .collect(),
        ..CloseDecision::default()
    }
}

/// Step execution IDs whose Execute became pending in the outcome.
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
fn a_canceled_step_returns_its_consumed_messages_to_the_front_in_order() {
    let mut harness = Harness::new();
    harness.start_steps(&["WaitStep", "ControlStep"]);
    harness.publish("orders", &["m1", "m2", "m3"]);
    harness.publish("tasks/alpha", &["a1"]);
    harness.publish("tasks/beta", &["b1", "b2"]);
    harness.wait_on(
        "WaitStep-1",
        condition(
            WaitingConditionType::AllCompleted,
            Vec::new(),
            vec![
                channel("c", "orders", Some(2), Some(2)),
                channel("d", "tasks/alpha", None, None),
                channel("e", "tasks/beta", Some(2), None),
            ],
        ),
        Vec::new(),
    );
    assert_eq!(
        phase(&harness.step("WaitStep-1")),
        StepExecutionPhase::Execute
    );
    harness.publish("orders", &["m4"]);
    harness.publish("tasks/alpha", &["a2"]);
    assert_eq!(harness.pending_texts("orders"), texts(&["m3", "m4"]));
    assert_eq!(harness.counts("orders"), (2, 2));

    harness.run_to_execute("ControlStep-1");
    harness.complete_execute("ControlStep-1", cancel("WaitStep"));

    assert!(harness.is_canceled("WaitStep-1"));
    assert_eq!(
        harness.pending_texts("orders"),
        texts(&["m1", "m2", "m3", "m4"])
    );
    assert_eq!(harness.pending_texts("tasks/alpha"), texts(&["a1", "a2"]));
    assert_eq!(harness.pending_texts("tasks/beta"), texts(&["b1", "b2"]));
    assert_eq!(harness.counts("orders"), (4, 0));
    assert_eq!(harness.counts("tasks/alpha"), (2, 0));
    assert_eq!(harness.counts("tasks/beta"), (2, 0));
}

#[test]
fn a_requeued_value_returns_to_its_channel_holder_without_becoming_garbage() {
    let mut harness = Harness::new();
    harness.start_steps(&["WaitStep", "ControlStep"]);
    let large = "x".repeat(500);
    harness.publish("orders", &[large.as_str()]);
    harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
    assert_eq!(harness.holder_kinds(), vec![CONDITION_RESULT_HOLDER]);
    harness.run_to_execute("ControlStep-1");
    harness.complete_execute("ControlStep-1", cancel("WaitStep"));
    assert_eq!(harness.holder_kinds(), vec![CHANNEL_MESSAGE_HOLDER]);
    assert!(harness.table_rows(RunTable::ValueGarbage).is_empty());
    assert_eq!(harness.pending_texts("orders"), vec![large]);
    assert_eq!(harness.counts("orders"), (1, 0));
}

#[test]
fn canceling_a_running_execute_requeues_and_ignores_its_late_result() {
    let mut harness = Harness::new();
    harness.start_steps(&["WaitStep", "OtherStep", "ControlStep"]);
    let large = "y".repeat(500);
    harness.publish("orders", &[large.as_str()]);
    harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
    harness.start_attempt("WaitStep-1", StepMethod::Execute, 1);
    harness.run_to_execute("ControlStep-1");
    harness.complete_execute("ControlStep-1", cancel("WaitStep"));
    assert_eq!(harness.pending_texts("orders"), vec![large.clone()]);
    assert_eq!(harness.holder_kinds(), vec![CHANNEL_MESSAGE_HOLDER]);
    assert!(harness.table_rows(RunTable::ValueGarbage).is_empty());
    let late = harness.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(invocation_key("WaitStep-1", StepMethod::Execute)),
        attempt: 1,
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse::default(),
        )),
        ..StepMethodCompletedCommand::default()
    }));
    assert!(
        late.writes.is_empty(),
        "a canceled Step's result changes nothing"
    );
    assert_eq!(harness.pending_texts("orders"), vec![large.clone()]);
    assert_eq!(harness.counts("orders"), (1, 0));
    harness.wait_on("OtherStep-1", exactly_one("orders"), Vec::new());
    assert_eq!(
        phase(&harness.step("OtherStep-1")),
        StepExecutionPhase::Execute
    );
    assert_eq!(harness.holder_kinds(), vec![CONDITION_RESULT_HOLDER]);
    assert_eq!(harness.counts("orders"), (0, 1));
    harness.start_attempt("OtherStep-1", StepMethod::Execute, 1);
    harness.complete_execute("OtherStep-1", StepDecision::default());
    assert!(harness.holder_kinds().is_empty());
    assert_eq!(harness.table_rows(RunTable::ValueGarbage).len(), 1);
    assert_eq!(harness.counts("orders"), (0, 0));
}

#[test]
fn another_waiter_takes_a_requeued_message_in_the_same_commit() {
    let mut harness = Harness::new();
    harness.start_steps(&["WaitStep", "OtherStep", "ControlStep"]);
    harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
    harness.wait_on("OtherStep-1", exactly_one("orders"), Vec::new());
    harness.publish("orders", &["m1"]);
    assert_eq!(
        harness.consumed_texts("WaitStep-1"),
        vec![(COMPLETED, texts(&["m1"]))]
    );
    harness.run_to_execute("ControlStep-1");
    let outcome = harness.complete_execute("ControlStep-1", cancel("WaitStep"));
    assert!(harness.is_canceled("WaitStep-1"));
    assert_eq!(
        harness.consumed_texts("OtherStep-1"),
        vec![(COMPLETED, texts(&["m1"]))]
    );
    assert_eq!(pending_executes(&outcome), vec!["OtherStep-1".to_owned()]);
    assert!(harness.pending_texts("orders").is_empty());
    assert_eq!(harness.counts("orders"), (0, 1));
}

#[test]
fn canceling_steps_of_one_type_requeues_all_of_them_before_waking() {
    let mut harness = Harness::new();
    harness.start_steps(&["WaitStep", "WaitStep", "ControlStep"]);
    harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
    harness.wait_on("WaitStep-2", exactly_one("orders"), Vec::new());
    harness.publish("orders", &["m1"]);
    assert_eq!(
        phase(&harness.step("WaitStep-2")),
        StepExecutionPhase::Waiting
    );
    harness.run_to_execute("ControlStep-1");
    let outcome = harness.complete_execute("ControlStep-1", cancel("WaitStep"));
    assert!(harness.is_canceled("WaitStep-1") && harness.is_canceled("WaitStep-2"));
    assert!(pending_executes(&outcome).is_empty());
    assert_eq!(harness.pending_texts("orders"), texts(&["m1"]));
    assert_eq!(harness.counts("orders"), (1, 0));
}

#[test]
fn a_flow_stop_drops_consumed_messages() {
    let mut harness = Harness::new();
    harness.start_waiting_flow(None);
    harness.publish("orders", &["m1"]);
    harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
    let stopped = harness.apply(Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            stop_type: StopType::Cancel as i32,
            ..StopFlowRequest::default()
        }),
    }));
    assert!(is_ok(&stopped.reply));
    assert!(harness.pending_texts("orders").is_empty());
    assert_eq!(harness.counts("orders"), (0, 0));
}

#[test]
fn a_decision_that_cancels_and_force_completes_drops_consumed_messages() {
    for close_type in [
        CloseDecisionType::ForceComplete,
        CloseDecisionType::ForceFail,
    ] {
        let mut harness = Harness::new();
        harness.start_steps(&["WaitStep", "OtherStep", "ControlStep"]);
        harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
        harness.wait_on("OtherStep-1", exactly_one("orders"), Vec::new());
        harness.publish("orders", &["m1"]);
        harness.run_to_execute("ControlStep-1");
        let outcome = harness.complete_execute(
            "ControlStep-1",
            StepDecision {
                close_decision: Some(close_decision(close_type, &[])),
                ..cancel("WaitStep")
            },
        );
        assert_ne!(harness.run_record().status, FlowStatus::Running as i32);
        assert!(pending_executes(&outcome).is_empty(), "{close_type:?}");
        assert!(harness.pending_texts("orders").is_empty(), "{close_type:?}");
        assert_eq!(harness.counts("orders"), (0, 0), "{close_type:?}");
    }
}

#[test]
fn a_force_close_drops_a_publication_that_a_waiter_took_in_the_same_commit() {
    let closes = [
        close_decision(CloseDecisionType::ForceComplete, &[]),
        close_decision(CloseDecisionType::ForceCompleteOnChannelsEmpty, &["other"]),
    ];
    for close in closes {
        let mut harness = Harness::new();
        harness.start_steps(&["Worker", "Controller"]);
        harness.wait_on("Worker-1", exactly_one("work"), Vec::new());
        harness.run_to_execute("Controller-1");
        let outcome = harness.complete_execute_publishing(
            "Controller-1",
            StepDecision {
                close_decision: Some(close.clone()),
                ..StepDecision::default()
            },
            vec![message("work", "m1")],
        );
        let close_type = close.close_decision_type;
        assert_eq!(
            harness.run_record().status,
            FlowStatus::Completed as i32,
            "{close_type}"
        );
        assert_eq!(
            pending_executes(&outcome),
            vec!["Worker-1".to_owned()],
            "{close_type}: the waiter took the message before the close"
        );
        assert!(harness.is_canceled("Worker-1"), "{close_type}");
        assert!(harness.pending_texts("work").is_empty(), "{close_type}");
        assert_eq!(harness.counts("work"), (0, 0), "{close_type}");
    }
}

#[test]
fn a_graceful_close_after_a_requeueing_cancel_leaves_the_messages_pending() {
    let mut harness = Harness::new();
    harness.start_steps(&["WaitStep", "ControlStep"]);
    harness.publish("orders", &["m1"]);
    harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
    harness.run_to_execute("ControlStep-1");
    harness.complete_execute(
        "ControlStep-1",
        StepDecision {
            close_decision: Some(close_decision(CloseDecisionType::GracefulComplete, &[])),
            ..cancel("WaitStep")
        },
    );
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
    assert!(harness.is_canceled("WaitStep-1"));
    assert_eq!(harness.pending_texts("orders"), texts(&["m1"]));
    assert_eq!(harness.counts("orders"), (1, 0));
}

#[test]
fn force_complete_on_empty_channels_sees_requeued_messages() {
    let mut harness = Harness::new();
    harness.start_steps(&["WaitStep", "ControlStep"]);
    harness.publish("orders", &["m1"]);
    harness.wait_on("WaitStep-1", exactly_one("orders"), Vec::new());
    harness.run_to_execute("ControlStep-1");
    harness.complete_execute(
        "ControlStep-1",
        StepDecision {
            close_decision: Some(close_decision(
                CloseDecisionType::ForceCompleteOnChannelsEmpty,
                &["orders"],
            )),
            ..cancel("WaitStep")
        },
    );
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    assert_eq!(harness.pending_texts("orders"), texts(&["m1"]));
    assert_eq!(harness.counts("orders"), (1, 0));
}

#[test]
fn a_step_decision_that_publishes_and_cancels_leaves_the_message_pending() {
    let mut harness = Harness::new();
    harness.start_steps(&["Worker", "Controller"]);
    harness.wait_on("Worker-1", exactly_one("work"), Vec::new());
    harness.run_to_execute("Controller-1");
    let outcome = harness.complete_execute_publishing(
        "Controller-1",
        StepDecision {
            next_steps: vec![movement("Worker")],
            ..cancel("Worker")
        },
        vec![message("work", "m1")],
    );
    assert!(harness.is_canceled("Worker-1"));
    assert!(pending_executes(&outcome).is_empty(), "Worker-1 never woke");
    assert_eq!(harness.pending_texts("work"), texts(&["m1"]));
    assert_eq!(harness.counts("work"), (1, 0));
    harness.wait_on("Worker-2", exactly_one("work"), Vec::new());
    assert_eq!(
        harness.consumed_texts("Worker-2"),
        vec![(COMPLETED, texts(&["m1"]))]
    );
}

#[test]
fn a_publishing_cancel_serves_the_remaining_waiters_in_fifo_order() {
    let mut harness = Harness::new();
    harness.start_steps(&["Worker", "OtherStep", "Controller"]);
    harness.wait_on("Worker-1", exactly_one("work"), Vec::new());
    harness.wait_on("OtherStep-1", exactly_one("work"), Vec::new());
    harness.run_to_execute("Controller-1");
    harness.complete_execute_publishing(
        "Controller-1",
        cancel("Worker"),
        vec![message("work", "m1"), message("work", "m2")],
    );
    assert!(harness.is_canceled("Worker-1"));
    assert_eq!(
        harness.consumed_texts("OtherStep-1"),
        vec![(COMPLETED, texts(&["m1"]))]
    );
    assert_eq!(harness.pending_texts("work"), texts(&["m2"]));
    assert_eq!(harness.counts("work"), (1, 1));
}

#[test]
fn an_rpc_that_publishes_and_cancels_leaves_the_message_pending() {
    let mut harness = Harness::new();
    harness.start_steps(&["Worker", "OtherStep"]);
    harness.wait_on("Worker-1", exactly_one("work"), Vec::new());
    harness.wait_on("OtherStep-1", exactly_one("work"), Vec::new());
    let outcome = harness.commit_rpc(
        StepDecision {
            next_steps: vec![movement("Worker")],
            ..cancel("Worker")
        },
        vec![message("work", "m1"), message("work", "m2")],
    );
    assert!(harness.is_canceled("Worker-1"));
    assert_eq!(pending_executes(&outcome), vec!["OtherStep-1".to_owned()]);
    assert_eq!(
        harness.consumed_texts("OtherStep-1"),
        vec![(COMPLETED, texts(&["m1"]))]
    );
    assert_eq!(harness.pending_texts("work"), texts(&["m2"]));
    harness.wait_on("Worker-2", exactly_one("work"), Vec::new());
    assert_eq!(
        harness.consumed_texts("Worker-2"),
        vec![(COMPLETED, texts(&["m2"]))]
    );
    assert_eq!(harness.counts("work"), (0, 2));
}

#[test]
fn an_rpc_cancel_requeues_messages_a_wait_consumed_earlier() {
    let mut harness = Harness::new();
    harness.start_steps(&["Worker"]);
    harness.publish("work", &["m1", "m2"]);
    harness.wait_on("Worker-1", exactly_one("work"), Vec::new());
    harness.commit_rpc(
        StepDecision {
            next_steps: vec![movement("Worker")],
            ..cancel("Worker")
        },
        Vec::new(),
    );
    assert!(harness.is_canceled("Worker-1"));
    assert_eq!(harness.pending_texts("work"), texts(&["m1", "m2"]));
    assert_eq!(harness.counts("work"), (2, 0));
}
