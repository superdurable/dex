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

use std::collections::BTreeMap;
use std::sync::Arc;

use dexzd_apply::FlowStateReader;
use dexzd_engine::SystemEngineIdSource;
use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key};
use dexzd_protocol::dex::{FlowStatus, flow_history_event};
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, HistoryRecord, RunRecord, StepExecutionRecord, StepInvocationPhase,
};
use prost::Message;

use crate::compat_node::{engine_node_config, start_engine_node};
use crate::format_decoding::decode_objects;
use crate::golden_replay::{RESTART_DELAY_MICROS, SnapshotUse, forget_snapshots};
use crate::scripted_clock::ScriptedClock;
use crate::state_dump::{StateDump, dump_committed_state, dumped_value, parse_state_dump};
use crate::testdata_tree::{
    OBJECTS_DIRECTORY, STATE_DUMP_FILE, load_objects, read_files, testdata_root,
};

/// The format-compat log that the last binary before attempt hand-out recorded.
const OLDER_LOG_DIRECTORY: &str = "pre-attempt-handout-log";
/// Full values of the recorded run records, which `state.txt` only hashes.
const RUN_RECORDS_FILE: &str = "run_records.txt";

/// Committed rows by column family name and key, as stored.
type StoredRows = BTreeMap<(String, Vec<u8>), Vec<u8>>;

#[tokio::test]
async fn a_log_without_task_ids_or_failure_guards_replays_to_the_older_state() {
    for snapshot_use in [SnapshotUse::Restore, SnapshotUse::Ignore] {
        let directory = testdata_root().join(OLDER_LOG_DIRECTORY);
        let recorded_text = std::fs::read_to_string(directory.join(STATE_DUMP_FILE))
            .expect("read the older state dump");
        let recorded = parse_state_dump(&recorded_text);
        let recorded_run_records = read_run_records(&directory);
        let (replayed, stored) = replay(&directory, snapshot_use).await;
        assert_eq!(
            recorded.keys().collect::<Vec<_>>(),
            replayed.keys().collect::<Vec<_>>(),
            "{snapshot_use:?}: the replay writes and deletes the same rows"
        );
        let changed_rows: Vec<_> = recorded
            .iter()
            .filter(|(key, value)| replayed.get(*key) != Some(*value))
            .filter(|((column_family, key), value)| {
                !matches_without_new_fields(
                    column_family,
                    key,
                    value,
                    &recorded_run_records,
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
            let mut checkpoint =
                CheckpointRecord::decode(value.as_slice()).expect("checkpoint rows decode");
            for row in &mut checkpoint.rows {
                if row.table == RunTable::StepExecution as u32 {
                    row.value = step_execution_without_new_fields(&row.value);
                }
            }
            checkpoint.encode_to_vec()
        }
        (name, RunTable::HistoryRecord) if name == ColumnFamily::History.name() => {
            history_without_scheduled_time(value)
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
fn run_record_without_storage(value: &[u8]) -> RunRecord {
    let mut run = RunRecord::decode(value).expect("run record rows decode");
    run.storage = None;
    run.row_write_bytes_since_checkpoint = 0;
    run.last_checkpoint_state_bytes = 0;
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
    let config = engine_node_config(
        &node.node_id,
        node.incarnation,
        decoded.cluster.partition_count,
    );
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
        for column_family in [ColumnFamily::Flow, ColumnFamily::History] {
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
