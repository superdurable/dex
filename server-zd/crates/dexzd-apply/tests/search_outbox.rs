// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The SearchOutbox queue stays deterministic across mode switches, acks, purge, and replay.

use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{
    SearchOutboxEntry, decode_search_outbox_key, partition_index_range, prefix_range,
    run_singleton_key, search_projection_mode_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, IndexKind, PartitionId, RunId, RunTable, run_prefix};
use dexzd_protocol::dex::{
    AttributeWrite, IndexConfig, IndexType, SetAttributesRequest, StartFlowRequest, Value, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, CommandReply, FlowCommand, FlowStartCommand, RunPurgeCommand, RunRecord,
    RunSettings, SearchOutboxAckCommand, SearchOutboxRecord, SearchProjectionModeRecord,
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

const FIRST_PROPOSED_AT_MICROS: i64 = 1_800_000_000_000_000;

impl SearchHarness {
    fn new() -> Self {
        Self {
            state: MemoryState::default(),
            commands: Vec::new(),
        }
    }

    /// Applies `command` and returns its reply; the state keeps its writes.
    fn apply_with_reply(&mut self, command: Command) -> CommandReply {
        let command = FlowCommand {
            flow_id: "outbox-flow".into(),
            tenant_id: 0,
            run_id: vec![9; 16],
            behavior_version: 1,
            proposed_at_micros: FIRST_PROPOSED_AT_MICROS + self.commands.len() as i64,
            command: Some(command),
            ..Default::default()
        };
        let outcome =
            apply_flow_command(&context(), &self.state, &command).expect("apply succeeds");
        self.state.commit(&outcome);
        self.commands.push(command);
        outcome.reply
    }

    fn apply(&mut self, command: Command) {
        let reply = self.apply_with_reply(command);
        assert!(
            !matches!(reply.reply, Some(command_reply::Reply::Error(_))),
            "{reply:?}"
        );
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

    /// Returns the queued entries of partition 0 in key order.
    fn entries(&self) -> Vec<(SearchOutboxEntry, SearchOutboxRecord)> {
        let (start, end) = partition_index_range(PartitionId(0), IndexKind::SearchOutbox);
        self.state
            .scan(ColumnFamily::PartitionIndex, &start, &end, usize::MAX)
            .expect("memory scans succeed")
            .into_iter()
            .map(|(key, value)| {
                (
                    decode_search_outbox_key(PartitionId(0), &key).expect("outbox keys decode"),
                    SearchOutboxRecord::decode(value.as_slice()).expect("outbox records decode"),
                )
            })
            .collect()
    }

    fn mode_record(&self) -> Option<SearchProjectionModeRecord> {
        self.state
            .get(
                ColumnFamily::PartitionIndex,
                &search_projection_mode_key(PartitionId(0)),
            )
            .expect("memory reads succeed")
            .map(|bytes| {
                SearchProjectionModeRecord::decode(bytes.as_slice()).expect("mode records decode")
            })
    }

    fn ack(&mut self, through_seq: u64) -> CommandReply {
        self.apply_with_reply(Command::SearchOutboxAck(SearchOutboxAckCommand {
            partition: 0,
            through_seq,
        }))
    }

    /// Returns the seq that the next applied command appends with.
    fn next_seq(&self) -> u64 {
        FIRST_PROPOSED_AT_MICROS.unsigned_abs() + self.commands.len() as u64
    }
}

#[test]
fn builtin_appends_nothing_and_enabling_captures_only_later_changes() {
    let mut harness = SearchHarness::new();
    harness.start();
    harness.write(1);
    assert!(harness.entries().is_empty());
    assert_eq!(harness.mode_record(), None);
    harness.mode(true);
    assert!(harness.entries().is_empty());
    let first_seq = harness.next_seq();
    harness.write(2);
    let entries = harness.entries();
    assert_eq!(entries.len(), 1);
    assert_eq!(entries[0].0.seq, first_seq);
    assert_eq!(entries[0].0.flow_hash, FlowHash(7));
    assert_eq!(entries[0].0.run_id, RunId([9; 16]));
    assert!(!entries[0].1.is_purge);
    harness.mode(false);
    assert!(harness.entries().is_empty());
    harness.write(3);
    assert!(harness.entries().is_empty());
    assert!(!harness.mode_record().unwrap().enabled);
}

#[test]
fn a_hot_run_appends_one_entry_per_change_in_fifo_order() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    let first_seq = harness.next_seq();
    harness.start();
    for number in 0..100 {
        harness.write(number);
    }
    let seqs: Vec<u64> = harness
        .entries()
        .iter()
        .map(|(entry, _)| entry.seq)
        .collect();
    assert_eq!(seqs, (first_seq..first_seq + 101).collect::<Vec<_>>());
}

#[test]
fn acks_range_delete_through_their_seq_and_lower_acks_change_nothing() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    for number in 0..10 {
        harness.write(number);
    }
    let seqs: Vec<u64> = harness
        .entries()
        .iter()
        .map(|(entry, _)| entry.seq)
        .collect();
    let reply = harness.ack(seqs[4]);
    assert!(matches!(reply.reply, Some(command_reply::Reply::Ok(_))));
    let remaining: Vec<u64> = harness
        .entries()
        .iter()
        .map(|(entry, _)| entry.seq)
        .collect();
    assert_eq!(remaining, seqs[5..]);
    assert_eq!(harness.mode_record().unwrap().acked_through_seq, seqs[4]);

    let before_lower_ack = harness.state.clone();
    let reply = harness.ack(seqs[2]);
    assert!(matches!(reply.reply, Some(command_reply::Reply::Ok(_))));
    assert_eq!(
        harness.state, before_lower_ack,
        "a lower ack writes nothing"
    );

    harness.ack(*seqs.last().unwrap());
    assert!(harness.entries().is_empty());
    harness.write(99);
    assert_eq!(harness.entries().len(), 1);
}

#[test]
fn an_append_at_or_below_the_acked_seq_fails_closed() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    let acked_seq = harness.entries().last().unwrap().0.seq;
    harness.ack(acked_seq);
    let indexed = FlowCommand {
        flow_id: "outbox-flow".into(),
        run_id: vec![9; 16],
        behavior_version: 1,
        proposed_at_micros: i64::try_from(acked_seq).unwrap(),
        command: Some(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: "number".into(),
                    value: Some(Value {
                        kind: Some(value::Kind::IntValue(1)),
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
        })),
        ..Default::default()
    };
    let error = apply_flow_command(&context(), &harness.state, &indexed)
        .expect_err("a stamp at the acked seq cannot append");
    assert!(
        error.to_string().contains("acked search outbox seq"),
        "{error}"
    );
}

#[test]
fn an_ack_at_or_above_its_own_seq_is_rejected_without_writes() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    let before = harness.state.clone();
    let own_seq = harness.next_seq();
    for through_seq in [own_seq, own_seq + 1, u64::MAX] {
        let reply = harness.ack(through_seq);
        assert!(
            matches!(reply.reply, Some(command_reply::Reply::Error(_))),
            "{reply:?}"
        );
        assert_eq!(harness.state, before);
    }
}

#[test]
fn enabling_records_the_enabling_seq_until_the_next_disable() {
    let mut harness = SearchHarness::new();
    let first_enable = harness.next_seq();
    harness.mode(true);
    assert_eq!(harness.mode_record().unwrap().enabled_at_seq, first_enable);
    harness.mode(true);
    assert_eq!(
        harness.mode_record().unwrap().enabled_at_seq,
        first_enable,
        "re-enabling an enabled partition keeps its seq"
    );
    harness.mode(false);
    let second_enable = harness.next_seq();
    harness.mode(true);
    assert_eq!(harness.mode_record().unwrap().enabled_at_seq, second_enable);
}

#[test]
fn an_ack_routed_to_another_partition_is_rejected_without_writes() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    let before = harness.state.clone();
    let reply = harness.apply_with_reply(Command::SearchOutboxAck(SearchOutboxAckCommand {
        partition: 3,
        through_seq: u64::MAX,
    }));
    assert!(matches!(reply.reply, Some(command_reply::Reply::Error(_))));
    assert_eq!(harness.state, before);
}

#[test]
fn disabling_keeps_the_acked_seq_and_reenabling_appends_after_it() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    harness.write(1);
    let acked = harness.entries().last().unwrap().0.seq;
    harness.ack(acked);
    harness.write(2);
    harness.mode(false);
    assert!(harness.entries().is_empty());
    let mode = harness.mode_record().unwrap();
    assert!(!mode.enabled);
    assert_eq!(mode.acked_through_seq, acked);
    assert!(matches!(
        harness.ack(acked + 1).reply,
        Some(command_reply::Reply::Ok(_))
    ));
    assert_eq!(harness.mode_record().unwrap().acked_through_seq, acked);
    harness.mode(true);
    harness.write(3);
    let entries = harness.entries();
    assert_eq!(entries.len(), 1);
    assert!(entries[0].0.seq > acked);
}

#[test]
fn replay_from_a_snapshot_rebuilds_the_queue_exactly() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    let snapshot = harness.state.clone();
    let snapshot_commands = harness.commands.len();
    for number in 0..20 {
        harness.write(number);
        if number % 7 == 6 {
            let through = harness.entries()[2].0.seq;
            harness.ack(through);
        }
    }
    harness.mode(false);
    harness.mode(true);
    harness.write(100);
    let mut recovered = snapshot;
    for command in &harness.commands[snapshot_commands..] {
        let outcome = apply_flow_command(&context(), &recovered, command).unwrap();
        recovered.commit(&outcome);
    }
    assert_eq!(recovered, harness.state);
    assert_eq!(harness.entries().len(), 1);
}

#[test]
fn purge_appends_a_purge_entry_that_survives_the_run_range_deletion() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
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
    let purge_seq = harness.next_seq();
    harness.apply(Command::RunPurge(RunPurgeCommand {}));
    let entries = harness.entries();
    let (purge_entry, purge_record) = entries.last().unwrap();
    assert_eq!(purge_entry.seq, purge_seq);
    assert!(purge_record.is_purge);
    let (start, end) = prefix_range(&run_prefix(FlowHash(7), RunId([9; 16])));
    assert!(
        harness
            .state
            .scan(ColumnFamily::Flow, &start, &end, 1000)
            .unwrap()
            .is_empty()
    );
    harness.ack(purge_seq);
    assert!(harness.entries().is_empty());
}

#[test]
fn acks_and_other_flows_between_speculative_and_committed_apply_keep_the_outcome() {
    let mut harness = SearchHarness::new();
    harness.mode(true);
    harness.start();
    harness.write(1);
    let next_write = FlowCommand {
        flow_id: "outbox-flow".into(),
        run_id: vec![9; 16],
        behavior_version: 1,
        proposed_at_micros: FIRST_PROPOSED_AT_MICROS + 1_000,
        command: Some(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: "number".into(),
                    value: Some(Value {
                        kind: Some(value::Kind::IntValue(2)),
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
        })),
        ..Default::default()
    };
    let speculative = apply_flow_command(&context(), &harness.state, &next_write).unwrap();

    // Another Flow of the partition appends, and an ack deletes queued entries, before the commit.
    let other_context = ApplyContext {
        partition: PartitionId(0),
        flow_hash: FlowHash(8),
    };
    let other_start = FlowCommand {
        flow_id: "other-flow".into(),
        behavior_version: 1,
        proposed_at_micros: FIRST_PROPOSED_AT_MICROS + 500,
        command: Some(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![8; 16],
            request: Some(StartFlowRequest {
                flow_id: "other-flow".into(),
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
        })),
        ..Default::default()
    };
    let other_outcome = apply_flow_command(&other_context, &harness.state, &other_start).unwrap();
    harness.state.commit(&other_outcome);
    let through = harness.entries().last().unwrap().0.seq;
    let ack = FlowCommand {
        flow_id: "outbox-flow".into(),
        behavior_version: 1,
        proposed_at_micros: FIRST_PROPOSED_AT_MICROS + 999,
        command: Some(Command::SearchOutboxAck(SearchOutboxAckCommand {
            partition: 0,
            through_seq: through,
        })),
        ..Default::default()
    };
    let ack_outcome = apply_flow_command(&context(), &harness.state, &ack).unwrap();
    harness.state.commit(&ack_outcome);
    assert!(harness.entries().is_empty());

    let committed = apply_flow_command(&context(), &harness.state, &next_write).unwrap();
    assert_eq!(speculative.writes, committed.writes);
    assert_eq!(speculative.effects, committed.effects);
    assert_eq!(
        speculative.reply.encode_to_vec(),
        committed.reply.encode_to_vec()
    );
}

fn context() -> ApplyContext {
    ApplyContext {
        partition: PartitionId(0),
        flow_hash: FlowHash(7),
    }
}
