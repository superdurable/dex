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

use std::sync::Arc;

use dexzd_apply::FlowStateReader;
use dexzd_engine::SystemEngineIdSource;
use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key};
use dexzd_protocol::dexzd::v1::{StepExecutionRecord, StepInvocationPhase};
use prost::Message;

use crate::compat_node::{engine_node_config, start_engine_node};
use crate::format_decoding::decode_objects;
use crate::golden_replay::{RESTART_DELAY_MICROS, SnapshotUse, forget_snapshots};
use crate::scripted_clock::ScriptedClock;
use crate::state_dump::{StateDump, dump_committed_state, parse_state_dump};
use crate::testdata_tree::{
    OBJECTS_DIRECTORY, STATE_DUMP_FILE, load_objects, read_files, testdata_root,
};

/// The `testdata/v1` that the last binary before attempt hand-out recorded.
const OLDER_LOG_DIRECTORY: &str = "pre-attempt-handout-log";

#[tokio::test]
async fn a_log_without_task_ids_or_failure_guards_replays_to_the_older_state() {
    for snapshot_use in [SnapshotUse::Restore, SnapshotUse::Ignore] {
        let directory = testdata_root().join(OLDER_LOG_DIRECTORY);
        let recorded_text = std::fs::read_to_string(directory.join(STATE_DUMP_FILE))
            .expect("read the older state dump");
        let recorded = parse_state_dump(&recorded_text);
        let (replayed, step_executions) = replay(&directory, snapshot_use).await;
        assert_eq!(
            recorded.keys().collect::<Vec<_>>(),
            replayed.keys().collect::<Vec<_>>(),
            "{snapshot_use:?}: the replay writes and deletes the same rows"
        );
        let changed_rows: Vec<_> = recorded
            .iter()
            .filter(|(key, value)| replayed.get(*key) != Some(*value))
            .map(|(key, _)| key)
            .filter(|(column_family, key)| !is_new_attempt_field_row(column_family, key))
            .collect();
        assert!(
            changed_rows.is_empty(),
            "{snapshot_use:?}: only Step, run, and checkpoint rows gain new fields: {changed_rows:?}"
        );
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
    }
}

/// Step execution rows carry the attempt progress, run records count it in their storage,
/// attempt checkpoints copy both, and history row metadata records the checkpoint sizes.
fn is_new_attempt_field_row(column_family: &str, hex_key: &str) -> bool {
    let key = (0..hex_key.len())
        .step_by(2)
        .map(|index| u8::from_str_radix(&hex_key[index..index + 2], 16).expect("hex key"))
        .collect::<Vec<_>>();
    let Ok(parsed) = parse_run_key(&key) else {
        return false;
    };
    if column_family == ColumnFamily::History.name() {
        return parsed.table == RunTable::Checkpoint;
    }
    column_family == ColumnFamily::Flow.name()
        && matches!(
            parsed.table,
            RunTable::StepExecution | RunTable::RunRecord | RunTable::HistoryRowMetadata
        )
}

/// Starts a node on the older objects and returns its committed state and Step executions.
async fn replay(
    directory: &std::path::Path,
    snapshot_use: SnapshotUse,
) -> (StateDump, Vec<StepExecutionRecord>) {
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
    let step_view = engine
        .committer()
        .committed_view()
        .expect("open a committed view");
    let state = dump_committed_state(view).await;
    let step_executions = tokio::task::spawn_blocking(move || {
        step_view
            .scan(ColumnFamily::Flow, &[], &[0xff; 64], usize::MAX)
            .expect("scan committed rows")
            .into_iter()
            .filter(|(key, _)| {
                parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::StepExecution)
            })
            .map(|(_, value)| {
                StepExecutionRecord::decode(value.as_slice()).expect("Step rows decode")
            })
            .collect::<Vec<_>>()
    })
    .await
    .expect("the scan task completes");
    engine
        .shutdown()
        .await
        .expect("the node releases its lease");
    (state, step_executions)
}
