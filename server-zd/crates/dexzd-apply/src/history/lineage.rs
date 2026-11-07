// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attempt lineages, and the history source that streams records and checkpoints.
//!
//! Only the current attempt receives commands, so attempts cover disjoint, contiguous seq ranges.

use std::collections::VecDeque;
use std::ops::RangeInclusive;

use dexzd_keyspace::row_keys::{checkpoint_key, history_record_key, prefix_range};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::{AttemptRecord, CheckpointRecord, HistoryRecord};

use crate::history::attempt_scope::hex;
use crate::history::replay_state::TransactionReader;
use crate::state_access::{FlowTransaction, RunRows};
use crate::{ApplyContext, ApplyError, FlowStateReader};

/// History records read per batch by time travel's history scans.
pub(crate) const HISTORY_PAGE_RECORDS: usize = 64;

/// Attempts of a lineage back to attempt 1, each with its largest seq in the lineage.
#[derive(Debug, Clone)]
pub(crate) struct Lineage {
    segments: Vec<(u32, u64)>,
}

impl Lineage {
    /// Builds the lineage of `attempt` from the attempt records.
    pub(crate) fn of_attempt(
        transaction: &FlowTransaction<'_>,
        rows: RunRows,
        attempt: u32,
    ) -> Result<Self, ApplyError> {
        let mut segments = Vec::new();
        let mut current = attempt;
        let mut max_seq = u64::MAX;
        while current != 0 {
            let record = required_attempt_record(transaction, rows, current)?;
            segments.push((current, max_seq));
            if record.parent_attempt >= current {
                return Err(ApplyError::InvariantViolation {
                    detail: format!(
                        "run attempt {current} has parent attempt {}",
                        record.parent_attempt
                    ),
                });
            }
            max_seq = record.parent_seq;
            current = record.parent_attempt;
        }
        Ok(Self { segments })
    }

    pub(crate) fn contains(&self, attempt: u32, seq: u64) -> bool {
        self.segments
            .iter()
            .any(|(segment_attempt, max_seq)| *segment_attempt == attempt && seq <= *max_seq)
    }
}

pub(crate) fn required_attempt_record(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    attempt: u32,
) -> Result<AttemptRecord, ApplyError> {
    rows.attempt_record(transaction, attempt)?
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!("run attempt {attempt} has no attempt record"),
        })
}

/// Seq of the FlowStart command, the genesis point.
pub(crate) fn genesis_seq(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
) -> Result<u64, ApplyError> {
    Ok(required_attempt_record(transaction, rows, 1)?.start_seq)
}

/// Synchronously streams one run's History records and checkpoints, which may live outside
/// the local store.
pub trait HistorySource {
    /// Streams records with seq in `seqs` ascending, ending after an error. `batch_records`
    /// hints the read size and never changes which records return.
    fn history_records(
        &self,
        seqs: RangeInclusive<u64>,
        batch_records: usize,
    ) -> HistoryRecords<'_>;

    /// Returns the checkpoint of `attempt` at `seq`, or `None` when the source holds none.
    ///
    /// Returns an error when the checkpoint cannot be read or decoded.
    fn checkpoint(&self, attempt: u32, seq: u64) -> Result<Option<CheckpointRecord>, ApplyError>;
}

/// History records of a [`HistorySource`] in ascending seq order.
pub type HistoryRecords<'source> =
    Box<dyn Iterator<Item = Result<HistoryRecord, ApplyError>> + 'source>;

/// The [`HistorySource`] over the History rows that a transaction reads.
pub(crate) struct LocalHistorySource<'transaction, 'reader> {
    reader: TransactionReader<'transaction, 'reader>,
    context: ApplyContext,
    rows: RunRows,
}

impl<'transaction, 'reader> LocalHistorySource<'transaction, 'reader> {
    pub(crate) fn new(transaction: &'transaction FlowTransaction<'reader>, rows: RunRows) -> Self {
        Self {
            reader: TransactionReader::new(transaction),
            context: transaction.context(),
            rows,
        }
    }
}

impl HistorySource for LocalHistorySource<'_, '_> {
    fn history_records(
        &self,
        seqs: RangeInclusive<u64>,
        batch_records: usize,
    ) -> HistoryRecords<'_> {
        stored_history_records(&self.reader, self.context, self.rows, seqs, batch_records)
    }

    fn checkpoint(&self, attempt: u32, seq: u64) -> Result<Option<CheckpointRecord>, ApplyError> {
        stored_checkpoint(&self.reader, self.context, self.rows, attempt, seq)
    }
}

/// The [`HistorySource`] over the History rows of one run that a state reader shows.
pub struct StateHistorySource<'state> {
    state: &'state dyn FlowStateReader,
    context: ApplyContext,
    rows: RunRows,
}

impl<'state> StateHistorySource<'state> {
    /// Returns the source of run `run_id` of the Flow that `context` names.
    pub fn new(state: &'state dyn FlowStateReader, context: ApplyContext, run_id: RunId) -> Self {
        Self {
            state,
            context,
            rows: RunRows::new(context.flow_hash, run_id),
        }
    }
}

impl HistorySource for StateHistorySource<'_> {
    fn history_records(
        &self,
        seqs: RangeInclusive<u64>,
        batch_records: usize,
    ) -> HistoryRecords<'_> {
        stored_history_records(self.state, self.context, self.rows, seqs, batch_records)
    }

    fn checkpoint(&self, attempt: u32, seq: u64) -> Result<Option<CheckpointRecord>, ApplyError> {
        stored_checkpoint(self.state, self.context, self.rows, attempt, seq)
    }
}

fn stored_history_records(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    rows: RunRows,
    seqs: RangeInclusive<u64>,
    batch_records: usize,
) -> HistoryRecords<'_> {
    Box::new(LocalHistoryRecords {
        state,
        context,
        rows,
        next_seq: (!seqs.is_empty()).then_some(*seqs.start()),
        last_seq: *seqs.end(),
        batch_records: batch_records.max(1),
        batch: VecDeque::new(),
    })
}

fn stored_checkpoint(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    rows: RunRows,
    attempt: u32,
    seq: u64,
) -> Result<Option<CheckpointRecord>, ApplyError> {
    FlowTransaction::new(state, context).get_message(
        ColumnFamily::History,
        &checkpoint_key(rows.flow_hash, rows.run_id, attempt, seq),
        "checkpoint",
    )
}

/// Reads local History rows in bounded batches as the caller consumes them.
struct LocalHistoryRecords<'state> {
    state: &'state dyn FlowStateReader,
    context: ApplyContext,
    rows: RunRows,
    /// Seq where the next batch starts, or `None` when no rows remain.
    next_seq: Option<u64>,
    last_seq: u64,
    batch_records: usize,
    batch: VecDeque<HistoryRecord>,
}

impl Iterator for LocalHistoryRecords<'_> {
    type Item = Result<HistoryRecord, ApplyError>;

    fn next(&mut self) -> Option<Self::Item> {
        if self.batch.is_empty() {
            let next_seq = self.next_seq?;
            if let Err(error) = self.read_batch(next_seq) {
                self.next_seq = None;
                self.batch.clear();
                return Some(Err(error));
            }
        }
        self.batch.pop_front().map(Ok)
    }
}

impl LocalHistoryRecords<'_> {
    /// Reads the batch starting at `next_seq`, checking each key against its record's seq.
    fn read_batch(&mut self, next_seq: u64) -> Result<(), ApplyError> {
        let rows = self.rows;
        let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistoryRecord);
        let start = history_record_key(rows.flow_hash, rows.run_id, next_seq);
        let end = match self.last_seq.checked_add(1) {
            Some(end_seq) => history_record_key(rows.flow_hash, rows.run_id, end_seq),
            None => prefix_range(&prefix).1,
        };
        let page: Vec<(Vec<u8>, HistoryRecord)> = FlowTransaction::new(self.state, self.context)
            .scan_messages(
                ColumnFamily::History,
                &start,
                &end,
                self.batch_records,
                "history record",
            )?;
        let is_last_batch = page.len() < self.batch_records;
        self.next_seq = None;
        for (key, record) in page {
            let key_seq = key
                .strip_prefix(prefix.as_slice())
                .and_then(|suffix| <[u8; 8]>::try_from(suffix).ok())
                .map(u64::from_be_bytes)
                .ok_or_else(|| ApplyError::InvariantViolation {
                    detail: format!("history record key {key:02x?} has no seq suffix"),
                })?;
            if record.seq != key_seq {
                return Err(ApplyError::CorruptState {
                    table: "history record",
                    key: hex(&key),
                    detail: format!("the record has seq {} under key seq {key_seq}", record.seq),
                });
            }
            self.next_seq = key_seq.checked_add(1);
            self.batch.push_back(record);
        }
        if is_last_batch {
            self.next_seq = None;
        }
        Ok(())
    }
}
