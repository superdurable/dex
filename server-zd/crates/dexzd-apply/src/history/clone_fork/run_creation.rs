// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The FlowCloneFork command: ID reuse on the new Flow, then its run from the staged copy.

use std::collections::BTreeSet;

use dexzd_keyspace::row_keys::{active_work_index_key, tenant_run_index_key, value_row_key};
use dexzd_keyspace::{ColumnFamily, IndexKind, RunId, RunTable, ValueId};
use dexzd_protocol::dex::{
    AttributeWrite, CloneForkFlowRequest, ErrorSubStatus, FlowConfig, FlowStatus, IdReusePolicy,
};
use dexzd_protocol::dexzd::v1::{
    AttemptRecord, CheckpointRow, CheckpointRunStatus, CommandReply, EndedStepRowSweep,
    FlowCloneForkCommand, FlowCloneForkStagedChunk, FlowCloneForkStagedTarget, FlowDirectoryRun,
    FlowTimeoutTimerId, RunRecord, StoredValue, StoredValueReference, TimeTravelStagedChildFlow,
    TimeTravelStagedValue, TimerId, ValueBirthRecord, ValueRecord, command_reply, stored_value,
    timer_id,
};
use prost::Message;

use super::computation::bound_request;
use super::copied_values::referenced_table_values;
use super::staging::{STAGING_ID_BYTES, delete_staging, no_run_output, read_complete_staging};
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::command_handlers::flow_data::apply_attribute_writes;
use crate::command_handlers::flow_start::{
    IdReuseDecision, IdReuseRequest, decide_id_reuse, run_settings_with_offload_choice,
    terminate_previous_run,
};
use crate::command_handlers::run_cursor::{HistoryKind, RunCursor};
use crate::command_handlers::{CommandScope, HandlerResult};
use crate::history::attempt_scope::{AttemptRow, decode_timer_id, holder_rows, set_run_status};
use crate::history::time_travel::{
    apply_clone_fork_sub_flow_actions, dispatch_restored_invocations,
    keep_time_travel_attempt_effects, plan_clone_fork_sub_flow_actions, synced_row_keys,
    write_restored_rows,
};
use crate::history_offload::segment_seal::open_first_history_segment;
use crate::projections::attribute_sync::mark_attribute_sync_entries;
use crate::projections::history_events::{
    FlowCloneForkStart, record_flow_clone_forked, start_flow_reply,
};
use crate::state_access::{FlowTransaction, RunRows, read_flow_directory, write_flow_directory};
use crate::steps::arm_timer;
use crate::values::{ValueHolder, hold_stored_value, rewrite_holders_for_time_travel};
use crate::{ApplyEffect, ApplyError};

/// Close reason of a run that CloneForkFlow terminated for ID reuse.
const CLONE_FORK_TERMINATION_REASON: &str =
    "terminated by CloneForkFlow with ALLOW_TERMINATE_IF_RUNNING";

/// The staged copy that one FlowCloneFork command starts its run from.
struct StagedCopy {
    target: FlowCloneForkStagedTarget,
    rows: Vec<AttemptRow>,
    values: Vec<TimeTravelStagedValue>,
    child_flows: Vec<TimeTravelStagedChildFlow>,
}

/// Applies ID reuse to the command's Flow and, when allowed, creates its run from the staging.
///
/// A conflict or a retry answers as StartFlow does and deletes the staging. Returns ABORTED,
/// which the owner settles by staging again, when the staging is missing or incomplete.
pub(crate) fn apply_flow_clone_fork(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &FlowCloneForkCommand,
) -> Result<HandlerResult, ApplyError> {
    let request = command.request.clone().unwrap_or_default();
    let Ok(run_id) = RunId::from_slice(&command.new_run_id) else {
        return Err(ApplyError::InvariantViolation {
            detail: "FlowCloneFork has no 16-byte run ID".to_owned(),
        });
    };
    if command.staging_id.len() != STAGING_ID_BYTES {
        return Ok(HandlerResult::Rejected(invalid_staging_reply(&format!(
            "the staging ID has {} bytes instead of {STAGING_ID_BYTES}",
            command.staging_id.len()
        ))));
    }
    let id_reuse = IdReuseRequest {
        request_id: &request.request_id,
        policy: IdReusePolicy::try_from(request.id_reuse_policy)
            .unwrap_or(IdReusePolicy::Unspecified),
        should_ignore_already_started_error: request
            .flow_already_started_options
            .as_ref()
            .is_some_and(|already| already.ignore_already_started_error),
    };
    let running_run_to_terminate = match decide_id_reuse(transaction, scope, &id_reuse)? {
        IdReuseDecision::Reply(reply) => {
            return answer_without_run(transaction, &command.staging_id, reply);
        }
        IdReuseDecision::Start {
            running_run_to_terminate,
        } => running_run_to_terminate,
    };
    let Some(chunks) = read_complete_staging(transaction, &command.staging_id)? else {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::ABORTED,
            ErrorSubStatus::Uncategorized,
            "the clone fork staging is missing or incomplete, for example because it expired or \
             a retry replaced it; retry the CloneForkFlow with the same request ID",
        )));
    };
    let copy = match staged_copy(chunks, &request)? {
        Ok(copy) => copy,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    let sub_flow_actions = match plan_clone_fork_sub_flow_actions(
        &copy.rows,
        request.sub_flow_reset_policy,
        &copy.child_flows,
    )? {
        Ok(actions) => actions,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    delete_staging(transaction, &command.staging_id)?;
    let mut terminated_run_effects = match running_run_to_terminate {
        Some(previous_run_id) => terminate_previous_run(
            transaction,
            scope,
            &previous_run_id,
            CLONE_FORK_TERMINATION_REASON,
        )?,
        None => Vec::new(),
    };
    let flow_config = command.effective_flow_config.clone().unwrap_or_default();
    let rows = RunRows::new(transaction.context().flow_hash, run_id);
    let record = new_run_record(scope, command, &request, &copy.target, run_id, &flow_config);
    let mut cursor = RunCursor::for_new_run(rows, record);
    open_first_history_segment(transaction, &mut cursor)?;
    rows.put_attempt_record(
        transaction,
        &AttemptRecord {
            attempt: 1,
            parent_attempt: 0,
            parent_seq: 0,
            start_seq: cursor.seq,
            created_at_micros: scope.now_micros,
            reason: request.reason.clone(),
            checkpoint_seqs: Vec::new(),
        },
    );
    let run_status = copy.target.run_status.clone().unwrap_or_default();
    write_copied_state(transaction, &mut cursor, scope, &copy, &run_status)?;
    apply_clone_fork_sub_flow_actions(transaction, &mut cursor, scope, sub_flow_actions)?;
    let writes: Vec<AttributeWrite> = command
        .copied_hard_delete_attributes
        .iter()
        .chain(&request.attributes)
        .cloned()
        .collect();
    let mut attribute_references = match apply_attribute_writes(
        transaction,
        &mut cursor,
        &writes,
        &command.hard_delete_objects,
        None,
        scope,
        true,
    )? {
        Ok(references) => references,
        Err(rejection) => return Ok(HandlerResult::from(rejection)),
    };
    let request_attribute_references =
        attribute_references.split_off(command.copied_hard_delete_attributes.len());
    pin_written_attributes(transaction, &cursor, &writes)?;
    if command.flow_timeout_duration_micros > 0 {
        let deadline = cursor.record.timeout_deadline_micros;
        arm_timer(
            transaction,
            &mut cursor,
            &TimerId {
                run_attempt: 1,
                kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
            },
            deadline,
        );
    }
    register_run(transaction, &cursor, scope, &request)?;
    complete_copied_waits(transaction, &mut cursor, scope)?;
    let final_rows = crate::history::attempt_scope::read_attempt_rows(transaction, rows)?;
    mark_synced_attributes(transaction, &mut cursor, scope, &final_rows)?;
    dispatch_restored_invocations(&mut cursor, &final_rows)?;
    let source = copy.target.source.clone().unwrap_or_default();
    record_flow_clone_forked(
        &mut cursor,
        FlowCloneForkStart {
            flow_id: scope.flow_id(),
            flow_type: &copy.target.flow_type,
            flow_config: &flow_config,
            flow_timeout_duration_micros: command.flow_timeout_duration_micros,
            timeout_handler_options: command.timeout_handler_options.clone(),
            attributes: &request.attributes,
            source,
        },
        request_attribute_references,
    );
    let reply = CommandReply {
        reply: Some(command_reply::Reply::StartFlow(start_flow_reply(
            &cursor, false,
        ))),
    };
    let mut output = cursor.finish(transaction, scope, reply, HistoryKind::SEMANTIC)?;
    terminated_run_effects.extend(output.effects);
    output.effects = terminated_run_effects;
    Ok(HandlerResult::Applied(output))
}

/// Answers a retry or an ID reuse conflict with `reply`. The command then commits only the
/// deletion of its staging, when one exists.
fn answer_without_run(
    transaction: &mut FlowTransaction<'_>,
    staging_id: &[u8],
    reply: CommandReply,
) -> Result<HandlerResult, ApplyError> {
    if delete_staging(transaction, staging_id)? {
        return Ok(HandlerResult::Applied(no_run_output(reply)));
    }
    Ok(HandlerResult::Rejected(reply))
}

/// Joins the staged chunks and checks them against the command.
///
/// Returns INVALID_ARGUMENT when the staging was computed for another request, or when the rows
/// reference a value the staging lacks.
fn staged_copy(
    chunks: Vec<FlowCloneForkStagedChunk>,
    request: &CloneForkFlowRequest,
) -> Result<Result<StagedCopy, CommandReply>, ApplyError> {
    let mut target = None;
    let mut rows = Vec::new();
    let mut checkpoint_rows: Vec<CheckpointRow> = Vec::new();
    let mut values = Vec::new();
    let mut child_flows = Vec::new();
    for chunk in chunks {
        target = target.or(chunk.target);
        for row in chunk.rows {
            rows.push(AttemptRow::from_checkpoint_row(&row)?);
            checkpoint_rows.push(row);
        }
        values.extend(chunk.values);
        child_flows.extend(chunk.child_flows);
    }
    let target = target.ok_or_else(|| ApplyError::InvariantViolation {
        detail: "a complete clone fork staging has no target".to_owned(),
    })?;
    if target.request.as_ref() != Some(&bound_request(request)) {
        return Ok(Err(invalid_staging_reply(
            "it was computed for a different request",
        )));
    }
    let staged_ids: BTreeSet<ValueId> = values
        .iter()
        .filter_map(|value| value.value_id)
        .map(|id| ValueId {
            run_seq: id.run_seq,
            index_in_command: id.index_in_command,
        })
        .collect();
    let run_status = target.run_status.clone().unwrap_or_default();
    let mut referenced = referenced_table_values(&checkpoint_rows)?;
    referenced.extend(referenced_table_values(&holder_rows(&[], &run_status))?);
    if let Some(missing) = referenced
        .keys()
        .find(|value_id| !staged_ids.contains(value_id))
    {
        return Ok(Err(invalid_staging_reply(&format!(
            "its rows reference value ({}, {}), which it does not stage",
            missing.run_seq, missing.index_in_command
        ))));
    }
    if let Some(value) = staged_ids
        .iter()
        .find(|value_id| value_id.run_seq >= target.first_run_seq)
    {
        return Ok(Err(invalid_staging_reply(&format!(
            "value ({}, {}) is not older than the first run seq {}",
            value.run_seq, value.index_in_command, target.first_run_seq
        ))));
    }
    Ok(Ok(StagedCopy {
        target,
        rows,
        values,
        child_flows,
    }))
}

/// Returns the record of the new run, which starts at the staged first run seq.
fn new_run_record(
    scope: &CommandScope<'_>,
    command: &FlowCloneForkCommand,
    request: &CloneForkFlowRequest,
    target: &FlowCloneForkStagedTarget,
    run_id: RunId,
    flow_config: &FlowConfig,
) -> RunRecord {
    let duration_micros = command.flow_timeout_duration_micros.max(0);
    RunRecord {
        tenant_id: scope.tenant_id.0,
        flow_id: scope.flow_id().to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        flow_type: target.flow_type.clone(),
        start_request_id: request.request_id.clone(),
        started_at_micros: scope.now_micros,
        settings: Some(run_settings_with_offload_choice(
            command.settings.unwrap_or_default(),
            flow_config,
        )),
        status: FlowStatus::Running as i32,
        current_attempt: 1,
        // Copied rows and values name seqs before it, as the source run assigned them.
        next_run_seq: target.first_run_seq,
        next_event_id: 1,
        flow_config: Some(flow_config.clone()),
        timeout_deadline_micros: if duration_micros > 0 {
            scope.now_micros.saturating_add(duration_micros)
        } else {
            0
        },
        timeout_duration_micros: duration_micros,
        timeout_policy: command.resolved_timeout_policy,
        timeout_handler_options: command.timeout_handler_options.clone(),
        window_start_seq: target.first_run_seq,
        window_event_count: Some(0),
        // Copied rows may come from a checkpoint that an older binary wrote.
        ended_step_row_sweep: Some(EndedStepRowSweep::default()),
        clone_fork_source: target.source.clone(),
        ..RunRecord::default()
    }
}

/// Writes the staged values and rows into the new run, under its attempt 1.
///
/// Every copied value gets the holders of its rows and a genesis pin: the run's start checkpoint
/// holds it, and a time travel to the beginning restores it.
fn write_copied_state(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    copy: &StagedCopy,
    run_status: &CheckpointRunStatus,
) -> Result<(), ApplyError> {
    let rows = cursor.rows;
    let mut pinned = Vec::with_capacity(copy.values.len());
    for staged in &copy.values {
        let (Some(value_id), Some(value)) = (staged.value_id, staged.value.clone()) else {
            return Err(ApplyError::InvariantViolation {
                detail: "a staged clone fork value has no ID or no value".to_owned(),
            });
        };
        let value_id = ValueId {
            run_seq: value_id.run_seq,
            index_in_command: value_id.index_in_command,
        };
        let size_bytes = u64::try_from(value.encoded_len()).unwrap_or(u64::MAX);
        let is_string_value = matches!(
            value.kind,
            Some(dexzd_protocol::dex::value::Kind::StringValue(_))
        );
        transaction.put_message(
            ColumnFamily::Value,
            value_row_key(rows.flow_hash, rows.run_id, RunTable::Value, value_id),
            &ValueRecord { value: Some(value) },
        );
        transaction.put_message(
            ColumnFamily::Flow,
            value_row_key(rows.flow_hash, rows.run_id, RunTable::ValueBirth, value_id),
            &ValueBirthRecord {
                size_bytes,
                written_seq: value_id.run_seq,
                garbage_seq: 0,
                is_offloaded: false,
            },
        );
        pinned.push(StoredValue {
            kind: Some(stored_value::Kind::TableValue(StoredValueReference {
                value_id: staged.value_id,
                size_bytes,
                is_string_value,
            })),
        });
    }
    for stored in &pinned {
        hold_stored_value(transaction, rows, stored, &ValueHolder::Genesis)?;
    }
    let mut copied_rows = Vec::with_capacity(copy.rows.len());
    for row in &copy.rows {
        // The new run's Flow timeout is armed from the command.
        let is_flow_timeout = row.table == RunTable::Timer
            && matches!(
                decode_timer_id(&row.key_suffix)?.kind,
                Some(timer_id::Kind::FlowTimeout(_))
            );
        if !is_flow_timeout {
            copied_rows.push(row.clone());
        }
    }
    set_run_status(&mut cursor.record, run_status);
    write_restored_rows(
        transaction,
        cursor,
        scope,
        true,
        copied_rows,
        &BTreeSet::new(),
    )?;
    let written_rows = crate::history::attempt_scope::read_attempt_rows(transaction, rows)?;
    rewrite_holders_for_time_travel(
        transaction,
        cursor,
        &[],
        &holder_rows(&written_rows, run_status),
        cursor.seq,
    )
}

/// Pins the values of the command's Attribute writes, as FlowStart pins its initial Attributes.
fn pin_written_attributes(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    writes: &[AttributeWrite],
) -> Result<(), ApplyError> {
    for write in writes {
        if let Some(record) = cursor.rows.attribute(transaction, &write.key)?
            && let Some(stored) = &record.value
        {
            hold_stored_value(transaction, cursor.rows, stored, &ValueHolder::Genesis)?;
        }
    }
    Ok(())
}

/// Adds the run to its Flow's directory and to the partition's run indexes.
fn register_run(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    scope: &CommandScope<'_>,
    request: &CloneForkFlowRequest,
) -> Result<(), ApplyError> {
    let run_id = cursor.run_id();
    let mut directory = read_flow_directory(transaction, scope.tenant_id, scope.flow_id())?;
    directory.runs.push(FlowDirectoryRun {
        run_id: run_id.as_bytes().to_vec(),
        status: FlowStatus::Running as i32,
        started_at_micros: scope.now_micros,
        closed_at_micros: 0,
        start_request_id: request.request_id.clone(),
    });
    write_flow_directory(transaction, scope.tenant_id, scope.flow_id(), &directory);
    let context = transaction.context();
    transaction.put(
        ColumnFamily::PartitionIndex,
        active_work_index_key(context.partition, context.flow_hash, run_id),
        Vec::new(),
    );
    transaction.put(
        ColumnFamily::PartitionIndex,
        tenant_run_index_key(
            context.partition,
            IndexKind::TenantRuns,
            scope.tenant_id,
            context.flow_hash,
            run_id,
        ),
        Vec::new(),
    );
    Ok(())
}

/// Completes the copied waits whose SubFlow results the command delivered. Dispatch follows the
/// final copied state instead of these completions' own dispatch effects.
fn complete_copied_waits(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
) -> Result<(), ApplyError> {
    let effects_before = cursor.effects.len();
    crate::cross_flow::complete_restored_sub_flow_waits(transaction, cursor, scope)?;
    let effects = cursor.effects.split_off(effects_before);
    keep_time_travel_attempt_effects(cursor, effects);
    Ok(())
}

/// Marks every synced Attribute of the new run, so Attribute Stores receive the new Flow's rows,
/// in one effect with the marks of the command's Attribute writes.
fn mark_synced_attributes(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    final_rows: &[AttemptRow],
) -> Result<(), ApplyError> {
    let mut pending_store_names =
        mark_attribute_sync_entries(transaction, cursor, &synced_row_keys(final_rows)?, scope)?;
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
    Ok(())
}

fn invalid_staging_reply(detail: &str) -> CommandReply {
    error_reply(
        grpc_code::INVALID_ARGUMENT,
        ErrorSubStatus::Uncategorized,
        format!("the clone fork staging does not fit its FlowCloneFork command: {detail}"),
    )
}
