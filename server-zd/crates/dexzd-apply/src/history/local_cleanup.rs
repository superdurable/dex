// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Local history cleanup of runs that stop receiving commands (plan section 5.5).

use dexzd_keyspace::ColumnFamily;
use dexzd_keyspace::row_keys::history_local_cleanup_pending_index_key;
use dexzd_protocol::dexzd::v1::HistoryLocalCleanupCommand;

use crate::command_handlers::command_replies::ok_reply;
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::history_offload::local_deletion::{
    delete_uploaded_history, suspended_deletion_recheck_micros,
};
use crate::state_access::FlowTransaction;
use crate::values::{prune_passed_history, reconcile_run_storage};
use crate::{ApplyEffect, ApplyError};

/// Quiet command time before pending cleanup comes due, so busy runs clean up in their commands.
const LOCAL_CLEANUP_QUIET_MICROS: i64 = 60 * 1_000_000;

/// The local cleanup that a command's bounded passes left for later commands.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
pub(crate) struct LocalCleanupBacklog {
    /// A per-command bound stopped a pass, so more work can be done at once.
    is_unfinished: bool,
    /// The earliest command time from which waiting work, such as a grace period, can continue.
    waits_until_micros: Option<i64>,
}

impl LocalCleanupBacklog {
    /// Records that a per-command bound stopped a pass.
    pub(crate) fn mark_unfinished(&mut self) {
        self.is_unfinished = true;
    }

    /// Records work that can continue from command time `micros`.
    pub(crate) fn wait_until(&mut self, micros: i64) {
        self.waits_until_micros = Some(
            self.waits_until_micros
                .map_or(micros, |earlier| earlier.min(micros)),
        );
    }

    /// Adds what another pass of the same command left.
    pub(crate) fn merge(&mut self, other: Self) {
        self.is_unfinished |= other.is_unfinished;
        if let Some(micros) = other.waits_until_micros {
            self.wait_until(micros);
        }
    }

    /// Returns when the left work is due, or `None` when none is left: at once for unfinished
    /// work after HistoryLocalCleanup, otherwise at least one quiet period after the command.
    fn due_micros(self, now_micros: i64, caller: LocalCleanupCaller) -> Option<i64> {
        if self.is_unfinished && caller == LocalCleanupCaller::HistoryLocalCleanup {
            return Some(now_micros);
        }
        let work_due_micros = if self.is_unfinished {
            now_micros
        } else {
            self.waits_until_micros?
        };
        Some(work_due_micros.max(now_micros.saturating_add(LOCAL_CLEANUP_QUIET_MICROS)))
    }
}

/// The kind of command that schedules a run's local cleanup.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum LocalCleanupCaller {
    /// Any command of the run other than HistoryLocalCleanup.
    RunCommand,
    /// The HistoryLocalCleanup command, which continues unfinished work at once.
    HistoryLocalCleanup,
}

/// Does the run's due local cleanup at the command's time, then records when more comes due.
///
/// Returns OK without writes when nothing is due yet, and NOT_FOUND when the run is gone or
/// purging.
pub(crate) fn apply_history_local_cleanup(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    _command: &HistoryLocalCleanupCommand,
) -> Result<HandlerResult, ApplyError> {
    let mut cursor = match RunCursor::open_without_seq(transaction, scope)? {
        Ok(cursor) => cursor,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    let is_due = cursor
        .record
        .history_local_cleanup_due_micros
        .is_some_and(|due_micros| due_micros <= scope.now_micros);
    if !is_due {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    let backlog = clean_up_local_history(transaction, &mut cursor, scope.now_micros)?;
    schedule_local_cleanup(
        transaction,
        &mut cursor,
        backlog,
        scope.now_micros,
        LocalCleanupCaller::HistoryLocalCleanup,
    )?;
    reconcile_run_storage(transaction, &mut cursor)?;
    Ok(HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: cursor.effects,
        run_id: None,
        replayed: None,
    }))
}

/// Deletes uploaded history, then prunes what the window start passed, each within its bound.
///
/// Returns what the passes left.
pub(crate) fn clean_up_local_history(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) -> Result<LocalCleanupBacklog, ApplyError> {
    let mut backlog = delete_uploaded_history(transaction, cursor, now_micros)?;
    prune_passed_history(transaction, cursor, now_micros, &mut backlog)?;
    Ok(backlog)
}

/// Records when `backlog` or offload-waiting history comes due, keeping the pending row and
/// emitting [`ApplyEffect::HistoryLocalCleanupScheduled`] for a new one.
pub(crate) fn schedule_local_cleanup(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    mut backlog: LocalCleanupBacklog,
    now_micros: i64,
    caller: LocalCleanupCaller,
) -> Result<(), ApplyError> {
    if let Some(recheck_micros) =
        suspended_deletion_recheck_micros(transaction, cursor, now_micros)?
    {
        backlog.wait_until(recheck_micros);
    }
    let due_micros = if cursor.record.is_purging {
        None
    } else {
        backlog.due_micros(now_micros, caller)
    };
    let previous_due_micros = cursor.record.history_local_cleanup_due_micros;
    if due_micros == previous_due_micros {
        return Ok(());
    }
    cursor.record.history_local_cleanup_due_micros = due_micros;
    let run_id = cursor.run_id();
    let key = history_local_cleanup_pending_index_key(
        transaction.context().partition,
        cursor.rows.flow_hash,
        run_id,
    );
    match (previous_due_micros, due_micros) {
        (None, Some(_)) => {
            transaction.put(ColumnFamily::PartitionIndex, key, Vec::new());
            cursor.add_effect(ApplyEffect::HistoryLocalCleanupScheduled { run_id });
        }
        (Some(_), None) => transaction.delete(ColumnFamily::PartitionIndex, key),
        _ => {}
    }
    Ok(())
}
