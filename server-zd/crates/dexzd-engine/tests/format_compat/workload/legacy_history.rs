// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Runs recorded while their partition did not offload history, and their migration once a
//! node restored from the recording turns offload on (plan section 5.17).

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use dexzd_apply::LoadedHistorySegments;
use dexzd_apply::read_model::{
    SegmentedHistoryPage, get_history_events, get_history_events_from_segments, resolve_run_id,
};
use dexzd_keyspace::{ValueId, run_prefix};
use dexzd_protocol::dex::{FlowStatus, GetHistoryEventsResponse};
use dexzd_protocol::dexzd::v1::{AttributeRecord, HistoryRecord, RunRecord, stored_value};

use super::*;
use crate::compat_node::{engine_node_config, start_engine_node};
use crate::format_decoding::decode_objects;
use crate::golden_replay::RESTART_DELAY_MICROS;
use crate::testdata_tree::{OBJECTS_DIRECTORY, load_objects, read_files, testdata_versions};

/// Never offloads history in the recording.
const NEVER_OFFLOADED_PARTITION: PartitionId = PartitionId(2);
/// Turns offload on at the end of the recording, while `legacy-migrating` runs.
const LATE_OFFLOADED_PARTITION: PartitionId = PartitionId(3);
const RUNNING_FLOW_PREFIX: &str = "legacy-running";
const CLOSED_FLOW_PREFIX: &str = "legacy-closed";
const MIGRATING_FLOW_PREFIX: &str = "legacy-migrating";
/// Enough commands for the eight-event window to prune and for several migration segments.
const LEGACY_ROUNDS: usize = 30;
/// Most commands a migration may take in these tests.
const MAX_MIGRATION_COMMANDS: usize = 60;
const RESTORE_SEED: u64 = 0x5eed_2026_1107;

impl Workload<'_> {
    /// `legacy-running`, `legacy-closed`, and `legacy-migrating`: runs with a small window
    /// and small segments that record history while their partition does not offload.
    pub(super) async fn legacy_runs_record_history_without_offload(&self) {
        let running = self
            .start_legacy_run(
                RUNNING_FLOW_PREFIX,
                NEVER_OFFLOADED_PARTITION,
                legacy_run_settings(),
            )
            .await;
        for round in 0..LEGACY_ROUNDS {
            self.apply_legacy_round(&running, round).await;
        }
        let closed = self
            .start_legacy_run(
                CLOSED_FLOW_PREFIX,
                NEVER_OFFLOADED_PARTITION,
                RunSettings {
                    retention_after_close_micros: 60 * SECOND_MICROS,
                    ..legacy_run_settings()
                },
            )
            .await;
        for round in 0..4 {
            self.apply_legacy_round(&closed, round).await;
        }
        self.commit(&closed, cancel_command(&closed)).await;
        let migrating = self
            .start_legacy_run(
                MIGRATING_FLOW_PREFIX,
                LATE_OFFLOADED_PARTITION,
                legacy_run_settings(),
            )
            .await;
        for round in 0..LEGACY_ROUNDS {
            self.apply_legacy_round(&migrating, round).await;
        }
        self.set_history_offload_modes(&[LATE_OFFLOADED_PARTITION], true)
            .await;
        for round in LEGACY_ROUNDS..LEGACY_ROUNDS + 2 {
            let outcome = self.apply_legacy_round(&migrating, round).await;
            assert_eq!(sealed_segment_count(&outcome), 1, "the run migrates");
        }
    }

    async fn start_legacy_run(
        &self,
        prefix: &str,
        partition: PartitionId,
        settings: RunSettings,
    ) -> FlowRun {
        let flow_id = self.legacy_flow_id(prefix, partition);
        self.start_flow(
            StartFlowRequest {
                flow_id: flow_id.clone(),
                flow_type: "LegacyFlow".to_owned(),
                request_id: format!("start-{flow_id}"),
                ..StartFlowRequest::default()
            },
            settings,
        )
        .await
    }

    fn legacy_flow_id(&self, prefix: &str, partition: PartitionId) -> String {
        (0..)
            .map(|index| format!("{prefix}-{index}"))
            .find(|flow_id| self.context(flow_id).partition == partition)
            .expect("some Flow ID hashes to the partition")
    }

    /// Replaces `profile` in even rounds and publishes to `updates` in odd rounds, so records
    /// alternate between superseding a value and recording an event.
    async fn apply_legacy_round(&self, run: &FlowRun, round: usize) -> ApplyOutcome {
        let text = format!("{LONG_PROFILE_TEXT} Round {round}.");
        if round.is_multiple_of(2) {
            let request_id = format!("{}-write-{round}", run.flow_id);
            let request = SetAttributesRequest {
                flow_id: run.flow_id.clone(),
                attributes: vec![attribute("profile", string_value(&text))],
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            };
            self.commit(
                run,
                Command::AttributesSet(AttributesSetCommand {
                    request_fingerprint: request_fingerprint(&request),
                    request: Some(request),
                    hard_delete_objects: Vec::new(),
                }),
            )
            .await
        } else {
            let message_id = format!("{}-message-{round}", run.flow_id);
            self.commit(
                run,
                publish_command(vec![channel_message("updates", &message_id, &text)]),
            )
            .await
        }
    }

    async fn legacy_run(&self, prefix: &str, partition: PartitionId) -> FlowRun {
        let flow_id = self.legacy_flow_id(prefix, partition);
        let flow_hash = self.context(&flow_id).flow_hash;
        let view = self
            .node
            .committer()
            .committed_view()
            .expect("open a committed view");
        let lookup_flow_id = flow_id.clone();
        let run_id = tokio::task::spawn_blocking(move || {
            resolve_run_id(&view, flow_hash, TenantId::DEFAULT, &lookup_flow_id, "")
                .expect("the Flow directory reads")
                .expect("the recorded Flow has a run")
        })
        .await
        .expect("the read task completes");
        FlowRun { flow_id, run_id }
    }

    async fn run_record_of(&self, run: &FlowRun) -> RunRecord {
        let mut records = self.run_rows::<RunRecord>(run, RunTable::RunRecord).await;
        assert_eq!(records.len(), 1, "{} has one run record", run.flow_id);
        records.remove(0)
    }

    async fn history_segments(&self, run: &FlowRun) -> Vec<HistorySegmentRecord> {
        self.run_rows::<HistorySegmentRecord>(run, RunTable::HistorySegment)
            .await
    }

    /// Committed history records of the run in the History column family, by seq.
    async fn stored_history_records(&self, run: &FlowRun) -> BTreeMap<u64, HistoryRecord> {
        let flow_hash = self.context(&run.flow_id).flow_hash;
        let prefix = run_table_prefix(flow_hash, run.run_id, RunTable::HistoryRecord);
        let (start, end) = prefix_range(&prefix);
        let view = self
            .node
            .committer()
            .committed_view()
            .expect("open a committed view");
        let rows = tokio::task::spawn_blocking(move || {
            view.scan(ColumnFamily::History, &start, &end, usize::MAX)
                .expect("scan committed history")
        })
        .await
        .expect("the scan task completes");
        rows.into_iter()
            .map(|(_, value)| {
                let record = HistoryRecord::decode(value.as_slice()).expect("records decode");
                (record.seq, record)
            })
            .collect()
    }

    async fn history_events(&self, run: &FlowRun) -> GetHistoryEventsResponse {
        let flow_hash = self.context(&run.flow_id).flow_hash;
        let run_id = run.run_id;
        let view = self
            .node
            .committer()
            .committed_view()
            .expect("open a committed view");
        tokio::task::spawn_blocking(move || {
            get_history_events(&view, flow_hash, run_id, 0, 1000, &[], 0, usize::MAX)
                .expect("history reads")
        })
        .await
        .expect("the read task completes")
    }

    /// Reads the first page as the engine does: seqs of `uploaded` segments from their content,
    /// the rest locally.
    async fn history_events_with_uploads(
        &self,
        run: &FlowRun,
        uploaded: &[(HistorySegmentRecord, HistorySegmentContent)],
    ) -> GetHistoryEventsResponse {
        let flow_hash = self.context(&run.flow_id).flow_hash;
        let run_id = run.run_id;
        let mut loaded = LoadedHistorySegments::default();
        for (segment, content) in uploaded {
            let records: Vec<HistoryRecord> = content
                .records
                .iter()
                .map(|row| HistoryRecord::decode(row.row.as_slice()).expect("record decodes"))
                .collect();
            loaded
                .insert(segment, records.into())
                .expect("uploaded records are in order");
        }
        let view = self
            .node
            .committer()
            .committed_view()
            .expect("open a committed view");
        tokio::task::spawn_blocking(move || {
            match get_history_events_from_segments(
                &view,
                flow_hash,
                run_id,
                0,
                1000,
                &[],
                0,
                usize::MAX,
                &loaded,
                false,
            )
            .expect("history reads")
            {
                SegmentedHistoryPage::Page(page) => page,
                SegmentedHistoryPage::NeedsSegments(needed) => {
                    panic!("every uploaded segment is loaded, yet the read needs {needed:?}")
                }
            }
        })
        .await
        .expect("the read task completes")
    }

    async fn segment_content(&self, run: &FlowRun, first_seq: u64) -> HistorySegmentContent {
        let flow_hash = self.context(&run.flow_id).flow_hash;
        let run_id = run.run_id;
        let view = self
            .node
            .committer()
            .committed_view()
            .expect("open a committed view");
        tokio::task::spawn_blocking(move || {
            read_history_segment_content(&view, flow_hash, run_id, first_seq)
                .expect("the segment content reads")
                .expect("the segment exists")
        })
        .await
        .expect("the read task completes")
    }

    /// Acknowledges every SEALED segment oldest first, as the uploader would.
    ///
    /// Returns each acknowledged segment with the content its object holds.
    async fn acknowledge_sealed_segments(
        &self,
        run: &FlowRun,
    ) -> Vec<(HistorySegmentRecord, HistorySegmentContent)> {
        let mut uploaded = Vec::new();
        loop {
            let uploaded_through_seq = self.run_record_of(run).await.history_uploaded_through_seq;
            let Some(segment) = self
                .history_segments(run)
                .await
                .into_iter()
                .find(|segment| segment.last_seq > uploaded_through_seq)
            else {
                return uploaded;
            };
            let content = self.segment_content(run, segment.first_seq).await;
            let encoded = encode_history_segment(&content).expect("the segment encodes");
            self.commit(
                run,
                Command::HistorySegmentUploaded(HistorySegmentUploadedCommand {
                    first_seq: segment.first_seq,
                    last_seq: segment.last_seq,
                    content_checksum: encoded.content_checksum.to_vec(),
                    object_size_bytes: encoded.bytes.len() as u64,
                }),
            )
            .await;
            uploaded.push((segment, content));
        }
    }

    /// Applies rounds from `first_round` until the migration caught up, requiring one sealed
    /// segment per command. Returns the commands it took.
    async fn migrate_to_completion(&self, run: &FlowRun, first_round: usize) -> usize {
        for command_count in 1..=MAX_MIGRATION_COMMANDS {
            let outcome = self
                .apply_legacy_round(run, first_round + command_count)
                .await;
            assert_eq!(sealed_segment_count(&outcome), 1, "one segment per command");
            let record = self.run_record_of(run).await;
            assert_segment_chain(&self.history_segments(run).await, &record);
            if record.history_migration_started_seq == 0 {
                return command_count;
            }
        }
        panic!("the migration did not catch up within {MAX_MIGRATION_COMMANDS} commands");
    }
}

fn legacy_run_settings() -> RunSettings {
    RunSettings {
        history_window_events: 8,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        history_segment_target_bytes: 1_500,
        ..workload_run_settings()
    }
}

fn cancel_command(run: &FlowRun) -> Command {
    Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            flow_id: run.flow_id.clone(),
            reason: "legacy run closed".to_owned(),
            stop_type: StopType::Cancel as i32,
            ..StopFlowRequest::default()
        }),
    })
}

fn sealed_segment_count(outcome: &ApplyOutcome) -> usize {
    outcome
        .effects
        .iter()
        .filter(|effect| matches!(effect, ApplyEffect::HistorySegmentSealed { .. }))
        .count()
}

/// Requires segments contiguous from seq 1 and event 1, followed by the open segment.
fn assert_segment_chain(segments: &[HistorySegmentRecord], record: &RunRecord) {
    let mut next_seq = 1;
    let mut next_event_id = 1;
    for segment in segments {
        assert_eq!(
            (segment.first_seq, segment.first_event_id),
            (next_seq, next_event_id),
            "{segment:?}"
        );
        assert!(segment.first_seq <= segment.last_seq, "{segment:?}");
        next_seq = segment.last_seq + 1;
        next_event_id = segment.last_event_id + 1;
    }
    assert_eq!(
        (
            record.history_open_segment_first_seq,
            record.history_open_segment_first_event_id
        ),
        (next_seq, next_event_id)
    );
}

fn profile_value_seq(attributes: &[AttributeRecord]) -> u64 {
    let [profile] = attributes else {
        panic!("the legacy run holds only its profile: {attributes:?}");
    };
    match profile
        .value
        .as_ref()
        .and_then(|stored| stored.kind.as_ref())
    {
        Some(stored_value::Kind::TableValue(reference)) => {
            let value_id = reference
                .value_id
                .as_ref()
                .expect("table values have an ID");
            ValueId {
                run_seq: value_id.run_seq,
                index_in_command: value_id.index_in_command,
            }
            .run_seq
        }
        other => panic!("the profile is not a table value: {other:?}"),
    }
}

/// A node restored from the newest recorded testdata version, driven with workload helpers.
struct RestoredNode {
    node: EngineNode,
    clock: Arc<ScriptedClock>,
    ids: Arc<SeededIdSource>,
    object_store: Arc<InMemoryObjectStore>,
    hasher: PartitionHasher,
}

impl RestoredNode {
    async fn start() -> Self {
        let version = testdata_versions().pop().expect("testdata holds a version");
        let decoded = decode_objects(&read_files(&version.directory.join(OBJECTS_DIRECTORY)));
        let object_store = Arc::new(load_objects(&version.directory).await);
        let recorded_node = decoded
            .manifest
            .nodes
            .values()
            .next()
            .expect("the manifest names the recording node");
        let last_command_micros = decoded
            .commands
            .iter()
            .map(|command| command.proposed_at_micros)
            .max()
            .expect("the log holds commands");
        let clock = Arc::new(ScriptedClock::starting_at(
            last_command_micros + RESTART_DELAY_MICROS,
        ));
        let ids = Arc::new(SeededIdSource::new(RESTORE_SEED, clock.clone()));
        let config = engine_node_config(
            &recorded_node.node_id,
            recorded_node.incarnation,
            decoded.cluster.partition_count,
        );
        let node =
            start_engine_node(&config, object_store.clone(), clock.clone(), ids.clone()).await;
        let hasher = PartitionHasher::new(
            &decoded.cluster.partition_hash_key,
            decoded.cluster.partition_count,
        )
        .expect("the recorded cluster has a valid partition hasher");
        Self {
            node,
            clock,
            ids,
            object_store,
            hasher,
        }
    }

    fn workload(&self) -> Workload<'_> {
        Workload {
            node: &self.node,
            clock: &self.clock,
            ids: &self.ids,
            object_store: &self.object_store,
            hasher: &self.hasher,
        }
    }

    async fn shutdown(self) {
        self.node
            .shutdown()
            .await
            .expect("the node releases its lease");
    }
}

#[tokio::test(start_paused = true)]
async fn a_recorded_running_run_migrates_one_segment_per_command_once_offload_is_on() {
    let restored = RestoredNode::start().await;
    let workload = restored.workload();
    let run = workload
        .legacy_run(RUNNING_FLOW_PREFIX, NEVER_OFFLOADED_PARTITION)
        .await;
    let record = workload.run_record_of(&run).await;
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert_eq!(record.history_open_segment_first_seq, 0, "no segment state");
    let stored_before = workload.stored_history_records(&run).await;
    let pruned_seqs: BTreeSet<u64> = (1..record.next_run_seq)
        .filter(|seq| !stored_before.contains_key(seq))
        .collect();
    assert!(!pruned_seqs.is_empty(), "the window pruned before offload");
    let before = workload.history_events(&run).await;
    let pruned_range = before
        .pruned_event_id_range
        .expect("pruned events are reported");

    workload
        .set_history_offload_modes(&[NEVER_OFFLOADED_PARTITION], true)
        .await;
    let first = workload.apply_legacy_round(&run, LEGACY_ROUNDS).await;
    assert_eq!(sealed_segment_count(&first), 1);
    let record = workload.run_record_of(&run).await;
    assert_eq!(
        record.history_migration_started_seq,
        record.next_run_seq - 1
    );
    let commands = 1 + workload.migrate_to_completion(&run, LEGACY_ROUNDS).await;
    assert!(commands >= 3, "the recorded history spans several segments");

    let segments = workload.history_segments(&run).await;
    let stored = workload.stored_history_records(&run).await;
    for segment in &segments {
        let content = workload.segment_content(&run, segment.first_seq).await;
        let held: Vec<u64> = content.records.iter().map(|row| row.seq).collect();
        let expected: Vec<u64> = stored
            .range(segment.first_seq..=segment.last_seq)
            .map(|(seq, _)| *seq)
            .collect();
        assert_eq!(held, expected, "{segment:?} holds the stored records");
        assert!(held.iter().all(|seq| !pruned_seqs.contains(seq)));
    }
    assert_eq!(segments[0].first_seq, 1);
    let uploaded = workload.acknowledge_sealed_segments(&run).await;

    let after = workload.history_events_with_uploads(&run, &uploaded).await;
    assert_eq!(
        after.pruned_event_id_range,
        Some(pruned_range),
        "events pruned before the migration are still reported as pruned"
    );
    let before_ids: Vec<i64> = before.events.iter().map(|event| event.event_id).collect();
    let after_ids: Vec<i64> = after.events.iter().map(|event| event.event_id).collect();
    assert!(
        after_ids.starts_with(&before_ids),
        "the migration keeps every event"
    );
    assert!(
        after_ids
            .iter()
            .all(|event_id| *event_id == 1 || *event_id > pruned_range.end_event_id)
    );
    restored.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_recorded_closed_run_is_not_migrated_and_its_purge_deletes_its_local_history() {
    let restored = RestoredNode::start().await;
    let workload = restored.workload();
    let run = workload
        .legacy_run(CLOSED_FLOW_PREFIX, NEVER_OFFLOADED_PARTITION)
        .await;
    let record = workload.run_record_of(&run).await;
    assert_eq!(record.status, FlowStatus::Canceled as i32);
    assert!(!workload.stored_history_records(&run).await.is_empty());

    workload
        .set_history_offload_modes(&[NEVER_OFFLOADED_PARTITION], true)
        .await;
    let fired = workload.fire_due_timers(&run, SECOND_MICROS).await;
    assert_eq!(sealed_segment_count(&fired), 0);
    assert!(
        fired
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::RunPurgeRequested { .. })),
        "retention waits for no upload"
    );
    let record = workload.run_record_of(&run).await;
    assert_eq!(record.history_open_segment_first_seq, 0);
    assert!(workload.history_segments(&run).await.is_empty());

    workload
        .commit(&run, Command::RunPurge(RunPurgeCommand {}))
        .await;
    let flow_hash = workload.context(&run.flow_id).flow_hash;
    let (start, end) = prefix_range(&run_prefix(flow_hash, run.run_id));
    let view = restored
        .node
        .committer()
        .committed_view()
        .expect("open a committed view");
    let remaining = tokio::task::spawn_blocking(move || {
        [
            ColumnFamily::Flow,
            ColumnFamily::History,
            ColumnFamily::Value,
        ]
        .into_iter()
        .map(|column_family| {
            view.scan(column_family, &start, &end, usize::MAX)
                .expect("scan committed rows")
                .len()
        })
        .sum::<usize>()
    })
    .await
    .expect("the scan task completes");
    assert_eq!(remaining, 0, "the purge deleted the run's local history");
    restored.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn time_travel_during_the_migration_of_a_recorded_run_is_correct() {
    let restored = RestoredNode::start().await;
    let workload = restored.workload();
    let run = workload
        .legacy_run(RUNNING_FLOW_PREFIX, NEVER_OFFLOADED_PARTITION)
        .await;
    workload
        .set_history_offload_modes(&[NEVER_OFFLOADED_PARTITION], true)
        .await;
    workload.apply_legacy_round(&run, LEGACY_ROUNDS).await;
    let record = workload.run_record_of(&run).await;
    assert_ne!(record.history_migration_started_seq, 0);
    let target_seq = record.next_run_seq - 1;
    let attributes = workload
        .run_rows::<AttributeRecord>(&run, RunTable::Attribute)
        .await;
    assert_eq!(profile_value_seq(&attributes), target_seq);
    let target_timestamp_micros =
        workload.stored_history_records(&run).await[&target_seq].timestamp_micros;
    workload.apply_legacy_round(&run, LEGACY_ROUNDS + 1).await;
    workload.apply_legacy_round(&run, LEGACY_ROUNDS + 2).await;

    let travel = workload
        .time_travel(
            &run,
            ResetFlowRequest {
                flow_id: run.flow_id.clone(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                reason: "travel during the migration".to_owned(),
                skip_writes_reapply: true,
                history_event_time: (i128::from(target_timestamp_micros) * 1000).to_string(),
                ..ResetFlowRequest::default()
            },
            false,
        )
        .await;
    assert!(
        matches!(
            travel.reply.reply,
            Some(command_reply::Reply::TimeTravel(ref reply)) if reply.run_attempt == 2
        ),
        "{:?}",
        travel.reply
    );
    assert_eq!(
        sealed_segment_count(&travel),
        1,
        "the time travel migrates too"
    );
    let travel_seq = workload.run_record_of(&run).await.next_run_seq - 1;
    let attributes = workload
        .run_rows::<AttributeRecord>(&run, RunTable::Attribute)
        .await;
    assert_eq!(
        profile_value_seq(&attributes),
        target_seq,
        "the travel restored the profile"
    );

    workload
        .migrate_to_completion(&run, LEGACY_ROUNDS + 2)
        .await;
    let segment = workload
        .history_segments(&run)
        .await
        .into_iter()
        .find(|segment| (segment.first_seq..=segment.last_seq).contains(&travel_seq))
        .expect("a segment holds the time travel");
    let content = workload.segment_content(&run, segment.first_seq).await;
    assert!(content.records.iter().any(|row| row.seq == travel_seq));
    assert!(
        content
            .checkpoints
            .iter()
            .any(|row| row.seq == travel_seq && row.attempt == 2),
        "the second attempt's start checkpoint is offloaded with the time travel"
    );
    workload.acknowledge_sealed_segments(&run).await;
    let record = workload.run_record_of(&run).await;
    assert_eq!(record.current_attempt, 2);
    assert_segment_chain(&workload.history_segments(&run).await, &record);
    restored.shutdown().await;
}
