// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Time travel as an owner computes it once offload deleted uploaded rows: the production
//! segmented computation over the segment objects the checker remembers, with the production
//! rule for which offloaded values still exist.

use std::cell::{Cell, RefCell};
use std::collections::BTreeMap;
use std::collections::btree_map::Entry;
use std::sync::Arc;

use dexzd_apply::read_model::{OffloadedValueLocation, ReadModelError, locate_offloaded_value};
use dexzd_apply::values::OffloadedValues;
use dexzd_apply::{
    ApplyContext, ApplyError, LoadedHistorySegments, SegmentCheckpoint,
    SegmentedTimeTravelComputation, TimeTravelComputation, TimeTravelComputationRequest,
    compute_time_travel_from_segments,
};
use dexzd_history_segment::{HistorySegmentContent, decode_history_segment};
use dexzd_keyspace::{RunId, ValueId};
use dexzd_protocol::dex::Value;
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, FlowCommand, HistoryRecord, HistorySegmentRecord, ValueRecord,
    flow_command::Command,
};
use prost::Message;

use crate::invariants::SealedSegmentEncodings;
use crate::memory_state::MemoryFlowState;

/// Rounds of segment fetches one computation takes at most; each round loads every segment the
/// last one named, so a correct computation needs few.
const MAX_SEGMENT_ROUNDS: usize = 64;

/// A computed time travel and whether it read uploaded history or offloaded values.
pub(crate) struct OffloadedTimeTravel {
    pub(crate) computation: TimeTravelComputation,
    pub(crate) has_read_objects: bool,
}

/// Computes the unstaged TimeTravel `command` as an owner does: rounds of the production
/// segmented computation, each with the segments the last round named, loaded from
/// `encodings`.
///
/// Returns an error when an uploaded segment the computation needs has no remembered object,
/// or when stored state is corrupt.
pub(crate) fn compute_offloaded_time_travel(
    state: &MemoryFlowState,
    context: ApplyContext,
    command: &FlowCommand,
    encodings: &SealedSegmentEncodings,
) -> Result<OffloadedTimeTravel, ApplyError> {
    let Some(Command::TimeTravel(time_travel)) = &command.command else {
        return Err(invariant("only a TimeTravel command is computed"));
    };
    let run_id = RunId::from_slice(&command.run_id)
        .map_err(|error| invariant(&format!("a TimeTravel names an invalid run: {error}")))?;
    let reset = time_travel.request.clone().unwrap_or_default();
    let request = TimeTravelComputationRequest {
        run_id,
        reset: &reset,
        now_micros: command.proposed_at_micros,
        replay_memory_budget_bytes: u64::MAX,
    };
    let objects = SegmentObjects {
        state,
        context,
        run_id,
        encodings,
        decoded: RefCell::new(BTreeMap::new()),
        has_served_values: Cell::new(false),
    };
    let mut loaded = LoadedHistorySegments::default();
    for _ in 0..MAX_SEGMENT_ROUNDS {
        match compute_time_travel_from_segments(&context, state, &loaded, &objects, &request)? {
            SegmentedTimeTravelComputation::Computed(computation) => {
                return Ok(OffloadedTimeTravel {
                    computation,
                    has_read_objects: !loaded.is_empty() || objects.has_served_values.get(),
                });
            }
            SegmentedTimeTravelComputation::NeedsSegments(missing) => {
                for segment in &missing.records {
                    let records = objects.with_content(segment, |content| {
                        content
                            .records
                            .iter()
                            .map(|row| HistoryRecord::decode(row.row.as_slice()).map_err(corrupt))
                            .collect::<Result<Vec<_>, _>>()
                    })??;
                    loaded.insert(segment, Arc::from(records))?;
                }
                for segment in &missing.checkpoints {
                    let checkpoints = objects.with_content(segment, |content| {
                        content
                            .checkpoints
                            .iter()
                            .map(|row| {
                                Ok(SegmentCheckpoint {
                                    attempt: row.attempt,
                                    seq: row.seq,
                                    record: CheckpointRecord::decode(row.row.as_slice())
                                        .map_err(corrupt)?,
                                })
                            })
                            .collect::<Result<Vec<_>, ApplyError>>()
                    })??;
                    loaded.insert_checkpoints(segment, Arc::from(checkpoints))?;
                }
            }
        }
    }
    Err(invariant(&format!(
        "a time travel needed segments in more than {MAX_SEGMENT_ROUNDS} rounds"
    )))
}

/// The remembered objects of one run's segments, and the offloaded values they hold.
struct SegmentObjects<'state> {
    state: &'state MemoryFlowState,
    context: ApplyContext,
    run_id: RunId,
    encodings: &'state SealedSegmentEncodings,
    /// Objects decoded so far, by first seq.
    decoded: RefCell<BTreeMap<u64, HistorySegmentContent>>,
    has_served_values: Cell<bool>,
}

impl SegmentObjects<'_> {
    /// Runs `read` on the decoded object of `segment`.
    fn with_content<T>(
        &self,
        segment: &HistorySegmentRecord,
        read: impl FnOnce(&HistorySegmentContent) -> T,
    ) -> Result<T, ApplyError> {
        let mut decoded = self.decoded.borrow_mut();
        let content = match decoded.entry(segment.first_seq) {
            Entry::Occupied(entry) => entry.into_mut(),
            Entry::Vacant(entry) => {
                let object = self
                    .encodings
                    .get(&(self.run_id, segment.first_seq))
                    .ok_or_else(|| {
                        invariant(&format!(
                            "uploaded history segment {}..={} has no object",
                            segment.first_seq, segment.last_seq
                        ))
                    })?;
                entry.insert(decode_history_segment(object).map_err(|error| {
                    invariant(&format!(
                        "a history segment object does not decode: {error}"
                    ))
                })?)
            }
        };
        Ok(read(content))
    }
}

impl OffloadedValues for SegmentObjects<'_> {
    /// Returns an offloaded value that production reads would find: one the window has not
    /// passed, in an UPLOADED segment.
    fn offloaded_value(
        &self,
        run_id: RunId,
        value_id: ValueId,
    ) -> Result<Option<Value>, ApplyError> {
        if run_id != self.run_id {
            return Ok(None);
        }
        let location =
            match locate_offloaded_value(self.state, self.context.flow_hash, run_id, value_id) {
                Ok(location) => location,
                Err(ReadModelError::RunNotFound { .. }) => return Ok(None),
                Err(ReadModelError::State(error)) => return Err(error),
                Err(error) => return Err(invariant(&error.to_string())),
            };
        let OffloadedValueLocation::Uploaded(segment) = location else {
            return Ok(None);
        };
        let row = self.with_content(&segment, |content| {
            content
                .values
                .iter()
                .find(|row| row.value_id == value_id)
                .map(|row| row.row.clone())
        })?;
        let row = row.ok_or_else(|| {
            invariant(&format!(
                "segment {} does not hold offloaded value {value_id:?}",
                segment.first_seq
            ))
        })?;
        self.has_served_values.set(true);
        Ok(ValueRecord::decode(row.as_slice()).map_err(corrupt)?.value)
    }
}

fn invariant(detail: &str) -> ApplyError {
    ApplyError::InvariantViolation {
        detail: detail.to_owned(),
    }
}

fn corrupt(error: prost::DecodeError) -> ApplyError {
    invariant(&format!("a history segment row does not decode: {error}"))
}
