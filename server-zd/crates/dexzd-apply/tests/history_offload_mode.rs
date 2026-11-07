// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The partition's history offload mode switches idempotently and only on its own partition.

use std::collections::BTreeMap;

use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::history_offload_mode_key;
use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistoryOffloadModeRecord, SetHistoryOffloadModeCommand, command_reply,
    flow_command::Command,
};
use prost::Message;

const FIRST_PROPOSED_AT_MICROS: i64 = 1_800_000_000_000_000;

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

struct ModeHarness {
    state: MemoryState,
    applied_count: i64,
}

impl ModeHarness {
    fn new() -> Self {
        Self {
            state: MemoryState::default(),
            applied_count: 0,
        }
    }

    /// Applies the mode command on `routed_to`, commits its writes, and returns the outcome.
    fn apply(
        &mut self,
        routed_to: PartitionId,
        named_partition: u32,
        is_enabled: bool,
    ) -> ApplyOutcome {
        let command = FlowCommand {
            flow_id: "partition-command-flow".into(),
            behavior_version: 1,
            proposed_at_micros: FIRST_PROPOSED_AT_MICROS + self.applied_count,
            command: Some(Command::SetHistoryOffloadMode(
                SetHistoryOffloadModeCommand {
                    enabled: is_enabled,
                    partition: named_partition,
                },
            )),
            ..FlowCommand::default()
        };
        let context = ApplyContext {
            partition: routed_to,
            flow_hash: FlowHash(7),
        };
        let outcome = apply_flow_command(&context, &self.state, &command).expect("apply succeeds");
        self.applied_count += 1;
        for write in &outcome.writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    self.state
                        .rows
                        .insert((*column_family, key.clone()), value.clone());
                }
                StateWrite::Delete { column_family, key }
                | StateWrite::SingleDelete { column_family, key } => {
                    self.state.rows.remove(&(*column_family, key.clone()));
                }
                StateWrite::DeleteRange { .. } => {
                    panic!("a mode command never deletes a range: {write:?}")
                }
            }
        }
        outcome
    }

    /// Applies the mode command on its own partition and requires an OK reply.
    fn set_mode(&mut self, partition: PartitionId, is_enabled: bool) -> ApplyOutcome {
        let outcome = self.apply(partition, u32::from(partition.0), is_enabled);
        assert!(!is_error(&outcome), "{:?}", outcome.reply);
        outcome
    }

    fn mode_record(&self, partition: PartitionId) -> Option<HistoryOffloadModeRecord> {
        self.state
            .get(
                ColumnFamily::PartitionIndex,
                &history_offload_mode_key(partition),
            )
            .expect("memory reads succeed")
            .map(|bytes| {
                HistoryOffloadModeRecord::decode(bytes.as_slice()).expect("mode records decode")
            })
    }
}

#[test]
fn enabling_writes_the_record_and_enabling_again_writes_nothing() {
    let mut harness = ModeHarness::new();
    let partition = PartitionId(2);
    assert_eq!(
        harness.mode_record(partition),
        None,
        "absent means disabled"
    );

    let enabled = harness.set_mode(partition, true);
    assert_eq!(enabled.writes.len(), 1);
    assert_eq!(
        harness.mode_record(partition),
        Some(HistoryOffloadModeRecord { enabled: true })
    );

    let before = harness.state.clone();
    let repeated = harness.set_mode(partition, true);
    assert!(repeated.writes.is_empty(), "{:?}", repeated.writes);
    assert!(repeated.effects.is_empty());
    assert_eq!(harness.state, before);
}

#[test]
fn disabling_deletes_the_record_and_disabling_again_writes_nothing() {
    let mut harness = ModeHarness::new();
    let partition = PartitionId(2);
    let never_enabled = harness.set_mode(partition, false);
    assert!(
        never_enabled.writes.is_empty(),
        "{:?}",
        never_enabled.writes
    );

    harness.set_mode(partition, true);
    let disabled = harness.set_mode(partition, false);
    assert_eq!(disabled.writes.len(), 1);
    assert_eq!(harness.mode_record(partition), None);
    assert!(harness.state.rows.is_empty());

    let repeated = harness.set_mode(partition, false);
    assert!(repeated.writes.is_empty(), "{:?}", repeated.writes);
    assert!(harness.state.rows.is_empty());

    harness.set_mode(partition, true);
    assert_eq!(
        harness.mode_record(partition),
        Some(HistoryOffloadModeRecord { enabled: true }),
        "re-enabling after a disable writes the record again"
    );
}

#[test]
fn a_mode_routed_to_another_partition_is_rejected_without_writes() {
    let mut harness = ModeHarness::new();
    harness.set_mode(PartitionId(2), true);
    let before = harness.state.clone();
    for is_enabled in [true, false] {
        let rejected = harness.apply(PartitionId(2), 3, is_enabled);
        assert!(is_error(&rejected), "{:?}", rejected.reply);
        assert!(rejected.writes.is_empty(), "{:?}", rejected.writes);
        assert_eq!(harness.state, before);
    }
}

#[test]
fn each_partition_keeps_its_own_mode() {
    let mut harness = ModeHarness::new();
    harness.set_mode(PartitionId(0), true);
    harness.set_mode(PartitionId(1), true);
    harness.set_mode(PartitionId(1), false);
    assert_eq!(
        harness.mode_record(PartitionId(0)),
        Some(HistoryOffloadModeRecord { enabled: true })
    );
    assert_eq!(harness.mode_record(PartitionId(1)), None);
}

fn is_error(outcome: &ApplyOutcome) -> bool {
    matches!(outcome.reply.reply, Some(command_reply::Reply::Error(_)))
}
