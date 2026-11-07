// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Seek-based lookups of a run's history segment catalog that count every row they examine.

use std::cell::Cell;

use dexzd_keyspace::ColumnFamily;
use dexzd_keyspace::row_keys::history_segment_key;
use dexzd_protocol::dexzd::v1::{HistorySegmentRecord, HistorySegmentState};

use crate::ApplyError;
use crate::state_access::{FlowTransaction, RunRows};

/// Catalog rows one prefetch scan reads at once.
const CATALOG_SCAN_ROWS: usize = 16;

/// Where a seq at most the uploaded seq U falls in the catalog.
#[derive(Debug, Clone, PartialEq)]
pub(crate) enum CatalogPosition {
    /// The catalog row whose range holds the seq, in any state.
    Covered(HistorySegmentRecord),
    /// No row holds the seq; the next row, if any, starts at this seq.
    Gap { next_first_seq: Option<u64> },
}

/// Looks up the catalog rows that start at most at the run's uploaded seq U.
pub(crate) struct SegmentCatalog<'view, 'reader> {
    transaction: &'view FlowTransaction<'reader>,
    rows: RunRows,
    uploaded_through_seq: u64,
    examined_rows: Cell<usize>,
}

impl<'view, 'reader> SegmentCatalog<'view, 'reader> {
    pub(crate) fn new(
        transaction: &'view FlowTransaction<'reader>,
        rows: RunRows,
        uploaded_through_seq: u64,
    ) -> Self {
        Self {
            transaction,
            rows,
            uploaded_through_seq,
            examined_rows: Cell::new(0),
        }
    }

    /// Returns the catalog rows these lookups have read so far.
    pub(crate) fn examined_rows(&self) -> usize {
        self.examined_rows.get()
    }

    /// Returns where `seq` falls, reading a number of rows logarithmic in `seq`.
    ///
    /// Returns an error when the catalog cannot be read or names an impossible range.
    pub(crate) fn position(&self, seq: u64) -> Result<CatalogPosition, ApplyError> {
        if seq > self.uploaded_through_seq {
            return Ok(CatalogPosition::Gap {
                next_first_seq: None,
            });
        }
        if let Some(segment) = self.last_starting_at_or_before(seq)?
            && segment.last_seq >= seq
        {
            return Ok(CatalogPosition::Covered(segment));
        }
        let next_first_seq = self
            .first_starting_in(seq.saturating_add(1), self.uploaded_through_seq)?
            .map(|segment| segment.first_seq);
        Ok(CatalogPosition::Gap { next_first_seq })
    }

    /// Returns where `seq` falls when no row starting before `seq` covers it, reading one row.
    ///
    /// Returns an error when the catalog cannot be read or names an impossible range.
    pub(crate) fn position_without_earlier_cover(
        &self,
        seq: u64,
    ) -> Result<CatalogPosition, ApplyError> {
        if seq > self.uploaded_through_seq {
            return Ok(CatalogPosition::Gap {
                next_first_seq: None,
            });
        }
        Ok(
            match self.first_starting_in(seq, self.uploaded_through_seq)? {
                Some(segment) if segment.first_seq == seq => CatalogPosition::Covered(segment),
                next => CatalogPosition::Gap {
                    next_first_seq: next.map(|segment| segment.first_seq),
                },
            },
        )
    }

    /// Returns the seq after the last row whose events all precede `first_event_id`, or `None`
    /// when no row does. No record before that seq holds a later event, since event IDs grow
    /// with seq.
    ///
    /// Returns an error when the catalog cannot be read or names an impossible range.
    pub(crate) fn seq_after_events_below(
        &self,
        first_event_id: i64,
    ) -> Result<Option<u64>, ApplyError> {
        let is_below = |segment: &HistorySegmentRecord| {
            let last_event_id = if segment.first_event_id <= segment.last_event_id {
                segment.last_event_id
            } else {
                segment.first_event_id.saturating_sub(1)
            };
            last_event_id < first_event_id
        };
        let last_seq = self.uploaded_through_seq;
        let Some(mut found) = self.first_starting_in(0, last_seq)? else {
            return Ok(None);
        };
        if !is_below(&found) {
            return Ok(None);
        }
        let (mut low, mut high) = (found.first_seq, last_seq);
        while low < high {
            let probe = low + (high - low).div_ceil(2);
            match self.first_starting_in(probe, last_seq)? {
                Some(segment) if is_below(&segment) => {
                    low = segment.first_seq;
                    found = segment;
                }
                _ => high = probe - 1,
            }
        }
        Ok(Some(found.last_seq.saturating_add(1)))
    }

    /// Returns the UPLOADED rows that start at or after `first_seq`, in seq order, after
    /// reading at most `max_examined_rows` rows.
    ///
    /// Returns an error when the catalog cannot be read or names an impossible range.
    pub(crate) fn uploaded_from(
        &self,
        first_seq: u64,
        max_examined_rows: usize,
    ) -> Result<Vec<HistorySegmentRecord>, ApplyError> {
        self.uploaded_starting_in(first_seq, u64::MAX, max_examined_rows)
    }

    /// Returns the UPLOADED rows that start in `[first_seq, last_seq]`, in seq order, after
    /// reading at most `max_examined_rows` rows.
    ///
    /// Returns an error when the catalog cannot be read or names an impossible range.
    pub(crate) fn uploaded_starting_in(
        &self,
        first_seq: u64,
        last_seq: u64,
        max_examined_rows: usize,
    ) -> Result<Vec<HistorySegmentRecord>, ApplyError> {
        let range_last_seq = self.uploaded_through_seq.min(last_seq);
        if first_seq > range_last_seq {
            return Ok(Vec::new());
        }
        let end = self.key(range_last_seq.saturating_add(1));
        let mut start = self.key(first_seq);
        let mut segments = Vec::new();
        let mut examined = 0;
        while examined < max_examined_rows {
            let page = self.transaction.scan_messages::<HistorySegmentRecord>(
                ColumnFamily::Flow,
                &start,
                &end,
                CATALOG_SCAN_ROWS.min(max_examined_rows - examined),
                "history segment",
            )?;
            let page_rows = page.len();
            examined += page_rows;
            self.note_examined(page_rows);
            for (_, segment) in page {
                self.check_range(&segment)?;
                start = self.key(segment.first_seq.saturating_add(1));
                if is_uploaded(&segment) {
                    segments.push(segment);
                }
            }
            if page_rows < CATALOG_SCAN_ROWS {
                break;
            }
        }
        Ok(segments)
    }

    /// Returns the row with the largest first seq at or below `seq`, in any state.
    ///
    /// Binary-searches with one-row scans, since keys sort by first seq.
    fn last_starting_at_or_before(
        &self,
        seq: u64,
    ) -> Result<Option<HistorySegmentRecord>, ApplyError> {
        let Some(mut found) = self.first_starting_in(0, seq)? else {
            return Ok(None);
        };
        let (mut low, mut high) = (found.first_seq, seq);
        while low < high {
            let probe = low + (high - low).div_ceil(2);
            match self.first_starting_in(probe, seq)? {
                Some(segment) => {
                    low = segment.first_seq;
                    found = segment;
                }
                None => high = probe - 1,
            }
        }
        Ok(Some(found))
    }

    /// Returns the first row that starts in `[first_seq, last_seq]`.
    fn first_starting_in(
        &self,
        first_seq: u64,
        last_seq: u64,
    ) -> Result<Option<HistorySegmentRecord>, ApplyError> {
        if first_seq > last_seq {
            return Ok(None);
        }
        let page = self.transaction.scan_messages::<HistorySegmentRecord>(
            ColumnFamily::Flow,
            &self.key(first_seq),
            &self.key(last_seq.saturating_add(1)),
            1,
            "history segment",
        )?;
        self.note_examined(page.len());
        let Some((_, segment)) = page.into_iter().next() else {
            return Ok(None);
        };
        self.check_range(&segment)?;
        Ok(Some(segment))
    }

    fn note_examined(&self, rows: usize) {
        self.examined_rows
            .set(self.examined_rows.get().saturating_add(rows));
    }

    fn check_range(&self, segment: &HistorySegmentRecord) -> Result<(), ApplyError> {
        let is_uploaded_past_u =
            is_uploaded(segment) && segment.last_seq > self.uploaded_through_seq;
        if segment.first_seq > segment.last_seq || is_uploaded_past_u {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "uploaded history segment {}..={} is not inside uploaded seq {}",
                    segment.first_seq, segment.last_seq, self.uploaded_through_seq
                ),
            });
        }
        Ok(())
    }

    fn key(&self, first_seq: u64) -> Vec<u8> {
        history_segment_key(self.rows.flow_hash, self.rows.run_id, first_seq)
    }
}

/// Returns whether the catalog row is UPLOADED.
pub(crate) fn is_uploaded(segment: &HistorySegmentRecord) -> bool {
    segment.state == HistorySegmentState::Uploaded as i32
}
