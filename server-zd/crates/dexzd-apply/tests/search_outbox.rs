// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Search outbox commands remain deterministic across mode switches, purge, and replay.

use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{
    prefix_range, run_singleton_key, search_outbox_key, search_projection_mode_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId, RunId, RunTable, run_prefix};
use dexzd_protocol::dex::{
    AttributeWrite, IndexConfig, IndexType, SetAttributesRequest, StartFlowRequest, Value, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, FlowStartCommand, RunPurgeCommand, RunRecord, RunSettings,
    SearchOutboxAckCommand, SearchOutboxAckEntry, SearchOutboxRecord, SearchProjectionModeRecord,
    SetSearchProjectionModeCommand, command_reply, flow_command::Command,
};
use prost::Message;
use std::collections::BTreeMap;

#[derive(Default, Clone, PartialEq, Eq, Debug)]
struct MemoryState {
    rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
}

impl FlowStateReader for MemoryState {
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

impl MemoryState {
    fn commit(&mut self, outcome: &ApplyOutcome) {
        for write in &outcome.writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    self.rows
                        .insert((*column_family, key.clone()), value.clone());
                }
                StateWrite::Delete { column_family, key } => {
                    self.rows.remove(&(*column_family, key.clone()));
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => {
                    let doomed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in doomed {
                        self.rows.remove(&key);
                    }
                }
            }
        }
    }
}

struct SearchHarness {
    state: MemoryState,
    commands: Vec<FlowCommand>,
}

impl SearchHarness {
    fn new() -> Self {
        Self {
            state: MemoryState::default(),
            commands: Vec::new(),
        }
    }

    fn apply(&mut self, command: Command) {
        let command = FlowCommand {
            flow_id: "outbox-flow".into(),
            tenant_id: 0,
            run_id: vec![9; 16],
            behavior_version: 1,
            proposed_at_micros: 1_800_000_000_000_000 + self.commands.len() as i64,
            command: Some(command),
            ..Default::default()
        };
        let outcome = apply_flow_command(&context(), &self.state, &command).unwrap();
        assert!(
            !matches!(outcome.reply.reply, Some(command_reply::Reply::Error(_))),
            "{:?}",
            outcome.reply
        );
        self.state.commit(&outcome);
        self.commands.push(command);
    }

    fn mode(&mut self, enabled: bool) {
        self.apply(Command::SetSearchProjectionMode(
            SetSearchProjectionModeCommand { enabled },
        ));
    }

    fn start(&mut self) {
        self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(StartFlowRequest {
                flow_id: "outbox-flow".into(),
                flow_type: "OutboxFlow".into(),
                ..Default::default()
            }),
            settings: Some(RunSettings {
                max_run_state_bytes: 1 << 30,
                max_value_bytes: 16 << 20,
                wire_inline_threshold_bytes: 100,
                history_window_semantic_events: 10000,
                checkpoint_min_written_bytes: 1 << 20,
                checkpoint_max_commands: 10000,
                request_dedup_capacity: 100,
                ..Default::default()
            }),
            ..Default::default()
        }));
    }

    fn write(&mut self, number: i64) {
        self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: "number".into(),
                    value: Some(Value {
                        kind: Some(value::Kind::IntValue(number)),
                    }),
                    index_config: Some(IndexConfig {
                        enable: true,
                        r#type: IndexType::Int as i32,
                        ..Default::default()
                    }),
                    ..Default::default()
                }],
                ..Default::default()
            }),
            ..Default::default()
        }));
    }

    fn pending(&self) -> Option<SearchOutboxRecord> {
        self.state
            .get(
                ColumnFamily::PartitionIndex,
                &search_outbox_key(PartitionId(0), FlowHash(7), RunId([9; 16])),
            )
            .unwrap()
            .map(|bytes| SearchOutboxRecord::decode(bytes.as_slice()).unwrap())
    }

    fn ack(&mut self, version: u64) {
        self.apply(Command::SearchOutboxAck(SearchOutboxAckCommand {
            partition: 0,
            entries: vec![SearchOutboxAckEntry {
                flow_hash: 7,
                run_id: vec![9; 16],
                version,
            }],
        }));
    }
}

#[test]
fn builtin_has_no_outbox_and_enabling_only_captures_subsequent_writes() {
    let mut harness = SearchHarness::new();
    harness.start();
    harness.write(1);
    assert_eq!(harness.pending(), None);
    harness.mode(true);
    assert_eq!(harness.pending(), None);
    harness.write(2);
    assert!(harness.pending().is_some());
    harness.mode(false);
    assert_eq!(harness.pending(), None);
    harness.write(3);
    assert_eq!(harness.pending(), None);
    let mode = harness
        .state
        .get(
            ColumnFamily::PartitionIndex,
            &search_projection_mode_key(PartitionId(0)),
        )
        .unwrap()
        .unwrap();
    assert!(
        !SearchProjectionModeRecord::decode(mode.as_slice())
            .unwrap()
            .enabled
    );
}

#[test]
fn coalesced_updates_survive_stale_acks_and_replay_from_a_snapshot_exactly() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    let initial = harness.pending().unwrap();
    let snapshot = harness.state.clone();
    let snapshot_commands = harness.commands.len();
    for number in 0..100 {
        harness.write(number);
    }
    let latest = harness.pending().unwrap();
    assert!(latest.version > initial.version);
    assert_eq!(latest.pending_since_micros, initial.pending_since_micros);
    harness.ack(initial.version);
    assert_eq!(harness.pending(), Some(latest.clone()));
    let mut recovered = snapshot;
    for command in &harness.commands[snapshot_commands..] {
        let outcome = apply_flow_command(&context(), &recovered, command).unwrap();
        recovered.commit(&outcome);
    }
    assert_eq!(recovered, harness.state);
    harness.ack(latest.version);
    assert_eq!(harness.pending(), None);
}

#[test]
fn purge_tombstone_survives_run_range_deletion_and_stale_ack() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    let previous = harness.pending().unwrap();
    let key = run_singleton_key(FlowHash(7), RunId([9; 16]), RunTable::RunRecord);
    let mut run = RunRecord::decode(
        harness
            .state
            .get(ColumnFamily::Flow, &key)
            .unwrap()
            .unwrap()
            .as_slice(),
    )
    .unwrap();
    run.is_purging = true;
    harness
        .state
        .rows
        .insert((ColumnFamily::Flow, key), run.encode_to_vec());
    harness.apply(Command::RunPurge(RunPurgeCommand {}));
    let pending = harness.pending().unwrap();
    assert!(pending.is_delete);
    assert!(pending.version > previous.version);
    let (start, end) = prefix_range(&run_prefix(FlowHash(7), RunId([9; 16])));
    assert!(
        harness
            .state
            .scan(ColumnFamily::Flow, &start, &end, 1000)
            .unwrap()
            .is_empty()
    );
    harness.ack(previous.version);
    assert_eq!(harness.pending(), Some(pending.clone()));
    harness.ack(pending.version);
    assert_eq!(harness.pending(), None);
}

fn context() -> ApplyContext {
    ApplyContext {
        partition: PartitionId(0),
        flow_hash: FlowHash(7),
    }
}
