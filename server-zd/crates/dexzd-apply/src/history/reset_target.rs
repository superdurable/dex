// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Maps a ResetFlow request to a semantic point of the selected lineage (plan section 8.4).

use std::collections::VecDeque;

use dexzd_keyspace::row_keys::{history_row_metadata_key, prefix_range};
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dex::{
    ErrorSubStatus, FlowResetStepMethod, FlowResetType, ResetFlowRequest, flow_history_event,
};
use dexzd_protocol::dexzd::v1::{
    CommandReply, HistoryRecord, HistoryRowMetadata, HistoryStepMethodPending, RunRecord,
    StepMethod,
};

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::history::lineage::{HISTORY_PAGE_RECORDS, HistorySource, Lineage, genesis_seq};
use crate::history::state_rebuild::HistoryPoint;
use crate::history::window::metadata_identity;
use crate::state_access::{FlowTransaction, RunRows};

/// The point a time travel returns to.
#[derive(Debug, Clone)]
pub(crate) struct TravelTarget {
    pub(crate) point: HistoryPoint,
    /// Largest history event ID at the point, for the fork event.
    pub(crate) last_event_id: i64,
    /// FlowClosed message from retained history for records written before it was persisted.
    pub(crate) close_history_message: Option<String>,
}

/// Where target resolution reads its candidate points.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum CandidateSource {
    /// The local metadata rows of the History records.
    Metadata,
    /// The History records of the history source.
    Records,
}

/// Resolves the target point, or returns the reply that rejects the request.
///
/// Reads metadata, or `history` records when the run's metadata predates the target fields.
pub(crate) fn resolve_target(
    transaction: &FlowTransaction<'_>,
    history: &dyn HistorySource,
    rows: RunRows,
    record: &RunRecord,
    now_micros: i64,
    request: &ResetFlowRequest,
    lineage: &Lineage,
) -> Result<Result<TravelTarget, CommandReply>, ApplyError> {
    let candidates = TargetCandidates {
        transaction,
        history,
        rows,
        lineage,
        genesis: genesis_seq(transaction, rows)?,
        window_start: record.window_start_seq,
    };
    let source = if has_target_metadata(transaction, rows, candidates.genesis)? {
        CandidateSource::Metadata
    } else {
        CandidateSource::Records
    };
    resolve_target_from(&candidates, record, now_micros, request, source)
}

/// Resolves the target point from the candidates `source` reads.
fn resolve_target_from(
    candidates: &TargetCandidates<'_, '_>,
    record: &RunRecord,
    now_micros: i64,
    request: &ResetFlowRequest,
    source: CandidateSource,
) -> Result<Result<TravelTarget, CommandReply>, ApplyError> {
    let reset_type =
        FlowResetType::try_from(request.reset_type).unwrap_or(FlowResetType::Unspecified);
    let matcher = match TargetMatcher::of_request(
        record,
        now_micros,
        request,
        reset_type,
        candidates.genesis,
    ) {
        Ok(matcher) => matcher,
        Err(reply) => return Ok(Err(reply)),
    };
    // Only BEGINNING may target the preserved genesis point outside the window (D12).
    let is_genesis_included =
        reset_type == FlowResetType::Beginning && candidates.genesis < candidates.window_start;
    let selection = match matcher {
        Some(matcher) => {
            select_first_match(candidates.stream(source, is_genesis_included), &matcher)?
        }
        None => None,
    };
    let Some(target) = selection else {
        return Ok(Err(error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "no {} point exists in the time travel window (from run seq {}) of the selected \
                 run attempt",
                describe_reset_type(reset_type),
                candidates.window_start
            ),
        )));
    };
    Ok(Ok(target))
}

/// Whether metadata has the target fields: FlowStart always records event 1, so genesis shows it.
fn has_target_metadata(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    genesis: u64,
) -> Result<bool, ApplyError> {
    let genesis_metadata: Option<HistoryRowMetadata> = transaction.get_message(
        ColumnFamily::Flow,
        &history_row_metadata_key(
            rows.flow_hash,
            rows.run_id,
            genesis,
            RunTable::HistoryRecord,
            0,
        ),
        "history metadata",
    )?;
    Ok(genesis_metadata.is_some_and(|metadata| metadata.max_event_id > 0))
}

/// A point a target may select: a History record, read from the record or its metadata.
struct TargetCandidate {
    attempt: u32,
    seq: u64,
    timestamp_micros: i64,
    pending_step_methods: Vec<HistoryStepMethodPending>,
    /// Largest event ID of the record, zero without events.
    max_event_id: i64,
    flow_closed_message: Option<String>,
}

impl TargetCandidate {
    fn from_record(record: HistoryRecord) -> Self {
        Self {
            attempt: record.attempt,
            seq: record.seq,
            timestamp_micros: record.timestamp_micros,
            max_event_id: record
                .events
                .iter()
                .map(|event| event.event_id)
                .max()
                .unwrap_or(0),
            flow_closed_message: record
                .events
                .iter()
                .filter_map(|event| match &event.payload {
                    Some(flow_history_event::Payload::FlowClosed(closed)) => {
                        Some(closed.error_message.clone())
                    }
                    _ => None,
                })
                .next_back(),
            pending_step_methods: record.pending_step_methods,
        }
    }

    fn from_metadata(seq: u64, metadata: HistoryRowMetadata) -> Self {
        Self {
            attempt: metadata.attempt,
            seq,
            timestamp_micros: metadata.timestamp_micros,
            pending_step_methods: metadata.pending_step_methods,
            max_event_id: metadata.max_event_id,
            flow_closed_message: metadata.flow_closed_message,
        }
    }
}

/// Candidates of target points in seq order.
type TargetCandidateStream<'stream> =
    Box<dyn Iterator<Item = Result<TargetCandidate, ApplyError>> + 'stream>;

/// The reads that list a run's target candidates.
struct TargetCandidates<'transaction, 'reader> {
    transaction: &'transaction FlowTransaction<'reader>,
    history: &'transaction dyn HistorySource,
    rows: RunRows,
    lineage: &'transaction Lineage,
    genesis: u64,
    window_start: u64,
}

impl TargetCandidates<'_, '_> {
    /// Streams the genesis point when included, then the window's semantic lineage points.
    fn stream(
        &self,
        source: CandidateSource,
        is_genesis_included: bool,
    ) -> TargetCandidateStream<'_> {
        match source {
            CandidateSource::Metadata => self.metadata_stream(is_genesis_included),
            CandidateSource::Records => self.record_stream(is_genesis_included),
        }
    }

    fn record_stream(&self, is_genesis_included: bool) -> TargetCandidateStream<'_> {
        let genesis_records = if is_genesis_included {
            self.history.history_records(self.genesis..=self.genesis, 1)
        } else {
            Box::new(std::iter::empty())
        };
        let window_records = self
            .history
            .history_records(self.window_start..=u64::MAX, HISTORY_PAGE_RECORDS)
            .filter(|record| {
                record.as_ref().map_or(true, |record| {
                    record.is_semantic && self.lineage.contains(record.attempt, record.seq)
                })
            });
        Box::new(
            genesis_records
                .chain(window_records)
                .map(|record| record.map(TargetCandidate::from_record)),
        )
    }

    fn metadata_stream(&self, is_genesis_included: bool) -> TargetCandidateStream<'_> {
        let rows = self.rows;
        let genesis_metadata = if is_genesis_included {
            self.transaction
                .get_message::<HistoryRowMetadata>(
                    ColumnFamily::Flow,
                    &history_row_metadata_key(
                        rows.flow_hash,
                        rows.run_id,
                        self.genesis,
                        RunTable::HistoryRecord,
                        0,
                    ),
                    "history metadata",
                )
                .transpose()
                .map(|metadata| {
                    metadata.map(|metadata| TargetCandidate::from_metadata(self.genesis, metadata))
                })
        } else {
            None
        };
        let window_metadata = WindowMetadataCandidates {
            candidates: self,
            prefix: run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistoryRowMetadata),
            next_key: Some(history_row_metadata_key(
                rows.flow_hash,
                rows.run_id,
                self.window_start,
                RunTable::HistoryRecord,
                0,
            )),
            page: VecDeque::new(),
        };
        Box::new(genesis_metadata.into_iter().chain(window_metadata))
    }
}

/// Metadata rows read per scan while listing target candidates.
const TARGET_METADATA_SCAN_ROWS: usize = 256;

/// Pages through the metadata from the window start, yielding semantic lineage records.
struct WindowMetadataCandidates<'candidates, 'transaction, 'reader> {
    candidates: &'candidates TargetCandidates<'transaction, 'reader>,
    prefix: Vec<u8>,
    /// First key of the next page, or `None` when no rows remain.
    next_key: Option<Vec<u8>>,
    page: VecDeque<(Vec<u8>, HistoryRowMetadata)>,
}

impl Iterator for WindowMetadataCandidates<'_, '_, '_> {
    type Item = Result<TargetCandidate, ApplyError>;

    fn next(&mut self) -> Option<Self::Item> {
        loop {
            while let Some((key, metadata)) = self.page.pop_front() {
                let (seq, history_table, _) = match metadata_identity(&key, &self.prefix) {
                    Ok(identity) => identity,
                    Err(error) => return Some(self.fail(error)),
                };
                if history_table == RunTable::HistoryRecord
                    && metadata.is_semantic
                    && self.candidates.lineage.contains(metadata.attempt, seq)
                {
                    return Some(Ok(TargetCandidate::from_metadata(seq, metadata)));
                }
            }
            let start = self.next_key.take()?;
            if let Err(error) = self.read_page(&start) {
                return Some(self.fail(error));
            }
        }
    }
}

impl WindowMetadataCandidates<'_, '_, '_> {
    fn read_page(&mut self, start: &[u8]) -> Result<(), ApplyError> {
        let (_, end) = prefix_range(&self.prefix);
        let page = self
            .candidates
            .transaction
            .scan_messages::<HistoryRowMetadata>(
                ColumnFamily::Flow,
                start,
                &end,
                TARGET_METADATA_SCAN_ROWS,
                "history metadata",
            )?;
        if page.len() == TARGET_METADATA_SCAN_ROWS
            && let Some((last_key, _)) = page.last()
        {
            let mut next_key = last_key.clone();
            next_key.push(0);
            self.next_key = Some(next_key);
        }
        self.page.extend(page);
        Ok(())
    }

    /// Ends the stream after an error.
    fn fail(&mut self, error: ApplyError) -> Result<TargetCandidate, ApplyError> {
        self.next_key = None;
        self.page.clear();
        Err(error)
    }
}

/// The candidate a request selects: the first one that matches.
enum TargetMatcher<'request> {
    Seq(u64),
    AtOrAfterNanos(i128),
    PendingStepType(&'request str),
    PendingStepMethod {
        step_execution_id: &'request str,
        method: FlowResetStepMethod,
    },
}

impl<'request> TargetMatcher<'request> {
    /// Returns the matcher, `None` when no retained point can match, or the rejection reply.
    fn of_request(
        record: &RunRecord,
        now_micros: i64,
        request: &'request ResetFlowRequest,
        reset_type: FlowResetType,
        genesis: u64,
    ) -> Result<Option<Self>, CommandReply> {
        match reset_type {
            FlowResetType::Beginning => Ok(Some(Self::Seq(genesis))),
            FlowResetType::HistoryEventTime => {
                let target_nanos = parse_reset_time(&request.history_event_time, now_micros)
                    .map_err(invalid_argument)?;
                Ok((!is_before_window(record, target_nanos))
                    .then_some(Self::AtOrAfterNanos(target_nanos)))
            }
            FlowResetType::StepType => Ok(Some(Self::PendingStepType(&request.step_type))),
            FlowResetType::StepExecutionId => {
                let method = FlowResetStepMethod::try_from(request.step_method)
                    .unwrap_or(FlowResetStepMethod::Unspecified);
                if method == FlowResetStepMethod::Unspecified {
                    return Err(invalid_argument(
                        "step method must be WaitFor or Execute".to_owned(),
                    ));
                }
                Ok(Some(Self::PendingStepMethod {
                    step_execution_id: &request.step_execution_id,
                    method,
                }))
            }
            FlowResetType::Unspecified => {
                Err(invalid_argument("time travel type is required".to_owned()))
            }
        }
    }

    fn matches(&self, candidate: &TargetCandidate) -> bool {
        match self {
            Self::Seq(seq) => candidate.seq == *seq,
            Self::AtOrAfterNanos(target_nanos) => {
                i128::from(candidate.timestamp_micros) * 1000 >= *target_nanos
            }
            Self::PendingStepType(step_type) => candidate
                .pending_step_methods
                .iter()
                .any(|pending| pending.step_type == *step_type),
            Self::PendingStepMethod {
                step_execution_id,
                method,
            } => candidate.pending_step_methods.iter().any(|pending| {
                pending.step_execution_id == *step_execution_id
                    && match method {
                        FlowResetStepMethod::WaitFor => pending.is_step_created,
                        _ => pending.method == StepMethod::Execute as i32,
                    }
            }),
        }
    }
}

/// Returns the first matching candidate as a target, with the event ID and FlowClosed message
/// of the candidates up to it.
fn select_first_match(
    candidates: TargetCandidateStream<'_>,
    matcher: &TargetMatcher<'_>,
) -> Result<Option<TravelTarget>, ApplyError> {
    let mut last_event_id = 0;
    let mut close_history_message = None;
    for candidate in candidates {
        let candidate = candidate?;
        last_event_id = last_event_id.max(candidate.max_event_id);
        if matcher.matches(&candidate) {
            return Ok(Some(TravelTarget {
                point: HistoryPoint {
                    attempt: candidate.attempt,
                    seq: candidate.seq,
                },
                last_event_id,
                close_history_message: candidate.flow_closed_message.or(close_history_message),
            }));
        }
        if candidate.flow_closed_message.is_some() {
            close_history_message = candidate.flow_closed_message;
        }
    }
    Ok(None)
}

/// Resolves the target from the candidates `source` reads, whatever the run's metadata holds.
#[cfg(feature = "test-util")]
#[expect(
    clippy::too_many_arguments,
    reason = "mirrors resolve_target plus the forced candidate source"
)]
pub(crate) fn resolve_target_from_source_for_test(
    transaction: &FlowTransaction<'_>,
    history: &dyn HistorySource,
    rows: RunRows,
    record: &RunRecord,
    now_micros: i64,
    request: &ResetFlowRequest,
    lineage: &Lineage,
    source: CandidateSource,
) -> Result<Result<TravelTarget, CommandReply>, ApplyError> {
    let candidates = TargetCandidates {
        transaction,
        history,
        rows,
        lineage,
        genesis: genesis_seq(transaction, rows)?,
        window_start: record.window_start_seq,
    };
    resolve_target_from(&candidates, record, now_micros, request, source)
}

/// The resolution from before metadata-based selection: every window record, read eagerly.
#[cfg(feature = "test-util")]
pub(crate) fn resolve_target_from_records_for_test(
    transaction: &FlowTransaction<'_>,
    history: &dyn HistorySource,
    rows: RunRows,
    record: &RunRecord,
    now_micros: i64,
    request: &ResetFlowRequest,
    lineage: &Lineage,
) -> Result<Result<TravelTarget, CommandReply>, ApplyError> {
    let reset_type =
        FlowResetType::try_from(request.reset_type).unwrap_or(FlowResetType::Unspecified);
    let genesis = genesis_seq(transaction, rows)?;
    let window_start = record.window_start_seq;
    let mut candidates = Vec::new();
    // Only BEGINNING may target the preserved genesis point outside the window (D12).
    if reset_type == FlowResetType::Beginning
        && genesis < window_start
        && let Some(record) = rows.history_record(transaction, genesis)?
    {
        candidates.push(record);
    }
    candidates.extend(
        history
            .history_records(window_start..=u64::MAX, HISTORY_PAGE_RECORDS)
            .collect::<Result<Vec<_>, _>>()?
            .into_iter()
            .filter(|record| record.is_semantic && lineage.contains(record.attempt, record.seq)),
    );
    let selection = match reset_type {
        FlowResetType::Beginning => candidates.iter().find(|record| record.seq == genesis),
        FlowResetType::HistoryEventTime => {
            let target_nanos = match parse_reset_time(&request.history_event_time, now_micros) {
                Ok(nanos) => nanos,
                Err(detail) => return Ok(Err(invalid_argument(detail))),
            };
            if is_before_window(record, target_nanos) {
                None
            } else {
                candidates
                    .iter()
                    .find(|record| i128::from(record.timestamp_micros) * 1000 >= target_nanos)
            }
        }
        FlowResetType::StepType => candidates.iter().find(|record| {
            record
                .pending_step_methods
                .iter()
                .any(|pending| pending.step_type == request.step_type)
        }),
        FlowResetType::StepExecutionId => {
            let method = FlowResetStepMethod::try_from(request.step_method)
                .unwrap_or(FlowResetStepMethod::Unspecified);
            if method == FlowResetStepMethod::Unspecified {
                return Ok(Err(invalid_argument(
                    "step method must be WaitFor or Execute".to_owned(),
                )));
            }
            candidates.iter().find(|record| {
                record.pending_step_methods.iter().any(|pending| {
                    pending.step_execution_id == request.step_execution_id
                        && match method {
                            FlowResetStepMethod::WaitFor => pending.is_step_created,
                            _ => pending.method == StepMethod::Execute as i32,
                        }
                })
            })
        }
        FlowResetType::Unspecified => {
            return Ok(Err(invalid_argument(
                "time travel type is required".to_owned(),
            )));
        }
    };
    let Some(selected) = selection else {
        return Ok(Err(error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "no {} point exists in the time travel window (from run seq {window_start}) of \
                 the selected run attempt",
                describe_reset_type(reset_type)
            ),
        )));
    };
    Ok(Ok(TravelTarget {
        point: HistoryPoint {
            attempt: selected.attempt,
            seq: selected.seq,
        },
        last_event_id: last_event_id_at(&candidates, selected.seq),
        close_history_message: candidates
            .iter()
            .filter(|record| record.seq <= selected.seq)
            .flat_map(|record| &record.events)
            .filter_map(|event| match &event.payload {
                Some(flow_history_event::Payload::FlowClosed(closed)) => {
                    Some(closed.error_message.clone())
                }
                _ => None,
            })
            .next_back(),
    }))
}

/// Whether a pruned semantic point is at or after the time, so it would be the target.
fn is_before_window(record: &RunRecord, target_nanos: i128) -> bool {
    record.pruned_semantic_event_count > 0
        && target_nanos <= i128::from(record.last_pruned_semantic_timestamp_micros) * 1000
}

#[cfg(feature = "test-util")]
fn last_event_id_at(candidates: &[HistoryRecord], seq: u64) -> i64 {
    candidates
        .iter()
        .filter(|record| record.seq <= seq)
        .flat_map(|record| record.events.iter().map(|event| event.event_id))
        .max()
        .unwrap_or(0)
}

fn describe_reset_type(reset_type: FlowResetType) -> &'static str {
    match reset_type {
        FlowResetType::Beginning => "beginning",
        FlowResetType::HistoryEventTime => "history event time",
        FlowResetType::StepType => "Step type",
        FlowResetType::StepExecutionId => "Step execution",
        FlowResetType::Unspecified => "unspecified",
    }
}

fn invalid_argument(detail: String) -> CommandReply {
    error_reply(
        grpc_code::INVALID_ARGUMENT,
        ErrorSubStatus::Uncategorized,
        detail,
    )
}

/// Parses RFC 3339, Unix nanoseconds, or `3d` before `now_micros`, like the Go Server.
pub(crate) fn parse_reset_time(text: &str, now_micros: i64) -> Result<i128, String> {
    if let Some(nanos) = parse_rfc3339_nanos(text) {
        return Ok(nanos);
    }
    if let Ok(nanos) = text.parse::<i64>() {
        return Ok(i128::from(nanos));
    }
    parse_relative_time(text, now_micros).ok_or_else(|| {
        format!(
            "cannot parse time {text:?}; use RFC 3339 time, a time range such as 3d, or Unix \
             nanoseconds"
        )
    })
}

fn parse_relative_time(text: &str, now_micros: i64) -> Option<i128> {
    let digits_end = text
        .find(|character: char| !character.is_ascii_digit())
        .unwrap_or(text.len());
    let (digits, unit) = text.split_at(digits_end);
    if digits.is_empty() || digits.starts_with('0') {
        return None;
    }
    let multiplier: i128 = digits.parse().ok()?;
    if multiplier >= 1_000_000 {
        return None;
    }
    const SECOND: i128 = 1_000_000_000;
    let unit_nanos = match unit {
        "s" | "second" => SECOND,
        "m" | "minute" => 60 * SECOND,
        "h" | "hour" => 3600 * SECOND,
        "d" | "day" => 24 * 3600 * SECOND,
        "w" | "week" => 7 * 24 * 3600 * SECOND,
        "M" | "month" => 30 * 24 * 3600 * SECOND,
        "y" | "year" => 365 * 24 * 3600 * SECOND,
        _ => return None,
    };
    Some((i128::from(now_micros) * 1000 - multiplier * unit_nanos).max(0))
}

/// Parses `YYYY-MM-DDTHH:MM:SS[.fraction](Z|±HH:MM)`.
fn parse_rfc3339_nanos(text: &str) -> Option<i128> {
    let bytes = text.as_bytes();
    if bytes.len() < 20 || bytes[4] != b'-' || bytes[7] != b'-' || bytes[10] != b'T' {
        return None;
    }
    if bytes[13] != b':' || bytes[16] != b':' {
        return None;
    }
    let number = |range: std::ops::Range<usize>| -> Option<i64> {
        let part = text.get(range)?;
        part.bytes()
            .all(|byte| byte.is_ascii_digit())
            .then(|| part.parse().ok())?
    };
    let (year, month, day) = (number(0..4)?, number(5..7)?, number(8..10)?);
    let (hour, minute, second) = (number(11..13)?, number(14..16)?, number(17..19)?);
    let is_valid_clock = hour < 24 && minute < 60 && second < 60;
    if !(1..=12).contains(&month) || day < 1 || day > days_in_month(year, month) || !is_valid_clock
    {
        return None;
    }
    let mut rest = &text[19..];
    let mut fraction_nanos: i128 = 0;
    if let Some(after_dot) = rest.strip_prefix('.') {
        let digits_end = after_dot
            .find(|character: char| !character.is_ascii_digit())
            .unwrap_or(after_dot.len());
        if digits_end == 0 || digits_end > 9 {
            return None;
        }
        let digits = &after_dot[..digits_end];
        fraction_nanos = digits.parse::<i128>().ok()? * 10_i128.pow(9 - digits_end as u32);
        rest = &after_dot[digits_end..];
    }
    let offset_seconds: i64 = match rest {
        "Z" => 0,
        _ => {
            let sign = match rest.as_bytes().first() {
                Some(b'+') => 1,
                Some(b'-') => -1,
                _ => return None,
            };
            if rest.len() != 6 || rest.as_bytes()[3] != b':' {
                return None;
            }
            let offset_hours: i64 = rest.get(1..3)?.parse().ok()?;
            let offset_minutes: i64 = rest.get(4..6)?.parse().ok()?;
            if offset_hours > 23 || offset_minutes > 59 {
                return None;
            }
            sign * (offset_hours * 3600 + offset_minutes * 60)
        }
    };
    let days = days_from_civil(year, month, day);
    let seconds = days * 86_400 + hour * 3600 + minute * 60 + second - offset_seconds;
    Some(i128::from(seconds) * 1_000_000_000 + fraction_nanos)
}

fn days_in_month(year: i64, month: i64) -> i64 {
    match month {
        2 if (year % 4 == 0 && year % 100 != 0) || year % 400 == 0 => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

/// Days since 1970-01-01 of a proleptic Gregorian date (Howard Hinnant's algorithm).
fn days_from_civil(year: i64, month: i64, day: i64) -> i64 {
    let shifted_year = if month <= 2 { year - 1 } else { year };
    let era = shifted_year.div_euclid(400);
    let year_of_era = shifted_year - era * 400;
    let shifted_month = (month + 9) % 12;
    let day_of_year = (153 * shifted_month + 2) / 5 + day - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    era * 146_097 + day_of_era - 719_468
}

#[cfg(test)]
mod tests {
    use super::parse_reset_time;

    #[test]
    fn reset_times_parse_like_the_go_server() {
        assert_eq!(
            parse_reset_time("2026-10-23T08:00:00Z", 0),
            Ok(1_792_742_400_000_000_000)
        );
        assert_eq!(
            parse_reset_time("2026-10-23T01:00:00.5-07:00", 0),
            Ok(1_792_742_400_500_000_000)
        );
        assert_eq!(parse_reset_time("1970-01-01T00:00:00Z", 0), Ok(0));
        assert_eq!(parse_reset_time("42", 0), Ok(42));
        assert_eq!(
            parse_reset_time("3d", 5 * 86_400_000_000),
            Ok(2 * 86_400_000_000_000)
        );
        assert_eq!(
            parse_reset_time("2minute", 600_000_000),
            Ok(480_000_000_000)
        );
        assert_eq!(parse_reset_time("1000y", 1_000_000), Ok(0));
        assert!(parse_reset_time("0d", 0).is_err());
        assert!(parse_reset_time("3x", 0).is_err());
        assert!(parse_reset_time("2026-02-30T00:00:00Z", 0).is_err());
    }
}
