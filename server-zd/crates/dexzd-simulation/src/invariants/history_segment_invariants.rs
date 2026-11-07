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
use dexzd_keyspace::row_keys::{value_garbage_key, value_row_key};
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, ValueId, parse_run_key};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistorySegmentState, RunRecord, ValueBirthRecord, ValueGarbageRecord,
    flow_command::Command,
};
use prost::Message;

use crate::invariants::history_migration_invariants::{is_migration_step, must_migrate};
use crate::invariants::value_invariants::{covering_uploaded_segment, offloaded_value_row};
use crate::invariants::{InvariantViolation, RunContext};
use crate::memory_state::MemoryFlowState;
use crate::state_view::{FlowView, RunView};

/// Encoding of each segment when the checker first saw it SEALED, by run and first seq. It
/// stays after the upload as the segment's object, until the catalog row goes.
pub(crate) type SealedSegmentEncodings = BTreeMap<(RunId, u64), Vec<u8>>;

const FIRST_VALUE_ID: ValueId = ValueId {
    run_seq: 0,
    index_in_command: 0,
};

/// The seqs and event IDs that left one run's segment chain: the open segment each command
/// applied while offload was off abandoned, through that command.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub(crate) struct OffloadGaps {
    seqs: BTreeSet<u64>,
    event_ids: BTreeSet<i64>,
}

/// Offload gaps by run.
pub(crate) type OffloadGapsByRun = BTreeMap<RunId, OffloadGaps>;

/// Checks how the command moved each touched run's open segment (H9), and records the gap that
/// a command applied while offload was off leaves.
///
/// While offload is off, a command that takes a run seq moves the open segment right after
/// itself and seals nothing. While it is on, the open segment stays, or moves after the command
/// that sealed it. A command without a run seq never moves it.
///
/// Returns how many TimeTravel commands applied to a run with segment state while offload was off.
pub(crate) fn check_open_segment_transitions(
    command: &FlowCommand,
    previous_records: &BTreeMap<RunId, RunRecord>,
    view: &FlowView,
    gaps: &mut OffloadGapsByRun,
) -> Result<usize, InvariantViolation> {
    let mut time_travels_while_off = 0;
    for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
        let (Some(previous), Some(current)) = (previous_records.get(run_id), run.record.as_ref())
        else {
            continue;
        };
        let context = RunContext {
            flow_id: &command.flow_id,
            run_id,
        };
        let open = |record: &RunRecord| {
            (
                record.history_open_segment_first_seq,
                record.history_open_segment_first_event_id,
                record.history_open_segment_bytes,
                record.history_open_segment_superseded_value_count,
            )
        };
        let seq = previous.next_run_seq.max(1);
        let is_migration_required = must_migrate(previous, current, view, seq);
        if previous.history_open_segment_first_seq == 0 {
            if (current.history_open_segment_first_seq != 0 || is_migration_required)
                && !is_migration_step(previous, current, run, view, seq)
            {
                return Err(violation(
                    "H9-open-segment-transition",
                    &context,
                    "an existing run without segment state gained it without migrating, or a \
                     running one did not start migrating",
                ));
            }
            continue;
        }
        if current.next_run_seq != seq + 1 {
            if open(current) != open(previous) {
                return Err(violation(
                    "H9-open-segment-transition",
                    &context,
                    "a command without a run seq moved the open segment",
                ));
            }
            continue;
        }
        let first_seq = previous.history_open_segment_first_seq;
        let sealed_now = run
            .history_segments
            .get(&first_seq)
            .is_some_and(|segment| segment.last_seq == seq);
        let moved_after_command = current.history_open_segment_first_seq == seq + 1
            && current.history_open_segment_first_event_id == current.next_event_id.max(1)
            && current.history_open_segment_bytes == 0
            && current.history_open_segment_superseded_value_count == 0;
        if !view.is_history_offload_enabled {
            if !moved_after_command || sealed_now {
                return Err(violation(
                    "H9-open-segment-transition",
                    &context,
                    format!(
                        "seq {seq} applied while offload was off did not leave the open segment"
                    ),
                ));
            }
            let gap = gaps.entry(*run_id).or_default();
            gap.seqs.extend(first_seq..=seq);
            gap.event_ids
                .extend(previous.history_open_segment_first_event_id..current.next_event_id.max(1));
            if matches!(command.command, Some(Command::TimeTravel(_))) {
                time_travels_while_off += 1;
            }
        } else {
            let is_kept = current.history_open_segment_first_seq == first_seq
                && current.history_open_segment_first_event_id
                    == previous.history_open_segment_first_event_id;
            let has_migration_step = is_migration_step(previous, current, run, view, seq);
            let is_valid = if is_migration_required {
                has_migration_step
            } else {
                is_kept || (moved_after_command && sealed_now) || has_migration_step
            };
            if !is_valid {
                return Err(violation(
                    "H9-open-segment-transition",
                    &context,
                    format!(
                        "seq {seq} moved the open segment without sealing it, or a running \
                         migration made no progress"
                    ),
                ));
            }
        }
    }
    Ok(time_travels_while_off)
}

/// Checks the run's segment chain, uploaded seq, pending index row, and value lists.
///
/// History reads show no seq between genesis and the run's window pruning frontier, so no
/// HistoryRecord metadata stays there: offload never moves the frontier, only pruning does.
pub(crate) fn check_window_frontier(
    context: &RunContext<'_>,
    run: &RunView,
) -> Result<(), InvariantViolation> {
    let Some(record) = &run.record else {
        return Ok(());
    };
    let Some(genesis_seq) = run.attempts.get(&1).map(|attempt| attempt.start_seq) else {
        return Ok(());
    };
    let pruned_through_seq = record.history_window_pruned_through_seq;
    let stale = if pruned_through_seq > genesis_seq {
        run.history_record_metadata
            .range(genesis_seq + 1..=pruned_through_seq)
            .next()
    } else {
        None
    };
    if let Some((seq, _)) = stale {
        return Err(violation(
            "H14-window-frontier",
            context,
            format!("record {seq} keeps metadata below the window frontier {pruned_through_seq}"),
        ));
    }
    if pruned_through_seq >= record.window_start_seq && record.window_start_seq > 1 {
        return Err(violation(
            "H14-window-frontier",
            context,
            format!(
                "the window frontier {pruned_through_seq} passed the window start {}",
                record.window_start_seq
            ),
        ));
    }
    Ok(())
}

/// Segments are contiguous in seqs and events, except across exactly the seqs and events that
/// `gaps` recorded for commands applied while offload was off.
pub(crate) fn check_segment_chain(
    context: &RunContext<'_>,
    run: &RunView,
    view: &FlowView,
    gaps: Option<&OffloadGaps>,
) -> Result<(), InvariantViolation> {
    let no_gaps = OffloadGaps::default();
    let gaps = gaps.unwrap_or(&no_gaps);
    let continues = |actual: u64, expected: u64| {
        actual >= expected && (expected..actual).all(|seq| gaps.seqs.contains(&seq))
    };
    let continues_events = |actual: i64, expected: i64| {
        actual >= expected && (expected..actual).all(|event_id| gaps.event_ids.contains(&event_id))
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
            || !continues_events(segment.first_event_id, next_event_id)
            || gaps
                .seqs
                .range(segment.first_seq..=segment.last_seq)
                .next()
                .is_some()
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
        || !continues_events(record.history_open_segment_first_event_id, next_event_id)
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
/// while the run has a SEALED segment (invariant 1). Such a row that no UPLOADED segment covers
/// must also be before the window start: only window pruning deletes it.
///
/// Returns how many history records and checkpoints the command deleted under UPLOADED
/// segments.
pub(crate) fn check_deletions_were_uploaded(
    flow_id: &str,
    writes: &[StateWrite],
    view: &FlowView,
) -> Result<usize, InvariantViolation> {
    let mut uploaded_rows_deleted = 0;
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
        let run = view.runs.get(&parsed.run_id);
        let record = run.and_then(|run| run.record.as_ref());
        let uploaded_through_seq = record.map_or(0, |record| record.history_uploaded_through_seq);
        let window_start_seq = record.map_or(0, |record| record.window_start_seq);
        let is_uploaded =
            run.is_some_and(|run| covering_uploaded_segment(run, deleted_seq).is_some());
        if !is_uploaded && deleted_seq >= window_start_seq {
            return Err(InvariantViolation::new(
                "H11-delete-uploaded-or-pruned",
                format!(
                    "Flow {flow_id} run {} deleted a {:?} row at seq {deleted_seq} that no \
                     UPLOADED segment covers, inside the window from seq {window_start_seq}",
                    parsed.run_id, parsed.table
                ),
            ));
        }
        if is_uploaded && *column_family == ColumnFamily::History {
            uploaded_rows_deleted += 1;
        }
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
    Ok(uploaded_rows_deleted)
}

/// Local history that offload deleted, and time travels it did not stop.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct LocalOffloadCounts {
    /// History records and checkpoints deleted under UPLOADED segments.
    pub rows_deleted: usize,
    /// Superseded values offloaded after their grace period.
    pub values_offloaded: usize,
    /// Offloaded values revived before a command that references them.
    pub values_revived: usize,
    /// Applied TimeTravel commands whose computation read uploaded history or values.
    pub time_travels_from_uploaded_history: usize,
}

impl LocalOffloadCounts {
    /// Adds another seed's counts.
    pub fn merge(&mut self, other: &Self) {
        self.rows_deleted += other.rows_deleted;
        self.values_offloaded += other.values_offloaded;
        self.values_revived += other.values_revived;
        self.time_travels_from_uploaded_history += other.time_travels_from_uploaded_history;
    }
}

/// Grace period of superseded values by command time; an oracle independent of apply.
const SUPERSEDED_VALUE_GRACE_MICROS: i64 = 10 * 60 * 1_000_000;

/// What the store held for a value that a command offloaded, read before the command committed.
pub(crate) struct ValueOffload {
    run_id: RunId,
    value_id: ValueId,
    superseded_seq: u64,
    value_row: Vec<u8>,
}

/// Reads, before `writes` commit, the values the command offloads, and requires each to have
/// passed its grace period by the command's time. Also requires every garbage row the command
/// adds to carry the command's time.
///
/// Returns the offloads for [`check_value_offloads`], or the first violation.
pub(crate) fn collect_value_offloads(
    command: &FlowCommand,
    writes: &[StateWrite],
    state: &MemoryFlowState,
    flow_hash: FlowHash,
) -> Result<Vec<ValueOffload>, InvariantViolation> {
    let mut offloads = Vec::new();
    for write in writes {
        let StateWrite::Put {
            column_family: ColumnFamily::Flow,
            key,
            value,
        } = write
        else {
            continue;
        };
        let Ok(parsed) = parse_run_key(key) else {
            continue;
        };
        let context = format!("Flow {} run {}", command.flow_id, parsed.run_id);
        match parsed.table {
            RunTable::ValueGarbage if state.row(ColumnFamily::Flow, key).is_none() => {
                let garbage = decode::<ValueGarbageRecord>(value, "value garbage")?;
                if garbage.superseded_at_micros != command.proposed_at_micros {
                    return Err(InvariantViolation::new(
                        "H12-superseded-time",
                        format!(
                            "{context} superseded a value at {} instead of its command time {}",
                            garbage.superseded_at_micros, command.proposed_at_micros
                        ),
                    ));
                }
            }
            RunTable::ValueBirth => {
                let birth = decode::<ValueBirthRecord>(value, "value birth")?;
                let previous = state
                    .row(ColumnFamily::Flow, key)
                    .map(|bytes| decode::<ValueBirthRecord>(bytes, "value birth"))
                    .transpose()?;
                if !birth.is_offloaded || previous.is_some_and(|previous| previous.is_offloaded) {
                    continue;
                }
                let value_id = ValueId::from_key_suffix(parsed.suffix).ok_or_else(|| {
                    InvariantViolation::new("H10-offloaded-value", "birth key has no value ID")
                })?;
                let garbage = state
                    .row(
                        ColumnFamily::Flow,
                        &value_garbage_key(flow_hash, parsed.run_id, birth.garbage_seq, value_id),
                    )
                    .map(|bytes| decode::<ValueGarbageRecord>(bytes, "value garbage"))
                    .transpose()?;
                let value_row = state
                    .row(
                        ColumnFamily::Value,
                        &value_row_key(flow_hash, parsed.run_id, RunTable::Value, value_id),
                    )
                    .map(<[u8]>::to_vec);
                let (Some(garbage), Some(value_row)) = (garbage, value_row) else {
                    return Err(InvariantViolation::new(
                        "H10-offloaded-value",
                        format!(
                            "{context} offloaded value {value_id:?}, which was not a stored \
                             superseded value"
                        ),
                    ));
                };
                let is_in_grace = garbage.superseded_at_micros != 0
                    && command.proposed_at_micros - garbage.superseded_at_micros
                        < SUPERSEDED_VALUE_GRACE_MICROS;
                if is_in_grace {
                    return Err(InvariantViolation::new(
                        "H13-superseded-value-grace",
                        format!(
                            "{context} offloaded value {value_id:?} at {}, within the grace \
                             period of its supersession at {}",
                            command.proposed_at_micros, garbage.superseded_at_micros
                        ),
                    ));
                }
                offloads.push(ValueOffload {
                    run_id: parsed.run_id,
                    value_id,
                    superseded_seq: birth.garbage_seq,
                    value_row,
                });
            }
            _ => {}
        }
    }
    Ok(offloads)
}

/// Requires the object of the UPLOADED segment that covers each offloaded value's superseded
/// seq to hold the value row the store deleted.
pub(crate) fn check_value_offloads(
    flow_id: &str,
    offloads: &[ValueOffload],
    view: &FlowView,
    objects: &SealedSegmentEncodings,
) -> Result<(), InvariantViolation> {
    for offload in offloads {
        let context = format!("Flow {flow_id} run {}", offload.run_id);
        let segment = view
            .runs
            .get(&offload.run_id)
            .and_then(|run| covering_uploaded_segment(run, offload.superseded_seq))
            .ok_or_else(|| {
                InvariantViolation::new(
                    "H10-offloaded-value",
                    format!(
                        "{context} offloaded value {:?} superseded at {}, which no UPLOADED \
                         segment covers",
                        offload.value_id, offload.superseded_seq
                    ),
                )
            })?;
        let stored = objects
            .get(&(offload.run_id, segment.first_seq))
            .ok_or_else(|| "the segment has no object".to_owned())
            .and_then(|object| offloaded_value_row(object, offload.value_id));
        if stored.as_deref() != Ok(offload.value_row.as_slice()) {
            return Err(InvariantViolation::new(
                "H10-offloaded-value",
                format!(
                    "{context} offloaded value {:?}, but segment {} does not hold its row: \
                     {stored:?}",
                    offload.value_id, segment.first_seq
                ),
            ));
        }
    }
    Ok(())
}

fn decode<RowMessage: Message + Default>(
    bytes: &[u8],
    table: &str,
) -> Result<RowMessage, InvariantViolation> {
    RowMessage::decode(bytes).map_err(|error| {
        InvariantViolation::new("row-decoding", format!("a {table} row is corrupt: {error}"))
    })
}

/// Encodes every SEALED segment from both stores and requires identical bytes, equal to the
/// bytes first encoded for the segment (invariant 2). Keeps the encoding of an UPLOADED segment
/// as its object, and forgets segments whose catalog row is gone.
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
            encoded_run != run_id || run.history_segments.contains_key(first_seq)
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
