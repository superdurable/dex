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
use dexzd_protocol::dex::{FlowHistoryEvent, GetHistoryEventsResponse};
use dexzd_protocol::dexzd::v1::HistoryRecord;

use crate::read_model::{ReadModelError, RunView};
use crate::{ApplyError, FlowStateReader};

/// Most History rows read by one scan call.
const MAX_HISTORY_SCAN_ROWS: usize = 256;

/// Most History rows one seek probe reads before it reports eventless rows.
const SEEK_PROBE_ROWS: usize = 32;

/// What one seek probe found from its start seq.
enum SeekProbe {
    /// The first record with events, and its first event ID.
    Event { seq: u64, event_id: i64 },
    /// Every probed row through `last_seq` has no events.
    Eventless { last_seq: u64 },
    /// No record with events exists before the probe's end.
    End,
}

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
    Ok(GetHistoryEventsResponse {
        events,
        next_page_token,
        next_internal_event_id,
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
        let start_seq = self.seek_seq(first_event_id)?;
        let mut events = Vec::new();
        let mut last_event_id = 0_i64;
        for segment in self.lineage_segments(lineage)? {
            let mut next_seq = segment.start.max(start_seq);
            let mut chunk_rows = event_limit.clamp(1, MAX_HISTORY_SCAN_ROWS);
            while next_seq < segment.end && events.len() < event_limit {
                let records = self.scan(next_seq, segment.end, chunk_rows)?;
                let Some((last_seq, _)) = records.last() else {
                    break;
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

    /// Returns the seq of the last record whose first event ID is below `first_event_id`, or 0.
    ///
    /// Earlier records hold only smaller event IDs, so a scan may start at the returned seq.
    fn seek_seq(&self, first_event_id: i64) -> Result<u64, ReadModelError> {
        let end = self.view.record.next_run_seq;
        // Reading from the oldest retained event skips the search that would only move left.
        match self.probe(0, end)? {
            SeekProbe::Event { event_id, .. } if event_id >= first_event_id => return Ok(0),
            SeekProbe::End => return Ok(0),
            SeekProbe::Event { .. } | SeekProbe::Eventless { .. } => {}
        }
        Ok(self.last_record_below(first_event_id, 0, end)?.unwrap_or(0))
    }

    /// Returns the seq of the last record in `[low, high)` whose first event ID is below
    /// `first_event_id`, or `None` when no record there qualifies.
    fn last_record_below(
        &self,
        first_event_id: i64,
        mut low: u64,
        mut high: u64,
    ) -> Result<Option<u64>, ReadModelError> {
        let mut found = None;
        while low < high {
            let probe = low + (high - low) / 2;
            match self.probe(probe, high)? {
                SeekProbe::Event { seq, event_id } if event_id < first_event_id => {
                    found = Some(seq);
                    low = seq.saturating_add(1);
                }
                SeekProbe::Event { .. } | SeekProbe::End => high = probe,
                // Searches past the eventless rows first, because a match there is the later one.
                SeekProbe::Eventless { last_seq } => {
                    let later =
                        self.last_record_below(first_event_id, last_seq.saturating_add(1), high)?;
                    if later.is_some() {
                        return Ok(later);
                    }
                    high = probe;
                }
            }
        }
        Ok(found)
    }

    /// Reads at most [`SEEK_PROBE_ROWS`] rows from `start` and reports the first event found.
    fn probe(&self, start: u64, end: u64) -> Result<SeekProbe, ReadModelError> {
        let mut next_seq = start;
        let mut chunk_rows = 1;
        let mut probed_rows = 0;
        while next_seq < end && probed_rows < SEEK_PROBE_ROWS {
            let records = self.scan(next_seq, end, chunk_rows)?;
            let Some((last_seq, _)) = records.last() else {
                break;
            };
            next_seq = last_seq.saturating_add(1);
            probed_rows += records.len();
            for (seq, record) in records {
                if let Some(event) = record.events.first() {
                    return Ok(SeekProbe::Event {
                        seq,
                        event_id: event.event_id,
                    });
                }
            }
            chunk_rows = chunk_rows
                .saturating_mul(2)
                .min(SEEK_PROBE_ROWS - probed_rows.min(SEEK_PROBE_ROWS))
                .max(1);
        }
        if next_seq >= end || probed_rows < SEEK_PROBE_ROWS {
            return Ok(SeekProbe::End);
        }
        Ok(SeekProbe::Eventless {
            last_seq: next_seq - 1,
        })
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
    fn scan(
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
