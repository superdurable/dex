// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attribute Sync outbox entries, keyed by Attribute key because apply cannot read store
//! configuration.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::row_keys::{
    attribute_sync_outbox_key, attribute_sync_outbox_store_prefix,
    attribute_sync_pending_index_key, prefix_range,
};
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dex::AttributeWrite;
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, AttributeSyncOutboxRecord, RunRecord,
    attribute_sync_outbox_record::SyncItem,
};

use crate::command_handlers::command_replies::ok_reply;
use crate::command_handlers::flow_close::finalize_pending_close_if_ready;
use crate::command_handlers::run_cursor::{HistoryKind, RunCursor};
use crate::command_handlers::{CommandScope, HandlerResult};
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::load_stored_value;
use crate::{ApplyEffect, ApplyError};

const OUTBOX_ROW_KIND: &str = "Attribute Sync outbox";

/// Returns the keys of the writes that request Attribute Store synchronization.
pub(crate) fn synced_write_keys<'write>(
    writes: impl IntoIterator<Item = &'write AttributeWrite>,
) -> BTreeSet<String> {
    writes
        .into_iter()
        .filter(|write| write.sync_config.is_some_and(|sync| sync.enabled))
        .map(|write| write.key.clone())
        .collect()
}

/// Marks `attribute_keys` pending for each current store; a pending key keeps its retry window.
pub(crate) fn mark_attribute_sync_entries(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    attribute_keys: &BTreeSet<String>,
    now_micros: i64,
) -> Result<BTreeSet<String>, ApplyError> {
    let store_names = attribute_store_names(&cursor.record);
    if attribute_keys.is_empty() || store_names.is_empty() {
        return Ok(BTreeSet::new());
    }
    let sync_items: BTreeMap<String, SyncItem> = attribute_keys
        .iter()
        .map(|attribute_key| {
            let item = match cursor.rows.attribute(transaction, attribute_key)? {
                Some(record) if record.sync_config.is_some_and(|config| config.enabled) => {
                    match record.value {
                        // Complete values survive Attribute replacement without retaining their value-table rows.
                        Some(stored) => SyncItem::Value(
                            load_stored_value(transaction, cursor.rows, &stored)?.ok_or_else(
                                || ApplyError::InvariantViolation {
                                    detail: format!(
                                        "synced Attribute {attribute_key:?} has no live value"
                                    ),
                                },
                            )?,
                        ),
                        None => SyncItem::IsDelete(true),
                    }
                }
                _ => SyncItem::IsDelete(true),
            };
            Ok((attribute_key.clone(), item))
        })
        .collect::<Result<_, ApplyError>>()?;
    let flow_hash = cursor.rows.flow_hash;
    let run_id = cursor.run_id();
    let partition = transaction.context().partition;
    for store_name in &store_names {
        for (attribute_key, sync_item) in &sync_items {
            let key = attribute_sync_outbox_key(flow_hash, run_id, store_name, attribute_key);
            let existing: Option<AttributeSyncOutboxRecord> =
                transaction.get_message(ColumnFamily::Flow, &key, OUTBOX_ROW_KIND)?;
            let record = match existing {
                Some(mut record) => {
                    record.written_seq = record.written_seq.max(cursor.seq);
                    record.sync_item = Some(sync_item.clone());
                    record
                }
                None => AttributeSyncOutboxRecord {
                    store_name: store_name.clone(),
                    attribute_key: attribute_key.clone(),
                    written_seq: cursor.seq,
                    retry_window_started_at_micros: now_micros,
                    sync_item: Some(sync_item.clone()),
                },
            };
            transaction.put_message(ColumnFamily::Flow, key, &record);
        }
        transaction.put(
            ColumnFamily::PartitionIndex,
            attribute_sync_pending_index_key(partition, store_name, flow_hash, run_id),
            Vec::new(),
        );
    }
    Ok(store_names)
}

/// Deletes the acknowledged entries of one store; entries marked after `acked_seq` stay pending.
pub(crate) fn apply_attribute_sync_ack(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
    command: &AttributeSyncAckCommand,
) -> Result<HandlerResult, ApplyError> {
    if scope.is_replay {
        return replay_recorded_close(transaction, cursor, scope);
    }
    let flow_hash = cursor.rows.flow_hash;
    let run_id = cursor.run_id();
    let selected_keys: BTreeSet<&str> = command.attribute_keys.iter().map(String::as_str).collect();
    let (start, end) = prefix_range(&attribute_sync_outbox_store_prefix(
        flow_hash,
        run_id,
        &command.store_name,
    ));
    let entries: Vec<(Vec<u8>, AttributeSyncOutboxRecord)> = transaction.scan_messages(
        ColumnFamily::Flow,
        &start,
        &end,
        usize::MAX,
        OUTBOX_ROW_KIND,
    )?;
    let mut deleted_keys = Vec::new();
    let mut has_remaining_entry = false;
    let mut is_changed = false;
    for (key, mut entry) in entries {
        if !selected_keys.is_empty() && !selected_keys.contains(entry.attribute_key.as_str()) {
            has_remaining_entry = true;
            continue;
        }
        if entry.written_seq <= command.acked_seq {
            transaction.delete(ColumnFamily::Flow, key);
            deleted_keys.push(entry.attribute_key);
            is_changed = true;
            continue;
        }
        has_remaining_entry = true;
        // A newer change arrived after the projector read the entry; its retries start now.
        if entry.retry_window_started_at_micros != scope.now_micros {
            entry.retry_window_started_at_micros = scope.now_micros;
            transaction.put_message(ColumnFamily::Flow, key, &entry);
            is_changed = true;
        }
    }
    if !is_changed {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    if !has_remaining_entry {
        transaction.delete(
            ColumnFamily::PartitionIndex,
            attribute_sync_pending_index_key(
                transaction.context().partition,
                &command.store_name,
                flow_hash,
                run_id,
            ),
        );
    }
    if command.is_dropped && !deleted_keys.is_empty() {
        deleted_keys.sort();
        cursor.add_effect(ApplyEffect::AttributeSyncDropped {
            run_id,
            store_name: command.store_name.clone(),
            attribute_keys: deleted_keys,
        });
    }
    let is_finalized =
        !has_remaining_entry && finalize_pending_close_if_ready(transaction, &mut cursor, scope)?;
    let kind = if is_finalized {
        HistoryKind::SEMANTIC
    } else {
        HistoryKind::BOOKKEEPING
    };
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        ok_reply(),
        kind,
    )?))
}

/// A rebuild replays only acks that finalized a pending close, and never reads the run-scoped outbox.
fn replay_recorded_close(
    transaction: &mut FlowTransaction<'_>,
    mut cursor: RunCursor,
    scope: &CommandScope<'_>,
) -> Result<HandlerResult, ApplyError> {
    if !finalize_pending_close_if_ready(transaction, &mut cursor, scope)? {
        return Err(ApplyError::InvariantViolation {
            detail: "a replayed Attribute Sync ack recorded a close without a pending close"
                .to_owned(),
        });
    }
    Ok(HandlerResult::Applied(cursor.finish(
        transaction,
        scope,
        ok_reply(),
        HistoryKind::SEMANTIC,
    )?))
}

/// Whether the run has an Attribute Sync outbox entry for any store, including buffered writes.
pub(crate) fn has_attribute_sync_entries(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
) -> Result<bool, ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::AttributeSyncOutbox);
    let (start, end) = prefix_range(&prefix);
    Ok(!transaction
        .scan(ColumnFamily::Flow, &start, &end, 1)?
        .is_empty())
}

/// Returns the Attribute keys with pending entries across every store of the run.
pub(crate) fn pending_attribute_sync_keys(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
) -> Result<BTreeSet<String>, ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::AttributeSyncOutbox);
    let (start, end) = prefix_range(&prefix);
    let entries: Vec<(Vec<u8>, AttributeSyncOutboxRecord)> = transaction.scan_messages(
        ColumnFamily::Flow,
        &start,
        &end,
        usize::MAX,
        OUTBOX_ROW_KIND,
    )?;
    Ok(entries
        .into_iter()
        .map(|(_, entry)| entry.attribute_key)
        .collect())
}

/// Deletes the pending index keys of a purged run; the range delete removes its entries.
pub(crate) fn remove_attribute_sync_indexes(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
) -> Result<(), ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::AttributeSyncOutbox);
    let (start, end) = prefix_range(&prefix);
    let entries: Vec<(Vec<u8>, AttributeSyncOutboxRecord)> = transaction.scan_messages(
        ColumnFamily::Flow,
        &start,
        &end,
        usize::MAX,
        OUTBOX_ROW_KIND,
    )?;
    let store_names: BTreeSet<String> = entries
        .into_iter()
        .map(|(_, entry)| entry.store_name)
        .collect();
    let partition = transaction.context().partition;
    for store_name in store_names {
        transaction.delete(
            ColumnFamily::PartitionIndex,
            attribute_sync_pending_index_key(partition, &store_name, rows.flow_hash, rows.run_id),
        );
    }
    Ok(())
}

/// Whether the run's Flow config names an Attribute Store, so marking a synced key creates entries.
pub(crate) fn has_attribute_store_names(record: &RunRecord) -> bool {
    !attribute_store_names(record).is_empty()
}

fn attribute_store_names(record: &RunRecord) -> BTreeSet<String> {
    record
        .flow_config
        .as_ref()
        .and_then(|config| config.attribute_store_names.as_ref())
        .map(|names| names.names.iter().cloned().collect())
        .unwrap_or_default()
}
