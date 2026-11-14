// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The DispatchQueue index of pending attempts no Worker took at once (plan section 3.2).
//!
//! Exit commands are replayed elsewhere, so a row never changes their outcome.

use dexzd_keyspace::dispatch_queue_keys::{
    DEFAULT_DISPATCH_PRIORITY, DispatchQueueEntry, DispatchQueueName, DispatchQueuePosition,
    TIMEOUT_HANDLER_DISPATCH_PRIORITY, dispatch_queue_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId, RunId, TenantId};
use dexzd_protocol::dex::ErrorSubStatus;
use dexzd_protocol::dexzd::v1::{
    DispatchQueueRecord, RunRecord, StepAttemptQueuedCommand, StepExecutionRecord,
    StepInvocationPhase, StepInvocationRecord,
};

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::state_access::FlowTransaction;

/// Returns the run's task queue, fixed when the Flow starts; empty for the default task queue.
pub fn run_task_queue(run: &RunRecord) -> &str {
    run.flow_config
        .as_ref()
        .and_then(|config| config.worker_target.as_ref())
        .map_or("", |target| target.task_queue.as_str())
}

/// Returns the DispatchQueue row of the Step's current invocation, whatever its phase.
///
/// Returns `None` when the Step has no invocation.
pub fn dispatch_queue_entry(
    partition: PartitionId,
    run: &RunRecord,
    flow_hash: FlowHash,
    run_id: RunId,
    step: &StepExecutionRecord,
) -> Option<DispatchQueueEntry> {
    let invocation = step.invocation.as_ref()?;
    let priority = if step.is_timeout_handler {
        TIMEOUT_HANDLER_DISPATCH_PRIORITY
    } else {
        DEFAULT_DISPATCH_PRIORITY
    };
    Some(DispatchQueueEntry {
        partition,
        queue: DispatchQueueName {
            tenant_id: TenantId(run.tenant_id),
            task_queue: run_task_queue(run).to_owned(),
            flow_type: run.flow_type.clone(),
            step_type: step.step_type.clone(),
        },
        position: DispatchQueuePosition {
            priority,
            pending_since_micros: effective_pending_since_micros(invocation),
            flow_hash,
            run_id,
            method: u8::try_from(invocation.method).unwrap_or(u8::MAX),
            attempt: invocation.attempt,
            step_execution_id: step.step_execution_id.clone(),
        },
    })
}

/// Returns when the invocation's current attempt became pending; records written before the
/// field existed use their first attempt's time.
pub fn effective_pending_since_micros(invocation: &StepInvocationRecord) -> i64 {
    if invocation.pending_since_micros != 0 {
        invocation.pending_since_micros
    } else {
        invocation.first_attempt_at_micros
    }
}

/// Removes the Step's DispatchQueue row, if any, before its invocation leaves the pending phase.
pub(crate) fn remove_dispatch_queue_row(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    step: &StepExecutionRecord,
) -> Result<(), ApplyError> {
    let partition = transaction.context().partition;
    let Some(entry) = dispatch_queue_entry(
        partition,
        &cursor.record,
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        step,
    ) else {
        return Ok(());
    };
    let key = dispatch_queue_key(&entry);
    // A point get first, so an attempt that never queued leaves no tombstone.
    if transaction
        .get(ColumnFamily::PartitionIndex, &key)?
        .is_some()
    {
        transaction.single_delete(ColumnFamily::PartitionIndex, key)?;
    }
    Ok(())
}

/// Writes the DispatchQueue row of a pending attempt that no Worker took at once.
///
/// Replies OK when the row was written or already exists, and FAILED_PRECONDITION without
/// writes when the attempt is no longer pending with this attempt number and time.
pub(crate) fn apply_attempt_queued(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &StepAttemptQueuedCommand,
) -> Result<HandlerResult, ApplyError> {
    let cursor = match RunCursor::open(transaction, scope)? {
        Ok(cursor) => cursor,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    let Some(key) = command.invocation.as_ref() else {
        return Ok(HandlerResult::Rejected(stale_queue_reply()));
    };
    let Some(step) = cursor
        .rows
        .step_execution(transaction, &key.step_execution_id)?
    else {
        return Ok(HandlerResult::Rejected(stale_queue_reply()));
    };
    let is_pending = step.invocation.as_ref().is_some_and(|invocation| {
        invocation.method == key.method
            && invocation.attempt == command.attempt
            && invocation.phase == StepInvocationPhase::Pending as i32
            && effective_pending_since_micros(invocation) == command.pending_since_micros
    });
    if !is_pending {
        return Ok(HandlerResult::Rejected(stale_queue_reply()));
    }
    let partition = transaction.context().partition;
    let entry = dispatch_queue_entry(
        partition,
        &cursor.record,
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        &step,
    )
    .expect("a pending Step has an invocation");
    let index_key = dispatch_queue_key(&entry);
    // Each key is put once between deletions, as SingleDelete requires.
    if transaction
        .get(ColumnFamily::PartitionIndex, &index_key)?
        .is_some()
    {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    transaction.put_message(
        ColumnFamily::PartitionIndex,
        index_key,
        &DispatchQueueRecord {
            attempt: command.attempt,
            run_attempt: cursor.record.current_attempt,
        },
    );
    Ok(HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: Vec::new(),
        run_id: None,
        replayed: None,
    }))
}

fn stale_queue_reply() -> dexzd_protocol::dexzd::v1::CommandReply {
    error_reply(
        grpc_code::FAILED_PRECONDITION,
        ErrorSubStatus::Uncategorized,
        "the attempt is no longer pending".to_owned(),
    )
}
