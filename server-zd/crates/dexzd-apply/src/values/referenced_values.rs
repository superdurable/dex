// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The values a Worker result, Client write, or RPC result hands to apply, which apply may
//! resolve as value references of the run.

use dexzd_protocol::dex::{
    ActionPermissionMappings, AttributeWrite, ChannelMessage, InvokeExecuteMethodResponse,
    InvokeWaitForMethodResponse, InvokeWorkerRpcResponse, Kv, StepDecision, Value,
};
use dexzd_protocol::dexzd::v1::flow_command::Command;
use dexzd_protocol::dexzd::v1::step_method_completed_command::Response;
use dexzd_protocol::dexzd::v1::{FlowCommand, step_attempt_heartbeat_command};

/// Whether [`command_values`] lists an RPC's output.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum RpcOutputValue {
    Included,
    /// A reapplied RPC returns no output, so time travel leaves it out.
    Excluded,
}

/// Returns every value of `command` that apply may resolve as a value reference, including the
/// inputs it passes to next Steps, SubFlows, and the Flow close. Commands that carry no Worker,
/// Client, or RPC values return none.
pub(crate) fn command_values(command: &FlowCommand, rpc_output: RpcOutputValue) -> Vec<&Value> {
    let mut values = Vec::new();
    match &command.command {
        Some(Command::AttributesSet(set)) => {
            if let Some(request) = &set.request {
                values.extend(attribute_values(&request.attributes));
                values.extend(permission_values(
                    request.action_permission_mappings.as_ref(),
                ));
            }
        }
        Some(Command::ChannelPublish(publish)) => {
            if let Some(request) = &publish.request {
                values.extend(message_values(&request.messages));
            }
        }
        Some(Command::RpcCommitted(rpc)) => {
            if let Some(response) = &rpc.response {
                if rpc_output == RpcOutputValue::Included {
                    values.extend(response.output.iter());
                }
                values.extend(rpc_response_values(response));
            }
        }
        Some(Command::StepMethodCompleted(completed)) => match &completed.response {
            Some(Response::WaitFor(response)) => values.extend(wait_for_values(response)),
            Some(Response::Execute(response)) => values.extend(execute_values(response)),
            None => {}
        },
        Some(Command::StepAttemptHeartbeat(heartbeat)) => {
            if let Some(step_attempt_heartbeat_command::Checkpoint::Value(value)) =
                &heartbeat.checkpoint
            {
                values.push(value);
            }
        }
        _ => {}
    }
    values
}

/// Returns the values of an RPC response apart from its output.
fn rpc_response_values(response: &InvokeWorkerRpcResponse) -> impl Iterator<Item = &Value> {
    attribute_values(&response.upsert_attributes)
        .chain(key_values(&response.record_events))
        .chain(message_values(&response.publish_to_channel))
        .chain(permission_values(
            response.action_permission_mappings.as_ref(),
        ))
        .chain(decision_values(response.step_decision.as_ref()))
}

fn wait_for_values(response: &InvokeWaitForMethodResponse) -> impl Iterator<Item = &Value> {
    let sub_flow_values = response
        .waiting_condition
        .iter()
        .flat_map(|condition| &condition.sub_flow_conditions)
        .flat_map(|condition| {
            condition.step_input.iter().chain(
                condition
                    .options
                    .iter()
                    .flat_map(|options| attribute_values(&options.attributes)),
            )
        });
    attribute_values(&response.upsert_attributes)
        .chain(sub_flow_values)
        .chain(key_values(&response.upsert_step_exe_locals))
        .chain(key_values(&response.record_events))
        .chain(message_values(&response.publish_to_channel))
        .chain(permission_values(
            response.action_permission_mappings.as_ref(),
        ))
}

fn execute_values(response: &InvokeExecuteMethodResponse) -> impl Iterator<Item = &Value> {
    decision_values(response.step_decision.as_ref())
        .chain(attribute_values(&response.upsert_attributes))
        .chain(key_values(&response.record_events))
        .chain(key_values(&response.upsert_step_exe_locals))
        .chain(message_values(&response.publish_to_channel))
        .chain(permission_values(
            response.action_permission_mappings.as_ref(),
        ))
}

/// Returns the next Steps' inputs and the close input of a Step decision.
fn decision_values(decision: Option<&StepDecision>) -> impl Iterator<Item = &Value> {
    decision.into_iter().flat_map(|decision| {
        decision
            .next_steps
            .iter()
            .filter_map(|movement| movement.step_input.as_ref())
            .chain(
                decision
                    .close_decision
                    .iter()
                    .filter_map(|close| close.close_input.as_ref()),
            )
    })
}

fn attribute_values(writes: &[AttributeWrite]) -> impl Iterator<Item = &Value> {
    writes.iter().filter_map(|write| write.value.as_ref())
}

fn key_values(values: &[Kv]) -> impl Iterator<Item = &Value> {
    values.iter().filter_map(|kv| kv.value.as_ref())
}

fn message_values(messages: &[ChannelMessage]) -> impl Iterator<Item = &Value> {
    messages.iter().filter_map(|message| message.value.as_ref())
}

fn permission_values(mappings: Option<&ActionPermissionMappings>) -> impl Iterator<Item = &Value> {
    mappings
        .into_iter()
        .flat_map(|mappings| &mappings.mappings)
        .flat_map(|mapping| &mapping.equal_values)
}
