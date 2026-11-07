// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The uploader of the history offload stream: it acknowledges sealed history segments, and
//! sometimes repeats or skips ahead as a stale owner would.

use dexzd_apply::read_model::read_history_segment_content;
use dexzd_history_segment::encode_history_segment;
use dexzd_keyspace::{FlowHash, RunId};
use dexzd_protocol::dexzd::v1::{HistorySegmentRecord, HistorySegmentUploadedCommand};

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
