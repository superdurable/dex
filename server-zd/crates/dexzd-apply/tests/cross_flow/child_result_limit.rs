// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dexzd_protocol::dex::FlowResult;
use dexzd_protocol::dexzd::v1::RunStorageUsage;

const MAX_CHILD_FLOW_RESULT_BYTES: u64 = 1000;

fn limited_settings() -> RunSettings {
    RunSettings {
        max_child_flow_result_bytes: MAX_CHILD_FLOW_RESULT_BYTES,
        ..settings()
    }
}

fn result_text(encoded_bytes: u64) -> Value {
    let mut text = "r".repeat(usize::try_from(encoded_bytes).expect("small test size"));
    loop {
        let value = Value {
            kind: Some(value::Kind::StringValue(text.clone())),
        };
        if value.encoded_len() as u64 <= encoded_bytes {
            return value;
        }
        text.pop();
    }
}

fn force_complete(close_input: Value) -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: CloseDecisionType::ForceComplete as i32,
            close_input: Some(close_input),
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    }
}

impl Harness {
    fn start_limited_child(&mut self) {
        self.start_parent();
        let start = self.register_sub_flow_wait(SubFlowReusePolicy::Unspecified);
        assert_ok(&self.deliver_start_outbox_with_settings(
            &start,
            FIRST_CHILD_RUN,
            limited_settings(),
        ));
    }

    fn complete_child_step(&mut self, run_id: [u8; 16], decision: StepDecision) {
        let invocation = StepInvocationKey {
            step_execution_id: "ChildStep-1".to_owned(),
            method: StepMethod::Execute as i32,
        };
        assert_ok(&self.apply(
            CHILD_ID,
            &run_id,
            Command::StepAttemptStarted(StepAttemptStartedCommand {
                invocation: Some(invocation.clone()),
                attempt: 1,
                task_id: vec![8; 16],
                pull_routing: None,
            }),
        ));
        assert_ok(&self.apply(
            CHILD_ID,
            &run_id,
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

    fn child_completion(&self, run_id: [u8; 16]) -> (OutboxRecord, FlowResult) {
        let outbox = self
            .outbox(CHILD_ID, run_id)
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
        (
            outbox,
            completed.result.expect("the completion has a result"),
        )
    }

    fn parent_sub_flow_result_status(&self) -> (i32, usize) {
        let step = self.step("parent", PARENT_RUN, "StartStep-1");
        assert_eq!(step.phase, StepExecutionPhase::Execute as i32);
        let results = step.condition_results.expect("the wait completed");
        let result = &results.sub_flow_results[0];
        (result.flow_status, result.results.len())
    }

    fn parent_storage(&self) -> RunStorageUsage {
        self.record("parent", PARENT_RUN)
            .storage
            .expect("storage is accounted")
    }

    /// Sets the parent's run state limit and keeps its own row size in `state_row_bytes`.
    fn set_parent_max_run_state_bytes(&mut self, max_run_state_bytes: u64) {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, "parent");
        let key = run_singleton_key(flow_hash, RunId(PARENT_RUN), RunTable::RunRecord);
        let original = self.state.rows[&(ColumnFamily::Flow, key.clone())].clone();
        let mut record = RunRecord::decode(original.as_slice()).expect("valid run record");
        record
            .settings
            .as_mut()
            .expect("run settings exist")
            .max_run_state_bytes = max_run_state_bytes;
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

#[test]
fn oversized_child_result_fails_the_child_and_delivers_a_failure_without_outputs() {
    let mut harness = Harness::new();
    harness.start_limited_child();
    harness.complete_child_step(
        FIRST_CHILD_RUN,
        force_complete(result_text(MAX_CHILD_FLOW_RESULT_BYTES + 1)),
    );
    let child = harness.record(CHILD_ID, FIRST_CHILD_RUN);
    assert_eq!(child.status, FlowStatus::Failed as i32);
    let close = child.close.expect("the child closed");
    assert!(close.results.is_empty());
    assert_eq!(close.error_type, FlowErrorType::WorkerApiFail as i32);
    assert!(
        close
            .error_message
            .contains("limits.maxChildFlowResultBytes"),
        "unexpected message {:?}",
        close.error_message
    );
    let (completion, delivered) = harness.child_completion(FIRST_CHILD_RUN);
    assert_eq!(delivered.flow_status, FlowStatus::Failed as i32);
    assert!(delivered.results.is_empty());
    assert_ok(&harness.deliver_completion(&completion));
    assert_eq!(
        harness.parent_sub_flow_result_status(),
        (FlowStatus::Failed as i32, 0)
    );
}

#[test]
fn child_result_at_the_limit_is_delivered() {
    let mut harness = Harness::new();
    harness.start_limited_child();
    let output = result_text(MAX_CHILD_FLOW_RESULT_BYTES);
    assert_eq!(output.encoded_len() as u64, MAX_CHILD_FLOW_RESULT_BYTES);
    harness.complete_child_step(FIRST_CHILD_RUN, force_complete(output));
    let child = harness.record(CHILD_ID, FIRST_CHILD_RUN);
    assert_eq!(child.status, FlowStatus::Completed as i32);
    let (completion, delivered) = harness.child_completion(FIRST_CHILD_RUN);
    assert_eq!(delivered.flow_status, FlowStatus::Completed as i32);
    assert_eq!(delivered.results.len(), 1);
    assert_ok(&harness.deliver_completion(&completion));
    assert_eq!(
        harness.parent_sub_flow_result_status(),
        (FlowStatus::Completed as i32, 1)
    );
}

#[test]
fn child_completion_applies_when_the_parent_exceeds_its_state_limit() {
    let mut harness = Harness::new();
    harness.start_limited_child();
    harness.complete_child_step(FIRST_CHILD_RUN, force_complete(result_text(500)));
    let (completion, _) = harness.child_completion(FIRST_CHILD_RUN);
    harness.set_parent_max_run_state_bytes(1);
    let before = harness.parent_storage();
    assert_ok(&harness.deliver_completion(&completion));
    let after = harness.parent_storage();
    assert!(
        after.state_row_bytes + after.live_value_bytes
            > before.state_row_bytes + before.live_value_bytes
    );
    assert_eq!(
        harness.parent_sub_flow_result_status(),
        (FlowStatus::Completed as i32, 1)
    );
}
