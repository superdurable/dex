// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Closing the current attempt of a run (plan section 9.1): every path (Step decisions, Stop,
//! timeouts, exhausted retries, ID reuse termination) goes through [`close_flow`] or
//! [`close_flow_immediately`].

use dexzd_keyspace::ColumnFamily;
use dexzd_keyspace::row_keys::{active_work_index_key, retention_index_key};
use dexzd_protocol::dex::{FlowErrorType, FlowStatus, StepCompletionOutput};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowTimeoutTimerId, PendingFlowClose, RetentionTimerId, StepExecutionPhase,
    TimerId, flow_command::Command, inbox_message_command, timer_id,
};

use crate::command_handlers::command_replies::ok_reply;
use crate::command_handlers::run_cursor::{HistoryKind, RunCursor};
use crate::command_handlers::{CloseBarrierSource, CommandScope, HandlerResult};
use crate::projections::attribute_sync::has_attribute_sync_entries;
use crate::projections::history_events;
use crate::state_access::{FlowTransaction, read_flow_directory, write_flow_directory};
use crate::steps::{arm_timer, disarm_timer, finish_step, is_active};
use crate::values::wire_value;
use crate::{ApplyEffect, ApplyError, WaitableChange};

/// Whether a command's natural close must wait for transactional RPCs that the owner admitted.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TransactionalRpcHold {
    /// May record a natural or pending close, so the owner checks it while such RPCs run.
    MayClose,
    /// StopFlow, StartFlow, and child starts terminate at once, whatever is in flight.
    ClosesAtOnce,
    /// Time travel bumps the attempt, which fails admitted RPCs, as a Go reset fails updates.
    ReplacesAttempt,
}

/// Classifies `command` for the hold. A new command kind must be classified here.
pub fn transactional_rpc_hold(command: &FlowCommand) -> TransactionalRpcHold {
    let Some(body) = &command.command else {
        return TransactionalRpcHold::MayClose;
    };
    match body {
        Command::FlowStop(_) | Command::FlowStart(_) => TransactionalRpcHold::ClosesAtOnce,
        Command::InboxMessage(inbox) => match inbox.payload {
            // A child start only closes a run through ID reuse termination.
            Some(inbox_message_command::Payload::ChildStart(_)) => {
                TransactionalRpcHold::ClosesAtOnce
            }
            Some(inbox_message_command::Payload::ChildCompleted(_)) | None => {
                TransactionalRpcHold::MayClose
            }
        },
        Command::TimeTravel(_) => TransactionalRpcHold::ReplacesAttempt,
        Command::FlowConfigUpdate(_)
        | Command::AttributesSet(_)
        | Command::ChannelPublish(_)
        | Command::ChannelMessageDelete(_)
        | Command::StepAttemptStarted(_)
        | Command::StepAttemptHeartbeat(_)
        | Command::StepAttemptFailed(_)
        | Command::StepMethodCompleted(_)
        | Command::RpcCommitted(_)
        | Command::TimersFired(_)
        | Command::TimerSkip(_)
        | Command::OutboxAck(_)
        | Command::AttributeSyncAck(_)
        | Command::PendingCloseCheck(_)
        | Command::HardDeleteObjectsDeleted(_)
        | Command::RunPurge(_) => TransactionalRpcHold::MayClose,
    }
}

/// Fails loudly in debug builds when a command classified as closing at once closes naturally.
fn debug_assert_close_is_classified(scope: &CommandScope<'_>) {
    debug_assert_ne!(
        transactional_rpc_hold(scope.command),
        TransactionalRpcHold::ClosesAtOnce,
        "a natural close in {:?} escapes the transactional RPC hold",
        scope.command.command
    );
}

/// How the attempt closes.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct FlowClosing {
    pub(crate) status: FlowStatus,
    pub(crate) error_type: FlowErrorType,
    /// Message of the Flow result.
    pub(crate) error_message: String,
    /// Message of the FlowClosed history event; differs only for a terminated Flow.
    pub(crate) history_message: String,
}

impl FlowClosing {
    pub(crate) fn completed() -> Self {
        Self {
            status: FlowStatus::Completed,
            error_type: FlowErrorType::Unspecified,
            error_message: String::new(),
            history_message: String::new(),
        }
    }

    pub(crate) fn failed(error_type: FlowErrorType, message: impl Into<String>) -> Self {
        let message = message.into();
        Self {
            status: FlowStatus::Failed,
            error_type,
            error_message: message.clone(),
            history_message: message,
        }
    }

    /// As in the Go Server, a canceled Flow reports no message, whatever the stop reason.
    pub(crate) fn canceled() -> Self {
        Self {
            status: FlowStatus::Canceled,
            error_type: FlowErrorType::Unspecified,
            error_message: String::new(),
            history_message: String::new(),
        }
    }

    /// As in the Go Server, only the FlowClosed history event keeps the termination reason.
    pub(crate) fn terminated(reason: impl Into<String>) -> Self {
        Self {
            status: FlowStatus::Terminated,
            error_type: FlowErrorType::Unspecified,
            error_message: String::new(),
            history_message: reason.into(),
        }
    }

    fn to_pending_close(&self) -> PendingFlowClose {
        PendingFlowClose {
            status: self.status as i32,
            error_type: self.error_type as i32,
            error_message: self.error_message.clone(),
            history_message: self.history_message.clone(),
        }
    }

    /// Returns the close that `pending` stores, or an error when apply never wrote it.
    fn from_pending_close(pending: PendingFlowClose) -> Result<Self, ApplyError> {
        let corrupt = || ApplyError::InvariantViolation {
            detail: format!("pending close {pending:?} has an unknown status or error type"),
        };
        Ok(Self {
            status: FlowStatus::try_from(pending.status).map_err(|_| corrupt())?,
            error_type: FlowErrorType::try_from(pending.error_type).map_err(|_| corrupt())?,
            error_message: pending.error_message.clone(),
            history_message: pending.history_message.clone(),
        })
    }
}

/// Closes the current attempt: cancels every active Step (results discarded, consumed
/// messages discarded, child Flows keep running), records the close, arms retention, reports
/// to the parent, and wakes waiters. Closing an already closed or closing attempt does nothing.
///
/// While the run has Attribute Sync outbox entries or an admitted transactional RPC, it records
/// a pending close instead.
pub(crate) fn close_flow(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    closing: FlowClosing,
) -> Result<(), ApplyError> {
    if cursor.record.status != FlowStatus::Running as i32 || cursor.record.pending_close.is_some() {
        return Ok(());
    }
    debug_assert_close_is_classified(scope);
    cancel_attempt_work(transaction, cursor, scope, &closing)?;
    if should_defer_close(transaction, cursor, scope)? {
        cursor.record.is_graceful_completion_requested = false;
        cursor.record.pending_close = Some(closing.to_pending_close());
        cursor.wake(WaitableChange::FlowCleanupStarted);
        hold_for_transactional_rpcs(cursor, scope);
        return Ok(());
    }
    record_close(transaction, cursor, scope, closing, false)
}

/// Closes at once for StopFlow and terminations, discarding a pending close; outbox entries keep syncing.
pub(crate) fn close_flow_immediately(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    closing: FlowClosing,
) -> Result<(), ApplyError> {
    if cursor.record.status != FlowStatus::Running as i32 {
        return Ok(());
    }
    cursor.record.pending_close = None;
    cancel_attempt_work(transaction, cursor, scope, &closing)?;
    record_close(transaction, cursor, scope, closing, true)
}

/// Closes the attempt as its pending close describes.
///
/// Returns false when the run has no pending close.
pub(crate) fn finalize_pending_close(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
) -> Result<bool, ApplyError> {
    let Some(pending) = cursor.record.pending_close.take() else {
        return Ok(false);
    };
    let closing = FlowClosing::from_pending_close(pending)?;
    record_close(transaction, cursor, scope, closing, false)?;
    Ok(true)
}

/// Records the pending close when no outbox entry and no admitted transactional RPC remains.
///
/// Returns whether the close was recorded. A rebuild records it exactly when the recorded
/// command did, and time travel's reapplied input never does.
pub(crate) fn finalize_pending_close_if_ready(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
) -> Result<bool, ApplyError> {
    if cursor.record.pending_close.is_none() {
        return Ok(false);
    }
    let is_ready = match scope.close_barrier_source {
        CloseBarrierSource::RecordedHistory { has_flow_closed } => has_flow_closed,
        CloseBarrierSource::Outbox if scope.is_replay => false,
        CloseBarrierSource::Outbox => {
            !has_attribute_sync_entries(transaction, cursor.rows)?
                && !scope.command.is_transactional_rpc_in_flight
        }
    };
    if !is_ready {
        hold_for_transactional_rpcs(cursor, scope);
        return Ok(false);
    }
    debug_assert_close_is_classified(scope);
    finalize_pending_close(transaction, cursor, scope)
}

/// Records a pending close for the owner once every transactional RPC of the run ended.
pub(crate) fn apply_pending_close_check(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
) -> Result<HandlerResult, ApplyError> {
    if !finalize_pending_close_if_ready(transaction, &mut cursor, scope)? {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        ok_reply(),
        HistoryKind::SEMANTIC,
    )?))
}

/// Whether a natural close waits for the Attribute Sync outbox.
pub(crate) fn is_terminal_cleanup_in_progress(cursor: &RunCursor) -> bool {
    cursor.record.pending_close.is_some()
}

/// A rebuild repeats the recorded decision because the run-scoped outbox has moved on.
fn should_defer_close(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    scope: &CommandScope<'_>,
) -> Result<bool, ApplyError> {
    match scope.close_barrier_source {
        CloseBarrierSource::Outbox => Ok(scope.command.is_transactional_rpc_in_flight
            || has_attribute_sync_entries(transaction, cursor.rows)?),
        CloseBarrierSource::RecordedHistory { has_flow_closed } => Ok(!has_flow_closed),
    }
}

/// Asks the owner to check the pending close again once its transactional RPCs end.
fn hold_for_transactional_rpcs(cursor: &mut RunCursor, scope: &CommandScope<'_>) {
    if scope.is_replay
        || !scope.command.is_transactional_rpc_in_flight
        || cursor.record.pending_close.is_none()
    {
        return;
    }
    let run_id = cursor.run_id();
    cursor.add_effect(ApplyEffect::PendingCloseHeldByRpcs { run_id });
}

/// Cancels every active Step and disarms the Flow timeout.
fn cancel_attempt_work(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    closing: &FlowClosing,
) -> Result<(), ApplyError> {
    let mut steps = cursor.rows.step_executions(transaction)?;
    steps.sort_by(|(_, left), (_, right)| {
        (left.created_seq, &left.step_execution_id)
            .cmp(&(right.created_seq, &right.step_execution_id))
    });
    let should_record_pending = matches!(
        closing.status,
        FlowStatus::Canceled | FlowStatus::Terminated | FlowStatus::ServerSideTimeoutInternalOnly
    );
    for (_, mut step) in steps {
        if is_active(&step) {
            if should_record_pending && step.invocation.is_some() {
                history_events::record_method_pending(cursor, &step, scope.now_micros);
            }
            finish_step(transaction, cursor, &mut step, StepExecutionPhase::Canceled)?;
        }
    }
    let run_attempt = cursor.attempt();
    disarm_timer(
        transaction,
        cursor,
        &TimerId {
            run_attempt,
            kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
        },
    )?;
    Ok(())
}

/// Records the close, arms retention, reports to the parent, and wakes waiters.
fn record_close(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    closing: FlowClosing,
    is_closed_at_once: bool,
) -> Result<(), ApplyError> {
    cursor.record.status = closing.status as i32;
    cursor.record.closed_at_micros = scope.now_micros;
    cursor.record.is_graceful_completion_requested = false;
    let close = cursor.record.close.get_or_insert_with(Default::default);
    close.error_type = closing.error_type as i32;
    close.history_message =
        (closing.history_message != closing.error_message).then(|| closing.history_message.clone());
    close.error_message = closing.error_message;
    close.is_closed_at_once = Some(is_closed_at_once);
    let results: Vec<StepCompletionOutput> = close
        .results
        .iter()
        .map(|output| StepCompletionOutput {
            completed_step_type: output.completed_step_type.clone(),
            completed_step_execution_id: output.completed_step_execution_id.clone(),
            completed_step_output: output
                .completed_step_output
                .as_ref()
                .map(|stored| wire_value(cursor.rows, stored)),
        })
        .collect();
    update_directory_status(transaction, cursor, scope)?;
    let settings = cursor.record.settings.unwrap_or_default();
    let retention_deadline = scope
        .now_micros
        .saturating_add(settings.retention_after_close_micros);
    cursor.record.retention_deadline_micros = retention_deadline;
    arm_timer(
        transaction,
        cursor,
        &TimerId {
            run_attempt: 0,
            kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
        },
        retention_deadline,
    );
    let partition = transaction.context().partition;
    transaction.put(
        ColumnFamily::PartitionIndex,
        retention_index_key(
            partition,
            retention_deadline,
            cursor.rows.flow_hash,
            cursor.run_id(),
        ),
        Vec::new(),
    );
    transaction.delete(
        ColumnFamily::PartitionIndex,
        active_work_index_key(partition, cursor.rows.flow_hash, cursor.run_id()),
    );
    history_events::record_flow_closed(cursor, results, closing.history_message);
    crate::cross_flow::report_child_closed(transaction, cursor, scope)?;
    cursor.wake(WaitableChange::FlowClosed);
    Ok(())
}

/// Mirrors the run's status into the Flow directory, used by ID reuse decisions.
pub(crate) fn update_directory_status(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    scope: &CommandScope<'_>,
) -> Result<(), ApplyError> {
    let mut directory = read_flow_directory(transaction, scope.tenant_id, &cursor.record.flow_id)?;
    let run_id = cursor.run_id().as_bytes().to_vec();
    if let Some(entry) = directory
        .runs
        .iter_mut()
        .find(|entry| entry.run_id == run_id)
    {
        entry.status = cursor.record.status;
        entry.closed_at_micros = cursor.record.closed_at_micros;
    }
    write_flow_directory(
        transaction,
        scope.tenant_id,
        &cursor.record.flow_id,
        &directory,
    );
    Ok(())
}
