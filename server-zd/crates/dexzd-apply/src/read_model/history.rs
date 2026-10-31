// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::ops::Range;

use dexzd_keyspace::row_keys::{history_record_key, prefix_range};
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dex::{FlowHistoryEvent, GetHistoryEventsResponse, HistoryEventIdRange};
use dexzd_protocol::dexzd::v1::HistoryRecord;

use crate::history::genesis_seq;
use crate::read_model::{ReadModelError, RunView};
use crate::{ApplyError, FlowStateReader};

/// Most History rows read by one scan call.
const MAX_HISTORY_SCAN_ROWS: usize = 256;

/// FlowStart records the first event of every run, so window pruning removes IDs from 2.
const FIRST_PRUNABLE_EVENT_ID: i64 = 2;

/// Reads one page of events from an attempt's lineage, using event IDs as cursors.
pub fn get_history_events(
    reader: &dyn FlowStateReader,
    flow_hash: FlowHash,
    run_id: RunId,
    start_event_id: i64,
    page_size: i32,
    next_page_token: &[u8],
    run_attempt: i32,
) -> Result<GetHistoryEventsResponse, ReadModelError> {
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
    let view = RunView::open(reader, flow_hash, run_id)?;
    let selected_attempt = if run_attempt == 0 {
        view.record.current_attempt
    } else {
        u32::try_from(run_attempt).unwrap_or(u32::MAX)
    };
    let lineage = lineage_cutoffs(&view, selected_attempt)?;
    let size = if page_size == 0 {
        100_usize
    } else {
        usize::try_from(page_size).unwrap_or(usize::MAX)
    };
    let history = HistoryRecordRows::new(&view);
    let mut events =
        history.collect_lineage_events(&lineage, first_event_id, size.saturating_add(1))?;
    events.sort_by_key(|event| event.event_id);
    let has_more = events.len() > size;
    events.truncate(size);
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
    let pruned_event_id_range = pruned_event_id_range(&view)?;
    Ok(GetHistoryEventsResponse {
        events,
        next_page_token,
        next_internal_event_id,
        pruned_event_id_range,
    })
}

fn lineage_cutoffs(
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
    table_prefix: Vec<u8>,
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
    fn new(view: &'view RunView<'reader>) -> Self {
        Self {
            view,
            table_prefix: run_table_prefix(
                view.rows.flow_hash,
                view.rows.run_id,
                RunTable::HistoryRecord,
            ),
        }
    }

    /// Returns lineage events at or after `first_event_id` in event ID order.
    ///
    /// Returns every such event when fewer than `event_limit` exist, and otherwise at least
    /// `event_limit` of the smallest ones.
    fn collect_lineage_events(
        &self,
        lineage: &BTreeMap<u32, u64>,
        first_event_id: i64,
        event_limit: usize,
    ) -> Result<Vec<FlowHistoryEvent>, ReadModelError> {
        if first_event_id >= self.view.record.next_event_id {
            return Ok(Vec::new());
        }
        let Some(seek) = self.seek_first_record(first_event_id)? else {
            return Ok(Vec::new());
        };
        let mut events = Vec::new();
        let mut last_event_id = 0_i64;
        for segment in self.lineage_segments(lineage)? {
            let mut next_seq = segment.start.max(seek.first_record_seq);
            let mut chunk_rows = event_limit.clamp(1, MAX_HISTORY_SCAN_ROWS);
            while events.len() < event_limit {
                next_seq = seek.eventless_ranges.skip_from(next_seq);
                if next_seq >= segment.end {
                    break;
                }
                let scan_end = seek
                    .eventless_ranges
                    .next_start_after(next_seq)
                    .map_or(segment.end, |start| start.min(segment.end));
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
                            events.push(event);
                        }
                    }
                    if events.len() >= event_limit {
                        break;
                    }
                }
                let remaining = event_limit.saturating_sub(events.len());
                chunk_rows = if events.len() == collected_before {
                    chunk_rows.saturating_mul(2).min(MAX_HISTORY_SCAN_ROWS)
                } else {
                    remaining.clamp(1, MAX_HISTORY_SCAN_ROWS)
                };
            }
            if events.len() >= event_limit {
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
        let mut low = 0;
        let mut high = self.view.record.next_run_seq;
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
        let view = self.view;
        let start_key = history_record_key(view.rows.flow_hash, view.rows.run_id, start);
        let end_key = if end == u64::MAX {
            prefix_range(&self.table_prefix).1
        } else {
            history_record_key(view.rows.flow_hash, view.rows.run_id, end)
        };
        let rows: Vec<(Vec<u8>, HistoryRecord)> = view.transaction.scan_messages(
            ColumnFamily::History,
            &start_key,
            &end_key,
            limit,
            "history record",
        )?;
        rows.into_iter()
            .map(|(key, record)| Ok((self.seq_of_key(&key)?, record)))
            .collect()
    }

    fn seq_of_key(&self, key: &[u8]) -> Result<u64, ReadModelError> {
        key.strip_prefix(self.table_prefix.as_slice())
            .and_then(|suffix| <[u8; 8]>::try_from(suffix).ok())
            .map(u64::from_be_bytes)
            .ok_or_else(|| {
                ReadModelError::State(ApplyError::InvariantViolation {
                    detail: format!("history record key {key:02x?} has no seq suffix"),
                })
            })
    }
}

/// Returns the event IDs that window pruning removed, or `None` when it removed none.
///
/// Runs whose window advanced before `max_pruned_event_id` existed scan for their first kept event.
fn pruned_event_id_range(
    view: &RunView<'_>,
) -> Result<Option<HistoryEventIdRange>, ReadModelError> {
    let genesis_seq = genesis_seq(&view.transaction, view.rows)?;
    // Pruning deletes rows only below the window start, so an unmoved window deleted none.
    if view.record.window_start_seq <= genesis_seq.saturating_add(1) {
        return Ok(None);
    }
    let end_event_id = if view.record.max_pruned_event_id > 0 {
        view.record.max_pruned_event_id
    } else {
        let history = HistoryRecordRows::new(view);
        let mut chunk_rows = 1;
        match history.find_first_event_record(
            genesis_seq.saturating_add(1),
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
