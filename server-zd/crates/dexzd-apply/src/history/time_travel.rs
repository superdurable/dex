// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! TimeTravel (plan section 8.4): forks attempt n+1 from a point and reapplies later input.

use std::collections::BTreeSet;

use dexzd_keyspace::row_keys::{active_work_index_key, retention_index_key, timer_index_key};
use dexzd_keyspace::{ColumnFamily, RunTable};
use dexzd_protocol::dex::{
    ErrorSubStatus, FlowErrorType, FlowStatus, TimeTravelForkHistoryEvent, flow_history_event,
};
use dexzd_protocol::dexzd::v1::{
    AttemptRecord, AttributeRecord, CheckpointRunStatus, CommandErrorReply, CommandReply,
    FlowCommand, RetentionTimerId, RunRecord, StepExecutionRecord, StepInvocationKey,
    StepInvocationPhase, StepInvocationRecord, TimeTravelCommand, TimeTravelReply, TimerId,
    TimerRecord, command_reply, flow_command::Command, timer_id,
};
use prost::Message;

use crate::command_handlers::command_replies::{TERMINAL_CLEANUP_MESSAGE, error_reply, grpc_code};
use crate::command_handlers::flow_close::{
    FlowClosing, close_flow, close_flow_immediately, finalize_pending_close_if_ready,
    update_directory_status,
};
use crate::command_handlers::run_cursor::{HistoryKind, RunCursor};
use crate::command_handlers::{CommandScope, HandlerResult, ReplayedCommandCounters};
use crate::history::attempt_scope::{
    AttemptRow, decode_timer_id, hex, holder_rows, read_attempt_rows, run_status_of, set_run_status,
};
use crate::history::lineage::{Lineage, history_records};
use crate::history::replay_state::{TransactionReader, absorb_writes};
use crate::history::reset_target::{TravelTarget, resolve_target};
use crate::history::state_rebuild::rebuild_attempt_state;
use crate::projections::attribute_sync::{
    has_attribute_store_names, mark_attribute_sync_entries, pending_attribute_sync_keys,
};
use crate::state_access::{FlowTransaction, read_flow_directory};
use crate::steps::{
    arm_timer, configured_method_timeout_seconds, disarm_timer, invocation_retry_deadline_micros,
};
use crate::values::ValueIdAllocator;
use crate::{ApplyEffect, ApplyError, BehaviorVersion, WaitableChange};

pub(crate) fn apply_time_travel(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &TimeTravelCommand,
) -> Result<HandlerResult, ApplyError> {
    let request = command.request.clone().unwrap_or_default();
    let previous_attempt = cursor.attempt();
    let selected_attempt = match u32::try_from(request.run_attempt) {
        Ok(0) => previous_attempt,
        Ok(attempt) if attempt <= previous_attempt => attempt,
        _ => {
            return Ok(HandlerResult::Rejected(error_reply(
                grpc_code::INVALID_ARGUMENT,
                ErrorSubStatus::Uncategorized,
                format!(
                    "run attempt {} does not exist; the run has attempts 1 to {previous_attempt}",
                    request.run_attempt
                ),
            )));
        }
    };
    let directory = read_flow_directory(transaction, scope.tenant_id, scope.flow_id())?;
    let is_latest_run = directory
        .runs
        .last()
        .is_some_and(|run| run.run_id == cursor.run_id().as_bytes());
    if !is_latest_run {
        // ID reuse decisions read only the latest run, so an older run must stay closed.
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "only the latest run of Flow {} can time travel",
                scope.flow_id()
            ),
        )));
    }
    let lineage = Lineage::of_attempt(transaction, cursor.rows, selected_attempt)?;
    let target = match resolve_target(transaction, &cursor, scope.now_micros, &request, &lineage)? {
        Ok(target) => target,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    let is_reapplying = !request.skip_writes_reapply;
    if is_reapplying && cursor.record.max_pruned_external_input_seq > target.point.seq {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "external input after the target point left the time travel window (run seq {}), \
                 so it cannot be reapplied; set skip_writes_reapply to travel without reapplying \
                 writes",
                cursor.record.max_pruned_external_input_seq
            ),
        )));
    }
    let reapplied_inputs = if is_reapplying {
        collect_external_inputs(transaction, &cursor, &lineage, target.point.seq)?
    } else {
        Vec::new()
    };
    let rebuilt = rebuild_attempt_state(transaction, cursor.rows, &cursor.record, target.point)?;
    let restored_closing = restored_closing(&rebuilt.run_status, &target)?;
    let previous_rows = read_attempt_rows(transaction, cursor.rows)?;
    let pending_sync_keys = pending_attribute_sync_keys(transaction, cursor.rows)?;
    let previous_status = run_status_of(&cursor.record);
    let was_running = cursor.record.status == FlowStatus::Running as i32;
    cancel_running_invocations(&mut cursor, &previous_rows)?;
    let new_attempt = previous_attempt + 1;
    cursor.rows.put_attempt_record(
        transaction,
        &AttemptRecord {
            attempt: new_attempt,
            parent_attempt: target.point.attempt,
            parent_seq: target.point.seq,
            start_seq: cursor.seq,
            created_at_micros: scope.now_micros,
            reason: request.reason.clone(),
            checkpoint_seqs: Vec::new(),
        },
    );
    cursor.record.current_attempt = new_attempt;
    set_run_status(&mut cursor.record, &rebuilt.run_status);
    cursor.is_starting_attempt = true;
    replace_attempt_rows(
        transaction,
        &mut cursor,
        scope,
        &previous_rows,
        rebuilt.rows,
    )?;
    prepare_run_lifecycle(transaction, &mut cursor, scope, was_running)?;
    let restored_rows = read_attempt_rows(transaction, cursor.rows)?;
    crate::values::rewrite_holders_for_time_travel(
        transaction,
        &cursor,
        &holder_rows(&previous_rows, &previous_status),
        &holder_rows(&restored_rows, &run_status_of(&cursor.record)),
        cursor.seq,
    )?;
    // Remarks precede reapplied input, so a reapplied natural close waits; the reset's end
    // re-checks it.
    // Marks synced Attributes of the abandoned state, so stores converge on the restored one (D16).
    let mut remark_keys = pending_sync_keys;
    remark_keys.extend(synced_row_keys(&previous_rows)?);
    // Returns false without Attribute Stores, because marking then creates no entry.
    let has_pending_remarks = has_attribute_store_names(&cursor.record)
        && (!remark_keys.is_empty() || !synced_row_keys(&restored_rows)?.is_empty());
    let mut pending_store_names =
        mark_attribute_sync_entries(transaction, &mut cursor, &remark_keys, scope.now_micros)?;
    let reapplied_events = reapply_external_inputs(
        transaction,
        &mut cursor,
        scope,
        &reapplied_inputs,
        has_pending_remarks,
    )?;
    apply_received_child_results(transaction, &mut cursor, scope, has_pending_remarks)?;
    let final_rows = read_attempt_rows(transaction, cursor.rows)?;
    pending_store_names.extend(mark_attribute_sync_entries(
        transaction,
        &mut cursor,
        &synced_row_keys(&final_rows)?,
        scope.now_micros,
    )?);
    let mut remaining_effects = Vec::with_capacity(cursor.effects.len());
    for effect in std::mem::take(&mut cursor.effects) {
        match effect {
            ApplyEffect::AttributeSyncPending {
                run_id,
                store_names,
            } if run_id == cursor.run_id() => pending_store_names.extend(store_names),
            other => remaining_effects.push(other),
        }
    }
    cursor.effects = remaining_effects;
    if !pending_store_names.is_empty() {
        cursor.add_effect(ApplyEffect::AttributeSyncPending {
            run_id: cursor.run_id(),
            store_names: pending_store_names.into_iter().collect(),
        });
    }
    dispatch_restored_invocations(&mut cursor, &final_rows)?;
    cursor.add_event(flow_history_event::Payload::TimeTravelFork(
        TimeTravelForkHistoryEvent {
            previous_run_id: cursor.run_id().to_string(),
            previous_run_attempt: i32::try_from(previous_attempt).unwrap_or(i32::MAX),
            target_run_attempt: i32::try_from(target.point.attempt).unwrap_or(i32::MAX),
            target_event_id: target.last_event_id,
        },
    ));
    for event in reapplied_events {
        cursor.add_event(event);
    }
    if let Some((closing, is_closed_at_once)) = restored_closing {
        close_restored_attempt(transaction, &mut cursor, scope, closing, is_closed_at_once)?;
    } else {
        // A restored or reapplied pending close finalizes now when nothing it waits for remains.
        finalize_pending_close_if_ready(transaction, &mut cursor, scope)?;
    }
    cursor.wake(WaitableChange::RunAttemptChanged);
    let reply = CommandReply {
        reply: Some(command_reply::Reply::TimeTravel(TimeTravelReply {
            run_attempt: new_attempt,
        })),
    };
    let kind = if cursor.reapplied_inputs.is_empty() {
        HistoryKind::SEMANTIC
    } else {
        HistoryKind::EXTERNAL_INPUT
    };
    let output = cursor.finish(transaction, scope, reply, kind)?;
    Ok(HandlerResult::Applied(output))
}

/// External input of the lineage after `target_seq`, in seq order. StopFlow is never input.
///
/// A TimeTravel record contributes the input it reapplied.
fn collect_external_inputs(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    lineage: &Lineage,
    target_seq: u64,
) -> Result<Vec<FlowCommand>, ApplyError> {
    let mut inputs = Vec::new();
    for record in history_records(transaction, cursor.rows, target_seq + 1, u64::MAX)? {
        if !record.is_external_input || !lineage.contains(record.attempt, record.seq) {
            continue;
        }
        let Some(command) = record.command else {
            continue;
        };
        if matches!(command.command, Some(Command::TimeTravel(_))) {
            inputs.extend(record.reapplied_inputs);
        } else if is_reapplied_input(&command) {
            inputs.push(command);
        }
    }
    Ok(inputs)
}

fn is_reapplied_input(command: &FlowCommand) -> bool {
    matches!(
        command.command,
        Some(
            Command::AttributesSet(_)
                | Command::ChannelPublish(_)
                | Command::ChannelMessageDelete(_)
                | Command::RpcCommitted(_)
                | Command::TimerSkip(_)
        )
    )
}

/// Tells Workers to stop every attempt the previous attempt state was running.
fn cancel_running_invocations(
    cursor: &mut RunCursor,
    previous_rows: &[AttemptRow],
) -> Result<(), ApplyError> {
    for step in step_executions(previous_rows)? {
        if let Some(invocation) = &step.invocation
            && invocation.phase == StepInvocationPhase::Running as i32
        {
            let run_id = cursor.run_id();
            cursor.add_effect(ApplyEffect::InvocationCanceled {
                run_id,
                task_id: invocation.task_id.clone(),
            });
        }
    }
    Ok(())
}

/// Replaces attempt rows; hard-delete Attributes stay current.
///
/// Restored running attempts get new dispatch IDs, so old cancellations miss them. Restored
/// calls restart their retry total duration, as a Go reset schedules a fresh activity.
fn replace_attempt_rows(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    previous_rows: &[AttemptRow],
    rebuilt_rows: Vec<AttemptRow>,
) -> Result<(), ApplyError> {
    let rows = cursor.rows;
    let partition = transaction.context().partition;
    let mut hard_delete_keys = BTreeSet::new();
    for row in previous_rows {
        if row.is_hard_delete_attribute()? {
            hard_delete_keys.insert(row.key_suffix.clone());
            continue;
        }
        if row.table == RunTable::Timer {
            let timer = decode_timer_record(row)?;
            transaction.delete(
                ColumnFamily::PartitionIndex,
                timer_index_key(
                    partition,
                    timer.fire_at_micros,
                    rows.flow_hash,
                    rows.run_id,
                    &row.key_suffix,
                ),
            );
        }
        transaction.delete(ColumnFamily::Flow, row.key(rows));
    }
    let mut restored_running_count = 0_u32;
    for row in rebuilt_rows {
        match row.table {
            RunTable::Attribute => {
                if row.is_hard_delete_attribute()? || hard_delete_keys.contains(&row.key_suffix) {
                    continue;
                }
                transaction.put(ColumnFamily::Flow, row.key(rows), row.value);
            }
            RunTable::Timer => {
                let timer = decode_timer_record(&row)?;
                let mut timer_id = match timer.timer_id {
                    Some(timer_id) => timer_id,
                    None => decode_timer_id(&row.key_suffix)?,
                };
                timer_id.run_attempt = cursor.attempt();
                arm_timer(transaction, cursor, &timer_id, timer.fire_at_micros);
            }
            RunTable::StepExecution => {
                let mut step = decode_step_execution(&row)?;
                if let Some(invocation) = step.invocation.as_mut() {
                    if invocation.phase == StepInvocationPhase::Running as i32 {
                        invocation.task_id =
                            restored_task_id(&scope.command.command_id, restored_running_count);
                        restored_running_count += 1;
                    }
                    restart_retry_total_duration(invocation, scope.now_micros);
                }
                cursor.rows.put_step_execution(transaction, &step);
            }
            _ => transaction.put(ColumnFamily::Flow, row.key(rows), row.value),
        }
    }
    Ok(())
}

/// Returns the keys of the Attribute rows whose last write requested synchronization.
pub(crate) fn synced_row_keys(rows: &[AttemptRow]) -> Result<BTreeSet<String>, ApplyError> {
    let mut keys = BTreeSet::new();
    for row in rows {
        if row.table != RunTable::Attribute {
            continue;
        }
        let record = AttributeRecord::decode(row.value.as_slice()).map_err(|error| {
            ApplyError::InvariantViolation {
                detail: format!("Attribute row is corrupt: {error}"),
            }
        })?;
        if !record.sync_config.is_some_and(|sync| sync.enabled) {
            continue;
        }
        let key = String::from_utf8(row.key_suffix.clone()).map_err(|error| {
            ApplyError::InvariantViolation {
                detail: format!("Attribute key is invalid UTF-8: {error}"),
            }
        })?;
        keys.insert(key);
    }
    Ok(keys)
}

/// Restarts the retry total duration at `now_micros`. A call pending attempt 1 starts it when
/// that attempt starts.
fn restart_retry_total_duration(invocation: &mut StepInvocationRecord, now_micros: i64) {
    let is_pending_first_attempt =
        invocation.phase == StepInvocationPhase::Pending as i32 && invocation.attempt == 1;
    invocation.retry_started_at_micros = if is_pending_first_attempt {
        0
    } else {
        now_micros
    };
}

/// A dispatch ID derived from the TimeTravel command ID and the restored attempt's index.
fn restored_task_id(command_id: &[u8], index: u32) -> Vec<u8> {
    let mut task_id = vec![0; 16];
    let prefix_length = command_id.len().min(12);
    task_id[..prefix_length].copy_from_slice(&command_id[..prefix_length]);
    task_id[12..].copy_from_slice(&index.to_be_bytes());
    task_id
}

/// Clears prior retention and reopens the run when its restored state is running.
fn prepare_run_lifecycle(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    was_running: bool,
) -> Result<(), ApplyError> {
    let is_running = cursor.record.status == FlowStatus::Running as i32;
    let partition = transaction.context().partition;
    let retention_timer = TimerId {
        run_attempt: 0,
        kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
    };
    if !was_running {
        let previous_deadline = cursor.record.retention_deadline_micros;
        if previous_deadline != 0 {
            disarm_timer(transaction, cursor, &retention_timer)?;
            transaction.delete(
                ColumnFamily::PartitionIndex,
                retention_index_key(
                    partition,
                    previous_deadline,
                    cursor.rows.flow_hash,
                    cursor.run_id(),
                ),
            );
        }
        cursor.record.retention_deadline_micros = 0;
    }
    if is_running && !was_running {
        transaction.put(
            ColumnFamily::PartitionIndex,
            active_work_index_key(partition, cursor.rows.flow_hash, cursor.run_id()),
            Vec::new(),
        );
        update_directory_status(transaction, cursor, scope)?;
    }
    Ok(())
}

fn restored_closing(
    status: &CheckpointRunStatus,
    target: &TravelTarget,
) -> Result<Option<(FlowClosing, bool)>, ApplyError> {
    if status.status == FlowStatus::Running as i32 {
        return Ok(None);
    }
    let close = status
        .close
        .as_ref()
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: "a restored closed run has no close record".to_owned(),
        })?;
    let restored_status =
        FlowStatus::try_from(status.status).map_err(|_| ApplyError::InvariantViolation {
            detail: format!("a restored run has unknown status {}", status.status),
        })?;
    let error_type =
        FlowErrorType::try_from(close.error_type).map_err(|_| ApplyError::InvariantViolation {
            detail: format!(
                "a restored close has unknown error type {}",
                close.error_type
            ),
        })?;
    // Older records lack the marker and retain the historical status-based classification.
    let is_closed_at_once = close.is_closed_at_once.unwrap_or(
        matches!(
            restored_status,
            FlowStatus::Canceled | FlowStatus::Terminated
        ) || error_type == FlowErrorType::ClientApiFailingFlow,
    );
    Ok(Some((
        FlowClosing {
            status: restored_status,
            error_type,
            error_message: close.error_message.clone(),
            history_message: close
                .history_message
                .clone()
                .or_else(|| target.close_history_message.clone())
                .unwrap_or_else(|| close.error_message.clone()),
        },
        is_closed_at_once,
    )))
}

fn close_restored_attempt(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    closing: FlowClosing,
    is_closed_at_once: bool,
) -> Result<(), ApplyError> {
    cursor.record.status = FlowStatus::Running as i32;
    cursor.record.closed_at_micros = 0;
    cursor.record.pending_close = None;
    if let Some(close) = cursor.record.close.as_mut() {
        close.is_reported_to_parent = false;
    }
    transaction.put(
        ColumnFamily::PartitionIndex,
        active_work_index_key(
            transaction.context().partition,
            cursor.rows.flow_hash,
            cursor.run_id(),
        ),
        Vec::new(),
    );
    update_directory_status(transaction, cursor, scope)?;
    if is_closed_at_once {
        close_flow_immediately(transaction, cursor, scope, closing)
    } else {
        close_flow(transaction, cursor, scope, closing)
    }
}

/// Completes restored SubFlow conditions from received child results (C15), even without reapply.
fn apply_received_child_results(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    has_pending_remarks: bool,
) -> Result<(), ApplyError> {
    // A close from a completed restored wait also precedes the remarks.
    let child_result_scope = CommandScope {
        has_pending_time_travel_remarks: has_pending_remarks,
        ..*scope
    };
    let effects_before = cursor.effects.len();
    crate::cross_flow::complete_restored_sub_flow_waits(transaction, cursor, &child_result_scope)?;
    let effects = cursor.effects.split_off(effects_before);
    keep_fork_effects(cursor, effects);
    Ok(())
}

/// Keeps effects of work folded into the fork; dispatch follows the final restored state instead.
fn keep_fork_effects(cursor: &mut RunCursor, effects: Vec<ApplyEffect>) {
    for effect in effects {
        match effect {
            ApplyEffect::InvocationPending {
                invocation,
                attempt: 1,
                ..
            } => cursor.fork_pending_invocations.push(invocation),
            ApplyEffect::InvocationPending { .. } | ApplyEffect::InvocationStarted { .. } => {}
            other => cursor.effects.push(other),
        }
    }
}

/// Re-dispatches the restored calls: running attempts with their stored snapshots and attempt
/// numbers, and pending calls.
fn dispatch_restored_invocations(
    cursor: &mut RunCursor,
    restored_rows: &[AttemptRow],
) -> Result<(), ApplyError> {
    let run_id = cursor.run_id();
    let run_attempt = cursor.attempt();
    for step in step_executions(restored_rows)? {
        let Some(invocation) = &step.invocation else {
            continue;
        };
        let key = StepInvocationKey {
            step_execution_id: step.step_execution_id.clone(),
            method: invocation.method,
        };
        let phase = StepInvocationPhase::try_from(invocation.phase)
            .unwrap_or(StepInvocationPhase::Unspecified);
        match phase {
            StepInvocationPhase::Pending => cursor.add_effect(ApplyEffect::InvocationPending {
                run_id,
                run_attempt,
                invocation: key,
                attempt: invocation.attempt,
            }),
            StepInvocationPhase::Running => cursor.add_effect(ApplyEffect::InvocationStarted {
                run_id,
                run_attempt,
                invocation: key,
                attempt: invocation.attempt,
                task_id: invocation.task_id.clone(),
                method_timeout_seconds: configured_method_timeout_seconds(
                    step.options.as_ref(),
                    invocation.method,
                ),
                retry_deadline_micros: invocation_retry_deadline_micros(
                    step.options.as_ref(),
                    invocation,
                ),
            }),
            StepInvocationPhase::Backoff | StepInvocationPhase::Unspecified => {}
        }
    }
    Ok(())
}

/// Reapplies input onto the restored state within the TimeTravel command, so its start
/// checkpoint includes it. Returns the reapplied input's history events.
fn reapply_external_inputs(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    inputs: &[FlowCommand],
    has_pending_remarks: bool,
) -> Result<Vec<flow_history_event::Payload>, ApplyError> {
    let mut events = Vec::new();
    if inputs.is_empty() {
        return Ok(events);
    }
    let behavior_version = BehaviorVersion::from_command(scope.command.behavior_version)?;
    let rows = cursor.rows;
    rows.put_run_record(transaction, &cursor.record);
    let mut counters = ReplayedCommandCounters {
        values: std::mem::replace(&mut cursor.values, ValueIdAllocator::new(cursor.seq)),
        next_channel_message_index: cursor.next_channel_message_index,
    };
    for input in inputs {
        cursor.reapplied_inputs.push(input.clone());
        let record = required_run_record(transaction, cursor)?;
        // Client writes other than hard-delete erases are rejected on a closed Flow, so a closed state skips the input.
        if record.status != FlowStatus::Running as i32 {
            continue;
        }
        let command = reapplied_command(input, scope);
        let Some(body) = &command.command else {
            return Err(ApplyError::InvariantViolation {
                detail: "a recorded external input has no command body".to_owned(),
            });
        };
        if let Command::AttributesSet(set) = body
            && set
                .request
                .as_ref()
                .is_none_or(|request| request.attributes.is_empty())
        {
            continue;
        }
        let mut input_scope = CommandScope::new(&command, behavior_version);
        input_scope.is_replay = true;
        input_scope.has_pending_time_travel_remarks = has_pending_remarks;
        let (writes, output) = {
            let reader = TransactionReader::new(transaction);
            let mut input_transaction = FlowTransaction::new(&reader, transaction.context());
            let input_cursor =
                RunCursor::for_reapplied_input(rows, record, cursor.seq, counters.clone());
            match crate::command_handlers::apply_run_command(
                &mut input_transaction,
                input_cursor,
                &input_scope,
                body,
            )? {
                HandlerResult::Applied(output) => (input_transaction.into_writes(), output),
                HandlerResult::Rejected(reply) if is_skippable_rejection(body, &reply) => continue,
                HandlerResult::Rejected(reply) => {
                    return Err(ApplyError::InvariantViolation {
                        detail: format!("time travel could not reapply recorded input: {reply:?}"),
                    });
                }
            }
        };
        absorb_writes(transaction, writes);
        let Some(replayed) = output.replayed else {
            return Err(ApplyError::InvariantViolation {
                detail: "a reapplied input recorded its own history".to_owned(),
            });
        };
        counters = replayed.counters;
        events.extend(replayed.events);
        keep_fork_effects(cursor, output.effects);
    }
    cursor.record = required_run_record(transaction, cursor)?;
    cursor.values = counters.values;
    cursor.next_channel_message_index = counters.next_channel_message_index;
    Ok(events)
}

/// The best-effort skips of plan section 8.4: input the restored state no longer accepts.
fn is_skippable_rejection(body: &Command, reply: &CommandReply) -> bool {
    let Some(command_reply::Reply::Error(error)) = &reply.reply else {
        return false;
    };
    match body {
        Command::TimerSkip(_) => error.grpc_code == grpc_code::INVALID_ARGUMENT,
        Command::ChannelMessageDelete(_) => {
            is_missing_channel_message(error) || is_terminal_cleanup_rejection(error)
        }
        // A transactional RPC whose deletions no longer hold is skipped as a whole.
        Command::RpcCommitted(rpc) => rpc.is_transactional && is_missing_channel_message(error),
        _ => false,
    }
}

/// A restored pending close rejects update-class input, as it would live.
fn is_terminal_cleanup_rejection(error: &CommandErrorReply) -> bool {
    error.grpc_code == grpc_code::FAILED_PRECONDITION && error.message == TERMINAL_CLEANUP_MESSAGE
}

fn is_missing_channel_message(error: &CommandErrorReply) -> bool {
    error.grpc_code == grpc_code::NOT_FOUND
        && error.sub_status == ErrorSubStatus::ChannelMessageNotFound as i32
}

fn required_run_record(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
) -> Result<RunRecord, ApplyError> {
    cursor
        .rows
        .run_record(transaction)?
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: "a time traveling run has no run record".to_owned(),
        })
}

/// Replaces request IDs, which the run-scoped deduplication table would otherwise answer.
fn reapplied_command(input: &FlowCommand, scope: &CommandScope<'_>) -> FlowCommand {
    let mut command = input.clone();
    command.proposed_at_micros = scope.now_micros;
    command.expected_run_attempt = 0;
    command.behavior_version = scope.command.behavior_version;
    match command.command.as_mut() {
        Some(Command::AttributesSet(set)) => {
            // Hard-delete Attributes stay current (C4); their objects may already be deleted.
            set.hard_delete_objects.clear();
            if let Some(request) = set.request.as_mut() {
                request.request_id.clear();
                request
                    .attributes
                    .retain(|write| !write.is_hard_delete_on_null);
            }
        }
        Some(Command::ChannelMessageDelete(delete)) => {
            if let Some(request) = delete.request.as_mut() {
                request.request_id.clear();
            }
        }
        Some(Command::RpcCommitted(rpc)) => {
            rpc.hard_delete_objects.clear();
            if let Some(response) = rpc.response.as_mut() {
                response
                    .upsert_attributes
                    .retain(|write| !write.is_hard_delete_on_null);
            }
        }
        _ => {}
    }
    command
}

fn step_executions(attempt_rows: &[AttemptRow]) -> Result<Vec<StepExecutionRecord>, ApplyError> {
    attempt_rows
        .iter()
        .filter(|row| row.table == RunTable::StepExecution)
        .map(decode_step_execution)
        .collect()
}

fn decode_step_execution(row: &AttemptRow) -> Result<StepExecutionRecord, ApplyError> {
    StepExecutionRecord::decode(row.value.as_slice()).map_err(|error| ApplyError::CorruptState {
        table: "step execution",
        key: hex(&row.key_suffix),
        detail: error.to_string(),
    })
}

fn decode_timer_record(row: &AttemptRow) -> Result<TimerRecord, ApplyError> {
    TimerRecord::decode(row.value.as_slice()).map_err(|error| ApplyError::CorruptState {
        table: "timer",
        key: hex(&row.key_suffix),
        detail: error.to_string(),
    })
}
