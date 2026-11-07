// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History reads across uploaded segments and local rows; see the crate README.

use std::cell::{Cell, RefCell};
use std::collections::BTreeMap;
use std::ops::RangeInclusive;
use std::sync::Arc;

use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, HistoryRecord, HistoryRowMetadata, HistorySegmentRecord, RunRecord,
};

use crate::ApplyError;
use crate::history::{
    HistoryRecords, HistorySource, LocalHistorySource, genesis_seq, metadata_identity,
};
use crate::history_offload::segment_catalog::{CatalogPosition, SegmentCatalog, is_uploaded};
use crate::state_access::{FlowTransaction, RunRows};

/// Most metadata rows one search probe reads before it continues past checkpoint rows.
const METADATA_PROBE_ROWS: usize = 16;
/// Catalog rows one read examines before its page ends early.
pub(crate) const MAX_EXAMINED_CATALOG_ROWS: usize = 4096;
/// Catalog rows one round's prefetch examines.
const MAX_PREFETCH_EXAMINED_ROWS: usize = 256;
/// Object bytes one fetch round names; one larger required object is fetched alone.
const MAX_FETCH_OBJECT_BYTES: u64 = 16 << 20;

/// Catalog rows that one replay's prefetch examines.
const MAX_REPLAY_PREFETCH_EXAMINED_ROWS: usize = 4096;
/// Segments whose records one replay round asks for; reached ones come first.
const MAX_REPLAY_FETCH_SEGMENTS: usize = 1024;

/// One checkpoint of an uploaded segment.
#[derive(Debug, Clone, PartialEq)]
pub struct SegmentCheckpoint {
    /// Run attempt of the checkpoint.
    pub attempt: u32,
    /// Seq of the checkpoint.
    pub seq: u64,
    /// The decoded checkpoint.
    pub record: CheckpointRecord,
}

/// History records and checkpoints of uploaded segments that a reader fetched, keyed by seq
/// range.
#[derive(Debug, Clone, Default)]
pub struct LoadedHistorySegments {
    records_by_range: BTreeMap<(u64, u64), Arc<[HistoryRecord]>>,
    checkpoints_by_range: BTreeMap<(u64, u64), Arc<[SegmentCheckpoint]>>,
}

impl LoadedHistorySegments {
    /// Adds the decoded records of `segment`, the catalog row they were fetched for.
    ///
    /// Returns an error, and adds nothing, when the records are not in strictly ascending seq
    /// order inside the segment's seq range.
    pub fn insert(
        &mut self,
        segment: &HistorySegmentRecord,
        records: Arc<[HistoryRecord]>,
    ) -> Result<(), ApplyError> {
        let mut previous_seq = None;
        for record in records.iter() {
            let is_in_order = previous_seq.is_none_or(|previous| record.seq > previous);
            if !is_in_order || record.seq < segment.first_seq || record.seq > segment.last_seq {
                return Err(ApplyError::InvariantViolation {
                    detail: format!(
                        "history segment {}..={} holds record seq {} out of order or range",
                        segment.first_seq, segment.last_seq, record.seq
                    ),
                });
            }
            previous_seq = Some(record.seq);
        }
        self.records_by_range
            .insert((segment.first_seq, segment.last_seq), records);
        Ok(())
    }

    /// Adds the decoded checkpoints of `segment`, the catalog row they were fetched for.
    ///
    /// Returns an error, and adds nothing, when the checkpoints are not in strictly ascending
    /// (seq, attempt) order inside the segment's seq range.
    pub fn insert_checkpoints(
        &mut self,
        segment: &HistorySegmentRecord,
        checkpoints: Arc<[SegmentCheckpoint]>,
    ) -> Result<(), ApplyError> {
        let mut previous_position = None;
        for checkpoint in checkpoints.iter() {
            let position = (checkpoint.seq, checkpoint.attempt);
            let is_in_order = previous_position.is_none_or(|previous| position > previous);
            if !is_in_order
                || checkpoint.seq < segment.first_seq
                || checkpoint.seq > segment.last_seq
            {
                return Err(ApplyError::InvariantViolation {
                    detail: format!(
                        "history segment {}..={} holds checkpoint ({}, {}) out of order or range",
                        segment.first_seq, segment.last_seq, checkpoint.attempt, checkpoint.seq
                    ),
                });
            }
            previous_position = Some(position);
        }
        self.checkpoints_by_range
            .insert((segment.first_seq, segment.last_seq), checkpoints);
        Ok(())
    }

    /// Returns whether no segment was added.
    pub fn is_empty(&self) -> bool {
        self.records_by_range.is_empty() && self.checkpoints_by_range.is_empty()
    }

    /// Returns whether the records of `segment` were added.
    pub fn contains(&self, segment: &HistorySegmentRecord) -> bool {
        self.records_by_range
            .contains_key(&(segment.first_seq, segment.last_seq))
    }

    fn records(&self, segment: &HistorySegmentRecord) -> Option<&[HistoryRecord]> {
        self.records_by_range
            .get(&(segment.first_seq, segment.last_seq))
            .map(AsRef::as_ref)
    }

    fn checkpoints(&self, segment: &HistorySegmentRecord) -> Option<&[SegmentCheckpoint]> {
        self.checkpoints_by_range
            .get(&(segment.first_seq, segment.last_seq))
            .map(AsRef::as_ref)
    }
}

/// The uploaded segments a replay reached unloaded, which it fetches before it runs again.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct MissingHistorySegments {
    /// Segments whose records a stream reached, then the rest of the uploaded segments that
    /// those streams go on to read, in seq order.
    pub records: Vec<HistorySegmentRecord>,
    /// Segments whose checkpoints a checkpoint read asked for, in seq order.
    pub checkpoints: Vec<HistorySegmentRecord>,
}

impl MissingHistorySegments {
    /// Returns whether the replay reached no unloaded segment.
    pub fn is_empty(&self) -> bool {
        self.records.is_empty() && self.checkpoints.is_empty()
    }
}

/// An unloaded segment a stream reached, and the last seq that stream asked for.
#[derive(Debug, Clone)]
struct MissedSegment {
    segment: HistorySegmentRecord,
    stream_last_seq: u64,
}

/// What a read does when it reaches an uploaded segment that is not loaded.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum UnloadedSegmentAction {
    /// End the stream with an error and record the segment.
    Report,
    /// End history before the segment, so the page stops there.
    EndBefore,
}

/// Where a page ends early: before `seq`, whose first event ID is `next_event_id` when known.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct PageEnd {
    pub(crate) seq: u64,
    pub(crate) next_event_id: Option<i64>,
}

/// A catalog lookup, or the stop of a read that examined its budget of catalog rows.
enum CatalogLookup {
    Found(CatalogPosition),
    /// `next_event_id` bounds the events before the stop when a row starts there.
    BudgetExhausted {
        next_event_id: Option<i64>,
    },
}

/// The seqs that a read shows: genesis, and everything window pruning kept.
#[derive(Debug, Clone, Copy)]
struct Visibility {
    genesis_seq: u64,
    /// Seqs after genesis and before this one were pruned.
    first_window_seq: u64,
}

impl Visibility {
    fn is_visible(self, seq: u64) -> bool {
        seq <= self.genesis_seq || seq >= self.first_window_seq
    }

    fn first_visible_from(self, seq: u64) -> u64 {
        if self.is_visible(seq) {
            seq
        } else {
            self.first_window_seq
        }
    }
}

/// The [`HistorySource`] over a run's uploaded segments and one committed view's local rows.
pub(crate) struct SegmentedHistorySource<'view, 'reader> {
    local: LocalHistorySource<'view, 'reader>,
    transaction: &'view FlowTransaction<'reader>,
    rows: RunRows,
    catalog: SegmentCatalog<'view, 'reader>,
    next_run_seq: u64,
    uploaded_through_seq: u64,
    visibility: Visibility,
    loaded: &'view LoadedHistorySegments,
    unloaded_action: UnloadedSegmentAction,
    missing: RefCell<Vec<MissedSegment>>,
    missing_checkpoints: RefCell<Vec<HistorySegmentRecord>>,
    /// The last catalog lookup: its seq and its answer.
    last_lookup: RefCell<Option<(u64, CatalogPosition)>>,
    /// The earliest point where a stream ended history early.
    page_end: Cell<Option<PageEnd>>,
    max_examined_catalog_rows: usize,
}

impl<'view, 'reader> SegmentedHistorySource<'view, 'reader> {
    /// Opens the source of the run whose committed `record` the view holds.
    ///
    /// Returns an error when history metadata cannot be read.
    pub(crate) fn open(
        transaction: &'view FlowTransaction<'reader>,
        rows: RunRows,
        record: &RunRecord,
        loaded: &'view LoadedHistorySegments,
        unloaded_action: UnloadedSegmentAction,
        max_examined_catalog_rows: usize,
    ) -> Result<Self, ApplyError> {
        let uploaded_through_seq = record.history_uploaded_through_seq;
        let genesis_seq = genesis_seq(transaction, rows)?;
        let first_window_seq = if uploaded_through_seq == 0 {
            genesis_seq.saturating_add(1)
        } else {
            first_record_metadata(
                transaction,
                rows,
                genesis_seq.saturating_add(1),
                record.next_run_seq,
            )?
            .map_or(record.next_run_seq, |(seq, _)| seq)
        };
        Ok(Self {
            local: LocalHistorySource::new(transaction, rows),
            transaction,
            rows,
            catalog: SegmentCatalog::new(transaction, rows, uploaded_through_seq),
            next_run_seq: record.next_run_seq,
            uploaded_through_seq,
            visibility: Visibility {
                genesis_seq,
                first_window_seq,
            },
            loaded,
            unloaded_action,
            missing: RefCell::new(Vec::new()),
            missing_checkpoints: RefCell::new(Vec::new()),
            last_lookup: RefCell::new(None),
            page_end: Cell::new(None),
            max_examined_catalog_rows,
        })
    }

    /// Returns the unloaded segments the read reached and, when they fit the fetch budget,
    /// later visible ones whose events help cover `page_events`. Clears the list.
    ///
    /// Returns an error when the catalog cannot be read.
    pub(crate) fn take_missing_segments(
        &self,
        page_events: usize,
    ) -> Result<Vec<HistorySegmentRecord>, ApplyError> {
        let mut required: Vec<HistorySegmentRecord> =
            std::mem::take(&mut *self.missing.borrow_mut())
                .into_iter()
                .map(|missed| missed.segment)
                .collect();
        required.sort_by_key(|segment| segment.first_seq);
        required.dedup_by_key(|segment| segment.first_seq);
        let Some(last_required) = required.last() else {
            return Ok(required);
        };
        let prefetch_from = last_required
            .last_seq
            .saturating_add(1)
            .max(self.visibility.first_window_seq);
        let candidates = self
            .catalog
            .uploaded_from(prefetch_from, MAX_PREFETCH_EXAMINED_ROWS)?
            .into_iter()
            .filter(|segment| {
                segment.first_event_id <= segment.last_event_id && !self.loaded.contains(segment)
            });
        Ok(select_fetch_segments(
            required,
            candidates,
            u64::try_from(page_events).unwrap_or(u64::MAX),
            MAX_FETCH_OBJECT_BYTES,
        ))
    }

    /// Returns the unloaded segments a replay reached: each one's records with the later
    /// uploaded segments its stream goes on to read, at most [`MAX_REPLAY_FETCH_SEGMENTS`] in
    /// all, and the asked-for checkpoints. Clears the lists.
    ///
    /// Returns an error when the catalog cannot be read.
    pub(crate) fn take_replay_misses(&self) -> Result<MissingHistorySegments, ApplyError> {
        let missed_segments = std::mem::take(&mut *self.missing.borrow_mut());
        let mut records: BTreeMap<u64, HistorySegmentRecord> = missed_segments
            .iter()
            .map(|missed| (missed.segment.first_seq, missed.segment.clone()))
            .collect();
        for missed in missed_segments {
            let read_on = self.catalog.uploaded_starting_in(
                missed.segment.last_seq.saturating_add(1),
                missed.stream_last_seq,
                MAX_REPLAY_PREFETCH_EXAMINED_ROWS,
            )?;
            for segment in read_on {
                if records.len() >= MAX_REPLAY_FETCH_SEGMENTS {
                    break;
                }
                if !self.loaded.contains(&segment) {
                    records.insert(segment.first_seq, segment);
                }
            }
        }
        let mut checkpoints = std::mem::take(&mut *self.missing_checkpoints.borrow_mut());
        checkpoints.sort_by_key(|segment| segment.first_seq);
        checkpoints.dedup_by_key(|segment| segment.first_seq);
        Ok(MissingHistorySegments {
            records: records.into_values().collect(),
            checkpoints,
        })
    }

    /// Returns the earliest point where a stream ended history early.
    pub(crate) fn page_end(&self) -> Option<PageEnd> {
        self.page_end.get()
    }

    /// Returns where `seq` falls in the catalog, or `None` once this read examined too many
    /// catalog rows.
    fn position(&self, seq: u64) -> Result<CatalogLookup, ApplyError> {
        let mut is_after_last_lookup = false;
        if let Some((lookup_seq, position)) = self.last_lookup.borrow().as_ref()
            && seq >= *lookup_seq
        {
            if position_holds(position, seq) {
                return Ok(CatalogLookup::Found(position.clone()));
            }
            is_after_last_lookup = position_ends_before(position, seq);
        }
        if self.catalog.examined_rows() >= self.max_examined_catalog_rows {
            // One more row gives the event bound of a row starting at `seq`, the usual stop.
            let next_event_id = if is_after_last_lookup {
                match self.catalog.position_without_earlier_cover(seq)? {
                    CatalogPosition::Covered(segment) => Some(segment.first_event_id),
                    CatalogPosition::Gap { .. } => None,
                }
            } else {
                None
            };
            return Ok(CatalogLookup::BudgetExhausted { next_event_id });
        }
        // Reading on from the last lookup needs one row: nothing earlier can cover `seq`.
        let position = if is_after_last_lookup {
            self.catalog.position_without_earlier_cover(seq)?
        } else {
            self.catalog.position(seq)?
        };
        *self.last_lookup.borrow_mut() = Some((seq, position.clone()));
        Ok(CatalogLookup::Found(position))
    }

    fn end_page_before(&self, page_end: PageEnd) {
        let earliest = match self.page_end.get() {
            Some(current) if current.seq <= page_end.seq => current,
            _ => page_end,
        };
        self.page_end.set(Some(earliest));
    }

    /// Handles a stream asking for seqs through `stream_last_seq` reaching `segment` unloaded;
    /// returns the stream's next item.
    fn reach_unloaded(
        &self,
        segment: HistorySegmentRecord,
        stream_last_seq: u64,
    ) -> Option<Result<HistoryRecord, ApplyError>> {
        let page_end = PageEnd {
            seq: segment.first_seq,
            next_event_id: Some(segment.first_event_id),
        };
        let detail = format!(
            "history segment {}..={} is not loaded",
            segment.first_seq, segment.last_seq
        );
        self.missing.borrow_mut().push(MissedSegment {
            segment,
            stream_last_seq,
        });
        match self.unloaded_action {
            UnloadedSegmentAction::Report => Some(Err(ApplyError::InvariantViolation { detail })),
            UnloadedSegmentAction::EndBefore => {
                self.end_page_before(page_end);
                None
            }
        }
    }
}

impl HistorySource for SegmentedHistorySource<'_, '_> {
    fn history_records(
        &self,
        seqs: RangeInclusive<u64>,
        batch_records: usize,
    ) -> HistoryRecords<'_> {
        Box::new(SegmentedHistoryRecords {
            source: self,
            next_seq: (!seqs.is_empty()).then_some(*seqs.start()),
            last_seq: *seqs.end(),
            batch_records,
            local: None,
        })
    }

    /// Reads a checkpoint that an UPLOADED segment covers from its loaded checkpoints; an
    /// unloaded one ends the read with an error and is recorded.
    fn checkpoint(&self, attempt: u32, seq: u64) -> Result<Option<CheckpointRecord>, ApplyError> {
        let segment = match self.catalog.position(seq)? {
            CatalogPosition::Covered(segment) if is_uploaded(&segment) => segment,
            _ => return self.local.checkpoint(attempt, seq),
        };
        let Some(checkpoints) = self.loaded.checkpoints(&segment) else {
            let detail = format!(
                "checkpoint ({attempt}, {seq}) of history segment {}..={} is not loaded",
                segment.first_seq, segment.last_seq
            );
            self.missing_checkpoints.borrow_mut().push(segment);
            return Err(ApplyError::InvariantViolation { detail });
        };
        Ok(checkpoints
            .binary_search_by_key(&(seq, attempt), |checkpoint| {
                (checkpoint.seq, checkpoint.attempt)
            })
            .ok()
            .and_then(|index| checkpoints.get(index))
            .map(|checkpoint| checkpoint.record.clone()))
    }

    /// Binary-searches local history metadata, whose `last_assigned_event_id` grows with seq.
    /// Metadata written before that field existed falls back to catalog event ranges.
    fn event_search_start_seq(&self, first_event_id: i64) -> Result<Option<u64>, ApplyError> {
        if self.uploaded_through_seq == 0 {
            return Ok(None);
        }
        match self.metadata_search_start_seq(first_event_id)? {
            Some(seq) => Ok(Some(seq)),
            None => self.catalog.seq_after_events_below(first_event_id),
        }
    }
}

impl SegmentedHistorySource<'_, '_> {
    /// Returns the first record seq whose metadata reaches `first_event_id`, or `None` for
    /// metadata without event IDs.
    fn metadata_search_start_seq(&self, first_event_id: i64) -> Result<Option<u64>, ApplyError> {
        let mut low = self.visibility.genesis_seq;
        let mut high = self.next_run_seq;
        let mut found_seq = None;
        while low < high {
            let probe = low + (high - low) / 2;
            let Some((seq, metadata)) =
                first_record_metadata(self.transaction, self.rows, probe, high)?
            else {
                high = probe;
                continue;
            };
            if metadata.last_assigned_event_id == 0 {
                return Ok(None);
            }
            if metadata.last_assigned_event_id >= first_event_id {
                found_seq = Some(seq);
                high = probe;
            } else {
                low = seq.saturating_add(1);
            }
        }
        Ok(found_seq)
    }
}

/// Returns whether `position` ends exactly before `seq`, so no earlier row covers `seq`.
fn position_ends_before(position: &CatalogPosition, seq: u64) -> bool {
    match position {
        CatalogPosition::Covered(segment) => segment.last_seq.checked_add(1) == Some(seq),
        CatalogPosition::Gap { next_first_seq } => *next_first_seq == Some(seq),
    }
}

/// Returns whether `position`, looked up at or before `seq`, still describes `seq`.
fn position_holds(position: &CatalogPosition, seq: u64) -> bool {
    match position {
        CatalogPosition::Covered(segment) => seq <= segment.last_seq,
        CatalogPosition::Gap { next_first_seq } => next_first_seq.is_none_or(|next| seq < next),
    }
}

/// Streams records of a [`SegmentedHistorySource`] in ascending seq order.
struct SegmentedHistoryRecords<'source, 'view, 'reader> {
    source: &'source SegmentedHistorySource<'view, 'reader>,
    /// Seq the next record may have, or `None` once the stream ended.
    next_seq: Option<u64>,
    last_seq: u64,
    batch_records: usize,
    /// Local rows of a seq range that no uploaded segment covers, with the range's last seq.
    local: Option<(HistoryRecords<'source>, u64)>,
}

impl Iterator for SegmentedHistoryRecords<'_, '_, '_> {
    type Item = Result<HistoryRecord, ApplyError>;

    fn next(&mut self) -> Option<Self::Item> {
        self.next_record()
    }
}

impl SegmentedHistoryRecords<'_, '_, '_> {
    fn next_record(&mut self) -> Option<Result<HistoryRecord, ApplyError>> {
        let source = self.source;
        let last_seq = match source.page_end.get() {
            Some(page_end) => self.last_seq.min(page_end.seq.checked_sub(1)?),
            None => self.last_seq,
        };
        loop {
            if let Some((local, range_last_seq)) = self.local.as_mut() {
                match local.next() {
                    Some(Ok(record)) if record.seq <= last_seq => return Some(Ok(record)),
                    Some(Ok(_)) => return self.end(None),
                    Some(Err(error)) => return self.end(Some(Err(error))),
                    None => {
                        self.next_seq = range_last_seq.checked_add(1);
                        self.local = None;
                        continue;
                    }
                }
            }
            // Pruned seqs hold nothing, so the catalog is never searched for them.
            let seq = source.visibility.first_visible_from(self.next_seq?);
            if seq > last_seq {
                return self.end(None);
            }
            let position = match source.position(seq) {
                Ok(CatalogLookup::Found(position)) => position,
                Ok(CatalogLookup::BudgetExhausted { next_event_id }) => {
                    source.end_page_before(PageEnd { seq, next_event_id });
                    return self.end(None);
                }
                Err(error) => return self.end(Some(Err(error))),
            };
            let local_last_seq = match position {
                CatalogPosition::Covered(segment) if is_uploaded(&segment) => {
                    let records = source.loaded.records(&segment);
                    match next_uploaded_step(records, &segment, seq, last_seq, source.visibility) {
                        UploadedStep::Record(record) => {
                            self.next_seq = record.seq.checked_add(1);
                            return Some(Ok(record.clone()));
                        }
                        UploadedStep::SkipTo(next_seq) => {
                            self.next_seq = Some(next_seq);
                            continue;
                        }
                        UploadedStep::Unloaded => {
                            let item = source.reach_unloaded(segment, self.last_seq);
                            return self.end(item);
                        }
                    }
                }
                CatalogPosition::Covered(segment) => segment.last_seq.min(last_seq),
                CatalogPosition::Gap { next_first_seq } => {
                    next_first_seq.map_or(last_seq, |next| next.saturating_sub(1).min(last_seq))
                }
            };
            self.local = Some((
                source
                    .local
                    .history_records(seq..=local_last_seq, self.batch_records),
                local_last_seq,
            ));
        }
    }

    fn end(
        &mut self,
        item: Option<Result<HistoryRecord, ApplyError>>,
    ) -> Option<Result<HistoryRecord, ApplyError>> {
        self.next_seq = None;
        self.local = None;
        item
    }
}

/// What the stream does at a seq that an UPLOADED segment covers.
#[derive(Debug, PartialEq)]
enum UploadedStep<'loaded> {
    Record(&'loaded HistoryRecord),
    SkipTo(u64),
    Unloaded,
}

/// Returns the next visible record of `segment` in `[seq, last_seq]`; `segment` covers `seq`.
fn next_uploaded_step<'loaded>(
    records: Option<&'loaded [HistoryRecord]>,
    segment: &HistorySegmentRecord,
    seq: u64,
    last_seq: u64,
    visibility: Visibility,
) -> UploadedStep<'loaded> {
    let after_segment_seq = segment.last_seq.saturating_add(1);
    let range_last_seq = segment.last_seq.min(last_seq);
    let visible_seq = visibility.first_visible_from(seq);
    if visible_seq > range_last_seq {
        return UploadedStep::SkipTo(after_segment_seq.min(visible_seq));
    }
    if visible_seq > seq {
        return UploadedStep::SkipTo(visible_seq);
    }
    let Some(records) = records else {
        return UploadedStep::Unloaded;
    };
    let index = records.partition_point(|record| record.seq < seq);
    match records.get(index) {
        Some(record) if record.seq <= range_last_seq => {
            if visibility.is_visible(record.seq) {
                UploadedStep::Record(record)
            } else {
                UploadedStep::SkipTo(visibility.first_visible_from(record.seq))
            }
        }
        _ => UploadedStep::SkipTo(range_last_seq.saturating_add(1)),
    }
}

/// Picks one fetch round's segments within `max_object_bytes`: required ones first, then
/// candidates until their events cover `page_events`.
///
/// Returns at least the first required segment, alone when it exceeds the budget by itself.
fn select_fetch_segments(
    required: Vec<HistorySegmentRecord>,
    candidates: impl IntoIterator<Item = HistorySegmentRecord>,
    page_events: u64,
    max_object_bytes: u64,
) -> Vec<HistorySegmentRecord> {
    let mut selected: Vec<HistorySegmentRecord> = Vec::new();
    let mut object_bytes = 0_u64;
    for segment in required {
        let with_segment = object_bytes.saturating_add(segment.object_size_bytes);
        if !selected.is_empty() && with_segment > max_object_bytes {
            return selected;
        }
        object_bytes = with_segment;
        selected.push(segment);
    }
    let mut events = selected.iter().map(segment_event_count).sum::<u64>();
    for segment in candidates {
        let with_segment = object_bytes.saturating_add(segment.object_size_bytes);
        if events >= page_events || with_segment > max_object_bytes {
            break;
        }
        object_bytes = with_segment;
        events = events.saturating_add(segment_event_count(&segment));
        selected.push(segment);
    }
    selected
}

fn segment_event_count(segment: &HistorySegmentRecord) -> u64 {
    u64::try_from(
        segment
            .last_event_id
            .saturating_sub(segment.first_event_id)
            .saturating_add(1),
    )
    .unwrap_or(0)
}

/// Returns the seq and metadata of the first HistoryRecord metadata row in `[seq, end)`.
///
/// Checkpoint rows are skipped: pruning may delete a record's row but keep a checkpoint's.
fn first_record_metadata(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    seq: u64,
    end: u64,
) -> Result<Option<(u64, HistoryRowMetadata)>, ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistoryRowMetadata);
    let mut start = seq_key(&prefix, seq);
    let end_key = if end == u64::MAX {
        prefix_range(&prefix).1
    } else {
        seq_key(&prefix, end)
    };
    loop {
        let page = transaction.scan_messages::<HistoryRowMetadata>(
            ColumnFamily::Flow,
            &start,
            &end_key,
            METADATA_PROBE_ROWS,
            "history metadata",
        )?;
        let is_last_page = page.len() < METADATA_PROBE_ROWS;
        let mut last_key = None;
        for (key, metadata) in page {
            let (row_seq, table, _) = metadata_identity(&key, &prefix)?;
            if table == RunTable::HistoryRecord {
                return Ok(Some((row_seq, metadata)));
            }
            last_key = Some(key);
        }
        match last_key {
            Some(mut key) if !is_last_page => {
                key.push(0);
                start = key;
            }
            _ => return Ok(None),
        }
    }
}

/// Returns the smallest metadata key with `seq`: the prefix followed by the seq.
fn seq_key(prefix: &[u8], seq: u64) -> Vec<u8> {
    let mut key = prefix.to_vec();
    key.extend_from_slice(&seq.to_be_bytes());
    key
}

#[cfg(test)]
mod tests {
    use dexzd_protocol::dexzd::v1::{HistoryRecord, HistorySegmentRecord};

    use super::{UploadedStep, Visibility, next_uploaded_step, select_fetch_segments};

    const EVERYTHING_VISIBLE: Visibility = Visibility {
        genesis_seq: 1,
        first_window_seq: 2,
    };
    const MIB: u64 = 1 << 20;

    fn record(seq: u64) -> HistoryRecord {
        HistoryRecord {
            seq,
            ..HistoryRecord::default()
        }
    }

    fn segment(first_seq: u64, object_size_bytes: u64) -> HistorySegmentRecord {
        HistorySegmentRecord {
            first_seq,
            last_seq: first_seq,
            first_event_id: i64::try_from(first_seq).expect("small seq"),
            last_event_id: i64::try_from(first_seq).expect("small seq"),
            object_size_bytes,
            ..HistorySegmentRecord::default()
        }
    }

    fn first_seqs(segments: &[HistorySegmentRecord]) -> Vec<u64> {
        segments.iter().map(|segment| segment.first_seq).collect()
    }

    #[test]
    fn a_sparse_segment_never_returns_a_record_past_the_requested_last_seq() {
        let segment = HistorySegmentRecord {
            first_seq: 5,
            last_seq: 9,
            ..HistorySegmentRecord::default()
        };
        let records = [record(5), record(9)];
        let step = next_uploaded_step(Some(&records), &segment, 6, 8, EVERYTHING_VISIBLE);
        assert_eq!(step, UploadedStep::SkipTo(9), "the stream ends after seq 8");
        let step = next_uploaded_step(Some(&records), &segment, 6, 9, EVERYTHING_VISIBLE);
        assert_eq!(step, UploadedStep::Record(&records[1]));
        let step = next_uploaded_step(Some(&records), &segment, 5, 8, EVERYTHING_VISIBLE);
        assert_eq!(step, UploadedStep::Record(&records[0]));
    }

    #[test]
    fn records_before_the_window_are_skipped_and_an_unloaded_segment_is_reported() {
        let segment = HistorySegmentRecord {
            first_seq: 1,
            last_seq: 9,
            ..HistorySegmentRecord::default()
        };
        let records = [record(1), record(3), record(7)];
        let window = Visibility {
            genesis_seq: 1,
            first_window_seq: 6,
        };
        assert_eq!(
            next_uploaded_step(Some(&records), &segment, 1, 9, window),
            UploadedStep::Record(&records[0])
        );
        assert_eq!(
            next_uploaded_step(Some(&records), &segment, 2, 9, window),
            UploadedStep::SkipTo(6)
        );
        assert_eq!(
            next_uploaded_step(Some(&records), &segment, 6, 9, window),
            UploadedStep::Record(&records[2])
        );
        assert_eq!(
            next_uploaded_step(None, &segment, 6, 9, window),
            UploadedStep::Unloaded
        );
    }

    #[test]
    fn fetch_rounds_charge_required_segments_first_and_never_exceed_the_budget() {
        let budget = 16 * MIB;
        // Two 12 MiB objects: the second waits for the next round.
        let selected = select_fetch_segments(
            vec![segment(1, 12 * MIB)],
            vec![segment(2, 12 * MIB)],
            100,
            budget,
        );
        assert_eq!(first_seqs(&selected), [1]);
        let selected = select_fetch_segments(
            vec![segment(1, 12 * MIB), segment(2, 12 * MIB)],
            Vec::new(),
            100,
            budget,
        );
        assert_eq!(first_seqs(&selected), [1]);
        // An oversized required object goes alone; nothing speculative joins it.
        let selected =
            select_fetch_segments(vec![segment(1, 20 * MIB)], vec![segment(2, 1)], 100, budget);
        assert_eq!(first_seqs(&selected), [1]);
        // An oversized speculative object is never fetched.
        let selected = select_fetch_segments(
            vec![segment(1, MIB)],
            vec![segment(2, 20 * MIB), segment(3, MIB)],
            100,
            budget,
        );
        assert_eq!(first_seqs(&selected), [1]);
        // Small segments fill the budget until their events cover the page.
        let candidates: Vec<_> = (2..10).map(|seq| segment(seq, MIB)).collect();
        let selected = select_fetch_segments(vec![segment(1, MIB)], candidates, 4, budget);
        assert_eq!(first_seqs(&selected), [1, 2, 3, 4]);
    }
}
