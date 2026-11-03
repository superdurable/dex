// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dexzd_protocol::dex::{InvokeRpcRequest, InvokeWorkerRpcResponse};
use dexzd_protocol::dexzd::v1::RpcCommittedCommand;

const SEEDED_MESSAGES: usize = 40;

fn message_key(index: usize) -> String {
    format!("messages/{index:04}")
}

fn message_text(index: usize) -> String {
    format!("{index:04}-{}", "m".repeat(75))
}

fn turn_options(lock_keys: &[&str]) -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        execute_load_attribute_map_instances: vec!["messages/".to_owned()],
        execute_lock_attribute_keys: lock_keys.iter().map(|key| (*key).to_owned()).collect(),
        ..StepOptions::default()
    }
}

fn next_turn(count: usize) -> StepDecision {
    StepDecision {
        next_steps: (0..count)
            .map(|_| StepMovement {
                step_type: "TurnStep".to_owned(),
                step_options: Some(turn_options(&[])),
                ..StepMovement::default()
            })
            .collect(),
        ..StepDecision::default()
    }
}

fn append_message(index: usize) -> AttributeWrite {
    AttributeWrite {
        key: message_key(index),
        value: Some(string_value(&message_text(index))),
        ..AttributeWrite::default()
    }
}

fn delete_message(index: usize) -> AttributeWrite {
    AttributeWrite {
        key: message_key(index),
        value: Some(Value { kind: None }),
        ..AttributeWrite::default()
    }
}

impl Harness {
    /// Starts a run whose `TurnStep-1` Execute loads the whole `messages/` map.
    fn start_turn_flow(&mut self, seeded_messages: usize, step_options: StepOptions) {
        let mut request = start_request("start-turns");
        request.start_step_type = "TurnStep".to_owned();
        request.step_options = Some(step_options);
        self.start(request);
        for index in 0..seeded_messages {
            self.set_attribute(
                &message_key(index),
                string_value(&message_text(index)),
                &format!("seed-{index}"),
            );
        }
    }

    fn complete_turn(
        &mut self,
        step_execution_id: &str,
        writes: Vec<AttributeWrite>,
        decision: StepDecision,
    ) -> ApplyOutcome {
        self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(key(step_execution_id, StepMethod::Execute)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    upsert_attributes: writes,
                    step_decision: Some(decision),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            app_worker_id: "worker-a".to_owned(),
            ..StepMethodCompletedCommand::default()
        }))
    }

    fn commit_rpc(&mut self, request_id: &str, response: InvokeWorkerRpcResponse) -> ApplyOutcome {
        self.apply(Command::RpcCommitted(RpcCommittedCommand {
            request: Some(InvokeRpcRequest {
                flow_id: self.flow_id.clone(),
                rpc_name: "appendMessage".to_owned(),
                request_id: request_id.to_owned(),
                ..InvokeRpcRequest::default()
            }),
            response: Some(response),
            app_worker_id: "worker-rpc".to_owned(),
            request_fingerprint: request_id.as_bytes().to_vec(),
            ..RpcCommittedCommand::default()
        }))
    }

    fn assert_failed_by_state_limit(&self) {
        let record = self.run_record();
        assert_eq!(record.status, FlowStatus::Failed as i32);
        let close = record.close.expect("the failed run has a close");
        assert!(
            close.error_message.contains("run state"),
            "unexpected close message {:?}",
            close.error_message
        );
    }
}

#[test]
fn state_limit_rejects_a_turn_that_grows_while_freeing_its_snapshot() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.start_attempt("TurnStep-1", StepMethod::Execute, 1);
    harness.set_max_run_state_bytes(1);
    let outcome = harness.complete_turn(
        "TurnStep-1",
        vec![append_message(SEEDED_MESSAGES)],
        next_turn(1),
    );
    assert!(!is_error(&outcome.reply, 8));
    harness.assert_failed_by_state_limit();
    assert!(harness.attribute(&message_key(SEEDED_MESSAGES)).is_none());
}

#[test]
fn state_limit_rejects_a_turn_that_grows_while_freeing_its_lock() {
    let mut harness = Harness::new("order-1");
    let options = StepOptions {
        execute_load_attribute_map_instances: Vec::new(),
        ..turn_options(&["conversation-lock-for-every-turn"])
    };
    harness.start_turn_flow(0, options);
    harness.start_attempt("TurnStep-1", StepMethod::Execute, 1);
    harness.set_max_run_state_bytes(1);
    harness.complete_turn(
        "TurnStep-1",
        vec![AttributeWrite {
            key: "messages/1".to_owned(),
            value: Some(string_value(&"n".repeat(30))),
            ..AttributeWrite::default()
        }],
        StepDecision::default(),
    );
    harness.assert_failed_by_state_limit();
}

#[test]
fn state_limit_allows_a_turn_that_shrinks_state_while_freeing_its_snapshot() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.start_attempt("TurnStep-1", StepMethod::Execute, 1);
    harness.set_max_run_state_bytes(1);
    let outcome = harness.complete_turn(
        "TurnStep-1",
        vec![delete_message(0), delete_message(1), delete_message(2)],
        next_turn(1),
    );
    assert!(!is_error(&outcome.reply, 8));
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    assert!(harness.attribute(&message_key(0)).is_none());
    assert_eq!(
        harness
            .step_execution("TurnStep-2")
            .invocation
            .map(|invocation| invocation.phase),
        Some(StepInvocationPhase::Pending as i32)
    );
}

#[test]
fn state_limit_measures_concurrent_attempts_separately() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.start_attempt("TurnStep-1", StepMethod::Execute, 1);
    harness.complete_turn("TurnStep-1", Vec::new(), next_turn(2));
    harness.start_attempt("TurnStep-2", StepMethod::Execute, 1);
    harness.start_attempt("TurnStep-3", StepMethod::Execute, 1);
    harness.set_max_run_state_bytes(1);
    // TurnStep-3 started after TurnStep-2, so its snapshot must not count against TurnStep-2.
    harness.complete_turn(
        "TurnStep-2",
        vec![delete_message(0), delete_message(1), delete_message(2)],
        StepDecision::default(),
    );
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    assert_eq!(
        harness
            .step_execution("TurnStep-3")
            .invocation
            .map(|invocation| invocation.phase),
        Some(StepInvocationPhase::Running as i32)
    );
    harness.complete_turn(
        "TurnStep-3",
        vec![append_message(SEEDED_MESSAGES)],
        StepDecision::default(),
    );
    harness.assert_failed_by_state_limit();
}

#[test]
fn state_limit_rejects_rpc_growth_but_allows_rpc_reads() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.set_max_run_state_bytes(1);
    let before = harness.state.rows.clone();
    let rejected = harness.commit_rpc(
        "append-1",
        InvokeWorkerRpcResponse {
            upsert_attributes: vec![append_message(SEEDED_MESSAGES)],
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert!(is_error(&rejected.reply, 8), "{:?}", rejected.reply);
    assert!(rejected.writes.is_empty());
    assert_eq!(harness.state.rows, before);

    let read = harness.commit_rpc(
        "read-1",
        InvokeWorkerRpcResponse {
            output: Some(string_value("current messages")),
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert!(
        matches!(read.reply.reply, Some(command_reply::Reply::Rpc(_))),
        "{:?}",
        read.reply
    );
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
}

#[test]
fn attempt_state_is_counted_while_an_attempt_runs_and_released_when_it_ends() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    let storage = |harness: &Harness| harness.run_record().storage.expect("storage is accounted");
    assert_eq!(storage(&harness).attempt_state_bytes, 0);
    harness.start_attempt("TurnStep-1", StepMethod::Execute, 1);
    let running = storage(&harness);
    assert!(running.attempt_state_bytes > (SEEDED_MESSAGES * 80) as u64);
    assert!(running.attempt_state_bytes < running.state_row_bytes);
    assert_eq!(running, harness.recount_storage());
    harness.complete_turn("TurnStep-1", Vec::new(), StepDecision::default());
    let ended = storage(&harness);
    assert_eq!(ended.attempt_state_bytes, 0);
    assert_eq!(ended, harness.recount_storage());
}

#[test]
fn attempt_state_missing_from_an_older_record_saturates_at_zero() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.start_attempt("TurnStep-1", StepMethod::Execute, 1);
    harness.rewrite_run_record(|record| {
        record
            .storage
            .as_mut()
            .expect("storage is accounted")
            .attempt_state_bytes = 0;
    });
    harness.complete_turn("TurnStep-1", Vec::new(), StepDecision::default());
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert_eq!(
        record
            .storage
            .expect("storage is accounted")
            .attempt_state_bytes,
        0
    );
}

impl Harness {
    /// Edits the run record in place and keeps its own row size in `state_row_bytes`.
    fn rewrite_run_record(&mut self, edit: impl FnOnce(&mut RunRecord)) {
        let run_id = RunId::from_slice(&self.run_id).expect("started run");
        let key = run_singleton_key(self.context.flow_hash, run_id, RunTable::RunRecord);
        let original = self.state.rows[&(ColumnFamily::Flow, key.clone())].clone();
        let mut record = RunRecord::decode(original.as_slice()).expect("valid run record");
        edit(&mut record);
        let other_state_bytes = record
            .storage
            .expect("storage is accounted")
            .state_row_bytes
            - original.len() as u64;
        loop {
            let new_state_bytes = other_state_bytes + record.encoded_len() as u64;
            let storage = record.storage.as_mut().expect("storage is accounted");
            if storage.state_row_bytes == new_state_bytes {
                break;
            }
            storage.state_row_bytes = new_state_bytes;
        }
        self.state
            .rows
            .insert((ColumnFamily::Flow, key), record.encode_to_vec());
    }
}
