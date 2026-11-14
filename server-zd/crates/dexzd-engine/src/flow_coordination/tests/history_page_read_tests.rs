// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! GetHistoryEvents over uploaded segments with the real apply: views, races, and caches.

use std::collections::BTreeMap;
use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_apply::read_model::{get_history_events, read_history_segment_content};
use dexzd_apply::{ApplyContext, ApplyOutcome, apply_flow_command};
use dexzd_history_segment::{
    HistorySegmentContent, encode_history_segment, history_segment_object_key,
};
use dexzd_keyspace::row_keys::{
    history_record_key, history_row_metadata_key, prefix_range, run_singleton_key,
};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, run_table_prefix};
use dexzd_metrics::{HistoryReadKind, HistoryReadSource, NodeMetrics};
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore,
    ObjectStoreOperation, ObjectStoreResult, WriteCondition, WriteOutcome,
};
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowTimeoutPolicy, GetFlowSummaryRequest, GetHistoryEventsRequest,
    GetHistoryEventsResponse, SetAttributesRequest, StartFlowRequest, UpdateFlowConfigRequest,
    Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, FlowConfigUpdateCommand, FlowStartCommand, HistoryRecord,
    HistoryRowMetadata, HistorySegmentRecord, HistorySegmentState, HistorySegmentUploadedCommand,
    RunRecord, RunSettings, SetHistoryOffloadModeCommand, command_reply, flow_command::Command,
};
use prost::Message;
use tokio::sync::watch;

use super::fixtures::TENANT;
use super::test_support::{FlowCoordinationHarness, MemoryStore, MemoryView};
use crate::BlockingWorkPlacement;
use crate::flow_coordination::history_reads::SegmentLocation;
use crate::flow_coordination::{
    FlowCoordinationConfig, HistoryReadsConfig, HistorySegmentReader, default_run_settings,
    grpc_code,
};

const FLOW_ID: &str = "offloaded-history";
const WAIT_ROUNDS: usize = 2_000;

/// An object store whose ranged reads wait while the gate is closed.
struct GatedObjectStore {
    inner: Arc<InMemoryObjectStore>,
    gate: watch::Sender<bool>,
    waiting_reads: AtomicUsize,
}

impl GatedObjectStore {
    fn new() -> Self {
        Self {
            inner: Arc::new(InMemoryObjectStore::new()),
            gate: watch::Sender::new(true),
            waiting_reads: AtomicUsize::new(0),
        }
    }

    fn close_gate(&self) {
        self.gate.send_replace(false);
    }

    fn open_gate(&self) {
        self.gate.send_replace(true);
    }

    fn history_reads(&self) -> u64 {
        self.inner.call_count(ObjectStoreOperation::Get, "history/")
    }
}

#[async_trait]
impl ObjectStore for GatedObjectStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put_file(key, path, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        let mut gate = self.gate.subscribe();
        if !*gate.borrow() {
            self.waiting_reads.fetch_add(1, Ordering::SeqCst);
            gate.wait_for(|is_open| *is_open)
                .await
                .expect("the gate sender lives as long as the store");
            self.waiting_reads.fetch_sub(1, Ordering::SeqCst);
        }
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.inner.head(key).await
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.inner.delete(key).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        self.inner.delete_many(keys).await
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        self.inner.list(prefix, start_after, max_keys).await
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        self.inner.supports_replace_if_etag_matches_on_every_key()
    }
}

/// A real-apply run whose sealed segments the test uploads and acknowledges like the uploader.
struct OffloadedRun {
    store: MemoryStore,
    objects: Arc<GatedObjectStore>,
    context: ApplyContext,
    run_id: RunId,
    now_micros: i64,
    request_count: u32,
    /// Content of each acknowledged segment by first seq, as its object holds it.
    uploaded: BTreeMap<u64, HistorySegmentContent>,
}

impl OffloadedRun {
    fn start(settings: RunSettings) -> Self {
        Self::start_with_request(
            settings,
            StartFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_type: "OffloadedFlow".to_owned(),
                request_id: "start".to_owned(),
                ..StartFlowRequest::default()
            },
        )
    }

    fn start_with_request(settings: RunSettings, request: StartFlowRequest) -> Self {
        // The harness registry hashes Flows with this key and partition count.
        let hasher = PartitionHasher::new(&[7; 16], 4).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TENANT, FLOW_ID);
        let mut run = Self {
            store: MemoryStore::default(),
            objects: Arc::new(GatedObjectStore::new()),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            run_id: RunId([0; 16]),
            now_micros: 1_800_000_000_000_000,
            request_count: 0,
            uploaded: BTreeMap::new(),
        };
        run.apply(Command::SetHistoryOffloadMode(
            SetHistoryOffloadModeCommand {
                enabled: true,
                partition: u32::from(run.context.partition.0),
            },
        ));
        let outcome = run.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(request),
            settings: Some(settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget {
                is_pull_mode: true,
                ..WorkerTarget::default()
            }),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = outcome.reply.reply else {
            panic!("the run starts: {:?}", outcome.reply);
        };
        run.run_id = RunId::from_slice(&reply.run_id).expect("a 16-byte run ID");
        run
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        self.now_micros += 1_000;
        let is_run_command = !matches!(
            body,
            Command::FlowStart(_) | Command::SetHistoryOffloadMode(_)
        );
        let command = FlowCommand {
            flow_id: FLOW_ID.to_owned(),
            run_id: if is_run_command {
                self.run_id.0.to_vec()
            } else {
                Vec::new()
            },
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            command: Some(body),
            ..FlowCommand::default()
        };
        let outcome = apply_flow_command(&self.context, &self.store.view(), &command)
            .expect("apply succeeds");
        self.store.apply_writes(&outcome.writes);
        outcome
    }

    fn write_rounds(&mut self, rounds: usize) {
        for _ in 0..rounds {
            self.request_count += 1;
            let request_id = format!("write-{}", self.request_count);
            let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
                request: Some(SetAttributesRequest {
                    attributes: vec![AttributeWrite {
                        key: "profile".to_owned(),
                        value: Some(Value {
                            kind: Some(value::Kind::StringValue(request_id.clone())),
                        }),
                        ..AttributeWrite::default()
                    }],
                    request_id: request_id.clone(),
                    ..SetAttributesRequest::default()
                }),
                hard_delete_objects: Vec::new(),
                request_fingerprint: request_id.into_bytes(),
            }));
            assert!(
                matches!(outcome.reply.reply, Some(command_reply::Reply::Ok(_))),
                "{:?}",
                outcome.reply
            );
        }
    }

    /// Uploads every SEALED segment in seq order and acknowledges it.
    async fn upload_all(&mut self) {
        for segment in self.segments() {
            if segment.state != HistorySegmentState::Sealed as i32 {
                continue;
            }
            let content = read_history_segment_content(
                &self.store.view(),
                self.context.flow_hash,
                self.run_id,
                segment.first_seq,
            )
            .expect("the segment content reads")
            .expect("the segment exists");
            let encoded = encode_history_segment(&content).expect("the content encodes");
            let size = encoded.bytes.len() as u64;
            self.uploaded.insert(segment.first_seq, content);
            self.objects
                .inner
                .put(
                    &self.object_key(&segment),
                    Bytes::from(encoded.bytes),
                    WriteCondition::CreateOnly,
                )
                .await
                .expect("the object writes");
            self.apply(Command::HistorySegmentUploaded(
                HistorySegmentUploadedCommand {
                    first_seq: segment.first_seq,
                    last_seq: segment.last_seq,
                    content_checksum: encoded.content_checksum.to_vec(),
                    object_size_bytes: size,
                },
            ));
        }
        assert!(self.run_record().history_uploaded_through_seq > 0);
    }

    fn object_key(&self, segment: &HistorySegmentRecord) -> String {
        history_segment_object_key(
            TENANT,
            self.context.flow_hash,
            self.run_id,
            segment.first_seq,
            segment.last_seq,
        )
    }

    fn segments(&self) -> Vec<HistorySegmentRecord> {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.run_id,
            RunTable::HistorySegment,
        );
        let (start, end) = prefix_range(&prefix);
        self.rows_in(ColumnFamily::Flow, &start, &end)
            .into_iter()
            .map(|(_, value)| HistorySegmentRecord::decode(value.as_slice()).expect("row decodes"))
            .collect()
    }

    fn uploaded_segments(&self) -> Vec<HistorySegmentRecord> {
        self.segments()
            .into_iter()
            .filter(|segment| segment.state == HistorySegmentState::Uploaded as i32)
            .collect()
    }

    fn rows_in(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
    ) -> Vec<(Vec<u8>, Vec<u8>)> {
        use dexzd_apply::FlowStateReader;
        self.store
            .view()
            .scan(column_family, start, end, usize::MAX)
            .expect("the memory view scans")
    }

    fn run_record_key(&self) -> Vec<u8> {
        run_singleton_key(self.context.flow_hash, self.run_id, RunTable::RunRecord)
    }

    fn run_record(&self) -> RunRecord {
        use dexzd_apply::FlowStateReader;
        let bytes = self
            .store
            .view()
            .get(ColumnFamily::Flow, &self.run_record_key())
            .expect("the memory view reads")
            .expect("the run record exists");
        RunRecord::decode(bytes.as_slice()).expect("the run record decodes")
    }

    /// Deletes local History records that uploaded segments cover, so reading them would show.
    fn delete_uploaded_local_records(&self) {
        let mut writes = Vec::new();
        for segment in self.uploaded_segments() {
            for seq in segment.first_seq..=segment.last_seq {
                writes.push(dexzd_apply::StateWrite::Delete {
                    column_family: ColumnFamily::History,
                    key: history_record_key(self.context.flow_hash, self.run_id, seq),
                });
            }
        }
        self.store.apply_writes(&writes);
    }

    /// Returns the first seq after genesis whose history metadata window pruning kept.
    fn first_window_seq(&self) -> u64 {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.run_id,
            RunTable::HistoryRowMetadata,
        );
        let start = [prefix.as_slice(), &2_u64.to_be_bytes()].concat();
        let (_, end) = prefix_range(&prefix);
        let rows = self.rows_in(ColumnFamily::Flow, &start, &end);
        let (key, value) = rows.first().expect("history metadata after genesis");
        HistoryRowMetadata::decode(value.as_slice()).expect("metadata decodes");
        u64::from_be_bytes(
            key[prefix.len()..prefix.len() + 8]
                .try_into()
                .expect("eight seq bytes"),
        )
    }

    /// Returns the committed rows plus every record that offload deleted after its upload:
    /// records of acknowledged segments whose metadata stays, or that hold no events. Records
    /// that window pruning deleted stay deleted.
    fn full_history_view(&self) -> MemoryView {
        let mut rows = self.store.rows();
        for content in self.uploaded.values() {
            for row in &content.records {
                let row_key = (
                    ColumnFamily::History,
                    history_record_key(self.context.flow_hash, self.run_id, row.seq),
                );
                let metadata_key = (
                    ColumnFamily::Flow,
                    history_row_metadata_key(
                        self.context.flow_hash,
                        self.run_id,
                        row.seq,
                        RunTable::HistoryRecord,
                        0,
                    ),
                );
                let record = HistoryRecord::decode(row.row.as_slice()).expect("record decodes");
                let is_kept = rows.contains_key(&metadata_key) || record.events.is_empty();
                if is_kept && !rows.contains_key(&row_key) {
                    rows.insert(row_key, row.row.clone());
                }
            }
        }
        MemoryView::from_rows(rows)
    }

    /// Reads a page from the run's whole stored history, before offload deleted any of it.
    fn local_page(&self, start_event_id: i64, page_size: i32) -> GetHistoryEventsResponse {
        get_history_events(
            &self.full_history_view(),
            self.context.flow_hash,
            self.run_id,
            start_event_id,
            page_size,
            &[],
            0,
            32 << 20,
        )
        .expect("the local page reads")
    }

    fn reader(
        &self,
        config: &HistoryReadsConfig,
        metrics: Arc<NodeMetrics>,
    ) -> HistorySegmentReader {
        HistorySegmentReader::new(
            config,
            self.objects.clone(),
            metrics,
            BlockingWorkPlacement::BlockingThreadPool,
        )
    }

    fn harness(&self, reader: HistorySegmentReader) -> FlowCoordinationHarness {
        FlowCoordinationHarness::with_history_reader(
            &FlowCoordinationConfig::default(),
            self.store.clone(),
            Arc::new(reader),
        )
    }
}

impl OffloadedRun {
    /// Shrinks the run's window to `events`, so later pruning passes older segments.
    fn shrink_window(&mut self, events: i32) {
        let outcome = self.apply(Command::FlowConfigUpdate(FlowConfigUpdateCommand {
            request: Some(UpdateFlowConfigRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_config: Some(FlowConfig {
                    history_window_events: Some(events),
                    ..FlowConfig::default()
                }),
                ..UpdateFlowConfigRequest::default()
            }),
        }));
        assert!(
            matches!(outcome.reply.reply, Some(command_reply::Reply::Ok(_))),
            "{:?}",
            outcome.reply
        );
    }

    /// Writes and uploads until the segments that start at `first_seqs` are all EXPIRED.
    async fn write_until_expired(&mut self, first_seqs: &[u64]) {
        for _ in 0..100 {
            let segments = self.segments();
            let is_expired = first_seqs.iter().all(|first_seq| {
                segments.iter().any(|segment| {
                    segment.first_seq == *first_seq
                        && segment.state == HistorySegmentState::Expired as i32
                })
            });
            if is_expired {
                return;
            }
            self.write_rounds(1);
            self.upload_all().await;
        }
        panic!("segments {first_seqs:?} never expired");
    }
}

fn every_command_seals_settings() -> RunSettings {
    RunSettings {
        history_segment_target_bytes: 1,
        ..default_run_settings()
    }
}

fn history_request(start_event_id: i64, page_size: i32) -> GetHistoryEventsRequest {
    GetHistoryEventsRequest {
        flow_id: FLOW_ID.to_owned(),
        start_internal_event_id: start_event_id,
        estimate_page_size: page_size,
        ..GetHistoryEventsRequest::default()
    }
}

async fn wait_for_waiting_read(objects: &GatedObjectStore) {
    for _ in 0..WAIT_ROUNDS {
        if objects.waiting_reads.load(Ordering::SeqCst) > 0 {
            return;
        }
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    panic!("no segment read reached object storage");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn pages_from_uploaded_segments_match_local_pages() {
    let mut run = OffloadedRun::start(every_command_seals_settings());
    run.write_rounds(12);
    run.upload_all().await;
    run.write_rounds(3);
    let expected = run.local_page(0, 0);
    let middle = run.local_page(8, 4);
    run.delete_uploaded_local_records();
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.harness(run.reader(&HistoryReadsConfig::default(), metrics.clone()));
    let page = harness
        .registry
        .get_history_events(TENANT, history_request(0, 0))
        .await
        .expect("the page reads");
    assert_eq!(page, expected);
    let page = harness
        .registry
        .get_history_events(TENANT, history_request(8, 4))
        .await
        .expect("the page reads");
    assert_eq!(page, middle);
    assert!(
        metrics
            .history_reads
            .count(HistoryReadKind::Page, HistoryReadSource::ObjectStore)
            > 0
    );
    assert_eq!(
        metrics
            .history_reads
            .count(HistoryReadKind::Page, HistoryReadSource::Local),
        0
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_slow_segment_read_holds_no_committed_view_and_other_reads_proceed() {
    let mut run = OffloadedRun::start(every_command_seals_settings());
    run.write_rounds(6);
    run.upload_all().await;
    let expected = run.local_page(0, 3);
    run.delete_uploaded_local_records();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    run.objects.close_gate();
    let registry = harness.registry.clone();
    let slow_read = tokio::spawn(async move {
        registry
            .get_history_events(TENANT, history_request(0, 3))
            .await
    });
    wait_for_waiting_read(&run.objects).await;
    assert_eq!(
        harness.committer.live_views.load(Ordering::SeqCst),
        0,
        "no committed view is held while the segment read waits"
    );
    let summary = tokio::time::timeout(
        Duration::from_secs(5),
        harness.registry.get_flow_summary(
            TENANT,
            GetFlowSummaryRequest {
                flow_id: FLOW_ID.to_owned(),
                ..GetFlowSummaryRequest::default()
            },
        ),
    )
    .await
    .expect("a committed read finishes while the segment read waits");
    summary.expect("the summary reads");
    run.objects.open_gate();
    let page = slow_read
        .await
        .expect("the read task finishes")
        .expect("the page reads");
    assert_eq!(page, expected);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_run_purged_while_its_segment_is_read_reads_as_not_found() {
    let mut run = OffloadedRun::start(every_command_seals_settings());
    run.write_rounds(4);
    run.upload_all().await;
    run.delete_uploaded_local_records();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    run.objects.close_gate();
    let registry = harness.registry.clone();
    let read = tokio::spawn(async move {
        registry
            .get_history_events(TENANT, history_request(0, 3))
            .await
    });
    wait_for_waiting_read(&run.objects).await;
    // The purge marks the run purging, then deletes its objects.
    let mut record = run.run_record();
    record.is_purging = true;
    run.store.put(
        ColumnFamily::Flow,
        run.run_record_key(),
        record.encode_to_vec(),
    );
    for segment in run.uploaded_segments() {
        run.objects
            .inner
            .delete(&run.object_key(&segment))
            .await
            .expect("the object deletes");
    }
    run.objects.open_gate();
    let error = read
        .await
        .expect("the read task finishes")
        .expect_err("a purged run has no history");
    assert_eq!(error.grpc_code(), grpc_code::NOT_FOUND, "{error}");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_segment_pruned_while_it_is_read_reads_as_pruned() {
    let mut run = OffloadedRun::start(RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..every_command_seals_settings()
    });
    for _ in 0..8 {
        run.write_rounds(1);
        run.upload_all().await;
    }
    let first_window_seq = run.first_window_seq();
    let target = run
        .uploaded_segments()
        .into_iter()
        .find(|segment| {
            segment.first_seq >= first_window_seq && segment.first_event_id <= segment.last_event_id
        })
        .expect("an uploaded segment inside the window");
    run.delete_uploaded_local_records();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    run.objects.close_gate();
    let registry = harness.registry.clone();
    let start_event_id = target.first_event_id;
    let read = tokio::spawn(async move {
        registry
            .get_history_events(TENANT, history_request(start_event_id, 2))
            .await
    });
    wait_for_waiting_read(&run.objects).await;
    for _ in 0..8 {
        run.write_rounds(1);
        run.upload_all().await;
    }
    assert!(
        run.first_window_seq() > target.last_seq,
        "the window moved past the segment being read"
    );
    run.objects
        .inner
        .delete(&run.object_key(&target))
        .await
        .expect("the object deletes");
    run.delete_uploaded_local_records();
    run.objects.open_gate();
    let page = read
        .await
        .expect("the read task finishes")
        .expect("a pruned segment reads as pruned, not as an error");
    let pruned = page
        .pruned_event_id_range
        .expect("the window pruned events");
    assert!(pruned.end_event_id >= target.last_event_id);
    assert!(!page.events.is_empty());
    for event in &page.events {
        assert!(event.event_id > pruned.end_event_id, "{event:?} is pruned");
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn expired_segments_are_never_read_even_from_a_warm_read_cache() {
    let mut run = OffloadedRun::start(RunSettings {
        history_window_events: 1000,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        ..every_command_seals_settings()
    });
    for _ in 0..8 {
        run.write_rounds(1);
        run.upload_all().await;
    }
    let directory = tempfile::tempdir().expect("a cache directory");
    let harness = run.harness(run.reader(
        &cache_config(directory.path(), 1 << 30),
        Arc::new(NodeMetrics::unregistered()),
    ));
    let warm = harness
        .registry
        .get_history_events(TENANT, history_request(0, 1000))
        .await
        .expect("the whole history reads");
    let first_uploaded: Vec<HistorySegmentRecord> = run
        .uploaded_segments()
        .into_iter()
        .skip(1)
        .take(3)
        .collect();
    for segment in &first_uploaded {
        assert!(
            warm.events
                .iter()
                .any(|event| event.event_id == segment.first_event_id),
            "the warm read returned the events of {segment:?}"
        );
    }

    // A smaller window expires those segments; with deletion off their objects stay.
    run.shrink_window(2);
    let first_seqs: Vec<u64> = first_uploaded
        .iter()
        .map(|segment| segment.first_seq)
        .collect();
    run.write_until_expired(&first_seqs).await;
    for segment in &first_uploaded {
        assert!(
            run.objects
                .inner
                .get(&run.object_key(segment))
                .await
                .expect("the store reads")
                .is_some(),
            "the object of {segment:?} stays"
        );
    }
    let page = harness
        .registry
        .get_history_events(TENANT, history_request(0, 1000))
        .await
        .expect("the history reads after the expiry");
    for event in &page.events {
        assert!(
            event.event_id == 1
                || first_uploaded.iter().all(|segment| {
                    !(segment.first_event_id..=segment.last_event_id).contains(&event.event_id)
                }),
            "{event:?} belongs to an expired segment"
        );
    }
    let pruned = page
        .pruned_event_id_range
        .expect("the window pruned the expired events");
    let last_expired_event_id = first_uploaded
        .iter()
        .map(|segment| segment.last_event_id)
        .max()
        .expect("expired segments");
    assert!(pruned.end_event_id >= last_expired_event_id, "{pruned:?}");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn an_uploaded_segment_missing_from_object_storage_is_unavailable() {
    let mut run = OffloadedRun::start(every_command_seals_settings());
    run.write_rounds(4);
    run.upload_all().await;
    run.delete_uploaded_local_records();
    let first = run.uploaded_segments()[0].clone();
    run.objects
        .inner
        .delete(&run.object_key(&first))
        .await
        .expect("the object deletes");
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    let error = harness
        .registry
        .get_history_events(TENANT, history_request(0, 3))
        .await
        .expect_err("a missing object fails the read");
    assert_eq!(error.grpc_code(), grpc_code::UNAVAILABLE, "{error}");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_run_without_uploads_reads_only_local_rows() {
    let mut run = OffloadedRun::start(RunSettings {
        history_segment_target_bytes: 64 << 20,
        ..default_run_settings()
    });
    run.write_rounds(5);
    let expected = run.local_page(0, 0);
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.harness(run.reader(&HistoryReadsConfig::default(), metrics.clone()));
    let page = harness
        .registry
        .get_history_events(TENANT, history_request(0, 0))
        .await
        .expect("the page reads");
    assert_eq!(page, expected);
    assert_eq!(run.objects.history_reads(), 0);
    assert_eq!(
        metrics
            .history_reads
            .count(HistoryReadKind::Page, HistoryReadSource::Local),
        1
    );
}

fn cache_config(directory: &Path, max_bytes: u64) -> HistoryReadsConfig {
    HistoryReadsConfig {
        read_cache_directory: Some(directory.to_path_buf()),
        read_cache_max_bytes: max_bytes,
        object_read_concurrency: 4,
        ..HistoryReadsConfig::default()
    }
}

async fn read_segment(
    reader: &HistorySegmentReader,
    run: &OffloadedRun,
    segment: &HistorySegmentRecord,
) -> usize {
    reader
        .read_records(SegmentLocation {
            tenant_id: TENANT,
            flow_hash: run.context.flow_hash,
            run_id: run.run_id,
            segment,
        })
        .await
        .expect("the segment reads")
        .expect("the object exists")
        .len()
}

async fn uploaded_run(rounds: usize) -> OffloadedRun {
    let mut run = OffloadedRun::start(every_command_seals_settings());
    run.write_rounds(rounds);
    run.upload_all().await;
    run
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn segment_reads_hit_the_disk_cache_and_reuse_it_after_a_restart() {
    let run = uploaded_run(3).await;
    let directory = tempfile::tempdir().expect("a cache directory");
    let segment = run.uploaded_segments()[1].clone();
    let metrics = Arc::new(NodeMetrics::unregistered());
    let reader = run.reader(&cache_config(directory.path(), 1 << 30), metrics.clone());
    let records = read_segment(&reader, &run, &segment).await;
    assert!(records > 0);
    assert_eq!(
        run.objects.history_reads(),
        2,
        "a header read and a records read"
    );
    assert_eq!(read_segment(&reader, &run, &segment).await, records);
    assert_eq!(run.objects.history_reads(), 2, "the second read is cached");
    assert_eq!(
        metrics
            .history_reads
            .count(HistoryReadKind::Page, HistoryReadSource::Cache),
        1,
        "the header came from memory and the records from disk"
    );
    drop(reader);

    let restarted_metrics = Arc::new(NodeMetrics::unregistered());
    let restarted = run.reader(
        &cache_config(directory.path(), 1 << 30),
        restarted_metrics.clone(),
    );
    assert_eq!(read_segment(&restarted, &run, &segment).await, records);
    assert_eq!(
        run.objects.history_reads(),
        2,
        "a restarted reader serves the segment from its disk cache"
    );
    assert_eq!(
        restarted_metrics
            .history_reads
            .count(HistoryReadKind::Page, HistoryReadSource::Cache),
        2
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_zero_byte_read_cache_reads_object_storage_every_time() {
    let run = uploaded_run(3).await;
    let directory = tempfile::tempdir().expect("a cache directory");
    let segment = run.uploaded_segments()[1].clone();
    let reader = run.reader(
        &cache_config(directory.path(), 0),
        Arc::new(NodeMetrics::unregistered()),
    );
    read_segment(&reader, &run, &segment).await;
    read_segment(&reader, &run, &segment).await;
    assert_eq!(
        run.objects.history_reads(),
        3,
        "the header stays in memory; the records are read again"
    );
    assert_eq!(
        std::fs::read_dir(directory.path())
            .expect("the directory lists")
            .count(),
        0,
        "a disabled cache writes nothing"
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn the_disk_read_cache_stays_within_its_byte_limit() {
    let run = uploaded_run(40).await;
    let directory = tempfile::tempdir().expect("a cache directory");
    let max_bytes = 8 * 1024;
    let reader = run.reader(
        &cache_config(directory.path(), max_bytes),
        Arc::new(NodeMetrics::unregistered()),
    );
    let segments = run.uploaded_segments();
    for segment in &segments {
        read_segment(&reader, &run, segment).await;
    }
    for segment in &segments {
        read_segment(&reader, &run, segment).await;
    }
    let cached_bytes = directory_bytes(directory.path());
    assert!(cached_bytes > 0, "the cache keeps some segments");
    assert!(
        cached_bytes <= max_bytes,
        "the cache holds {cached_bytes} bytes, above its {max_bytes}-byte limit"
    );
}

fn directory_bytes(directory: &Path) -> u64 {
    let mut total = 0;
    let mut pending = vec![directory.to_path_buf()];
    while let Some(path) = pending.pop() {
        for entry in std::fs::read_dir(&path).expect("the directory lists") {
            let entry = entry.expect("the entry reads");
            let metadata = entry.metadata().expect("the metadata reads");
            if metadata.is_dir() {
                pending.push(entry.path());
            } else {
                total += metadata.len();
            }
        }
    }
    total
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn one_event_segments_page_to_the_end_at_every_page_size() {
    let mut run = OffloadedRun::start(every_command_seals_settings());
    run.write_rounds(1_100);
    run.upload_all().await;
    assert!(run.uploaded_segments().len() > 1_000);
    let expected: Vec<i64> = run
        .local_page(0, 1_000)
        .events
        .iter()
        .chain(run.local_page(1_001, 1_000).events.iter())
        .map(|event| event.event_id)
        .collect();
    run.delete_uploaded_local_records();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    for page_size in [100, 1_000] {
        let mut event_ids = Vec::new();
        let mut token = Vec::new();
        for _ in 0..1_000 {
            let page = harness
                .registry
                .get_history_events(
                    TENANT,
                    GetHistoryEventsRequest {
                        next_page_token: token,
                        ..history_request(0, page_size)
                    },
                )
                .await
                .expect("every page read makes progress");
            assert!(page.events.len() <= usize::try_from(page_size).expect("small size"));
            event_ids.extend(page.events.iter().map(|event| event.event_id));
            token = page.next_page_token;
            if token.is_empty() {
                break;
            }
        }
        assert!(
            token.is_empty(),
            "paging with size {page_size} reached the end"
        );
        assert_eq!(event_ids, expected, "page size {page_size}");
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn the_last_round_returns_a_shorter_page_that_paging_continues() {
    let mut run = OffloadedRun::start(every_command_seals_settings());
    run.write_rounds(700);
    run.upload_all().await;
    let expected: Vec<i64> = run
        .local_page(0, 1_000)
        .events
        .iter()
        .map(|event| event.event_id)
        .collect();
    run.delete_uploaded_local_records();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig {
            max_page_rounds: 2,
            ..HistoryReadsConfig::default()
        },
        Arc::new(NodeMetrics::unregistered()),
    ));
    let mut event_ids = Vec::new();
    let mut token = Vec::new();
    let mut page_lengths = Vec::new();
    for _ in 0..100 {
        let page = harness
            .registry
            .get_history_events(
                TENANT,
                GetHistoryEventsRequest {
                    next_page_token: token,
                    ..history_request(0, 1_000)
                },
            )
            .await
            .expect("the last round returns a page");
        page_lengths.push(page.events.len());
        event_ids.extend(page.events.iter().map(|event| event.event_id));
        token = page.next_page_token;
        if token.is_empty() {
            break;
        }
    }
    assert!(token.is_empty(), "paging reached the end");
    assert!(
        page_lengths[0] < expected.len(),
        "two rounds cannot load the whole page, so it came back shorter: {page_lengths:?}"
    );
    assert_eq!(event_ids, expected);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_read_cache_that_cannot_be_created_disables_itself() {
    let run = uploaded_run(3).await;
    let parent = tempfile::tempdir().expect("a temporary directory");
    let occupied = parent.path().join("not-a-directory");
    std::fs::write(&occupied, b"a file where the cache directory would go")
        .expect("the file writes");
    let segment = run.uploaded_segments()[1].clone();
    let metrics = Arc::new(NodeMetrics::unregistered());
    let reader = run.reader(&cache_config(&occupied, 1 << 30), metrics.clone());
    let records = read_segment(&reader, &run, &segment).await;
    assert!(records > 0, "reads work without the cache");
    assert_eq!(read_segment(&reader, &run, &segment).await, records);
    assert_eq!(
        run.objects.history_reads(),
        3,
        "the header stays in memory; the records come from object storage each time"
    );
    assert_eq!(
        metrics
            .history_reads
            .count(HistoryReadKind::Page, HistoryReadSource::Cache),
        0
    );
    assert!(occupied.is_file(), "the reader leaves the path alone");
}

mod load_blob_read_tests;
mod value_revival_tests;
