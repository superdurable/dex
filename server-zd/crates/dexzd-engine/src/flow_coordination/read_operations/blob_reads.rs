// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! LoadBlobs across local values and uploaded segments; see the crate README.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::FlowStateReader;
use dexzd_apply::read_model::{OffloadedValueLocation, locate_offloaded_value};
use dexzd_keyspace::row_keys::history_offload_mode_key;
use dexzd_keyspace::{
    ColumnFamily, PartitionId, RunId, RunTable, TenantId, ValueReference, run_key,
};
use dexzd_protocol::dex::{
    LoadBlobRequestEntry, LoadBlobsRequest, LoadBlobsResponse, Value, value,
};
use dexzd_protocol::dexzd::v1::{
    HistoryOffloadModeRecord, HistorySegmentRecord, RunRecord, ValueRecord,
};
use futures::future::join_all;
use prost::Message;

use crate::flow_coordination::history_reads::{
    HistorySegmentReadError, SegmentLocation, SegmentReadPurpose,
};
use crate::flow_coordination::read_operations::state_rows::{
    decode_row, read_directory, read_table_value,
};
use crate::flow_coordination::{FlowAddress, FlowCoordinationError, FlowCoordinatorRegistry};

/// Most entries one LoadBlobs request may name while a named Flow's partition offloads history,
/// so one request reads a bounded number of values from object storage.
pub(crate) const MAX_LOAD_BLOBS_ENTRIES: usize = 1000;

/// One requested value that its run and Flow own.
struct BlobEntry {
    reference_text: String,
    flow: FlowAddress,
    reference: ValueReference,
}

/// Where one committed view found a requested value.
enum BlobLookup {
    Local(Value),
    Uploaded(HistorySegmentRecord),
    Absent,
}

impl FlowCoordinatorRegistry {
    /// Returns the values behind value references, keyed by reference text.
    ///
    /// References outside the named Flow and values that no longer exist are omitted. A value
    /// that offload deleted locally is read from its uploaded segment, through the disk read
    /// cache. No committed view is held while object storage is read.
    ///
    /// Reads only committed state, so no client sees a value that may never commit. Dispatch
    /// inlines uncommitted values instead.
    ///
    /// Returns `INVALID_ARGUMENT` for more than 1000 entries when the partition of a named Flow
    /// offloads history, and `UNAVAILABLE` when object storage fails or lacks an uploaded
    /// segment.
    pub async fn load_blobs(
        &self,
        tenant_id: TenantId,
        request: LoadBlobsRequest,
    ) -> Result<LoadBlobsResponse, FlowCoordinationError> {
        self.check_running()?;
        let entries = parse_blob_entries(tenant_id, request.entries)?;
        let lookups = self.look_up_blobs(&entries)?;
        let reader = &self.inner.dependencies.history_segment_reader;
        let mut values = BTreeMap::new();
        let mut uploaded = Vec::new();
        for (entry, lookup) in entries.iter().zip(lookups) {
            match lookup {
                Some(BlobLookup::Local(value)) => {
                    reader.record_local_blob();
                    values.insert(entry.reference_text.clone(), value);
                }
                Some(BlobLookup::Uploaded(segment)) => uploaded.push((entry, segment)),
                Some(BlobLookup::Absent) => log_omitted_value(entry),
                None => {}
            }
        }
        if !uploaded.is_empty() {
            self.load_uploaded_blobs(uploaded, &mut values).await?;
        }
        Ok(LoadBlobsResponse { values })
    }

    /// Finds each entry's value on one committed view, which is released before this returns.
    ///
    /// Returns `None` for an entry whose run or Flow no longer owns it. Returns NOT_FOUND when
    /// a listed run has no matching run record.
    fn look_up_blobs(
        &self,
        entries: &[BlobEntry],
    ) -> Result<Vec<Option<BlobLookup>>, FlowCoordinationError> {
        let committed = self.inner.dependencies.committer.committed_view()?;
        let state = committed.as_ref();
        if entries.len() > MAX_LOAD_BLOBS_ENTRIES {
            let partitions: BTreeSet<PartitionId> = entries
                .iter()
                .map(|entry| self.flow_context(&entry.flow).partition)
                .collect();
            for partition in partitions {
                if is_history_offload_enabled(state, partition)? {
                    return Err(FlowCoordinationError::invalid_argument(format!(
                        "while history offload is on, LoadBlobs takes at most \
                         {MAX_LOAD_BLOBS_ENTRIES} entries; this request has {}",
                        entries.len()
                    )));
                }
            }
        }
        let mut lookups = Vec::with_capacity(entries.len());
        for entry in entries {
            let flow = &entry.flow;
            let context = self.flow_context(flow);
            let run_id = entry.reference.run_id;
            let Some(directory) = read_directory(state, context, flow)? else {
                // A purged and an unknown Flow have no directory; the Go Server omits both.
                lookups.push(None);
                continue;
            };
            let is_run_of_flow = directory
                .runs
                .iter()
                .any(|run| run.run_id.as_slice() == run_id.as_bytes());
            if !is_run_of_flow {
                lookups.push(None);
                continue;
            }
            let run_record: Option<RunRecord> = decode_row(
                state,
                ColumnFamily::Flow,
                &run_key(context.flow_hash, run_id, RunTable::RunRecord, &[]),
                "run",
            )?;
            match run_record {
                Some(record)
                    if record.flow_id == flow.flow_id
                        && record.tenant_id == flow.tenant_id.0
                        && record.is_purging =>
                {
                    lookups.push(None);
                    continue;
                }
                Some(record)
                    if record.flow_id == flow.flow_id && record.tenant_id == flow.tenant_id.0 => {}
                _ => return Err(FlowCoordinationError::flow_not_found(&flow.flow_id)),
            }
            let value_id = entry.reference.value_id;
            if let Some(value) = read_table_value(state, context.flow_hash, run_id, value_id)? {
                lookups.push(Some(BlobLookup::Local(value)));
                continue;
            }
            let location = locate_offloaded_value(state, context.flow_hash, run_id, value_id)
                .map_err(|error| FlowCoordinationError::Internal {
                    detail: format!("locating offloaded value {}: {error}", entry.reference_text),
                })?;
            lookups.push(Some(match location {
                OffloadedValueLocation::Uploaded(segment) => BlobLookup::Uploaded(segment),
                OffloadedValueLocation::Absent => BlobLookup::Absent,
            }));
        }
        Ok(lookups)
    }

    /// Reads offloaded values from the disk read cache, then the rest from object storage with
    /// one ranged read per segment, and adds them to `values`.
    ///
    /// An object that is missing is looked up again on a new committed view: the run or the
    /// value may be gone. One that the new view still names fails the read with UNAVAILABLE.
    async fn load_uploaded_blobs(
        &self,
        uploaded: Vec<(&BlobEntry, HistorySegmentRecord)>,
        values: &mut BTreeMap<String, Value>,
    ) -> Result<(), FlowCoordinationError> {
        let reader = &self.inner.dependencies.history_segment_reader;
        let mut by_segment: BTreeMap<(RunId, u64, u64), (HistorySegmentRecord, Vec<&BlobEntry>)> =
            BTreeMap::new();
        for (entry, segment) in uploaded {
            if let Some(row) = reader
                .cached_value_row(&entry.reference_text, &segment, SegmentReadPurpose::Blob)
                .await
            {
                match decode_value_row(&row) {
                    Some(value) => {
                        values.insert(entry.reference_text.clone(), value);
                        continue;
                    }
                    None => {
                        tracing::warn!(
                            reference = %entry.reference_text,
                            "a cached offloaded value is damaged"
                        );
                        reader
                            .forget_cached_value_row(&entry.reference_text, &segment)
                            .await;
                    }
                }
            }
            let key = (entry.reference.run_id, segment.first_seq, segment.last_seq);
            by_segment
                .entry(key)
                .or_insert_with(|| (segment, Vec::new()))
                .1
                .push(entry);
        }
        let reads = by_segment.values().map(|(segment, entries)| {
            let first = entries.first().expect("a segment group has an entry");
            let value_ids: Vec<_> = entries
                .iter()
                .map(|entry| entry.reference.value_id)
                .collect();
            let location = SegmentLocation {
                tenant_id: first.flow.tenant_id,
                flow_hash: self.flow_context(&first.flow).flow_hash,
                run_id: first.reference.run_id,
                segment,
            };
            async move {
                reader
                    .read_value_rows(location, &value_ids, SegmentReadPurpose::Blob)
                    .await
            }
        });
        let results = join_all(reads).await;
        let mut missing_objects = Vec::new();
        for ((segment, entries), result) in by_segment.values().zip(results) {
            let Some(rows) = result.map_err(|error| value_read_error(&entries[0].flow, error))?
            else {
                missing_objects.extend(entries.iter().copied());
                continue;
            };
            for (entry, (_, row)) in entries.iter().zip(rows) {
                let Some(value) = decode_value_row(&row) else {
                    return Err(value_read_error(
                        &entry.flow,
                        HistorySegmentReadError::Mismatch {
                            key: format!("segment {}..={}", segment.first_seq, segment.last_seq),
                            detail: format!("value {} does not decode", entry.reference_text),
                        },
                    ));
                };
                reader
                    .cache_value_row(&entry.reference_text, segment, row)
                    .await;
                values.insert(entry.reference_text.clone(), value);
            }
        }
        if !missing_objects.is_empty() {
            self.reread_missing_objects(&missing_objects, values)?;
        }
        Ok(())
    }

    /// Looks up the entries whose segment object was missing again on a new committed view.
    fn reread_missing_objects(
        &self,
        entries: &[&BlobEntry],
        values: &mut BTreeMap<String, Value>,
    ) -> Result<(), FlowCoordinationError> {
        let owned: Vec<BlobEntry> = entries
            .iter()
            .map(|entry| BlobEntry {
                reference_text: entry.reference_text.clone(),
                flow: entry.flow.clone(),
                reference: entry.reference,
            })
            .collect();
        for (entry, lookup) in owned.iter().zip(self.look_up_blobs(&owned)?) {
            match lookup {
                Some(BlobLookup::Local(value)) => {
                    values.insert(entry.reference_text.clone(), value);
                }
                Some(BlobLookup::Uploaded(segment)) => {
                    tracing::error!(
                        flow_id = %entry.flow.flow_id,
                        run_id = %entry.reference.run_id,
                        first_seq = segment.first_seq,
                        last_seq = segment.last_seq,
                        "an uploaded history segment object is missing from object storage"
                    );
                    return Err(FlowCoordinationError::Unavailable {
                        detail: format!(
                            "history segment {}..={} of flow {} is missing from object storage",
                            segment.first_seq, segment.last_seq, entry.flow.flow_id
                        ),
                    });
                }
                Some(BlobLookup::Absent) => log_omitted_value(entry),
                None => {}
            }
        }
        Ok(())
    }
}

/// Validates every entry in request order.
///
/// Returns `INVALID_ARGUMENT` for the first entry without a Flow ID or a value reference.
fn parse_blob_entries(
    tenant_id: TenantId,
    entries: Vec<LoadBlobRequestEntry>,
) -> Result<Vec<BlobEntry>, FlowCoordinationError> {
    entries
        .into_iter()
        .map(|entry| {
            if entry.flow_id.is_empty() {
                return Err(FlowCoordinationError::invalid_argument(
                    "Flow ID is required for every Blob",
                ));
            }
            let reference_text = match entry.blob_value.and_then(|blob_value| blob_value.kind) {
                Some(
                    value::Kind::InternalBlobIdForStringValue(text)
                    | value::Kind::InternalBlobIdForObjValue(text),
                ) => text,
                _ => {
                    return Err(FlowCoordinationError::invalid_argument(
                        "blob value must be an internal blob ID",
                    ));
                }
            };
            let reference: ValueReference = reference_text.parse().map_err(|_| {
                FlowCoordinationError::invalid_argument(format!(
                    "blob ID {reference_text:?} is not a value reference"
                ))
            })?;
            Ok(BlobEntry {
                reference_text,
                flow: FlowAddress::new(tenant_id, entry.flow_id),
                reference,
            })
        })
        .collect()
}

/// Returns whether `partition` offloads run history; an absent mode record means it does not.
fn is_history_offload_enabled(
    state: &dyn FlowStateReader,
    partition: PartitionId,
) -> Result<bool, FlowCoordinationError> {
    let mode: Option<HistoryOffloadModeRecord> = decode_row(
        state,
        ColumnFamily::PartitionIndex,
        &history_offload_mode_key(partition),
        "history offload mode",
    )?;
    Ok(mode.is_some_and(|mode| mode.enabled))
}

fn decode_value_row(row: &[u8]) -> Option<Value> {
    ValueRecord::decode(row).ok()?.value
}

fn log_omitted_value(entry: &BlobEntry) {
    // A Worker that receives no value cannot run its Step.
    tracing::debug!(
        flow_id = %entry.flow.flow_id,
        reference = %entry.reference_text,
        "LoadBlobs omitted a value reference that names no committed value"
    );
}

fn value_read_error(flow: &FlowAddress, error: HistorySegmentReadError) -> FlowCoordinationError {
    if matches!(error, HistorySegmentReadError::Mismatch { .. }) {
        tracing::error!(flow_id = %flow.flow_id, %error, "an offloaded value cannot be read");
    }
    FlowCoordinationError::Unavailable {
        detail: error.to_string(),
    }
}
