// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Replays a log that a binary without attempt hand-out wrote (plan section 3.9). Its
//! heartbeats and results carry no task ID and its failures no guard, so apply takes the
//! older-log path: the replay must reach the older binary's state, except for the attempt
//! fields that only the new binary writes.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use dexzd_apply::read_model::{StepCompletionState, step_completion_state};
use dexzd_apply::{FlowStateReader, StateReadError, StateRow};
use dexzd_engine::SystemEngineIdSource;
use dexzd_keyspace::row_keys::{history_record_key, value_row_key};
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, ValueReference, parse_run_key};
use dexzd_protocol::dex::flow_started_or_continued_history_event::StartOrContinue;
use dexzd_protocol::dex::{
    AttributeWrite, ChannelMessage, FlowStatus, Kv, Value, flow_history_event, value,
};
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, HistoryRecord, RunRecord, StepExecutionPhase, StepExecutionRecord,
    StepInvocationPhase, ValueRecord,
};
use prost::Message;

use crate::compat_node::{engine_node_config, start_engine_node};
use crate::format_decoding::decode_objects;
use crate::golden_replay::{RESTART_DELAY_MICROS, SnapshotUse, forget_snapshots};
use crate::scripted_clock::ScriptedClock;
use crate::state_dump::{StateDump, dump_committed_state, dumped_value, hex, parse_state_dump};
use crate::testdata_tree::{
    OBJECTS_DIRECTORY, STATE_DUMP_FILE, load_objects, read_files, testdata_root,
};

/// The format-compat log that the last binary before attempt hand-out recorded.
const OLDER_LOG_DIRECTORY: &str = "pre-attempt-handout-log";
/// Full values of the recorded run records, which `state.txt` only hashes.
const RUN_RECORDS_FILE: &str = "run_records.txt";

/// Full values of the older binary's StepExecution and Checkpoint rows, which `state.txt` only
/// hashes; see the file's header.
const OLDER_STEP_AND_CHECKPOINT_ROWS: &str = include_str!("pre_attempt_handout_rows.txt");

/// Committed rows by column family name and key, as stored.
pub(crate) type StoredRows = BTreeMap<(String, Vec<u8>), Vec<u8>>;

/// Rows of the older binary by column family name and hex key, as `state.txt` names them.
type OlderRows = BTreeMap<(String, String), Vec<u8>>;

#[tokio::test]
async fn a_log_without_task_ids_or_failure_guards_replays_to_the_older_state() {
    for snapshot_use in [SnapshotUse::Restore, SnapshotUse::Ignore] {
        let directory = testdata_root().join(OLDER_LOG_DIRECTORY);
        let recorded_text = std::fs::read_to_string(directory.join(STATE_DUMP_FILE))
            .expect("read the older state dump");
        let recorded = parse_state_dump(&recorded_text);
        let recorded_run_records = read_run_records(&directory);
        let older_rows = read_older_rows(&recorded);
        let (replayed, stored) = replay(&directory, snapshot_use).await;
        // Rows that the restored snapshot holds are not rewritten, so only a full replay deletes
        // every eligible row.
        let deletion = if snapshot_use == SnapshotUse::Ignore {
            ExpectedDeletion::EveryEligibleRow
        } else {
            ExpectedDeletion::OnlyEligibleRows
        };
        let deleted_step_rows = deleted_ended_step_rows(
            &recorded,
            &replayed,
            &older_rows,
            &recorded_run_records,
            &stored,
            deletion,
        )
        .unwrap_or_else(|error| panic!("{snapshot_use:?}: {error}"));
        assert!(
            !deleted_step_rows.is_empty(),
            "{snapshot_use:?}: the older binary kept rows of completed Steps"
        );
        assert_eq!(
            recorded
                .keys()
                .filter(|key| !deleted_step_rows.contains(*key))
                .collect::<Vec<_>>(),
            replayed.keys().collect::<Vec<_>>(),
            "{snapshot_use:?}: the replay writes and deletes the same rows"
        );
        let changed_rows: Vec<_> = recorded
            .iter()
            .filter(|(key, _)| !deleted_step_rows.contains(*key))
            .filter(|(key, value)| replayed.get(*key) != Some(*value))
            .filter(|((column_family, key), value)| {
                !matches_without_new_fields(
                    column_family,
                    key,
                    value,
                    &recorded_run_records,
                    &older_rows,
                    &stored,
                )
            })
            .map(|(key, _)| key)
            .collect();
        assert!(
            changed_rows.is_empty(),
            "{snapshot_use:?}: only the new attempt fields and their byte counts differ: \
             {changed_rows:?}"
        );
        let step_executions = decoded_rows::<StepExecutionRecord>(&stored, RunTable::StepExecution);
        assert!(!step_executions.is_empty());
        for step in &step_executions {
            assert!(
                step.invocation.as_ref().is_none_or(|invocation| {
                    invocation.phase != StepInvocationPhase::Running as i32
                }),
                "{snapshot_use:?}: every recorded result applied, so {} is not running",
                step.step_execution_id
            );
        }
        let runs = decoded_rows::<RunRecord>(&stored, RunTable::RunRecord);
        assert!(!runs.is_empty());
        for run in &runs {
            let settings = run.settings.expect("every run records its settings");
            assert_eq!(
                (
                    settings.checkpoint_command_threshold,
                    settings.checkpoint_row_write_bytes_threshold
                ),
                (10_000, 1024 * 1024),
                "{snapshot_use:?}: run {} keeps the checkpoint thresholds the older binary recorded",
                run.flow_id
            );
        }
        // An older time travel restored a running attempt as Running; its result without a
        // task ID then completed the Flow.
        let travel_runs: Vec<_> = runs
            .into_iter()
            .filter(|run| run.flow_type == "TravelRunningFlow")
            .collect();
        assert_eq!(travel_runs.len(), 1, "{snapshot_use:?}: one travel run");
        assert_eq!(travel_runs[0].current_attempt, 2);
        assert_eq!(travel_runs[0].status, FlowStatus::Completed as i32);
    }
}

/// Each oracle must reject a replay that deletes a row it must keep or changes a checkpoint.
#[tokio::test]
async fn the_older_log_oracles_reject_wrong_deletions_and_checkpoint_changes() {
    let directory = testdata_root().join(OLDER_LOG_DIRECTORY);
    let recorded = parse_state_dump(
        &std::fs::read_to_string(directory.join(STATE_DUMP_FILE))
            .expect("read the older state dump"),
    );
    let recorded_run_records = read_run_records(&directory);
    let older_rows = read_older_rows(&recorded);
    let (replayed, stored) = replay(&directory, SnapshotUse::Restore).await;
    let deleted = deleted_ended_step_rows(
        &recorded,
        &replayed,
        &older_rows,
        &recorded_run_records,
        &stored,
        ExpectedDeletion::OnlyEligibleRows,
    )
    .expect("the replay deletes only eligible rows");

    let mut kept_phases = BTreeSet::new();
    for (row_key, older_value) in &older_rows {
        let (parsed_key, step) = match older_step_row(row_key, older_value) {
            Some(row) => row,
            None => continue,
        };
        let parsed = parse_run_key(&parsed_key).expect("pinned keys parse");
        if deleted.contains(row_key)
            || is_expected_deletion(&step, parsed.flow_hash, parsed.run_id, &stored)
        {
            continue;
        }
        kept_phases.insert(step.phase);
        let mut wrongly_deleted = replayed.clone();
        wrongly_deleted.remove(row_key);
        let mut wrongly_stored = stored.clone();
        wrongly_stored.remove(&(row_key.0.clone(), parsed_key));
        assert!(
            deleted_ended_step_rows(
                &recorded,
                &wrongly_deleted,
                &older_rows,
                &recorded_run_records,
                &wrongly_stored,
                ExpectedDeletion::OnlyEligibleRows,
            )
            .is_err(),
            "deleting kept {} in phase {} must fail the oracle",
            step.step_execution_id,
            step.phase
        );
    }
    assert!(
        kept_phases.contains(&(StepExecutionPhase::Canceled as i32)),
        "the fixture keeps a Step that a Flow close canceled: {kept_phases:?}"
    );

    let (checkpoint_key, older_checkpoint) = older_rows
        .iter()
        .find(|((column_family, hex_key), older)| {
            let key = decode_hex(hex_key);
            column_family == ColumnFamily::History.name()
                && checkpoint_without_dropped_eligible_rows(
                    &key,
                    &stored[&(column_family.clone(), key.clone())],
                    older,
                    &stored,
                ) != **older
        })
        .expect("a TimeTravel checkpoint restored rows that newer binaries delete");
    let key = decode_hex(&checkpoint_key.1);
    let replayed_checkpoint = &stored[&(checkpoint_key.0.clone(), key.clone())];
    let mut without_step_row =
        CheckpointRecord::decode(replayed_checkpoint.as_slice()).expect("checkpoint rows decode");
    let kept_step_row = without_step_row
        .rows
        .iter()
        .position(|row| row.table == RunTable::StepExecution as u32)
        .expect("the checkpoint keeps a Step row");
    without_step_row.rows.remove(kept_step_row);
    assert!(
        !checkpoint_matches_older(
            &key,
            &without_step_row.encode_to_vec(),
            older_checkpoint,
            &stored
        ),
        "dropping a Step row that newer binaries keep must fail the comparison"
    );
    assert!(checkpoint_matches_older(
        &key,
        replayed_checkpoint,
        older_checkpoint,
        &stored
    ));
    let mut without_other_row =
        CheckpointRecord::decode(replayed_checkpoint.as_slice()).expect("checkpoint rows decode");
    let other_row = without_other_row
        .rows
        .iter()
        .position(|row| row.table != RunTable::StepExecution as u32)
        .expect("the checkpoint holds a row besides Step executions");
    without_other_row.rows.remove(other_row);
    assert!(!checkpoint_matches_older(
        &key,
        &without_other_row.encode_to_vec(),
        older_checkpoint,
        &stored
    ));
    let mut changed_status =
        CheckpointRecord::decode(replayed_checkpoint.as_slice()).expect("checkpoint rows decode");
    changed_status.run_status.get_or_insert_default().status = FlowStatus::Terminated as i32;
    assert!(!checkpoint_matches_older(
        &key,
        &changed_status.encode_to_vec(),
        older_checkpoint,
        &stored
    ));
}

/// Reads the pinned older rows; each must hash to its `state.txt` value, and none may be missing.
fn read_older_rows(recorded: &StateDump) -> OlderRows {
    let mut older_rows = OlderRows::new();
    for line in OLDER_STEP_AND_CHECKPOINT_ROWS
        .lines()
        .filter(|line| !line.starts_with('#'))
    {
        let mut fields = line.split(' ');
        let (Some(column_family), Some(hex_key), Some(hex_value), None) =
            (fields.next(), fields.next(), fields.next(), fields.next())
        else {
            panic!("a pinned row line has a column family, a key, and a value");
        };
        let row_key = (column_family.to_owned(), hex_key.to_owned());
        let value = decode_hex(hex_value);
        assert_eq!(
            recorded.get(&row_key),
            Some(&dumped_value(&value)),
            "the pinned row {row_key:?} is the row that state.txt records"
        );
        older_rows.insert(row_key, value);
    }
    for (column_family, hex_key) in recorded.keys() {
        let table = parse_run_key(&decode_hex(hex_key)).map(|parsed| parsed.table);
        let is_pinned_table = matches!(
            (column_family.as_str(), table),
            ("flow", Ok(RunTable::StepExecution)) | ("history", Ok(RunTable::Checkpoint))
        );
        if is_pinned_table {
            assert!(
                older_rows.contains_key(&(column_family.clone(), hex_key.clone())),
                "the recorded row {column_family} {hex_key} is pinned"
            );
        }
    }
    older_rows
}

/// Returns the key and decoded row of a pinned StepExecution row, or None for another row.
fn older_step_row(
    (column_family, hex_key): &(String, String),
    value: &[u8],
) -> Option<(Vec<u8>, StepExecutionRecord)> {
    if column_family != ColumnFamily::Flow.name() {
        return None;
    }
    let key = decode_hex(hex_key);
    let is_step_row =
        parse_run_key(&key).is_ok_and(|parsed| parsed.table == RunTable::StepExecution);
    is_step_row.then(|| {
        let step = StepExecutionRecord::decode(value).expect("pinned Step rows decode");
        (key, step)
    })
}

/// Which eligible older Step rows a replay must have deleted.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ExpectedDeletion {
    /// Every eligible row, because the replay rewrote every row.
    EveryEligibleRow,
    /// Some eligible rows: rows a restored snapshot holds stay until a command rewrites them.
    OnlyEligibleRows,
}

/// Returns the recorded Step execution rows that the replay deleted.
///
/// Returns an error when the replay deleted an older row that newer binaries keep, when a
/// deleted row did not answer Completed in the older state and the replayed one, or when
/// `deletion` requires every eligible row deleted and one remains.
fn deleted_ended_step_rows(
    recorded: &StateDump,
    replayed: &StateDump,
    older_rows: &OlderRows,
    recorded_run_records: &BTreeMap<String, Vec<u8>>,
    stored: &StoredRows,
    deletion: ExpectedDeletion,
) -> Result<BTreeSet<(String, String)>, String> {
    let mut deleted = BTreeSet::new();
    let mut expected = BTreeSet::new();
    for (row_key, older_value) in older_rows {
        let Some((key, step)) = older_step_row(row_key, older_value) else {
            continue;
        };
        if !recorded.contains_key(row_key) {
            continue;
        }
        let parsed = parse_run_key(&key).expect("pinned keys parse");
        if is_expected_deletion(&step, parsed.flow_hash, parsed.run_id, stored) {
            expected.insert(row_key.clone());
        }
        if replayed.contains_key(row_key) {
            continue;
        }
        deleted.insert(row_key.clone());
        let (step_type, number) = step
            .step_execution_id
            .rsplit_once('-')
            .expect("a Step execution ID ends with its number");
        let number: u32 = number.parse().expect("a Step execution number");
        let run_record_key = hex(&dexzd_keyspace::row_keys::run_singleton_key(
            parsed.flow_hash,
            parsed.run_id,
            RunTable::RunRecord,
        ));
        let older_run_record = recorded_run_records
            .get(&run_record_key)
            .ok_or_else(|| format!("no recorded run record for {}", step.step_execution_id))?;
        let older_reader = OverlayReader {
            stored,
            overlay: BTreeMap::from([
                (key.clone(), older_value.clone()),
                (decode_hex(&run_record_key), older_run_record.clone()),
            ]),
        };
        let replayed_reader = OverlayReader {
            stored,
            overlay: BTreeMap::new(),
        };
        for (state, reader) in [("older", &older_reader), ("replayed", &replayed_reader)] {
            let answer =
                step_completion_state(reader, parsed.flow_hash, parsed.run_id, step_type, number)
                    .map_err(|error| format!("{} reads: {error}", step.step_execution_id))?;
            if answer != StepCompletionState::Completed {
                return Err(format!(
                    "{} answers {answer:?} in the {state} state",
                    step.step_execution_id
                ));
            }
        }
    }
    let unexpected: Vec<_> = deleted.difference(&expected).collect();
    if !unexpected.is_empty() {
        return Err(format!(
            "the replay deleted rows it must keep: {unexpected:?}"
        ));
    }
    let remaining: Vec<_> = expected.difference(&deleted).collect();
    if deletion == ExpectedDeletion::EveryEligibleRow && !remaining.is_empty() {
        return Err(format!("the replay kept eligible rows: {remaining:?}"));
    }
    Ok(deleted)
}

/// Whether newer binaries delete this older row: it completed, or a cancel without FlowClosed ended it.
fn is_expected_deletion(
    step: &StepExecutionRecord,
    flow_hash: FlowHash,
    run_id: RunId,
    stored: &StoredRows,
) -> bool {
    if step.is_timeout_handler {
        return false;
    }
    match StepExecutionPhase::try_from(step.phase) {
        Ok(StepExecutionPhase::Completed) => true,
        Ok(StepExecutionPhase::Canceled) => {
            let key = history_record_key(flow_hash, run_id, step.completed_seq);
            let record = stored
                .get(&(ColumnFamily::History.name().to_owned(), key))
                .map(|bytes| HistoryRecord::decode(bytes.as_slice()).expect("history rows decode"));
            record.is_some_and(|record| {
                !record.events.iter().any(|event| {
                    matches!(
                        event.payload,
                        Some(flow_history_event::Payload::FlowClosed(_))
                    )
                })
            })
        }
        _ => false,
    }
}

/// Returns `older` without the eligible Step rows that the `replayed` checkpoint lacks.
fn checkpoint_without_dropped_eligible_rows(
    key: &[u8],
    replayed: &[u8],
    older: &[u8],
    stored: &StoredRows,
) -> Vec<u8> {
    let parsed = parse_run_key(key).expect("checkpoint keys parse");
    let replayed_step_keys: BTreeSet<Vec<u8>> = CheckpointRecord::decode(replayed)
        .expect("checkpoint rows decode")
        .rows
        .into_iter()
        .filter(|row| row.table == RunTable::StepExecution as u32)
        .map(|row| row.key_suffix)
        .collect();
    let mut checkpoint = CheckpointRecord::decode(older).expect("checkpoint rows decode");
    checkpoint.rows.retain(|row| {
        row.table != RunTable::StepExecution as u32
            || replayed_step_keys.contains(&row.key_suffix)
            || !is_expected_deletion(
                &StepExecutionRecord::decode(row.value.as_slice()).expect("Step rows decode"),
                parsed.flow_hash,
                parsed.run_id,
                stored,
            )
    });
    checkpoint.encode_to_vec()
}

/// Whether the replayed checkpoint equals the older one minus dropped eligible Step rows, ignoring newer fields.
fn checkpoint_matches_older(
    key: &[u8],
    replayed: &[u8],
    older: &[u8],
    stored: &StoredRows,
) -> bool {
    let expected = checkpoint_without_dropped_eligible_rows(key, replayed, older, stored);
    checkpoint_without_new_fields(replayed) == checkpoint_without_new_fields(&expected)
}

fn checkpoint_without_new_fields(value: &[u8]) -> Vec<u8> {
    let mut checkpoint = CheckpointRecord::decode(value).expect("checkpoint rows decode");
    for row in &mut checkpoint.rows {
        if row.table == RunTable::StepExecution as u32 {
            row.value = step_execution_without_new_fields(&row.value);
        }
    }
    checkpoint.encode_to_vec()
}

/// Reads the rows that a replay committed, with `overlay` rows of the Flow column family in place.
struct OverlayReader<'rows> {
    stored: &'rows StoredRows,
    overlay: BTreeMap<Vec<u8>, Vec<u8>>,
}

impl FlowStateReader for OverlayReader<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        if column_family == ColumnFamily::Flow
            && let Some(value) = self.overlay.get(key)
        {
            return Ok(Some(value.clone()));
        }
        Ok(self
            .stored
            .get(&(column_family.name().to_owned(), key.to_vec()))
            .cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let name = column_family.name().to_owned();
        let mut rows: BTreeMap<Vec<u8>, Vec<u8>> = self
            .stored
            .range((name.clone(), start.to_vec())..(name, end.to_vec()))
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect();
        if column_family == ColumnFamily::Flow {
            rows.extend(
                self.overlay
                    .range(start.to_vec()..end.to_vec())
                    .map(|(key, value)| (key.clone(), value.clone())),
            );
        }
        Ok(rows.into_iter().take(limit).collect())
    }
}

/// Returns whether the replayed row equals the recorded one after clearing the fields that only
/// the new binary writes: the attempt progress and the flow close marker on Step executions
/// and in attempt checkpoints.
/// Run records are compared without their byte counters, which count the bytes of those
/// fields. History row metadata, which records checkpoint sizes, is not compared.
fn matches_without_new_fields(
    column_family: &str,
    hex_key: &str,
    recorded_value: &str,
    recorded_run_records: &BTreeMap<String, Vec<u8>>,
    older_rows: &OlderRows,
    stored: &StoredRows,
) -> bool {
    let key = decode_hex(hex_key);
    let Ok(parsed) = parse_run_key(&key) else {
        return false;
    };
    let Some(value) = stored.get(&(column_family.to_owned(), key.clone())) else {
        return false;
    };
    let cleared = match (column_family, parsed.table) {
        (name, RunTable::StepExecution) if name == ColumnFamily::Flow.name() => {
            step_execution_without_new_fields(value)
        }
        (name, RunTable::Checkpoint) if name == ColumnFamily::History.name() => {
            let Some(older) = older_rows.get(&(column_family.to_owned(), hex_key.to_owned()))
            else {
                return false;
            };
            return checkpoint_matches_older(&key, value, older, stored);
        }
        (name, RunTable::HistoryRecord) if name == ColumnFamily::History.name() => {
            history_without_scheduled_time(&history_with_inline_effect_values(
                value,
                parsed.flow_hash,
                parsed.run_id,
                stored,
            ))
        }
        (name, RunTable::RunRecord) if name == ColumnFamily::Flow.name() => {
            let Some(recorded_run) = recorded_run_records.get(hex_key) else {
                return false;
            };
            return run_record_without_storage(recorded_run) == run_record_without_storage(value);
        }
        (name, RunTable::HistoryRowMetadata) if name == ColumnFamily::Flow.name() => {
            return true;
        }
        _ => return false,
    };
    dumped_value(&cleared) == recorded_value
}

/// Resolves the effect value references that newer binaries write into history events, as the
/// older binary recorded the complete values.
fn history_with_inline_effect_values(
    value: &[u8],
    flow_hash: FlowHash,
    run_id: RunId,
    stored: &StoredRows,
) -> Vec<u8> {
    let mut record = HistoryRecord::decode(value).expect("history rows decode");
    for event in &mut record.events {
        let effect_values: Vec<&mut Option<Value>> = match event.payload.as_mut() {
            Some(flow_history_event::Payload::StepWaitForCompleted(completed)) => completed
                .output
                .as_mut()
                .map(|output| {
                    effect_value_slots(
                        &mut output.upsert_attributes,
                        &mut output.publish_to_channel,
                        &mut output.upsert_step_execution_locals,
                    )
                })
                .unwrap_or_default(),
            Some(flow_history_event::Payload::StepExecuteCompleted(completed)) => completed
                .output
                .as_mut()
                .map(|output| {
                    effect_value_slots(
                        &mut output.upsert_attributes,
                        &mut output.publish_to_channel,
                        &mut output.upsert_step_execution_locals,
                    )
                })
                .unwrap_or_default(),
            Some(flow_history_event::Payload::RpcExecutionCompleted(completed)) => {
                effect_value_slots(
                    &mut completed.upsert_attributes,
                    &mut completed.publish_to_channel,
                    &mut [],
                )
            }
            Some(flow_history_event::Payload::ChannelExternalPublish(publish)) => {
                effect_value_slots(&mut [], &mut publish.messages, &mut [])
            }
            Some(flow_history_event::Payload::FlowStartedOrContinued(started)) => {
                match started.start_or_continue.as_mut() {
                    Some(StartOrContinue::InitialStart(start)) => {
                        effect_value_slots(&mut [], &mut [], &mut start.initial_attributes)
                    }
                    _ => Vec::new(),
                }
            }
            _ => Vec::new(),
        };
        for slot in effect_values {
            let Some(reference) = slot.as_ref().and_then(value_reference_text) else {
                continue;
            };
            let reference: ValueReference = reference.parse().expect("history references parse");
            assert_eq!(reference.run_id, run_id, "history references its own run");
            let key = value_row_key(flow_hash, run_id, RunTable::Value, reference.value_id);
            let row = stored
                .get(&(ColumnFamily::Value.name().to_owned(), key))
                .expect("a referenced value is stored");
            *slot = ValueRecord::decode(row.as_slice())
                .expect("value rows decode")
                .value;
        }
    }
    record.encode_to_vec()
}

fn effect_value_slots<'event>(
    writes: &'event mut [AttributeWrite],
    messages: &'event mut [ChannelMessage],
    key_values: &'event mut [Kv],
) -> Vec<&'event mut Option<Value>> {
    writes
        .iter_mut()
        .map(|write| &mut write.value)
        .chain(messages.iter_mut().map(|message| &mut message.value))
        .chain(key_values.iter_mut().map(|key_value| &mut key_value.value))
        .collect()
}

fn value_reference_text(value: &Value) -> Option<&str> {
    match value.kind.as_ref() {
        Some(
            value::Kind::InternalBlobIdForStringValue(reference)
            | value::Kind::InternalBlobIdForObjValue(reference),
        ) => Some(reference),
        _ => None,
    }
}

/// Clears the Step method event field that newer binaries add to history.
fn history_without_scheduled_time(value: &[u8]) -> Vec<u8> {
    let mut record = HistoryRecord::decode(value).expect("history rows decode");
    for event in &mut record.events {
        let context = match event.payload.as_mut() {
            Some(
                flow_history_event::Payload::StepWaitForPending(pending)
                | flow_history_event::Payload::StepExecutePending(pending),
            ) => pending.context.as_mut(),
            Some(flow_history_event::Payload::StepWaitForCompleted(completed)) => {
                completed.context.as_mut()
            }
            Some(flow_history_event::Payload::StepWaitForFailed(failed)) => failed.context.as_mut(),
            Some(flow_history_event::Payload::StepExecuteCompleted(completed)) => {
                completed.context.as_mut()
            }
            Some(flow_history_event::Payload::StepExecuteFailed(failed)) => failed.context.as_mut(),
            _ => None,
        };
        if let Some(context) = context {
            context.scheduled_time = None;
        }
    }
    record.encode_to_vec()
}

/// Decodes a run record without its byte counters: storage usage and checkpoint byte counts.
///
/// It also clears the window event counters, which the older binary did not write. A run that
/// counts events counts at least one per semantic record.
fn run_record_without_storage(value: &[u8]) -> RunRecord {
    let mut run = RunRecord::decode(value).expect("run record rows decode");
    run.storage = None;
    run.row_write_bytes_since_checkpoint = 0;
    run.last_checkpoint_state_bytes = 0;
    if let Some(window_event_count) = run.window_event_count {
        assert!(window_event_count >= run.semantic_event_count);
        assert!(run.pruned_window_event_count >= run.pruned_semantic_event_count);
        assert!(run.pruned_window_event_count <= window_event_count);
    }
    run.window_event_count = None;
    run.pruned_window_event_count = 0;
    run
}

/// Reads the recorded run records: one `<key hex> <value hex>` line each, after a comment line.
fn read_run_records(directory: &std::path::Path) -> BTreeMap<String, Vec<u8>> {
    std::fs::read_to_string(directory.join(RUN_RECORDS_FILE))
        .expect("read the recorded run records")
        .lines()
        .filter(|line| !line.starts_with('#'))
        .map(|line| {
            let (key, value) = line
                .split_once(' ')
                .expect("a run record line has a key and a value");
            (key.to_owned(), decode_hex(value))
        })
        .collect()
}

fn step_execution_without_new_fields(value: &[u8]) -> Vec<u8> {
    let mut step = StepExecutionRecord::decode(value).expect("Step execution rows decode");
    if let Some(invocation) = step.invocation.as_mut() {
        invocation.furthest_attempt_progress = 0;
        invocation.pending_since_micros = 0;
    }
    if let Some(recovery_error) = step.recovery_error.as_mut() {
        recovery_error.attempt_progress = 0;
    }
    step.is_canceled_by_flow_close = None;
    step.encode_to_vec()
}

fn decoded_rows<T: Message + Default>(stored: &StoredRows, table: RunTable) -> Vec<T> {
    stored
        .iter()
        .filter(|((column_family, key), _)| {
            *column_family == table.column_family().name()
                && parse_run_key(key).is_ok_and(|parsed| parsed.table == table)
        })
        .map(|(_, value)| T::decode(value.as_slice()).expect("rows decode"))
        .collect()
}

fn decode_hex(hex_key: &str) -> Vec<u8> {
    (0..hex_key.len())
        .step_by(2)
        .map(|index| u8::from_str_radix(&hex_key[index..index + 2], 16).expect("hex key"))
        .collect()
}

/// Starts a node on the older objects and returns its committed state dump and stored rows.
async fn replay(directory: &std::path::Path, snapshot_use: SnapshotUse) -> (StateDump, StoredRows) {
    replay_with_checks(directory, snapshot_use, true).await
}

/// Starts a node on the recorded objects, with or without the debug replay checks, and returns
/// its committed state dump and stored rows.
pub(crate) async fn replay_with_checks(
    directory: &std::path::Path,
    snapshot_use: SnapshotUse,
    debug_replay_checks: bool,
) -> (StateDump, StoredRows) {
    let decoded = decode_objects(&read_files(&directory.join(OBJECTS_DIRECTORY)));
    let object_store = Arc::new(load_objects(directory).await);
    if snapshot_use == SnapshotUse::Ignore {
        forget_snapshots(object_store.as_ref(), decoded.manifest.clone()).await;
    }
    let node = decoded
        .manifest
        .nodes
        .values()
        .next()
        .expect("the manifest names the recording node");
    let last_command_micros = decoded
        .commands
        .iter()
        .map(|command| command.proposed_at_micros)
        .max()
        .expect("the log holds commands");
    let clock = Arc::new(ScriptedClock::starting_at(
        last_command_micros + RESTART_DELAY_MICROS,
    ));
    let mut config = engine_node_config(
        &node.node_id,
        node.incarnation,
        decoded.cluster.partition_count,
    );
    config.commit.debug_replay_checks = debug_replay_checks;
    let engine =
        start_engine_node(&config, object_store, clock, Arc::new(SystemEngineIdSource)).await;
    let view = engine
        .committer()
        .committed_view()
        .expect("open a committed view");
    let row_view = engine
        .committer()
        .committed_view()
        .expect("open a committed view");
    let state = dump_committed_state(view).await;
    let stored = tokio::task::spawn_blocking(move || {
        let mut stored = StoredRows::new();
        for column_family in [
            ColumnFamily::Flow,
            ColumnFamily::History,
            ColumnFamily::Value,
        ] {
            let rows = row_view
                .scan(column_family, &[], &[0xff; 64], usize::MAX)
                .expect("scan committed rows");
            for (key, value) in rows {
                stored.insert((column_family.name().to_owned(), key), value);
            }
        }
        stored
    })
    .await
    .expect("the scan task completes");
    engine
        .shutdown()
        .await
        .expect("the node releases its lease");
    (state, stored)
}
