// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::row_keys::run_singleton_key;
use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key, run_prefix, run_table_prefix};
use dexzd_protocol::dexzd::v1::{
    AttributeSyncOutboxRecord, HistoryRowMetadata, RunRecord, RunStorageUsage, StepExecutionRecord,
    ValueBirthRecord, ValueGarbageRecord,
};
use prost::Message;

use crate::ApplyError;
use crate::command_handlers::run_cursor::RunCursor;
use crate::state_access::{FlowTransaction, StateRowChange};
use crate::values::value_holder::is_attempt_snapshot_holder_row;

// The RunRecord size can change when its byte totals cross protobuf varint boundaries.
const MAX_RUN_RECORD_SIZE_PASSES: usize = 12;

/// Reconciles storage totals with this command's row writes and deletes.
pub(crate) fn reconcile_run_storage(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
) -> Result<(), ApplyError> {
    cursor.rows.put_run_record(transaction, &cursor.record);
    let prefix = run_prefix(cursor.rows.flow_hash, cursor.rows.run_id);
    let mut changes = transaction.changed_run_rows(&prefix)?;
    let record_key = run_singleton_key(
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        RunTable::RunRecord,
    );
    let record_change = changes
        .iter()
        .position(|change| change.column_family == ColumnFamily::Flow && change.key == record_key)
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: "run storage accounting has no buffered run record".to_owned(),
        })?;
    // Step rows can hold large snapshots, so they are decoded once, not on every pass.
    let attempt_state_delta = attempt_state_delta(&changes)?;
    for _ in 0..MAX_RUN_RECORD_SIZE_PASSES {
        cursor.rows.put_run_record(transaction, &cursor.record);
        changes[record_change].current = Some(cursor.record.encode_to_vec());
        let usage = usage_after_changes(&changes, cursor, attempt_state_delta)?;
        if cursor.record.storage.as_ref() == Some(&usage) {
            return Ok(());
        }
        cursor.record.storage = Some(usage);
    }
    Err(ApplyError::InvariantViolation {
        detail: "run storage accounting did not converge".to_owned(),
    })
}

fn usage_after_changes(
    changes: &[StateRowChange],
    cursor: &RunCursor,
    attempt_state_delta: i128,
) -> Result<RunStorageUsage, ApplyError> {
    let record_key = run_singleton_key(
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        RunTable::RunRecord,
    );
    let committed_usage = changes
        .iter()
        .find(|change| change.column_family == ColumnFamily::Flow && change.key == record_key)
        .and_then(|change| change.previous.as_ref())
        .map(|bytes| decode_record::<RunRecord>(bytes, "run record"))
        .transpose()?
        .and_then(|record| record.storage)
        .unwrap_or_default();
    let mut state_delta = 0_i128;
    let mut history_delta = 0_i128;
    let mut birth_delta = 0_i128;
    let mut garbage_delta = 0_i128;
    for change in changes {
        let parsed =
            parse_run_key(&change.key).map_err(|error| ApplyError::InvariantViolation {
                detail: format!("buffered run key is invalid: {error}"),
            })?;
        match change.column_family {
            ColumnFamily::Flow => {
                let row_delta = if parsed.table == RunTable::AttributeSyncOutbox {
                    accounted_outbox_row_size(change.current.as_ref(), &change.key)?
                        - accounted_outbox_row_size(change.previous.as_ref(), &change.key)?
                } else {
                    row_size(change.current.as_ref(), &change.key)
                        - row_size(change.previous.as_ref(), &change.key)
                };
                match parsed.table {
                    RunTable::ValueBirth => {
                        state_delta += row_delta;
                        birth_delta += record_size::<ValueBirthRecord>(change, "value birth")?;
                    }
                    RunTable::ValueGarbage => {
                        state_delta += row_delta;
                        garbage_delta +=
                            record_size::<ValueGarbageRecord>(change, "value garbage")?;
                    }
                    RunTable::HistoryRowMetadata => {
                        history_delta += row_delta
                            + record_size::<HistoryRowMetadata>(change, "history metadata")?;
                    }
                    // Segment rows describe history, so the run state limit never counts them.
                    RunTable::HistorySegment | RunTable::HistorySegmentValue => {
                        history_delta += row_delta;
                    }
                    _ => state_delta += row_delta,
                }
            }
            _ => {
                return Err(ApplyError::InvariantViolation {
                    detail: "a non-run column family entered storage accounting".to_owned(),
                });
            }
        }
    }
    Ok(RunStorageUsage {
        state_row_bytes: add_delta(committed_usage.state_row_bytes, state_delta)?,
        live_value_bytes: add_delta(
            committed_usage.live_value_bytes,
            birth_delta - garbage_delta,
        )?,
        superseded_value_bytes: add_delta(committed_usage.superseded_value_bytes, garbage_delta)?,
        history_bytes: add_delta(committed_usage.history_bytes, history_delta)?,
        attempt_state_bytes: add_attempt_state_delta(
            committed_usage.attempt_state_bytes,
            attempt_state_delta,
        ),
    })
}

/// Returns the change of attempt state bytes: rows and Step fields that exist only while a
/// method attempt is started or retrying, and that an unchecked command writes.
fn attempt_state_delta(changes: &[StateRowChange]) -> Result<i128, ApplyError> {
    let mut delta = 0_i128;
    for change in changes {
        let parsed =
            parse_run_key(&change.key).map_err(|error| ApplyError::InvariantViolation {
                detail: format!("buffered run key is invalid: {error}"),
            })?;
        delta += match parsed.table {
            RunTable::LockTable => {
                row_size(change.current.as_ref(), &change.key)
                    - row_size(change.previous.as_ref(), &change.key)
            }
            RunTable::ValueHolder if is_attempt_snapshot_holder_row(parsed.suffix) => {
                row_size(change.current.as_ref(), &change.key)
                    - row_size(change.previous.as_ref(), &change.key)
            }
            RunTable::StepExecution => {
                step_attempt_state_size(change.current.as_ref())?
                    - step_attempt_state_size(change.previous.as_ref())?
            }
            _ => 0,
        };
    }
    Ok(delta)
}

fn step_attempt_state_size(value: Option<&Vec<u8>>) -> Result<i128, ApplyError> {
    let Some(bytes) = value else {
        return Ok(0);
    };
    let record = decode_record::<StepExecutionRecord>(bytes, "Step execution")?;
    Ok(i128::from(attempt_scoped_step_bytes(record)))
}

/// Returns the encoded bytes of a Step execution that only its started or retrying attempt
/// needs: the snapshot and the fields that attempt starts and failures write.
fn attempt_scoped_step_bytes(mut record: StepExecutionRecord) -> u64 {
    let full_size = record.encoded_len();
    let Some(invocation) = record.invocation.as_mut() else {
        return 0;
    };
    invocation.snapshot = None;
    invocation.task_id.clear();
    invocation.pull_routing = None;
    invocation.attempt_started_at_micros = 0;
    invocation.retry_started_at_micros = 0;
    invocation.last_failure = None;
    invocation.backoff_fires_at_micros = 0;
    u64::try_from(full_size - record.encoded_len()).expect("encoded sizes fit a u64")
}

/// Projects superseded bytes before final RunRecord storage reconciliation.
pub(crate) fn projected_superseded_value_bytes(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
) -> Result<u64, ApplyError> {
    let prefix = run_table_prefix(
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        RunTable::ValueGarbage,
    );
    let delta = transaction
        .buffered_row_changes_with_prefix(ColumnFamily::Flow, &prefix)?
        .iter()
        .map(|change| record_size::<ValueGarbageRecord>(change, "value garbage"))
        .try_fold(0_i128, |total, size| size.map(|size| total + size))?;
    let committed = cursor.record.storage.unwrap_or_default();
    add_delta(committed.superseded_value_bytes, delta)
}

fn row_size(value: Option<&Vec<u8>>, key: &[u8]) -> i128 {
    value.map_or(0, |value| (key.len() + value.len()) as i128)
}

fn accounted_outbox_row_size(value: Option<&Vec<u8>>, key: &[u8]) -> Result<i128, ApplyError> {
    let Some(bytes) = value else {
        return Ok(0);
    };
    let mut record = decode_record::<AttributeSyncOutboxRecord>(bytes, "Attribute Sync outbox")?;
    let full_record_size = record.encoded_len();
    record.sync_item = None;
    // The snapshot is additional storage; it does not change the established run-state limit.
    Ok((key.len() + bytes.len()) as i128 - (full_record_size - record.encoded_len()) as i128)
}

trait SizedValueRecord: Message + Default {
    fn size_bytes(&self) -> u64;
}

impl SizedValueRecord for ValueBirthRecord {
    fn size_bytes(&self) -> u64 {
        self.size_bytes
    }
}

impl SizedValueRecord for ValueGarbageRecord {
    fn size_bytes(&self) -> u64 {
        self.size_bytes
    }
}

impl SizedValueRecord for HistoryRowMetadata {
    fn size_bytes(&self) -> u64 {
        self.row_size_bytes
    }
}

fn record_size<Record: SizedValueRecord>(
    change: &StateRowChange,
    table: &'static str,
) -> Result<i128, ApplyError> {
    let old_size = change
        .previous
        .as_ref()
        .map(|bytes| decode_record::<Record>(bytes, table))
        .transpose()?
        .map_or(0, |record| i128::from(record.size_bytes()));
    let new_size = change
        .current
        .as_ref()
        .map(|bytes| decode_record::<Record>(bytes, table))
        .transpose()?
        .map_or(0, |record| i128::from(record.size_bytes()));
    Ok(new_size - old_size)
}

fn decode_record<Record: Message + Default>(
    bytes: &[u8],
    table: &'static str,
) -> Result<Record, ApplyError> {
    Record::decode(bytes).map_err(|error| ApplyError::InvariantViolation {
        detail: format!("stored {table} row is corrupt: {error}"),
    })
}

fn add_delta(current: u64, delta: i128) -> Result<u64, ApplyError> {
    u64::try_from(i128::from(current) + delta).map_err(|_| ApplyError::InvariantViolation {
        detail: "run storage byte count overflowed or became negative".to_owned(),
    })
}

/// Older records omit attempt bytes; attempts concurrent with theirs may each escape one check.
/// Exact once they end.
fn add_attempt_state_delta(current: u64, delta: i128) -> u64 {
    let sum = (i128::from(current) + delta).clamp(0, i128::from(u64::MAX));
    u64::try_from(sum).expect("the sum is clamped to the u64 range")
}

/// Returns whether the run's committed active state already exceeds its state budget.
pub(crate) fn is_run_over_state_budget(record: &RunRecord) -> bool {
    let max_run_state_bytes = record.settings.unwrap_or_default().max_run_state_bytes;
    record
        .storage
        .as_ref()
        .is_some_and(|usage| active_run_storage_bytes(usage) > max_run_state_bytes)
}

/// Returns the run's current storage without attempt state, which unchecked commands write and
/// method ends release. The run state limit compares it before and after a command.
pub(crate) fn active_run_storage_bytes_without_attempts(usage: &RunStorageUsage) -> u64 {
    active_run_storage_bytes(usage).saturating_sub(usage.attempt_state_bytes)
}

/// Returns the run's active state, which the run state limit compares with its maximum.
///
/// It is the bytes of the current state rows plus the values they hold. History records,
/// checkpoints, and superseded values are not counted. The state rows still include the small
/// ValueBirth row of each table value and the ValueGarbage row of each superseded value, so
/// existing runs need no recount.
pub(crate) fn active_run_storage_bytes(usage: &RunStorageUsage) -> u64 {
    usage.state_row_bytes.saturating_add(usage.live_value_bytes)
}
