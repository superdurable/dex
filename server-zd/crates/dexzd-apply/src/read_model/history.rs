// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::ops::Range;

use dexzd_keyspace::{FlowHash, RunId};
use dexzd_protocol::dex::{FlowHistoryEvent, GetHistoryEventsResponse, HistoryEventIdRange};
use dexzd_protocol::dexzd::v1::{HistoryRecord, HistorySegmentRecord};
use prost::Message;

use crate::history::{HistorySource, LocalHistorySource, genesis_seq};
use crate::history_offload::segmented_history::{
    LoadedHistorySegments, MAX_EXAMINED_CATALOG_ROWS, PageEnd, SegmentedHistorySource,
    UnloadedSegmentAction,
};
use crate::read_model::{ReadModelError, RunView};
use crate::{ApplyError, FlowStateReader};

/// Most History rows read by one scan call.
const MAX_HISTORY_SCAN_ROWS: usize = 256;

/// Most events of one page; a page read from object storage stays bounded in cost.
pub const MAX_HISTORY_PAGE_EVENTS: usize = 1000;

/// FlowStart records the first event of every run, so window pruning removes IDs from 2.
const FIRST_PRUNABLE_EVENT_ID: i64 = 2;

/// Field number of `events` in `GetHistoryEventsResponse`.
const EVENTS_FIELD_NUMBER: u32 = 1;

/// Reads one page of events from an attempt's lineage, using event IDs as cursors.
///
/// The page ends early when one more event would make the encoded response larger than
/// `max_response_bytes`. It still holds at least one event, and its next page token continues
/// after its last event. Returns `HistoryEventTooLarge` when the first event alone exceeds
/// the limit.
#[expect(
    clippy::too_many_arguments,
    reason = "Mirrors the GetHistoryEvents request fields plus the response size limit."
)]
pub fn get_history_events(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    start_event_id: i64,
    page_size: i32,
    next_page_token: &[u8],
    run_attempt: i32,
    max_response_bytes: usize,
) -> Result<GetHistoryEventsResponse, ReadModelError> {
    let request = HistoryPageRequest::parse(
        start_event_id,
        page_size,
        next_page_token,
        run_attempt,
        max_response_bytes,
    )?;
    let view = RunView::open(reader, flow_hash, run_id)?;
    let history = LocalHistorySource::new(&view.transaction, view.rows);
    read_history_page(&view, &history, &request, None)
}

/// Like [`get_history_events`], but reads History records from `history`, which holds every
/// unpruned record.
#[expect(
    clippy::too_many_arguments,
    reason = "mirrors get_history_events plus the history source"
)]
pub fn get_history_events_from_source(
    reader: &dyn FlowStateReader,
    history: &dyn HistorySource,
    flow_hash: FlowHash,
    run_id: RunId,
    start_event_id: i64,
    page_size: i32,
    next_page_token: &[u8],
    run_attempt: i32,
    max_response_bytes: usize,
) -> Result<GetHistoryEventsResponse, ReadModelError> {
    let request = HistoryPageRequest::parse(
        start_event_id,
        page_size,
        next_page_token,
        run_attempt,
        max_response_bytes,
    )?;
    let view = RunView::open(reader, flow_hash, run_id)?;
    read_history_page(&view, history, &request, None)
}

/// A history page read over uploaded history segments and local rows.
#[derive(Debug)]
pub enum SegmentedHistoryPage {
    /// The page, read entirely from loaded segments and the committed view.
    Page(GetHistoryEventsResponse),
    /// Uploaded segments the read needs, in seq order; fetch them and read again.
    NeedsSegments(Vec<HistorySegmentRecord>),
}

/// Like [`get_history_events`], but takes covered records from `segments` and the rest locally.
///
/// When the read reaches an uploaded segment that `segments` lacks, it returns
/// [`SegmentedHistoryPage::NeedsSegments`], naming it and later segments the page likely needs.
/// With `should_end_before_unloaded_segment`, it instead returns the shorter page of the events
/// before that segment, with a next page token that continues at it, when that page makes
/// progress. It never returns a page that skips a segment.
#[expect(
    clippy::too_many_arguments,
    reason = "mirrors get_history_events plus the loaded segments and the round's policy"
)]
pub fn get_history_events_from_segments(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    start_event_id: i64,
    page_size: i32,
    next_page_token: &[u8],
    run_attempt: i32,
    max_response_bytes: usize,
    segments: &LoadedHistorySegments,
    should_end_before_unloaded_segment: bool,
) -> Result<SegmentedHistoryPage, ReadModelError> {
    let request = HistoryPageRequest::parse(
        start_event_id,
        page_size,
        next_page_token,
        run_attempt,
        max_response_bytes,
    )?;
    read_segmented_history_page(
        reader,
        flow_hash,
        run_id,
        &request,
        segments,
        should_end_before_unloaded_segment,
        MAX_EXAMINED_CATALOG_ROWS,
    )
}

/// Like [`get_history_events_from_segments`] without ending early at unloaded segments, but
/// with a test's catalog row budget per read.
#[cfg(feature = "test-util")]
#[expect(
    clippy::too_many_arguments,
    reason = "mirrors get_history_events_from_segments plus the budget"
)]
pub fn get_history_events_from_segments_with_catalog_budget_for_test(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    start_event_id: i64,
    page_size: i32,
    next_page_token: &[u8],
    max_response_bytes: usize,
    segments: &LoadedHistorySegments,
    max_examined_catalog_rows: usize,
) -> Result<SegmentedHistoryPage, ReadModelError> {
    let request = HistoryPageRequest::parse(
        start_event_id,
        page_size,
        next_page_token,
        0,
        max_response_bytes,
    )?;
    read_segmented_history_page(
        reader,
        flow_hash,
        run_id,
        &request,
        segments,
        false,
        max_examined_catalog_rows,
    )
}

fn read_segmented_history_page(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    request: &HistoryPageRequest,
    segments: &LoadedHistorySegments,
    should_end_before_unloaded_segment: bool,
    max_examined_catalog_rows: usize,
) -> Result<SegmentedHistoryPage, ReadModelError> {
    let view = RunView::open(reader, flow_hash, run_id)?;
    // A separate source and budget, so an early page end never cuts the legacy pruned range scan.
    let pruned_source = SegmentedHistorySource::open(
        &view.transaction,
        view.rows,
        &view.record,
        segments,
        UnloadedSegmentAction::Report,
        max_examined_catalog_rows,
    )?;
    let pruned_range = pruned_event_id_range(&view, &pruned_source);
    let pruned_missing = pruned_source.take_missing_segments(1)?;
    if !pruned_missing.is_empty() {
        return Ok(SegmentedHistoryPage::NeedsSegments(pruned_missing));
    }
    let pruned_range = match pruned_source.page_end() {
        // The scan saw only eventless records before its budget stop, so events below the
        // stop's bound were all pruned; reporting less never hides a retained event.
        Some(page_end) => page_end
            .next_event_id
            .map(|next_event_id| next_event_id.saturating_sub(1))
            .filter(|end_event_id| *end_event_id >= FIRST_PRUNABLE_EVENT_ID)
            .map(|end_event_id| HistoryEventIdRange {
                start_event_id: FIRST_PRUNABLE_EVENT_ID,
                end_event_id,
            }),
        None => pruned_range?,
    };
    let unloaded_action = if should_end_before_unloaded_segment {
        UnloadedSegmentAction::EndBefore
    } else {
        UnloadedSegmentAction::Report
    };
    let history = SegmentedHistorySource::open(
        &view.transaction,
        view.rows,
        &view.record,
        segments,
        unloaded_action,
        max_examined_catalog_rows,
    )?;
    let page = read_history_page(&view, &history, request, Some(pruned_range));
    // A missing segment ends the stream with an error, so check for one before the result.
    let page_end = history.page_end();
    let missing = history.take_missing_segments(request.page_events().saturating_add(1))?;
    match page_end {
        // More rounds cannot remove a catalog budget stop, so it ends the page in any round.
        Some(page_end) if should_end_before_unloaded_segment || missing.is_empty() => {
            if let Some(page) = continue_before(page?, page_end, request)? {
                return Ok(SegmentedHistoryPage::Page(page));
            }
        }
        None if missing.is_empty() => return page.map(SegmentedHistoryPage::Page),
        _ => {}
    }
    Ok(SegmentedHistoryPage::NeedsSegments(missing))
}

/// Adds a token continuing at `page_end`, dropping last events while the response is too big.
///
/// Returns `None` when the page would hold no event and the token would not move past the
/// request's first event, so the caller cannot make progress with it. Returns
/// `HistoryEventTooLarge` when its single event fits only without the token.
fn continue_before(
    mut page: GetHistoryEventsResponse,
    page_end: PageEnd,
    request: &HistoryPageRequest,
) -> Result<Option<GetHistoryEventsResponse>, ReadModelError> {
    if !page.next_page_token.is_empty() {
        return Ok(Some(page));
    }
    // Event IDs grow with seq, so every event before the end was read.
    let last_read_event_id = match page.events.last() {
        Some(event) => event.event_id,
        None => match page_end.next_event_id {
            Some(next_event_id) => next_event_id.saturating_sub(1),
            None => return Ok(None),
        },
    };
    if last_read_event_id < request.first_event_id.max(1) {
        return Ok(None);
    }
    set_continuation(&mut page, last_read_event_id);
    while page.encoded_len() > request.max_response_bytes {
        if page.events.len() <= 1 {
            let Some(event) = page.events.first() else {
                return Ok(None);
            };
            return Err(ReadModelError::HistoryEventTooLarge {
                event_id: event.event_id,
                size_bytes: page.encoded_len(),
                max_response_bytes: request.max_response_bytes,
            });
        }
        page.events.pop();
        let last_event_id = page
            .events
            .last()
            .map_or(last_read_event_id, |event| event.event_id);
        set_continuation(&mut page, last_event_id);
    }
    Ok(Some(page))
}

fn set_continuation(page: &mut GetHistoryEventsResponse, last_event_id: i64) {
    page.next_page_token = last_event_id.to_be_bytes().to_vec();
    page.next_internal_event_id = last_event_id.saturating_add(1);
}

/// A validated history page request.
struct HistoryPageRequest {
    first_event_id: i64,
    page_size: i32,
    run_attempt: i32,
    max_response_bytes: usize,
}

impl HistoryPageRequest {
    /// Returns the events the page holds at most: 100 for size 0, never more than 1000.
    fn page_events(&self) -> usize {
        if self.page_size == 0 {
            100
        } else {
            usize::try_from(self.page_size)
                .unwrap_or(usize::MAX)
                .min(MAX_HISTORY_PAGE_EVENTS)
        }
    }

    fn parse(
        start_event_id: i64,
        page_size: i32,
        next_page_token: &[u8],
        run_attempt: i32,
        max_response_bytes: usize,
    ) -> Result<Self, ReadModelError> {
        if start_event_id < 0 || page_size < 0 || run_attempt < 0 {
            return Err(ReadModelError::InvalidRequest {
                detail: "event ID, page size, and run attempt must be non-negative".to_owned(),
            });
        }
        let mut first_event_id = start_event_id.max(1);
        if !next_page_token.is_empty() {
            let token: [u8; 8] =
                next_page_token
                    .try_into()
                    .map_err(|_| ReadModelError::InvalidRequest {
                        detail: "history page token must contain one event ID".to_owned(),
                    })?;
            let last_event_id = i64::from_be_bytes(token);
            if last_event_id < 1 {
                return Err(ReadModelError::InvalidRequest {
                    detail: "history page token has an invalid event ID".to_owned(),
                });
            }
            first_event_id = first_event_id.max(last_event_id.saturating_add(1));
        }
        Ok(Self {
            first_event_id,
            page_size,
            run_attempt,
            max_response_bytes,
        })
    }
}

/// Reads the page `request` names from the run `view` opened.
///
/// A given `known_pruned_range` replaces the pruned range computed from `history`.
fn read_history_page(
    view: &RunView<'_>,
    history: &dyn HistorySource,
    request: &HistoryPageRequest,
    known_pruned_range: Option<Option<HistoryEventIdRange>>,
) -> Result<GetHistoryEventsResponse, ReadModelError> {
    let first_event_id = request.first_event_id;
    let selected_attempt = if request.run_attempt == 0 {
        view.record.current_attempt
    } else {
        u32::try_from(request.run_attempt).unwrap_or(u32::MAX)
    };
    let lineage = lineage_cutoffs(view, selected_attempt)?;
    let size = request.page_events();
    let max_response_bytes = request.max_response_bytes;
    let history_rows = HistoryRecordRows::new(view, history);
    let mut events = history_rows.collect_lineage_events(
        &lineage,
        first_event_id,
        size.saturating_add(1),
        max_response_bytes,
    )?;
    events.sort_by_key(|event| event.event_id);
    let pruned_event_id_range = match known_pruned_range {
        Some(range) => range,
        None => pruned_event_id_range(view, history)?,
    };
    let page_len = events_within_limit(
        &events,
        size,
        max_response_bytes,
        pruned_event_id_range.as_ref(),
    )
    .map_err(
        |(event_id, size_bytes)| ReadModelError::HistoryEventTooLarge {
            event_id,
            size_bytes,
            max_response_bytes,
        },
    )?;
    let has_more = events.len() > page_len;
    events.truncate(page_len);
    let next_internal_event_id = events
        .last()
        .map_or(first_event_id, |event| event.event_id.saturating_add(1));
    let next_page_token = if has_more {
        events
            .last()
            .map_or_else(Vec::new, |event| event.event_id.to_be_bytes().to_vec())
    } else {
        Vec::new()
    };
    Ok(GetHistoryEventsResponse {
        events,
        next_page_token,
        next_internal_event_id,
        pruned_event_id_range,
    })
}

/// Returns how many of the first `page_size` events fit in a response of at most
/// `max_response_bytes`, counting the page token, next event ID, and pruned range.
///
/// Returns the ID of the first event and the size of a response holding only it when that
/// response does not fit.
fn events_within_limit(
    events: &[FlowHistoryEvent],
    page_size: usize,
    max_response_bytes: usize,
    pruned_event_id_range: Option<&HistoryEventIdRange>,
) -> Result<usize, (i64, usize)> {
    let mut event_bytes = 0_usize;
    for (index, event) in events.iter().take(page_size).enumerate() {
        event_bytes = event_bytes.saturating_add(encoded_event_bytes(event));
        let has_more = index + 1 < events.len();
        let response_bytes = event_bytes.saturating_add(non_event_response_bytes(
            event,
            has_more,
            pruned_event_id_range,
        ));
        if response_bytes > max_response_bytes {
            return if index == 0 {
                Err((event.event_id, response_bytes))
            } else {
                Ok(index)
            };
        }
    }
    Ok(events.len().min(page_size))
}

/// Encoded size of the response fields other than `events` for a page ending at `last_event`.
fn non_event_response_bytes(
    last_event: &FlowHistoryEvent,
    has_more: bool,
    pruned_event_id_range: Option<&HistoryEventIdRange>,
) -> usize {
    GetHistoryEventsResponse {
        events: Vec::new(),
        next_page_token: if has_more {
            last_event.event_id.to_be_bytes().to_vec()
        } else {
            Vec::new()
        },
        next_internal_event_id: last_event.event_id.saturating_add(1),
        pruned_event_id_range: pruned_event_id_range.copied(),
    }
    .encoded_len()
}

/// Bytes the event adds to an encoded response, including its field tag and length prefix.
fn encoded_event_bytes(event: &FlowHistoryEvent) -> usize {
    prost::encoding::message::encoded_len(EVENTS_FIELD_NUMBER, event)
}

/// Returns each attempt of `selected_attempt`'s lineage with the last seq it contributes.
pub(super) fn lineage_cutoffs(
    view: &RunView<'_>,
    selected_attempt: u32,
) -> Result<BTreeMap<u32, u64>, ReadModelError> {
    let mut lineage = BTreeMap::new();
    let mut attempt = selected_attempt;
    let mut cutoff = u64::MAX;
    while attempt != 0 {
        if lineage.insert(attempt, cutoff).is_some() {
            return Err(ReadModelError::State(
                crate::ApplyError::InvariantViolation {
                    detail: "run attempt lineage contains a cycle".to_owned(),
                },
            ));
        }
        let Some(record) = view.rows.attempt_record(&view.transaction, attempt)? else {
            return Err(ReadModelError::InvalidRequest {
                detail: format!("run attempt {attempt} does not exist"),
            });
        };
        cutoff = record.parent_seq;
        attempt = record.parent_attempt;
    }
    Ok(lineage)
}

/// Bounded seq-ordered reads of a run's HistoryRecord rows.
/// Event IDs increase with seq across attempts.
struct HistoryRecordRows<'view, 'reader> {
    view: &'view RunView<'reader>,
    history: &'view dyn HistorySource,
}

/// The first record with events in a scanned seq range.
struct FirstEventRecord {
    seq: u64,
    first_event_id: i64,
    last_event_id: i64,
}

/// Where a page scan starts, and the seq ranges the seek found without events.
struct HistorySeek {
    first_record_seq: u64,
    eventless_ranges: EventlessRanges,
}

/// The seqs after genesis and before the window start W, which reads hide.
///
/// Rows there may remain to rebuild W, but no point there is time-travelable.
#[derive(Clone, Copy)]
struct PrunedSeqs {
    start: u64,
    end: u64,
}

impl PrunedSeqs {
    fn of_run(view: &RunView<'_>) -> Result<Self, ReadModelError> {
        let genesis_seq = genesis_seq(&view.transaction, view.rows)?;
        Ok(Self {
            start: genesis_seq.saturating_add(1),
            end: view.record.window_start_seq,
        })
    }

    /// Returns `seq`, or W when `seq` is hidden.
    fn skip_from(self, seq: u64) -> u64 {
        if (self.start..self.end).contains(&seq) {
            self.end
        } else {
            seq
        }
    }

    /// Returns the end of a scan from visible `seq` that stops before the hidden seqs.
    fn scan_end(self, seq: u64, end: u64) -> u64 {
        if seq < self.start && self.start < self.end {
            end.min(self.start)
        } else {
            end
        }
    }
}

/// Disjoint seq ranges known to hold no events, keyed by start with exclusive ends.
#[derive(Default)]
struct EventlessRanges {
    ends_by_start: BTreeMap<u64, u64>,
}

impl EventlessRanges {
    fn insert(&mut self, range: Range<u64>) {
        if range.start < range.end {
            self.ends_by_start.insert(range.start, range.end);
        }
    }

    /// Returns the first seq at or after `seq` outside every known eventless range.
    fn skip_from(&self, mut seq: u64) -> u64 {
        while let Some((_, end)) = self.ends_by_start.range(..=seq).next_back() {
            if *end <= seq {
                break;
            }
            seq = *end;
        }
        seq
    }

    /// Returns the start of the first known eventless range after `seq`, if any.
    fn next_start_after(&self, seq: u64) -> Option<u64> {
        self.ends_by_start
            .range(seq.saturating_add(1)..)
            .next()
            .map(|(start, _)| *start)
    }
}

impl<'view, 'reader> HistoryRecordRows<'view, 'reader> {
    fn new(view: &'view RunView<'reader>, history: &'view dyn HistorySource) -> Self {
        Self { view, history }
    }

    /// Returns lineage events at or after `first_event_id` in event ID order.
    ///
    /// Returns every such event when fewer than `event_limit` exist, and otherwise at least
    /// `event_limit` of the smallest ones. Stops earlier once the events exceed `byte_budget`;
    /// the result then ends with at least one event past the budget.
    fn collect_lineage_events(
        &self,
        lineage: &BTreeMap<u32, u64>,
        first_event_id: i64,
        event_limit: usize,
        byte_budget: usize,
    ) -> Result<Vec<FlowHistoryEvent>, ReadModelError> {
        if first_event_id >= self.view.record.next_event_id {
            return Ok(Vec::new());
        }
        let Some(seek) = self.seek_first_record(first_event_id)? else {
            return Ok(Vec::new());
        };
        let pruned_seqs = PrunedSeqs::of_run(self.view)?;
        let mut events = Vec::new();
        let mut event_bytes = 0_usize;
        let mut last_event_id = 0_i64;
        for segment in self.lineage_segments(lineage)? {
            let mut next_seq = segment.start.max(seek.first_record_seq);
            let mut chunk_rows = event_limit.clamp(1, MAX_HISTORY_SCAN_ROWS);
            while events.len() < event_limit && event_bytes <= byte_budget {
                next_seq = pruned_seqs.skip_from(seek.eventless_ranges.skip_from(next_seq));
                if next_seq >= segment.end {
                    break;
                }
                let scan_end = pruned_seqs.scan_end(
                    next_seq,
                    seek.eventless_ranges
                        .next_start_after(next_seq)
                        .map_or(segment.end, |start| start.min(segment.end)),
                );
                let records = self.scan_history_records(next_seq, scan_end, chunk_rows)?;
                let Some((last_seq, _)) = records.last() else {
                    next_seq = scan_end;
                    continue;
                };
                next_seq = last_seq.saturating_add(1);
                let collected_before = events.len();
                for (seq, record) in records {
                    if !lineage
                        .get(&record.attempt)
                        .is_some_and(|cutoff| record.seq <= *cutoff)
                    {
                        continue;
                    }
                    for mut event in record.events {
                        if event.event_id <= last_event_id {
                            return Err(ReadModelError::State(ApplyError::InvariantViolation {
                                detail: format!(
                                    "history seq {seq} has event ID {} after {last_event_id}",
                                    event.event_id
                                ),
                            }));
                        }
                        last_event_id = event.event_id;
                        if event.event_id >= first_event_id {
                            if event.run_attempt == 0 {
                                event.run_attempt =
                                    i32::try_from(record.attempt).unwrap_or(i32::MAX);
                            }
                            event_bytes = event_bytes.saturating_add(encoded_event_bytes(&event));
                            events.push(event);
                        }
                    }
                    if events.len() >= event_limit || event_bytes > byte_budget {
                        break;
                    }
                }
                let remaining =
                    event_limit
                        .saturating_sub(events.len())
                        .min(events_left_in_budget(
                            events.len(),
                            event_bytes,
                            byte_budget,
                        ));
                chunk_rows = if events.len() == collected_before {
                    chunk_rows.saturating_mul(2).min(MAX_HISTORY_SCAN_ROWS)
                } else {
                    remaining.clamp(1, MAX_HISTORY_SCAN_ROWS)
                };
            }
            if events.len() >= event_limit || event_bytes > byte_budget {
                break;
            }
        }
        Ok(events)
    }

    /// Returns the first record holding an event at or after `first_event_id`, with the
    /// eventless seq ranges found on the way, or `None` when no record holds such an event.
    fn seek_first_record(
        &self,
        first_event_id: i64,
    ) -> Result<Option<HistorySeek>, ReadModelError> {
        let mut eventless_ranges = EventlessRanges::default();
        let mut chunk_rows = 1;
        let mut high = self.view.record.next_run_seq;
        let mut low = self
            .history
            .event_search_start_seq(first_event_id)?
            .unwrap_or(0)
            .min(high);
        let mut first_record_seq = None;
        // Probing the oldest record first answers a read from the start without a search.
        let mut probe = low;
        while low < high {
            let found = self.find_first_event_record(probe, high, &mut chunk_rows)?;
            match found {
                Some(record) => {
                    eventless_ranges.insert(probe..record.seq);
                    if record.last_event_id < first_event_id {
                        low = record.seq.saturating_add(1);
                    } else {
                        first_record_seq = Some(record.seq);
                        high = probe;
                    }
                }
                None => {
                    eventless_ranges.insert(probe..high);
                    high = probe;
                }
            }
            probe = low + (high.saturating_sub(low)) / 2;
        }
        Ok(first_record_seq.map(|first_record_seq| HistorySeek {
            first_record_seq,
            eventless_ranges,
        }))
    }

    /// Returns the first record with events in `[start, end)`, or `None` when it holds none.
    /// `chunk_rows` carries the scan size across calls.
    fn find_first_event_record(
        &self,
        start: u64,
        end: u64,
        chunk_rows: &mut usize,
    ) -> Result<Option<FirstEventRecord>, ReadModelError> {
        let mut next_seq = start;
        let mut is_first_chunk = true;
        while next_seq < end {
            let records = self.scan_history_records(next_seq, end, *chunk_rows)?;
            let Some((last_seq, _)) = records.last() else {
                return Ok(None);
            };
            next_seq = last_seq.saturating_add(1);
            for (seq, record) in records {
                if let (Some(first), Some(last)) = (record.events.first(), record.events.last()) {
                    if is_first_chunk {
                        *chunk_rows = 1;
                    }
                    return Ok(Some(FirstEventRecord {
                        seq,
                        first_event_id: first.event_id,
                        last_event_id: last.event_id,
                    }));
                }
            }
            is_first_chunk = false;
            *chunk_rows = chunk_rows.saturating_mul(2).min(MAX_HISTORY_SCAN_ROWS);
        }
        Ok(None)
    }

    /// Returns each lineage attempt's seq range in seq order.
    ///
    /// An attempt's range ends where the next attempt starts.
    fn lineage_segments(
        &self,
        lineage: &BTreeMap<u32, u64>,
    ) -> Result<Vec<Range<u64>>, ReadModelError> {
        let view = self.view;
        let mut segments = Vec::with_capacity(lineage.len());
        for (attempt, cutoff) in lineage {
            let Some(record) = view.rows.attempt_record(&view.transaction, *attempt)? else {
                return Err(ReadModelError::State(ApplyError::InvariantViolation {
                    detail: format!("run attempt {attempt} of the lineage has no attempt record"),
                }));
            };
            let mut end = cutoff.saturating_add(1);
            if let Some(next_attempt) = attempt.checked_add(1)
                && let Some(next) = view.rows.attempt_record(&view.transaction, next_attempt)?
            {
                end = end.min(next.start_seq);
            }
            segments.push(record.start_seq..end);
        }
        segments.sort_by_key(|segment| segment.start);
        Ok(segments)
    }

    /// Returns up to `limit` records with seqs in `[start, end)`, with the seq from each key.
    fn scan_history_records(
        &self,
        start: u64,
        end: u64,
        limit: usize,
    ) -> Result<Vec<(u64, HistoryRecord)>, ReadModelError> {
        if start >= end {
            return Ok(Vec::new());
        }
        // u64::MAX stands for the end of the table, so it stays in the scanned range.
        let last = if end == u64::MAX { end } else { end - 1 };
        self.history
            .history_records(start..=last, limit)
            .take(limit)
            .map(|record| record.map(|record| (record.seq, record)))
            .collect::<Result<Vec<_>, ApplyError>>()
            .map_err(ReadModelError::State)
    }
}

/// Estimates how many more events fit in `byte_budget` from the average event size so far, plus
/// one that shows whether the page ends there.
fn events_left_in_budget(event_count: usize, event_bytes: usize, byte_budget: usize) -> usize {
    if event_count == 0 {
        return usize::MAX;
    }
    let average_event_bytes = (event_bytes / event_count).max(1);
    (byte_budget.saturating_sub(event_bytes) / average_event_bytes).saturating_add(1)
}

/// Returns the event IDs before the window start W after FlowStart, or `None` when there are none.
///
/// Runs whose window advanced before `max_pruned_event_id` existed scan for W's first event.
fn pruned_event_id_range(
    view: &RunView<'_>,
    history: &dyn HistorySource,
) -> Result<Option<HistoryEventIdRange>, ReadModelError> {
    let genesis_seq = genesis_seq(&view.transaction, view.rows)?;
    // Pruning deletes rows only below the window start, so an unmoved window deleted none.
    if view.record.window_start_seq <= genesis_seq.saturating_add(1) {
        return Ok(None);
    }
    let end_event_id = if view.record.max_pruned_event_id > 0 {
        view.record.max_pruned_event_id
    } else {
        let history_rows = HistoryRecordRows::new(view, history);
        let mut chunk_rows = 1;
        match history_rows.find_first_event_record(
            view.record.window_start_seq,
            view.record.next_run_seq,
            &mut chunk_rows,
        )? {
            Some(record) => record.first_event_id.saturating_sub(1),
            None => view.record.next_event_id.saturating_sub(1),
        }
    };
    Ok(
        (end_event_id >= FIRST_PRUNABLE_EVENT_ID).then_some(HistoryEventIdRange {
            start_event_id: FIRST_PRUNABLE_EVENT_ID,
            end_event_id,
        }),
    )
}
