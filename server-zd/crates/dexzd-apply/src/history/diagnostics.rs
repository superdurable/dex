// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Read-only state hashes for debug and test verification; apply never calls these helpers.

use dexzd_keyspace::{RunId, RunTable};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::{
    FlowCommand, StepExecutionRecord, TimerRecord, flow_command::Command,
};
use prost::Message;

use super::attempt_scope::{AttemptRow, decode_timer_id, read_attempt_rows, run_status_of};
use super::lineage::{Lineage, LocalHistorySource, genesis_seq};
use super::reset_target::resolve_target;
use super::state_rebuild::{
    HistoryPoint, RebuiltAttempt, ReplayMemoryBudget, ReplayResources, rebuild_attempt_state,
};
use super::time_travel::staged_attempt_state;
use crate::command_handlers::CommandScope;
use crate::command_handlers::run_cursor::RunCursor;
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::NoOffloadedValues;
use crate::{ApplyContext, ApplyError, BehaviorVersion, FlowStateReader};

const FNV_128_OFFSET_BASIS: u128 = 0x6c62_272e_07bb_0142_62b8_2175_6295_c58d;
const FNV_128_PRIME: u128 = 0x0000_0000_0100_0000_0000_0000_0000_013b;

/// Hash of the attempt state at one semantic history point.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SemanticPointHash {
    /// Run containing the point.
    pub run_id: RunId,
    /// Attempt containing the point.
    pub run_attempt: u32,
    /// Run sequence of the semantic point.
    pub run_seq: u64,
    /// Stable hash of the attempt's rows and status.
    pub state_hash: [u8; 16],
    /// Oldest retained semantic sequence for this run.
    pub window_start_seq: u64,
    /// Preserved start sequence of the run.
    pub genesis_seq: u64,
}

/// Hashes the command's staged semantic point; returns `None` when no matching point exists.
pub fn current_semantic_point_hash(
    context: &ApplyContext,
    state: &dyn FlowStateReader,
    run_id: RunId,
    command_id: &[u8],
) -> Result<Option<SemanticPointHash>, ApplyError> {
    let transaction = FlowTransaction::new(state, *context);
    let rows = RunRows::new(context.flow_hash, run_id);
    let Some(record) = rows.run_record(&transaction)? else {
        return Ok(None);
    };
    let Some(run_seq) = record.next_run_seq.checked_sub(1) else {
        return Ok(None);
    };
    let Some(history) = rows.history_record(&transaction, run_seq)? else {
        return Ok(None);
    };
    if !history.is_semantic
        || history
            .command
            .as_ref()
            .is_none_or(|command| command.command_id != command_id)
    {
        return Ok(None);
    }
    let attempt_rows = read_attempt_rows(&transaction, rows)?;
    let encoded_state = encode_attempt_state(&attempt_rows, &run_status_of(&record))?;
    Ok(Some(SemanticPointHash {
        run_id,
        run_attempt: record.current_attempt,
        run_seq,
        state_hash: hash_attempt_state(&encoded_state),
        window_start_seq: record.window_start_seq,
        genesis_seq: genesis_seq(&transaction, rows)?,
    }))
}

/// How the run stands at a TimeTravel target, read from its rebuilt state.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TimeTravelTargetClose {
    /// The run is running at the target, possibly with a pending close.
    Running,
    /// The run is closed at the target.
    Closed {
        /// True when the close skipped the Attribute Sync barrier; absent in records written before the marker.
        is_closed_at_once: Option<bool>,
    },
}

/// Hashes a TimeTravel target's reconstructed state; returns `None` when no valid target exists or permission projection bytes were collected.
///
/// A staged TimeTravel hashes the result its owner staged; an unstaged one rebuilds it from
/// local history.
pub fn time_travel_target_hash(
    context: &ApplyContext,
    state: &dyn FlowStateReader,
    command: &FlowCommand,
) -> Result<Option<SemanticPointHash>, ApplyError> {
    let transaction = FlowTransaction::new(state, *context);
    let Some((cursor, point, rebuilt)) = rebuild_time_travel_target(&transaction, command)? else {
        return Ok(None);
    };
    let encoded_state = encode_attempt_state(&rebuilt.rows, &rebuilt.run_status)?;
    Ok(Some(SemanticPointHash {
        run_id: cursor.rows.run_id,
        run_attempt: point.attempt,
        run_seq: point.seq,
        state_hash: hash_attempt_state(&encoded_state),
        window_start_seq: cursor.record.window_start_seq,
        genesis_seq: genesis_seq(&transaction, cursor.rows)?,
    }))
}

/// Reads the close of a TimeTravel target's rebuilt state, without apply's close classification.
///
/// Returns `None` when no valid target exists or permission projection bytes were collected.
pub fn time_travel_target_close(
    context: &ApplyContext,
    state: &dyn FlowStateReader,
    command: &FlowCommand,
) -> Result<Option<TimeTravelTargetClose>, ApplyError> {
    let transaction = FlowTransaction::new(state, *context);
    let Some((_, _, rebuilt)) = rebuild_time_travel_target(&transaction, command)? else {
        return Ok(None);
    };
    let status = rebuilt.run_status;
    if status.status == FlowStatus::Running as i32 {
        return Ok(Some(TimeTravelTargetClose::Running));
    }
    Ok(Some(TimeTravelTargetClose::Closed {
        is_closed_at_once: status.close.and_then(|close| close.is_closed_at_once),
    }))
}

/// Resolves the command's TimeTravel target and rebuilds the attempt state there.
///
/// A staged TimeTravel reads the target and state from the complete staging it names instead.
///
/// Returns the run's cursor, the target point, and the rebuilt state, or `None` when the command
/// is not a TimeTravel with a valid target or permission projection bytes were collected.
fn rebuild_time_travel_target(
    transaction: &FlowTransaction<'_>,
    command: &FlowCommand,
) -> Result<Option<(RunCursor, HistoryPoint, RebuiltAttempt)>, ApplyError> {
    let Some(Command::TimeTravel(time_travel)) = &command.command else {
        return Ok(None);
    };
    let scope = CommandScope::new(
        command,
        BehaviorVersion::from_command(command.behavior_version)?,
    );
    let Ok(cursor) = RunCursor::open(transaction, &scope)? else {
        return Ok(None);
    };
    if !time_travel.staging_id.is_empty() {
        let staged = staged_attempt_state(transaction, &cursor, &time_travel.staging_id)?;
        return Ok(staged.map(|(point, rebuilt)| (cursor, point, rebuilt)));
    }
    let request = time_travel.request.clone().unwrap_or_default();
    let selected_attempt = match u32::try_from(request.run_attempt) {
        Ok(0) => cursor.record.current_attempt,
        Ok(attempt) if attempt <= cursor.record.current_attempt => attempt,
        _ => return Ok(None),
    };
    let lineage = Lineage::of_attempt(transaction, cursor.rows, selected_attempt)?;
    let history = LocalHistorySource::new(transaction, cursor.rows);
    let Ok(target) = resolve_target(
        transaction,
        &history,
        cursor.rows,
        &cursor.record,
        scope.now_micros,
        &request,
        &lineage,
    )?
    else {
        return Ok(None);
    };
    let Ok(rebuilt) = rebuild_attempt_state(
        transaction,
        &history,
        cursor.rows,
        &cursor.record,
        target.point,
        ReplayResources {
            offloaded_values: &NoOffloadedValues,
            memory_budget: &ReplayMemoryBudget::unlimited(),
        },
    )?
    else {
        return Ok(None);
    };
    Ok(Some((cursor, target.point, rebuilt)))
}

fn encode_attempt_state(
    rows: &[AttemptRow],
    status: &dexzd_protocol::dexzd::v1::CheckpointRunStatus,
) -> Result<Vec<u8>, ApplyError> {
    let mut encoded = Vec::new();
    append_bytes(&mut encoded, &status.encode_to_vec());
    let mut canonical_rows = Vec::with_capacity(rows.len());
    for row in rows {
        if row.is_hard_delete_attribute()? {
            continue;
        }
        let (key_suffix, value) = canonicalized_attempt_row(row)?;
        canonical_rows.push((row.table, key_suffix, value));
    }
    canonical_rows.sort_by(|first, second| (first.0, &first.1).cmp(&(second.0, &second.1)));
    for (table, key_suffix, value) in canonical_rows {
        encoded.push(table as u8);
        append_bytes(&mut encoded, &key_suffix);
        append_bytes(&mut encoded, &value);
    }
    Ok(encoded)
}

fn hash_attempt_state(encoded: &[u8]) -> [u8; 16] {
    let mut hash = FNV_128_OFFSET_BASIS;
    for byte in encoded {
        hash = (hash ^ u128::from(*byte)).wrapping_mul(FNV_128_PRIME);
    }
    hash.to_be_bytes()
}

fn canonicalized_attempt_row(row: &AttemptRow) -> Result<(Vec<u8>, Vec<u8>), ApplyError> {
    match row.table {
        RunTable::Timer => {
            let mut timer_id = decode_timer_id(&row.key_suffix)?;
            timer_id.run_attempt = 0;
            let mut timer = TimerRecord::decode(row.value.as_slice()).map_err(|error| {
                ApplyError::InvariantViolation {
                    detail: format!("diagnostic timer row does not decode: {error}"),
                }
            })?;
            if let Some(stored_id) = timer.timer_id.as_mut() {
                stored_id.run_attempt = 0;
            }
            Ok((timer_id.encode_to_vec(), timer.encode_to_vec()))
        }
        RunTable::StepExecution => {
            let mut step = StepExecutionRecord::decode(row.value.as_slice()).map_err(|error| {
                ApplyError::InvariantViolation {
                    detail: format!("diagnostic Step execution row does not decode: {error}"),
                }
            })?;
            if let Some(invocation) = step.invocation.as_mut() {
                invocation.task_id.clear();
            }
            Ok((row.key_suffix.clone(), step.encode_to_vec()))
        }
        _ => Ok((row.key_suffix.clone(), row.value.clone())),
    }
}

fn append_bytes(encoded: &mut Vec<u8>, bytes: &[u8]) {
    encoded.extend_from_slice(&(bytes.len() as u64).to_be_bytes());
    encoded.extend_from_slice(bytes);
}

#[cfg(feature = "test-util")]
pub use target_resolution::{
    TargetResolutionForTest, TimeTravelTargetForTest, resolve_time_travel_target_for_test,
};

/// Test entry points that compare the ways of resolving a TimeTravel target.
#[cfg(feature = "test-util")]
mod target_resolution {
    use std::ops::RangeInclusive;

    use dexzd_keyspace::RunId;
    use dexzd_protocol::dex::ResetFlowRequest;
    use dexzd_protocol::dexzd::v1::{CheckpointRecord, CommandReply};

    use crate::history::lineage::{HistoryRecords, HistorySource, Lineage, LocalHistorySource};
    use crate::history::reset_target::{
        CandidateSource, resolve_target, resolve_target_from_records_for_test,
        resolve_target_from_source_for_test,
    };
    use crate::state_access::{FlowTransaction, RunRows};
    use crate::{ApplyContext, ApplyError, FlowStateReader};

    /// Which target resolution [`resolve_time_travel_target_for_test`] runs.
    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    pub enum TargetResolutionForTest {
        /// TimeTravel's resolution: metadata, or records when the run's metadata is older.
        Apply,
        /// TimeTravel's resolution with a history source that fails every read.
        ApplyWithoutHistoryReads,
        /// Candidates from metadata rows, even when the run's metadata is older.
        Metadata,
        /// Candidates from History records read through the local history source.
        Records,
        /// The resolution from before metadata-based selection, reading every window record.
        RecordOracle,
    }

    /// A resolved TimeTravel target.
    #[derive(Debug, Clone, PartialEq, Eq)]
    pub struct TimeTravelTargetForTest {
        /// Attempt of the target point.
        pub run_attempt: u32,
        /// Run seq of the target point.
        pub run_seq: u64,
        /// Largest event ID of the selected lineage's semantic points up to the target.
        pub last_event_id: i64,
        /// Message of the last FlowClosed event of those points.
        pub close_history_message: Option<String>,
    }

    /// Resolves the target of `request` on run `run_id` the way `resolution` names.
    ///
    /// Returns the target or the rejection reply. Returns an error when state cannot be read,
    /// or when the request names an attempt the run does not have.
    pub fn resolve_time_travel_target_for_test(
        context: &ApplyContext,
        state: &dyn FlowStateReader,
        run_id: RunId,
        now_micros: i64,
        request: &ResetFlowRequest,
        resolution: TargetResolutionForTest,
    ) -> Result<Result<TimeTravelTargetForTest, CommandReply>, ApplyError> {
        let transaction = FlowTransaction::new(state, *context);
        let rows = RunRows::new(context.flow_hash, run_id);
        let record =
            rows.run_record(&transaction)?
                .ok_or_else(|| ApplyError::InvariantViolation {
                    detail: format!("run {run_id} has no run record"),
                })?;
        let selected_attempt = match u32::try_from(request.run_attempt) {
            Ok(0) => record.current_attempt,
            Ok(attempt) if attempt <= record.current_attempt => attempt,
            _ => {
                return Err(ApplyError::InvariantViolation {
                    detail: format!("run attempt {} does not exist", request.run_attempt),
                });
            }
        };
        let lineage = Lineage::of_attempt(&transaction, rows, selected_attempt)?;
        let local_history = LocalHistorySource::new(&transaction, rows);
        let target = match resolution {
            TargetResolutionForTest::Apply => resolve_target(
                &transaction,
                &local_history,
                rows,
                &record,
                now_micros,
                request,
                &lineage,
            )?,
            TargetResolutionForTest::ApplyWithoutHistoryReads => resolve_target(
                &transaction,
                &UnreadableHistorySource,
                rows,
                &record,
                now_micros,
                request,
                &lineage,
            )?,
            TargetResolutionForTest::Metadata | TargetResolutionForTest::Records => {
                let source = if resolution == TargetResolutionForTest::Metadata {
                    CandidateSource::Metadata
                } else {
                    CandidateSource::Records
                };
                resolve_target_from_source_for_test(
                    &transaction,
                    &local_history,
                    rows,
                    &record,
                    now_micros,
                    request,
                    &lineage,
                    source,
                )?
            }
            TargetResolutionForTest::RecordOracle => resolve_target_from_records_for_test(
                &transaction,
                &local_history,
                rows,
                &record,
                now_micros,
                request,
                &lineage,
            )?,
        };
        Ok(target.map(|target| TimeTravelTargetForTest {
            run_attempt: target.point.attempt,
            run_seq: target.point.seq,
            last_event_id: target.last_event_id,
            close_history_message: target.close_history_message,
        }))
    }

    /// A history source whose every read fails, proving a resolution read no history.
    struct UnreadableHistorySource;

    impl HistorySource for UnreadableHistorySource {
        fn history_records(
            &self,
            seqs: RangeInclusive<u64>,
            _batch_records: usize,
        ) -> HistoryRecords<'_> {
            Box::new(std::iter::once(Err(ApplyError::InvariantViolation {
                detail: format!("History records {seqs:?} were read"),
            })))
        }

        fn checkpoint(
            &self,
            attempt: u32,
            seq: u64,
        ) -> Result<Option<CheckpointRecord>, ApplyError> {
            Err(ApplyError::InvariantViolation {
                detail: format!("checkpoint ({attempt}, {seq}) was read"),
            })
        }
    }
}
