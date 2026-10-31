// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_apply::{ApplyContext, ApplyOutcome, StateWrite};
use dexzd_engine::CommittedCommand;
use dexzd_keyspace::row_keys::run_singleton_key;
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId, RunTable};
use dexzd_protocol::dexzd::v1::{CommandReply, FlowCommand, SearchDocumentRecord};
use prost::Message;

use super::*;

const PARTITION: PartitionId = PartitionId(3);
const DOCUMENT_BYTES: usize = 1024;

fn committed_upsert(run_number: u8) -> CommittedFlowCommand {
    let flow_hash = FlowHash(7);
    let run_id = RunId([run_number; 16]);
    let document = SearchDocumentRecord {
        tenant_id: 1,
        version: 1,
        status: FlowStatus::Running as i32,
        run_id: run_id.as_bytes().to_vec(),
        flow_type: "x".repeat(DOCUMENT_BYTES),
        ..SearchDocumentRecord::default()
    };
    CommittedFlowCommand {
        command: FlowCommand::default(),
        context: ApplyContext {
            partition: PARTITION,
            flow_hash,
        },
        committed: CommittedCommand {
            outcome: ApplyOutcome {
                writes: vec![StateWrite::Put {
                    column_family: ColumnFamily::Flow,
                    key: run_singleton_key(flow_hash, run_id, RunTable::SearchDocument),
                    value: document.encode_to_vec(),
                }],
                effects: Vec::new(),
                reply: CommandReply::default(),
                run_id: Some(run_id),
            },
            position: LogPosition::default(),
        },
    }
}

fn commit_frame(frames: &CommittedSearchFrames, run_number: u8) {
    frames.record_command(&committed_upsert(run_number));
    frames.finish_frame(
        PARTITION,
        &LogPosition {
            seq: u64::from(run_number),
            ..LogPosition::default()
        },
    );
}

/// A stalled projection, as during a SQL visibility outage, retains every protected Delete above the byte limit.
#[test]
fn deletes_survive_byte_overflow_while_the_projection_is_stalled() {
    let frame_bytes = CHANGE_OVERHEAD_BYTES;
    let frames = new_frames(1_000, 3 * frame_bytes);
    for run_number in 1..=20 {
        record_delete(&frames, run_number);
        finish_frame(&frames, u64::from(run_number));
    }
    assert_eq!(
        frames.lock_tracking().queued.len(),
        20,
        "protected Deletes remain queued beyond the byte limit"
    );
    assert_eq!(frames.lock_tracking().queued_bytes, 20 * frame_bytes);
    assert!(frames.rebuild_requests().is_empty());
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(0));

    // The projection recovers: it drains every queued Delete without rebuilding the partition.
    while let Some(frame) = take_frame(&frames) {
        frames.finish_projected_frame(&frame);
    }
    frames.finish_rebuild(PARTITION, frames.last_sequence());
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(20));
    assert_eq!(frames.lock_tracking().queued_bytes, 0);
    assert!(frames.rebuild_requests().is_empty());

    record_delete(&frames, 21);
    finish_frame(&frames, 21);
    assert_eq!(
        frames.lock_tracking().queued.len(),
        1,
        "freed bytes let new frames queue again"
    );
    assert!(frames.rebuild_requests().is_empty());
}

#[test]
fn delete_frame_overflow_does_not_schedule_an_automatic_rebuild() {
    let max_queued_bytes = 1024 * 1024;
    let frames = new_frames(2, max_queued_bytes);
    for run_number in 1..=20 {
        record_delete(&frames, run_number);
        finish_frame(&frames, u64::from(run_number));
    }
    assert_eq!(frames.lock_tracking().queued.len(), 20);
    assert!(frames.lock_tracking().queued_bytes < max_queued_bytes);
    assert!(frames.rebuild_requests().is_empty());
    assert_eq!(frames.lock_tracking().rebuild_requests.len(), 0);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(0));

    while let Some(frame) = take_frame(&frames) {
        frames.finish_projected_frame(&frame);
    }
    frames.finish_rebuild(PARTITION, frames.last_sequence());
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(20));
    assert!(frames.rebuild_requests().is_empty());
}

#[test]
fn permanent_projection_failure_records_partition_and_sqlstate() {
    let frames = new_frames(1, 1024);
    frames.record_permanent_failure(PARTITION, "upsert run failed: SQLSTATE 54023");
    let failure = frames.permanent_failure.borrow();
    let reason = failure.as_ref().expect("the failure is recorded");
    assert!(reason.contains("partition 3"));
    assert!(reason.contains("SQLSTATE 54023"));
}
fn new_frames(frame_limit: usize, byte_limit: usize) -> CommittedSearchFrames {
    frames_with_limits(frame_limit, byte_limit, frame_limit, byte_limit)
}

fn frames_with_limits(
    soft_frames: usize,
    soft_bytes: usize,
    hard_frames: usize,
    hard_bytes: usize,
) -> CommittedSearchFrames {
    CommittedSearchFrames::new(
        Arc::new(SearchProjectionQueueConfig {
            soft_frames,
            soft_bytes,
            hard_frames,
            hard_bytes,
            ..Default::default()
        }),
        Arc::new(NodeMetrics::unregistered()),
    )
}

fn take_frame(frames: &CommittedSearchFrames) -> Option<CommittedSearchFrame> {
    frames.lock_tracking().take_frame()
}

fn record_document(
    frames: &CommittedSearchFrames,
    run_number: u8,
    version: u64,
    status: FlowStatus,
) {
    let mut committed = committed_upsert(run_number);
    if let StateWrite::Put { value, .. } = &mut committed.committed.outcome.writes[0] {
        let mut document = SearchDocumentRecord::decode(value.as_slice()).unwrap();
        document.version = version;
        document.status = status as i32;
        *value = document.encode_to_vec();
    }
    frames.record_command(&committed);
}

fn finish_frame(frames: &CommittedSearchFrames, position: u64) {
    frames.finish_frame(
        PARTITION,
        &LogPosition {
            seq: position,
            ..Default::default()
        },
    );
}

fn record_delete(frames: &CommittedSearchFrames, run_number: u8) {
    let mut committed = committed_upsert(run_number);
    committed.committed.outcome.writes = vec![StateWrite::Delete {
        column_family: ColumnFamily::PartitionIndex,
        key: dexzd_keyspace::row_keys::tenant_run_index_key(
            PARTITION,
            dexzd_keyspace::IndexKind::SearchDocument,
            TenantId(1),
            FlowHash(7),
            RunId([run_number; 16]),
        ),
    }];
    frames.record_command(&committed);
}

#[test]
fn coalescing_keeps_latest_complete_document_and_delays_earlier_watermarks() {
    let frames = new_frames(100, 1024 * 1024);
    record_document(&frames, 1, 1, FlowStatus::Running);
    finish_frame(&frames, 1);
    record_document(&frames, 1, 2, FlowStatus::Running);
    finish_frame(&frames, 2);
    record_document(&frames, 1, 3, FlowStatus::Completed);
    finish_frame(&frames, 3);
    assert_eq!(frames.metrics.search_projection_coalesced_changes.get(), 2);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(0));
    let frame = take_frame(&frames).unwrap();
    let [
        SearchDocumentChange::Upsert {
            encoded_document, ..
        },
    ] = frame.changes.as_slice()
    else {
        panic!("latest document is retained")
    };
    let document = SearchDocumentRecord::decode(encoded_document.as_slice()).unwrap();
    assert_eq!(document.version, 3);
    assert_eq!(document.status, FlowStatus::Completed as i32);
    frames.finish_projected_frame(&frame);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(3));
    assert_eq!(frames.lock_tracking().queued_bytes, 0);
    assert!(frames.lock_tracking().pending_watermarks.is_empty());
}

#[test]
fn delete_supersedes_queued_upsert_and_inherits_its_watermark() {
    let frames = new_frames(100, 1024 * 1024);
    commit_frame(&frames, 1);
    record_delete(&frames, 1);
    finish_frame(&frames, 2);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(0));
    let deleted = take_frame(&frames).unwrap();
    assert!(matches!(
        deleted.changes.as_slice(),
        [SearchDocumentChange::Delete { .. }]
    ));
    frames.finish_projected_frame(&deleted);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(2));
    assert_eq!(frames.metrics.search_projection_coalesced_changes.get(), 1);
}

#[test]
fn soft_limit_sheds_only_intermediates_and_preserves_starts_closes_and_deletes() {
    let frames = frames_with_limits(1, 1024 * 1024, 8, 2 * 1024 * 1024);
    commit_frame(&frames, 1);
    record_document(&frames, 2, 7, FlowStatus::Running);
    record_document(&frames, 3, 1, FlowStatus::Running);
    record_document(&frames, 4, 9, FlowStatus::Completed);
    record_delete(&frames, 5);
    finish_frame(&frames, 2);
    assert_eq!(
        frames
            .metrics
            .search_projection_dropped_updates
            .count(ProjectionDropReason::Intermediate),
        1
    );
    assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 0);
    let first = take_frame(&frames).unwrap();
    frames.finish_projected_frame(&first);
    let second = take_frame(&frames).unwrap();
    assert_eq!(second.changes.len(), 3);
    assert!(
        second
            .changes
            .iter()
            .all(|change| change_identity(change).1 != RunId([2; 16]))
    );
    frames.finish_projected_frame(&second);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(2));
}

#[test]
fn intermediate_is_kept_below_both_soft_limits() {
    let frames = new_frames(100, 1024 * 1024);
    record_document(&frames, 1, 7, FlowStatus::Running);
    finish_frame(&frames, 1);
    assert_eq!(take_frame(&frames).unwrap().changes.len(), 1);
    assert_eq!(
        frames
            .metrics
            .search_projection_dropped_updates
            .count(ProjectionDropReason::Intermediate),
        0
    );
}

#[test]
fn coalesced_start_cannot_be_shed_as_an_intermediate() {
    let frames = frames_with_limits(1, 1024 * 1024, 4, 2 * 1024 * 1024);
    commit_frame(&frames, 1);
    record_document(&frames, 1, 7, FlowStatus::Running);
    finish_frame(&frames, 2);
    assert_eq!(take_frame(&frames).unwrap().changes.len(), 1);
    assert_eq!(
        frames
            .metrics
            .search_projection_dropped_updates
            .count(ProjectionDropReason::Intermediate),
        0
    );
    assert_eq!(frames.metrics.search_projection_coalesced_changes.get(), 1);
}

#[test]
fn bytes_trigger_shedding_and_protected_changes_use_the_hard_headroom() {
    let frames = frames_with_limits(100, DOCUMENT_BYTES, 200, 4 * DOCUMENT_BYTES);
    record_document(&frames, 1, 7, FlowStatus::Running);
    finish_frame(&frames, 1);
    assert!(take_frame(&frames).is_none());
    assert_eq!(
        frames
            .metrics
            .search_projection_dropped_updates
            .count(ProjectionDropReason::Intermediate),
        1
    );
    commit_frame(&frames, 2);
    record_document(&frames, 3, 8, FlowStatus::Completed);
    finish_frame(&frames, 3);
    record_delete(&frames, 4);
    finish_frame(&frames, 4);
    assert_eq!(frames.lock_tracking().queued.len(), 3);
    assert!(frames.rebuild_requests().is_empty());
}

#[test]
fn hard_limit_sheds_closes_then_inherited_starts_without_resurrecting_older_documents() {
    let frames = frames_with_limits(1, 1024 * 1024, 2, 2 * 1024 * 1024);
    commit_frame(&frames, 1);
    commit_frame(&frames, 2);
    record_document(&frames, 3, 8, FlowStatus::Completed);
    finish_frame(&frames, 3);
    assert_eq!(dropped(&frames, ProjectionDropReason::Close), 1);
    record_document(&frames, 1, 7, FlowStatus::Running);
    record_document(&frames, 3, 1, FlowStatus::Running);
    finish_frame(&frames, 4);
    assert_eq!(frames.lock_tracking().queued.len(), 2);
    commit_frame(&frames, 4);
    assert_eq!(dropped(&frames, ProjectionDropReason::Start), 1);
    assert!(frames.rebuild_requests().is_empty());
    while let Some(frame) = take_frame(&frames) {
        for change in &frame.changes {
            if change_identity(change).1 == RunId([1; 16]) {
                let SearchDocumentChange::Upsert {
                    encoded_document, ..
                } = change
                else {
                    panic!("retained change is an Upsert")
                };
                assert_eq!(
                    SearchDocumentRecord::decode(encoded_document.as_slice())
                        .unwrap()
                        .version,
                    7
                );
            }
        }
        frames.finish_projected_frame(&frame);
    }
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(5));
}

#[test]
fn delete_reclaims_queued_upserts_in_priority_and_oldest_order() {
    let frames = frames_with_limits(3, 1024 * 1024, 3, 2 * 1024 * 1024);
    record_document(&frames, 1, 2, FlowStatus::Running);
    finish_frame(&frames, 1);
    record_document(&frames, 2, 3, FlowStatus::Completed);
    finish_frame(&frames, 2);
    commit_frame(&frames, 3);
    for (run_number, reason) in [
        (4, ProjectionDropReason::Intermediate),
        (5, ProjectionDropReason::Close),
        (6, ProjectionDropReason::Start),
    ] {
        record_delete(&frames, run_number);
        finish_frame(&frames, u64::from(run_number));
        assert_eq!(dropped(&frames, reason), 1);
        assert_eq!(frames.lock_tracking().queued.len(), 3);
        assert!(frames.rebuild_requests().is_empty());
    }
    record_delete(&frames, 7);
    finish_frame(&frames, 7);
    record_delete(&frames, 8);
    finish_frame(&frames, 8);
    assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 0);
    assert!(frames.rebuild_requests().is_empty());
    while let Some(frame) = take_frame(&frames) {
        assert!(
            frame
                .changes
                .iter()
                .all(|change| matches!(change, SearchDocumentChange::Delete { .. }))
        );
        frames.finish_projected_frame(&frame);
    }
    frames.finish_rebuild(PARTITION, frames.last_sequence());
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(8));

    let frames = new_frames(2, 1024 * 1024);
    commit_frame(&frames, 1);
    commit_frame(&frames, 2);
    record_delete(&frames, 3);
    finish_frame(&frames, 3);
    assert_eq!(
        change_identity(&take_frame(&frames).unwrap().changes[0]).1,
        RunId([2; 16])
    );
}

#[test]
fn delete_admission_excludes_in_flight_upserts_but_counts_in_flight_deletes() {
    let frames = frames_with_limits(100, DOCUMENT_BYTES, 100, 2 * DOCUMENT_BYTES);
    commit_frame(&frames, 1);
    let in_flight = take_frame(&frames).unwrap();
    for run_number in 2..=33 {
        record_delete(&frames, run_number);
        finish_frame(&frames, u64::from(run_number));
    }
    assert_eq!(frames.lock_tracking().queued.len(), 32);
    assert!(frames.lock_tracking().queued_bytes > frames.cfg.hard_bytes);
    assert!(frames.rebuild_requests().is_empty());
    frames.finish_projected_frame(&in_flight);
    assert_eq!(frames.lock_tracking().queued_bytes, frames.cfg.hard_bytes);
    let in_flight_delete = take_frame(&frames).unwrap();
    record_delete(&frames, 34);
    finish_frame(&frames, 34);
    assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 0);
    frames.finish_projected_frame(&in_flight_delete);
}

fn dropped(frames: &CommittedSearchFrames, reason: ProjectionDropReason) -> u64 {
    frames
        .metrics
        .search_projection_dropped_updates
        .count(reason)
}

#[test]
fn taken_upserts_are_not_coalesced_or_removed_from_byte_accounting() {
    let frames = new_frames(100, 1024 * 1024);
    commit_frame(&frames, 1);
    let first = take_frame(&frames).unwrap();
    let first_size = first.size_bytes;
    record_document(&frames, 1, 2, FlowStatus::Running);
    finish_frame(&frames, 2);
    assert_eq!(frames.metrics.search_projection_coalesced_changes.get(), 0);
    assert!(frames.lock_tracking().queued_bytes > first_size);
    frames.finish_projected_frame(&first);
    let second = take_frame(&frames).unwrap();
    assert_eq!(frames.lock_tracking().queued_bytes, second.size_bytes);
}

#[test]
fn one_frame_coalesces_repeated_commands_and_a_delete() {
    let frames = new_frames(100, 1024 * 1024);
    record_document(&frames, 1, 1, FlowStatus::Running);
    record_document(&frames, 1, 2, FlowStatus::Running);
    record_delete(&frames, 1);
    finish_frame(&frames, 1);
    assert!(matches!(
        take_frame(&frames).unwrap().changes.as_slice(),
        [SearchDocumentChange::Delete { .. }]
    ));
    assert_eq!(frames.metrics.search_projection_coalesced_changes.get(), 2);
}

#[test]
fn hard_byte_limit_sheds_upserts_and_retains_overflowing_deletes() {
    for (priority, reason) in [
        (ChangePriority::Start, ProjectionDropReason::Start),
        (ChangePriority::Close, ProjectionDropReason::Close),
    ] {
        let frames = frames_with_limits(1, 1, 2, CHANGE_OVERHEAD_BYTES - 1);
        match priority {
            ChangePriority::Start => record_document(&frames, 1, 1, FlowStatus::Running),
            ChangePriority::Close => record_document(&frames, 1, 9, FlowStatus::Completed),
            _ => panic!("only Start and Close are tested"),
        }
        finish_frame(&frames, 1);
        assert!(frames.rebuild_requests().is_empty());
        assert_eq!(dropped(&frames, reason), 1);
        assert!(take_frame(&frames).is_none());
    }
    let frames = frames_with_limits(1, 1, 2, CHANGE_OVERHEAD_BYTES - 1);
    record_delete(&frames, 1);
    finish_frame(&frames, 1);
    assert!(frames.rebuild_requests().is_empty());
    assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 0);
}

#[test]
fn coalesced_start_can_be_shed_at_the_hard_limit_without_restoring_its_old_version() {
    let frames = frames_with_limits(10, 2048, 10, 2048);
    commit_frame(&frames, 1);
    record_document(&frames, 1, 7, FlowStatus::Running);
    record_document(&frames, 2, 1, FlowStatus::Running);
    finish_frame(&frames, 2);
    assert!(take_frame(&frames).is_none());
    assert_eq!(dropped(&frames, ProjectionDropReason::Start), 2);
    assert_eq!(frames.metrics.search_projection_coalesced_changes.get(), 1);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(2));
    record_document(&frames, 1, 8, FlowStatus::Running);
    finish_frame(&frames, 3);
    let projected = take_frame(&frames).unwrap();
    let SearchDocumentChange::Upsert {
        encoded_document, ..
    } = &projected.changes[0]
    else {
        panic!("later committed document projects normally")
    };
    assert_eq!(
        SearchDocumentRecord::decode(encoded_document.as_slice())
            .unwrap()
            .version,
        8
    );
}

#[test]
fn repair_set_deduplicates_and_drains_starts_before_closes_and_intermediates() {
    let mut repairs = ProjectionRepairs::default();
    for (run_number, priority) in [
        (1, RepairPriority::Intermediate),
        (2, RepairPriority::Close),
        (3, RepairPriority::Start),
        (4, RepairPriority::Intermediate),
    ] {
        assert!(repairs.record(
            ProjectionRepair::new(
                TenantId(1),
                RunId([run_number; 16]),
                FlowHash(7),
                PARTITION,
                priority
            ),
            1,
            4
        ));
    }
    assert!(repairs.record(
        ProjectionRepair::new(
            TenantId(1),
            RunId([1; 16]),
            FlowHash(7),
            PARTITION,
            RepairPriority::Start
        ),
        2,
        4
    ));
    assert!(repairs.record(
        ProjectionRepair::new(
            TenantId(1),
            RunId([1; 16]),
            FlowHash(7),
            PARTITION,
            RepairPriority::Intermediate
        ),
        3,
        4
    ));
    assert_eq!(repairs.len(), 4);
    let batch = repairs.take_batch(4);
    assert_eq!(
        batch
            .iter()
            .map(|repair| repair.priority)
            .collect::<Vec<_>>(),
        vec![
            RepairPriority::Start,
            RepairPriority::Start,
            RepairPriority::Close,
            RepairPriority::Intermediate
        ]
    );
    repairs.finish_batch(&batch);
    assert_eq!(repairs.len(), 0);
}

#[test]
fn a_drop_during_an_in_flight_repair_remains_pending() {
    let mut repairs = ProjectionRepairs::default();
    let repair = ProjectionRepair::new(
        TenantId(1),
        RunId([1; 16]),
        FlowHash(7),
        PARTITION,
        RepairPriority::Start,
    );
    assert!(repairs.record(repair, 1, 1));
    let first = repairs.take_batch(1);
    assert!(repairs.record(repair, 2, 1));
    repairs.finish_batch(&first);
    assert_eq!(repairs.len(), 1);
    let second = repairs.take_batch(1);
    assert_eq!(second.len(), 1);
    repairs.finish_batch(&second);
    assert_eq!(repairs.len(), 0);
}

#[test]
fn live_frames_and_in_flight_bytes_take_priority_over_repairs() {
    let frames = new_frames(1, 1024 * 1024);
    commit_frame(&frames, 1);
    record_document(&frames, 2, 8, FlowStatus::Completed);
    finish_frame(&frames, 2);
    assert_eq!(frames.lock_tracking().repairs.len(), 1);
    assert!(frames.take_repair_batch().is_empty());
    let live = take_frame(&frames).unwrap();
    assert!(frames.take_repair_batch().is_empty());
    frames.finish_projected_frame(&live);
    let repairs = frames.take_repair_batch();
    assert_eq!(repairs.len(), 1);
    commit_frame(&frames, 3);
    assert!(frames.take_repair_batch().is_empty());
    frames.finish_repair_batch(&repairs, &BTreeSet::from([(TenantId(1), RunId([2; 16]))]));
    assert_eq!(frames.lock_tracking().repairs.len(), 0);
    assert_eq!(
        frames
            .metrics
            .search_projection_repaired_runs
            .count(ProjectionDropReason::Close),
        1
    );
}

#[test]
fn staging_overflow_requires_version_comparison_without_automatic_rebuilding() {
    let frames = CommittedSearchFrames::new(
        Arc::new(SearchProjectionQueueConfig {
            soft_frames: 1,
            hard_frames: 1,
            max_repair_entries: 1,
            ..Default::default()
        }),
        Arc::new(NodeMetrics::unregistered()),
    );
    commit_frame(&frames, 1);
    commit_frame(&frames, 2);
    commit_frame(&frames, 3);
    assert_eq!(frames.lock_tracking().repairs.len(), 1);
    assert_eq!(frames.metrics.search_projection_repair_overflow.get(), 1);
    assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 0);
    assert_eq!(frames.diff_partitions(), vec![PARTITION]);
    assert!(frames.has_pending_staging(PARTITION));
    frames.finish_rebuild(PARTITION, frames.last_sequence());
    assert_eq!(frames.lock_tracking().repairs.len(), 0);
}

#[test]
fn staging_updates_do_not_overwrite_the_persisted_pending_run_gauge() {
    let frames = frames_with_limits(1, 1024 * 1024, 1, 2 * 1024 * 1024);
    frames.metrics.search_projection_repair_pending.set(17);
    commit_frame(&frames, 1);
    commit_frame(&frames, 2);
    assert_eq!(frames.lock_tracking().repairs.len(), 1);
    assert_eq!(frames.metrics.search_projection_repair_pending.get(), 17);
}
