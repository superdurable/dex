// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::row_keys::value_row_key;
use dexzd_keyspace::{
    ColumnFamily, FlowHash, PartitionHasher, RunId, RunTable, TenantId, ValueId, ValueReference,
};
use dexzd_protocol::dex::{LoadBlobRequestEntry, LoadBlobsResponse, value};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistorySegmentRecord, StoredValue, StoredValueId, StoredValueReference,
    ValueBirthRecord, stored_value,
};

use crate::FlowStateReader;
use crate::history_offload::segment_catalog::{CatalogPosition, SegmentCatalog, is_uploaded};
use crate::history_offload::value_revive::is_offloaded_in_window;
use crate::read_model::{ReadModelError, RunView};
use crate::state_access::RunRows;
use crate::values::{RpcOutputValue, command_values, load_stored_value, own_referenced_value_id};

/// Loads references under each entry's Flow; missing and hard-delete values are omitted.
pub fn load_blobs(
    reader: &dyn FlowStateReader,
    hasher: &PartitionHasher,
    tenant_id: TenantId,
    entries: &[LoadBlobRequestEntry],
) -> Result<LoadBlobsResponse, ReadModelError> {
    let mut values = BTreeMap::new();
    for entry in entries {
        if entry.flow_id.is_empty() {
            return Err(ReadModelError::InvalidRequest {
                detail: "Flow ID is required for every Blob".to_owned(),
            });
        }
        let (reference_text, is_string_value) = match entry
            .blob_value
            .as_ref()
            .and_then(|value| value.kind.as_ref())
        {
            Some(value::Kind::InternalBlobIdForStringValue(text)) => (text, true),
            Some(value::Kind::InternalBlobIdForObjValue(text)) => (text, false),
            _ => {
                return Err(ReadModelError::InvalidRequest {
                    detail: "LoadBlobs requires a value reference".to_owned(),
                });
            }
        };
        if reference_text.starts_with("hd1|") {
            continue;
        }
        let reference: ValueReference =
            reference_text
                .parse()
                .map_err(|error| ReadModelError::InvalidRequest {
                    detail: format!("invalid value reference: {error}"),
                })?;
        let flow_hash = hasher.flow_hash(tenant_id, &entry.flow_id);
        let view = match RunView::open(reader, flow_hash, reference.run_id) {
            Ok(view) => view,
            Err(ReadModelError::RunNotFound { .. }) => {
                return Err(ReadModelError::InvalidRequest {
                    detail: format!(
                        "Blob reference run {} does not belong to Flow {:?}",
                        reference.run_id, entry.flow_id
                    ),
                });
            }
            Err(error) => return Err(error),
        };
        if view.record.tenant_id != tenant_id.0 || view.record.flow_id != entry.flow_id {
            return Err(ReadModelError::InvalidRequest {
                detail: format!(
                    "Blob reference run {} does not belong to Flow {:?}",
                    reference.run_id, entry.flow_id
                ),
            });
        }
        let stored = StoredValue {
            kind: Some(stored_value::Kind::TableValue(StoredValueReference {
                value_id: Some(StoredValueId {
                    run_seq: reference.value_id.run_seq,
                    index_in_command: reference.value_id.index_in_command,
                }),
                size_bytes: 0,
                is_string_value,
            })),
        };
        if let Some(value) = load_stored_value(&view.transaction, view.rows, &stored)? {
            values.insert(reference_text.clone(), value);
        }
    }
    Ok(LoadBlobsResponse { values })
}

/// Where LoadBlobs finds a value of a run that the local store no longer holds.
#[derive(Debug, Clone, PartialEq)]
pub enum OffloadedValueLocation {
    /// The value is gone: it was never offloaded, the window passed it, or no UPLOADED segment
    /// holds it any more.
    Absent,
    /// The value is in the object of this UPLOADED segment of the run.
    Uploaded(HistorySegmentRecord),
}

/// Returns where value `value_id` of run `run_id` is, when the local store holds no value row for
/// it. Reads only the committed view; the caller reads the object afterwards.
///
/// A value superseded before the window start reads as absent, as window pruning deletes such
/// local values. Returns [`ReadModelError::RunNotFound`] for a missing or purging run, or an
/// error when stored state is corrupt.
pub fn locate_offloaded_value(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    value_id: ValueId,
) -> Result<OffloadedValueLocation, ReadModelError> {
    let view = RunView::open(reader, flow_hash, run_id)?;
    Ok(match uploaded_value_birth(&view, value_id)? {
        Some((_, segment)) => OffloadedValueLocation::Uploaded(segment),
        None => OffloadedValueLocation::Absent,
    })
}

/// Returns the birth record of value `value_id` of the run of `view` and the UPLOADED segment
/// that holds it, or `None` when the value is not offloaded inside the window or no UPLOADED
/// segment covers its superseded seq.
fn uploaded_value_birth(
    view: &RunView<'_>,
    value_id: ValueId,
) -> Result<Option<(ValueBirthRecord, HistorySegmentRecord)>, ReadModelError> {
    let birth: Option<ValueBirthRecord> = view.transaction.get_message(
        ColumnFamily::Flow,
        &value_row_key(
            view.rows.flow_hash,
            view.rows.run_id,
            RunTable::ValueBirth,
            value_id,
        ),
        "value birth",
    )?;
    let Some(birth) = birth.filter(|birth| is_offloaded_in_window(birth, &view.record)) else {
        return Ok(None);
    };
    let catalog = SegmentCatalog::new(
        &view.transaction,
        view.rows,
        view.record.history_uploaded_through_seq,
    );
    Ok(match catalog.position(birth.garbage_seq)? {
        CatalogPosition::Covered(segment) if is_uploaded(&segment) => Some((birth, segment)),
        CatalogPosition::Covered(_) | CatalogPosition::Gap { .. } => None,
    })
}

/// A value that a command references by value reference and that only an UPLOADED segment of
/// the run holds.
#[derive(Debug, Clone, PartialEq)]
pub struct OffloadedReference {
    /// ID of the referenced value in the command's run.
    pub value_id: ValueId,
    /// Encoded size of the value, as its birth record holds it.
    pub size_bytes: u64,
    /// The UPLOADED segment whose object holds the value.
    pub segment: HistorySegmentRecord,
}

/// Returns the values of the command's run that `command` references and that only uploaded
/// segments hold, in value ID order, so the owner revives them before it proposes the command.
/// Reads only `reader`; the caller reads the objects afterwards.
///
/// Returns none for a command without a run ID and for a missing or purging run, whose commands
/// apply rejects without resolving references. References to other runs, local values, and
/// values outside the window are not listed. Returns an error when stored state is corrupt.
pub fn offloaded_command_references(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    command: &FlowCommand,
) -> Result<Vec<OffloadedReference>, ReadModelError> {
    let Ok(run_id) = RunId::from_slice(&command.run_id) else {
        return Ok(Vec::new());
    };
    let rows = RunRows::new(flow_hash, run_id);
    let value_ids: BTreeSet<ValueId> = command_values(command, RpcOutputValue::Included)
        .into_iter()
        .filter_map(|value| own_referenced_value_id(value, rows))
        .collect();
    if value_ids.is_empty() {
        return Ok(Vec::new());
    }
    let view = match RunView::open(reader, flow_hash, run_id) {
        Ok(view) => view,
        Err(ReadModelError::RunNotFound { .. }) => return Ok(Vec::new()),
        Err(error) => return Err(error),
    };
    let mut offloaded = Vec::new();
    for value_id in value_ids {
        if let Some((birth, segment)) = uploaded_value_birth(&view, value_id)? {
            offloaded.push(OffloadedReference {
                value_id,
                size_bytes: birth.size_bytes,
                segment,
            });
        }
    }
    Ok(offloaded)
}
