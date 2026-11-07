// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Computes a TimeTravel result: the target point, its rebuilt attempt state, and later input.
//!
//! Owners run it before they stage a result; apply runs it only for unstaged log records.

use std::ops::RangeInclusive;

use dexzd_keyspace::{RunId, TenantId};
use dexzd_protocol::dex::{ErrorSubStatus, ResetFlowRequest};
use dexzd_protocol::dexzd::v1::{
    CommandReply, FlowCommand, RunRecord, TimeTravelCommand, TimeTravelStageCommand,
    TimeTravelStagedInput, flow_command::Command,
};
use prost::Message;

use super::TimeTravelResult;
use super::staging::{staged_chunks, staged_result_bytes};
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::command_handlers::run_cursor::flow_not_found_reply;
use crate::history::lineage::{HISTORY_PAGE_RECORDS, HistorySource, Lineage};
use crate::history::reset_target::resolve_target;
use crate::history::state_rebuild::{
    ReplayMemoryBudget, ReplayResources, encoded_bytes, rebuild_attempt_state,
    replay_memory_exceeded_reply,
};
use crate::state_access::{FlowTransaction, RunRows, read_flow_directory};
use crate::values::OffloadedValues;
use crate::{ApplyContext, ApplyError, FlowStateReader};

/// What a time travel computation reads.
pub struct TimeTravelComputationSources<'sources> {
    /// The committed state of the Flow.
    pub state: &'sources dyn FlowStateReader,
    /// History records and checkpoints of the run, as `state` shows them.
    pub history: &'sources dyn HistorySource,
    /// Values of the run that `state` no longer holds.
    pub offloaded_values: &'sources dyn OffloadedValues,
}

/// A time travel request that an owner computes before it proposes TimeTravel.
pub struct TimeTravelComputationRequest<'request> {
    /// The run to time travel; it must be the latest run of its Flow.
    pub run_id: RunId,
    /// The client's request.
    pub reset: &'request ResetFlowRequest,
    /// The owner's current time, which HistoryEventTime targets are checked against.
    pub now_micros: i64,
    /// Largest number of bytes the computation may hold in memory, for replay, rows, values,
    /// input, and staging.
    pub replay_memory_budget_bytes: u64,
}

/// The outcome of [`compute_time_travel`].
#[derive(Debug)]
pub enum TimeTravelComputation {
    /// The result, ready to stage.
    Computed(Box<ComputedTimeTravel>),
    /// The reply that rejects the request, as apply would reject it.
    Rejected(CommandReply),
}

/// A computed TimeTravel result and the run state it was computed from.
#[derive(Debug)]
pub struct ComputedTimeTravel {
    request: ResetFlowRequest,
    run_attempt: u32,
    computed_through_seq: u64,
    result: TimeTravelResult,
}

impl ComputedTimeTravel {
    /// Returns the run's current attempt when the result was computed. Stage and TimeTravel
    /// commands expect it, so a time travel in between rejects them.
    pub fn run_attempt(&self) -> u32 {
        self.run_attempt
    }

    /// Returns the last run seq the result covers; the TimeTravel command carries it.
    pub fn computed_through_seq(&self) -> u64 {
        self.computed_through_seq
    }

    /// Returns the TimeTravelStage commands that stage the result under `staging_id`.
    ///
    /// Chunks hold at most 8 MiB of entries or one larger entry; chunk 0 binds the TimeTravel.
    ///
    /// Returns [`StagedEntryTooLarge`] when an entry cannot fit in one log record.
    pub fn stage_commands(
        &self,
        staging_id: &[u8],
    ) -> Result<Vec<TimeTravelStageCommand>, StagedEntryTooLarge> {
        staged_chunks(
            staging_id,
            &self.result,
            &self.request,
            self.run_attempt,
            self.computed_through_seq,
        )
    }

    /// Returns the TimeTravel command that applies the result staged under `staging_id`.
    ///
    /// Apply checks its request, seq, and target point against the staging. Propose it at
    /// [`Self::run_attempt`].
    pub fn time_travel_command(
        &self,
        staging_id: &[u8],
        should_restore_running_as_pending: bool,
    ) -> TimeTravelCommand {
        TimeTravelCommand {
            request: Some(self.request.clone()),
            should_restore_running_as_pending,
            staging_id: staging_id.to_vec(),
            computed_through_seq: self.computed_through_seq,
            target_run_attempt: self.result.target.point.attempt,
            target_run_seq: self.result.target.point.seq,
        }
    }

    /// Returns the IDs of the values the result stages because the store lacked them.
    #[cfg(feature = "test-util")]
    pub fn unstored_value_ids_for_test(&self) -> Vec<dexzd_keyspace::ValueId> {
        self.result
            .unstored_values
            .iter()
            .filter_map(|staged| staged.value_id)
            .map(|id| dexzd_keyspace::ValueId {
                run_seq: id.run_seq,
                index_in_command: id.index_in_command,
            })
            .collect()
    }
}

/// A staged result entry is larger than one log record can carry.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error(
    "time travel cannot stage a {kind} of {size_bytes} bytes; one command carries at most \
     {max_bytes} bytes"
)]
pub struct StagedEntryTooLarge {
    /// What the entry is: a row, a value, or an external input.
    pub kind: &'static str,
    /// Encoded size of the entry.
    pub size_bytes: usize,
    /// Largest entry one TimeTravelStage command carries.
    pub max_bytes: usize,
}

/// Computes what a TimeTravel of `request` restores, with the same code that apply runs.
///
/// The result covers the run through its last applied seq; apply adds the input recorded later.
///
/// Returns [`TimeTravelComputation::Rejected`] with the reply apply would give, including when
/// the replay exceeds its memory budget or the target state holds a value that no source has.
/// Returns an error when stored state is corrupt.
pub fn compute_time_travel(
    context: &ApplyContext,
    sources: &TimeTravelComputationSources<'_>,
    request: &TimeTravelComputationRequest<'_>,
) -> Result<TimeTravelComputation, ApplyError> {
    let transaction = FlowTransaction::new(sources.state, *context);
    let rows = RunRows::new(context.flow_hash, request.run_id);
    let Some(record) = rows.run_record(&transaction)? else {
        return Ok(TimeTravelComputation::Rejected(flow_not_found_reply(
            &request.reset.flow_id,
        )));
    };
    if record.is_purging {
        return Ok(TimeTravelComputation::Rejected(flow_not_found_reply(
            &record.flow_id,
        )));
    }
    let computed_through_seq = record.next_run_seq.saturating_sub(1);
    let memory_budget = ReplayMemoryBudget::new(request.replay_memory_budget_bytes);
    let resources = ReplayResources {
        offloaded_values: sources.offloaded_values,
        memory_budget: &memory_budget,
    };
    let result = match compute_result(
        &transaction,
        sources.history,
        rows,
        &record,
        request.reset,
        request.now_micros,
        computed_through_seq,
        resources,
    )? {
        Ok(result) => result,
        Err(reply) => return Ok(TimeTravelComputation::Rejected(reply)),
    };
    // Staging encodes every entry once more.
    if !memory_budget.reserve(staged_result_bytes(&result)) {
        return Ok(TimeTravelComputation::Rejected(
            replay_memory_exceeded_reply(
                result.target.point,
                memory_budget.limit_bytes(),
                "the staged result",
            ),
        ));
    }
    Ok(TimeTravelComputation::Computed(Box::new(
        ComputedTimeTravel {
            request: request.reset.clone(),
            run_attempt: record.current_attempt,
            computed_through_seq,
            result,
        },
    )))
}

/// Computes the result of `request` through `computed_through_seq` on the state `transaction`
/// reads.
///
/// Returns the reply that rejects the request, or an error when stored state is corrupt.
#[expect(
    clippy::too_many_arguments,
    reason = "Each input is a distinct part of one computation."
)]
pub(super) fn compute_result(
    transaction: &FlowTransaction<'_>,
    history: &dyn HistorySource,
    rows: RunRows,
    record: &RunRecord,
    request: &ResetFlowRequest,
    now_micros: i64,
    computed_through_seq: u64,
    resources: ReplayResources<'_>,
) -> Result<Result<TimeTravelResult, CommandReply>, ApplyError> {
    let (selected_attempt, lineage) = match selected_lineage(transaction, rows, record, request)? {
        Ok(selected) => selected,
        Err(reply) => return Ok(Err(reply)),
    };
    let target = match resolve_target(
        transaction,
        history,
        rows,
        record,
        now_micros,
        request,
        &lineage,
    )? {
        Ok(target) => target,
        Err(reply) => return Ok(Err(reply)),
    };
    let is_reapplying = !request.skip_writes_reapply;
    if let Some(reply) = pruned_input_rejection(record, target.point.seq, is_reapplying) {
        return Ok(Err(reply));
    }
    let reapplied_inputs = if is_reapplying {
        match collect_external_inputs(
            history,
            &lineage,
            target.point.seq + 1..=computed_through_seq,
            record.window_start_seq,
            resources.memory_budget,
        )? {
            Ok(inputs) => inputs,
            Err(InputCollectionFailure::MemoryExceeded) => {
                return Ok(Err(replay_memory_exceeded_reply(
                    target.point,
                    resources.memory_budget.limit_bytes(),
                    "the external input reapplied after the target",
                )));
            }
            Err(InputCollectionFailure::MissingRecord(missing_seq)) => {
                return Err(ApplyError::InvariantViolation {
                    detail: format!(
                        "history of run {} has no record at run seq {missing_seq}, between the \
                         time travel target and run seq {computed_through_seq}",
                        rows.run_id
                    ),
                });
            }
        }
    } else {
        Vec::new()
    };
    let rebuilt =
        match rebuild_attempt_state(transaction, history, rows, record, target.point, resources)? {
            Ok(rebuilt) => rebuilt,
            Err(reply) => return Ok(Err(reply)),
        };
    Ok(Ok(TimeTravelResult {
        selected_attempt,
        target,
        run_status: rebuilt.run_status,
        rows: rebuilt.rows,
        unstored_values: rebuilt.unstored_values,
        reapplied_inputs,
    }))
}

/// Returns the attempt whose lineage the request selects, with that lineage, after checking
/// that the run is its Flow's latest run.
///
/// Returns the reply that rejects the request, or an error when stored state is corrupt.
pub(super) fn selected_lineage(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
    request: &ResetFlowRequest,
) -> Result<Result<(u32, Lineage), CommandReply>, ApplyError> {
    let current_attempt = record.current_attempt;
    let selected_attempt = match u32::try_from(request.run_attempt) {
        Ok(0) => current_attempt,
        Ok(attempt) if attempt <= current_attempt => attempt,
        _ => {
            return Ok(Err(error_reply(
                grpc_code::INVALID_ARGUMENT,
                ErrorSubStatus::Uncategorized,
                format!(
                    "run attempt {} does not exist; the run has attempts 1 to {current_attempt}",
                    request.run_attempt
                ),
            )));
        }
    };
    let directory = read_flow_directory(transaction, TenantId(record.tenant_id), &record.flow_id)?;
    let is_latest_run = directory
        .runs
        .last()
        .is_some_and(|run| run.run_id == rows.run_id.as_bytes());
    if !is_latest_run {
        // ID reuse decisions read only the latest run, so an older run must stay closed.
        return Ok(Err(error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "only the latest run of Flow {} can time travel",
                record.flow_id
            ),
        )));
    }
    let lineage = Lineage::of_attempt(transaction, rows, selected_attempt)?;
    Ok(Ok((selected_attempt, lineage)))
}

/// Returns the rejection when reapplied input after `target_seq` left the window.
pub(super) fn pruned_input_rejection(
    record: &RunRecord,
    target_seq: u64,
    is_reapplying: bool,
) -> Option<CommandReply> {
    (is_reapplying && record.max_pruned_external_input_seq > target_seq).then(|| {
        error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "external input after the target point left the time travel window (run seq {}), \
                 so it cannot be reapplied; set skip_writes_reapply to travel without reapplying \
                 writes",
                record.max_pruned_external_input_seq
            ),
        )
    })
}

/// Why [`collect_external_inputs`] collected no input.
pub(super) enum InputCollectionFailure {
    /// `history` lacks the record at this seq, which is at or after the window start.
    MissingRecord(u64),
    /// Holding the input would exceed the computation's memory budget.
    MemoryExceeded,
}

/// External input of the lineage with seq in `seqs`, in (run seq, index) order. StopFlow is
/// never input.
///
/// A TimeTravel record contributes the input it reapplied. Each input reserves its bytes in
/// `memory_budget` first.
///
/// Every seq from the window start `window_start_seq` on has a record; pruning removed earlier
/// ones.
///
/// Returns [`InputCollectionFailure::MissingRecord`] with the first seq of `seqs` from the
/// window start on whose record `history` lacks, or [`InputCollectionFailure::MemoryExceeded`].
/// Returns an error when a record cannot be read.
pub(super) fn collect_external_inputs(
    history: &dyn HistorySource,
    lineage: &Lineage,
    seqs: RangeInclusive<u64>,
    window_start_seq: u64,
    memory_budget: &ReplayMemoryBudget,
) -> Result<Result<Vec<TimeTravelStagedInput>, InputCollectionFailure>, ApplyError> {
    let mut inputs = Vec::new();
    let mut expected_seq = (*seqs.start()).max(window_start_seq);
    let last_seq = *seqs.end();
    for record in history.history_records(seqs, HISTORY_PAGE_RECORDS) {
        let record = record?;
        if record.seq >= window_start_seq {
            if record.seq != expected_seq {
                return Ok(Err(InputCollectionFailure::MissingRecord(expected_seq)));
            }
            expected_seq += 1;
        }
        if !record.is_external_input || !lineage.contains(record.attempt, record.seq) {
            continue;
        }
        let Some(command) = record.command else {
            continue;
        };
        let record_inputs = if matches!(command.command, Some(Command::TimeTravel(_))) {
            record.reapplied_inputs
        } else if is_reapplied_input(&command) {
            vec![command]
        } else {
            continue;
        };
        for (index, input) in record_inputs.into_iter().enumerate() {
            if !memory_budget.reserve(encoded_bytes(input.encoded_len())) {
                return Ok(Err(InputCollectionFailure::MemoryExceeded));
            }
            let index_in_record =
                u32::try_from(index).map_err(|_| ApplyError::InvariantViolation {
                    detail: format!("history record {} holds over 2^32 inputs", record.seq),
                })?;
            inputs.push(TimeTravelStagedInput {
                run_seq: record.seq,
                index_in_record,
                command: Some(input),
            });
        }
    }
    if expected_seq <= last_seq {
        return Ok(Err(InputCollectionFailure::MissingRecord(expected_seq)));
    }
    Ok(Ok(inputs))
}

/// Whether `command` is a kind of external input that a time travel reapplies.
pub(super) fn is_reapplied_input(command: &FlowCommand) -> bool {
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
