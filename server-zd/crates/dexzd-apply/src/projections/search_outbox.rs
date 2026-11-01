// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SearchOutbox entries, keyed by proposed_at_micros so speculative and committed apply agree.

use dexzd_keyspace::row_keys::{
    partition_index_range, search_outbox_key, search_outbox_range_after,
    search_outbox_range_through, search_projection_mode_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, IndexKind, RunId, TenantId};
use dexzd_protocol::dex::ErrorSubStatus;
use dexzd_protocol::dexzd::v1::{
    SearchOutboxAckCommand, SearchOutboxRecord, SearchProjectionModeRecord,
    SetSearchProjectionModeCommand,
};

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::state_access::FlowTransaction;

/// Appends one change of a run's search document when the partition's projection is enabled.
pub(crate) fn append_search_outbox_entry(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    tenant_id: TenantId,
    flow_hash: FlowHash,
    run_id: RunId,
    is_purge: bool,
) -> Result<(), ApplyError> {
    // Replayed commands keep their recorded timestamps, which are older than the queue's acks.
    if scope.is_replay {
        return Ok(());
    }
    let partition = transaction.context().partition;
    let mode = read_mode(transaction)?;
    if !mode.enabled {
        return Ok(());
    }
    // An entry at or below the acked seq would be deleted unprojected, so stop instead.
    let seq = u64::try_from(scope.now_micros)
        .ok()
        .filter(|seq| *seq > mode.acked_through_seq)
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!(
                "command timestamp {} cannot follow the acked search outbox seq {}",
                scope.now_micros, mode.acked_through_seq
            ),
        })?;
    transaction.put_message(
        ColumnFamily::PartitionIndex,
        search_outbox_key(partition, seq, flow_hash, run_id),
        &SearchOutboxRecord {
            tenant_id: tenant_id.0,
            is_purge,
        },
    );
    Ok(())
}

/// Stores the partition's mode; disabling deletes every queued entry.
pub(crate) fn apply_search_projection_mode(
    transaction: &mut FlowTransaction<'_>,
    command: &SetSearchProjectionModeCommand,
) -> Result<HandlerResult, ApplyError> {
    let partition = transaction.context().partition;
    let mut mode = read_mode(transaction)?;
    mode.enabled = command.enabled;
    transaction.put_message(
        ColumnFamily::PartitionIndex,
        search_projection_mode_key(partition),
        &mode,
    );
    if !command.enabled {
        let (start, end) = partition_index_range(partition, IndexKind::SearchOutbox);
        transaction.delete_range(ColumnFamily::PartitionIndex, start, end);
    }
    Ok(applied_partition_command())
}

/// Deletes the partition's entries through the acknowledged seq.
pub(crate) fn apply_search_outbox_ack(
    transaction: &mut FlowTransaction<'_>,
    command: &SearchOutboxAckCommand,
) -> Result<HandlerResult, ApplyError> {
    let partition = transaction.context().partition;
    if command.partition != u32::from(partition.0) {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            format!(
                "search outbox ack of partition {} was routed to partition {}",
                command.partition, partition.0
            ),
        )));
    }
    let mut mode = read_mode(transaction)?;
    if !mode.enabled || command.through_seq <= mode.acked_through_seq {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    // Point deletes, because every read must skip each range tombstone left in the memtable.
    let (_, end) = search_outbox_range_through(partition, command.through_seq);
    let (start, _) = search_outbox_range_after(partition, mode.acked_through_seq);
    for (key, _) in transaction.scan(ColumnFamily::PartitionIndex, &start, &end, usize::MAX)? {
        transaction.delete(ColumnFamily::PartitionIndex, key);
    }
    mode.acked_through_seq = command.through_seq;
    transaction.put_message(
        ColumnFamily::PartitionIndex,
        search_projection_mode_key(partition),
        &mode,
    );
    Ok(applied_partition_command())
}

fn read_mode(transaction: &FlowTransaction<'_>) -> Result<SearchProjectionModeRecord, ApplyError> {
    let key = search_projection_mode_key(transaction.context().partition);
    Ok(transaction
        .get_message(ColumnFamily::PartitionIndex, &key, "search projection mode")?
        .unwrap_or_default())
}

fn applied_partition_command() -> HandlerResult {
    HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: Vec::new(),
        run_id: None,
        replayed: None,
    })
}
