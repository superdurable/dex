// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Checks that the prune cursors of a run skip only rows the run no longer holds.

use std::collections::BTreeMap;

use dexzd_keyspace::row_keys::{attempt_record_key, prefix_range, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::{AttemptRecord, RunRecord};
use prost::Message;

/// Panics when a metadata row after genesis or a garbage row lies below the run's prune cursor.
///
/// Skips runs without a run record or first attempt record.
pub(crate) fn assert_prune_cursors_skip_no_rows(
    rows: &BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
    flow_hash: FlowHash,
    run_id: RunId,
) {
    let Some(record_bytes) = rows.get(&(
        ColumnFamily::Flow,
        run_singleton_key(flow_hash, run_id, RunTable::RunRecord),
    )) else {
        return;
    };
    let Some(attempt_bytes) =
        rows.get(&(ColumnFamily::Flow, attempt_record_key(flow_hash, run_id, 1)))
    else {
        return;
    };
    let record = RunRecord::decode(record_bytes.as_slice()).expect("the run record decodes");
    let genesis_seq = AttemptRecord::decode(attempt_bytes.as_slice())
        .expect("the first attempt record decodes")
        .start_seq;
    let metadata_prefix = run_table_prefix(flow_hash, run_id, RunTable::HistoryRowMetadata);
    if let Some(seq) = first_seq_before(
        rows,
        &metadata_prefix,
        genesis_seq + 1,
        record.history_metadata_pruned_before_seq,
    ) {
        panic!(
            "run {run_id} holds history metadata at seq {seq}, below its metadata cursor {}",
            record.history_metadata_pruned_before_seq
        );
    }
    let garbage_prefix = run_table_prefix(flow_hash, run_id, RunTable::ValueGarbage);
    if let Some(seq) = first_seq_before(
        rows,
        &garbage_prefix,
        0,
        record.value_garbage_pruned_before_seq,
    ) {
        panic!(
            "run {run_id} holds value garbage at seq {seq}, below its garbage cursor {}",
            record.value_garbage_pruned_before_seq
        );
    }
}

/// Returns the seq of the first row of `prefix` whose seq is in `[from_seq, before_seq)`.
fn first_seq_before(
    rows: &BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
    prefix: &[u8],
    from_seq: u64,
    before_seq: u64,
) -> Option<u64> {
    if from_seq >= before_seq {
        return None;
    }
    let (_, end) = prefix_range(prefix);
    let mut start = prefix.to_vec();
    start.extend_from_slice(&from_seq.to_be_bytes());
    rows.range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
        .next()
        .map(|((_, key), _)| {
            u64::from_be_bytes(
                key[prefix.len()..prefix.len() + 8]
                    .try_into()
                    .expect("history metadata and value garbage keys start with a seq"),
            )
        })
        .filter(|seq| *seq < before_seq)
}
