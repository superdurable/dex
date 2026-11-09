// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Replays a log whose TimeTravel results an owner staged before apply deleted ended Step rows.

use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::RunRecord;
use prost::Message;

use crate::golden_replay::SnapshotUse;
use crate::old_log_replay::replay_with_checks;
use crate::state_dump::parse_state_dump;
use crate::testdata_tree::{STATE_DUMP_FILE, testdata_root};

/// A log that server-zd at `e2e451513` recorded; see its README. Its `travel-flow` TimeTravel
/// stages a result that still holds the rows of completed Steps.
const STAGED_TIME_TRAVEL_LOG_DIRECTORY: &str = "pre-offload-choice-log";

#[tokio::test]
async fn an_older_staged_time_travel_passes_the_debug_replay_checks() {
    let directory = testdata_root().join(STAGED_TIME_TRAVEL_LOG_DIRECTORY);
    let recorded = parse_state_dump(
        &std::fs::read_to_string(directory.join(STATE_DUMP_FILE))
            .expect("read the recorded state dump"),
    );
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
        let removed_tables: Vec<_> = recorded
            .keys()
            .filter(|key| !checked.contains_key(*key))
            .map(|(column_family, hex_key)| {
                let key: Vec<u8> = (0..hex_key.len())
                    .step_by(2)
                    .map(|index| {
                        u8::from_str_radix(&hex_key[index..index + 2], 16).expect("hex keys")
                    })
                    .collect();
                (
                    column_family.clone(),
                    parse_run_key(&key).map(|parsed| parsed.table),
                )
            })
            .collect();
        assert!(!removed_tables.is_empty());
        assert!(
            removed_tables
                .iter()
                .all(
                    |(column_family, table)| column_family == ColumnFamily::Flow.name()
                        && *table == Ok(RunTable::StepExecution)
                ),
            "{snapshot_use:?}: the replay removes only Step rows: {removed_tables:?}"
        );
    }
}
