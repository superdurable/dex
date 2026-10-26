// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Reads one store's pending Attribute Sync work of one partition from a committed view.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::FlowStateReader;
use dexzd_apply::read_model::get_attributes;
use dexzd_apply::values::load_for_wire;
use dexzd_keyspace::row_keys::{
    attribute_sync_outbox_key, attribute_sync_outbox_store_prefix,
    attribute_sync_pending_store_prefix, decode_attribute_sync_pending_index_key, run_named_key,
};
use dexzd_keyspace::{ColumnFamily, PartitionId, RunId, RunTable, TenantId, flow_directory_key};
use dexzd_protocol::dex::{Value, value};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, AttributeSyncOutboxRecord, FlowDirectoryRecord, RunRecord,
    attribute_sync_outbox_record::SyncItem,
};

use super::AttributeSyncItem;
use crate::effects::{EffectError, RunLocation, decode_row, read_run_record, scan_all};
use crate::runtime::FlowAddress;

/// One pending entry as committed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(super) struct PendingEntry {
    pub(super) attribute_key: String,
    pub(super) written_seq: u64,
    pub(super) retry_window_started_at_micros: i64,
}

/// One run's pending entries of one store and the values to write for them.
#[derive(Debug, Clone, PartialEq)]
pub(super) struct PendingRunEntries {
    pub(super) run: RunLocation,
    pub(super) flow: FlowAddress,
    /// Every pending entry, in key order.
    pub(super) entries: Vec<PendingEntry>,
    /// Synced values to write, in key order.
    ///
    /// Entries without an item are superseded, hold hard-delete values, or have legacy unsynced values.
    pub(super) items: Vec<AttributeSyncItem>,
    /// Set when a stored value reference cannot be loaded; the entries are dropped.
    pub(super) unreadable_value_detail: Option<String>,
}

/// One store's pending runs of a partition, grouped by Flow ID in run order.
#[derive(Debug, Default)]
pub(super) struct PendingStoreWork {
    pub(super) flows: Vec<Vec<PendingRunEntries>>,
    pub(super) entry_count: usize,
    pub(super) oldest_retry_window_started_at_micros: Option<i64>,
}

/// A run of a Flow ID with its directory position; later positions are newer runs.
struct DirectoryRun {
    run: RunLocation,
    record: RunRecord,
}

/// Reads every pending run of `store_name` in `partition` with one committed `reader`.
pub(super) fn read_pending_store_work(
    reader: &dyn FlowStateReader,
    partition: PartitionId,
    store_name: &str,
) -> Result<PendingStoreWork, EffectError> {
    let prefix = attribute_sync_pending_store_prefix(partition, store_name);
    let mut pending_runs: BTreeMap<(u64, String), Vec<(RunLocation, RunRecord)>> = BTreeMap::new();
    for (key, _) in scan_all(reader, ColumnFamily::PartitionIndex, &prefix)? {
        let entry = decode_attribute_sync_pending_index_key(&key).ok_or_else(|| {
            EffectError::CorruptState {
                table: "AttributeSyncPending index",
                key: crate::effects::hex(&key),
                detail: "malformed key".to_owned(),
            }
        })?;
        let run = RunLocation {
            flow_hash: entry.flow_hash,
            run_id: entry.run_id,
        };
        // A purged run's entries are deleted with it.
        let Some(record) = read_run_record(reader, run)? else {
            continue;
        };
        pending_runs
            .entry((record.tenant_id, record.flow_id.clone()))
            .or_default()
            .push((run, record));
    }
    let mut work = PendingStoreWork::default();
    for ((tenant_id, flow_id), runs) in pending_runs {
        let flow = FlowAddress::new(TenantId(tenant_id), flow_id);
        let flow_hash = runs[0].0.flow_hash;
        let directory_runs = read_directory_runs(reader, flow_hash, &flow)?;
        let mut flow_work = Vec::with_capacity(runs.len());
        let mut ordered_runs: Vec<(usize, RunLocation)> = runs
            .iter()
            .map(|(run, _)| {
                let position = directory_runs
                    .iter()
                    .position(|directory_run| directory_run.run == *run)
                    .unwrap_or(usize::MAX);
                (position, *run)
            })
            .collect();
        ordered_runs.sort();
        for (position, run) in ordered_runs {
            let newer_runs = directory_runs
                .get(position.saturating_add(1)..)
                .unwrap_or(&[]);
            let run_entries = read_run_entries(reader, &flow, run, store_name, newer_runs)?;
            work.entry_count += run_entries.entries.len();
            for entry in &run_entries.entries {
                work.oldest_retry_window_started_at_micros = Some(
                    work.oldest_retry_window_started_at_micros
                        .map_or(entry.retry_window_started_at_micros, |oldest| {
                            oldest.min(entry.retry_window_started_at_micros)
                        }),
                );
            }
            flow_work.push(run_entries);
        }
        work.flows.push(flow_work);
    }
    Ok(work)
}

/// Returns the retained runs of a Flow ID, oldest first.
fn read_directory_runs(
    reader: &dyn FlowStateReader,
    flow_hash: dexzd_keyspace::FlowHash,
    flow: &FlowAddress,
) -> Result<Vec<DirectoryRun>, EffectError> {
    let key = flow_directory_key(flow_hash, flow.tenant_id, &flow.flow_id);
    let Some(bytes) = reader.get(ColumnFamily::Flow, &key)? else {
        return Ok(Vec::new());
    };
    let directory: FlowDirectoryRecord = decode_row("FlowDirectory", &key, &bytes)?;
    let mut runs = Vec::with_capacity(directory.runs.len());
    for directory_run in directory.runs {
        let run_id = RunId::from_slice(&directory_run.run_id).map_err(|error| {
            EffectError::CorruptState {
                table: "FlowDirectory",
                key: crate::effects::hex(&key),
                detail: error.to_string(),
            }
        })?;
        let run = RunLocation { flow_hash, run_id };
        if let Some(record) = read_run_record(reader, run)? {
            runs.push(DirectoryRun { run, record });
        }
    }
    Ok(runs)
}

fn read_run_entries(
    reader: &dyn FlowStateReader,
    flow: &FlowAddress,
    run: RunLocation,
    store_name: &str,
    newer_runs: &[DirectoryRun],
) -> Result<PendingRunEntries, EffectError> {
    let prefix = attribute_sync_outbox_store_prefix(run.flow_hash, run.run_id, store_name);
    let records: Vec<AttributeSyncOutboxRecord> = scan_all(reader, ColumnFamily::Flow, &prefix)?
        .into_iter()
        .map(|(key, bytes)| decode_row("AttributeSyncOutbox", &key, &bytes))
        .collect::<Result<_, EffectError>>()?;
    let mut synced_items = BTreeMap::new();
    let mut legacy_keys = Vec::new();
    for record in &records {
        if is_written_by_newer_run(reader, &record.attribute_key, store_name, newer_runs)? {
            continue;
        }
        match &record.sync_item {
            Some(SyncItem::Value(value)) => {
                synced_items.insert(
                    record.attribute_key.clone(),
                    AttributeSyncItem {
                        key: record.attribute_key.clone(),
                        value: Some(value.clone()),
                    },
                );
            }
            Some(SyncItem::IsDelete(_)) => {
                synced_items.insert(
                    record.attribute_key.clone(),
                    AttributeSyncItem {
                        key: record.attribute_key.clone(),
                        value: None,
                    },
                );
            }
            None => legacy_keys.push(record.attribute_key.clone()),
        }
    }
    let (items, unreadable_value_detail) = match read_latest_values(reader, run, &legacy_keys)? {
        Ok(items) => {
            synced_items.extend(items.into_iter().map(|item| (item.key.clone(), item)));
            (synced_items.into_values().collect(), None)
        }
        Err(detail) => (Vec::new(), Some(detail)),
    };
    let entries = records
        .into_iter()
        .map(|record| PendingEntry {
            attribute_key: record.attribute_key,
            written_seq: record.written_seq,
            retry_window_started_at_micros: record.retry_window_started_at_micros,
        })
        .collect();
    Ok(PendingRunEntries {
        run,
        flow: flow.clone(),
        entries,
        items,
        unreadable_value_detail,
    })
}

/// A newer run of the Flow ID writes the same row; an older value must not land after it.
fn is_written_by_newer_run(
    reader: &dyn FlowStateReader,
    attribute_key: &str,
    store_name: &str,
    newer_runs: &[DirectoryRun],
) -> Result<bool, EffectError> {
    for newer_run in newer_runs {
        let run = newer_run.run;
        let outbox_key =
            attribute_sync_outbox_key(run.flow_hash, run.run_id, store_name, attribute_key);
        if reader.get(ColumnFamily::Flow, &outbox_key)?.is_some() {
            return Ok(true);
        }
        let syncs_to_store = newer_run
            .record
            .flow_config
            .as_ref()
            .and_then(|config| config.attribute_store_names.as_ref())
            .is_some_and(|stores| stores.names.iter().any(|name| name == store_name));
        if !syncs_to_store {
            continue;
        }
        let attribute_key_bytes = run_named_key(
            run.flow_hash,
            run.run_id,
            RunTable::Attribute,
            attribute_key,
        );
        if let Some(bytes) = reader.get(ColumnFamily::Flow, &attribute_key_bytes)? {
            let record: AttributeRecord = decode_row("Attribute", &attribute_key_bytes, &bytes)?;
            if record.sync_config.is_some_and(|config| config.enabled) {
                return Ok(true);
            }
        }
    }
    Ok(false)
}

/// Returns complete values in key order, or the inner `Err` naming a reference that cannot load.
fn read_latest_values(
    reader: &dyn FlowStateReader,
    run: RunLocation,
    keys: &[String],
) -> Result<Result<Vec<AttributeSyncItem>, String>, EffectError> {
    if keys.is_empty() {
        return Ok(Ok(Vec::new()));
    }
    let read = get_attributes(reader, run.flow_hash, run.run_id, keys, false).map_err(|error| {
        EffectError::InvariantViolation {
            detail: format!("reading Attributes of run {}: {error}", run.run_id),
        }
    })?;
    let hard_delete_keys: BTreeSet<String> = read
        .hard_delete_attributes
        .into_iter()
        .map(|attribute| attribute.key)
        .collect();
    let mut values: BTreeMap<String, Option<Value>> = read
        .response
        .attributes
        .into_iter()
        .map(|attribute| (attribute.key, attribute.value))
        .collect();
    let mut items = Vec::with_capacity(keys.len());
    for key in keys {
        let attribute_key = run_named_key(run.flow_hash, run.run_id, RunTable::Attribute, key);
        if let Some(bytes) = reader.get(ColumnFamily::Flow, &attribute_key)? {
            let record: AttributeRecord = decode_row("Attribute", &attribute_key, &bytes)?;
            // Legacy entries lack snapshots; never project their unsynced current values.
            if !record.sync_config.is_some_and(|config| config.enabled) {
                continue;
            }
        }
        // Request validation rejects syncing hard-delete Attributes; a later one is left unwritten.
        if hard_delete_keys.contains(key) {
            continue;
        }
        let value = match values.remove(key).flatten() {
            Some(value) => match complete_value(reader, run, value)? {
                Some(value) => Some(value),
                None => {
                    return Ok(Err(format!(
                        "the value reference of Attribute {key:?} cannot be loaded"
                    )));
                }
            },
            None => None,
        };
        items.push(AttributeSyncItem {
            key: key.clone(),
            value,
        });
    }
    Ok(Ok(items))
}

/// Replaces a value reference with its stored value; returns `None` when the reference cannot load.
fn complete_value(
    reader: &dyn FlowStateReader,
    run: RunLocation,
    wire_value: Value,
) -> Result<Option<Value>, EffectError> {
    let reference = match &wire_value.kind {
        Some(
            value::Kind::InternalBlobIdForStringValue(reference)
            | value::Kind::InternalBlobIdForObjValue(reference),
        ) => reference,
        _ => return Ok(Some(wire_value)),
    };
    load_for_wire(reader, run.flow_hash, run.run_id, reference).map_err(|error| {
        EffectError::InvariantViolation {
            detail: format!("loading an Attribute value of run {}: {error}", run.run_id),
        }
    })
}
