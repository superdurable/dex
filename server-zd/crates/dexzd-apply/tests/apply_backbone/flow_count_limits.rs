// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dexzd_protocol::dex::{
    ChannelMessage, InvokeRpcRequest, InvokeWorkerRpcResponse, PublishToChannelRequest,
};
use dexzd_protocol::dexzd::v1::{ChannelPublishCommand, RpcCommittedCommand};

const MAX_ACTIVE_STEPS: u32 = 3;
const MAX_CHANNEL_MESSAGES: u32 = 2;
const RESOURCE_EXHAUSTED: i32 = 8;

fn work_options() -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    }
}

fn work_steps(count: usize) -> Vec<StepMovement> {
    (0..count)
        .map(|_| StepMovement {
            step_type: "WorkStep".to_owned(),
            step_options: Some(work_options()),
            ..StepMovement::default()
        })
        .collect()
}

fn go_to_work_steps(count: usize) -> StepDecision {
    StepDecision {
        next_steps: work_steps(count),
        ..StepDecision::default()
    }
}

fn messages(channel_name: &str, count: usize) -> Vec<ChannelMessage> {
    (0..count)
        .map(|index| ChannelMessage {
            channel_name: channel_name.to_owned(),
            message_id: format!("{channel_name}-{index}"),
            value: Some(string_value("message")),
        })
        .collect()
}

fn assert_rejected_with(outcome: &ApplyOutcome, expected_text: &str) {
    let Some(command_reply::Reply::Error(error)) = &outcome.reply.reply else {
        panic!("expected a rejection, got {:?}", outcome.reply);
    };
    assert_eq!(error.grpc_code, RESOURCE_EXHAUSTED, "{:?}", error.message);
    assert!(error.message.contains(expected_text), "{:?}", error.message);
}

impl Harness {
    /// Starts a run whose `WorkStep-1` Execute is running, with the given Step limit.
    fn start_step_limit_flow(&mut self, max_active_step_executions: u32) {
        self.start_count_limit_flow(RunSettings {
            max_active_step_executions,
            ..settings()
        });
    }

    /// Starts a run whose `WorkStep-1` Execute is running, with the given Channel limit.
    fn start_channel_limit_flow(&mut self, max_channel_messages: u32) {
        self.start_count_limit_flow(RunSettings {
            max_channel_messages,
            ..settings()
        });
    }

    fn start_count_limit_flow(&mut self, run_settings: RunSettings) {
        let mut request = start_request("start-count-limit");
        request.start_step_type = "WorkStep".to_owned();
        request.step_options = Some(work_options());
        self.start_with_settings(request, run_settings);
        self.start_attempt("WorkStep-1", StepMethod::Execute, 1);
    }

    fn publish_messages(&mut self, messages: Vec<ChannelMessage>) -> ApplyOutcome {
        self.apply(Command::ChannelPublish(ChannelPublishCommand {
            request: Some(PublishToChannelRequest {
                messages,
                ..PublishToChannelRequest::default()
            }),
        }))
    }

    fn commit_count_limit_rpc(
        &mut self,
        request_id: &str,
        response: InvokeWorkerRpcResponse,
    ) -> ApplyOutcome {
        self.apply(Command::RpcCommitted(RpcCommittedCommand {
            request: Some(InvokeRpcRequest {
                flow_id: self.flow_id.clone(),
                rpc_name: "addWork".to_owned(),
                request_id: request_id.to_owned(),
                ..InvokeRpcRequest::default()
            }),
            response: Some(response),
            app_worker_id: "worker-rpc".to_owned(),
            request_fingerprint: request_id.as_bytes().to_vec(),
            ..RpcCommittedCommand::default()
        }))
    }

    fn assert_failed_with(&self, expected_text: &str) {
        let record = self.run_record();
        assert_eq!(record.status, FlowStatus::Failed as i32);
        let close = record.close.expect("the failed run has a close");
        assert!(
            close.error_message.contains(expected_text),
            "unexpected close message {:?}",
            close.error_message
        );
    }
}

#[test]
fn step_limit_fails_a_step_whose_next_steps_exceed_it() {
    let mut harness = Harness::new("order-1");
    harness.start_step_limit_flow(MAX_ACTIVE_STEPS);
    let outcome = harness.complete_execute(
        "WorkStep-1",
        1,
        go_to_work_steps(MAX_ACTIVE_STEPS as usize + 1),
    );
    assert!(!is_error(&outcome.reply, RESOURCE_EXHAUSTED));
    harness.assert_failed_with("limits.maxConcurrentStepsPerFlow");
    assert!(harness.step_execution_if_present("WorkStep-2").is_none());
}

#[test]
fn step_limit_allows_next_steps_up_to_it() {
    let mut harness = Harness::new("order-1");
    harness.start_step_limit_flow(MAX_ACTIVE_STEPS);
    harness.complete_execute("WorkStep-1", 1, go_to_work_steps(MAX_ACTIVE_STEPS as usize));
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    for step_number in 2..=MAX_ACTIVE_STEPS + 1 {
        harness.step_execution(&format!("WorkStep-{step_number}"));
    }
}

#[test]
fn step_limit_allows_a_move_that_keeps_the_count_at_it() {
    let mut harness = Harness::new("order-1");
    harness.start_step_limit_flow(1);
    // The completed Step ends as its successor starts, so the count stays at the limit.
    harness.complete_execute("WorkStep-1", 1, go_to_work_steps(1));
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    harness.step_execution("WorkStep-2");
}

#[test]
fn step_limit_rejects_an_rpc_whose_next_steps_exceed_it() {
    let mut harness = Harness::new("order-1");
    harness.start_step_limit_flow(MAX_ACTIVE_STEPS);
    let accepted = harness.commit_count_limit_rpc(
        "rpc-within-limit",
        InvokeWorkerRpcResponse {
            step_decision: Some(go_to_work_steps(MAX_ACTIVE_STEPS as usize - 1)),
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert!(!is_error(&accepted.reply, RESOURCE_EXHAUSTED));
    let rejected = harness.commit_count_limit_rpc(
        "rpc-over-limit",
        InvokeWorkerRpcResponse {
            step_decision: Some(go_to_work_steps(1)),
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert_rejected_with(&rejected, "limits.maxConcurrentStepsPerFlow");
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    assert!(
        harness
            .step_execution_if_present(&format!("WorkStep-{}", MAX_ACTIVE_STEPS + 1))
            .is_none()
    );
}

#[test]
fn step_limit_zero_sets_no_limit() {
    let mut harness = Harness::new("order-1");
    harness.start_step_limit_flow(0);
    harness.complete_execute("WorkStep-1", 1, go_to_work_steps(5));
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    harness.step_execution("WorkStep-6");
}

#[test]
fn channel_limit_rejects_a_client_publish_over_it_per_channel() {
    let mut harness = Harness::new("order-1");
    harness.start_channel_limit_flow(MAX_CHANNEL_MESSAGES);
    let accepted = harness.publish_messages(messages("orders", MAX_CHANNEL_MESSAGES as usize));
    assert!(!is_error(&accepted.reply, RESOURCE_EXHAUSTED));
    let rejected = harness.publish_messages(vec![ChannelMessage {
        channel_name: "orders".to_owned(),
        message_id: "orders-over".to_owned(),
        value: Some(string_value("message")),
    }]);
    assert_rejected_with(&rejected, "limits.maxChannelMessages");
    // The limit counts each Channel on its own.
    let other_channel = harness.publish_messages(messages("refunds", 1));
    assert!(!is_error(&other_channel.reply, RESOURCE_EXHAUSTED));
}

#[test]
fn channel_limit_rejects_a_whole_publish_request_over_it() {
    let mut harness = Harness::new("order-1");
    harness.start_channel_limit_flow(MAX_CHANNEL_MESSAGES);
    let rejected = harness.publish_messages(messages("orders", MAX_CHANNEL_MESSAGES as usize + 1));
    assert_rejected_with(&rejected, "limits.maxChannelMessages");
    // The rejected request stored none of its messages.
    let accepted = harness.publish_messages(messages("orders", MAX_CHANNEL_MESSAGES as usize));
    assert!(!is_error(&accepted.reply, RESOURCE_EXHAUSTED));
}

#[test]
fn channel_limit_fails_a_step_that_publishes_over_it() {
    let mut harness = Harness::new("order-1");
    harness.start_channel_limit_flow(MAX_CHANNEL_MESSAGES);
    let outcome = harness.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(key("WorkStep-1", StepMethod::Execute)),
        attempt: 1,
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                publish_to_channel: messages("orders", MAX_CHANNEL_MESSAGES as usize + 1),
                step_decision: Some(StepDecision::default()),
                ..InvokeExecuteMethodResponse::default()
            },
        )),
        app_worker_id: "worker-a".to_owned(),
        ..StepMethodCompletedCommand::default()
    }));
    assert!(!is_error(&outcome.reply, RESOURCE_EXHAUSTED));
    harness.assert_failed_with("limits.maxChannelMessages");
}

#[test]
fn channel_limit_rejects_an_rpc_that_publishes_over_it() {
    let mut harness = Harness::new("order-1");
    harness.start_channel_limit_flow(MAX_CHANNEL_MESSAGES);
    let rejected = harness.commit_count_limit_rpc(
        "rpc-publish-over-limit",
        InvokeWorkerRpcResponse {
            publish_to_channel: messages("orders", MAX_CHANNEL_MESSAGES as usize + 1),
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert_rejected_with(&rejected, "limits.maxChannelMessages");
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
}

#[test]
fn channel_limit_zero_sets_no_limit() {
    let mut harness = Harness::new("order-1");
    harness.start_channel_limit_flow(0);
    let accepted = harness.publish_messages(messages("orders", 5));
    assert!(!is_error(&accepted.reply, RESOURCE_EXHAUSTED));
}
