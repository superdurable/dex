// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_apply::{ApplyContext, FlowStateReader, StateReadError, StateRow, apply_flow_command};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, TenantId};
use dexzd_protocol::dex::{
    AttributeWrite, FlowStartOptions, InvokeExecuteMethodResponse, InvokeWaitForMethodResponse,
    InvokeWorkerRpcResponse, SetAttributesRequest, StartFlowRequest, SubFlowCondition,
    SubFlowOptions, Value, WaitingCondition, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, ChildFlowStartMessage, FlowCommand, FlowStartCommand,
    InboxMessageCommand, RpcCommittedCommand, StepMethodCompletedCommand, command_reply,
    flow_command::Command, inbox_message_command, step_method_completed_command,
};

struct EmptyState;

impl FlowStateReader for EmptyState {
    fn get(&self, _: ColumnFamily, _: &[u8]) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(None)
    }
    fn scan(
        &self,
        _: ColumnFamily,
        _: &[u8],
        _: &[u8],
        _: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(Vec::new())
    }
}

#[test]
fn every_attribute_write_entry_point_rejects_missing_objects_without_an_apply_error() {
    for kind in [
        value::Kind::StringValue("secret".to_owned()),
        value::Kind::IntValue(42),
        value::Kind::InternalBlobIdForStringValue("forged".to_owned()),
    ] {
        let write = AttributeWrite {
            key: "secret".to_owned(),
            value: Some(Value { kind: Some(kind) }),
            is_hard_delete_on_null: true,
            ..AttributeWrite::default()
        };
        let child = SubFlowCondition {
            options: Some(SubFlowOptions {
                attributes: vec![write.clone()],
                ..SubFlowOptions::default()
            }),
            ..SubFlowCondition::default()
        };
        let commands = [
            Command::FlowStart(FlowStartCommand {
                request: Some(StartFlowRequest {
                    flow_start_options: Some(FlowStartOptions {
                        attributes: vec![write.clone()],
                        ..FlowStartOptions::default()
                    }),
                    ..StartFlowRequest::default()
                }),
                ..FlowStartCommand::default()
            }),
            Command::AttributesSet(AttributesSetCommand {
                request: Some(SetAttributesRequest {
                    attributes: vec![write.clone()],
                    ..SetAttributesRequest::default()
                }),
                ..AttributesSetCommand::default()
            }),
            Command::RpcCommitted(RpcCommittedCommand {
                response: Some(InvokeWorkerRpcResponse {
                    upsert_attributes: vec![write.clone()],
                    ..InvokeWorkerRpcResponse::default()
                }),
                ..RpcCommittedCommand::default()
            }),
            Command::StepMethodCompleted(StepMethodCompletedCommand {
                response: Some(step_method_completed_command::Response::WaitFor(
                    InvokeWaitForMethodResponse {
                        upsert_attributes: vec![write.clone()],
                        ..InvokeWaitForMethodResponse::default()
                    },
                )),
                ..StepMethodCompletedCommand::default()
            }),
            Command::StepMethodCompleted(StepMethodCompletedCommand {
                response: Some(step_method_completed_command::Response::Execute(
                    InvokeExecuteMethodResponse {
                        upsert_attributes: vec![write],
                        ..InvokeExecuteMethodResponse::default()
                    },
                )),
                ..StepMethodCompletedCommand::default()
            }),
            Command::StepMethodCompleted(StepMethodCompletedCommand {
                response: Some(step_method_completed_command::Response::WaitFor(
                    InvokeWaitForMethodResponse {
                        waiting_condition: Some(WaitingCondition {
                            sub_flow_conditions: vec![child.clone()],
                            ..WaitingCondition::default()
                        }),
                        ..InvokeWaitForMethodResponse::default()
                    },
                )),
                ..StepMethodCompletedCommand::default()
            }),
            Command::InboxMessage(InboxMessageCommand {
                payload: Some(inbox_message_command::Payload::ChildStart(
                    ChildFlowStartMessage {
                        condition: Some(child),
                        ..ChildFlowStartMessage::default()
                    },
                )),
                ..InboxMessageCommand::default()
            }),
        ];
        let hasher = PartitionHasher::new(&[7; 16], 1024).unwrap();
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, "admission-test");
        let context = ApplyContext {
            partition: hasher.partition_of(flow_hash),
            flow_hash,
        };
        for body in commands {
            let command = FlowCommand {
                flow_id: "admission-test".to_owned(),
                behavior_version: 1,
                command: Some(body),
                ..FlowCommand::default()
            };
            let outcome = apply_flow_command(&context, &EmptyState, &command)
                .expect("untrusted writes return a rejection instead of halting apply");
            let Some(command_reply::Reply::Error(error)) = outcome.reply.reply else {
                panic!("expected input error");
            };
            assert_eq!(error.grpc_code, 3);
            assert!(error.message.contains("no uploaded object"));
            assert!(outcome.writes.is_empty());
            assert!(outcome.effects.is_empty());
        }
    }
}
