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

/// A stalled projection, as during a SQL visibility outage, holds at most the byte limit in memory.
#[test]
fn queued_bytes_stay_bounded_while_the_projection_is_stalled() {
    let frame_bytes = DOCUMENT_BYTES + CHANGE_OVERHEAD_BYTES + 32;
    let frames = new_frames(1_000, 3 * frame_bytes);
    for run_number in 1..=20 {
        commit_frame(&frames, run_number);
    }
    assert_eq!(
        frames.lock_tracking().queue_order.len(),
        3,
        "frames beyond the byte limit are not queued"
    );
    assert!(frames.lock_tracking().queued_bytes <= 3 * frame_bytes);
    assert_eq!(frames.rebuild_requests(), vec![PARTITION]);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(0));

    // The projection recovers: it drains the queue, then rebuilds the partition.
    while let Some(frame) = take_frame(&frames) {
        frames.finish_projected_frame(&frame);
    }
    frames.finish_rebuild(PARTITION, frames.last_sequence());
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(20));
    assert_eq!(frames.lock_tracking().queued_bytes, 0);
    assert!(frames.rebuild_requests().is_empty());

    commit_frame(&frames, 21);
    assert_eq!(
        frames.lock_tracking().queue_order.len(),
        1,
        "freed bytes let new frames queue again"
    );
    assert!(frames.rebuild_requests().is_empty());
}

#[test]
fn frame_count_overflow_coalesces_into_a_partition_rebuild() {
    let max_queued_bytes = 1024 * 1024;
    let frames = new_frames(2, max_queued_bytes);
    for run_number in 1..=20 {
        commit_frame(&frames, run_number);
    }
    assert_eq!(frames.lock_tracking().queue_order.len(), 2);
    assert!(frames.lock_tracking().queued_bytes < max_queued_bytes);
    assert_eq!(frames.rebuild_requests(), vec![PARTITION]);
    assert_eq!(frames.lock_tracking().rebuild_requests.len(), 1);
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
    for sequence in 1..=2 {
        let frame = take_frame(&frames).unwrap();
        assert_eq!(frame.sequence, sequence);
        assert!(frame.changes.is_empty());
        frames.finish_projected_frame(&frame);
        frames.publish_projected_through();
        assert_eq!(*frames.projected_through.borrow(), Some(0));
    }
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
    let emptied = take_frame(&frames).unwrap();
    assert!(emptied.changes.is_empty());
    frames.finish_projected_frame(&emptied);
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
    assert_eq!(frames.metrics.search_projection_dropped_updates.get(), 1);
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
    assert_eq!(frames.metrics.search_projection_dropped_updates.get(), 0);
}

#[test]
fn coalesced_start_cannot_be_shed_as_an_intermediate() {
    let frames = frames_with_limits(1, 1024 * 1024, 4, 2 * 1024 * 1024);
    commit_frame(&frames, 1);
    record_document(&frames, 1, 7, FlowStatus::Running);
    finish_frame(&frames, 2);
    assert!(take_frame(&frames).unwrap().changes.is_empty());
    assert_eq!(take_frame(&frames).unwrap().changes.len(), 1);
    assert_eq!(frames.metrics.search_projection_dropped_updates.get(), 0);
    assert_eq!(frames.metrics.search_projection_coalesced_changes.get(), 1);
}

#[test]
fn bytes_trigger_shedding_and_protected_changes_use_the_hard_headroom() {
    let frames = frames_with_limits(100, DOCUMENT_BYTES, 200, 4 * DOCUMENT_BYTES);
    record_document(&frames, 1, 7, FlowStatus::Running);
    finish_frame(&frames, 1);
    assert!(take_frame(&frames).is_none());
    assert_eq!(frames.metrics.search_projection_dropped_updates.get(), 1);
    commit_frame(&frames, 2);
    record_document(&frames, 3, 8, FlowStatus::Completed);
    finish_frame(&frames, 3);
    record_delete(&frames, 4);
    finish_frame(&frames, 4);
    assert_eq!(frames.lock_tracking().queue_order.len(), 3);
    assert!(frames.rebuild_requests().is_empty());
}

#[test]
fn hard_limit_requests_rebuild_once_per_pending_partition_and_preserves_dependencies() {
    let frames = frames_with_limits(1, 1024 * 1024, 2, 2 * 1024 * 1024);
    commit_frame(&frames, 1);
    commit_frame(&frames, 2);
    record_document(&frames, 1, 8, FlowStatus::Completed);
    finish_frame(&frames, 3);
    assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 1);
    assert_eq!(
        frames.lock_tracking().rebuild_requests[&PARTITION].first_sequence,
        1
    );
    commit_frame(&frames, 4);
    assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 1);
    while let Some(frame) = take_frame(&frames) {
        frames.finish_projected_frame(&frame);
    }
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(0));
    frames.finish_rebuild(PARTITION, 4);
    frames.publish_projected_through();
    assert_eq!(*frames.projected_through.borrow(), Some(4));
    assert_eq!(frames.lock_tracking().queued_bytes, 0);
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
fn hard_byte_limit_rebuilds_instead_of_dropping_starts_closes_or_deletes() {
    for priority in [
        ChangePriority::Start,
        ChangePriority::Close,
        ChangePriority::Delete,
    ] {
        let frames = frames_with_limits(1, 1, 2, CHANGE_OVERHEAD_BYTES - 1);
        match priority {
            ChangePriority::Start => record_document(&frames, 1, 1, FlowStatus::Running),
            ChangePriority::Close => record_document(&frames, 1, 9, FlowStatus::Completed),
            ChangePriority::Delete => record_delete(&frames, 1),
            ChangePriority::Intermediate => unreachable!("only protected classes are tested"),
        }
        finish_frame(&frames, 1);
        assert_eq!(frames.rebuild_requests(), vec![PARTITION]);
        assert_eq!(frames.metrics.search_projection_rebuild_requests.get(), 1);
        assert_eq!(frames.metrics.search_projection_dropped_updates.get(), 0);
        assert!(take_frame(&frames).is_none());
    }
}
