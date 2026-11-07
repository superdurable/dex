// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;

use dexzd_keyspace::row_keys::value_row_key;
use dexzd_keyspace::{
    ColumnFamily, FlowHash, PartitionHasher, RunId, RunTable, TenantId, ValueId, ValueReference,
};
use dexzd_protocol::dex::{LoadBlobRequestEntry, LoadBlobsResponse, value};
use dexzd_protocol::dexzd::v1::{
    HistorySegmentRecord, StoredValue, StoredValueId, StoredValueReference, ValueBirthRecord,
    stored_value,
};

use crate::FlowStateReader;
use crate::history_offload::segment_catalog::{CatalogPosition, SegmentCatalog, is_uploaded};
use crate::read_model::{ReadModelError, RunView};
use crate::values::load_stored_value;

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
    let birth: Option<ValueBirthRecord> = view.transaction.get_message(
        ColumnFamily::Flow,
        &value_row_key(flow_hash, run_id, RunTable::ValueBirth, value_id),
        "value birth",
    )?;
    let Some(birth) = birth
        .filter(|birth| birth.is_offloaded && birth.garbage_seq >= view.record.window_start_seq)
    else {
        return Ok(OffloadedValueLocation::Absent);
    };
    let catalog = SegmentCatalog::new(
        &view.transaction,
        view.rows,
        view.record.history_uploaded_through_seq,
    );
    Ok(match catalog.position(birth.garbage_seq)? {
        CatalogPosition::Covered(segment) if is_uploaded(&segment) => {
            OffloadedValueLocation::Uploaded(segment)
        }
        CatalogPosition::Covered(_) | CatalogPosition::Gap { .. } => OffloadedValueLocation::Absent,
    })
}
