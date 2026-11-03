// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Applies a Worker's committed RPC response to one run.

use std::collections::BTreeMap;

use dexzd_keyspace::row_keys::RequestKind;
use dexzd_protocol::dex::{
    ChannelMessageDeletion, ErrorSubStatus, FlowStatus, InvokeWorkerRpcResponse,
    RpcExecutionCompletedEvent, Value, flow_history_event,
};
use dexzd_protocol::dexzd::v1::{
    CommandReply, RequestDedupRecord, RpcCommittedCommand, RpcReply, StoredValue, command_reply,
    stored_value,
};

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::command_handlers::flow_close::{
    finalize_pending_close_if_ready, is_terminal_cleanup_in_progress,
};
use crate::command_handlers::flow_data::{
    apply_attribute_writes, apply_channel_deletions, apply_channel_publications,
};
use crate::command_handlers::run_cursor::{HistoryKind, RunCursor, flow_not_found_reply};
use crate::command_handlers::{CommandScope, HandlerResult, has_attribute_value};
use crate::projections::history_event_links::StepEventLink;
use crate::state_access::FlowTransaction;
use crate::steps::{
    NewStepExecution, cancel_steps_by_type, create_step_execution, wait_conditions,
};
use crate::values::{
    ValueHolder, ValueRejection, complete_worker_value, is_null_value, is_run_over_state_budget,
    store_value, validate_value,
};

pub(crate) fn apply_rpc_committed(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &RpcCommittedCommand,
) -> Result<HandlerResult, ApplyError> {
    let Some(request) = command.request.as_ref() else {
        return Ok(reject_invalid("RPC request is missing"));
    };
    if request.request_id.is_empty() {
        return Ok(reject_invalid("RPC request ID is missing"));
    }
    // Replays answer no client, so they skip request deduplication and store no reply.
    if !scope.is_replay
        && let Some(record) =
            cursor
                .rows
                .request_dedup(transaction, RequestKind::InvokeRpc, &request.request_id)?
    {
        if record.request_fingerprint != command.request_fingerprint {
            return Ok(reject_invalid(format!(
                "request ID {} was already used for a different request",
                request.request_id
            )));
        }
        let reply = record.reply.ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!("RPC dedup entry {} has no reply", request.request_id),
        })?;
        return Ok(HandlerResult::Rejected(reply));
    }
    if cursor.record.status != FlowStatus::Running as i32 {
        return Ok(HandlerResult::Rejected(flow_not_found_reply(
            scope.flow_id(),
        )));
    }
    let Some(response) = command.response.as_ref() else {
        return Ok(reject_invalid("RPC response is missing"));
    };
    let is_transactional = command.is_transactional;
    if let Some(decision) = response.step_decision.as_ref()
        && (decision.close_decision.is_some() || !decision.cancel_sibling_step_types.is_empty())
    {
        return Ok(reject_invalid(
            "RPC Step decision has unsupported operations",
        ));
    }
    if is_transactional
        && let Some(deletion) =
            first_missing_deletion(transaction, &cursor, &response.delete_from_channel)?
    {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::NOT_FOUND,
            ErrorSubStatus::ChannelMessageNotFound,
            format!(
                "Channel message {:?} was not found in channel {:?}",
                deletion.message_id, deletion.channel_name
            ),
        )));
    }
    if let Err(rejection) = apply_attribute_writes(
        transaction,
        &mut cursor,
        &response.upsert_attributes,
        &command.hard_delete_objects,
        response.action_permission_mappings.as_ref(),
        scope,
        true,
    )? {
        return Ok(HandlerResult::from(rejection));
    }
    apply_channel_deletions(transaction, &mut cursor, &response.delete_from_channel)?;
    let changed_channels =
        match apply_channel_publications(transaction, &mut cursor, &response.publish_to_channel)? {
            Ok(channels) => channels,
            Err(rejection) => return Ok(reject_value(rejection)),
        };
    wait_conditions::on_channels_changed(
        transaction,
        &mut cursor,
        scope.now_micros,
        &changed_channels,
    )?;
    // InvokeRPC admits no transactional call during cleanup; earlier ones commit, as in Go.
    // As in the Go Server, an RPC result starts no Step during terminal cleanup.
    let mut started_step_execution_ids = Vec::new();
    if let Some(decision) = response.step_decision.as_ref()
        && !is_terminal_cleanup_in_progress(&cursor)
    {
        if !decision.cancel_step_types.is_empty() {
            cancel_steps_by_type(
                transaction,
                &mut cursor,
                &decision.cancel_step_types,
                &[],
                "",
            )?;
        }
        let source = format!("__rpc/{}", request.rpc_name);
        for movement in &decision.next_steps {
            let new_step = NewStepExecution {
                step_type: &movement.step_type,
                input: movement.step_input.as_ref(),
                options: movement.step_options.as_ref(),
                from_step_execution_id: &source,
                is_timeout_handler: false,
            };
            match create_step_execution(transaction, &mut cursor, scope.now_micros, &new_step)? {
                Ok(step_execution_id) => started_step_execution_ids.push(step_execution_id),
                Err(rejection) => return Ok(reject_value(rejection)),
            }
        }
    }
    if is_transactional && !command.app_worker_id.is_empty() {
        cursor.record.sticky_app_worker_id = command.app_worker_id.clone();
    }
    // A full run neither stores a read's output nor remembers its request ID.
    let should_retain_reply = !is_run_over_state_budget(&cursor.record)
        || is_null_value(response.output.as_ref())
        || is_checked_by_run_state_limit(response);
    let output = if !scope.is_replay
        && let Some(value) = response.output.as_ref()
    {
        match retained_or_complete_output(
            transaction,
            &mut cursor,
            &request.request_id,
            value,
            should_retain_reply,
        )? {
            Ok(output) => Some(output),
            Err(rejection) => return Ok(reject_value(rejection)),
        }
    } else {
        None
    };
    if let Err(rejection) = validate_ephemeral_response_values(transaction, &cursor, response)? {
        return Ok(reject_value(rejection));
    }
    let rpc_event = cursor.add_event(flow_history_event::Payload::RpcExecutionCompleted(
        RpcExecutionCompletedEvent {
            rpc_name: request.rpc_name.clone(),
            input: if is_transactional {
                request.input.clone()
            } else {
                None
            },
            output: if is_transactional {
                response.output.clone()
            } else {
                None
            },
            step_decision: response.step_decision.clone(),
            upsert_attributes: response.upsert_attributes.clone(),
            record_events: response.record_events.clone(),
            publish_to_channel: response.publish_to_channel.clone(),
            delete_from_channel: response.delete_from_channel.clone(),
            served_by_app_worker_id: command.app_worker_id.clone(),
            ..RpcExecutionCompletedEvent::default()
        },
    ));
    for step_execution_id in started_step_execution_ids {
        cursor.link_step_event(
            rpc_event,
            StepEventLink::StartedStepExecution(step_execution_id),
        );
    }
    // The commit of an RPC admitted before cleanup may be the last thing a close waits for.
    finalize_pending_close_if_ready(transaction, &mut cursor, scope)?;
    let reply = CommandReply {
        reply: Some(command_reply::Reply::Rpc(RpcReply { output })),
    };
    if !scope.is_replay && should_retain_reply {
        remember_reply(
            transaction,
            &cursor,
            request.request_id.as_str(),
            command,
            &reply,
        )?;
    }
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        reply,
        HistoryKind::EXTERNAL_INPUT,
    )?))
}

/// Returns whether the run state limit checks this RPC response.
///
/// It checks responses that write Flow state, and responses that keep an output beside deletions
/// or cancellations, whose replies request deduplication must keep for retries.
pub(crate) fn is_checked_by_run_state_limit(response: &InvokeWorkerRpcResponse) -> bool {
    let writes_flow_state = has_attribute_value(&response.upsert_attributes)
        || !response.publish_to_channel.is_empty()
        || response
            .step_decision
            .as_ref()
            .is_some_and(|decision| !decision.next_steps.is_empty())
        || response
            .action_permission_mappings
            .as_ref()
            .is_some_and(|mappings| !mappings.mappings.is_empty());
    let changes_flow_state = !response.delete_from_channel.is_empty()
        || response
            .step_decision
            .as_ref()
            .is_some_and(|decision| !decision.cancel_step_types.is_empty());
    writes_flow_state || (changes_flow_state && !is_null_value(response.output.as_ref()))
}

/// Returns the reply output: stored for request deduplication, or complete and unstored.
fn retained_or_complete_output(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    request_id: &str,
    value: &Value,
    should_retain_reply: bool,
) -> Result<Result<StoredValue, ValueRejection>, ApplyError> {
    let limits = cursor.value_limits();
    if !should_retain_reply {
        return Ok(
            complete_worker_value(transaction, cursor.rows, limits, value)?.map(|complete| {
                StoredValue {
                    kind: Some(stored_value::Kind::InlineValue(complete)),
                }
            }),
        );
    }
    let holder = ValueHolder::RpcReply {
        request_id: request_id.to_owned(),
    };
    store_value(
        transaction,
        cursor.rows,
        limits,
        &mut cursor.values,
        value,
        &holder,
    )
}

fn first_missing_deletion<'a>(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    deletions: &'a [ChannelMessageDeletion],
) -> Result<Option<&'a ChannelMessageDeletion>, ApplyError> {
    let mut remaining_by_channel = BTreeMap::<String, BTreeMap<String, usize>>::new();
    for deletion in deletions {
        if !remaining_by_channel.contains_key(&deletion.channel_name) {
            let mut counts = BTreeMap::<String, usize>::new();
            for message in
                cursor
                    .rows
                    .channel_messages(transaction, &deletion.channel_name, usize::MAX)?
            {
                *counts.entry(message.message_id).or_default() += 1;
            }
            remaining_by_channel.insert(deletion.channel_name.clone(), counts);
        }
        let Some(remaining) = remaining_by_channel
            .get_mut(&deletion.channel_name)
            .and_then(|counts| counts.get_mut(&deletion.message_id))
        else {
            return Ok(Some(deletion));
        };
        if *remaining == 0 {
            return Ok(Some(deletion));
        }
        *remaining -= 1;
    }
    Ok(None)
}

fn validate_ephemeral_response_values(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    response: &InvokeWorkerRpcResponse,
) -> Result<Result<(), ValueRejection>, ApplyError> {
    for event in &response.record_events {
        let Some(value) = event.value.as_ref() else {
            continue;
        };
        match validate_value(transaction, cursor.rows, cursor.value_limits(), value)? {
            Ok(()) => {}
            Err(rejection) => return Ok(Err(rejection)),
        }
    }
    if let Some(mappings) = response.action_permission_mappings.as_ref() {
        for mapping in &mappings.mappings {
            for value in &mapping.equal_values {
                match validate_value(transaction, cursor.rows, cursor.value_limits(), value)? {
                    Ok(()) => {}
                    Err(rejection) => return Ok(Err(rejection)),
                }
            }
        }
    }
    Ok(Ok(()))
}

fn remember_reply(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    request_id: &str,
    command: &RpcCommittedCommand,
    reply: &CommandReply,
) -> Result<(), ApplyError> {
    let capacity = cursor
        .record
        .settings
        .as_ref()
        .map_or(1000, |settings| settings.request_dedup_capacity)
        .max(1);
    cursor.rows.put_request_dedup(
        transaction,
        RequestKind::InvokeRpc,
        request_id,
        &RequestDedupRecord {
            request_fingerprint: command.request_fingerprint.clone(),
            reply: Some(reply.clone()),
            recorded_seq: cursor.seq,
        },
        capacity,
    )?;
    Ok(())
}

fn reject_invalid(message: impl Into<String>) -> HandlerResult {
    HandlerResult::Rejected(error_reply(
        grpc_code::INVALID_ARGUMENT,
        ErrorSubStatus::Uncategorized,
        message,
    ))
}

fn reject_value(rejection: ValueRejection) -> HandlerResult {
    reject_invalid(rejection.message())
}
