// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Applies a command to runs in the snapshot that a binary before the event-counted window
//! wrote: each running run's window counters must start from its semantic counters.

use std::collections::BTreeMap;

use dexzd_apply::{
    ApplyContext, BehaviorVersion, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::history_row_metadata_key;
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, parse_run_key};
use dexzd_protocol::dex::{AttributeWrite, FlowStatus, SetAttributesRequest, Value, value};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, HistoryRowMetadata, RunRecord, command_reply,
    flow_command::Command,
};
use prost::Message;

use crate::format_decoding::{decode_objects, memory_snapshot_rows};
use crate::testdata_tree::{OBJECTS_DIRECTORY, read_files, testdata_root};

/// The format-compat log that the last binary before attempt hand-out recorded.
const OLDER_LOG_DIRECTORY: &str = "pre-attempt-handout-log";

/// Committed rows of the older snapshot.
struct SnapshotState {
    rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
}

impl FlowStateReader for SnapshotState {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(self.rows.get(&(column_family, key.to_vec())).cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(self
            .rows
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect())
    }
}

#[test]
fn a_running_run_from_an_older_snapshot_starts_its_window_counters_from_its_semantic_counters() {
    let objects = read_files(
        &testdata_root()
            .join(OLDER_LOG_DIRECTORY)
            .join(OBJECTS_DIRECTORY),
    );
    let decoded = decode_objects(&objects);
    let hasher = PartitionHasher::new(
        &decoded.cluster.partition_hash_key,
        decoded.cluster.partition_count,
    )
    .expect("the recorded cluster has a valid partition hasher");
    let mut state = SnapshotState {
        rows: BTreeMap::new(),
    };
    for (key, body) in &objects {
        if key.contains("/files/memory-") {
            for (column_family, row_key, row_value) in memory_snapshot_rows(body) {
                state.rows.insert((column_family, row_key), row_value);
            }
        }
    }
    let older_runs: Vec<RunRecord> = state
        .rows
        .iter()
        .filter(|((column_family, key), _)| {
            *column_family == ColumnFamily::Flow
                && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::RunRecord)
        })
        .map(|(_, value)| RunRecord::decode(value.as_slice()).expect("run records decode"))
        .filter(|run| run.status == FlowStatus::Running as i32 && !run.is_purging)
        .collect();
    assert!(
        !older_runs.is_empty(),
        "the older snapshot holds a running run"
    );
    let latest_command_micros = decoded
        .commands
        .iter()
        .map(|command| command.proposed_at_micros)
        .max()
        .expect("the log holds commands");
    for older in older_runs {
        assert_eq!(older.window_event_count, None, "{}", older.flow_id);
        let flow_hash = hasher.flow_hash(TenantId(older.tenant_id), &older.flow_id);
        let context = ApplyContext {
            partition: hasher.partition_of(flow_hash),
            flow_hash,
        };
        let command = FlowCommand {
            flow_id: older.flow_id.clone(),
            run_id: older.run_id.clone(),
            tenant_id: older.tenant_id,
            behavior_version: BehaviorVersion::CURRENT.number(),
            proposed_at_micros: latest_command_micros + 1_000_000,
            command_id: vec![7; 16],
            command: Some(Command::AttributesSet(AttributesSetCommand {
                request: Some(SetAttributesRequest {
                    attributes: vec![AttributeWrite {
                        key: "window-check".to_owned(),
                        value: Some(Value {
                            kind: Some(value::Kind::StringValue("after upgrade".to_owned())),
                        }),
                        ..AttributeWrite::default()
                    }],
                    ..SetAttributesRequest::default()
                }),
                ..AttributesSetCommand::default()
            })),
            ..FlowCommand::default()
        };
        let outcome = apply_flow_command(&context, &state, &command).expect("the command applies");
        assert!(
            matches!(outcome.reply.reply, Some(command_reply::Reply::Ok(_))),
            "{}: {:?}",
            older.flow_id,
            outcome.reply
        );
        let run_id = RunId::from_slice(&older.run_id).expect("a 16-byte run ID");
        let run: RunRecord = put_row(
            &outcome.writes,
            &dexzd_keyspace::row_keys::run_singleton_key(flow_hash, run_id, RunTable::RunRecord),
        );
        // Older records count one per semantic record, and the write adds its one event.
        assert_eq!(
            run.window_event_count,
            Some(older.semantic_event_count + 1),
            "{}",
            older.flow_id
        );
        assert_eq!(run.semantic_event_count, older.semantic_event_count + 1);
        assert_eq!(
            run.pruned_window_event_count, older.pruned_semantic_event_count,
            "{}",
            older.flow_id
        );
        // The recorded window size now counts events.
        assert_eq!(
            run.settings.map(|settings| settings.history_window_events),
            older
                .settings
                .map(|settings| settings.history_window_events)
        );
        let metadata: HistoryRowMetadata = put_row(
            &outcome.writes,
            &history_row_metadata_key(
                flow_hash,
                run_id,
                older.next_run_seq,
                RunTable::HistoryRecord,
                0,
            ),
        );
        assert_eq!(
            metadata.window_event_count_before,
            Some(older.semantic_event_count),
            "{}",
            older.flow_id
        );
    }
}

/// Decodes the row that `writes` puts at `key` in the Flow column family.
fn put_row<RowMessage: Message + Default>(writes: &[StateWrite], key: &[u8]) -> RowMessage {
    writes
        .iter()
        .rev()
        .find_map(|write| match write {
            StateWrite::Put {
                column_family: ColumnFamily::Flow,
                key: written_key,
                value,
            } if written_key.as_slice() == key => Some(value),
            _ => None,
        })
        .map(|value| RowMessage::decode(value.as_slice()).expect("the written row decodes"))
        .expect("the command writes the row")
}
