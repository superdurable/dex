// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The partition's committed history offload mode; apply cannot read configuration.

use dexzd_keyspace::ColumnFamily;
use dexzd_keyspace::row_keys::history_offload_mode_key;
use dexzd_protocol::dex::ErrorSubStatus;
use dexzd_protocol::dexzd::v1::{HistoryOffloadModeRecord, SetHistoryOffloadModeCommand};

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::{HandlerOutput, HandlerResult};
use crate::state_access::FlowTransaction;

/// Stores the partition's mode: enabling writes the record and disabling deletes it.
///
/// Returns an applied result without writes when the mode is unchanged. Returns a rejection
/// without writes when the command names another partition than the one it was routed to.
pub(crate) fn apply_history_offload_mode(
    transaction: &mut FlowTransaction<'_>,
    command: &SetHistoryOffloadModeCommand,
) -> Result<HandlerResult, ApplyError> {
    let partition = transaction.context().partition;
    if command.partition != u32::from(partition.0) {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            format!(
                "history offload mode of partition {} was routed to partition {}",
                command.partition, partition.0
            ),
        )));
    }
    if is_history_offload_enabled(transaction)? == command.enabled {
        return Ok(applied_partition_command());
    }
    let key = history_offload_mode_key(partition);
    if command.enabled {
        transaction.put_message(
            ColumnFamily::PartitionIndex,
            key,
            &HistoryOffloadModeRecord { enabled: true },
        );
    } else {
        transaction.delete(ColumnFamily::PartitionIndex, key);
    }
    Ok(applied_partition_command())
}

/// Returns whether the transaction's partition offloads run history; an absent record means disabled.
pub(crate) fn is_history_offload_enabled(
    transaction: &FlowTransaction<'_>,
) -> Result<bool, ApplyError> {
    let key = history_offload_mode_key(transaction.context().partition);
    Ok(transaction
        .get_message::<HistoryOffloadModeRecord>(
            ColumnFamily::PartitionIndex,
            &key,
            "history offload mode",
        )?
        .is_some_and(|mode| mode.enabled))
}

fn applied_partition_command() -> HandlerResult {
    HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: Vec::new(),
        run_id: None,
        replayed: None,
    })
}
