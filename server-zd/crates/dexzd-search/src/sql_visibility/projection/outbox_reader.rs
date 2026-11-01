// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Blocking reads of SearchOutbox entries and their runs' documents from a committed view.

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::{
    decode_search_outbox_key, partition_index_range, run_singleton_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, IndexKind, PartitionId, RunId, RunTable, TenantId};
use dexzd_protocol::dexzd::v1::{RunRecord, SearchOutboxRecord};
use prost::Message;

use super::changes::{SearchDocumentChange, decode_search_index_key};
use super::projector::ProjectionError;

/// One SearchOutbox entry read from a committed view.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OutboxEntry {
    /// The entry's full key; scans continue after it.
    pub key: Vec<u8>,
    /// The appending command's proposed_at_micros.
    pub seq: u64,
    /// Tenant owning the run.
    pub tenant_id: TenantId,
    /// Flow hash of the run.
    pub flow_hash: FlowHash,
    /// The changed run.
    pub run_id: RunId,
}

/// One page of documents for an initial load.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct InitialLoadPage {
    /// Where the next page starts.
    pub next_start: Vec<u8>,
    /// Upserts of the page's searchable runs; purging runs are omitted.
    pub changes: Vec<SearchDocumentChange>,
}

/// Returns up to `limit` SearchOutbox entries of `partition` in `[start, end)`, in queue order.
///
/// Returns [`ProjectionError::InvalidState`] for a malformed key or record.
pub fn read_outbox_entries(
    view: &dyn FlowStateReader,
    partition: PartitionId,
    start: &[u8],
    end: &[u8],
    limit: usize,
) -> Result<Vec<OutboxEntry>, ProjectionError> {
    view.scan(ColumnFamily::PartitionIndex, start, end, limit)?
        .into_iter()
        .map(|(key, value)| {
            let entry = decode_search_outbox_key(partition, &key).ok_or_else(|| {
                ProjectionError::InvalidState("malformed SearchOutbox key".into())
            })?;
            let record = SearchOutboxRecord::decode(value.as_slice()).map_err(|error| {
                ProjectionError::InvalidState(format!("decode SearchOutbox record: {error}"))
            })?;
            Ok(OutboxEntry {
                key,
                seq: entry.seq,
                tenant_id: TenantId(record.tenant_id),
                flow_hash: entry.flow_hash,
                run_id: entry.run_id,
            })
        })
        .collect()
}

/// Returns the run's current upsert, a delete for a purged or purging run, or
/// [`ProjectionError::InvalidState`] when a retained run has no document.
pub fn read_run_change(
    view: &dyn FlowStateReader,
    tenant_id: TenantId,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Result<SearchDocumentChange, ProjectionError> {
    let Some(run_record) = read_run_record(view, flow_hash, run_id)? else {
        return Ok(SearchDocumentChange::Delete { tenant_id, run_id });
    };
    if run_record.is_purging {
        return Ok(SearchDocumentChange::Delete { tenant_id, run_id });
    }
    let document_key = run_singleton_key(flow_hash, run_id, RunTable::SearchDocument);
    let encoded_document = view
        .get(ColumnFamily::Flow, &document_key)?
        .ok_or_else(|| {
            ProjectionError::InvalidState(format!("missing committed search document for {run_id}"))
        })?;
    Ok(SearchDocumentChange::Upsert {
        tenant_id,
        run_id,
        encoded_document,
    })
}

/// Reads up to `limit` SearchDocument index entries of `partition` from `start`.
///
/// Returns `None` once the partition has no more documents.
pub fn read_initial_load_page(
    view: &dyn FlowStateReader,
    partition: PartitionId,
    start: Option<&[u8]>,
    limit: usize,
) -> Result<Option<InitialLoadPage>, ProjectionError> {
    let (partition_start, end) = partition_index_range(partition, IndexKind::SearchDocument);
    let start = start.unwrap_or(&partition_start);
    let rows = view.scan(ColumnFamily::PartitionIndex, start, &end, limit)?;
    let Some((last_key, _)) = rows.last() else {
        return Ok(None);
    };
    let mut next_start = last_key.clone();
    next_start.push(0);
    let mut changes = Vec::with_capacity(rows.len());
    for (index_key, _) in &rows {
        let (tenant_id, flow_hash, run_id) = decode_search_index_key(partition, index_key)?
            .ok_or_else(|| ProjectionError::InvalidState("search index left partition".into()))?;
        let change = read_run_change(view, tenant_id, flow_hash, run_id)?;
        if matches!(change, SearchDocumentChange::Upsert { .. }) {
            changes.push(change);
        }
    }
    Ok(Some(InitialLoadPage {
        next_start,
        changes,
    }))
}

fn read_run_record(
    view: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Result<Option<RunRecord>, ProjectionError> {
    let key = run_singleton_key(flow_hash, run_id, RunTable::RunRecord);
    view.get(ColumnFamily::Flow, &key)?
        .map(|bytes| {
            RunRecord::decode(bytes.as_slice()).map_err(|error| {
                ProjectionError::InvalidState(format!(
                    "decode committed run record for {run_id}: {error}"
                ))
            })
        })
        .transpose()
}
