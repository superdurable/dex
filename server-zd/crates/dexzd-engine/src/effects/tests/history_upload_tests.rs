// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History segment uploads with the real apply state machine: seq-ordered acknowledgments,
//! repeated uploads after a restart or restore, replaced objects, object storage outages,
//! retention, and purges.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicU8, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use bytes::Bytes;
use dexzd_apply::FlowStateReader;
use dexzd_apply::read_model::read_history_segment_content;
use dexzd_history_segment::{
    HISTORY_SEGMENT_OBJECT_KEY_PREFIX, HistorySegmentContent, decode_history_segment,
    encode_history_segment, history_run_object_key_prefix, history_segment_object_key,
};
use dexzd_keyspace::row_keys::{
    history_segment_key, history_upload_pending_index_key, prefix_range,
};
use dexzd_keyspace::{ColumnFamily, PartitionId, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_metrics::HistoryUploadOutcome;
use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, ObjectStore, ObjectStoreOperation,
    WriteCondition,
};
use dexzd_protocol::dex::{AttributeWrite, SetAttributesRequest, StopFlowRequest, StopType};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, FlowStopCommand, HistorySegmentRecord, HistorySegmentState,
    RunSettings, SetHistoryOffloadModeCommand, command_reply, flow_command,
};
use dexzd_storage::InMemoryLocalStore;
use prost::Message;
use tokio::sync::OwnedSemaphorePermit;

use super::outbox_and_purge_tests::mark_purging;
use super::{
    DurableState, EffectHarness, RecordedCommand, SECOND_MICROS, is_error_reply, ok_reply,
    settings, start_request, string_value,
};
use crate::effects::{EffectsConfig, HistoryUploadLimits};
use crate::flow_coordination::CommitUrgency;

/// Rounds of 100 ms that a wait allows; paused time makes them instant.
const WAIT_ROUNDS: usize = 3_000;
const SHORT_RETENTION: Duration = Duration::from_secs(60);
const RESCAN_INTERVAL: Duration = Duration::from_secs(1);

/// What the store does with history segment writes and with batch deletes; every other call
/// proceeds.
#[derive(Default)]
struct HistoryWriteFaults {
    mode: AtomicU8,
    delete_mode: AtomicU8,
}

impl HistoryWriteFaults {
    const PROCEED: u8 = 0;
    const FAIL: u8 = 1;
    const APPLY_THEN_OUTCOME_UNKNOWN_ONCE: u8 = 2;
    const DELAY: u8 = 3;
    const FAIL_PERMANENTLY: u8 = 4;
    /// Longer than every wait of the tests, as a write stuck in a slow network.
    const DELAY_FOR_TEN_MINUTES: u8 = 5;

    fn set(&self, mode: u8) {
        self.mode.store(mode, Ordering::SeqCst);
    }

    fn set_deletes(&self, mode: u8) {
        self.delete_mode.store(mode, Ordering::SeqCst);
    }
}

impl FaultInjector for HistoryWriteFaults {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        let mode = match operation {
            ObjectStoreOperation::Put if key.starts_with(HISTORY_SEGMENT_OBJECT_KEY_PREFIX) => {
                self.mode.load(Ordering::SeqCst)
            }
            ObjectStoreOperation::Delete => self.delete_mode.load(Ordering::SeqCst),
            _ => Self::PROCEED,
        };
        match mode {
            Self::FAIL => FaultAction::FailBeforeEffect,
            Self::APPLY_THEN_OUTCOME_UNKNOWN_ONCE => {
                self.set(Self::PROCEED);
                FaultAction::ApplyThenOutcomeUnknown
            }
            Self::DELAY => FaultAction::Delay(Duration::from_secs(10)),
            Self::FAIL_PERMANENTLY => FaultAction::FailPermanently,
            Self::DELAY_FOR_TEN_MINUTES => FaultAction::Delay(Duration::from_secs(600)),
            _ => FaultAction::Proceed,
        }
    }
}

fn durable_with_faults(faults: Arc<HistoryWriteFaults>) -> DurableState {
    DurableState {
        objects: Arc::new(InMemoryObjectStore::with_injector(faults)),
        ..DurableState::new()
    }
}

fn rescan_config() -> EffectsConfig {
    EffectsConfig {
        history_upload_rescan_interval: RESCAN_INTERVAL,
        ..EffectsConfig::default()
    }
}

/// Commits the enabled offload mode to every partition, as a node with `history.offloadEnabled`
/// does.
pub(super) fn enable_history_offload(harness: &EffectHarness) {
    set_history_offload_mode(harness, true);
}

/// Commits the offload mode to every partition.
pub(super) fn set_history_offload_mode(harness: &EffectHarness, is_enabled: bool) {
    for (partition, flow_id) in harness
        .hasher
        .partition_command_flow_ids()
        .into_iter()
        .enumerate()
    {
        let partition = u32::try_from(partition).expect("few partitions");
        let reply = harness
            .flow_effect_operations
            .commit(
                &EffectHarness::flow(&flow_id),
                FlowCommand {
                    flow_id: flow_id.clone(),
                    command: Some(flow_command::Command::SetHistoryOffloadMode(
                        SetHistoryOffloadModeCommand {
                            enabled: is_enabled,
                            partition,
                        },
                    )),
                    ..FlowCommand::default()
                },
                CommitUrgency::Prompt,
            )
            .expect("the mode commits");
        assert!(
            !is_error_reply(&reply),
            "setting the mode failed: {reply:?}"
        );
    }
}

/// Takes every upload slot, so uploads wait until the permits drop.
async fn hold_upload_slots(harness: &EffectHarness) -> OwnedSemaphorePermit {
    harness
        .effect_context()
        .history_upload_limiter
        .hold_every_slot_for_test()
        .await
}

/// Terminates the run; the closing command seals its open segment.
pub(super) fn close_run(harness: &EffectHarness, flow_id: &str, run_id: RunId) {
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: flow_id.to_owned(),
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow(flow_id),
            command,
            CommitUrgency::Prompt,
        )
        .expect("stop commits");
    assert!(!is_error_reply(&reply), "stop failed: {reply:?}");
}

pub(super) fn short_retention_settings() -> RunSettings {
    RunSettings {
        retention_after_close_micros: 60 * SECOND_MICROS,
        ..settings()
    }
}

/// Returns the run's committed history segments in seq order.
pub(super) fn segments(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
) -> Vec<HistorySegmentRecord> {
    let run = harness.run_location(flow_id, run_id);
    let prefix = run_table_prefix(run.flow_hash, run.run_id, RunTable::HistorySegment);
    let (start, end) = prefix_range(&prefix);
    harness
        .flow_effect_operations
        .snapshot()
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .expect("memory reads never fail")
        .into_iter()
        .map(|(_, bytes)| HistorySegmentRecord::decode(bytes.as_slice()).expect("decodes"))
        .collect()
}

fn is_upload_pending(harness: &EffectHarness, flow_id: &str, run_id: RunId) -> bool {
    let run = harness.run_location(flow_id, run_id);
    let partition: PartitionId = harness.partition_of(flow_id);
    harness
        .flow_effect_operations
        .snapshot()
        .get(
            ColumnFamily::PartitionIndex,
            &history_upload_pending_index_key(partition, run.flow_hash, run.run_id),
        )
        .expect("memory reads never fail")
        .is_some()
}

fn is_fully_uploaded(harness: &EffectHarness, flow_id: &str, run_id: RunId) -> bool {
    let segments = segments(harness, flow_id, run_id);
    !segments.is_empty()
        && segments
            .iter()
            .all(|segment| segment.state == HistorySegmentState::Uploaded as i32)
        && !is_upload_pending(harness, flow_id, run_id)
}

pub(super) fn object_key(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    segment: &HistorySegmentRecord,
) -> String {
    let run = harness.run_location(flow_id, run_id);
    history_segment_object_key(
        TenantId::DEFAULT,
        run.flow_hash,
        run_id,
        segment.first_seq,
        segment.last_seq,
    )
}

/// Returns the content that committed state assigns to the segment.
fn committed_content(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    first_seq: u64,
) -> HistorySegmentContent {
    let run = harness.run_location(flow_id, run_id);
    read_history_segment_content(
        &harness.flow_effect_operations.snapshot(),
        run.flow_hash,
        run_id,
        first_seq,
    )
    .expect("the segment content reads")
    .expect("the segment exists")
}

/// Contents of the run's SEALED segments by first seq, read before their upload lets offload
/// delete the local rows.
fn sealed_contents(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
) -> BTreeMap<u64, HistorySegmentContent> {
    segments(harness, flow_id, run_id)
        .into_iter()
        .filter(|segment| segment.state == HistorySegmentState::Sealed as i32)
        .map(|segment| {
            let content = committed_content(harness, flow_id, run_id, segment.first_seq);
            (segment.first_seq, content)
        })
        .collect()
}

pub(super) async fn stored_object(harness: &EffectHarness, key: &str) -> Option<Bytes> {
    harness
        .durable
        .objects
        .get(key)
        .await
        .expect("the in-memory store reads")
        .map(|content| content.bytes)
}

pub(super) async fn object_keys_with_prefix(harness: &EffectHarness, prefix: &str) -> Vec<String> {
    harness
        .durable
        .objects
        .list(prefix, None, 1000)
        .await
        .expect("the in-memory store lists")
        .objects
        .into_iter()
        .map(|object| object.key)
        .collect()
}

fn acknowledgments(harness: &EffectHarness) -> Vec<RecordedCommand> {
    harness.flow_effect_operations.commands_matching(|command| {
        matches!(command, flow_command::Command::HistorySegmentUploaded(_))
    })
}

fn acknowledged_first_seq(recorded: &RecordedCommand) -> u64 {
    match &recorded.command.command {
        Some(flow_command::Command::HistorySegmentUploaded(acknowledgment)) => {
            acknowledgment.first_seq
        }
        other => panic!("not an acknowledgment: {other:?}"),
    }
}

pub(super) fn run_purges(harness: &EffectHarness) -> Vec<RecordedCommand> {
    harness
        .flow_effect_operations
        .commands_matching(|command| matches!(command, flow_command::Command::RunPurge(_)))
}

/// Waits until `condition` holds, letting tasks and paused time advance.
pub(super) async fn wait_until(harness: &EffectHarness, what: &str, condition: impl Fn() -> bool) {
    for _ in 0..WAIT_ROUNDS {
        harness.settle().await;
        if condition() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    panic!("timed out waiting until {what}");
}

async fn wait_until_uploaded(harness: &EffectHarness, flow_id: &str, run_id: RunId) {
    wait_until(harness, "every sealed segment is acknowledged", || {
        is_fully_uploaded(harness, flow_id, run_id)
    })
    .await;
}

/// Checks the run's uploaded chain: contiguous segments, U at the last one, and one object per
/// segment whose content and checksum match `sealed`, the committed content when it sealed.
async fn assert_uploaded_chain(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    sealed: &BTreeMap<u64, HistorySegmentContent>,
) {
    let segments = segments(harness, flow_id, run_id);
    for pair in segments.windows(2) {
        assert_eq!(pair[1].first_seq, pair[0].last_seq + 1, "{segments:?}");
    }
    let last = segments.last().expect("the run sealed a segment");
    assert_eq!(
        harness
            .run_record(flow_id, run_id)
            .history_uploaded_through_seq,
        last.last_seq
    );
    let run = harness.run_location(flow_id, run_id);
    let run_prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    let mut expected_keys = Vec::new();
    for segment in &segments {
        let key = object_key(harness, flow_id, run_id, segment);
        let object = stored_object(harness, &key)
            .await
            .unwrap_or_else(|| panic!("{key} exists"));
        let content = sealed
            .get(&segment.first_seq)
            .unwrap_or_else(|| panic!("segment {segment:?} was read when it sealed"))
            .clone();
        assert_eq!(
            decode_history_segment(&object).expect("the object decodes"),
            content
        );
        let encoded = encode_history_segment(&content).expect("committed content encodes");
        assert_eq!(segment.content_checksum, encoded.content_checksum.to_vec());
        assert_eq!(segment.object_size_bytes, object.len() as u64);
        expected_keys.push(key);
    }
    assert_eq!(
        object_keys_with_prefix(harness, &run_prefix).await,
        expected_keys,
        "one object per segment, without gaps or extra objects"
    );
}

#[tokio::test(start_paused = true)]
async fn sealed_segments_upload_and_are_acknowledged_in_seq_order() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(
        start_request("order-1"),
        RunSettings {
            history_segment_target_bytes: 1,
            ..settings()
        },
    );
    close_run(&harness, "order-1", run_id);
    let sealed = sealed_contents(&harness, "order-1", run_id);

    wait_until_uploaded(&harness, "order-1", run_id).await;
    assert_uploaded_chain(&harness, "order-1", run_id, &sealed).await;
    let segments = segments(&harness, "order-1", run_id);
    assert!(segments.len() >= 2, "every command sealed: {segments:?}");
    let acknowledgments = acknowledgments(&harness);
    assert_eq!(
        acknowledgments
            .iter()
            .map(acknowledged_first_seq)
            .collect::<Vec<_>>(),
        segments
            .iter()
            .map(|segment| segment.first_seq)
            .collect::<Vec<_>>(),
        "one acknowledgment per segment, oldest first"
    );
    for acknowledgment in &acknowledgments {
        assert_eq!(acknowledgment.reply, ok_reply());
        assert_eq!(acknowledgment.urgency, CommitUrgency::Batched);
        assert_eq!(acknowledgment.command.expected_run_attempt, 0);
    }
    let uploads = &harness.metrics.history_uploads;
    assert_eq!(
        uploads.count(HistoryUploadOutcome::Created),
        segments.len() as u64
    );
    assert_eq!(uploads.count(HistoryUploadOutcome::Failed), 0);
    assert_eq!(
        harness.metrics.history_upload_bytes.get(),
        segments
            .iter()
            .map(|segment| segment.object_size_bytes)
            .sum::<u64>()
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_restarted_owner_reuploads_an_unacknowledged_segment_and_acknowledges_it() {
    let durable = DurableState::new();
    let first = EffectHarness::with_config(durable.clone(), EffectsConfig::default());
    first.rebuild_all_partitions().await;
    enable_history_offload(&first);
    let run_id = first.start_flow(start_request("order-1"), settings());
    close_run(&first, "order-1", run_id);
    let sealed = sealed_contents(&first, "order-1", run_id);
    // No later proposal commits, so the owner stops between the upload and its acknowledgment.
    first
        .flow_effect_operations
        .unavailable_proposals
        .store(usize::MAX, Ordering::SeqCst);
    let segment = segments(&first, "order-1", run_id)
        .pop()
        .expect("the close sealed a segment");
    let key = object_key(&first, "order-1", run_id, &segment);
    wait_until(&first, "the segment object exists", || {
        durable.objects.call_count(ObjectStoreOperation::Put, &key) == 1
    })
    .await;
    assert!(stored_object(&first, &key).await.is_some());
    assert!(is_upload_pending(&first, "order-1", run_id));
    assert_eq!(
        first
            .metrics
            .history_uploads
            .count(HistoryUploadOutcome::Created),
        0,
        "an upload counts once its acknowledgment commits"
    );
    first.shutdown().await;

    let second = EffectHarness::with_config(durable.clone(), EffectsConfig::default());
    second.rebuild_all_partitions().await;
    wait_until_uploaded(&second, "order-1", run_id).await;

    assert_uploaded_chain(&second, "order-1", run_id, &sealed).await;
    let uploads = &second.metrics.history_uploads;
    assert_eq!(uploads.count(HistoryUploadOutcome::AlreadyPresent), 1);
    assert_eq!(uploads.count(HistoryUploadOutcome::Created), 0);
    assert_eq!(second.metrics.history_upload_checksum_mismatches.get(), 0);
    assert_eq!(
        durable.objects.call_count(ObjectStoreOperation::Put, &key),
        2,
        "the new owner's CreateOnly write found the object"
    );
    let acknowledgments = acknowledgments(&second);
    assert_eq!(acknowledgments.len(), 1);
    assert_eq!(acknowledgments[0].reply, ok_reply());
    second.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_unknown_write_outcome_is_retried_and_finds_the_written_object() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::APPLY_THEN_OUTCOME_UNKNOWN_ONCE);
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(start_request("order-1"), settings());
    close_run(&harness, "order-1", run_id);
    let sealed = sealed_contents(&harness, "order-1", run_id);

    wait_until_uploaded(&harness, "order-1", run_id).await;
    assert_uploaded_chain(&harness, "order-1", run_id, &sealed).await;
    let uploads = &harness.metrics.history_uploads;
    assert_eq!(uploads.count(HistoryUploadOutcome::Failed), 1);
    assert_eq!(uploads.count(HistoryUploadOutcome::AlreadyPresent), 1);
    assert_eq!(acknowledgments(&harness).len(), 1);
    harness.shutdown().await;
}

/// Seeds the segment's key with `seeded_object` before the first upload, then checks that the
/// upload replaced it with committed content and acknowledged it.
async fn assert_seeded_object_is_replaced(
    seeded_object: impl FnOnce(&HistorySegmentContent) -> Vec<u8>,
) {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let upload_slots = hold_upload_slots(&harness).await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    close_run(&harness, "order-1", run_id);
    let sealed = sealed_contents(&harness, "order-1", run_id);
    let segment = segments(&harness, "order-1", run_id)
        .pop()
        .expect("the close sealed a segment");
    let content = committed_content(&harness, "order-1", run_id, segment.first_seq);
    let key = object_key(&harness, "order-1", run_id, &segment);
    harness
        .durable
        .objects
        .put(
            &key,
            Bytes::from(seeded_object(&content)),
            WriteCondition::Unconditional,
        )
        .await
        .expect("the seeded object is written");
    drop(upload_slots);

    wait_until_uploaded(&harness, "order-1", run_id).await;
    assert_uploaded_chain(&harness, "order-1", run_id, &sealed).await;
    let uploads = &harness.metrics.history_uploads;
    assert_eq!(uploads.count(HistoryUploadOutcome::Replaced), 1);
    assert_eq!(uploads.count(HistoryUploadOutcome::Created), 0);
    assert_eq!(harness.metrics.history_upload_checksum_mismatches.get(), 1);
    assert_eq!(acknowledgments(&harness).len(), 1);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_existing_object_with_other_content_is_replaced_then_acknowledged() {
    assert_seeded_object_is_replaced(|content| {
        let mut stale = content.clone();
        let record = stale.records.first_mut().expect("the segment has records");
        let last_byte = record.row.last_mut().expect("a record row is not empty");
        *last_byte ^= 0xff;
        encode_history_segment(&stale)
            .expect("the stale content encodes")
            .bytes
    })
    .await;
}

#[tokio::test(start_paused = true)]
async fn an_existing_object_that_does_not_decode_is_replaced_then_acknowledged() {
    assert_seeded_object_is_replaced(|_| b"not a history segment".to_vec()).await;
}

#[tokio::test(start_paused = true)]
async fn an_object_store_outage_grows_the_backlog_without_blocking_commands_then_drains() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::FAIL);
    let harness =
        EffectHarness::with_config(durable_with_faults(Arc::clone(&faults)), rescan_config());
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let flow_ids = ["order-1", "order-2", "order-3"];
    let mut run_ids = Vec::new();
    let mut sealed = Vec::new();
    for flow_id in flow_ids {
        let run_id = harness.start_flow(start_request(flow_id), settings());
        close_run(&harness, flow_id, run_id);
        run_ids.push(run_id);
        sealed.push(sealed_contents(&harness, flow_id, run_id));
    }
    tokio::time::sleep(RESCAN_INTERVAL * 3).await;
    harness.settle().await;

    let backlog = &harness.metrics.history_upload_backlog;
    assert_eq!(backlog.segments(), 3);
    assert!(backlog.bytes() > 0);
    let oldest_sealed_at = flow_ids
        .iter()
        .zip(&run_ids)
        .flat_map(|(flow_id, run_id)| segments(&harness, flow_id, *run_id))
        .map(|segment| segment.sealed_at_micros)
        .min();
    assert_eq!(backlog.oldest_sealed_at_micros(), oldest_sealed_at);
    assert!(
        harness
            .metrics
            .history_uploads
            .count(HistoryUploadOutcome::Failed)
            >= 3
    );
    assert!(
        object_keys_with_prefix(&harness, HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
            .await
            .is_empty()
    );
    assert!(acknowledgments(&harness).is_empty());

    // Commands keep committing during the outage, and the backlog keeps growing.
    let late_run = harness.start_flow(start_request("order-late"), settings());
    close_run(&harness, "order-late", late_run);
    let bytes_before = backlog.bytes();
    tokio::time::sleep(RESCAN_INTERVAL * 3).await;
    harness.settle().await;
    assert_eq!(backlog.segments(), 4);
    assert!(backlog.bytes() > bytes_before);

    faults.set(HistoryWriteFaults::PROCEED);
    for ((flow_id, run_id), sealed) in flow_ids.iter().zip(&run_ids).zip(&sealed) {
        wait_until_uploaded(&harness, flow_id, *run_id).await;
        assert_uploaded_chain(&harness, flow_id, *run_id, sealed).await;
    }
    wait_until_uploaded(&harness, "order-late", late_run).await;
    wait_until(&harness, "the backlog gauges drain", || {
        backlog.segments() == 0
    })
    .await;
    assert_eq!(backlog.bytes(), 0);
    assert_eq!(backlog.oldest_sealed_at_micros(), None);
    assert_eq!(acknowledgments(&harness).len(), 4);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn backlog_gauges_rebuild_during_an_outage_and_track_recovery() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::FAIL_PERMANENTLY);
    let durable = durable_with_faults(Arc::clone(&faults));
    let first = EffectHarness::with_config(durable.clone(), rescan_config());
    first.rebuild_all_partitions().await;
    enable_history_offload(&first);
    let flow_id = "backlog-restart";
    let run_id = first.start_flow(
        start_request(flow_id),
        RunSettings {
            history_segment_target_bytes: 1,
            ..settings()
        },
    );
    for round in 0..3 {
        set_long_attribute(&first, flow_id, run_id, "notes", &format!("v{round}"));
    }
    close_run(&first, flow_id, run_id);
    tokio::time::sleep(RESCAN_INTERVAL).await;
    first.settle().await;
    let expected_segments = segments(&first, flow_id, run_id)
        .iter()
        .filter(|segment| segment.state == HistorySegmentState::Sealed as i32)
        .count() as u64;
    let backlog = &first.metrics.history_upload_backlog;
    assert_eq!(backlog.segments(), expected_segments);
    let expected_bytes = backlog.bytes();
    let expected_oldest = backlog.oldest_sealed_at_micros();
    assert!(expected_bytes > 0);
    assert!(expected_oldest.is_some());
    first.shutdown().await;

    let disabled = EffectHarness::with_config(
        durable.clone(),
        EffectsConfig {
            history_upload_rescan_interval: Duration::ZERO,
            ..rescan_config()
        },
    );
    disabled.rebuild_all_partitions().await;
    disabled.settle().await;
    assert_eq!(disabled.metrics.history_upload_backlog.segments(), 0);
    assert_eq!(disabled.metrics.history_upload_backlog.bytes(), 0);
    assert_eq!(
        disabled
            .metrics
            .history_upload_backlog
            .oldest_sealed_at_micros(),
        None
    );
    disabled.shutdown().await;

    let second = EffectHarness::with_config(durable, rescan_config());
    // Consume the initial tick before any partition belongs to the restarted owner.
    second.settle().await;
    let rebuilt_at = tokio::time::Instant::now();
    second.rebuild_all_partitions().await;
    let backlog = &second.metrics.history_upload_backlog;
    wait_until(&second, "rebuilt backlog gauges publish", || {
        backlog.segments() == expected_segments
    })
    .await;
    assert!(rebuilt_at.elapsed() < RESCAN_INTERVAL);
    assert_eq!(backlog.segments(), expected_segments);
    assert_eq!(backlog.bytes(), expected_bytes);
    assert_eq!(backlog.oldest_sealed_at_micros(), expected_oldest);

    let later_flow_id = "backlog-after-restart";
    let later_run_id = second.start_flow(
        start_request(later_flow_id),
        RunSettings {
            history_segment_target_bytes: 1,
            ..settings()
        },
    );
    set_long_attribute(
        &second,
        later_flow_id,
        later_run_id,
        "notes",
        "after restart",
    );
    close_run(&second, later_flow_id, later_run_id);
    tokio::time::sleep(RESCAN_INTERVAL).await;
    second.settle().await;
    assert_eq!(
        backlog.segments(),
        segments(&second, flow_id, run_id)
            .iter()
            .chain(segments(&second, later_flow_id, later_run_id).iter())
            .filter(|segment| segment.state == HistorySegmentState::Sealed as i32)
            .count() as u64
    );
    assert!(backlog.segments() > expected_segments);
    assert!(backlog.bytes() > expected_bytes);
    assert_eq!(backlog.oldest_sealed_at_micros(), expected_oldest);

    faults.set(HistoryWriteFaults::PROCEED);
    wait_until_uploaded(&second, flow_id, run_id).await;
    wait_until_uploaded(&second, later_flow_id, later_run_id).await;
    tokio::time::sleep(RESCAN_INTERVAL).await;
    second.settle().await;
    assert_eq!(backlog.segments(), 0);
    assert_eq!(backlog.bytes(), 0);
    assert_eq!(backlog.oldest_sealed_at_micros(), None);
    second.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn retention_waits_for_uploads_and_the_purge_deletes_the_runs_history_objects() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::FAIL);
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(
        start_request("order-1"),
        RunSettings {
            history_segment_target_bytes: 1,
            ..short_retention_settings()
        },
    );
    let kept_run = harness.start_flow(start_request("order-2"), settings());
    close_run(&harness, "order-1", run_id);
    close_run(&harness, "order-2", kept_run);
    let first_deadline = harness
        .run_record("order-1", run_id)
        .retention_deadline_micros;

    tokio::time::sleep(SHORT_RETENTION + Duration::from_secs(1)).await;
    harness.settle().await;
    let postponed = harness.run_record("order-1", run_id);
    assert!(
        !postponed.is_purging,
        "a run with sealed segments is not purged"
    );
    assert!(postponed.retention_deadline_micros > first_deadline);
    assert!(run_purges(&harness).is_empty());

    faults.set(HistoryWriteFaults::PROCEED);
    wait_until_uploaded(&harness, "order-1", run_id).await;
    wait_until_uploaded(&harness, "order-2", kept_run).await;
    let run = harness.run_location("order-1", run_id);
    let run_prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    let kept = harness.run_location("order-2", kept_run);
    let kept_prefix = history_run_object_key_prefix(TenantId::DEFAULT, kept.flow_hash, kept_run);
    assert_eq!(
        object_keys_with_prefix(&harness, &run_prefix).await.len(),
        segments(&harness, "order-1", run_id).len()
    );
    let kept_keys = object_keys_with_prefix(&harness, &kept_prefix).await;
    assert_eq!(kept_keys.len(), 1);

    tokio::time::sleep(Duration::from_secs(3600)).await;
    // The purge waits for in-flight history writes to settle before it deletes.
    wait_until(&harness, "the purge commits", || {
        !run_purges(&harness).is_empty()
    })
    .await;
    let purges = run_purges(&harness);
    assert_eq!(purges.len(), 1);
    assert_eq!(purges[0].reply, ok_reply());
    assert!(
        object_keys_with_prefix(&harness, &run_prefix)
            .await
            .is_empty(),
        "the purge deleted every history object of the run"
    );
    assert_eq!(
        object_keys_with_prefix(&harness, &kept_prefix).await,
        kept_keys
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn uploads_of_a_purging_run_stop_without_writing_or_retrying() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let upload_slots = hold_upload_slots(&harness).await;
    let run_id = harness.start_flow(start_request("order-1"), settings());
    close_run(&harness, "order-1", run_id);
    mark_purging(&harness, "order-1", run_id);
    drop(upload_slots);

    tokio::time::sleep(Duration::from_secs(600)).await;
    harness.settle().await;
    let run = harness.run_location("order-1", run_id);
    let run_prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    assert_eq!(
        harness
            .durable
            .objects
            .call_count(ObjectStoreOperation::Put, &run_prefix),
        0
    );
    assert!(acknowledgments(&harness).is_empty());
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_acknowledgment_that_finds_the_run_purging_ends_its_uploads() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::DELAY);
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(start_request("order-1"), settings());
    close_run(&harness, "order-1", run_id);
    let segment = segments(&harness, "order-1", run_id)
        .pop()
        .expect("the close sealed a segment");
    let key = object_key(&harness, "order-1", run_id, &segment);
    wait_until(&harness, "the upload is in flight", || {
        harness
            .durable
            .objects
            .call_count(ObjectStoreOperation::Put, &key)
            == 1
    })
    .await;
    mark_purging(&harness, "order-1", run_id);

    wait_until(&harness, "the acknowledgment is rejected", || {
        acknowledgments(&harness).len() == 1
    })
    .await;
    let acknowledgment = &acknowledgments(&harness)[0];
    assert!(
        matches!(
            &acknowledgment.reply.reply,
            Some(command_reply::Reply::Error(error)) if error.grpc_code == 5
        ),
        "{:?}",
        acknowledgment.reply
    );
    tokio::time::sleep(Duration::from_secs(600)).await;
    harness.settle().await;
    assert_eq!(acknowledgments(&harness).len(), 1, "no retry");
    assert_eq!(
        harness
            .durable
            .objects
            .call_count(ObjectStoreOperation::Put, &key),
        1
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_node_restored_from_a_snapshot_before_the_acknowledgments_finishes_the_uploads() {
    let durable = DurableState::new();
    let first = EffectHarness::with_config(durable.clone(), EffectsConfig::default());
    first.rebuild_all_partitions().await;
    enable_history_offload(&first);
    let upload_slots = hold_upload_slots(&first).await;
    let flow_ids = ["order-1", "order-2"];
    let run_ids: Vec<RunId> = flow_ids
        .iter()
        .map(|flow_id| {
            let run_id = first.start_flow(start_request(flow_id), settings());
            close_run(&first, flow_id, run_id);
            run_id
        })
        .collect();
    let sealed: Vec<_> = flow_ids
        .iter()
        .zip(&run_ids)
        .map(|(flow_id, run_id)| sealed_contents(&first, flow_id, *run_id))
        .collect();
    let snapshot = first.flow_effect_operations.committed_snapshot();
    drop(upload_slots);
    for (flow_id, run_id) in flow_ids.iter().zip(&run_ids) {
        wait_until_uploaded(&first, flow_id, *run_id).await;
    }
    first.shutdown().await;

    // The restored state predates the acknowledgments, but the objects exist.
    let restored = DurableState {
        state: Arc::new(Mutex::new(snapshot)),
        objects: Arc::clone(&durable.objects),
        local_store: Arc::new(InMemoryLocalStore::new()),
        clock: Arc::clone(&durable.clock),
    };
    let second = EffectHarness::with_config(restored, EffectsConfig::default());
    for (flow_id, run_id) in flow_ids.iter().zip(&run_ids) {
        assert!(is_upload_pending(&second, flow_id, *run_id));
    }
    second.rebuild_all_partitions().await;
    for ((flow_id, run_id), sealed) in flow_ids.iter().zip(&run_ids).zip(&sealed) {
        wait_until_uploaded(&second, flow_id, *run_id).await;
        assert_uploaded_chain(&second, flow_id, *run_id, sealed).await;
    }
    assert_eq!(
        second
            .metrics
            .history_uploads
            .count(HistoryUploadOutcome::AlreadyPresent),
        2
    );
    second.shutdown().await;
}

/// Writes the Attribute `key` with a value long enough to live in the value table.
fn set_long_attribute(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    key: &str,
    text: &str,
) {
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: flow_id.to_owned(),
                attributes: vec![AttributeWrite {
                    key: key.to_owned(),
                    value: Some(string_value(&format!("{text} {}", "x".repeat(200)))),
                    ..AttributeWrite::default()
                }],
                request_id: uuid::Uuid::now_v7().to_string(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: vec![1],
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow(flow_id),
            command,
            CommitUrgency::Prompt,
        )
        .expect("the write commits");
    assert!(!is_error_reply(&reply), "the write failed: {reply:?}");
}

#[tokio::test(start_paused = true)]
async fn a_stopping_owner_abandons_its_pending_write_before_the_new_owner_purges_the_run() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::DELAY_FOR_TEN_MINUTES);
    let durable = durable_with_faults(Arc::clone(&faults));
    let first = EffectHarness::with_config(durable.clone(), EffectsConfig::default());
    first.rebuild_all_partitions().await;
    enable_history_offload(&first);
    let run_id = first.start_flow(start_request("order-1"), short_retention_settings());
    close_run(&first, "order-1", run_id);
    let segment = segments(&first, "order-1", run_id)
        .pop()
        .expect("the close sealed a segment");
    let key = object_key(&first, "order-1", run_id, &segment);
    wait_until(&first, "the old owner's write is in flight", || {
        durable.objects.call_count(ObjectStoreOperation::Put, &key) == 1
    })
    .await;
    // The old owner stops, as at a takeover, before the store accepted its delayed write.
    first.effect_context().shutdown.cancel();
    faults.set(HistoryWriteFaults::PROCEED);

    let second = EffectHarness::with_config(durable.clone(), EffectsConfig::default());
    second.rebuild_all_partitions().await;
    wait_until_uploaded(&second, "order-1", run_id).await;
    // An object outside the catalog, as an older timeline wrote it, under the run's prefix.
    let run = second.run_location("order-1", run_id);
    let stale_key = history_segment_object_key(
        TenantId::DEFAULT,
        run.flow_hash,
        run_id,
        segment.first_seq,
        segment.last_seq + 7,
    );
    second
        .durable
        .objects
        .put(
            &stale_key,
            Bytes::from_static(b"stale"),
            WriteCondition::Unconditional,
        )
        .await
        .expect("the stale object is written");
    tokio::time::sleep(SHORT_RETENTION + Duration::from_secs(1)).await;
    wait_until(&second, "the purge commits", || {
        !run_purges(&second).is_empty()
    })
    .await;
    let run_prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    assert!(
        object_keys_with_prefix(&second, &run_prefix)
            .await
            .is_empty()
    );

    // Past the delay, the dropped call wrote nothing; a write the store accepted is not covered.
    tokio::time::sleep(Duration::from_secs(15 * 60)).await;
    second.settle().await;
    assert!(
        object_keys_with_prefix(&second, &run_prefix)
            .await
            .is_empty(),
        "no write of the old owner landed after the purge"
    );
    assert_eq!(
        durable.objects.call_count(ObjectStoreOperation::Put, &key),
        2
    );
    first.shutdown().await;
    second.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn uploads_hold_no_slot_or_object_bytes_while_their_acknowledgments_wait() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::DELAY);
    let concurrency = 2;
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig {
            history_upload_limits: HistoryUploadLimits {
                max_concurrency: concurrency,
                ..HistoryUploadLimits::default()
            },
            ..EffectsConfig::default()
        },
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_count = 20;
    for index in 0..run_count {
        let flow_id = format!("order-{index}");
        let run_id = harness.start_flow(start_request(&flow_id), settings());
        close_run(&harness, &flow_id, run_id);
    }
    // No acknowledgment commits, so every run's upload waits on it.
    harness
        .flow_effect_operations
        .unavailable_proposals
        .store(usize::MAX, Ordering::SeqCst);
    let started = tokio::time::Instant::now();
    wait_until(&harness, "every upload started", || {
        harness
            .durable
            .objects
            .call_count(ObjectStoreOperation::Put, HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
            == run_count
    })
    .await;
    // Each write takes 10 seconds, so two at a time start the last after at least 90 seconds.
    assert!(
        started.elapsed() >= Duration::from_secs(90),
        "{:?}",
        started.elapsed()
    );
    tokio::time::sleep(Duration::from_secs(11)).await;
    harness.settle().await;
    assert_eq!(
        object_keys_with_prefix(&harness, HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
            .await
            .len(),
        usize::try_from(run_count).expect("few runs")
    );
    assert!(acknowledgments(&harness).is_empty());
    // Encoded bytes live only with an upload slot, so free slots mean no bytes are held.
    assert_eq!(harness.metrics.history_upload_in_flight_bytes.get(), 0);
    let held_slots = tokio::time::timeout(
        Duration::from_secs(1),
        harness
            .effect_context()
            .history_upload_limiter
            .hold_every_slot_for_test(),
    )
    .await
    .expect("every upload slot is free");
    drop(held_slots);
    assert_eq!(
        harness.metrics.history_upload_concurrency_limit.get(),
        i64::try_from(concurrency).expect("small")
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_segment_sealed_before_offload_turns_off_keeps_its_content_through_pruning() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let upload_slots = hold_upload_slots(&harness).await;
    let run_id = harness.start_flow(
        start_request("order-1"),
        RunSettings {
            history_segment_target_bytes: 1,
            history_window_events: 4,
            history_prune_chunk_events: 1,
            ..settings()
        },
    );
    for round in 0..3 {
        set_long_attribute(&harness, "order-1", run_id, "notes", &format!("v{round}"));
    }
    let sealed = segments(&harness, "order-1", run_id);
    assert!(sealed.len() >= 4, "each command sealed: {sealed:?}");
    // A previous owner stored every segment completely, but its acknowledgments were lost.
    let mut seeded = Vec::new();
    for segment in &sealed {
        let key = object_key(&harness, "order-1", run_id, segment);
        let content = committed_content(&harness, "order-1", run_id, segment.first_seq);
        let object = Bytes::from(
            encode_history_segment(&content)
                .expect("committed content encodes")
                .bytes,
        );
        harness
            .durable
            .objects
            .put(&key, object.clone(), WriteCondition::CreateOnly)
            .await
            .expect("the complete object is written");
        seeded.push((key, object));
    }

    set_history_offload_mode(&harness, false);
    for round in 3..40 {
        set_long_attribute(&harness, "order-1", run_id, "notes", &format!("v{round}"));
    }
    assert_eq!(
        segments(&harness, "order-1", run_id).len(),
        sealed.len(),
        "nothing seals while offload is off"
    );
    drop(upload_slots);

    wait_until_uploaded(&harness, "order-1", run_id).await;
    let uploads = &harness.metrics.history_uploads;
    assert_eq!(
        uploads.count(HistoryUploadOutcome::AlreadyPresent),
        sealed.len() as u64
    );
    assert_eq!(uploads.count(HistoryUploadOutcome::Replaced), 0);
    assert_eq!(harness.metrics.history_upload_checksum_mismatches.get(), 0);
    for (key, object) in seeded {
        assert_eq!(stored_object(&harness, &key).await, Some(object), "{key}");
    }
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_permanent_upload_rejection_waits_for_the_rescan_and_acknowledges_nothing() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::FAIL_PERMANENTLY);
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(start_request("order-1"), settings());
    close_run(&harness, "order-1", run_id);
    let sealed = sealed_contents(&harness, "order-1", run_id);
    let puts = || {
        harness
            .durable
            .objects
            .call_count(ObjectStoreOperation::Put, HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
    };
    wait_until(&harness, "the first upload is rejected", || puts() == 1).await;

    tokio::time::sleep(Duration::from_secs(30)).await;
    harness.settle().await;
    assert_eq!(
        puts(),
        1,
        "a permanent rejection is not retried with backoff"
    );
    assert_eq!(
        harness
            .metrics
            .history_uploads
            .count(HistoryUploadOutcome::Failed),
        1
    );
    tokio::time::sleep(Duration::from_secs(40)).await;
    harness.settle().await;
    assert_eq!(puts(), 2, "the rescan rechecks the run");
    assert!(acknowledgments(&harness).is_empty());
    assert!(is_upload_pending(&harness, "order-1", run_id));

    faults.set(HistoryWriteFaults::PROCEED);
    wait_until_uploaded(&harness, "order-1", run_id).await;
    assert_uploaded_chain(&harness, "order-1", run_id, &sealed).await;
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_permanent_deletion_rejection_holds_the_purge_until_deletion_succeeds() {
    let faults = Arc::new(HistoryWriteFaults::default());
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(start_request("order-1"), short_retention_settings());
    close_run(&harness, "order-1", run_id);
    wait_until_uploaded(&harness, "order-1", run_id).await;
    faults.set_deletes(HistoryWriteFaults::FAIL_PERMANENTLY);

    tokio::time::sleep(SHORT_RETENTION + Duration::from_secs(200)).await;
    harness.settle().await;
    assert!(run_purges(&harness).is_empty(), "the purge waits");
    assert!(harness.run_record("order-1", run_id).is_purging);
    let run = harness.run_location("order-1", run_id);
    let run_prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    assert_eq!(
        object_keys_with_prefix(&harness, &run_prefix).await.len(),
        1
    );
    let deletions = harness
        .durable
        .objects
        .call_count(ObjectStoreOperation::Delete, "");
    assert!(
        (1..=4).contains(&deletions),
        "a permanent rejection is rechecked each minute: {deletions} deletions"
    );

    faults.set_deletes(HistoryWriteFaults::PROCEED);
    wait_until(&harness, "the purge commits", || {
        !run_purges(&harness).is_empty()
    })
    .await;
    assert_eq!(run_purges(&harness)[0].reply, ok_reply());
    assert!(
        object_keys_with_prefix(&harness, &run_prefix)
            .await
            .is_empty()
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_run_the_rescan_cannot_read_never_hides_the_other_runs_of_its_partition() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::FAIL_PERMANENTLY);
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let healthy_flow = "order-healthy";
    let partition = harness.partition_of(healthy_flow);
    let broken_flow = (0..)
        .map(|index| format!("order-broken-{index}"))
        .find(|flow_id| harness.partition_of(flow_id) == partition)
        .expect("some Flow ID shares the partition");
    let broken_run = harness.start_flow(start_request(&broken_flow), settings());
    close_run(&harness, &broken_flow, broken_run);
    let healthy_run = harness.start_flow(start_request(healthy_flow), settings());
    close_run(&harness, healthy_flow, healthy_run);
    let sealed = sealed_contents(&harness, healthy_flow, healthy_run);
    wait_until(&harness, "both uploads were rejected", || {
        harness
            .durable
            .objects
            .call_count(ObjectStoreOperation::Put, HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
            == 2
    })
    .await;
    // The broken run's segment row no longer decodes.
    let broken_segment = segments(&harness, &broken_flow, broken_run)
        .pop()
        .expect("the close sealed a segment");
    let broken = harness.run_location(&broken_flow, broken_run);
    harness
        .flow_effect_operations
        .state
        .lock()
        .expect("lock")
        .rows
        .insert(
            (
                ColumnFamily::Flow,
                history_segment_key(broken.flow_hash, broken_run, broken_segment.first_seq),
            ),
            vec![0xff; 3],
        );
    faults.set(HistoryWriteFaults::PROCEED);

    wait_until_uploaded(&harness, healthy_flow, healthy_run).await;
    assert_uploaded_chain(&harness, healthy_flow, healthy_run, &sealed).await;
    assert!(harness.metrics.history_upload_rescan_errors.get() >= 1);
    assert!(is_upload_pending(&harness, &broken_flow, broken_run));
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn seals_after_a_permanent_rejection_wait_for_the_rescan() {
    let faults = Arc::new(HistoryWriteFaults::default());
    faults.set(HistoryWriteFaults::FAIL_PERMANENTLY);
    let harness = EffectHarness::with_config(
        durable_with_faults(Arc::clone(&faults)),
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(
        start_request("order-1"),
        RunSettings {
            history_segment_target_bytes: 1,
            ..settings()
        },
    );
    let puts = || {
        harness
            .durable
            .objects
            .call_count(ObjectStoreOperation::Put, HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
    };
    wait_until(&harness, "the first upload is rejected", || puts() == 1).await;

    let sealed_before = segments(&harness, "order-1", run_id).len();
    for round in 0..10 {
        set_long_attribute(&harness, "order-1", run_id, "notes", &format!("v{round}"));
    }
    assert!(segments(&harness, "order-1", run_id).len() >= sealed_before + 10);
    tokio::time::sleep(Duration::from_secs(5)).await;
    harness.settle().await;
    assert_eq!(
        puts(),
        1,
        "new seals do not retry a rejected run before the rescan"
    );
    tokio::time::sleep(Duration::from_secs(60)).await;
    harness.settle().await;
    assert_eq!(puts(), 2, "the rescan retries the run once");
    assert!(acknowledgments(&harness).is_empty());
    harness.shutdown().await;
}
