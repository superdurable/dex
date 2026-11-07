// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Rebuilds S' (plan section 8.4 step 2): a checkpoint, then the attempt's commands replayed.

use std::collections::BTreeMap;

use dexzd_keyspace::{ValueId, ValueReference};
use dexzd_protocol::dex::{ErrorSubStatus, flow_history_event};
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, CheckpointRunStatus, CommandReply, FlowCommand, HistoryRecord, RunRecord,
    StoredValueId, TimeTravelStagedValue, TimersFiredCommand, flow_command::Command,
};

use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::history::attempt_scope::{
    AttemptRow, holder_rows, read_attempt_rows, run_status_of, set_run_status,
};
use crate::history::lineage::{HISTORY_PAGE_RECORDS, HistorySource, required_attempt_record};
use crate::history::replay_state::{ReplayState, TransactionReader};
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::{OffloadedValues, held_table_values, is_stored_value};
use crate::{ApplyContext, ApplyError, BehaviorVersion};

/// A point of a run: the state right after the command at `seq`, in attempt `attempt`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct HistoryPoint {
    pub(crate) attempt: u32,
    pub(crate) seq: u64,
}

/// The attempt-scoped state at a point.
pub(crate) struct RebuiltAttempt {
    pub(crate) rows: Vec<AttemptRow>,
    pub(crate) run_status: CheckpointRunStatus,
    /// Values the rows hold that the transaction's store lacks, from replay or offloaded values.
    pub(crate) unstored_values: Vec<TimeTravelStagedValue>,
}

/// What a rebuild may use besides the transaction's store and the history source.
#[derive(Clone, Copy)]
pub(crate) struct ReplayResources<'resources> {
    /// Values the store no longer holds.
    pub(crate) offloaded_values: &'resources dyn OffloadedValues,
    /// Largest number of key and value bytes the replayed writes may hold in memory.
    pub(crate) memory_budget_bytes: u64,
}

/// Rebuilds the attempt state at `point` on top of what `transaction` reads.
///
/// The checkpoint and the replayed records come from `history`.
///
/// Returns a rejection if required permission projection bytes were collected, if the replay
/// exceeds its memory budget, or if the rows hold a value that neither the store, the replay,
/// nor the offloaded values have. Returns an error for other state failures.
pub(crate) fn rebuild_attempt_state(
    transaction: &FlowTransaction<'_>,
    history: &dyn HistorySource,
    rows: RunRows,
    record: &RunRecord,
    point: HistoryPoint,
    resources: ReplayResources<'_>,
) -> Result<Result<RebuiltAttempt, CommandReply>, ApplyError> {
    let checkpoint_seq = required_attempt_record(transaction, rows, point.attempt)?
        .checkpoint_seqs
        .iter()
        .copied()
        .filter(|seq| *seq <= point.seq)
        .max()
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!(
                "run attempt {} has no checkpoint at or before seq {}",
                point.attempt, point.seq
            ),
        })?;
    let checkpoint: CheckpointRecord = history
        .checkpoint(point.attempt, checkpoint_seq)?
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!(
                "checkpoint ({}, {checkpoint_seq}) is listed but missing",
                point.attempt
            ),
        })?;
    let checkpoint_rows = checkpoint
        .rows
        .iter()
        .map(AttemptRow::from_checkpoint_row)
        .collect::<Result<Vec<_>, _>>()?;
    let mut replay_record = record.clone();
    set_run_status(
        &mut replay_record,
        &checkpoint.run_status.unwrap_or_default(),
    );
    replay_record.current_attempt = point.attempt;
    replay_record.is_purging = false;
    let base = TransactionReader::new(transaction);
    let mut replay_state = ReplayState::new(
        &base,
        resources.offloaded_values,
        rows,
        &checkpoint_rows,
        &replay_record,
    );
    let context = transaction.context();
    let over_budget = |replay_state: &ReplayState<'_>| {
        (replay_state.overlay_bytes() > resources.memory_budget_bytes).then(|| {
            replay_memory_exceeded_reply(checkpoint_seq, point, resources.memory_budget_bytes)
        })
    };
    if let Some(reply) = over_budget(&replay_state) {
        return Ok(Err(reply));
    }
    for history_record in
        history.history_records(checkpoint_seq + 1..=point.seq, HISTORY_PAGE_RECORDS)
    {
        let history_record = history_record?;
        if history_record.attempt != point.attempt {
            continue;
        }
        if let Err(reply) = replay_command(&mut replay_state, &context, &history_record)? {
            return Ok(Err(reply));
        }
        if let Some(reply) = over_budget(&replay_state) {
            return Ok(Err(reply));
        }
    }
    let replay_transaction = FlowTransaction::new(&replay_state, context);
    let rebuilt_rows = read_attempt_rows(&replay_transaction, rows)?;
    let run_status = run_status_of(&replay_state.run_record()?);
    let newly_held = newly_held_values(transaction, rows, record, &rebuilt_rows, &run_status)?;
    let unstored_values = match unstored_values(
        transaction,
        &replay_state,
        resources.offloaded_values,
        rows,
        newly_held,
    )? {
        Ok(values) => values,
        Err(reply) => return Ok(Err(reply)),
    };
    Ok(Ok(RebuiltAttempt {
        rows: rebuilt_rows,
        run_status,
        unstored_values,
    }))
}

/// Returns the value-table values, with their sizes, that the restored attempt state holds and
/// the run's current attempt state does not: restoring it holds them anew.
pub(crate) fn newly_held_values(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
    restored_rows: &[AttemptRow],
    restored_status: &CheckpointRunStatus,
) -> Result<BTreeMap<ValueId, u64>, ApplyError> {
    let current_rows = read_attempt_rows(transaction, rows)?;
    let current = held_table_values(&holder_rows(&current_rows, &run_status_of(record)))?;
    let mut restored = held_table_values(&holder_rows(restored_rows, restored_status))?;
    restored.retain(|value_id, _| !current.contains_key(value_id));
    Ok(restored)
}

/// Returns the values of `value_ids` that the transaction's store lacks, read from replayed
/// writes or offloaded values.
///
/// Returns a rejection naming the first value that neither has: a rebuild must never restore a
/// stand-in in its place.
fn unstored_values(
    transaction: &FlowTransaction<'_>,
    replay_state: &ReplayState<'_>,
    offloaded_values: &dyn OffloadedValues,
    rows: RunRows,
    value_ids: BTreeMap<ValueId, u64>,
) -> Result<Result<Vec<TimeTravelStagedValue>, CommandReply>, ApplyError> {
    let mut unstored = Vec::new();
    for (value_id, size_bytes) in value_ids {
        if is_stored_value(transaction, rows, value_id)? {
            continue;
        }
        let value = match replay_state.replayed_value(value_id)? {
            Some(value) => Some(value),
            None => offloaded_values.offloaded_value(rows.run_id, value_id)?,
        };
        let Some(value) = value else {
            return Ok(Err(unavailable_value_reply(rows, value_id)));
        };
        unstored.push(TimeTravelStagedValue {
            value_id: Some(StoredValueId {
                run_seq: value_id.run_seq,
                index_in_command: value_id.index_in_command,
            }),
            size_bytes,
            value: Some(value),
        });
    }
    Ok(Ok(unstored))
}

fn unavailable_value_reply(rows: RunRows, value_id: ValueId) -> CommandReply {
    let reference = ValueReference {
        run_id: rows.run_id,
        value_id,
    };
    error_reply(
        grpc_code::FAILED_PRECONDITION,
        ErrorSubStatus::Uncategorized,
        format!(
            "the state at the time travel target holds value {reference}, which the run no \
             longer stores; choose a later target point"
        ),
    )
}

fn replay_memory_exceeded_reply(
    checkpoint_seq: u64,
    point: HistoryPoint,
    memory_budget_bytes: u64,
) -> CommandReply {
    error_reply(
        grpc_code::RESOURCE_EXHAUSTED,
        ErrorSubStatus::Uncategorized,
        format!(
            "time travel needs more than {memory_budget_bytes} bytes of memory to replay run \
             attempt {} from its checkpoint at run seq {checkpoint_seq} to the target at run seq \
             {}; choose a later target point or raise history.timeTravelReplayMemoryBytes",
            point.attempt, point.seq
        ),
    )
}

fn replay_command(
    replay_state: &mut ReplayState<'_>,
    context: &ApplyContext,
    history_record: &HistoryRecord,
) -> Result<Result<(), CommandReply>, ApplyError> {
    let Some(command) = replayed_command(history_record)? else {
        return Ok(Ok(()));
    };
    replay_state.prepare_command(history_record.seq)?;
    let behavior_version = BehaviorVersion::from_command(command.behavior_version)?;
    let replayed = crate::command_handlers::apply_replayed_command(
        context,
        replay_state,
        &command,
        behavior_version,
        has_flow_closed(history_record),
        &history_record.events,
    )?;
    let writes = match replayed {
        Ok(writes) => writes,
        Err(reply) => return Ok(Err(reply)),
    };
    replay_state.commit(writes)?;
    Ok(Ok(()))
}

/// The command to replay for a history record, or `None` when it changes only run-scoped state.
fn replayed_command(history_record: &HistoryRecord) -> Result<Option<FlowCommand>, ApplyError> {
    let Some(command) = &history_record.command else {
        return Err(ApplyError::InvariantViolation {
            detail: format!("history record {} has no command", history_record.seq),
        });
    };
    let Some(body) = &command.command else {
        return Err(ApplyError::InvariantViolation {
            detail: format!("history record {} has no command body", history_record.seq),
        });
    };
    let replayed_body = match body {
        Command::TimersFired(fired) => {
            let timers: Vec<_> = fired
                .timers
                .iter()
                .filter(|timer| timer.run_attempt != 0)
                .cloned()
                .collect();
            if timers.is_empty() {
                return Ok(None);
            }
            Command::TimersFired(TimersFiredCommand { timers })
        }
        // Only an ack or check that finalized a pending close changed attempt-scoped state.
        Command::AttributeSyncAck(_) | Command::PendingCloseCheck(_)
            if has_flow_closed(history_record) =>
        {
            body.clone()
        }
        Command::FlowConfigUpdate(_)
        | Command::OutboxAck(_)
        | Command::AttributeSyncAck(_)
        | Command::PendingCloseCheck(_)
        | Command::HardDeleteObjectsDeleted(_)
        | Command::RunPurge(_) => return Ok(None),
        // A queued attempt never records history.
        Command::StepAttemptQueued(_) => {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history record {} holds a StepAttemptQueued command",
                    history_record.seq
                ),
            });
        }
        // An upload acknowledgment never records history.
        Command::HistorySegmentUploaded(_) => {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history record {} holds a HistorySegmentUploaded command",
                    history_record.seq
                ),
            });
        }
        // Staging a time travel result never records history.
        Command::TimeTravelStage(_) => {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history record {} holds a TimeTravelStage command",
                    history_record.seq
                ),
            });
        }
        // A partition command belongs to no run, so it never records history.
        Command::SetHistoryOffloadMode(_) => {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history record {} holds a SetHistoryOffloadMode command",
                    history_record.seq
                ),
            });
        }
        // Attempts start at a checkpoint after these, and only the latest run travels.
        Command::FlowStart(_) | Command::TimeTravel(_) => {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history record {} starts a run or attempt inside the replayed range",
                    history_record.seq
                ),
            });
        }
        other => other.clone(),
    };
    Ok(Some(FlowCommand {
        command: Some(replayed_body),
        ..command.clone()
    }))
}

/// Whether the recorded command closed its run, which its FlowClosed history event shows.
fn has_flow_closed(history_record: &HistoryRecord) -> bool {
    history_record.events.iter().any(|event| {
        matches!(
            event.payload,
            Some(flow_history_event::Payload::FlowClosed(_))
        )
    })
}
