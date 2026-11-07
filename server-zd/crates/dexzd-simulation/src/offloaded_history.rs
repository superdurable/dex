// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A run's history as an owner reads it for time travel once offload deleted uploaded rows:
//! seqs of UPLOADED segments from their objects, every other seq from local rows.

use std::cell::Cell;
use std::collections::BTreeMap;
use std::ops::RangeInclusive;

use dexzd_apply::values::OffloadedValues;
use dexzd_apply::{
    ApplyContext, ApplyError, FlowStateReader, HistoryRecords, HistorySource, StateHistorySource,
};
use dexzd_history_segment::{HistorySegmentContent, decode_history_segment};
use dexzd_keyspace::row_keys::{prefix_range, value_row_key};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, ValueId, run_table_prefix};
use dexzd_protocol::dex::Value;
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, HistoryRecord, HistorySegmentRecord, HistorySegmentState, ValueBirthRecord,
    ValueRecord,
};
use prost::Message;

use crate::invariants::SealedSegmentEncodings;
use crate::memory_state::MemoryFlowState;

/// The [`HistorySource`] and [`OffloadedValues`] of one run over local rows and the objects of
/// its UPLOADED segments.
pub(crate) struct OffloadedRunHistory<'state> {
    state: &'state MemoryFlowState,
    local: StateHistorySource<'state>,
    context: ApplyContext,
    run_id: RunId,
    /// Decoded objects of the run's UPLOADED segments, by first seq.
    uploaded: BTreeMap<u64, (HistorySegmentRecord, HistorySegmentContent)>,
    /// Whether a read returned a record, checkpoint, or value from an object.
    has_served_objects: Cell<bool>,
}

impl<'state> OffloadedRunHistory<'state> {
    /// Opens the history of `run_id`, decoding every UPLOADED segment from `encodings`.
    ///
    /// Returns an error when an UPLOADED segment has no remembered object or it does not decode.
    pub(crate) fn open(
        state: &'state MemoryFlowState,
        context: ApplyContext,
        run_id: RunId,
        encodings: &SealedSegmentEncodings,
    ) -> Result<Self, ApplyError> {
        let prefix = run_table_prefix(context.flow_hash, run_id, RunTable::HistorySegment);
        let (start, end) = prefix_range(&prefix);
        let mut uploaded = BTreeMap::new();
        for (_, bytes) in state
            .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
            .map_err(ApplyError::from)?
        {
            let segment = HistorySegmentRecord::decode(bytes.as_slice()).map_err(corrupt)?;
            if segment.state != HistorySegmentState::Uploaded as i32 {
                continue;
            }
            let object = encodings.get(&(run_id, segment.first_seq)).ok_or_else(|| {
                ApplyError::InvariantViolation {
                    detail: format!(
                        "uploaded history segment {}..={} has no object",
                        segment.first_seq, segment.last_seq
                    ),
                }
            })?;
            let content =
                decode_history_segment(object).map_err(|error| ApplyError::InvariantViolation {
                    detail: format!("a history segment object does not decode: {error}"),
                })?;
            uploaded.insert(segment.first_seq, (segment, content));
        }
        Ok(Self {
            state,
            local: StateHistorySource::new(state, context, run_id),
            context,
            run_id,
            uploaded,
            has_served_objects: Cell::new(false),
        })
    }

    /// Returns whether a read returned a record, checkpoint, or value from an object.
    pub(crate) fn has_served_objects(&self) -> bool {
        self.has_served_objects.get()
    }

    /// Returns the UPLOADED segment whose range holds `seq` and its decoded object.
    fn covering(&self, seq: u64) -> Option<&(HistorySegmentRecord, HistorySegmentContent)> {
        self.uploaded
            .range(..=seq)
            .next_back()
            .map(|(_, entry)| entry)
            .filter(|(segment, _)| seq <= segment.last_seq)
    }
}

impl HistorySource for OffloadedRunHistory<'_> {
    fn history_records(
        &self,
        seqs: RangeInclusive<u64>,
        batch_records: usize,
    ) -> HistoryRecords<'_> {
        let mut records = BTreeMap::new();
        for record in self.local.history_records(seqs.clone(), batch_records) {
            match record {
                Ok(record) if self.covering(record.seq).is_none() => {
                    records.insert(record.seq, Ok(record));
                }
                Ok(_) => {}
                Err(error) => {
                    records.insert(record_error_seq(&records), Err(error));
                    break;
                }
            }
        }
        for (segment, content) in self.uploaded.values() {
            if segment.last_seq < *seqs.start() || segment.first_seq > *seqs.end() {
                continue;
            }
            for row in content.records.iter().filter(|row| seqs.contains(&row.seq)) {
                let record = HistoryRecord::decode(row.row.as_slice()).map_err(corrupt);
                records.insert(row.seq, record);
                self.has_served_objects.set(true);
            }
        }
        Box::new(records.into_values())
    }

    fn checkpoint(&self, attempt: u32, seq: u64) -> Result<Option<CheckpointRecord>, ApplyError> {
        let Some((_, content)) = self.covering(seq) else {
            return self.local.checkpoint(attempt, seq);
        };
        self.has_served_objects.set(true);
        content
            .checkpoints
            .iter()
            .find(|row| row.seq == seq && row.attempt == attempt)
            .map(|row| CheckpointRecord::decode(row.row.as_slice()).map_err(corrupt))
            .transpose()
    }
}

impl OffloadedValues for OffloadedRunHistory<'_> {
    fn offloaded_value(
        &self,
        run_id: RunId,
        value_id: ValueId,
    ) -> Result<Option<Value>, ApplyError> {
        if run_id != self.run_id {
            return Ok(None);
        }
        let birth_key = value_row_key(
            self.context.flow_hash,
            run_id,
            RunTable::ValueBirth,
            value_id,
        );
        let Some(birth) = self
            .state
            .row(ColumnFamily::Flow, &birth_key)
            .map(ValueBirthRecord::decode)
            .transpose()
            .map_err(corrupt)?
            .filter(|birth| birth.is_offloaded)
        else {
            return Ok(None);
        };
        let Some((_, content)) = self.covering(birth.garbage_seq) else {
            return Ok(None);
        };
        self.has_served_objects.set(true);
        content
            .values
            .iter()
            .find(|row| row.value_id == value_id)
            .map(|row| {
                ValueRecord::decode(row.row.as_slice())
                    .map(|record| record.value)
                    .map_err(corrupt)
            })
            .transpose()
            .map(Option::flatten)
    }
}

/// Keys an error after every record read so far, so it ends the stream in order.
fn record_error_seq(records: &BTreeMap<u64, Result<HistoryRecord, ApplyError>>) -> u64 {
    records
        .keys()
        .next_back()
        .map_or(0, |seq| seq.saturating_add(1))
}

fn corrupt(error: prost::DecodeError) -> ApplyError {
    ApplyError::InvariantViolation {
        detail: format!("a history segment row does not decode: {error}"),
    }
}
