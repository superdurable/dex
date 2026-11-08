// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::fmt;

use dexzd_apply::diagnostics::SemanticPointHash;
use dexzd_apply::{ApplyOutcome, StateWrite};
use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistoryRowMetadata, LogPosition, flow_command::Command,
};
use dexzd_storage::{LocalReadView, ScanDirection, StorageError};
use prost::Message;

use crate::flow_coordination::CommittedCommand;

/// Two applications of the same command to the same state produced different outcomes.
#[derive(Debug, Clone, PartialEq, thiserror::Error)]
pub struct ReplayMismatch {
    /// Which comparison failed.
    pub check: ReplayCheck,
    /// First outcome.
    pub expected: Box<ApplyOutcome>,
    /// Second outcome.
    pub actual: Box<ApplyOutcome>,
    /// The stamped command that produced the mismatch.
    pub command: Box<FlowCommand>,
    /// Partition that applied the command.
    pub partition: PartitionId,
    /// Committed log position of the command.
    pub position: LogPosition,
}

/// The replay comparison that detected a mismatch.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReplayCheck {
    /// The applier applied the command twice to committed state.
    RepeatedApply,
    /// The runtime's speculative outcome differs from the committed outcome.
    SpeculativeVersusCommitted,
}

impl fmt::Display for ReplayMismatch {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        let check = match self.check {
            ReplayCheck::RepeatedApply => "repeated apply",
            ReplayCheck::SpeculativeVersusCommitted => "speculative versus committed apply",
        };
        write!(
            formatter,
            "{check} mismatch for partition {} seq {} {} Flow {} command {}: {}",
            self.partition.0,
            self.position.seq,
            command_kind(&self.command),
            self.command.flow_id,
            hex(&self.command.command_id),
            first_difference(&self.expected, &self.actual)
        )
    }
}

pub(crate) fn compare_repeated_apply(
    command: &FlowCommand,
    partition: PartitionId,
    position: &LogPosition,
    first: &ApplyOutcome,
    second: &ApplyOutcome,
) -> Result<(), ReplayMismatch> {
    compare_outcomes(
        ReplayCheck::RepeatedApply,
        command,
        partition,
        position,
        first,
        second,
    )
}

pub(crate) fn compare_speculative_outcome(
    speculative: &ApplyOutcome,
    command: &FlowCommand,
    partition: PartitionId,
    committed: &CommittedCommand,
) -> Result<(), ReplayMismatch> {
    compare_outcomes(
        ReplayCheck::SpeculativeVersusCommitted,
        command,
        partition,
        &committed.position,
        speculative,
        &committed.outcome,
    )
}

fn compare_outcomes(
    check: ReplayCheck,
    command: &FlowCommand,
    partition: PartitionId,
    position: &LogPosition,
    expected: &ApplyOutcome,
    actual: &ApplyOutcome,
) -> Result<(), ReplayMismatch> {
    if expected.writes == actual.writes
        && expected.effects == actual.effects
        && expected.run_id == actual.run_id
        && expected.reply.encode_to_vec() == actual.reply.encode_to_vec()
    {
        return Ok(());
    }
    Err(ReplayMismatch {
        check,
        expected: Box::new(expected.clone()),
        actual: Box::new(actual.clone()),
        command: Box::new(command.clone()),
        partition,
        position: position.clone(),
    })
}

fn first_difference(expected: &ApplyOutcome, actual: &ApplyOutcome) -> String {
    for (index, (first, second)) in expected.writes.iter().zip(&actual.writes).enumerate() {
        if first != second {
            let first_key = write_key(first);
            let second_key = write_key(second);
            if first_key != second_key {
                return format!("write {index} key {first_key} versus {second_key}");
            }
            if let (
                StateWrite::Put {
                    value: first_value, ..
                },
                StateWrite::Put {
                    value: second_value,
                    ..
                },
            ) = (first, second)
            {
                return format!(
                    "write {index} at {first_key} differs: {}",
                    value_difference(first_value, second_value)
                );
            }
            return format!("write {index} at {first_key} changes operation type");
        }
    }
    if expected.writes.len() != actual.writes.len() {
        let extra = expected
            .writes
            .get(actual.writes.len())
            .or_else(|| actual.writes.get(expected.writes.len()));
        return format!(
            "write count {} versus {} at {}",
            expected.writes.len(),
            actual.writes.len(),
            extra.map_or_else(|| "unknown key".to_owned(), write_key)
        );
    }
    if expected.effects != actual.effects {
        return format!(
            "effects differ: {:?} versus {:?}",
            expected.effects, actual.effects
        );
    }
    if expected.run_id != actual.run_id {
        return format!(
            "run ID differs: {:?} versus {:?}",
            expected.run_id, actual.run_id
        );
    }
    "reply bytes differ".to_owned()
}

fn value_difference(first: &[u8], second: &[u8]) -> String {
    if let Some(offset) = first
        .iter()
        .zip(second)
        .position(|(first_byte, second_byte)| first_byte != second_byte)
    {
        return format!(
            "byte {offset} is {:02x} versus {:02x}; lengths {} versus {}",
            first[offset],
            second[offset],
            first.len(),
            second.len()
        );
    }
    format!("lengths {} versus {}", first.len(), second.len())
}

fn write_key(write: &StateWrite) -> String {
    match write {
        StateWrite::Put {
            column_family, key, ..
        }
        | StateWrite::Delete { column_family, key }
        | StateWrite::SingleDelete { column_family, key } => {
            format!("{column_family:?}/{}", hex(key))
        }
        StateWrite::DeleteRange {
            column_family,
            start,
            end,
        } => format!("{column_family:?}/{}..{}", hex(start), hex(end)),
    }
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

fn command_kind(command: &FlowCommand) -> &'static str {
    match &command.command {
        None => "None",
        Some(Command::FlowStart(_)) => "FlowStart",
        Some(Command::FlowStop(_)) => "FlowStop",
        Some(Command::FlowConfigUpdate(_)) => "FlowConfigUpdate",
        Some(Command::AttributesSet(_)) => "AttributesSet",
        Some(Command::ChannelPublish(_)) => "ChannelPublish",
        Some(Command::ChannelMessageDelete(_)) => "ChannelMessageDelete",
        Some(Command::StepAttemptStarted(_)) => "StepAttemptStarted",
        Some(Command::StepAttemptHeartbeat(_)) => "StepAttemptHeartbeat",
        Some(Command::StepAttemptFailed(_)) => "StepAttemptFailed",
        Some(Command::StepMethodCompleted(_)) => "StepMethodCompleted",
        Some(Command::StepAttemptReturned(_)) => "StepAttemptReturned",
        Some(Command::StepAttemptQueued(_)) => "StepAttemptQueued",
        Some(Command::RpcCommitted(_)) => "RpcCommitted",
        Some(Command::TimersFired(_)) => "TimersFired",
        Some(Command::TimerSkip(_)) => "TimerSkip",
        Some(Command::TimeTravel(_)) => "TimeTravel",
        Some(Command::TimeTravelStage(_)) => "TimeTravelStage",
        Some(Command::InboxMessage(_)) => "InboxMessage",
        Some(Command::OutboxAck(_)) => "OutboxAck",
        Some(Command::AttributeSyncAck(_)) => "AttributeSyncAck",
        Some(Command::PendingCloseCheck(_)) => "PendingCloseCheck",
        Some(Command::SearchOutboxAck(_)) => "SearchOutboxAck",
        Some(Command::SetSearchProjectionMode(_)) => "SetSearchProjectionMode",
        Some(Command::SetHistoryOffloadMode(_)) => "SetHistoryOffloadMode",
        Some(Command::HardDeleteObjectsDeleted(_)) => "HardDeleteObjectsDeleted",
        Some(Command::RunPurge(_)) => "RunPurge",
        Some(Command::HistorySegmentUploaded(_)) => "HistorySegmentUploaded",
        Some(Command::ValueRevive(_)) => "ValueRevive",
        Some(Command::HistorySegmentObjectsDeleted(_)) => "HistorySegmentObjectsDeleted",
    }
}

/// History metadata rows one scan of the pre-replay semantic points reads.
const METADATA_SCAN_ROWS: usize = 256;

/// Returns the semantic points that the history metadata of `runs` lists, by run, attempt,
/// and seq, including points whose records offload deleted locally.
///
/// Returns an error when the store cannot be read or holds an invalid row.
pub(crate) fn semantic_metadata_points(
    store: &dyn LocalReadView,
    runs: &BTreeSet<(FlowHash, RunId)>,
) -> Result<BTreeSet<(RunId, u32, u64)>, StorageError> {
    let mut points = BTreeSet::new();
    for (flow_hash, run_id) in runs {
        let prefix = run_table_prefix(*flow_hash, *run_id, RunTable::HistoryRowMetadata);
        let (mut start, end) = prefix_range(&prefix);
        loop {
            let rows = store.scan(
                ColumnFamily::Flow,
                Some(&start),
                Some(&end),
                ScanDirection::Forward,
                METADATA_SCAN_ROWS,
            )?;
            for (key, value) in &rows {
                let suffix = &key[prefix.len()..];
                if suffix.len() != 13 || suffix[8] != RunTable::HistoryRecord as u8 {
                    continue;
                }
                let metadata = HistoryRowMetadata::decode(value.as_slice())?;
                if metadata.is_semantic {
                    let seq = u64::from_be_bytes(suffix[..8].try_into().expect("eight seq bytes"));
                    points.insert((*run_id, metadata.attempt, seq));
                }
            }
            let Some((last_key, _)) = rows.last() else {
                break;
            };
            if rows.len() < METADATA_SCAN_ROWS {
                break;
            }
            start.clone_from(last_key);
            start.push(0);
        }
    }
    Ok(points)
}

pub(crate) fn compare_semantic_point_hash(
    expected: &[u8; 16],
    reconstructed: &SemanticPointHash,
    command: &FlowCommand,
    position: &LogPosition,
) -> Result<(), String> {
    if expected == &reconstructed.state_hash {
        return Ok(());
    }
    Err(format!(
        "time-travel reconstruction differs for Flow {} command {} at log seq {} semantic key ({}, {}, {}): expected hash {}, actual hash {}",
        command.flow_id,
        hex(&command.command_id),
        position.seq,
        reconstructed.run_id,
        reconstructed.run_attempt,
        reconstructed.run_seq,
        hex(expected),
        hex(&reconstructed.state_hash)
    ))
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;

    use dexzd_apply::diagnostics::SemanticPointHash;
    use dexzd_keyspace::row_keys::history_row_metadata_key;
    use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable};
    use dexzd_protocol::dexzd::v1::{FlowCommand, HistoryRowMetadata, LogPosition};
    use dexzd_storage::{InMemoryLocalStore, LocalStore, WriteBatch};
    use prost::Message;

    use super::{METADATA_SCAN_ROWS, compare_semantic_point_hash, semantic_metadata_points};

    // Offload deletes uploaded records but keeps semantic metadata, which time travel targets.
    #[test]
    fn semantic_points_come_from_metadata_of_records_offload_deleted() {
        let store = InMemoryLocalStore::new();
        let flow_hash = FlowHash(7);
        let run_id = RunId([9; 16]);
        let other_run = RunId([8; 16]);
        let mut batch = WriteBatch::new();
        let mut expected = BTreeSet::new();
        let seq_count = u64::try_from(METADATA_SCAN_ROWS).expect("small") * 2 + 3;
        for seq in 1..=seq_count {
            let is_semantic = seq % 3 != 0;
            let attempt = if seq > seq_count / 2 { 2 } else { 1 };
            let metadata = HistoryRowMetadata {
                is_semantic,
                attempt,
                ..HistoryRowMetadata::default()
            };
            batch.put(
                ColumnFamily::Flow,
                history_row_metadata_key(flow_hash, run_id, seq, RunTable::HistoryRecord, 0),
                metadata.encode_to_vec(),
            );
            batch.put(
                ColumnFamily::Flow,
                history_row_metadata_key(flow_hash, run_id, seq, RunTable::Checkpoint, attempt),
                HistoryRowMetadata {
                    is_semantic: true,
                    ..HistoryRowMetadata::default()
                }
                .encode_to_vec(),
            );
            if is_semantic {
                expected.insert((run_id, attempt, seq));
            }
        }
        batch.put(
            ColumnFamily::Flow,
            history_row_metadata_key(flow_hash, other_run, 5, RunTable::HistoryRecord, 0),
            HistoryRowMetadata {
                is_semantic: true,
                attempt: 1,
                ..HistoryRowMetadata::default()
            }
            .encode_to_vec(),
        );
        store.write(batch).expect("the memory store writes");

        let points = semantic_metadata_points(&store, &BTreeSet::from([(flow_hash, run_id)]))
            .expect("the metadata scans");
        assert_eq!(points, expected);
    }

    #[test]
    fn corrupted_reconstruction_names_the_command_and_semantic_key() {
        let reconstructed = SemanticPointHash {
            run_id: RunId([9; 16]),
            run_attempt: 2,
            run_seq: 7,
            state_hash: [2; 16],
            window_start_seq: 1,
            genesis_seq: 1,
        };
        let command = FlowCommand {
            flow_id: "order-flow".to_owned(),
            command_id: vec![3; 16],
            ..FlowCommand::default()
        };
        let error = compare_semantic_point_hash(
            &[1; 16],
            &reconstructed,
            &command,
            &LogPosition {
                seq: 11,
                ..LogPosition::default()
            },
        )
        .expect_err("corrupted reconstruction differs");
        assert!(error.contains("order-flow command 0303"));
        assert!(error.contains("semantic key ("));
        assert!(error.contains(", 2, 7)"));
    }
}
