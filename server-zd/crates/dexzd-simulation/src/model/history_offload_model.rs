// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The uploader of the history offload stream: it acknowledges sealed history segments, records
//! deleted objects of expired ones, and sometimes repeats or skips ahead as a stale owner would.
//! A client of the stream turns runs' delete switches on and off. The owner also proposes each
//! run's local history cleanup.

use dexzd_apply::is_expired_history_object_deletion_enabled;
use dexzd_apply::read_model::read_history_segment_content;
use dexzd_history_segment::encode_history_segment;
use dexzd_keyspace::{FlowHash, RunId};
use dexzd_protocol::dexzd::v1::{
    HistorySegmentObjectsDeletedCommand, HistorySegmentRecord, HistorySegmentState,
    HistorySegmentUploadedCommand,
};

use super::Proposal;
use crate::apply_checker::ApplyChecker;
use crate::invariants::InvariantViolation;
use crate::state_view::FlowView;

/// Returns the acknowledgments the uploader may propose for the view's runs: the oldest SEALED
/// segment, which apply accepts, a later SEALED one, and a repeat of the newest UPLOADED one.
pub(super) fn history_segment_acknowledgments<'view>(
    view: &FlowView,
    checker: &ApplyChecker,
) -> Result<Vec<(u32, Proposal<'view>)>, InvariantViolation> {
    let Some((_, flow_hash)) = view.location else {
        return Ok(Vec::new());
    };
    let mut proposals = Vec::new();
    for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
        let Some(record) = run.record.as_ref().filter(|record| !record.is_purging) else {
            continue;
        };
        let uploaded_through_seq = record.history_uploaded_through_seq;
        let mut sealed = run
            .history_segments
            .values()
            .filter(|segment| segment.first_seq > uploaded_through_seq);
        if let Some(oldest) = sealed.next() {
            proposals.push((
                6,
                encoded_acknowledgment(checker, flow_hash, *run_id, oldest)?,
            ));
        }
        if let Some(later) = sealed.next() {
            proposals.push((
                1,
                encoded_acknowledgment(checker, flow_hash, *run_id, later)?,
            ));
        }
        // Rows of an uploaded segment may be pruned, so a repeat reuses its recorded checksum.
        if let Some(uploaded) = run
            .history_segments
            .values()
            .rfind(|segment| segment.last_seq <= uploaded_through_seq)
        {
            proposals.push((
                1,
                Proposal::AcknowledgeHistorySegment {
                    run_id: *run_id,
                    acknowledgment: HistorySegmentUploadedCommand {
                        first_seq: uploaded.first_seq,
                        last_seq: uploaded.last_seq,
                        content_checksum: uploaded.content_checksum.clone(),
                        object_size_bytes: uploaded.object_size_bytes,
                    },
                },
            ));
        }
    }
    Ok(proposals)
}

/// Encodes the segment from committed state, as the uploader does, and acknowledges it.
fn encoded_acknowledgment<'view>(
    checker: &ApplyChecker,
    flow_hash: FlowHash,
    run_id: RunId,
    segment: &HistorySegmentRecord,
) -> Result<Proposal<'view>, InvariantViolation> {
    let content =
        read_history_segment_content(checker.state(), flow_hash, run_id, segment.first_seq)
            .map_err(|error| InvariantViolation::new("H6-segment-content", error.to_string()))?
            .ok_or_else(|| {
                InvariantViolation::new(
                    "H6-segment-content",
                    format!("run {run_id} has no segment at seq {}", segment.first_seq),
                )
            })?;
    let encoded = encode_history_segment(&content)
        .map_err(|error| InvariantViolation::new("H6-segment-content", error.to_string()))?;
    Ok(Proposal::AcknowledgeHistorySegment {
        run_id,
        acknowledgment: HistorySegmentUploadedCommand {
            first_seq: segment.first_seq,
            last_seq: segment.last_seq,
            content_checksum: encoded.content_checksum.to_vec(),
            object_size_bytes: encoded.bytes.len() as u64,
        },
    })
}

/// Returns the local history cleanup of each of the view's runs that has one pending.
pub(super) fn local_cleanup_proposals<'view>(view: &FlowView) -> Vec<(u32, Proposal<'view>)> {
    view.runs
        .iter()
        .filter(|(_, run)| run.is_complete)
        .filter_map(|(run_id, run)| {
            let due_micros = run.record.as_ref()?.history_local_cleanup_due_micros?;
            Some((
                1,
                Proposal::CleanUpLocalHistory {
                    run_id: *run_id,
                    due_micros,
                },
            ))
        })
        .collect()
}

/// Returns the deletions the owner may record for the view's runs whose delete switch is on: the
/// oldest EXPIRED segments, which apply accepts, one that skips the oldest, and a repeat of a
/// name already deleted. Also
/// returns a switch of the latest run's delete switch to its other value, except while the run
/// migrates, so that switching leaves migrations to the stream's other commands.
pub(super) fn expired_object_proposals<'view>(view: &FlowView) -> Vec<(u32, Proposal<'view>)> {
    let mut proposals = Vec::new();
    let latest_run_id = view.latest_run().map(|(run_id, _)| run_id);
    for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
        let Some(record) = run.record.as_ref().filter(|record| !record.is_purging) else {
            continue;
        };
        let is_switchable = record.history_open_segment_first_seq != 0
            && record.history_migration_started_seq == 0
            && latest_run_id == Some(*run_id);
        if is_switchable {
            proposals.push((
                1,
                Proposal::SetExpiredObjectDeletion {
                    run_id: *run_id,
                    is_enabled: !is_expired_history_object_deletion_enabled(record),
                },
            ));
        }
        // The owner deletes objects only while the run's delete switch is on.
        if !is_expired_history_object_deletion_enabled(record) {
            continue;
        }
        let expired: Vec<u64> = run
            .history_segments
            .values()
            .filter(|segment| segment.state == HistorySegmentState::Expired as i32)
            .map(|segment| segment.first_seq)
            .collect();
        let delete = |first_seqs: Vec<u64>| Proposal::RecordDeletedHistoryObjects {
            run_id: *run_id,
            deleted: HistorySegmentObjectsDeletedCommand { first_seqs },
        };
        if !expired.is_empty() {
            proposals.push((2, delete(expired.iter().copied().take(3).collect())));
        }
        if expired.len() > 1 {
            proposals.push((1, delete(expired[1..].to_vec())));
        }
        if record.history_objects_deleted_through_seq > 0 {
            proposals.push((1, delete(vec![1])));
        }
    }
    proposals
}
