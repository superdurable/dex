// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, Instant};

use async_trait::async_trait;
use dexzd_engine::CommittedFlowCommand;
use dexzd_keyspace::{PartitionId, RunId, TenantId};
use dexzd_metrics::{NodeMetrics, ProjectionDropReason};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::LogPosition;
use dexzd_search::sql_visibility::{
    ProjectionVisibilityWait, SearchDocumentChange, extract_search_document_changes,
};
use dexzd_search::{SearchError, SearchRequest};
use prost::Message;
use tokio::sync::{Notify, watch};

use crate::config::SearchProjectionQueueConfig;

/// Estimated memory of one change besides its encoded document: identifiers and enum layout.
const CHANGE_OVERHEAD_BYTES: usize = 64;

/// One committed partition frame's search document changes, numbered in commit order.
pub(crate) struct CommittedSearchFrame {
    pub(crate) sequence: u64,
    pub(crate) partition: PartitionId,
    pub(crate) position: LogPosition,
    pub(crate) changes: Vec<SearchDocumentChange>,
    /// Estimated memory of `changes`, counted against the queue's byte limit until projected.
    pub(crate) size_bytes: usize,
}

/// Feeds committed frames' search document changes to the SQL visibility projection without delaying commits.
pub(crate) struct CommittedSearchFrames {
    tracking: Mutex<FrameTracking>,
    cfg: Arc<SearchProjectionQueueConfig>,
    metrics: Arc<NodeMetrics>,
    available: Notify,
    rebuild_requested: Notify,
    /// Highest frame sequence searchable along with all earlier frames; `None` until startup catch-up.
    projected_through: watch::Sender<Option<u64>>,
    permanent_failure: watch::Sender<Option<String>>,
}

#[derive(Default)]
struct FrameTracking {
    open_frames: BTreeMap<PartitionId, OpenSearchFrame>,
    last_sequence: u64,
    /// Estimated bytes of frames queued or being projected.
    queued_bytes: usize,
    in_flight_upsert_bytes: usize,
    /// Open and queued frames; frames waiting for a rebuild move to `rebuild_requests`.
    unprojected: BTreeMap<u64, FrameWatermark>,
    pending_watermarks: BTreeSet<(u64, u64)>,
    queued: BTreeMap<u64, QueuedSearchFrame>,
    queued_upserts: BTreeMap<(TenantId, RunId), u64>,
    last_rebuild_warning: Option<Instant>,
    rebuild_requests: BTreeMap<PartitionId, RebuildRequest>,
}

/// Frame sequences of one partition that only a rebuild can project; a range bounds memory.
struct RebuildRequest {
    first_sequence: u64,
    last_sequence: u64,
}

struct OpenSearchFrame {
    sequence: u64,
    changes: BTreeMap<(TenantId, RunId), PrioritizedSearchChange>,
    is_unreadable: bool,
}

impl CommittedSearchFrames {
    /// Returns a queue bounded by frames and by bytes, including protected changes above the soft limits.
    pub(crate) fn new(cfg: Arc<SearchProjectionQueueConfig>, metrics: Arc<NodeMetrics>) -> Self {
        Self {
            tracking: Mutex::new(FrameTracking::default()),
            cfg,
            metrics,
            available: Notify::new(),
            rebuild_requested: Notify::new(),
            projected_through: watch::Sender::new(None),
            permanent_failure: watch::Sender::new(None),
        }
    }

    /// Adds one committed command's search document changes to its partition's open frame.
    pub(crate) fn record_command(&self, committed: &CommittedFlowCommand) {
        let partition = committed.context.partition;
        let extracted = extract_search_document_changes(partition, &committed.committed.outcome);
        if matches!(&extracted, Ok(changes) if changes.is_empty()) {
            return;
        }
        match extracted {
            Ok(changes) => {
                let changes: Vec<_> = changes
                    .into_iter()
                    .map(|change| PrioritizedSearchChange {
                        priority: change_priority(&change),
                        change,
                    })
                    .collect();
                let mut tracking = self.lock_tracking();
                let frame = tracking.open_frame(partition);
                for mut prioritized in changes {
                    let identity = change_identity(&prioritized.change);
                    if let Some(previous) = frame.changes.remove(&identity) {
                        prioritized.inherit_priority(&previous);
                        self.metrics.search_projection_coalesced_changes.inc();
                    }
                    frame.changes.insert(identity, prioritized);
                }
            }
            Err(error) => {
                tracing::error!(
                    partition = partition.0,
                    %error,
                    "a committed outcome has unreadable search documents; rebuilding its partition"
                );
                self.lock_tracking().open_frame(partition).is_unreadable = true;
            }
        }
    }

    /// Queues the partition's open frame for projection, or requests a rebuild when it cannot queue.
    pub(crate) fn finish_frame(&self, partition: PartitionId, position: &LogPosition) {
        let mut tracking = self.lock_tracking();
        let Some(frame) = tracking.open_frames.remove(&partition) else {
            return;
        };
        if frame.is_unreadable {
            self.request_rebuild(&mut tracking, partition, frame.sequence);
            return;
        }
        let mut queued = QueuedSearchFrame {
            sequence: frame.sequence,
            partition,
            position: position.clone(),
            changes: frame.changes,
        };
        tracking.coalesce_upserts(&mut queued, &self.metrics);
        let would_exceed_soft_limit = tracking.queued.len() >= self.cfg.soft_frames
            || tracking.queued_bytes.saturating_add(queued.size_bytes()) > self.cfg.soft_bytes;
        if would_exceed_soft_limit {
            queued.drop_priority(ChangePriority::Intermediate, &self.metrics);
        }
        let has_delete = queued
            .changes
            .values()
            .any(|change| change.priority == ChangePriority::Delete);
        for priority in [
            ChangePriority::Intermediate,
            ChangePriority::Close,
            ChangePriority::Start,
        ] {
            if !tracking.exceeds_hard_limit(&queued, &self.cfg) {
                break;
            }
            queued.drop_priority(priority, &self.metrics);
            if has_delete {
                tracking.reclaim_queued_priority(&queued, priority, &self.cfg, &self.metrics);
            }
        }
        if queued.changes.is_empty() {
            tracking.remove_unprojected(queued.sequence);
            self.available.notify_one();
            return;
        }
        if tracking.exceeds_hard_limit(&queued, &self.cfg) {
            self.request_rebuild(&mut tracking, partition, queued.sequence);
            return;
        }
        for (identity, change) in &queued.changes {
            if matches!(change.change, SearchDocumentChange::Upsert { .. }) {
                tracking.queued_upserts.insert(*identity, queued.sequence);
            }
        }
        tracking.queued_bytes += queued.size_bytes();
        tracking.queued.insert(queued.sequence, queued);
        self.available.notify_one();
    }

    pub(crate) async fn next_frame(&self) -> CommittedSearchFrame {
        loop {
            let available = self.available.notified();
            if let Some(frame) = self.lock_tracking().take_frame() {
                return frame;
            }
            self.publish_projected_through();
            available.await;
        }
    }

    pub(crate) fn rebuild_requested(&self) -> &Notify {
        &self.rebuild_requested
    }

    /// Returns the newest frame sequence handed to the projection.
    pub(crate) fn last_sequence(&self) -> u64 {
        self.lock_tracking().last_sequence
    }

    pub(crate) fn rebuild_requests(&self) -> Vec<PartitionId> {
        self.lock_tracking()
            .rebuild_requests
            .keys()
            .copied()
            .collect()
    }

    pub(crate) fn has_rebuild_request(&self, partition: PartitionId) -> bool {
        self.lock_tracking()
            .rebuild_requests
            .contains_key(&partition)
    }

    /// Marks a dequeued frame searchable and releases its bytes from the queue's limit.
    pub(crate) fn finish_projected_frame(&self, frame: &CommittedSearchFrame) {
        let mut tracking = self.lock_tracking();
        tracking.remove_unprojected(frame.sequence);
        tracking.queued_bytes = tracking.queued_bytes.saturating_sub(frame.size_bytes);
        tracking.in_flight_upsert_bytes -= frame
            .changes
            .iter()
            .filter(|change| matches!(change, SearchDocumentChange::Upsert { .. }))
            .map(change_size_bytes)
            .sum::<usize>();
    }

    /// Marks the partition's frames through `sequence` searchable after a rebuild.
    pub(crate) fn finish_rebuild(&self, partition: PartitionId, sequence: u64) {
        let mut tracking = self.lock_tracking();
        tracking.finish_through(Some(partition), sequence);
        if let Some(request) = tracking.rebuild_requests.get_mut(&partition) {
            if request.last_sequence <= sequence {
                tracking.rebuild_requests.remove(&partition);
            } else {
                request.first_sequence = request.first_sequence.max(sequence + 1);
            }
        }
    }

    /// Marks every frame through `sequence` searchable once all partitions caught up after startup.
    pub(crate) fn finish_catch_up(&self, sequence: u64) {
        let mut tracking = self.lock_tracking();
        tracking.finish_through(None, sequence);
        tracking.rebuild_requests.retain(|_, request| {
            request.first_sequence = request.first_sequence.max(sequence + 1);
            request.last_sequence > sequence
        });
    }

    /// Lets read-your-writes waits pass for every frame the projection has made searchable.
    pub(crate) fn publish_projected_through(&self) {
        let projected_through = {
            let tracking = self.lock_tracking();
            let first_unprojected = tracking.pending_watermarks.first().map(|(first, _)| *first);
            let first_to_rebuild = tracking
                .rebuild_requests
                .values()
                .map(|request| request.first_sequence)
                .min();
            match first_unprojected.into_iter().chain(first_to_rebuild).min() {
                Some(sequence) => sequence - 1,
                None => tracking.last_sequence,
            }
        };
        self.projected_through.send_if_modified(|published| {
            let is_newer = published.is_none_or(|published| published < projected_through);
            if is_newer {
                *published = Some(projected_through);
            }
            is_newer
        });
    }

    pub(crate) fn record_permanent_failure(&self, partition: PartitionId, error: &str) {
        let reason = format!(
            "SQL visibility projection partition {} failed: {error}",
            partition.0
        );
        tracing::error!(partition = partition.0, %error, "the search projection stopped");
        self.permanent_failure.send_replace(Some(reason));
    }

    fn request_rebuild(&self, tracking: &mut FrameTracking, partition: PartitionId, sequence: u64) {
        let first_sequence = tracking.remove_unprojected(sequence).unwrap_or(sequence);
        match tracking.rebuild_requests.get_mut(&partition) {
            Some(request) => {
                request.first_sequence = request.first_sequence.min(first_sequence);
                request.last_sequence = request.last_sequence.max(sequence);
            }
            None => {
                self.metrics.search_projection_rebuild_requests.inc();
                let now = Instant::now();
                if tracking
                    .last_rebuild_warning
                    .is_none_or(|last| now.duration_since(last) >= Duration::from_secs(1))
                {
                    tracing::warn!(
                        partition = partition.0,
                        "a committed frame could not be queued for the search projection; rebuilding its partition"
                    );
                    tracking.last_rebuild_warning = Some(now);
                }
                tracking.rebuild_requests.insert(
                    partition,
                    RebuildRequest {
                        first_sequence,
                        last_sequence: sequence,
                    },
                );
            }
        }
        self.rebuild_requested.notify_one();
    }

    fn lock_tracking(&self) -> MutexGuard<'_, FrameTracking> {
        self.tracking
            .lock()
            .expect("frame tracking lock is poisoned only if a holder panicked")
    }
}

impl FrameTracking {
    fn open_frame(&mut self, partition: PartitionId) -> &mut OpenSearchFrame {
        if !self.open_frames.contains_key(&partition) {
            self.last_sequence += 1;
            self.unprojected.insert(
                self.last_sequence,
                FrameWatermark {
                    partition,
                    first_sequence: self.last_sequence,
                },
            );
            self.pending_watermarks
                .insert((self.last_sequence, self.last_sequence));
        }
        let sequence = self.last_sequence;
        self.open_frames
            .entry(partition)
            .or_insert_with(|| OpenSearchFrame {
                sequence,
                changes: BTreeMap::new(),
                is_unreadable: false,
            })
    }

    fn coalesce_upserts(&mut self, frame: &mut QueuedSearchFrame, metrics: &NodeMetrics) {
        for (identity, change) in &mut frame.changes {
            let Some(previous_sequence) = self.queued_upserts.remove(identity) else {
                continue;
            };
            let previous = self
                .queued
                .get_mut(&previous_sequence)
                .expect("indexed upsert belongs to a queued frame")
                .changes
                .remove(identity)
                .expect("indexed upsert is still queued");
            self.queued_bytes -= change_size_bytes(&previous.change);
            change.inherit_priority(&previous);
            metrics.search_projection_coalesced_changes.inc();
            if let Some(previous_watermark) = self.unprojected.get(&previous_sequence) {
                let first_sequence = previous_watermark.first_sequence;
                let watermark = self
                    .unprojected
                    .get_mut(&frame.sequence)
                    .expect("an admitted frame has a watermark");
                self.pending_watermarks
                    .remove(&(watermark.first_sequence, frame.sequence));
                watermark.first_sequence = watermark.first_sequence.min(first_sequence);
                self.pending_watermarks
                    .insert((watermark.first_sequence, frame.sequence));
            }
            if self.queued[&previous_sequence].changes.is_empty() {
                self.queued.remove(&previous_sequence);
                self.remove_unprojected(previous_sequence);
            }
        }
    }

    fn exceeds_hard_limit(
        &self,
        frame: &QueuedSearchFrame,
        cfg: &SearchProjectionQueueConfig,
    ) -> bool {
        if frame.changes.is_empty() {
            return false;
        }
        let has_only_deletes = frame
            .changes
            .values()
            .all(|change| change.priority == ChangePriority::Delete);
        // Delete admission may exceed total bytes by at most the worker's in-flight Upserts.
        let counted_bytes = if has_only_deletes {
            self.queued_bytes - self.in_flight_upsert_bytes
        } else {
            self.queued_bytes
        };
        self.queued.len() >= cfg.hard_frames
            || counted_bytes.saturating_add(frame.size_bytes()) > cfg.hard_bytes
    }

    fn reclaim_queued_priority(
        &mut self,
        incoming: &QueuedSearchFrame,
        priority: ChangePriority,
        cfg: &SearchProjectionQueueConfig,
        metrics: &NodeMetrics,
    ) {
        let sequences: Vec<_> = self.queued.keys().copied().collect();
        for sequence in sequences {
            if !self.exceeds_hard_limit(incoming, cfg) {
                break;
            }
            let frame = self.queued.get_mut(&sequence).expect("queued frame exists");
            let identities: Vec<_> = frame
                .changes
                .iter()
                .filter(|(_, change)| change.priority == priority)
                .map(|(identity, _)| *identity)
                .collect();
            for identity in identities {
                let change = self
                    .queued
                    .get_mut(&sequence)
                    .expect("queued frame exists")
                    .changes
                    .remove(&identity)
                    .expect("selected change exists");
                self.queued_bytes -= change_size_bytes(&change.change);
                self.queued_upserts.remove(&identity);
                metrics
                    .search_projection_dropped_updates
                    .record(priority.drop_reason());
                if self.queued[&sequence].changes.is_empty() {
                    self.queued.remove(&sequence);
                    self.remove_unprojected(sequence);
                    break;
                }
                if !self.exceeds_hard_limit(incoming, cfg) {
                    break;
                }
            }
        }
    }

    fn take_frame(&mut self) -> Option<CommittedSearchFrame> {
        let (sequence, queued) = self.queued.pop_first()?;
        for identity in queued.changes.keys() {
            if self.queued_upserts.get(identity) == Some(&sequence) {
                self.queued_upserts.remove(identity);
            }
        }
        let size_bytes = queued.size_bytes();
        self.in_flight_upsert_bytes += queued
            .changes
            .values()
            .filter(|change| matches!(change.change, SearchDocumentChange::Upsert { .. }))
            .map(|change| change_size_bytes(&change.change))
            .sum::<usize>();
        Some(CommittedSearchFrame {
            sequence,
            partition: queued.partition,
            position: queued.position,
            size_bytes,
            changes: queued
                .changes
                .into_values()
                .map(|change| change.change)
                .collect(),
        })
    }

    fn finish_through(&mut self, partition: Option<PartitionId>, sequence: u64) {
        self.unprojected.retain(|frame_sequence, watermark| {
            let is_covered = *frame_sequence <= sequence
                && partition.is_none_or(|partition| watermark.partition == partition);
            if is_covered {
                self.pending_watermarks
                    .remove(&(watermark.first_sequence, *frame_sequence));
            }
            !is_covered
        });
    }

    fn remove_unprojected(&mut self, sequence: u64) -> Option<u64> {
        let watermark = self.unprojected.remove(&sequence)?;
        self.pending_watermarks
            .remove(&(watermark.first_sequence, sequence));
        Some(watermark.first_sequence)
    }
}

#[async_trait]
impl ProjectionVisibilityWait for CommittedSearchFrames {
    async fn wait_until_visible(&self, _request: SearchRequest<'_>) -> Result<(), SearchError> {
        let mut permanent_failure = self.permanent_failure.subscribe();
        if let Some(reason) = permanent_failure.borrow().as_ref() {
            return Err(SearchError::Internal(reason.clone()));
        }
        let required_sequence = self.last_sequence();
        let mut projected_through = self.projected_through.subscribe();
        let result = tokio::select! {
            projected = projected_through.wait_for(|projected| projected.is_some_and(|through| through >= required_sequence)) => {
                projected.map(|_| ()).map_err(|_| SearchError::Unavailable("the search projection stopped".to_owned()))
            }
            failed = permanent_failure.wait_for(Option::is_some) => {
                let reason = failed.map_err(|_| SearchError::Unavailable("the search projection stopped".to_owned()))?;
                Err(SearchError::Internal(reason.as_ref().expect("failure notification carries a reason").clone()))
            }
        };
        match permanent_failure.borrow().as_ref() {
            Some(reason) => Err(SearchError::Internal(reason.clone())),
            None => result,
        }
    }
}

struct FrameWatermark {
    partition: PartitionId,
    first_sequence: u64,
}

struct QueuedSearchFrame {
    sequence: u64,
    partition: PartitionId,
    position: LogPosition,
    changes: BTreeMap<(TenantId, RunId), PrioritizedSearchChange>,
}

impl QueuedSearchFrame {
    fn drop_priority(&mut self, priority: ChangePriority, metrics: &NodeMetrics) {
        self.changes.retain(|_, change| {
            if change.priority == priority {
                metrics
                    .search_projection_dropped_updates
                    .record(priority.drop_reason());
                false
            } else {
                true
            }
        });
    }

    fn size_bytes(&self) -> usize {
        self.changes
            .values()
            .map(|change| change_size_bytes(&change.change))
            .sum()
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum ChangePriority {
    Start,
    Delete,
    Close,
    Intermediate,
}

impl ChangePriority {
    fn drop_reason(self) -> ProjectionDropReason {
        match self {
            Self::Intermediate => ProjectionDropReason::Intermediate,
            Self::Close => ProjectionDropReason::Close,
            Self::Start => ProjectionDropReason::Start,
            Self::Delete => panic!("Deletes are never shed"),
        }
    }
}

struct PrioritizedSearchChange {
    change: SearchDocumentChange,
    priority: ChangePriority,
}

// Classification skips document values already validated by extraction, avoiding a second allocation-heavy decode.
#[derive(Message)]
struct SearchChangeState {
    #[prost(uint64, tag = "1")]
    version: u64,
    #[prost(int32, tag = "6")]
    status: i32,
}

impl PrioritizedSearchChange {
    fn inherit_priority(&mut self, previous: &Self) {
        // A newer Running document also carries a queued start or a reopened run's protected state.
        if self.priority == ChangePriority::Intermediate {
            self.priority = previous.priority;
        }
    }
}

fn change_identity(change: &SearchDocumentChange) -> (TenantId, RunId) {
    match change {
        SearchDocumentChange::Upsert {
            tenant_id, run_id, ..
        }
        | SearchDocumentChange::Delete { tenant_id, run_id } => (*tenant_id, *run_id),
        _ => panic!("every extracted change has a run identity"),
    }
}

fn change_priority(change: &SearchDocumentChange) -> ChangePriority {
    match change {
        SearchDocumentChange::Delete { .. } => ChangePriority::Delete,
        SearchDocumentChange::Upsert {
            encoded_document, ..
        } => {
            let document = SearchChangeState::decode(encoded_document.as_slice())
                .expect("extraction has already decoded the committed search document");
            // The first run command creates the document, including child starts.
            if document.version == 1 {
                ChangePriority::Start
            } else if document.status == FlowStatus::Running as i32 {
                ChangePriority::Intermediate
            } else {
                ChangePriority::Close
            }
        }
        _ => panic!("every extracted change has a projection priority"),
    }
}

fn change_size_bytes(change: &SearchDocumentChange) -> usize {
    let document_bytes = match change {
        SearchDocumentChange::Upsert {
            encoded_document, ..
        } => encoded_document.len(),
        _ => 0,
    };
    CHANGE_OVERHEAD_BYTES + document_bytes
}

#[cfg(test)]
#[path = "committed_search_frames_tests.rs"]
mod tests;
