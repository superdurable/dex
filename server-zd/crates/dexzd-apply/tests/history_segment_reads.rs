// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History pages read across uploaded segments and local rows, checked against local reads of
//! the same committed history.
//!
//! Tests copy the local read, then delete covered local rows, so reading them would show.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use dexzd_apply::read_model::{
    MAX_HISTORY_PAGE_EVENTS, SegmentedHistoryPage, get_history_events,
    get_history_events_from_segments, read_history_segment_content,
};
use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, LoadedHistorySegments, StateReadError, StateRow,
    StateWrite, apply_flow_command,
};
use dexzd_history_segment::{decode_history_segment, encode_history_segment};
use dexzd_keyspace::row_keys::{history_record_key, history_row_metadata_key, prefix_range};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_protocol::dex::{
    AttributeWrite, FlowHistoryEvent, FlowResetType, FlowTimeoutPolicy, GetHistoryEventsResponse,
    ResetFlowRequest, SetAttributesRequest, StartFlowRequest, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, CommandReply, FlowCommand, FlowStartCommand, HistoryRecord,
    HistoryRowMetadata, HistorySegmentRecord, HistorySegmentState, HistorySegmentUploadedCommand,
    RunRecord, RunSettings, SetHistoryOffloadModeCommand, TimeTravelCommand, command_reply,
    flow_command::Command,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const RESPONSE_LIMIT: usize = 32 << 20;
const PAGE_SIZES: [i32; 4] = [0, 1, 3, 7];

/// Uploaded segments with their decoded records, by first seq.
type SegmentObjects = BTreeMap<u64, (HistorySegmentRecord, Arc<[HistoryRecord]>)>;

#[derive(Default, Clone)]
struct MemoryState {
    rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
}

impl FlowStateReader for MemoryState {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(self.rows.get(&(column_family, key.to_vec())).cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(self
            .rows
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect())
    }
}

impl MemoryState {
    fn commit(&mut self, outcome: &ApplyOutcome) {
        for write in &outcome.writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    self.rows
                        .insert((*column_family, key.clone()), value.clone());
                }
                StateWrite::Delete { column_family, key }
                | StateWrite::SingleDelete { column_family, key } => {
                    self.rows.remove(&(*column_family, key.clone()));
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => {
                    let doomed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in doomed {
                        self.rows.remove(&key);
                    }
                }
            }
        }
    }
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    flow_id: String,
    run_id: Vec<u8>,
    request_count: u32,
    /// Records of every acknowledged segment, read when it was acknowledged as an upload does.
    objects: SegmentObjects,
}

impl Harness {
    /// A run whose partition offloads history from its start.
    fn offloading(settings: RunSettings) -> Self {
        let flow_id = "segment-read-flow";
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let mut harness = Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000 * SECOND,
            flow_id: flow_id.to_owned(),
            run_id: Vec::new(),
            request_count: 0,
            objects: SegmentObjects::new(),
        };
        harness.set_offload_mode(true);
        harness.start(settings);
        harness
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        self.now_micros += 1000;
        let command = FlowCommand {
            flow_id: self.flow_id.clone(),
            run_id: if matches!(
                body,
                Command::FlowStart(_) | Command::SetHistoryOffloadMode(_)
            ) {
                Vec::new()
            } else {
                self.run_id.clone()
            },
            expected_run_attempt: 0,
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            tenant_id: 0,
            is_transactional_rpc_in_flight: false,
            command: Some(body),
        };
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        self.state.commit(&outcome);
        outcome
    }

    fn set_offload_mode(&mut self, is_enabled: bool) {
        let outcome = self.apply(Command::SetHistoryOffloadMode(
            SetHistoryOffloadModeCommand {
                enabled: is_enabled,
                partition: u32::from(self.context.partition.0),
            },
        ));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
    }

    fn start(&mut self, settings: RunSettings) {
        let outcome = self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(StartFlowRequest {
                flow_id: self.flow_id.clone(),
                flow_type: "SegmentReadFlow".to_owned(),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            }),
            settings: Some(settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
            panic!("the run starts: {:?}", outcome.reply);
        };
        self.run_id = reply.run_id.clone();
    }

    fn set_attribute(&mut self, key: &str, text: String) {
        self.request_count += 1;
        let request_id = format!("write-{}", self.request_count);
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: key.to_owned(),
                    value: Some(Value {
                        kind: Some(value::Kind::StringValue(text)),
                    }),
                    ..AttributeWrite::default()
                }],
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }));
        assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
    }

    fn write_rounds(&mut self, tag: &str, rounds: usize) {
        for round in 0..rounds {
            self.set_attribute("profile", format!("{tag}-{round}"));
        }
    }

    fn travel_to_seq(&mut self, seq: u64) {
        let metadata: HistoryRowMetadata = self
            .get(
                ColumnFamily::Flow,
                &history_row_metadata_key(
                    self.context.flow_hash,
                    self.run(),
                    seq,
                    RunTable::HistoryRecord,
                    0,
                ),
            )
            .unwrap_or_else(|| panic!("history record {seq} has metadata"));
        let outcome = self.apply(Command::TimeTravel(TimeTravelCommand {
            staging_id: Vec::new(),
            computed_through_seq: 0,
            target_run_attempt: 0,
            target_run_seq: 0,
            should_restore_running_as_pending: false,
            request: Some(ResetFlowRequest {
                flow_id: self.flow_id.clone(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                reason: "test".to_owned(),
                skip_writes_reapply: true,
                history_event_time: (i128::from(metadata.timestamp_micros) * 1000).to_string(),
                ..ResetFlowRequest::default()
            }),
        }));
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "{:?}",
            outcome.reply
        );
    }

    /// Acknowledges the oldest SEALED segments, at most `count`, and returns them.
    fn ack_oldest(&mut self, count: usize) -> Vec<HistorySegmentRecord> {
        let sealed: Vec<HistorySegmentRecord> = self
            .segments()
            .into_iter()
            .filter(|segment| segment.state == HistorySegmentState::Sealed as i32)
            .take(count)
            .collect();
        for segment in &sealed {
            let records = self.segment_records(segment);
            let uploaded = HistorySegmentRecord {
                state: HistorySegmentState::Uploaded as i32,
                ..segment.clone()
            };
            self.objects.insert(segment.first_seq, (uploaded, records));
            let outcome = self.apply(Command::HistorySegmentUploaded(
                HistorySegmentUploadedCommand {
                    first_seq: segment.first_seq,
                    last_seq: segment.last_seq,
                    content_checksum: vec![0xc5; 16],
                    object_size_bytes: 1234,
                },
            ));
            assert!(is_ok(&outcome.reply), "{:?}", outcome.reply);
        }
        sealed
    }

    fn ack_all(&mut self) {
        self.ack_oldest(usize::MAX);
    }

    fn run(&self) -> RunId {
        RunId::from_slice(&self.run_id).expect("the harness has a run")
    }

    fn get<RowMessage: Message + Default>(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Option<RowMessage> {
        self.state
            .rows
            .get(&(column_family, key.to_vec()))
            .map(|bytes| RowMessage::decode(bytes.as_slice()).expect("row decodes"))
    }

    fn run_record(&self) -> RunRecord {
        let key = dexzd_keyspace::row_keys::run_singleton_key(
            self.context.flow_hash,
            self.run(),
            RunTable::RunRecord,
        );
        self.get(ColumnFamily::Flow, &key)
            .expect("the run record exists")
    }

    fn segments(&self) -> Vec<HistorySegmentRecord> {
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), RunTable::HistorySegment);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|(_, value)| {
                HistorySegmentRecord::decode(value.as_slice()).expect("segment row decodes")
            })
            .collect()
    }

    fn uploaded_segments(&self) -> Vec<HistorySegmentRecord> {
        self.segments()
            .into_iter()
            .filter(|segment| segment.state == HistorySegmentState::Uploaded as i32)
            .collect()
    }

    /// Encodes and decodes the segment's object, as an upload and a read would.
    fn segment_records(&self, segment: &HistorySegmentRecord) -> Arc<[HistoryRecord]> {
        let content = read_history_segment_content(
            &self.state,
            self.context.flow_hash,
            self.run(),
            segment.first_seq,
        )
        .expect("the segment content reads")
        .expect("the segment exists");
        let encoded = encode_history_segment(&content).expect("the content encodes");
        decode_history_segment(&encoded.bytes)
            .expect("the object decodes")
            .records
            .into_iter()
            .map(|row| HistoryRecord::decode(row.row.as_slice()).expect("the record decodes"))
            .collect()
    }

    /// Returns every uploaded segment's records, read before local rows are deleted.
    fn every_uploaded_segment(&self) -> LoadedHistorySegments {
        let mut loaded = LoadedHistorySegments::default();
        for (segment, records) in self.uploaded_objects().into_values() {
            loaded
                .insert(&segment, records)
                .expect("segment records are ordered");
        }
        loaded
    }

    /// Returns every uploaded segment with its records, by first seq, as object storage holds.
    fn uploaded_objects(&self) -> SegmentObjects {
        self.objects.clone()
    }

    /// Deletes every local History record that an uploaded segment covers, genesis included.
    fn delete_uploaded_local_records(&mut self) {
        let flow_hash = self.context.flow_hash;
        let run_id = self.run();
        for segment in self.uploaded_segments() {
            for seq in segment.first_seq..=segment.last_seq {
                self.state.rows.remove(&(
                    ColumnFamily::History,
                    history_record_key(flow_hash, run_id, seq),
                ));
            }
        }
    }

    /// Returns the local rows plus every record that offload deleted after its upload, as the
    /// run stored them before: records of acknowledged segments whose metadata stays, or that
    /// hold no events. Records that window pruning deleted stay deleted.
    fn full_history_state(&self) -> MemoryState {
        let flow_hash = self.context.flow_hash;
        let run_id = self.run();
        let mut state = self.state.clone();
        for (_, records) in self.objects.values() {
            for record in records.iter() {
                let row_key = (
                    ColumnFamily::History,
                    history_record_key(flow_hash, run_id, record.seq),
                );
                let metadata_key = (
                    ColumnFamily::Flow,
                    history_row_metadata_key(
                        flow_hash,
                        run_id,
                        record.seq,
                        RunTable::HistoryRecord,
                        0,
                    ),
                );
                let is_kept = state.rows.contains_key(&metadata_key) || record.events.is_empty();
                if is_kept && !state.rows.contains_key(&row_key) {
                    state.rows.insert(row_key, record.encode_to_vec());
                }
            }
        }
        state
    }

    /// Stores the records of `segment` locally again, as a segment that was never uploaded keeps
    /// them.
    fn restore_local_records(&mut self, segment: &HistorySegmentRecord) {
        let flow_hash = self.context.flow_hash;
        let run_id = self.run();
        let (_, records) = &self.objects[&segment.first_seq];
        for record in records.iter() {
            self.state.rows.insert(
                (
                    ColumnFamily::History,
                    history_record_key(flow_hash, run_id, record.seq),
                ),
                record.encode_to_vec(),
            );
        }
    }

    /// Reads a page from the run's whole stored history, before offload deleted any of it.
    fn local_page(
        &self,
        start_event_id: i64,
        page_size: i32,
        token: &[u8],
        attempt: i32,
    ) -> GetHistoryEventsResponse {
        get_history_events(
            &self.full_history_state(),
            self.context.flow_hash,
            self.run(),
            start_event_id,
            page_size,
            token,
            attempt,
            RESPONSE_LIMIT,
        )
        .expect("the local page reads")
    }

    fn segmented_page(
        &self,
        start_event_id: i64,
        page_size: i32,
        token: &[u8],
        attempt: i32,
        loaded: &LoadedHistorySegments,
    ) -> SegmentedHistoryPage {
        segmented_page_of(
            &self.state,
            self,
            start_event_id,
            page_size,
            token,
            attempt,
            loaded,
            false,
        )
    }

    /// Reads a page, loading the segments it names from `objects` as the engine does.
    ///
    /// Returns the page and the segments it loaded.
    fn read_loading(
        &self,
        start_event_id: i64,
        page_size: i32,
        token: &[u8],
        attempt: i32,
        objects: &SegmentObjects,
        loaded: &mut LoadedHistorySegments,
    ) -> (GetHistoryEventsResponse, BTreeSet<u64>) {
        let mut fetched = BTreeSet::new();
        for _ in 0..64 {
            match self.segmented_page(start_event_id, page_size, token, attempt, loaded) {
                SegmentedHistoryPage::Page(page) => return (page, fetched),
                SegmentedHistoryPage::NeedsSegments(segments) => {
                    assert!(!segments.is_empty());
                    for segment in segments {
                        assert!(
                            !loaded.contains(&segment),
                            "a loaded segment is not named again"
                        );
                        let (_, records) = objects
                            .get(&segment.first_seq)
                            .unwrap_or_else(|| panic!("segment {segment:?} has an object"));
                        loaded
                            .insert(&segment, Arc::clone(records))
                            .expect("ordered records");
                        fetched.insert(segment.first_seq);
                    }
                }
            }
        }
        panic!("the page read never finished");
    }

    fn attempts(&self) -> Vec<i32> {
        let current = i32::try_from(self.run_record().current_attempt).expect("small attempt");
        (0..=current).collect()
    }
}

#[expect(
    clippy::too_many_arguments,
    reason = "mirrors get_history_events_from_segments"
)]
fn segmented_result(
    state: &dyn FlowStateReader,
    harness: &Harness,
    start_event_id: i64,
    page_size: i32,
    token: &[u8],
    loaded: &LoadedHistorySegments,
    should_end_before_unloaded_segment: bool,
    max_response_bytes: usize,
) -> Result<SegmentedHistoryPage, dexzd_apply::read_model::ReadModelError> {
    get_history_events_from_segments(
        state,
        harness.context.flow_hash,
        harness.run(),
        start_event_id,
        page_size,
        token,
        0,
        max_response_bytes,
        loaded,
        should_end_before_unloaded_segment,
    )
}

#[expect(
    clippy::too_many_arguments,
    reason = "mirrors get_history_events_from_segments"
)]
fn segmented_page_of(
    state: &dyn FlowStateReader,
    harness: &Harness,
    start_event_id: i64,
    page_size: i32,
    token: &[u8],
    attempt: i32,
    loaded: &LoadedHistorySegments,
    should_end_before_unloaded_segment: bool,
) -> SegmentedHistoryPage {
    get_history_events_from_segments(
        state,
        harness.context.flow_hash,
        harness.run(),
        start_event_id,
        page_size,
        token,
        attempt,
        RESPONSE_LIMIT,
        loaded,
        should_end_before_unloaded_segment,
    )
    .expect("the segmented page reads")
}

/// Counts the catalog rows that scans return.
struct CatalogRowCounter<'state> {
    state: &'state MemoryState,
    catalog_rows: std::cell::Cell<usize>,
}

impl FlowStateReader for CatalogRowCounter<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let rows = self.state.scan(column_family, start, end, limit)?;
        let catalog_rows = rows
            .iter()
            .filter(|(key, _)| {
                dexzd_keyspace::parse_run_key(key)
                    .is_ok_and(|parsed| parsed.table == RunTable::HistorySegment)
            })
            .count();
        self.catalog_rows
            .set(self.catalog_rows.get() + catalog_rows);
        Ok(rows)
    }
}

fn settings(segment_target_bytes: u64) -> RunSettings {
    RunSettings {
        history_window_events: 10_000,
        history_prune_chunk_events: 1000,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 20,
        checkpoint_command_threshold: 4,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: segment_target_bytes,
        delete_expired_history_objects: false,
        superseded_value_grace_micros: 0,
        offload_history: None,
    }
}

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

fn event_ids(events: &[FlowHistoryEvent]) -> Vec<i64> {
    events.iter().map(|event| event.event_id).collect()
}

fn expect_page(page: SegmentedHistoryPage) -> GetHistoryEventsResponse {
    match page {
        SegmentedHistoryPage::Page(page) => page,
        SegmentedHistoryPage::NeedsSegments(segments) => {
            panic!("every segment is loaded, yet the read needs {segments:?}")
        }
    }
}

/// Follows next page tokens from `start_event_id` and returns every page.
fn all_pages(
    mut read: impl FnMut(&[u8]) -> GetHistoryEventsResponse,
) -> Vec<GetHistoryEventsResponse> {
    let mut pages = Vec::new();
    let mut token = Vec::new();
    loop {
        let page = read(&token);
        token = page.next_page_token.clone();
        pages.push(page);
        if token.is_empty() {
            return pages;
        }
        assert!(pages.len() < 10_000, "paging never ends");
    }
}

/// A run with three Time Travel forks over many uploaded segments and a local tail.
fn forked_harness() -> Harness {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("a", 12);
    harness.travel_to_seq(6);
    harness.write_rounds("b", 9);
    harness.travel_to_seq(16);
    harness.write_rounds("c", 7);
    harness.travel_to_seq(3);
    harness.write_rounds("d", 10);
    harness
}

#[test]
fn pages_across_the_uploaded_seq_match_local_pages_for_every_lineage() {
    let mut harness = forked_harness();
    let segment_count = harness.segments().len();
    harness.ack_oldest(segment_count * 2 / 3);
    let record = harness.run_record();
    assert!(record.history_uploaded_through_seq > 20, "{record:?}");
    assert!(record.current_attempt >= 4);
    let next_event_id = record.next_event_id;
    let mut expected = BTreeMap::new();
    for attempt in harness.attempts() {
        for start_event_id in 0..=next_event_id {
            for page_size in PAGE_SIZES {
                let pages = all_pages(|token| {
                    harness.local_page(start_event_id, page_size, token, attempt)
                });
                expected.insert((attempt, start_event_id, page_size), pages);
            }
        }
    }
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();

    for ((attempt, start_event_id, page_size), expected_pages) in &expected {
        let pages = all_pages(|token| {
            expect_page(harness.segmented_page(
                *start_event_id,
                *page_size,
                token,
                *attempt,
                &loaded,
            ))
        });
        assert_eq!(
            &pages, expected_pages,
            "attempt {attempt}, start {start_event_id}, page size {page_size}"
        );
    }
    let everything =
        all_pages(|token| expect_page(harness.segmented_page(0, 0, token, 0, &loaded)));
    let ids: Vec<i64> = everything
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    assert_eq!(ids.iter().filter(|id| **id == 1).count(), 1, "event 1 once");
    assert!(ids.windows(2).all(|pair| pair[0] < pair[1]), "{ids:?}");
}

#[test]
fn a_read_needs_the_uploaded_segments_and_loads_few_for_a_page() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("linear", 60);
    harness.ack_all();
    let objects = harness.uploaded_objects();
    harness.delete_uploaded_local_records();
    let uploaded = harness.uploaded_segments();
    assert!(uploaded.len() > 50, "{} segments", uploaded.len());

    let SegmentedHistoryPage::NeedsSegments(needed) =
        harness.segmented_page(0, 3, &[], 0, &LoadedHistorySegments::default())
    else {
        panic!("nothing is loaded, so the read needs segments");
    };
    assert_eq!(needed[0], uploaded[0], "the read starts at genesis");

    let middle_event_id = harness.run_record().next_event_id / 2;
    let mut loaded = LoadedHistorySegments::default();
    let (page, fetched) = harness.read_loading(middle_event_id, 3, &[], 0, &objects, &mut loaded);
    assert_eq!(
        page.events.first().map(|event| event.event_id),
        Some(middle_event_id)
    );
    for first_seq in &fetched {
        let (segment, _) = &objects[first_seq];
        assert!(
            segment.last_event_id >= middle_event_id,
            "the search read segment {segment:?} before the page"
        );
    }
    // Each command is its own segment: four events show whether the page ends, plus a prefetch.
    assert!(
        fetched.len() <= 6,
        "a three-event page loads a few segments: {fetched:?}"
    );
}

#[test]
fn paging_while_acknowledgments_advance_the_uploaded_seq_has_no_gaps_or_duplicates() {
    let mut harness = forked_harness();
    harness.ack_oldest(2);
    let expected = all_pages(|token| harness.local_page(0, 4, token, 0));
    let expected_ids: Vec<i64> = expected
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    let mut objects = harness.uploaded_objects();
    let mut loaded = LoadedHistorySegments::default();
    let mut ids = Vec::new();
    let mut token = Vec::new();
    loop {
        let (page, _) = harness.read_loading(0, 4, &token, 0, &objects, &mut loaded);
        ids.extend(event_ids(&page.events));
        token = page.next_page_token.clone();
        if token.is_empty() {
            break;
        }
        // Between pages more segments upload and their local rows disappear.
        let acknowledged = harness.ack_oldest(3);
        assert!(!acknowledged.is_empty() || harness.uploaded_segments().len() == objects.len());
        objects = harness.uploaded_objects();
        harness.delete_uploaded_local_records();
    }
    assert_eq!(ids, expected_ids);
}

#[test]
fn records_that_window_pruning_removed_stay_hidden_inside_an_uploaded_segment() {
    let mut harness = Harness::offloading(RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..settings(2_500)
    });
    let mut straddled = None;
    for round in 0..60 {
        harness.set_attribute("profile", format!("{round}:{}", "x".repeat(400)));
        harness.ack_all();
        let metadata_prefix = run_table_prefix(
            harness.context.flow_hash,
            harness.run(),
            RunTable::HistoryRowMetadata,
        );
        let first_kept = harness
            .state
            .rows
            .range(
                (
                    ColumnFamily::Flow,
                    [metadata_prefix.as_slice(), &2_u64.to_be_bytes()].concat(),
                )..(ColumnFamily::Flow, prefix_range(&metadata_prefix).1),
            )
            .next()
            .map(|((_, key), _)| {
                u64::from_be_bytes(
                    key[metadata_prefix.len()..metadata_prefix.len() + 8]
                        .try_into()
                        .expect("eight seq bytes"),
                )
            });
        if let Some(first_kept) = first_kept
            && let Some(segment) = harness.uploaded_segments().into_iter().find(|segment| {
                segment.first_seq > 1
                    && segment.first_seq < first_kept
                    && first_kept <= segment.last_seq
            })
        {
            straddled = Some((segment, first_kept));
            break;
        }
    }
    let (segment, first_kept) = straddled.expect("pruning cut an uploaded segment");
    assert!(harness.run_record().max_pruned_event_id > 1);
    let expected = all_pages(|token| harness.local_page(0, 3, token, 0));
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();
    let pages = all_pages(|token| expect_page(harness.segmented_page(0, 3, token, 0, &loaded)));
    assert_eq!(pages, expected, "segment {segment:?} cut at {first_kept}");
    let pruned = pages[0]
        .pruned_event_id_range
        .expect("the window pruned events");
    for page in &pages {
        assert_eq!(page.pruned_event_id_range, Some(pruned));
        for event in &page.events {
            assert!(
                event.event_id == 1 || event.event_id > pruned.end_event_id,
                "event {} is inside the pruned range {pruned:?}",
                event.event_id
            );
        }
    }
}

#[test]
fn commands_applied_while_offload_was_off_read_from_local_rows_between_segments() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("on", 4);
    let gap_first_seq = harness.run_record().next_run_seq;
    harness.set_offload_mode(false);
    harness.write_rounds("off", 3);
    let gap_last_seq = harness.run_record().next_run_seq - 1;
    harness.set_offload_mode(true);
    harness.write_rounds("again", 4);
    harness.ack_all();
    let record = harness.run_record();
    assert!(
        record.history_uploaded_through_seq > gap_last_seq,
        "the gap is below the uploaded seq: {record:?}"
    );
    assert!(
        harness
            .uploaded_segments()
            .iter()
            .all(|segment| segment.last_seq < gap_first_seq || segment.first_seq > gap_last_seq),
        "no segment covers the gap"
    );
    let expected = all_pages(|token| harness.local_page(0, 3, token, 0));
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();
    let pages = all_pages(|token| expect_page(harness.segmented_page(0, 3, token, 0, &loaded)));
    assert_eq!(
        pages, expected,
        "the gap's events come from local rows, once"
    );
    assert_eq!(pages[0].pruned_event_id_range, None);
}

#[test]
fn a_page_returns_at_most_a_thousand_events() {
    let mut harness = Harness::offloading(settings(64 << 10));
    harness.write_rounds("bulk", 1_100);
    harness.ack_all();
    assert!(harness.run_record().next_event_id > 1_050);
    let local = harness.local_page(0, 5_000, &[], 0);
    assert_eq!(local.events.len(), MAX_HISTORY_PAGE_EVENTS);
    assert!(!local.next_page_token.is_empty());
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();
    let segmented = expect_page(harness.segmented_page(0, 5_000, &[], 0, &loaded));
    assert_eq!(segmented, local);
}

#[test]
fn a_run_without_uploads_reads_its_local_rows() {
    let harness = forked_harness();
    assert_eq!(harness.run_record().history_uploaded_through_seq, 0);
    let expected = all_pages(|token| harness.local_page(0, 3, token, 0));
    let pages = all_pages(|token| {
        expect_page(harness.segmented_page(0, 3, token, 0, &LoadedHistorySegments::default()))
    });
    assert_eq!(pages, expected);
}

#[test]
fn records_out_of_seq_order_are_rejected() {
    let segment = HistorySegmentRecord {
        first_seq: 5,
        last_seq: 9,
        ..HistorySegmentRecord::default()
    };
    let record = |seq| HistoryRecord {
        seq,
        ..HistoryRecord::default()
    };
    let mut loaded = LoadedHistorySegments::default();
    assert!(
        loaded
            .insert(&segment, vec![record(6), record(6)].into())
            .is_err()
    );
    assert!(loaded.insert(&segment, vec![record(4)].into()).is_err());
    assert!(loaded.insert(&segment, vec![record(10)].into()).is_err());
    assert!(!loaded.contains(&segment));
    assert!(
        loaded
            .insert(&segment, vec![record(5), record(9)].into())
            .is_ok()
    );
    assert!(loaded.contains(&segment));
}

#[test]
fn a_record_pruned_apart_from_its_checkpoint_metadata_stays_hidden() {
    let mut harness = Harness::offloading(RunSettings {
        checkpoint_command_threshold: 2,
        ..settings(1)
    });
    harness.write_rounds("split", 12);
    harness.ack_all();
    let flow_hash = harness.context.flow_hash;
    let run_id = harness.run();
    let metadata_key =
        |seq, table, attempt| history_row_metadata_key(flow_hash, run_id, seq, table, attempt);
    // A seq whose record and checkpoint both have metadata.
    let split_seq = (3..harness.run_record().next_run_seq)
        .find(|seq| {
            harness.state.rows.contains_key(&(
                ColumnFamily::Flow,
                metadata_key(*seq, RunTable::Checkpoint, 1),
            ))
        })
        .expect("a seq with a checkpoint");
    let split_metadata: HistoryRowMetadata = harness
        .get(
            ColumnFamily::Flow,
            &metadata_key(split_seq, RunTable::HistoryRecord, 0),
        )
        .expect("the record has metadata");
    // A pruning batch that ends between the record's and the checkpoint's metadata of split_seq.
    for seq in 2..=split_seq {
        harness.state.rows.remove(&(
            ColumnFamily::History,
            history_record_key(flow_hash, run_id, seq),
        ));
        harness.state.rows.remove(&(
            ColumnFamily::Flow,
            metadata_key(seq, RunTable::HistoryRecord, 0),
        ));
        if seq < split_seq {
            harness.state.rows.remove(&(
                ColumnFamily::Flow,
                metadata_key(seq, RunTable::Checkpoint, 1),
            ));
        }
    }
    let mut record = harness.run_record();
    record.max_pruned_event_id = split_metadata.last_assigned_event_id;
    record.window_start_seq = split_seq + 1;
    // Such a batch moves the frontier through its last record.
    record.history_window_pruned_through_seq = split_seq;
    let record_key =
        dexzd_keyspace::row_keys::run_singleton_key(flow_hash, run_id, RunTable::RunRecord);
    harness
        .state
        .rows
        .insert((ColumnFamily::Flow, record_key), record.encode_to_vec());

    let expected = all_pages(|token| harness.local_page(0, 3, token, 0));
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();
    let pages = all_pages(|token| expect_page(harness.segmented_page(0, 3, token, 0, &loaded)));
    assert_eq!(pages, expected);
    let pruned_end = pages[0]
        .pruned_event_id_range
        .expect("the window pruned events")
        .end_event_id;
    for event in pages.iter().flat_map(|page| &page.events) {
        assert!(
            event.event_id == 1 || event.event_id > pruned_end,
            "event {} of a pruned record came back",
            event.event_id
        );
    }
}

#[test]
fn a_page_that_ends_before_an_unloaded_segment_continues_at_it() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("short", 30);
    harness.ack_all();
    let expected: Vec<i64> = all_pages(|token| harness.local_page(0, 0, token, 0))
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    let objects = harness.uploaded_objects();
    harness.delete_uploaded_local_records();
    let mut loaded = LoadedHistorySegments::default();
    let mut ids = Vec::new();
    let mut token = Vec::new();
    for _ in 0..1_000 {
        match segmented_page_of(&harness.state, &harness, 0, 0, &token, 0, &loaded, true) {
            SegmentedHistoryPage::Page(page) => {
                ids.extend(event_ids(&page.events));
                token = page.next_page_token;
                if token.is_empty() {
                    break;
                }
            }
            SegmentedHistoryPage::NeedsSegments(needed) => {
                // Load only the first segment, as a round budget that ran out would.
                let (segment, records) = &objects[&needed[0].first_seq];
                loaded
                    .insert(segment, Arc::clone(records))
                    .expect("ordered records");
            }
        }
    }
    assert!(token.is_empty(), "paging reached the end");
    assert_eq!(ids, expected, "shorter pages skip and repeat nothing");
}

#[test]
fn a_page_read_reads_a_bounded_number_of_catalog_rows() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("many", 2_000);
    harness.ack_all();
    let segment_count = harness.uploaded_segments().len();
    assert!(segment_count > 2_000, "{segment_count} segments");
    let loaded = harness.every_uploaded_segment();
    let middle_event_id = harness.run_record().next_event_id / 2;
    let counter = CatalogRowCounter {
        state: &harness.state,
        catalog_rows: std::cell::Cell::new(0),
    };
    let page = expect_page(segmented_page_of(
        &counter,
        &harness,
        middle_event_id,
        1,
        &[],
        0,
        &loaded,
        false,
    ));
    assert_eq!(page.events.len(), 1);
    let catalog_rows = counter.catalog_rows.get();
    assert!(
        catalog_rows < 200,
        "a one-event page read {catalog_rows} of {segment_count} catalog rows"
    );
}

/// Pages ending before unloaded segments, loading only the first segment each read names.
///
/// Returns every event ID read.
fn page_with_partial_loads(
    harness: &Harness,
    objects: &SegmentObjects,
    start_event_id: i64,
    page_size: i32,
    attempt: i32,
) -> Vec<i64> {
    let mut loaded = LoadedHistorySegments::default();
    let mut ids = Vec::new();
    let mut token = Vec::new();
    for _ in 0..2_000 {
        let page = segmented_page_of(
            &harness.state,
            harness,
            start_event_id,
            page_size,
            &token,
            attempt,
            &loaded,
            true,
        );
        match page {
            SegmentedHistoryPage::Page(page) => {
                ids.extend(event_ids(&page.events));
                token = page.next_page_token;
                if token.is_empty() {
                    return ids;
                }
            }
            SegmentedHistoryPage::NeedsSegments(needed) => {
                let (segment, records) = &objects[&needed[0].first_seq];
                loaded
                    .insert(segment, Arc::clone(records))
                    .expect("ordered records");
            }
        }
    }
    panic!("paging with partial loads made no progress");
}

#[test]
fn partial_loads_page_every_lineage_with_empty_pages_across_abandoned_forks() {
    let mut harness = forked_harness();
    harness.ack_all();
    let objects = harness.uploaded_objects();
    let next_event_id = harness.run_record().next_event_id;
    let mut expected = BTreeMap::new();
    for attempt in harness.attempts() {
        for start_event_id in [0, 5, 12, 20, 28, next_event_id - 1] {
            let ids: Vec<i64> =
                all_pages(|token| harness.local_page(start_event_id, 3, token, attempt))
                    .iter()
                    .flat_map(|page| event_ids(&page.events))
                    .collect();
            expected.insert((attempt, start_event_id), ids);
        }
    }
    harness.delete_uploaded_local_records();
    for ((attempt, start_event_id), ids) in expected {
        assert_eq!(
            page_with_partial_loads(&harness, &objects, start_event_id, 3, attempt),
            ids,
            "attempt {attempt} from event {start_event_id}"
        );
    }
}

#[test]
fn a_continuation_token_never_pushes_a_shorter_page_over_the_response_limit() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("limit", 12);
    harness.ack_all();
    let objects = harness.uploaded_objects();
    harness.delete_uploaded_local_records();
    let uploaded = harness.uploaded_segments();
    // Load the first five segments, so pages end before the sixth.
    let mut loaded = LoadedHistorySegments::default();
    for segment in &uploaded[..5] {
        let (_, records) = &objects[&segment.first_seq];
        loaded
            .insert(segment, Arc::clone(records))
            .expect("ordered records");
    }
    let read = |max_response_bytes| {
        segmented_result(
            &harness.state,
            &harness,
            0,
            100,
            &[],
            &loaded,
            true,
            max_response_bytes,
        )
    };
    let Ok(SegmentedHistoryPage::Page(page)) = read(RESPONSE_LIMIT) else {
        panic!("a page ends before the first unloaded segment");
    };
    assert!(page.events.len() > 1);
    assert!(!page.next_page_token.is_empty());
    let full_size = page.encoded_len();
    let Ok(SegmentedHistoryPage::Page(shorter)) = read(full_size - 1) else {
        panic!("a smaller limit still returns a page");
    };
    assert!(shorter.encoded_len() < full_size);
    assert_eq!(shorter.events, page.events[..page.events.len() - 1]);
    let last_event_id = shorter.events.last().expect("events remain").event_id;
    assert_eq!(
        shorter.next_page_token,
        last_event_id.to_be_bytes().to_vec()
    );

    // One event that fits only without the token fails like any event too large to return.
    let mut genesis_only = LoadedHistorySegments::default();
    let (segment, records) = &objects[&uploaded[0].first_seq];
    genesis_only
        .insert(segment, Arc::clone(records))
        .expect("ordered records");
    let read_one = |max_response_bytes| {
        segmented_result(
            &harness.state,
            &harness,
            0,
            100,
            &[],
            &genesis_only,
            true,
            max_response_bytes,
        )
    };
    let Ok(SegmentedHistoryPage::Page(single)) = read_one(RESPONSE_LIMIT) else {
        panic!("the genesis page ends before the next segment");
    };
    assert_eq!(single.events.len(), 1);
    assert!(matches!(
        read_one(single.encoded_len() - 1),
        Err(dexzd_apply::read_model::ReadModelError::HistoryEventTooLarge { .. })
    ));
}

/// A HistorySegmentState value that this binary does not know, as a later state would be.
const UNKNOWN_SEGMENT_STATE: i32 = 3;

#[test]
fn reads_skip_a_long_expired_prefix_and_sparse_rows_without_scanning_them() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("expired", 2_000);
    harness.ack_all();
    let flow_hash = harness.context.flow_hash;
    let run_id = harness.run();
    let segments = harness.uploaded_segments();
    let expired_through = segments[1_200].last_seq;
    let segment_key =
        |first_seq| dexzd_keyspace::row_keys::history_segment_key(flow_hash, run_id, first_seq);
    for (index, segment) in segments.iter().enumerate() {
        let is_expired = segment.last_seq <= expired_through;
        if is_expired || index % 70 == 0 {
            let mut row = segment.clone();
            row.state = UNKNOWN_SEGMENT_STATE;
            harness.state.rows.insert(
                (ColumnFamily::Flow, segment_key(segment.first_seq)),
                row.encode_to_vec(),
            );
            harness.restore_local_records(segment);
        } else if index % 50 == 0 {
            harness
                .state
                .rows
                .remove(&(ColumnFamily::Flow, segment_key(segment.first_seq)));
            harness.restore_local_records(segment);
        }
    }
    // The window pruned every expired segment's records, but never genesis.
    let last_pruned: HistoryRowMetadata = harness
        .get(
            ColumnFamily::Flow,
            &history_row_metadata_key(
                flow_hash,
                run_id,
                expired_through,
                RunTable::HistoryRecord,
                0,
            ),
        )
        .expect("the last pruned record has metadata");
    let metadata_prefix = run_table_prefix(flow_hash, run_id, RunTable::HistoryRowMetadata);
    for seq in 2..=expired_through {
        harness.state.rows.remove(&(
            ColumnFamily::History,
            history_record_key(flow_hash, run_id, seq),
        ));
        let start = [metadata_prefix.as_slice(), &seq.to_be_bytes()].concat();
        let end = [metadata_prefix.as_slice(), &(seq + 1).to_be_bytes()].concat();
        let doomed: Vec<_> = harness
            .state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|(key, _)| key.clone())
            .collect();
        for key in doomed {
            harness.state.rows.remove(&key);
        }
    }
    let mut record = harness.run_record();
    record.max_pruned_event_id = last_pruned.last_assigned_event_id;
    record.window_start_seq = expired_through + 1;
    record.history_window_pruned_through_seq = expired_through;
    harness.state.rows.insert(
        (
            ColumnFamily::Flow,
            dexzd_keyspace::row_keys::run_singleton_key(flow_hash, run_id, RunTable::RunRecord),
        ),
        record.encode_to_vec(),
    );

    let expected = all_pages(|token| harness.local_page(0, 50, token, 0));
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();
    let pages = all_pages(|token| expect_page(harness.segmented_page(0, 50, token, 0, &loaded)));
    assert_eq!(pages, expected, "rows that are not UPLOADED read locally");

    let counter = CatalogRowCounter {
        state: &harness.state,
        catalog_rows: std::cell::Cell::new(0),
    };
    let page = expect_page(segmented_page_of(
        &counter,
        &harness,
        0,
        1,
        &[],
        0,
        &loaded,
        false,
    ));
    assert_eq!(page.events.first().map(|event| event.event_id), Some(1));
    let middle_event_id = harness.run_record().next_event_id - 300;
    let page = expect_page(segmented_page_of(
        &counter,
        &harness,
        middle_event_id,
        1,
        &[],
        0,
        &loaded,
        false,
    ));
    assert_eq!(page.events.len(), 1);
    let catalog_rows = counter.catalog_rows.get();
    assert!(
        catalog_rows < 300,
        "two short pages read {catalog_rows} catalog rows past a prefix of 1200 expired rows"
    );
}

/// Clears the event ID fields of every history record's metadata, as older binaries wrote it.
fn clear_metadata_event_ids(harness: &mut Harness) {
    let prefix = run_table_prefix(
        harness.context.flow_hash,
        harness.run(),
        RunTable::HistoryRowMetadata,
    );
    let (start, end) = prefix_range(&prefix);
    let rewritten: Vec<_> = harness
        .state
        .rows
        .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
        .map(|((_, key), value)| {
            let mut metadata =
                HistoryRowMetadata::decode(value.as_slice()).expect("metadata decodes");
            metadata.last_assigned_event_id = 0;
            metadata.max_event_id = 0;
            (key.clone(), metadata.encode_to_vec())
        })
        .collect();
    for (key, value) in rewritten {
        harness.state.rows.insert((ColumnFamily::Flow, key), value);
    }
}

#[test]
fn a_catalog_budget_stop_after_far_seek_probes_keeps_its_continuation_token() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("legacy", 3_000);
    harness.ack_all();
    clear_metadata_event_ids(&mut harness);
    let expected: Vec<i64> = all_pages(|token| harness.local_page(2, 1_000, token, 0))
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();
    let read = |token: &[u8]| {
        match dexzd_apply::read_model::get_history_events_from_segments_with_catalog_budget_for_test(
            &harness.state,
            harness.context.flow_hash,
            harness.run(),
            2,
            1_000,
            token,
            RESPONSE_LIMIT,
            &loaded,
            300,
        )
        .expect("the page reads")
        {
            SegmentedHistoryPage::Page(page) => page,
            SegmentedHistoryPage::NeedsSegments(needed) => panic!("needs {needed:?}"),
        }
    };
    let first = read(&[]);
    assert!(
        first.events.len() < 1_000,
        "the catalog budget ended the first page early"
    );
    assert!(
        !first.next_page_token.is_empty(),
        "a page the budget ended continues with a token"
    );
    let ids: Vec<i64> = all_pages(read)
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    assert_eq!(ids, expected);
}

#[test]
fn a_last_round_read_with_far_seek_probes_still_ends_before_an_unloaded_segment() {
    let mut harness = Harness::offloading(settings(1));
    harness.write_rounds("legacy", 200);
    harness.ack_all();
    clear_metadata_event_ids(&mut harness);
    let objects = harness.uploaded_objects();
    harness.delete_uploaded_local_records();
    let uploaded = harness.uploaded_segments();
    let unloaded = &uploaded[10];
    let mut loaded = LoadedHistorySegments::default();
    for segment in &uploaded {
        if segment.first_seq != unloaded.first_seq {
            let (_, records) = &objects[&segment.first_seq];
            loaded
                .insert(segment, Arc::clone(records))
                .expect("ordered records");
        }
    }
    let SegmentedHistoryPage::Page(page) =
        segmented_page_of(&harness.state, &harness, 2, 1_000, &[], 0, &loaded, true)
    else {
        panic!("the last round returns a page even though the seek probed far records");
    };
    let last_event_id = page.events.last().expect("events before the gap").event_id;
    assert!(last_event_id < unloaded.first_event_id);
    assert_eq!(page.next_page_token, last_event_id.to_be_bytes().to_vec());
}

/// A run whose window pruned uploaded records, written by binaries that kept no event IDs in
/// history metadata or the run record, so reads scan for the pruned range.
fn legacy_pruned_harness(rounds: usize, window_events: u32) -> Harness {
    let mut harness = Harness::offloading(RunSettings {
        history_window_events: window_events,
        history_prune_chunk_events: (window_events / 4).max(1),
        checkpoint_command_threshold: 2,
        ..settings(1)
    });
    for round in 0..rounds {
        harness.set_attribute("profile", format!("legacy-{round}"));
        harness.ack_all();
    }
    clear_metadata_event_ids(&mut harness);
    let mut record = harness.run_record();
    assert!(record.window_start_seq > 2, "the window moved: {record:?}");
    record.max_pruned_event_id = 0;
    let key = dexzd_keyspace::row_keys::run_singleton_key(
        harness.context.flow_hash,
        harness.run(),
        RunTable::RunRecord,
    );
    harness
        .state
        .rows
        .insert((ColumnFamily::Flow, key), record.encode_to_vec());
    harness
}

/// Returns the first seq after genesis whose record metadata pruning kept.
fn first_kept_seq(harness: &Harness) -> u64 {
    (2..harness.run_record().next_run_seq)
        .find(|seq| {
            harness.state.rows.contains_key(&(
                ColumnFamily::Flow,
                history_row_metadata_key(
                    harness.context.flow_hash,
                    harness.run(),
                    *seq,
                    RunTable::HistoryRecord,
                    0,
                ),
            ))
        })
        .expect("the window keeps records")
}

#[test]
fn a_last_round_read_fetches_the_segment_the_legacy_pruned_range_scan_needs() {
    let mut harness = legacy_pruned_harness(40, 4);
    let start_event_id = harness.run_record().next_event_id - 5;
    let expected = harness.local_page(start_event_id, 3, &[], 0);
    assert!(expected.pruned_event_id_range.is_some());
    let objects = harness.uploaded_objects();
    harness.delete_uploaded_local_records();
    let first_kept_seq = first_kept_seq(&harness);
    let scanned = harness
        .uploaded_segments()
        .into_iter()
        .find(|segment| segment.first_seq <= first_kept_seq && first_kept_seq <= segment.last_seq)
        .expect("an uploaded segment holds the first kept record");
    let mut loaded = LoadedHistorySegments::default();
    for segment in harness.uploaded_segments() {
        if segment.first_seq != scanned.first_seq {
            let (_, records) = &objects[&segment.first_seq];
            loaded
                .insert(&segment, Arc::clone(records))
                .expect("ordered records");
        }
    }
    let read = |loaded: &LoadedHistorySegments| {
        segmented_page_of(
            &harness.state,
            &harness,
            start_event_id,
            3,
            &[],
            0,
            loaded,
            true,
        )
    };
    let SegmentedHistoryPage::NeedsSegments(needed) = read(&loaded) else {
        panic!("a page whose pruned range is unknown is never returned");
    };
    assert_eq!(needed.first(), Some(&scanned));
    let (_, records) = &objects[&scanned.first_seq];
    loaded
        .insert(&scanned, Arc::clone(records))
        .expect("ordered records");
    assert_eq!(expect_page(read(&loaded)), expected);
}

#[test]
fn a_budget_stop_keeps_the_legacy_pruned_range_and_makes_progress() {
    let mut harness = legacy_pruned_harness(500, 300);
    let expected = all_pages(|token| harness.local_page(0, 1_000, token, 0));
    let expected_ids: Vec<i64> = expected
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    let pruned = expected[0].pruned_event_id_range;
    assert!(pruned.is_some());
    let loaded = harness.every_uploaded_segment();
    harness.delete_uploaded_local_records();
    let read = |token: &[u8]| {
        match dexzd_apply::read_model::get_history_events_from_segments_with_catalog_budget_for_test(
            &harness.state,
            harness.context.flow_hash,
            harness.run(),
            0,
            1_000,
            token,
            RESPONSE_LIMIT,
            &loaded,
            150,
        )
        .expect("the page reads")
        {
            SegmentedHistoryPage::Page(page) => page,
            SegmentedHistoryPage::NeedsSegments(needed) => {
                panic!("everything is loaded, yet the read needs {needed:?}")
            }
        }
    };
    let pages = all_pages(read);
    assert!(pages.len() > 1, "the budget shortened the pages");
    for page in &pages {
        assert_eq!(page.pruned_event_id_range, pruned);
    }
    let ids: Vec<i64> = pages
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    assert_eq!(ids, expected_ids);
}

#[test]
fn a_retained_eventless_prefix_longer_than_the_budget_still_pages_with_a_safe_pruned_range() {
    let mut harness = legacy_pruned_harness(500, 300);
    let objects = harness.uploaded_objects();
    harness.delete_uploaded_local_records();
    let first_kept_seq = first_kept_seq(&harness);
    let eventless = first_kept_seq..first_kept_seq + 200;
    // The retained records right after the window start hold no events.
    let mut loaded = LoadedHistorySegments::default();
    let mut expected = vec![1];
    for (segment, records) in objects.values() {
        if segment.state != HistorySegmentState::Uploaded as i32 {
            continue;
        }
        let records: Vec<HistoryRecord> = records
            .iter()
            .map(|record| {
                let mut record = record.clone();
                if eventless.contains(&record.seq) {
                    record.events.clear();
                } else if record.seq >= first_kept_seq {
                    expected.extend(record.events.iter().map(|event| event.event_id));
                }
                record
            })
            .collect();
        loaded
            .insert(segment, records.into())
            .expect("ordered records");
    }
    let read = |token: &[u8]| {
        match dexzd_apply::read_model::get_history_events_from_segments_with_catalog_budget_for_test(
            &harness.state,
            harness.context.flow_hash,
            harness.run(),
            0,
            1_000,
            token,
            RESPONSE_LIMIT,
            &loaded,
            150,
        )
        .expect("the page reads")
        {
            SegmentedHistoryPage::Page(page) => page,
            SegmentedHistoryPage::NeedsSegments(needed) => {
                panic!("everything is loaded, yet the read needs {needed:?}")
            }
        }
    };
    let pages = all_pages(read);
    let ids: Vec<i64> = pages
        .iter()
        .flat_map(|page| event_ids(&page.events))
        .collect();
    assert_eq!(ids, expected, "every retained event, once and in order");
    for page in &pages {
        if let Some(pruned) = page.pruned_event_id_range {
            for event_id in &ids {
                assert!(
                    *event_id == 1 || *event_id > pruned.end_event_id,
                    "the pruned range {pruned:?} hides retained event {event_id}"
                );
            }
        }
    }
}
