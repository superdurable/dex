// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Rebuilds S' (plan section 8.4 step 2): a checkpoint, then the attempt's commands replayed.

use std::cell::Cell;
use std::collections::BTreeMap;

use dexzd_keyspace::{ValueId, ValueReference};
use dexzd_protocol::dex::{ErrorSubStatus, flow_history_event};
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, CheckpointRow, CheckpointRunStatus, CommandReply, FlowCommand, HistoryRecord,
    RunRecord, StoredValueId, TimeTravelStagedValue, TimersFiredCommand, flow_command::Command,
};
use prost::Message;

use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::history::attempt_scope::{
    AttemptRow, holder_rows, read_attempt_rows, run_status_of, set_run_status,
    visit_attempt_row_pages,
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
    /// Memory the whole time travel computation may hold.
    pub(crate) memory_budget: &'resources ReplayMemoryBudget,
}

/// Memory one time travel computation may hold.
///
/// The computation reserves bytes before each allocation it keeps; transient replay writes
/// must fit beside the reservations.
pub(crate) struct ReplayMemoryBudget {
    limit_bytes: u64,
    reserved_bytes: Cell<u64>,
}

impl ReplayMemoryBudget {
    pub(crate) fn new(limit_bytes: u64) -> Self {
        Self {
            limit_bytes,
            reserved_bytes: Cell::new(0),
        }
    }

    /// Returns a budget no computation exceeds, for apply's own computations.
    pub(crate) fn unlimited() -> Self {
        Self::new(u64::MAX)
    }

    /// Reserves `bytes` for an allocation the computation keeps.
    ///
    /// Returns false, reserving nothing, when the reservations would exceed the limit.
    pub(crate) fn reserve(&self, bytes: u64) -> bool {
        let reserved_bytes = self.reserved_bytes.get().saturating_add(bytes);
        if reserved_bytes > self.limit_bytes {
            return false;
        }
        self.reserved_bytes.set(reserved_bytes);
        true
    }

    /// Whether `transient_bytes` fit beside the reservations, without reserving them.
    pub(crate) fn fits(&self, transient_bytes: u64) -> bool {
        self.reserved_bytes.get().saturating_add(transient_bytes) <= self.limit_bytes
    }

    pub(crate) fn limit_bytes(&self) -> u64 {
        self.limit_bytes
    }
}

/// Returns an encoded length as the byte count a budget reserves.
pub(crate) fn encoded_bytes(length: usize) -> u64 {
    u64::try_from(length).unwrap_or(u64::MAX)
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
    let memory_budget = resources.memory_budget;
    let exceeded = |held: &str| {
        Ok(Err(replay_memory_exceeded_reply(
            point,
            memory_budget.limit_bytes(),
            held,
        )))
    };
    let checkpoint_held = format!("the checkpoint at run seq {checkpoint_seq}");
    if !memory_budget.reserve(encoded_bytes(checkpoint.encoded_len())) {
        return exceeded(&checkpoint_held);
    }
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
    let replay_held = format!("the replay from its checkpoint at run seq {checkpoint_seq}");
    if !memory_budget.fits(replay_state.overlay_bytes()) {
        return exceeded(&replay_held);
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
        if !memory_budget.fits(replay_state.overlay_bytes()) {
            return exceeded(&replay_held);
        }
    }
    // The rebuilt rows copy at most the replayed writes, which stay alive beside them.
    if !memory_budget.reserve(replay_state.overlay_bytes())
        || !memory_budget.fits(replay_state.overlay_bytes())
    {
        return exceeded(&replay_held);
    }
    let replay_transaction = FlowTransaction::new(&replay_state, context);
    let rebuilt_rows = read_attempt_rows(&replay_transaction, rows)?;
    let run_status = run_status_of(&replay_state.run_record()?);
    let Some(newly_held) = newly_held_values(
        transaction,
        rows,
        record,
        &rebuilt_rows,
        &run_status,
        HeldValueScan {
            memory_budget,
            live_bytes: replay_state.overlay_bytes(),
        },
    )?
    else {
        return exceeded("the values the current attempt state holds");
    };
    let unstored_values = match unstored_values(
        transaction,
        &replay_state,
        resources,
        rows,
        point,
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

/// Rows of the current attempt that one page of the holder scan reads.
const HOLDER_SCAN_PAGE_ROWS: usize = 64;

/// Bytes one held value entry is charged: its ID, size, and map node.
const HELD_VALUE_ENTRY_BYTES: u64 = 64;

/// The memory a held value scan may use.
#[derive(Clone, Copy)]
pub(crate) struct HeldValueScan<'budget> {
    /// Charged for each held value entry and checked for each page of rows.
    pub(crate) memory_budget: &'budget ReplayMemoryBudget,
    /// Unreserved bytes alive beside the scan, such as the replayed writes.
    pub(crate) live_bytes: u64,
}

/// Returns the value-table values, with their sizes, that the restored attempt state holds and
/// the run's current attempt state does not: restoring it holds them anew.
///
/// It reads the current attempt a page at a time and charges each held value to the budget.
///
/// Returns `None` when the scan exceeds `scan`'s memory budget, or an error when a row is
/// corrupt.
pub(crate) fn newly_held_values(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
    restored_rows: &[AttemptRow],
    restored_status: &CheckpointRunStatus,
    scan: HeldValueScan<'_>,
) -> Result<Option<BTreeMap<ValueId, u64>>, ApplyError> {
    let mut current = HeldValues::new(scan.memory_budget);
    if !current.add(&holder_rows(&[], &run_status_of(record)))? {
        return Ok(None);
    }
    let is_scanned = visit_attempt_row_pages(transaction, rows, HOLDER_SCAN_PAGE_ROWS, |page| {
        let page_bytes = page
            .iter()
            .map(|row| encoded_bytes(row.key_suffix.len() + row.value.len()))
            .fold(0, u64::saturating_add);
        // The page and its checkpoint-row copy are alive together.
        let transient_bytes = scan.live_bytes.saturating_add(page_bytes.saturating_mul(2));
        if !scan.memory_budget.fits(transient_bytes) {
            return Ok(false);
        }
        let page_rows: Vec<CheckpointRow> =
            page.iter().map(AttemptRow::to_checkpoint_row).collect();
        current.add(&page_rows)
    })?;
    if !is_scanned {
        return Ok(None);
    }
    let mut restored = HeldValues::new(scan.memory_budget);
    if !restored.add(&holder_rows(&[], restored_status))? {
        return Ok(None);
    }
    for row in restored_rows {
        if !restored.add(&[row.to_checkpoint_row()])? {
            return Ok(None);
        }
    }
    let mut newly_held = restored.values;
    newly_held.retain(|value_id, _| !current.values.contains_key(value_id));
    Ok(Some(newly_held))
}

/// Value-table values that rows hold, each charged to a memory budget once.
struct HeldValues<'budget> {
    memory_budget: &'budget ReplayMemoryBudget,
    values: BTreeMap<ValueId, u64>,
}

impl<'budget> HeldValues<'budget> {
    fn new(memory_budget: &'budget ReplayMemoryBudget) -> Self {
        Self {
            memory_budget,
            values: BTreeMap::new(),
        }
    }

    /// Adds the values `rows` hold.
    ///
    /// Returns false when a new entry does not fit in the budget, or an error for a corrupt row.
    fn add(&mut self, rows: &[CheckpointRow]) -> Result<bool, ApplyError> {
        for (value_id, size_bytes) in held_table_values(rows)? {
            if self.values.contains_key(&value_id) {
                continue;
            }
            if !self.memory_budget.reserve(HELD_VALUE_ENTRY_BYTES) {
                return Ok(false);
            }
            self.values.insert(value_id, size_bytes);
        }
        Ok(true)
    }
}

/// Returns the values of `value_ids` that the transaction's store lacks, read from replayed
/// writes or offloaded values. Each value reserves its recorded size before it is read.
///
/// Returns a rejection naming the first value that neither has: a rebuild must never restore a
/// stand-in in its place. Returns a rejection when the values exceed the memory budget.
fn unstored_values(
    transaction: &FlowTransaction<'_>,
    replay_state: &ReplayState<'_>,
    resources: ReplayResources<'_>,
    rows: RunRows,
    point: HistoryPoint,
    value_ids: BTreeMap<ValueId, u64>,
) -> Result<Result<Vec<TimeTravelStagedValue>, CommandReply>, ApplyError> {
    let mut unstored = Vec::new();
    let mut first_unavailable = None;
    for (value_id, size_bytes) in value_ids {
        if is_stored_value(transaction, rows, value_id)? {
            continue;
        }
        if !resources.memory_budget.reserve(size_bytes) {
            return Ok(Err(replay_memory_exceeded_reply(
                point,
                resources.memory_budget.limit_bytes(),
                "the values the restored state holds",
            )));
        }
        let value = match replay_state.replayed_value(value_id)? {
            Some(value) => Some(value),
            None => resources
                .offloaded_values
                .offloaded_value(rows.run_id, value_id)?,
        };
        // Every value is asked for, so an owner learns all it must fetch at once.
        let Some(value) = value else {
            first_unavailable.get_or_insert(value_id);
            continue;
        };
        unstored.push(TimeTravelStagedValue {
            value_id: Some(StoredValueId {
                run_seq: value_id.run_seq,
                index_in_command: value_id.index_in_command,
            }),
            value: Some(value),
        });
    }
    if let Some(value_id) = first_unavailable {
        return Ok(Err(unavailable_value_reply(rows, value_id)));
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

/// Returns the RESOURCE_EXHAUSTED reply of a computation for the target `point` that could not
/// hold `held` within `memory_budget_bytes`.
pub(crate) fn replay_memory_exceeded_reply(
    point: HistoryPoint,
    memory_budget_bytes: u64,
    held: &str,
) -> CommandReply {
    error_reply(
        grpc_code::RESOURCE_EXHAUSTED,
        ErrorSubStatus::Uncategorized,
        format!(
            "time travel to run attempt {} at run seq {} needs more than {memory_budget_bytes} \
             bytes of memory to hold {held}; choose a later target point, set \
             skip_writes_reapply to true, or raise history.timeTravelReplayMemoryBytes",
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
        // Reviving an offloaded value never records history.
        Command::ValueRevive(_) => {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history record {} holds a ValueRevive command",
                    history_record.seq
                ),
            });
        }
        // Recording deleted history objects never records history.
        Command::HistorySegmentObjectsDeleted(_) => {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history record {} holds a HistorySegmentObjectsDeleted command",
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
