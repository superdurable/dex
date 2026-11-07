// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History segment invariants (plan section 9, invariants 1 to 3): segments are contiguous and
//! acknowledged in order, their content never changes once sealed, and offloaded history is
//! deleted only once uploaded.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::StateWrite;
use dexzd_apply::read_model::read_history_segment_content;
use dexzd_history_segment::encode_history_segment;
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, ValueId, parse_run_key};
use dexzd_protocol::dexzd::v1::HistorySegmentState;

use crate::invariants::{InvariantViolation, RunContext};
use crate::memory_state::MemoryFlowState;
use crate::state_view::{FlowView, RunView};

/// Encoding of each SEALED segment when the checker first saw it, by run and first seq.
pub(crate) type SealedSegmentEncodings = BTreeMap<(RunId, u64), Vec<u8>>;

const FIRST_VALUE_ID: ValueId = ValueId {
    run_seq: 0,
    index_in_command: 0,
};

/// Checks the run's segment chain, uploaded seq, pending index row, and value lists.
///
/// Segments are contiguous in seqs and events, except that commands applied while offload was
/// off form gaps; `has_offload_gap` tells whether the run applied any.
pub(crate) fn check_segment_chain(
    context: &RunContext<'_>,
    run: &RunView,
    view: &FlowView,
    has_offload_gap: bool,
) -> Result<(), InvariantViolation> {
    let continues = |actual: u64, expected: u64| {
        if has_offload_gap {
            actual >= expected
        } else {
            actual == expected
        }
    };
    let Some(record) = run.record.as_ref() else {
        return Ok(());
    };
    let is_pending = view.history_upload_pending_runs.contains(context.run_id);
    if record.history_open_segment_first_seq == 0 {
        if !run.history_segments.is_empty() || !run.history_segment_values.is_empty() || is_pending
        {
            return Err(violation(
                "H1-segment-chain",
                context,
                "a run without segment state has segment rows or a pending index row",
            ));
        }
        return Ok(());
    }
    let uploaded_through_seq = record.history_uploaded_through_seq;
    let mut next_seq = run.attempts.get(&1).map_or(1, |attempt| attempt.start_seq);
    let mut next_event_id: i64 = 1;
    let mut has_sealed = false;
    let mut names_uploaded_seq = uploaded_through_seq == 0;
    for (first_seq, segment) in &run.history_segments {
        if *first_seq != segment.first_seq
            || !continues(segment.first_seq, next_seq)
            || segment.first_seq > segment.last_seq
            || segment.first_event_id < 1
            || !continues(
                segment.first_event_id.unsigned_abs(),
                next_event_id.unsigned_abs(),
            )
        {
            return Err(violation(
                "H1-segment-chain",
                context,
                format!(
                    "segment {segment:?} does not continue at seq {next_seq}, event {next_event_id}"
                ),
            ));
        }
        let is_uploaded = segment.last_seq <= uploaded_through_seq;
        let expected_state = if is_uploaded {
            HistorySegmentState::Uploaded
        } else {
            HistorySegmentState::Sealed
        };
        if segment.state != expected_state as i32
            || (segment.content_checksum.len() == 16) != is_uploaded
        {
            return Err(violation(
                "H2-uploaded-in-order",
                context,
                format!("segment {segment:?} disagrees with uploaded seq {uploaded_through_seq}"),
            ));
        }
        names_uploaded_seq |= segment.last_seq == uploaded_through_seq;
        has_sealed |= !is_uploaded;
        let listed = run
            .history_segment_values
            .range((segment.first_seq, FIRST_VALUE_ID)..)
            .take_while(|((first_seq, _), _)| *first_seq == segment.first_seq);
        let mut listed_count = 0;
        for ((_, value_id), row) in listed {
            listed_count += 1;
            if !(segment.first_seq..=segment.last_seq).contains(&row.superseded_seq) {
                return Err(violation(
                    "H4-segment-values",
                    context,
                    format!(
                        "value {value_id:?} of segment {first_seq} was superseded at {}",
                        row.superseded_seq
                    ),
                ));
            }
            if !is_uploaded && !run.value_rows.contains(value_id) {
                return Err(violation(
                    "H4-segment-values",
                    context,
                    format!("value {value_id:?} of SEALED segment {first_seq} is no longer stored"),
                ));
            }
        }
        if listed_count != segment.superseded_value_count {
            return Err(violation(
                "H4-segment-values",
                context,
                format!(
                    "segment {first_seq} lists {listed_count} values but records {}",
                    segment.superseded_value_count
                ),
            ));
        }
        next_seq = segment.last_seq + 1;
        next_event_id = segment.last_event_id + 1;
    }
    if !continues(record.history_open_segment_first_seq, next_seq)
        || record.history_open_segment_first_event_id < 1
        || !continues(
            record.history_open_segment_first_event_id.unsigned_abs(),
            next_event_id.unsigned_abs(),
        )
        || !names_uploaded_seq
    {
        return Err(violation(
            "H1-segment-chain",
            context,
            format!(
                "the open segment starts at seq {}, event {}, instead of seq {next_seq}, event \
                 {next_event_id}, or uploaded seq {uploaded_through_seq} names no segment",
                record.history_open_segment_first_seq, record.history_open_segment_first_event_id
            ),
        ));
    }
    if has_sealed != is_pending {
        return Err(violation(
            "H3-upload-pending-index",
            context,
            format!("has a SEALED segment: {has_sealed}; has a pending index row: {is_pending}"),
        ));
    }
    for (garbage_seq, value_id, _) in &run.value_garbage {
        let Some((first_seq, segment)) = run.history_segments.range(..=garbage_seq).next_back()
        else {
            continue;
        };
        if *garbage_seq > segment.last_seq {
            continue;
        }
        let is_listed = run
            .history_segment_values
            .get(&(*first_seq, *value_id))
            .is_some_and(|row| row.superseded_seq == *garbage_seq);
        if !is_listed {
            return Err(violation(
                "H4-segment-values",
                context,
                format!(
                    "value {value_id:?} superseded at {garbage_seq} is missing from segment {first_seq}"
                ),
            ));
        }
    }
    Ok(())
}

/// Requires every history record, checkpoint, or superseded value the command deleted to be at
/// or below the run's uploaded seq while the partition offloads history or, whatever the mode,
/// while the run has a SEALED segment (invariant 1).
pub(crate) fn check_deletions_were_uploaded(
    flow_id: &str,
    writes: &[StateWrite],
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    let deleted_value_rows: BTreeSet<(RunId, &[u8])> = writes
        .iter()
        .filter_map(|write| match write {
            StateWrite::Delete {
                column_family: ColumnFamily::Value,
                key,
            } => parse_run_key(key)
                .ok()
                .map(|parsed| (parsed.run_id, parsed.suffix)),
            _ => None,
        })
        .collect();
    for write in writes {
        let StateWrite::Delete { column_family, key } = write else {
            continue;
        };
        let Ok(parsed) = parse_run_key(key) else {
            continue;
        };
        let deleted_seq = match (column_family, parsed.table) {
            (ColumnFamily::History, RunTable::HistoryRecord) => seq_at(parsed.suffix, 0),
            (ColumnFamily::History, RunTable::Checkpoint) => seq_at(parsed.suffix, 4),
            // A revival deletes only the garbage row; pruning also deletes the value.
            (ColumnFamily::Flow, RunTable::ValueGarbage)
                if parsed.suffix.len() == 20
                    && deleted_value_rows.contains(&(parsed.run_id, &parsed.suffix[8..])) =>
            {
                seq_at(parsed.suffix, 0)
            }
            _ => continue,
        };
        let is_fenced = view.is_history_offload_enabled
            || view.history_upload_pending_runs.contains(&parsed.run_id);
        if !is_fenced {
            continue;
        }
        let uploaded_through_seq = view
            .runs
            .get(&parsed.run_id)
            .and_then(|run| run.record.as_ref())
            .map_or(0, |record| record.history_uploaded_through_seq);
        if deleted_seq > uploaded_through_seq {
            return Err(InvariantViolation::new(
                "H7-delete-after-upload",
                format!(
                    "Flow {flow_id} run {} deleted a {:?} row at seq {deleted_seq} above uploaded \
                     seq {uploaded_through_seq}",
                    parsed.run_id, parsed.table
                ),
            ));
        }
    }
    Ok(())
}

/// Encodes every SEALED segment from both stores and requires identical bytes, equal to the
/// bytes first encoded for the segment (invariant 2). Forgets segments that are no longer SEALED.
pub(crate) fn check_sealed_segment_encodings(
    flow_id: &str,
    view: &FlowView,
    flow_hash: FlowHash,
    state: &MemoryFlowState,
    redecoded_state: &MemoryFlowState,
    encodings: &mut SealedSegmentEncodings,
) -> Result<usize, InvariantViolation> {
    let mut new_segments = 0;
    for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
        let uploaded_through_seq = run
            .record
            .as_ref()
            .map_or(0, |record| record.history_uploaded_through_seq);
        let is_purging = run.record.as_ref().is_some_and(|record| record.is_purging);
        encodings.retain(|(encoded_run, first_seq), _| {
            encoded_run != run_id
                || (!is_purging
                    && run
                        .history_segments
                        .get(first_seq)
                        .is_some_and(|segment| segment.last_seq > uploaded_through_seq))
        });
        if is_purging {
            continue;
        }
        for segment in run.history_segments.values() {
            if segment.last_seq <= uploaded_through_seq {
                continue;
            }
            let encoded = encode_segment(state, flow_hash, *run_id, segment.first_seq)?;
            let redecoded = encode_segment(redecoded_state, flow_hash, *run_id, segment.first_seq)?;
            if encoded != redecoded {
                return Err(InvariantViolation::new(
                    "H8-segment-determinism",
                    format!(
                        "Flow {flow_id} run {run_id} segment {} encodes differently from the \
                         re-decoded store",
                        segment.first_seq
                    ),
                ));
            }
            match encodings.get(&(*run_id, segment.first_seq)) {
                Some(first) if *first != encoded => {
                    return Err(InvariantViolation::new(
                        "H8-segment-determinism",
                        format!(
                            "Flow {flow_id} run {run_id} segment {} changed after it was sealed",
                            segment.first_seq
                        ),
                    ));
                }
                Some(_) => {}
                None => {
                    encodings.insert((*run_id, segment.first_seq), encoded);
                    new_segments += 1;
                }
            }
        }
    }
    Ok(new_segments)
}

fn encode_segment(
    state: &MemoryFlowState,
    flow_hash: FlowHash,
    run_id: RunId,
    first_seq: u64,
) -> Result<Vec<u8>, InvariantViolation> {
    let content = read_history_segment_content(state, flow_hash, run_id, first_seq)
        .map_err(|error| InvariantViolation::new("H6-segment-content", error.to_string()))?
        .ok_or_else(|| {
            InvariantViolation::new(
                "H6-segment-content",
                format!("run {run_id} has no segment at seq {first_seq}"),
            )
        })?;
    Ok(encode_history_segment(&content)
        .map_err(|error| InvariantViolation::new("H6-segment-content", error.to_string()))?
        .bytes)
}

fn seq_at(suffix: &[u8], offset: usize) -> u64 {
    suffix
        .get(offset..offset + 8)
        .and_then(|bytes| bytes.try_into().ok())
        .map_or(u64::MAX, u64::from_be_bytes)
}

fn violation(
    invariant: &'static str,
    context: &RunContext<'_>,
    detail: impl std::fmt::Display,
) -> InvariantViolation {
    InvariantViolation::new(invariant, format!("{context}: {detail}"))
}
