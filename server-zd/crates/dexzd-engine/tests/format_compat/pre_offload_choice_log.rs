// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Replays the log of `testdata/pre-offload-choice-log`, which server-zd at `e2e451513` recorded
//! before runs chose history offload and before apply deleted or swept the rows of ended Steps.
//! Its `travel-flow` TimeTravel stages a result that still holds the rows of completed Steps.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::row_keys::{
    checkpoint_key, history_row_metadata_key, run_named_key, run_singleton_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, parse_run_key};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, EndedStepRowSweep, HistoryRowMetadata, RunRecord, RunStorageUsage,
    StepExecutionPhase, StepExecutionRecord,
};
use prost::Message;

use crate::format_decoding::memory_snapshot_rows;
use crate::golden_replay::SnapshotUse;
use crate::old_log_replay::{
    ExpectedDeletion, OlderRows, StoredRows, decode_hex, deleted_ended_step_rows,
    is_expected_deletion, read_pinned_rows, replay_with_checks,
};
use crate::older_backend_error::replay_with_older_backend_errors;
use crate::removed_flow_config_fields::{
    RemovedFlowConfigFields, history_paths, removed_fields_in, run_record_paths, with_fields,
    with_kept_removed_fields,
};
use crate::state_dump::{StateDump, dumped_value, hex, parse_state_dump};
use crate::testdata_tree::{OBJECTS_DIRECTORY, STATE_DUMP_FILE, read_files, testdata_root};

/// A log that server-zd at `e2e451513` recorded; see its README.
const OFFLOAD_CHOICE_LOG_DIRECTORY: &str = "pre-offload-choice-log";
/// Full values of the recorded RunRecord, StepExecution, and Checkpoint rows, which `state.txt`
/// only hashes; see the file's header.
const RECORDED_ROWS: &str = include_str!("pre_offload_choice_rows.txt");
/// Step rows that apply's ended Step row sweep visits in one command; every run of the log holds
/// fewer, so one command completes its sweep.
const SWEEP_ROWS_PER_COMMAND: usize = 256;

/// Rows of the recorded snapshot by column family name and key.
type SnapshotRows = BTreeMap<(String, Vec<u8>), Vec<u8>>;

#[tokio::test]
async fn an_older_staged_time_travel_passes_the_debug_replay_checks() {
    let directory = testdata_root().join(OFFLOAD_CHOICE_LOG_DIRECTORY);
    for snapshot_use in [SnapshotUse::Restore, SnapshotUse::Ignore] {
        let (checked, stored) = replay_with_checks(&directory, snapshot_use, true).await;
        let (unchecked, _) = replay_with_checks(&directory, snapshot_use, false).await;
        assert!(
            checked == unchecked,
            "{snapshot_use:?}: the debug replay checks stop no partition"
        );
        let travel_run = stored
            .iter()
            .filter(|((column_family, key), _)| {
                column_family == ColumnFamily::Flow.name()
                    && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::RunRecord)
            })
            .map(|(_, value)| RunRecord::decode(value.as_slice()).expect("run records decode"))
            .find(|run| run.flow_id == "travel-flow")
            .expect("the travel run exists");
        assert_eq!(
            (travel_run.current_attempt, travel_run.status),
            (2, FlowStatus::Completed as i32),
            "{snapshot_use:?}: the time travel forked attempt 2, which completed"
        );
    }
}

/// Every command of the log applies as the binary that recorded it applied it, from the snapshot
/// and from the start of the log, with the debug replay checks on. The replayed state equals the
/// recorded state row for row, except for what later apply changes compute from recorded rows:
/// the rows of ended Steps that apply deletes since #1429, the checkpoints and byte counters
/// without them, and the prune cursors that apply maintains since #1427.
/// Failure text is compared as the recording binary wrote it; see `older_backend_error`. So are
/// the FlowConfig fields that #1072 removed; see `removed_flow_config_fields`.
#[tokio::test]
async fn the_log_recorded_before_runs_chose_offload_replays_to_its_recorded_state() {
    let directory = testdata_root().join(OFFLOAD_CHOICE_LOG_DIRECTORY);
    let log = read_recorded_log();
    for snapshot_use in [SnapshotUse::Restore, SnapshotUse::Ignore] {
        let (replayed, stored) = replay_with_older_backend_errors(&directory, snapshot_use).await;
        log.compare(&replayed, &stored, snapshot_use)
            .unwrap_or_else(|error| panic!("{snapshot_use:?}: {error}"));
    }
}

/// The comparison must reject a replay that keeps a recorded value where later apply derives a
/// new one: a stale checkpoint size in history row metadata, or a stale run record counter.
#[tokio::test]
async fn the_offload_choice_comparison_rejects_stale_derived_values() {
    let directory = testdata_root().join(OFFLOAD_CHOICE_LOG_DIRECTORY);
    let log = read_recorded_log();
    let (replayed, stored) =
        replay_with_older_backend_errors(&directory, SnapshotUse::Ignore).await;
    log.compare(&replayed, &stored, SnapshotUse::Ignore)
        .expect("the unchanged replay matches");
    let deleted = deleted_ended_step_rows(
        &log.recorded,
        &replayed,
        &log.recorded_rows,
        &log.recorded_run_records,
        &stored,
        ExpectedDeletion::EveryEligibleRow,
    )
    .expect("the replay deletes every eligible row");
    let expected = ExpectedRows::new(&log.recorded, &log.recorded_rows, &deleted, &stored, None);

    let stale_metadata = log
        .recorded
        .iter()
        .find(|(row_key, recorded_value)| {
            expected.is_derived(row_key)
                && parse_run_key(&decode_hex(&row_key.1))
                    .is_ok_and(|parsed| parsed.table == RunTable::HistoryRowMetadata)
                && expected
                    .metadata(&row_key.1)
                    .is_some_and(|value| dumped_value(&value) != **recorded_value)
        })
        .map(|(row_key, recorded_value)| (row_key.clone(), recorded_value.clone()))
        .expect("a checkpoint's history row metadata changed");
    let recorded_metadata = decode_hex(
        stale_metadata
            .1
            .strip_prefix('=')
            .expect("metadata values are dumped in full"),
    );
    let mut stale_runs = Vec::new();
    let counters: [(&str, CounterAccess); 4] = [
        ("state row bytes", |record| {
            &mut record.storage.as_mut().expect("storage").state_row_bytes
        }),
        ("history bytes", |record| {
            &mut record.storage.as_mut().expect("storage").history_bytes
        }),
        ("row write bytes since the checkpoint", |record| {
            &mut record.row_write_bytes_since_checkpoint
        }),
        ("last checkpoint state bytes", |record| {
            &mut record.last_checkpoint_state_bytes
        }),
    ];
    for (name, counter) in counters {
        let run = log
            .recorded_run_records
            .iter()
            .find_map(|(hex_key, recorded_value)| {
                let key = decode_hex(hex_key);
                let parsed = parse_run_key(&key).ok()?;
                let mut expected_record = expected.run_record(parsed.flow_hash, parsed.run_id)?;
                let mut recorded_record = RunRecord::decode(recorded_value.as_slice()).ok()?;
                let recorded_counter = *counter(&mut recorded_record);
                (*counter(&mut expected_record) != recorded_counter)
                    .then(|| (hex_key.clone(), recorded_counter))
            })
            .unwrap_or_else(|| panic!("some run record recomputes its {name}"));
        stale_runs.push((name, counter, run));
    }

    let mut cases: Vec<(String, StateDump, StoredRows)> = Vec::new();
    let mut with_stale_metadata = (replayed.clone(), stored.clone());
    with_stale_metadata
        .0
        .insert(stale_metadata.0.clone(), stale_metadata.1.clone());
    with_stale_metadata.1.insert(
        (stale_metadata.0.0.clone(), decode_hex(&stale_metadata.0.1)),
        recorded_metadata,
    );
    cases.push((
        "stale history row metadata".to_owned(),
        with_stale_metadata.0,
        with_stale_metadata.1,
    ));
    for (name, counter, (hex_key, recorded_counter)) in stale_runs {
        let stored_key = (ColumnFamily::Flow.name().to_owned(), decode_hex(&hex_key));
        let mut record = RunRecord::decode(stored[&stored_key].as_slice()).expect("decodes");
        let cursor_bytes = if name == "state row bytes" {
            let mut without_cursors = record.clone();
            without_cursors.history_metadata_pruned_before_seq = 0;
            without_cursors.value_garbage_pruned_before_seq = 0;
            (record.encoded_len() - without_cursors.encoded_len()) as u64
        } else {
            0
        };
        *counter(&mut record) = recorded_counter + cursor_bytes;
        let value = with_kept_removed_fields(
            RunTable::RunRecord,
            &stored[&stored_key],
            &record.encode_to_vec(),
        );
        let mut case_replayed = replayed.clone();
        let mut case_stored = stored.clone();
        case_replayed.insert(
            (ColumnFamily::Flow.name().to_owned(), hex_key.clone()),
            dumped_value(&value),
        );
        case_stored.insert(stored_key, value);
        cases.push((format!("stale {name}"), case_replayed, case_stored));
    }
    let (swept_key, swept_value) = stored
        .iter()
        .find(|((column_family, key), value)| {
            column_family == ColumnFamily::Flow.name()
                && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::RunRecord)
                && RunRecord::decode(value.as_slice())
                    .is_ok_and(|record| record.ended_step_row_sweep.is_some())
        })
        .expect("a replayed run record holds its ended Step row sweep");
    // The recorded run record lacks the sweep, and its state row bytes lack the sweep's bytes.
    let mut unswept_record = RunRecord::decode(swept_value.as_slice()).expect("decodes");
    let swept_len = unswept_record.encoded_len();
    unswept_record.ended_step_row_sweep = None;
    let sweep_bytes = (swept_len - unswept_record.encoded_len()) as u64;
    unswept_record
        .storage
        .as_mut()
        .expect("storage")
        .state_row_bytes -= sweep_bytes;
    let unswept_value = with_kept_removed_fields(
        RunTable::RunRecord,
        swept_value,
        &unswept_record.encode_to_vec(),
    );
    let mut unswept = (replayed.clone(), stored.clone());
    unswept.0.insert(
        (swept_key.0.clone(), hex(&swept_key.1)),
        dumped_value(&unswept_value),
    );
    unswept.1.insert(swept_key.clone(), unswept_value);
    cases.push(("an unswept run record".to_owned(), unswept.0, unswept.1));
    let (checkpoint_row, recorded_checkpoint) = log
        .recorded_rows
        .iter()
        .find(|((column_family, hex_key), recorded_value)| {
            column_family == ColumnFamily::History.name()
                && expected
                    .checkpoints
                    .get(&decode_hex(hex_key))
                    .is_some_and(|checkpoint| checkpoint != *recorded_value)
        })
        .expect("a checkpoint drops deletable Step rows");
    let mut with_stale_checkpoint = (replayed.clone(), stored.clone());
    with_stale_checkpoint
        .0
        .insert(checkpoint_row.clone(), log.recorded[checkpoint_row].clone());
    with_stale_checkpoint.1.insert(
        (checkpoint_row.0.clone(), decode_hex(&checkpoint_row.1)),
        recorded_checkpoint.clone(),
    );
    let error = log
        .compare(
            &with_stale_checkpoint.0,
            &with_stale_checkpoint.1,
            SnapshotUse::Ignore,
        )
        .expect_err("the comparison accepts a checkpoint that keeps deletable Step rows");
    assert!(error.contains(&checkpoint_row.1), "{error}");
    for (case, case_replayed, case_stored) in cases {
        assert!(
            log.compare(&case_replayed, &case_stored, SnapshotUse::Ignore)
                .is_err(),
            "the comparison accepts a replay with {case}"
        );
    }
}

/// The comparison must reject removed FlowConfig fields that differ from the logged ones.
#[tokio::test]
async fn the_offload_choice_comparison_rejects_other_removed_flow_config_fields() {
    let directory = testdata_root().join(OFFLOAD_CHOICE_LOG_DIRECTORY);
    let log = read_recorded_log();
    let (replayed, stored) =
        replay_with_older_backend_errors(&directory, SnapshotUse::Ignore).await;
    log.compare(&replayed, &stored, SnapshotUse::Ignore)
        .expect("the unchanged replay matches");
    let other_fields = RemovedFlowConfigFields {
        continue_as_new_threshold: Some(99),
        continue_as_new_page_size_in_bytes: None,
    };
    let (run_record_key, run_record) = stored
        .iter()
        .find(|((column_family, key), value)| {
            column_family == ColumnFamily::Flow.name()
                && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::RunRecord)
                && !removed_fields_in(RunTable::RunRecord, value)
                    .iter()
                    .all(|fields| *fields == RemovedFlowConfigFields::default())
        })
        .expect("a run record holds the removed fields");
    let other_record = with_fields(run_record, run_record_paths(), other_fields);
    assert_eq!(
        other_record.len(),
        run_record.len(),
        "only the value differs"
    );
    let (history_key, history_row) = stored
        .iter()
        .find(|((column_family, key), value)| {
            column_family == ColumnFamily::History.name()
                && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::HistoryRecord)
                && removed_fields_in(RunTable::HistoryRecord, value)
                    .iter()
                    .any(|fields| *fields != RemovedFlowConfigFields::default())
        })
        .expect("the history row of a Flow start holds the removed fields");
    let without_fields = with_fields(
        history_row,
        history_paths(),
        RemovedFlowConfigFields::default(),
    );
    for (case, row_key, value) in [
        ("another removed field value", run_record_key, other_record),
        (
            "a Flow start without the removed fields",
            history_key,
            without_fields,
        ),
    ] {
        let mut case_replayed = replayed.clone();
        let mut case_stored = stored.clone();
        case_replayed.insert((row_key.0.clone(), hex(&row_key.1)), dumped_value(&value));
        case_stored.insert(row_key.clone(), value);
        let error = log
            .compare(&case_replayed, &case_stored, SnapshotUse::Ignore)
            .expect_err(case);
        assert!(error.contains(&hex(&row_key.1)), "{case}: {error}");
    }
}

#[tokio::test]
async fn the_offload_choice_comparison_requires_every_deletion_after_a_restored_snapshot() {
    let directory = testdata_root().join(OFFLOAD_CHOICE_LOG_DIRECTORY);
    let log = read_recorded_log();
    let (replayed, stored) =
        replay_with_older_backend_errors(&directory, SnapshotUse::Restore).await;
    log.compare(&replayed, &stored, SnapshotUse::Restore)
        .expect("the unchanged replay matches");
    let deleted = deleted_ended_step_rows(
        &log.recorded,
        &replayed,
        &log.recorded_rows,
        &log.recorded_run_records,
        &stored,
        ExpectedDeletion::OnlyEligibleRows,
    )
    .expect("the replay deletes only eligible rows");
    let is_unchanged_in_snapshot = |row_key: &(String, String)| {
        log.snapshot
            .get(&(row_key.0.clone(), decode_hex(&row_key.1)))
            == log.recorded_rows.get(row_key)
    };
    assert!(
        deleted.iter().any(is_unchanged_in_snapshot),
        "the sweep deleted a row that the snapshot held"
    );
    assert!(
        deleted
            .iter()
            .any(|row_key| !is_unchanged_in_snapshot(row_key)),
        "a replayed command deleted the row of the Step it ended"
    );
    for row_key in &deleted {
        let (case_replayed, case_stored) =
            replay_keeping_row(&log, &replayed, &stored, &deleted, row_key);
        let error = log
            .compare(&case_replayed, &case_stored, SnapshotUse::Restore)
            .expect_err("the comparison accepts a replay that kept a deletable row");
        assert!(error.starts_with("the replay must delete"), "{error}");
    }
}

/// Returns the restored replay with one deleted Step row kept, and its run record counting the
/// row as the derivation does, so that only the missing deletion differs.
fn replay_keeping_row(
    log: &RecordedLog,
    replayed: &StateDump,
    stored: &StoredRows,
    deleted: &BTreeSet<(String, String)>,
    row_key: &(String, String),
) -> (StateDump, StoredRows) {
    let key = decode_hex(&row_key.1);
    let parsed = parse_run_key(&key).expect("pinned keys parse");
    let record_key = (
        ColumnFamily::Flow.name().to_owned(),
        run_singleton_key(parsed.flow_hash, parsed.run_id, RunTable::RunRecord),
    );
    let mut case_replayed = replayed.clone();
    let mut case_stored = stored.clone();
    case_replayed.insert(row_key.clone(), log.recorded[row_key].clone());
    case_stored.insert(
        (row_key.0.clone(), key.clone()),
        log.recorded_rows[row_key].clone(),
    );
    let mut kept_deleted = deleted.clone();
    kept_deleted.remove(row_key);
    let expected = ExpectedRows::new(
        &log.recorded,
        &log.recorded_rows,
        &kept_deleted,
        &case_stored,
        Some(&log.snapshot),
    );
    let mut record = expected
        .run_record(parsed.flow_hash, parsed.run_id)
        .expect("the run record derives");
    let replayed_record =
        RunRecord::decode(stored[&record_key].as_slice()).expect("run records decode");
    let size_without_cursors = record.encoded_len();
    record.history_metadata_pruned_before_seq = replayed_record.history_metadata_pruned_before_seq;
    record.value_garbage_pruned_before_seq = replayed_record.value_garbage_pruned_before_seq;
    let cursor_bytes = (record.encoded_len() - size_without_cursors) as u64;
    record.storage.as_mut().expect("storage").state_row_bytes += cursor_bytes;
    let record_value = with_kept_removed_fields(
        RunTable::RunRecord,
        &stored[&record_key],
        &record.encode_to_vec(),
    );
    case_replayed.insert(
        (record_key.0.clone(), hex(&record_key.1)),
        dumped_value(&record_value),
    );
    case_stored.insert(record_key.clone(), record_value);
    assert!(
        expected.matches(&(record_key.0, hex(&record_key.1)), &case_stored),
        "the kept row's run record follows the derivation"
    );
    (case_replayed, case_stored)
}

/// Returns a mutable counter of a run record.
type CounterAccess = fn(&mut RunRecord) -> &mut u64;

/// The recorded state of the log: its state dump, its pinned full rows, its run records by hex
/// key, and the rows of its snapshot.
struct RecordedLog {
    recorded: StateDump,
    recorded_rows: OlderRows,
    recorded_run_records: BTreeMap<String, Vec<u8>>,
    snapshot: SnapshotRows,
}

impl RecordedLog {
    /// Compares a replay with the recorded state.
    ///
    /// Returns an error when the replay deleted a row that is not an eligible ended Step row,
    /// writes or deletes another row, or holds a row that differs from its expected value. A row
    /// that later apply derives anew must equal the derived value; every other row must equal
    /// the recorded one.
    fn compare(
        &self,
        replayed: &StateDump,
        stored: &StoredRows,
        snapshot_use: SnapshotUse,
    ) -> Result<(), String> {
        // The helper checks each deleted row; check_deletions then requires the exact set.
        let (deletion, snapshot) = match snapshot_use {
            SnapshotUse::Ignore => (ExpectedDeletion::EveryEligibleRow, None),
            SnapshotUse::Restore => (ExpectedDeletion::OnlyEligibleRows, Some(&self.snapshot)),
        };
        let deleted = deleted_ended_step_rows(
            &self.recorded,
            replayed,
            &self.recorded_rows,
            &self.recorded_run_records,
            stored,
            deletion,
        )?;
        if deleted.is_empty() {
            return Err("the recording binary kept rows of ended Steps".to_owned());
        }
        let kept_keys: Vec<_> = self
            .recorded
            .keys()
            .filter(|key| !deleted.contains(*key))
            .collect();
        if kept_keys != replayed.keys().collect::<Vec<_>>() {
            return Err("the replay writes or deletes other rows".to_owned());
        }
        let expected = ExpectedRows::new(
            &self.recorded,
            &self.recorded_rows,
            &deleted,
            stored,
            snapshot,
        );
        expected.check_deletions(stored)?;
        let changed: Vec<_> = self
            .recorded
            .iter()
            .filter(|(key, _)| !deleted.contains(*key))
            .filter(|(key, recorded_value)| {
                if expected.is_derived(key) {
                    !expected.matches(key, stored)
                } else {
                    replayed.get(*key) != Some(*recorded_value)
                }
            })
            .map(|(key, _)| key)
            .collect();
        if changed.is_empty() {
            Ok(())
        } else {
            Err(format!(
                "only rows that the Step row deletion and the prune cursors change differ: \
                 {changed:?}"
            ))
        }
    }
}

/// Returns the recorded state dump, its pinned full rows, and its run records by hex key.
fn read_recorded_log() -> RecordedLog {
    let directory = testdata_root().join(OFFLOAD_CHOICE_LOG_DIRECTORY);
    let recorded = parse_state_dump(
        &std::fs::read_to_string(directory.join(STATE_DUMP_FILE))
            .expect("read the recorded state dump"),
    );
    let recorded_rows = read_pinned_rows(
        &recorded,
        RECORDED_ROWS,
        &[
            (ColumnFamily::Flow, RunTable::RunRecord),
            (ColumnFamily::Flow, RunTable::StepExecution),
            (ColumnFamily::History, RunTable::Checkpoint),
        ],
    );
    let recorded_run_records = recorded_rows
        .iter()
        .filter(|((column_family, hex_key), _)| {
            column_family == ColumnFamily::Flow.name()
                && parse_run_key(&decode_hex(hex_key))
                    .is_ok_and(|parsed| parsed.table == RunTable::RunRecord)
        })
        .map(|((_, hex_key), value)| (hex_key.clone(), value.clone()))
        .collect();
    let mut snapshot = SnapshotRows::new();
    for (object_key, body) in read_files(&directory.join(OBJECTS_DIRECTORY)) {
        if object_key.starts_with("snapshot/") && object_key.contains("/files/") {
            for (column_family, key, value) in memory_snapshot_rows(&body) {
                snapshot.insert((column_family.name().to_owned(), key), value);
            }
        }
    }
    assert!(!snapshot.is_empty(), "the log holds a snapshot");
    RecordedLog {
        recorded,
        recorded_rows,
        recorded_run_records,
        snapshot,
    }
}

/// The recorded rows as later apply derives them: checkpoints without the Step rows it deleted,
/// their history row metadata, and run records with byte counters that follow both.
struct ExpectedRows<'rows> {
    recorded: &'rows StateDump,
    recorded_rows: &'rows OlderRows,
    deleted: &'rows BTreeSet<(String, String)>,
    checkpoints: BTreeMap<Vec<u8>, Vec<u8>>,
    /// The restored snapshot, or `None` for a replay of the whole log.
    snapshot: Option<&'rows SnapshotRows>,
}

impl<'rows> ExpectedRows<'rows> {
    /// Derives the expected checkpoints of the replayed checkpoint keys from the recorded ones.
    fn new(
        recorded: &'rows StateDump,
        recorded_rows: &'rows OlderRows,
        deleted: &'rows BTreeSet<(String, String)>,
        stored: &StoredRows,
        snapshot: Option<&'rows SnapshotRows>,
    ) -> Self {
        let mut expected = Self {
            recorded,
            recorded_rows,
            deleted,
            checkpoints: BTreeMap::new(),
            snapshot,
        };
        for ((column_family, hex_key), recorded_value) in recorded_rows {
            let key = decode_hex(hex_key);
            let is_checkpoint = column_family == ColumnFamily::History.name()
                && parse_run_key(&key).is_ok_and(|parsed| parsed.table == RunTable::Checkpoint);
            if !is_checkpoint || !stored.contains_key(&(column_family.clone(), key.clone())) {
                continue;
            }
            let checkpoint = expected.checkpoint(&key, recorded_value, stored);
            expected.checkpoints.insert(key, checkpoint);
        }
        expected
    }

    /// Returns the recorded checkpoint as later apply writes it: unchanged when the restored
    /// snapshot holds it, and otherwise without the Step rows that apply drops.
    fn checkpoint(&self, key: &[u8], recorded_value: &[u8], stored: &StoredRows) -> Vec<u8> {
        let history_name = ColumnFamily::History.name().to_owned();
        let is_in_snapshot = self.snapshot.is_some_and(|snapshot| {
            snapshot
                .get(&(history_name, key.to_vec()))
                .map(Vec::as_slice)
                == Some(recorded_value)
        });
        if is_in_snapshot {
            return recorded_value.to_vec();
        }
        let parsed = parse_run_key(key).expect("checkpoint keys parse");
        let mut checkpoint = CheckpointRecord::decode(recorded_value).expect("checkpoints decode");
        checkpoint.rows.retain(|row| {
            if row.table != RunTable::StepExecution as u32 {
                return true;
            }
            let step = StepExecutionRecord::decode(row.value.as_slice()).expect("Step rows decode");
            let step_key = run_named_key(
                parsed.flow_hash,
                parsed.run_id,
                RunTable::StepExecution,
                &step.step_execution_id,
            );
            !self.is_dropped(
                parsed.flow_hash,
                parsed.run_id,
                &step_key,
                &step,
                &row.value,
                stored,
            )
        });
        checkpoint.encode_to_vec()
    }

    /// Returns whether later apply derives this row anew from the recorded state: a checkpoint,
    /// the history row metadata of a recorded checkpoint, or a run record.
    fn is_derived(&self, (column_family, hex_key): &(String, String)) -> bool {
        let key = decode_hex(hex_key);
        let Ok(parsed) = parse_run_key(&key) else {
            return false;
        };
        match (column_family.as_str(), parsed.table) {
            (name, RunTable::Checkpoint) => name == ColumnFamily::History.name(),
            // Offload deletes uploaded checkpoints locally; their metadata keeps its size.
            (name, RunTable::HistoryRowMetadata) => {
                name == ColumnFamily::Flow.name()
                    && self
                        .checkpoint_key_of_metadata(&key)
                        .is_some_and(|checkpoint_key| {
                            self.recorded_rows.contains_key(&(
                                ColumnFamily::History.name().to_owned(),
                                hex(&checkpoint_key),
                            ))
                        })
            }
            (name, RunTable::RunRecord) => name == ColumnFamily::Flow.name(),
            _ => false,
        }
    }

    /// Returns whether the replayed row equals the row that later apply derives from the
    /// recorded state.
    fn matches(&self, (column_family, hex_key): &(String, String), stored: &StoredRows) -> bool {
        let key = decode_hex(hex_key);
        let Ok(parsed) = parse_run_key(&key) else {
            return false;
        };
        let Some(replayed) = stored.get(&(column_family.clone(), key.clone())) else {
            return false;
        };
        match (column_family.as_str(), parsed.table) {
            (name, RunTable::Checkpoint) if name == ColumnFamily::History.name() => {
                self.checkpoints.get(&key) == Some(replayed)
            }
            (name, RunTable::HistoryRowMetadata) if name == ColumnFamily::Flow.name() => {
                self.metadata(hex_key).as_ref() == Some(replayed)
            }
            (name, RunTable::RunRecord) if name == ColumnFamily::Flow.name() => {
                let Some(expected) = self.run_record(parsed.flow_hash, parsed.run_id) else {
                    return false;
                };
                without_prune_cursors(replayed)
                    == self.with_recorded_removed_fields(&key, &expected.encode_to_vec())
            }
            _ => false,
        }
    }

    /// Returns the recorded history row metadata with the size of its expected checkpoint, or
    /// `None` when it describes no changed checkpoint.
    fn metadata(&self, hex_key: &str) -> Option<Vec<u8>> {
        let mut metadata = HistoryRowMetadata::decode(
            decode_hex(
                self.recorded
                    .get(&(ColumnFamily::Flow.name().to_owned(), hex_key.to_owned()))?
                    .strip_prefix('=')?,
            )
            .as_slice(),
        )
        .ok()?;
        let checkpoint_key = self.checkpoint_key_of_metadata(&decode_hex(hex_key))?;
        let checkpoint = self.checkpoints.get(&checkpoint_key)?;
        metadata.row_size_bytes = (checkpoint_key.len() + checkpoint.len()) as u64;
        Some(metadata.encode_to_vec())
    }

    fn checkpoint_key_of_metadata(&self, metadata_key: &[u8]) -> Option<Vec<u8>> {
        let parsed = parse_run_key(metadata_key).ok()?;
        let suffix = parsed.suffix;
        if suffix.len() != 13 || suffix[8] != RunTable::Checkpoint as u8 {
            return None;
        }
        let seq = u64::from_be_bytes(suffix[..8].try_into().ok()?);
        let attempt = u32::from_be_bytes(suffix[9..].try_into().ok()?);
        Some(checkpoint_key(
            parsed.flow_hash,
            parsed.run_id,
            attempt,
            seq,
        ))
    }

    /// Returns the recorded run record with what later apply computes: the ended Step row sweep
    /// of #1430, complete once a replayed command reached the run; its state row bytes without
    /// the deleted Step rows; its row write bytes since its last checkpoint without the Step
    /// rows that the commands ending them deleted instead of writing; the row bytes of its
    /// expected last checkpoint; its checkpoints' new sizes in its history bytes; and its own new
    /// size.
    fn run_record(&self, flow_hash: FlowHash, run_id: RunId) -> Option<RunRecord> {
        let record_key = run_singleton_key(flow_hash, run_id, RunTable::RunRecord);
        let recorded_value = self
            .recorded_rows
            .get(&(ColumnFamily::Flow.name().to_owned(), hex(&record_key)))?;
        let mut record = RunRecord::decode(recorded_value.as_slice()).ok()?;
        let mut usage: RunStorageUsage = record.storage?;
        let run_prefix = hex(&dexzd_keyspace::run_prefix(flow_hash, run_id));
        let flow_name = ColumnFamily::Flow.name().to_owned();
        if self.is_reached_by_replay(flow_hash, run_id) {
            if self.step_row_keys(flow_hash, run_id).len() >= SWEEP_ROWS_PER_COMMAND {
                return None;
            }
            record.ended_step_row_sweep = Some(EndedStepRowSweep {
                last_visited_step_execution_id: String::new(),
                is_complete: true,
            });
        }
        let mut deleted_step_bytes = 0_u64;
        let mut unwritten_step_bytes = 0_u64;
        for row_key in self
            .deleted
            .iter()
            .filter(|(_, hex_key)| hex_key.starts_with(&run_prefix))
        {
            let value = self.recorded_rows.get(row_key)?;
            let row_bytes = (row_key.1.len() / 2 + value.len()) as u64;
            deleted_step_bytes += row_bytes;
            // A Step that ended before the snapshot was written then; the sweep deletes its row.
            let is_swept = self.snapshot.is_some_and(|snapshot| {
                snapshot.get(&(flow_name.clone(), decode_hex(&row_key.1))) == Some(value)
            });
            // The command that ended any other Step deletes the row instead of writing it.
            let step = StepExecutionRecord::decode(value.as_slice()).ok()?;
            if !is_swept && step.completed_seq > record.last_checkpoint_seq {
                unwritten_step_bytes += row_bytes;
            }
        }
        record.row_write_bytes_since_checkpoint = record
            .row_write_bytes_since_checkpoint
            .checked_sub(unwritten_step_bytes)?;
        let last_checkpoint_key = checkpoint_key(
            flow_hash,
            run_id,
            record.current_attempt,
            record.last_checkpoint_seq,
        );
        if let Some(checkpoint) = self.checkpoints.get(&last_checkpoint_key) {
            record.last_checkpoint_state_bytes = CheckpointRecord::decode(checkpoint.as_slice())
                .ok()?
                .rows
                .iter()
                .map(|row| (row.key_suffix.len() + row.value.len()) as u64)
                .sum();
        }
        let mut history_delta: i128 = 0;
        for (checkpoint_key, expected_checkpoint) in &self.checkpoints {
            let parsed = parse_run_key(checkpoint_key).expect("checkpoint keys parse");
            if parsed.flow_hash != flow_hash || parsed.run_id != run_id {
                continue;
            }
            let attempt = u32::from_be_bytes(parsed.suffix[..4].try_into().ok()?);
            let seq = u64::from_be_bytes(parsed.suffix[4..].try_into().ok()?);
            let metadata_key =
                history_row_metadata_key(flow_hash, run_id, seq, RunTable::Checkpoint, attempt);
            let metadata_hex = hex(&metadata_key);
            let recorded_metadata = decode_hex(
                self.recorded
                    .get(&(ColumnFamily::Flow.name().to_owned(), metadata_hex.clone()))?
                    .strip_prefix('=')?,
            );
            let expected_metadata = self.metadata(&metadata_hex)?;
            let recorded_checkpoint = self
                .recorded_rows
                .get(&(ColumnFamily::History.name().to_owned(), hex(checkpoint_key)))?;
            history_delta += (expected_metadata.len() + expected_checkpoint.len()) as i128
                - (recorded_metadata.len() + recorded_checkpoint.len()) as i128;
        }
        usage.history_bytes =
            u64::try_from(i128::from(usage.history_bytes) + history_delta).ok()?;
        let other_state_bytes = usage
            .state_row_bytes
            .checked_sub(deleted_step_bytes + (record_key.len() + recorded_value.len()) as u64)?;
        // The record's own row counts in its state row bytes; repeat until its size settles.
        for _ in 0..4 {
            record.storage = Some(usage);
            let own_value = self.with_recorded_removed_fields(&record_key, &record.encode_to_vec());
            let own_bytes = (record_key.len() + own_value.len()) as u64;
            if usage.state_row_bytes == other_state_bytes + own_bytes {
                return Some(record);
            }
            usage.state_row_bytes = other_state_bytes + own_bytes;
        }
        None
    }

    /// Returns the encoded run record with the removed FlowConfig fields of the recorded one,
    /// which decoding drops.
    fn with_recorded_removed_fields(&self, record_key: &[u8], encoded: &[u8]) -> Vec<u8> {
        let recorded_value =
            &self.recorded_rows[&(ColumnFamily::Flow.name().to_owned(), hex(record_key))];
        with_kept_removed_fields(RunTable::RunRecord, recorded_value, encoded)
    }

    /// Whether a replayed command reached the run: every run of a full replay, and after a
    /// restored snapshot, each run whose record changed after the snapshot.
    ///
    /// A full replay starts every run with its FlowStart, which records a complete sweep. After
    /// a restored snapshot, the first replayed command of an older run sweeps it.
    fn is_reached_by_replay(&self, flow_hash: FlowHash, run_id: RunId) -> bool {
        let record_key = run_singleton_key(flow_hash, run_id, RunTable::RunRecord);
        let recorded_value = self
            .recorded_rows
            .get(&(ColumnFamily::Flow.name().to_owned(), hex(&record_key)));
        self.snapshot.is_none_or(|snapshot| {
            snapshot.get(&(ColumnFamily::Flow.name().to_owned(), record_key)) != recorded_value
        })
    }

    /// Returns the keys of the run's Step rows in the snapshot or the recording, which bound the
    /// rows its sweep visits.
    fn step_row_keys(&self, flow_hash: FlowHash, run_id: RunId) -> BTreeSet<Vec<u8>> {
        let flow_name = ColumnFamily::Flow.name();
        let snapshot_keys = self
            .snapshot
            .into_iter()
            .flat_map(|snapshot| snapshot.keys())
            .filter(|(column_family, _)| column_family == flow_name)
            .map(|(_, key)| key.clone());
        let recorded_keys = self
            .recorded_rows
            .keys()
            .filter(|(column_family, _)| column_family == flow_name)
            .map(|(_, hex_key)| decode_hex(hex_key));
        snapshot_keys
            .chain(recorded_keys)
            .filter(|key| {
                parse_run_key(key).is_ok_and(|parsed| {
                    parsed.table == RunTable::StepExecution
                        && parsed.flow_hash == flow_hash
                        && parsed.run_id == run_id
                })
            })
            .collect()
    }

    /// Whether later apply drops this value of a Step row from the run's state.
    ///
    /// The sweep decides for a row that the restored snapshot held with this value; the command
    /// that ended the Step decides for every other row.
    fn is_dropped(
        &self,
        flow_hash: FlowHash,
        run_id: RunId,
        step_key: &[u8],
        step: &StepExecutionRecord,
        step_value: &[u8],
        stored: &StoredRows,
    ) -> bool {
        let flow_name = ColumnFamily::Flow.name().to_owned();
        let is_unchanged_in_snapshot = self.snapshot.is_some_and(|snapshot| {
            snapshot
                .get(&(flow_name, step_key.to_vec()))
                .map(Vec::as_slice)
                == Some(step_value)
        });
        if is_unchanged_in_snapshot {
            self.is_reached_by_replay(flow_hash, run_id) && is_deletable_by_sweep(step)
        } else {
            is_expected_deletion(step, flow_hash, run_id, stored)
        }
    }

    /// Returns an error unless the replay deleted exactly the recorded Step rows that later
    /// apply drops; no eligible row may stay.
    fn check_deletions(&self, stored: &StoredRows) -> Result<(), String> {
        let flow_name = ColumnFamily::Flow.name();
        for (row_key, recorded_value) in self.recorded_rows {
            let key = decode_hex(&row_key.1);
            let Ok(parsed) = parse_run_key(&key) else {
                continue;
            };
            let is_step_row = row_key.0 == flow_name && parsed.table == RunTable::StepExecution;
            if !is_step_row || !self.recorded.contains_key(row_key) {
                continue;
            }
            let step = StepExecutionRecord::decode(recorded_value.as_slice())
                .map_err(|error| format!("a recorded Step row does not decode: {error}"))?;
            let is_dropped = self.is_dropped(
                parsed.flow_hash,
                parsed.run_id,
                &key,
                &step,
                recorded_value,
                stored,
            );
            if self.deleted.contains(row_key) != is_dropped {
                return Err(format!(
                    "the replay must {} the row of {}",
                    if is_dropped { "delete" } else { "keep" },
                    step.step_execution_id
                ));
            }
        }
        Ok(())
    }
}

/// Whether the sweep deletes an older Step row: a Completed Step that is no Flow timeout
/// handler, or a cancellation marked as not caused by FlowClosed.
fn is_deletable_by_sweep(step: &StepExecutionRecord) -> bool {
    if step.is_timeout_handler {
        return false;
    }
    match StepExecutionPhase::try_from(step.phase) {
        Ok(StepExecutionPhase::Completed) => true,
        Ok(StepExecutionPhase::Canceled) => step.is_canceled_by_flow_close == Some(false),
        _ => false,
    }
}

/// Clears RunRecord fields 76 and 77 of a replayed run record and the bytes they add to the
/// record's own size in its state row bytes.
///
/// Panics when the run record does not encode back to the same bytes, apart from the removed
/// FlowConfig fields that it keeps, so that unknown or noncanonical fields cannot hide behind
/// the rewrite.
fn without_prune_cursors(value: &[u8]) -> Vec<u8> {
    // Apply maintains these prune cursors since #1427; the recording binary had neither field.
    let mut record = RunRecord::decode(value).expect("run records decode");
    assert_eq!(
        with_kept_removed_fields(RunTable::RunRecord, value, &record.encode_to_vec()),
        value,
        "a replayed run record holds bytes that decoding drops or rewrites"
    );
    let size_with_cursors = record.encoded_len();
    record.history_metadata_pruned_before_seq = 0;
    record.value_garbage_pruned_before_seq = 0;
    let cursor_bytes = (size_with_cursors - record.encoded_len()) as u64;
    if let Some(storage) = record.storage.as_mut() {
        storage.state_row_bytes -= cursor_bytes;
    }
    with_kept_removed_fields(RunTable::RunRecord, value, &record.encode_to_vec())
}

#[test]
fn the_prune_cursor_rewrite_clears_only_the_cursors_and_their_bytes() {
    let without_cursors = RunRecord {
        flow_id: "cursor-flow".to_owned(),
        window_start_seq: 9,
        storage: Some(RunStorageUsage {
            state_row_bytes: 2_000,
            ..RunStorageUsage::default()
        }),
        ..RunRecord::default()
    };
    let mut with_cursors = without_cursors.clone();
    with_cursors.history_metadata_pruned_before_seq = 393;
    with_cursors.value_garbage_pruned_before_seq = 393;
    let cursor_bytes = (with_cursors.encoded_len() - without_cursors.encoded_len()) as u64;
    with_cursors
        .storage
        .as_mut()
        .expect("the record counts storage")
        .state_row_bytes += cursor_bytes;
    assert_eq!(
        without_prune_cursors(&with_cursors.encode_to_vec()),
        without_cursors.encode_to_vec()
    );
}

#[test]
#[should_panic(expected = "decoding drops or rewrites")]
fn the_prune_cursor_rewrite_rejects_a_run_record_with_an_unknown_field() {
    let mut value = RunRecord {
        flow_id: "cursor-flow".to_owned(),
        history_metadata_pruned_before_seq: 5,
        ..RunRecord::default()
    }
    .encode_to_vec();
    // Field 78 as a varint: RunRecord has no such field, so decoding drops it.
    value.extend_from_slice(&[0xf0, 0x04, 0x01]);
    without_prune_cursors(&value);
}
