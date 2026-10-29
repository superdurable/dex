// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Persisted records written before the push Worker target and local activity lineage fields were
//! removed still decode, and the removed fields are skipped as unknown fields.

use dexzd_protocol::dex::{
    FlowConfig, InvokeExecuteMethodResponse, InvokeWaitForMethodResponse, Kv, StepDecision,
    StepDurability, WaitingCondition, WaitingConditionType, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::{
    FlowStartCommand, RunRecord, StepMethodCompletedCommand, step_method_completed_command,
};
use prost::Message;
use prost::encoding::{WireType, encode_key, encode_varint};

const REMOVED_WORKER_TARGET_ADDRESS: &[u8] = b"worker.example:9000";

#[test]
fn worker_target_with_removed_address_fields_decodes_as_pull_target() {
    let decoded = WorkerTarget::decode(legacy_worker_target().as_slice())
        .expect("legacy WorkerTarget decodes");

    assert_eq!(decoded, WorkerTarget { is_pull_mode: true });
    assert_eq!(decoded.encode_to_vec(), vec![0x18, 0x01]);
}

#[test]
fn persisted_flow_configs_with_removed_address_fields_decode() {
    let expected_flow_config = FlowConfig {
        step_durability: Some(StepDurability::Async as i32),
        worker_target: Some(WorkerTarget { is_pull_mode: true }),
        ..FlowConfig::default()
    };
    let flow_config_bytes = legacy_flow_config();
    assert_eq!(
        FlowConfig::decode(flow_config_bytes.as_slice()).expect("legacy FlowConfig decodes"),
        expected_flow_config
    );

    let mut run_record_bytes = Vec::new();
    append_varint_field(&mut run_record_bytes, 11, 3);
    append_bytes_field(&mut run_record_bytes, 14, &flow_config_bytes);
    let run_record =
        RunRecord::decode(run_record_bytes.as_slice()).expect("legacy RunRecord decodes");
    assert_eq!(run_record.current_attempt, 3);
    assert_eq!(run_record.flow_config, Some(expected_flow_config.clone()));
    assert!(!contains_removed_address(&run_record.encode_to_vec()));

    let mut start_command_bytes = Vec::new();
    append_bytes_field(&mut start_command_bytes, 1, b"run-1");
    append_bytes_field(&mut start_command_bytes, 5, &legacy_worker_target());
    append_bytes_field(&mut start_command_bytes, 8, &flow_config_bytes);
    let start_command = FlowStartCommand::decode(start_command_bytes.as_slice())
        .expect("legacy FlowStartCommand decodes");
    assert_eq!(start_command.new_run_id, b"run-1".to_vec());
    assert_eq!(
        start_command.worker_target,
        Some(WorkerTarget { is_pull_mode: true })
    );
    assert_eq!(
        start_command.effective_flow_config,
        Some(expected_flow_config)
    );
}

#[test]
fn step_method_responses_with_removed_local_activity_metadata_decode() {
    let waiting_condition = WaitingCondition {
        waiting_condition_type: WaitingConditionType::AllCompleted as i32,
        ..WaitingCondition::default()
    };
    let local = Kv {
        key: "cursor".to_owned(),
        value: None,
    };
    let mut wait_for_bytes = legacy_local_activity_metadata_field();
    append_bytes_field(&mut wait_for_bytes, 3, &waiting_condition.encode_to_vec());
    append_bytes_field(&mut wait_for_bytes, 4, &local.encode_to_vec());
    let mut wait_for_command_bytes = Vec::new();
    append_varint_field(&mut wait_for_command_bytes, 2, 1);
    append_bytes_field(&mut wait_for_command_bytes, 3, &wait_for_bytes);
    let wait_for_command = StepMethodCompletedCommand::decode(wait_for_command_bytes.as_slice())
        .expect("legacy WaitFor completion decodes");
    assert_eq!(wait_for_command.attempt, 1);
    assert_eq!(
        wait_for_command.response,
        Some(step_method_completed_command::Response::WaitFor(
            InvokeWaitForMethodResponse {
                waiting_condition: Some(waiting_condition),
                upsert_step_exe_locals: vec![local],
                ..InvokeWaitForMethodResponse::default()
            }
        ))
    );

    let step_decision = StepDecision {
        cancel_step_types: vec!["ReviewStep".to_owned()],
        ..StepDecision::default()
    };
    let mut execute_bytes = legacy_local_activity_metadata_field();
    append_bytes_field(&mut execute_bytes, 2, &step_decision.encode_to_vec());
    let mut execute_command_bytes = Vec::new();
    append_bytes_field(&mut execute_command_bytes, 4, &execute_bytes);
    let execute_command = StepMethodCompletedCommand::decode(execute_command_bytes.as_slice())
        .expect("legacy Execute completion decodes");
    assert_eq!(
        execute_command.response,
        Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                step_decision: Some(step_decision),
                ..InvokeExecuteMethodResponse::default()
            }
        ))
    );
}

/// Encodes the removed address (1) and is_headless_address (2) beside is_pull_mode (3).
fn legacy_worker_target() -> Vec<u8> {
    let mut bytes = Vec::new();
    append_bytes_field(&mut bytes, 1, REMOVED_WORKER_TARGET_ADDRESS);
    append_varint_field(&mut bytes, 2, 1);
    append_varint_field(&mut bytes, 3, 1);
    bytes
}

fn legacy_flow_config() -> Vec<u8> {
    let mut bytes = Vec::new();
    append_varint_field(&mut bytes, 4, StepDurability::Async as u64);
    append_bytes_field(&mut bytes, 5, &legacy_worker_target());
    bytes
}

/// Encodes the removed local_activity_metadata (1) with both of its string fields.
fn legacy_local_activity_metadata_field() -> Vec<u8> {
    let mut metadata = Vec::new();
    append_bytes_field(&mut metadata, 1, b"ReviewStep-2");
    append_bytes_field(&mut metadata, 2, b"ReviewStep-1");
    let mut bytes = Vec::new();
    append_bytes_field(&mut bytes, 1, &metadata);
    bytes
}

fn append_bytes_field(buffer: &mut Vec<u8>, field_number: u32, payload: &[u8]) {
    encode_key(field_number, WireType::LengthDelimited, buffer);
    encode_varint(payload.len() as u64, buffer);
    buffer.extend_from_slice(payload);
}

fn append_varint_field(buffer: &mut Vec<u8>, field_number: u32, value: u64) {
    encode_key(field_number, WireType::Varint, buffer);
    encode_varint(value, buffer);
}

fn contains_removed_address(bytes: &[u8]) -> bool {
    bytes
        .windows(REMOVED_WORKER_TARGET_ADDRESS.len())
        .any(|window| window == REMOVED_WORKER_TARGET_ADDRESS)
}
