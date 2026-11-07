// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dexzd_protocol::dex::{ChannelMessageDeletion, InvokeRpcRequest, InvokeWorkerRpcResponse};
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
        self.commit_rpc_with_transaction(request_id, response, false)
    }

    fn commit_rpc_with_transaction(
        &mut self,
        request_id: &str,
        response: InvokeWorkerRpcResponse,
        is_transactional: bool,
    ) -> ApplyOutcome {
        self.apply(Command::RpcCommitted(RpcCommittedCommand {
            request: Some(InvokeRpcRequest {
                flow_id: self.flow_id.clone(),
                rpc_name: "appendMessage".to_owned(),
                request_id: request_id.to_owned(),
                is_transactional,
                ..InvokeRpcRequest::default()
            }),
            response: Some(response),
            app_worker_id: "worker-rpc".to_owned(),
            request_fingerprint: request_id.as_bytes().to_vec(),
            is_transactional,
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
            value: Some(string_value(&"n".repeat(60))),
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
fn state_limit_rejects_transactional_rpc_growth_without_writes() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.set_max_run_state_bytes(1);
    let before = harness.state.rows.clone();
    let rejected = harness.commit_rpc_with_transaction(
        "append-transactional-1",
        InvokeWorkerRpcResponse {
            upsert_attributes: vec![append_message(SEEDED_MESSAGES)],
            step_decision: Some(next_turn(1)),
            ..InvokeWorkerRpcResponse::default()
        },
        true,
    );
    assert!(is_error(&rejected.reply, 8), "{:?}", rejected.reply);
    assert!(rejected.writes.is_empty());
    assert_eq!(harness.state.rows, before);
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

/// An attempt that runs beside one an older binary started may escape the limit once.
#[test]
fn attempt_concurrent_with_an_older_attempt_escapes_the_limit_at_most_once() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.start_attempt("TurnStep-1", StepMethod::Execute, 1);
    harness.complete_turn("TurnStep-1", Vec::new(), next_turn(2));
    harness.start_attempt("TurnStep-2", StepMethod::Execute, 1);
    harness.set_max_run_state_bytes(1);
    harness.rewrite_run_record(|record| {
        record
            .storage
            .as_mut()
            .expect("storage is accounted")
            .attempt_state_bytes = 0;
    });
    harness.is_storage_recount_checked = false;
    harness.start_attempt("TurnStep-3", StepMethod::Execute, 1);
    harness.complete_turn("TurnStep-2", Vec::new(), StepDecision::default());
    let erased = harness.run_record().storage.expect("storage is accounted");
    assert_eq!(erased.attempt_state_bytes, 0);
    assert!(harness.recount_storage().attempt_state_bytes > 0);

    harness.complete_turn(
        "TurnStep-3",
        vec![append_message(SEEDED_MESSAGES)],
        next_turn(1),
    );
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    assert!(harness.attribute(&message_key(SEEDED_MESSAGES)).is_some());
    assert_eq!(
        harness.run_record().storage.expect("storage is accounted"),
        harness.recount_storage()
    );

    harness.is_storage_recount_checked = true;
    harness.start_attempt("TurnStep-4", StepMethod::Execute, 1);
    harness.complete_turn(
        "TurnStep-4",
        vec![append_message(SEEDED_MESSAGES + 1)],
        next_turn(1),
    );
    harness.assert_failed_by_state_limit();
}

fn read_output(reply: &CommandReply) -> Value {
    let Some(command_reply::Reply::Rpc(rpc)) = &reply.reply else {
        panic!("expected an RPC reply, got {reply:?}");
    };
    match rpc.output.as_ref().and_then(|output| output.kind.as_ref()) {
        Some(stored_value::Kind::InlineValue(value)) => value.clone(),
        other => panic!("expected a complete inline output, got {other:?}"),
    }
}

fn read_response(output: Value) -> InvokeWorkerRpcResponse {
    InvokeWorkerRpcResponse {
        output: Some(output),
        ..InvokeWorkerRpcResponse::default()
    }
}

impl Harness {
    /// Flow rows that a read RPC must not add to: value bookkeeping and request deduplication.
    fn retained_reply_rows(&self) -> Vec<(ColumnFamily, Vec<u8>)> {
        let run_id = RunId::from_slice(&self.run_id).expect("started run");
        let prefix = run_prefix(self.context.flow_hash, run_id);
        self.state
            .rows
            .keys()
            .filter(|(column_family, key)| {
                key.starts_with(&prefix)
                    && (*column_family == ColumnFamily::Value
                        || matches!(
                            parse_run_key(key).expect("valid run key").table,
                            RunTable::ValueBirth
                                | RunTable::ValueHolder
                                | RunTable::RequestDedup
                                | RunTable::RequestDedupOrder
                        ))
            })
            .cloned()
            .collect()
    }
}

#[test]
fn over_budget_read_rpcs_return_outputs_without_storing_them() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.set_max_run_state_bytes(1);
    let before = harness.run_record().storage.expect("storage is accounted");
    let retained_before = harness.retained_reply_rows();
    for (index, output) in [
        string_value("inline output"),
        string_value(&"t".repeat(10_000)),
        string_value("inline output"),
        string_value(&"u".repeat(10_000)),
    ]
    .into_iter()
    .enumerate()
    {
        let read = harness.commit_rpc(&format!("read-{index}"), read_response(output.clone()));
        assert_eq!(read_output(&read.reply), output);
    }
    let after = harness.run_record().storage.expect("storage is accounted");
    assert_eq!(after.live_value_bytes, before.live_value_bytes);
    assert_eq!(after.superseded_value_bytes, before.superseded_value_bytes);
    assert_eq!(harness.retained_reply_rows(), retained_before);
    // Only the run record's own counters can change its size.
    assert!(after.state_row_bytes <= before.state_row_bytes + 4);
}

#[test]
fn over_budget_read_rpc_retry_runs_again_instead_of_replaying() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.set_max_run_state_bytes(1);
    let first = harness.commit_rpc("read-again", read_response(string_value("first")));
    assert_eq!(read_output(&first.reply), string_value("first"));
    let second = harness.commit_rpc("read-again", read_response(string_value("second")));
    assert_eq!(read_output(&second.reply), string_value("second"));
}

#[test]
fn under_budget_read_rpc_retry_replays_its_stored_reply() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    let large_output = string_value(&"v".repeat(10_000));
    let before = harness.run_record().storage.expect("storage is accounted");
    let first = harness.commit_rpc("read-once", read_response(large_output.clone()));
    let Some(command_reply::Reply::Rpc(first_reply)) = &first.reply.reply else {
        panic!("expected an RPC reply, got {:?}", first.reply);
    };
    assert!(matches!(
        first_reply
            .output
            .as_ref()
            .and_then(|output| output.kind.as_ref()),
        Some(stored_value::Kind::TableValue(_))
    ));
    let after = harness.run_record().storage.expect("storage is accounted");
    assert!(after.live_value_bytes > before.live_value_bytes);
    let replayed = harness.commit_rpc("read-once", read_response(string_value("second")));
    assert_eq!(replayed.reply, first.reply);
    assert!(replayed.writes.is_empty());
}

#[test]
fn over_budget_rpc_that_deletes_and_returns_output_is_checked() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.set_max_run_state_bytes(1);
    let before = harness.state.rows.clone();
    let rejected = harness.commit_rpc(
        "delete-and-read",
        InvokeWorkerRpcResponse {
            output: Some(string_value(&"w".repeat(10_000))),
            delete_from_channel: vec![ChannelMessageDeletion {
                channel_name: "missing".to_owned(),
                message_id: "missing".to_owned(),
            }],
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert!(is_error(&rejected.reply, 8), "{:?}", rejected.reply);
    assert!(rejected.writes.is_empty());
    assert_eq!(harness.state.rows, before);
}

fn is_rpc_reply(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Rpc(_)))
}

#[test]
fn over_budget_read_rpcs_keep_no_request_id_for_any_output() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.set_max_run_state_bytes(1);
    let before = harness.run_record().storage.expect("storage is accounted");
    let retained_before = harness.retained_reply_rows();
    let outputs = [
        None,
        Some(Value { kind: None }),
        Some(Value {
            kind: Some(value::Kind::NullValue(0)),
        }),
        Some(string_value(&"o".repeat(10_000))),
    ];
    for is_transactional in [false, true] {
        for (index, output) in outputs.iter().enumerate() {
            // A non-transactional read reaches the commit path only when it records an event.
            let response = InvokeWorkerRpcResponse {
                output: output.clone(),
                record_events: if is_transactional {
                    Vec::new()
                } else {
                    vec![Kv {
                        key: "read".to_owned(),
                        value: Some(string_value("event")),
                    }]
                },
                ..InvokeWorkerRpcResponse::default()
            };
            let request_id = format!("{}-{index}-{is_transactional}", "r".repeat(10_000));
            let first = harness.commit_rpc_with_transaction(
                &request_id,
                response.clone(),
                is_transactional,
            );
            assert!(is_rpc_reply(&first.reply), "{:?}", first.reply);
            let retry =
                harness.commit_rpc_with_transaction(&request_id, response, is_transactional);
            assert!(is_rpc_reply(&retry.reply), "{:?}", retry.reply);
            assert!(
                !retry.writes.is_empty(),
                "a retry runs again instead of replaying"
            );
        }
    }
    let after = harness.run_record().storage.expect("storage is accounted");
    assert_eq!(after.live_value_bytes, before.live_value_bytes);
    assert_eq!(harness.retained_reply_rows(), retained_before);
    // Only run record counters and the sticky Worker ID can change its size.
    assert!(after.state_row_bytes < before.state_row_bytes + 64);
}

#[test]
fn transactional_attribute_delete_retry_replays_after_recreation() {
    for output in [Some(string_value("deleted")), None] {
        let mut harness = Harness::new("order-1");
        harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
        harness.set_max_run_state_bytes(1);
        let delete = InvokeWorkerRpcResponse {
            output,
            upsert_attributes: vec![delete_message(0), delete_message(1)],
            ..InvokeWorkerRpcResponse::default()
        };
        let first = harness.commit_rpc_with_transaction("delete-id", delete.clone(), true);
        assert!(is_rpc_reply(&first.reply), "{:?}", first.reply);
        assert!(harness.attribute(&message_key(0)).is_none());

        harness.set_max_run_state_bytes(1 << 30);
        harness.set_attribute(&message_key(0), string_value("recreated"), "recreate-0");
        harness.set_max_run_state_bytes(1);
        let retry = harness.commit_rpc_with_transaction("delete-id", delete, true);
        assert_eq!(retry.reply, first.reply);
        assert!(retry.writes.is_empty());
        assert!(harness.attribute(&message_key(0)).is_some());
    }
}

#[test]
fn over_budget_attribute_delete_with_output_is_admitted_only_when_it_shrinks() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.set_max_run_state_bytes(1);
    let before = harness.state.rows.clone();
    let growing = harness.commit_rpc(
        "delete-one-with-large-output",
        InvokeWorkerRpcResponse {
            output: Some(string_value(&"g".repeat(10_000))),
            upsert_attributes: vec![delete_message(0)],
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert!(is_error(&growing.reply, 8), "{:?}", growing.reply);
    assert!(growing.writes.is_empty());
    assert_eq!(harness.state.rows, before);

    let shrinking = harness.commit_rpc(
        "delete-three-with-small-output",
        InvokeWorkerRpcResponse {
            output: Some(string_value("deleted")),
            upsert_attributes: vec![delete_message(0), delete_message(1), delete_message(2)],
            ..InvokeWorkerRpcResponse::default()
        },
    );
    assert!(is_rpc_reply(&shrinking.reply), "{:?}", shrinking.reply);
    assert!(harness.attribute(&message_key(0)).is_none());
    assert!(harness.attribute(&message_key(2)).is_none());
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

const SUPERSEDED_REWRITES: usize = 20;
const SUPERSEDED_VALUE_BYTES: usize = 10_000;
/// Room under the limit for a small write; far below the superseded bytes.
const ACTIVE_HEADROOM_BYTES: u64 = 4096;

fn active_bytes(usage: &RunStorageUsage) -> u64 {
    usage.state_row_bytes + usage.live_value_bytes
}

fn active_bytes_without_attempts(usage: &RunStorageUsage) -> u64 {
    active_bytes(usage) - usage.attempt_state_bytes
}

impl Harness {
    /// Rewrites one Attribute with distinct table values, so each earlier value becomes superseded.
    fn build_superseded_values(&mut self) {
        for index in 0..SUPERSEDED_REWRITES {
            let text = format!("{index:04}-{}", "s".repeat(SUPERSEDED_VALUE_BYTES));
            self.set_attribute("report", string_value(&text), &format!("rewrite-{index}"));
        }
    }

    /// Sets the limit `headroom` bytes above active state and checks superseded bytes far exceed it.
    fn limit_active_state_with_headroom(&mut self, headroom: u64) -> u64 {
        let usage = self.run_record().storage.expect("storage is accounted");
        let max_bytes = active_bytes(&usage) + headroom;
        self.set_max_run_state_bytes(max_bytes);
        let usage = self.run_record().storage.expect("storage is accounted");
        assert!(active_bytes(&usage) < max_bytes, "{usage:?}");
        assert!(usage.superseded_value_bytes > 4 * max_bytes, "{usage:?}");
        max_bytes
    }

    /// Completes a WaitFor attempt that writes a new `notes` Attribute of `note_bytes` bytes.
    fn complete_wait_for_with_note(
        &mut self,
        step_execution_id: &str,
        note_bytes: usize,
    ) -> ApplyOutcome {
        self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(key(step_execution_id, StepMethod::WaitFor)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::WaitFor(
                InvokeWaitForMethodResponse {
                    upsert_attributes: vec![AttributeWrite {
                        key: "notes".to_owned(),
                        value: Some(string_value(&"n".repeat(note_bytes))),
                        ..AttributeWrite::default()
                    }],
                    ..InvokeWaitForMethodResponse::default()
                },
            )),
            app_worker_id: "worker-a".to_owned(),
            ..StepMethodCompletedCommand::default()
        }))
    }
}

#[test]
fn superseded_values_above_the_limit_do_not_reject_a_growing_client_write() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("request-1"));
    harness.build_superseded_values();
    harness.limit_active_state_with_headroom(ACTIVE_HEADROOM_BYTES);
    let before = harness.run_record().storage.expect("storage is accounted");
    let accepted = harness.try_set_attribute("notes", string_value(&"n".repeat(1000)), "notes-1");
    assert!(
        !matches!(accepted.reply.reply, Some(command_reply::Reply::Error(_))),
        "{:?}",
        accepted.reply
    );
    let after = harness.run_record().storage.expect("storage is accounted");
    assert!(active_bytes(&after) > active_bytes(&before));
    assert!(harness.attribute("notes").is_some());
}

#[test]
fn superseded_values_above_the_limit_do_not_fail_a_growing_step_completion() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("request-1"));
    harness.build_superseded_values();
    harness.start_attempt("ChargeStep-1", StepMethod::WaitFor, 1);
    harness.limit_active_state_with_headroom(ACTIVE_HEADROOM_BYTES);
    let before = harness.run_record().storage.expect("storage is accounted");
    harness.complete_wait_for_with_note("ChargeStep-1", 1000);
    let record = harness.run_record();
    assert_eq!(
        record.status,
        FlowStatus::Running as i32,
        "{:?}",
        record.close
    );
    let after = record.storage.expect("storage is accounted");
    assert!(active_bytes_without_attempts(&after) > active_bytes_without_attempts(&before));
    assert!(harness.attribute("notes").is_some());
    assert_eq!(
        harness
            .step_execution("ChargeStep-1")
            .invocation
            .map(|invocation| invocation.method),
        Some(StepMethod::Execute as i32)
    );
}

#[test]
fn active_state_above_the_limit_still_rejects_a_growing_client_write() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("request-1"));
    harness.build_superseded_values();
    let max_bytes = harness.limit_active_state_with_headroom(1000);
    let before = harness.state.rows.clone();
    let rejected = harness.try_set_attribute("notes", string_value(&"n".repeat(9000)), "notes-1");
    assert!(is_error(&rejected.reply, 8), "{:?}", rejected.reply);
    let Some(command_reply::Reply::Error(error)) = &rejected.reply.reply else {
        panic!("expected a rejection, got {:?}", rejected.reply);
    };
    assert!(
        error.message.starts_with("active run state of ")
            && error
                .message
                .ends_with(&format!("exceeds the maximum of {max_bytes} bytes")),
        "{error:?}"
    );
    assert!(rejected.writes.is_empty());
    assert_eq!(harness.state.rows, before);
}

#[test]
fn active_state_above_the_limit_still_fails_a_growing_step_completion_without_retry() {
    let mut harness = Harness::new("order-1");
    let mut request = start_request("request-1");
    request.step_options = Some(StepOptions {
        wait_for_retry_policy: Some(RetryPolicy {
            maximum_attempts: 5,
            ..RetryPolicy::default()
        }),
        ..StepOptions::default()
    });
    harness.start(request);
    harness.build_superseded_values();
    harness.start_attempt("ChargeStep-1", StepMethod::WaitFor, 1);
    harness.limit_active_state_with_headroom(1000);
    let outcome = harness.complete_wait_for_with_note("ChargeStep-1", 9000);
    assert!(!is_error(&outcome.reply, 8));
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Failed as i32);
    let close = record.close.expect("the failed run has a close");
    assert!(
        close.error_message.contains("active run state of "),
        "{:?}",
        close.error_message
    );
    assert!(harness.attribute("notes").is_none());
    assert_eq!(
        record.storage.expect("storage is accounted"),
        harness.recount_storage()
    );
}

#[test]
fn superseded_values_above_the_limit_keep_read_rpc_replies_for_deduplication() {
    let mut harness = Harness::new("order-1");
    harness.start_turn_flow(SEEDED_MESSAGES, turn_options(&[]));
    harness.build_superseded_values();
    harness.limit_active_state_with_headroom(ACTIVE_HEADROOM_BYTES);
    let first = harness.commit_rpc(
        "read-once",
        read_response(string_value(&"v".repeat(10_000))),
    );
    assert!(is_rpc_reply(&first.reply), "{:?}", first.reply);
    let replayed = harness.commit_rpc("read-once", read_response(string_value("second")));
    assert_eq!(replayed.reply, first.reply);
    assert!(replayed.writes.is_empty());
}
