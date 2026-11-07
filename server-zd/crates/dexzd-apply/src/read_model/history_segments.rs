// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History segment rows and the committed content of one segment, for uploads and checks.

use dexzd_history_segment::{
    HistorySegmentCheckpointRow, HistorySegmentContent, HistorySegmentIdentity,
    HistorySegmentRecordRow, HistorySegmentValueRow,
};
use dexzd_keyspace::row_keys::{
    checkpoint_key, history_record_key, history_segment_key, history_segment_value_prefix,
    prefix_range, value_row_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, ValueId, run_table_prefix};
use dexzd_protocol::dexzd::v1::HistorySegmentRecord;

use crate::history_offload::segment_upload::sealed_history_segments;
use crate::read_model::{ReadModelError, RunView};
use crate::{ApplyError, FlowStateReader};

/// Returns the run's oldest SEALED history segment, which is the only one an upload
/// acknowledgment accepts, or `None` when every sealed segment is uploaded.
///
/// Returns `RunNotFound` when the run is gone or purging.
pub fn oldest_sealed_history_segment(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Result<Option<HistorySegmentRecord>, ReadModelError> {
    let view = RunView::open(reader, flow_hash, run_id)?;
    Ok(sealed_history_segments(
        &view.transaction,
        view.rows,
        view.record.history_uploaded_through_seq,
        1,
    )?
    .into_iter()
    .next())
}

/// Assembles what the run's history segment that starts at `first_seq` uploads: the stored
/// history records and checkpoints inside its seq range and its listed superseded values, as
/// the exact row bytes. The result depends only on committed state.
///
/// Returns `None` when the run has no such segment, and `RunNotFound` when the run is gone or
/// purging. A listed value that is no longer stored is an invariant violation.
pub fn read_history_segment_content(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    first_seq: u64,
) -> Result<Option<HistorySegmentContent>, ReadModelError> {
    let view = RunView::open(reader, flow_hash, run_id)?;
    let transaction = &view.transaction;
    let Some(segment) = transaction.get_message::<HistorySegmentRecord>(
        ColumnFamily::Flow,
        &history_segment_key(flow_hash, run_id, first_seq),
        "history segment",
    )?
    else {
        return Ok(None);
    };
    let first_seq = segment.first_seq;
    let after_last_seq = segment.last_seq.saturating_add(1);
    let records = transaction
        .scan(
            ColumnFamily::History,
            &history_record_key(flow_hash, run_id, first_seq),
            &history_record_key(flow_hash, run_id, after_last_seq),
            usize::MAX,
        )?
        .into_iter()
        .map(|(key, row)| {
            Ok(HistorySegmentRecordRow {
                seq: trailing_seq(&key)?,
                row,
            })
        })
        .collect::<Result<Vec<_>, ApplyError>>()?;
    let mut checkpoints = Vec::new();
    let metadata_prefix = run_table_prefix(flow_hash, run_id, RunTable::HistoryRowMetadata);
    let mut metadata_start = metadata_prefix.clone();
    metadata_start.extend_from_slice(&first_seq.to_be_bytes());
    let mut metadata_end = metadata_prefix.clone();
    metadata_end.extend_from_slice(&after_last_seq.to_be_bytes());
    // Metadata is ordered by seq, then table, then attempt: the checkpoint order of the object.
    for (key, _) in transaction.scan(
        ColumnFamily::Flow,
        &metadata_start,
        &metadata_end,
        usize::MAX,
    )? {
        let suffix = &key[metadata_prefix.len()..];
        if suffix.len() != 13 || suffix[8] != RunTable::Checkpoint as u8 {
            continue;
        }
        let seq = u64::from_be_bytes(suffix[..8].try_into().expect("eight seq bytes"));
        let attempt = u32::from_be_bytes(suffix[9..].try_into().expect("four attempt bytes"));
        let row = transaction
            .get(
                ColumnFamily::History,
                &checkpoint_key(flow_hash, run_id, attempt, seq),
            )?
            .ok_or_else(|| ApplyError::InvariantViolation {
                detail: format!("checkpoint ({seq}, {attempt}) has metadata but no row"),
            })?;
        checkpoints.push(HistorySegmentCheckpointRow { seq, attempt, row });
    }
    let value_prefix = history_segment_value_prefix(flow_hash, run_id, first_seq);
    let (value_start, value_end) = prefix_range(&value_prefix);
    let mut values = Vec::new();
    for (key, _) in transaction.scan(ColumnFamily::Flow, &value_start, &value_end, usize::MAX)? {
        let value_id = ValueId::from_key_suffix(&key[value_prefix.len()..]).ok_or_else(|| {
            ApplyError::InvariantViolation {
                detail: "history segment value key must end with a 12-byte value ID".to_owned(),
            }
        })?;
        let row = transaction
            .get(
                ColumnFamily::Value,
                &value_row_key(flow_hash, run_id, RunTable::Value, value_id),
            )?
            .ok_or_else(|| ApplyError::InvariantViolation {
                detail: format!(
                    "history segment {first_seq}..={} lists value {}/{}, which is not stored",
                    segment.last_seq, value_id.run_seq, value_id.index_in_command
                ),
            })?;
        values.push(HistorySegmentValueRow { value_id, row });
    }
    Ok(Some(HistorySegmentContent {
        identity: HistorySegmentIdentity {
            run_id,
            first_seq,
            last_seq: segment.last_seq,
            first_event_id: segment.first_event_id,
            last_event_id: segment.last_event_id,
        },
        records,
        checkpoints,
        values,
    }))
}

fn trailing_seq(key: &[u8]) -> Result<u64, ApplyError> {
    key.len()
        .checked_sub(8)
        .and_then(|start| key[start..].try_into().ok())
        .map(u64::from_be_bytes)
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: "history record key must end with an eight-byte seq".to_owned(),
        })
}
