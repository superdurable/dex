// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Client writes: SetAttributes, PublishToChannel, DeleteChannelMessage, StopFlow, and
//! UpdateFlowConfig.

use dexzd_keyspace::row_keys::RequestKind;
use dexzd_protocol::dex::{
    ChannelExternalDeleteEvent, ChannelExternalPublishEvent, ErrorSubStatus, FlowErrorType,
    RpcExecutionCompletedEvent, StopType, WorkerTarget, flow_history_event,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, ChannelMessageDeleteCommand, ChannelPublishCommand, CommandReply,
    FlowConfigUpdateCommand, FlowStopCommand, RequestDedupRecord,
};
use dexzd_protocol::task_queue_names::overridden_task_queue;

use crate::ApplyError;
use crate::command_handlers::command_replies::{
    CLOSED_RUN_STOP_MESSAGE, error_reply, grpc_code, ok_reply, terminal_cleanup_reply,
};
use crate::command_handlers::flow_close::{
    FlowClosing, close_flow_immediately, is_terminal_cleanup_in_progress,
};
use crate::command_handlers::flow_data::{
    EffectValueReferences, apply_attribute_writes, apply_channel_deletions,
    apply_channel_publications, find_pending_message,
};
use crate::command_handlers::run_cursor::{HistoryKind, RunCursor};
use crate::command_handlers::{CommandScope, HandlerResult};
use crate::history_offload::segment_expiry::update_history_object_delete_pending;
use crate::projections::history_events::reference_effect_values;
use crate::state_access::FlowTransaction;
use crate::steps::wait_conditions;
use crate::values::is_null_value;

/// Returns the recorded reply for a repeated request ID, or a rejection for a reused ID with
/// different content (plan C7).
fn deduplicate(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    scope: &CommandScope<'_>,
    kind: RequestKind,
    request_id: &str,
    fingerprint: &[u8],
) -> Result<Option<CommandReply>, ApplyError> {
    // Replays answer no client, so they skip request deduplication and store no reply.
    if request_id.is_empty() || scope.is_replay {
        return Ok(None);
    }
    let Some(record) = cursor.rows.request_dedup(transaction, kind, request_id)? else {
        return Ok(None);
    };
    if record.request_fingerprint != fingerprint {
        return Ok(Some(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            format!("request ID {request_id} was already used for a different request"),
        )));
    }
    Ok(Some(record.reply.unwrap_or_else(ok_reply)))
}

fn remember_request(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    scope: &CommandScope<'_>,
    kind: RequestKind,
    request_id: &str,
    fingerprint: &[u8],
    reply: &CommandReply,
) -> Result<(), ApplyError> {
    if request_id.is_empty() || scope.is_replay {
        return Ok(());
    }
    let capacity = cursor
        .record
        .settings
        .as_ref()
        .map_or(1000, |settings| settings.request_dedup_capacity);
    cursor.rows.put_request_dedup(
        transaction,
        kind,
        request_id,
        &RequestDedupRecord {
            request_fingerprint: fingerprint.to_vec(),
            reply: Some(reply.clone()),
            recorded_seq: cursor.seq,
        },
        capacity,
    )
}

fn closed_flow_reply(flow_id: &str) -> CommandReply {
    crate::command_handlers::run_cursor::flow_not_found_reply(flow_id)
}

fn is_running(cursor: &RunCursor) -> bool {
    cursor.record.status == dexzd_protocol::dex::FlowStatus::Running as i32
}

fn is_closed_hard_delete_erase(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    request: &dexzd_protocol::dex::SetAttributesRequest,
    hard_delete_objects: &[dexzd_protocol::dexzd::v1::HardDeleteObjectReference],
) -> Result<bool, ApplyError> {
    if request.attributes.is_empty()
        || request.action_permission_mappings.is_some()
        || !hard_delete_objects.is_empty()
    {
        return Ok(false);
    }
    for write in &request.attributes {
        if !is_null_value(write.value.as_ref())
            || !cursor
                .rows
                .attribute(transaction, &write.key)?
                .is_some_and(|record| record.is_hard_delete_on_null)
        {
            return Ok(false);
        }
    }
    Ok(true)
}

pub(crate) fn apply_attributes_set(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &AttributesSetCommand,
) -> Result<HandlerResult, ApplyError> {
    let request = command.request.clone().unwrap_or_default();
    if let Some(reply) = deduplicate(
        transaction,
        &cursor,
        scope,
        RequestKind::SetAttributes,
        &request.request_id,
        &command.request_fingerprint,
    )? {
        return Ok(HandlerResult::Rejected(reply));
    }
    let is_closed = !is_running(&cursor);
    if is_closed
        && !is_closed_hard_delete_erase(
            transaction,
            &cursor,
            &request,
            &command.hard_delete_objects,
        )?
    {
        return Ok(HandlerResult::Rejected(closed_flow_reply(scope.flow_id())));
    }
    let attribute_references = match apply_attribute_writes(
        transaction,
        &mut cursor,
        &request.attributes,
        &command.hard_delete_objects,
        request.action_permission_mappings.as_ref(),
        scope,
        !is_closed,
    )? {
        Ok(references) => references,
        Err(rejection) => return Ok(HandlerResult::from(rejection)),
    };
    let mut upsert_attributes = request.attributes.clone();
    reference_effect_values(
        EffectValueReferences {
            attributes: attribute_references,
            ..EffectValueReferences::default()
        },
        &mut upsert_attributes,
        &mut [],
        &mut [],
    );
    cursor.add_event(flow_history_event::Payload::RpcExecutionCompleted(
        RpcExecutionCompletedEvent {
            upsert_attributes,
            is_set_attribute_api: true,
            ..RpcExecutionCompletedEvent::default()
        },
    ));
    let reply = ok_reply();
    remember_request(
        transaction,
        &cursor,
        scope,
        RequestKind::SetAttributes,
        &request.request_id,
        &command.request_fingerprint,
        &reply,
    )?;
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        reply,
        HistoryKind::EXTERNAL_INPUT,
    )?))
}

pub(crate) fn apply_channel_publish(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &ChannelPublishCommand,
) -> Result<HandlerResult, ApplyError> {
    let request = command.request.clone().unwrap_or_default();
    if !is_running(&cursor) {
        return Ok(HandlerResult::Rejected(closed_flow_reply(scope.flow_id())));
    }
    // A pending close canceled every Step and starts none, so nothing could consume the message.
    if is_terminal_cleanup_in_progress(&cursor) {
        return Ok(HandlerResult::Rejected(terminal_cleanup_reply()));
    }
    let published = match apply_channel_publications(transaction, &mut cursor, &request.messages)? {
        Ok(published) => published,
        Err(rejection) => {
            return Ok(HandlerResult::Rejected(error_reply(
                grpc_code::INVALID_ARGUMENT,
                ErrorSubStatus::Uncategorized,
                rejection.message(),
            )));
        }
    };
    let mut messages = request.messages;
    reference_effect_values(
        EffectValueReferences {
            publications: published.value_references,
            ..EffectValueReferences::default()
        },
        &mut [],
        &mut messages,
        &mut [],
    );
    cursor.add_event(flow_history_event::Payload::ChannelExternalPublish(
        ChannelExternalPublishEvent { messages },
    ));
    wait_conditions::on_channels_changed(
        transaction,
        &mut cursor,
        scope.now_micros,
        &published.changed_channels,
    )?;
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        ok_reply(),
        HistoryKind::EXTERNAL_INPUT,
    )?))
}

pub(crate) fn apply_channel_message_delete(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &ChannelMessageDeleteCommand,
) -> Result<HandlerResult, ApplyError> {
    let request = command.request.clone().unwrap_or_default();
    if let Some(reply) = deduplicate(
        transaction,
        &cursor,
        scope,
        RequestKind::DeleteChannelMessage,
        &request.request_id,
        &command.request_fingerprint,
    )? {
        return Ok(HandlerResult::Rejected(reply));
    }
    if !is_running(&cursor) {
        return Ok(HandlerResult::Rejected(closed_flow_reply(scope.flow_id())));
    }
    // The Go Server deletes through a workflow update, which terminal cleanup rejects.
    if is_terminal_cleanup_in_progress(&cursor) {
        return Ok(HandlerResult::Rejected(terminal_cleanup_reply()));
    }
    if find_pending_message(
        transaction,
        &cursor,
        &request.channel_name,
        &request.message_id,
    )?
    .is_none()
    {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::NOT_FOUND,
            ErrorSubStatus::ChannelMessageNotFound,
            format!(
                "Channel message {:?} was not found in channel {:?}",
                request.message_id, request.channel_name
            ),
        )));
    }
    let deletion = dexzd_protocol::dex::ChannelMessageDeletion {
        channel_name: request.channel_name.clone(),
        message_id: request.message_id.clone(),
    };
    apply_channel_deletions(transaction, &mut cursor, std::slice::from_ref(&deletion))?;
    cursor.add_event(flow_history_event::Payload::ChannelExternalDelete(
        ChannelExternalDeleteEvent {
            messages: vec![deletion],
        },
    ));
    let reply = ok_reply();
    remember_request(
        transaction,
        &cursor,
        scope,
        RequestKind::DeleteChannelMessage,
        &request.request_id,
        &command.request_fingerprint,
        &reply,
    )?;
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        reply,
        HistoryKind::EXTERNAL_INPUT,
    )?))
}

pub(crate) fn apply_flow_stop(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &FlowStopCommand,
) -> Result<HandlerResult, ApplyError> {
    let request = command.request.clone().unwrap_or_default();
    if !is_running(&cursor) {
        // As in the Go Server, a closed run cannot be stopped, even when a newer run exists.
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::NOT_FOUND,
            ErrorSubStatus::FlowNotExists,
            CLOSED_RUN_STOP_MESSAGE,
        )));
    }
    let stop_type = StopType::try_from(request.stop_type).unwrap_or(StopType::Unspecified);
    let closing = match stop_type {
        StopType::Unspecified | StopType::Cancel => FlowClosing::canceled(),
        StopType::Terminate => FlowClosing::terminated(if request.reason.is_empty() {
            // The Go Server's default termination reason, spelling included.
            "Force termiantion from user".to_owned()
        } else {
            request.reason.clone()
        }),
        StopType::Fail => {
            let reason = if request.reason.is_empty() {
                "fail by client".to_owned()
            } else {
                request.reason.clone()
            };
            FlowClosing::failed(FlowErrorType::ClientApiFailingFlow, reason)
        }
    };
    close_flow_immediately(transaction, &mut cursor, scope, closing)?;
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        ok_reply(),
        HistoryKind::SEMANTIC,
    )?))
}

/// Merges FlowConfig field by field; the Worker target and Attribute Store names replace the
/// whole value (as in the Go Server).
///
/// The task queue is fixed when the Flow starts, so an update never changes it.
pub(crate) fn apply_flow_config_update(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &FlowConfigUpdateCommand,
) -> Result<HandlerResult, ApplyError> {
    let update = command
        .request
        .as_ref()
        .and_then(|request| request.flow_config.clone())
        .unwrap_or_default();
    let config = cursor
        .record
        .flow_config
        .get_or_insert_with(Default::default);
    let worker_target = match update.worker_target.as_ref() {
        Some(target) => match updated_worker_target(config.worker_target.as_ref(), target) {
            Ok(updated) => Some(updated),
            Err(reply) => return Ok(HandlerResult::Rejected(reply)),
        },
        None => None,
    };
    if update.active_step_search_mode.is_some() {
        config.active_step_search_mode = update.active_step_search_mode;
    }
    if update.step_durability.is_some() {
        config.step_durability = update.step_durability;
    }
    if worker_target.is_some() {
        config.worker_target = worker_target;
    }
    if update.attribute_store_names.is_some() {
        config.attribute_store_names = update.attribute_store_names;
    }
    if update.history_window_events.is_some() {
        config.history_window_events = update.history_window_events;
    }
    if update.delete_expired_history_objects.is_some() {
        config.delete_expired_history_objects = update.delete_expired_history_objects;
        // Turning deletion on deletes what already expired; turning it off keeps the rest.
        update_history_object_delete_pending(transaction, &mut cursor, false)?;
    }
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        ok_reply(),
        HistoryKind::BOOKKEEPING,
    )?))
}

/// Returns the Worker target an update sets: an empty task queue keeps the run's.
///
/// Returns the INVALID_ARGUMENT reply when `target` selects another task queue.
fn updated_worker_target(
    current: Option<&WorkerTarget>,
    target: &WorkerTarget,
) -> Result<WorkerTarget, CommandReply> {
    let current_task_queue = current.map_or("", |current| current.task_queue.as_str());
    let task_queue = overridden_task_queue(current_task_queue, &target.task_queue);
    if task_queue != current_task_queue {
        return Err(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            format!(
                "worker_target.task_queue cannot change from {} to {}: the task queue is fixed when the Flow starts",
                quote_task_queue(current_task_queue),
                quote_task_queue(&task_queue),
            ),
        ));
    }
    Ok(WorkerTarget {
        is_pull_mode: target.is_pull_mode,
        task_queue,
    })
}

/// Returns `task_queue` quoted, or "the default task queue" for the empty name.
fn quote_task_queue(task_queue: &str) -> String {
    if task_queue.is_empty() {
        "the default task queue".to_owned()
    } else {
        format!("\"{task_queue}\"")
    }
}
