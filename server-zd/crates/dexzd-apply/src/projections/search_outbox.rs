// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::row_keys::{
    partition_index_range, search_outbox_key, search_projection_mode_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, IndexKind, RunId};
use dexzd_protocol::dexzd::v1::{
    SearchOutboxAckCommand, SearchOutboxRecord, SearchProjectionModeRecord,
    SetSearchProjectionModeCommand,
};

use crate::ApplyError;
use crate::command_handlers::command_replies::ok_reply;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::state_access::FlowTransaction;

pub(crate) fn mark_search_outbox(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    run_id: RunId,
    version: u64,
    is_delete: bool,
) -> Result<(), ApplyError> {
    if scope.is_history_rebuild() {
        return Ok(());
    }
    let context = transaction.context();
    let mode: Option<SearchProjectionModeRecord> = transaction.get_message(
        ColumnFamily::PartitionIndex,
        &search_projection_mode_key(context.partition),
        "search projection mode",
    )?;
    if !mode.is_some_and(|mode| mode.enabled) {
        return Ok(());
    }
    let key = search_outbox_key(context.partition, context.flow_hash, run_id);
    let previous: Option<SearchOutboxRecord> =
        transaction.get_message(ColumnFamily::PartitionIndex, &key, "search outbox")?;
    transaction.put_message(
        ColumnFamily::PartitionIndex,
        key,
        &SearchOutboxRecord {
            version,
            tenant_id: scope.tenant_id.0,
            is_delete,
            pending_since_micros: previous
                .map_or(scope.now_micros, |record| record.pending_since_micros),
        },
    );
    Ok(())
}

pub(crate) fn apply_search_projection_mode(
    transaction: &mut FlowTransaction<'_>,
    command: &SetSearchProjectionModeCommand,
) -> HandlerResult {
    let partition = transaction.context().partition;
    transaction.put_message(
        ColumnFamily::PartitionIndex,
        search_projection_mode_key(partition),
        &SearchProjectionModeRecord {
            enabled: command.enabled,
        },
    );
    if !command.enabled {
        let (start, end) = partition_index_range(partition, IndexKind::SearchOutbox);
        transaction.delete_range(ColumnFamily::PartitionIndex, start, end);
    }
    applied_partition_command()
}

pub(crate) fn apply_search_outbox_ack(
    transaction: &mut FlowTransaction<'_>,
    command: &SearchOutboxAckCommand,
) -> Result<HandlerResult, ApplyError> {
    let partition = transaction.context().partition;
    if command.partition != u32::from(partition.0) {
        return Err(ApplyError::InvariantViolation {
            detail: "search outbox ack targets another partition".into(),
        });
    }
    for entry in &command.entries {
        let run_id =
            RunId::from_slice(&entry.run_id).map_err(|error| ApplyError::InvariantViolation {
                detail: format!("search outbox ack run ID: {error}"),
            })?;
        let key = search_outbox_key(partition, FlowHash(entry.flow_hash), run_id);
        let pending: Option<SearchOutboxRecord> =
            transaction.get_message(ColumnFamily::PartitionIndex, &key, "search outbox")?;
        if pending.is_some_and(|pending| pending.version <= entry.version) {
            transaction.delete(ColumnFamily::PartitionIndex, key);
        }
    }
    Ok(applied_partition_command())
}

fn applied_partition_command() -> HandlerResult {
    HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: Vec::new(),
        run_id: None,
        replayed: None,
    })
}
