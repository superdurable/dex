// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Reviving an offloaded value before a command that references it (plan section 5.11).
//!
//! A Worker or Client may hand back a reference to a superseded value that offload deleted
//! locally. The owner reads the value from its uploaded segment and commits a ValueRevive
//! command first, so the referencing command commits unchanged and resolves the value locally.

use dexzd_keyspace::row_keys::value_row_key;
use dexzd_keyspace::{ColumnFamily, RunTable, ValueId};
use dexzd_protocol::dex::ErrorSubStatus;
use dexzd_protocol::dexzd::v1::{RunRecord, ValueBirthRecord, ValueReviveCommand};
use prost::Message;

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::state_access::FlowTransaction;
use crate::values::{reconcile_run_storage, restore_unheld_value};

/// Restores an offloaded value of the run locally, unheld, so a later command that references
/// it resolves it.
///
/// The value row, its birth record, and its garbage row return as they were before offload,
/// at the seq that superseded the value, so window pruning deletes it once the window passes.
/// The garbage row carries this command's time, as every garbage row a command adds does.
///
/// The command records no history, takes no run seq, and is not bound to an attempt. Replies OK
/// without writes when the run stores the value locally, has no birth record of it, or the
/// window passed it; NOT_FOUND when the run is gone or purging; and INVALID_ARGUMENT without
/// writes when the command carries no value or a value whose size differs from its birth record.
pub(crate) fn apply_value_revive(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &ValueReviveCommand,
) -> Result<HandlerResult, ApplyError> {
    let mut cursor = match RunCursor::open_without_seq(transaction, scope)? {
        Ok(cursor) => cursor,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    let Some(value) = command.value.clone() else {
        return Ok(HandlerResult::Rejected(invalid_argument_reply(
            "value revive carries no value".to_owned(),
        )));
    };
    let rows = cursor.rows;
    let value_id = ValueId {
        run_seq: command.value_run_seq,
        index_in_command: command.value_index_in_command,
    };
    let birth: Option<ValueBirthRecord> = transaction.get_message(
        ColumnFamily::Flow,
        &value_row_key(rows.flow_hash, rows.run_id, RunTable::ValueBirth, value_id),
        "value birth",
    )?;
    let Some(birth) = birth.filter(|birth| is_offloaded_in_window(birth, &cursor.record)) else {
        return Ok(HandlerResult::Rejected(ok_reply()));
    };
    let size_bytes = u64::try_from(value.encoded_len()).expect("encoded Value length fits a u64");
    if size_bytes != birth.size_bytes {
        return Ok(HandlerResult::Rejected(invalid_argument_reply(format!(
            "value revive carries {size_bytes} bytes for value {}-{}, but the run recorded {} \
             bytes",
            value_id.run_seq, value_id.index_in_command, birth.size_bytes
        ))));
    }
    restore_unheld_value(
        transaction,
        rows,
        value_id,
        &birth,
        value,
        birth.garbage_seq,
        scope.now_micros,
    );
    reconcile_run_storage(transaction, &mut cursor)?;
    Ok(HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: Vec::new(),
        run_id: None,
        replayed: None,
    }))
}

/// Whether `birth` marks a value that offload deleted locally and that the window of the run
/// `record` still holds, so its uploaded segment serves it.
pub(crate) fn is_offloaded_in_window(birth: &ValueBirthRecord, record: &RunRecord) -> bool {
    birth.is_offloaded && birth.garbage_seq >= record.window_start_seq
}

fn invalid_argument_reply(detail: String) -> dexzd_protocol::dexzd::v1::CommandReply {
    error_reply(
        grpc_code::INVALID_ARGUMENT,
        ErrorSubStatus::Uncategorized,
        detail,
    )
}
