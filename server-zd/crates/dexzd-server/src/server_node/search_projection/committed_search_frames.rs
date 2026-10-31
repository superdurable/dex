// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex, MutexGuard};

use async_trait::async_trait;
use dexzd_apply::StateWrite;
use dexzd_engine::CommittedFlowCommand;
use dexzd_keyspace::{
    ColumnFamily, FlowHash, PartitionId, RunId, RunTable, TenantId, parse_run_key,
};
use dexzd_metrics::{NodeMetrics, ProjectionDropReason, ProjectionRebuildReason};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::LogPosition;
use dexzd_search::sql_visibility::{
    ProjectionVisibilityWait, SearchDocumentChange, extract_search_document_changes,
};
use dexzd_search::{SearchError, SearchRequest};
use prost::Message;
use tokio::sync::{Notify, watch};

use crate::config::SearchProjectionQueueConfig;

use super::projection_repairs::{ProjectionRepair, ProjectionRepairs, RepairPriority};

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
    repair_available: Notify,
    /// Highest frame sequence searchable along with all earlier frames; `None` until startup catch-up.
    projected_through: watch::Sender<Option<u64>>,
    permanent_failure: watch::Sender<Option<String>>,
}

#[derive(Default)]
struct FrameTracking {
    open_frames: BTreeMap<PartitionId, OpenSearchFrame>,
    last_sequence: u64,
    latest_positions: BTreeMap<PartitionId, LogPosition>,
    /// Estimated bytes of frames queued or being projected.
    queued_bytes: usize,
    in_flight_upsert_bytes: usize,
    /// Open and queued frames; frames waiting for a rebuild move to `rebuild_requests`.
    unprojected: BTreeMap<u64, FrameWatermark>,
    pending_watermarks: BTreeSet<(u64, u64)>,
    queued: BTreeMap<u64, QueuedSearchFrame>,
    queued_upserts: BTreeMap<(TenantId, RunId), u64>,
    rebuild_requests: BTreeMap<PartitionId, RebuildRequest>,
    repairs: ProjectionRepairs,
    diff_needed: BTreeMap<PartitionId, u64>,
    rebuild_reasons: BTreeMap<PartitionId, BTreeSet<ProjectionRebuildReason>>,
    in_progress: BTreeSet<PartitionId>,
    quarantined: BTreeSet<PartitionId>,
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
            repair_available: Notify::new(),
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
                        flow_hash: change_flow_hash(committed, &change),
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
                tracing::debug!(
                    partition = partition.0,
                    %error,
                    "a committed outcome has unreadable search documents; manual repair is required"
                );
                self.lock_tracking().open_frame(partition).is_unreadable = true;
            }
        }
    }

    /// Queues the partition's open frame for projection, or records repairs when it sheds changes.
    pub(crate) fn finish_frame(&self, partition: PartitionId, position: &LogPosition) {
        let mut tracking = self.lock_tracking();
        tracking
            .latest_positions
            .insert(partition, position.clone());
        let Some(frame) = tracking.open_frames.remove(&partition) else {
            return;
        };
        if frame.is_unreadable {
            self.mark_needed(
                &mut tracking,
                partition,
                ProjectionRebuildReason::Unreadable,
            );
            tracking.quarantined.insert(partition);
            tracking.remove_unprojected(frame.sequence);
            return;
        }
        let mut queued = QueuedSearchFrame {
            sequence: frame.sequence,
            partition,
            position: position.clone(),
            changes: frame.changes,
        };
        tracking.coalesce_upserts(&mut queued, &self.metrics);
        let is_over_soft_limit = tracking.queued.len() >= self.cfg.soft_frames
            || tracking.queued_bytes.saturating_add(queued.size_bytes()) > self.cfg.soft_bytes;
        if is_over_soft_limit {
            self.drop_priority(&mut tracking, &mut queued, ChangePriority::Intermediate);
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
            self.drop_priority(&mut tracking, &mut queued, priority);
            if has_delete {
                self.reclaim_queued_priority(&mut tracking, &queued, priority);
            }
        }
        if queued.changes.is_empty() {
            tracking.remove_unprojected(queued.sequence);
            self.available.notify_one();
            return;
        }
        if tracking.exceeds_hard_limit(&queued, &self.cfg) {
            self.metrics.search_projection_delete_overflow.inc();
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

    #[cfg(test)]
    fn take_repair_batch(&self) -> Vec<ProjectionRepair> {
        let mut tracking = self.lock_tracking();
        if !tracking.queued.is_empty() || tracking.queued_bytes != 0 {
            return Vec::new();
        }
        tracking.repairs.take_batch(self.cfg.repair_batch_runs)
    }

    #[cfg(test)]
    pub(crate) fn finish_repair_batch(
        &self,
        repairs: &[ProjectionRepair],
        projected: &BTreeSet<(TenantId, RunId)>,
    ) {
        let mut tracking = self.lock_tracking();
        tracking.repairs.finish_batch(repairs);
        for repair in repairs {
            if projected.contains(&(repair.tenant_id, repair.run_id)) {
                self.metrics
                    .search_projection_repaired_runs
                    .record(repair.priority.reason());
            }
        }
        self.repair_available.notify_one();
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
        self.repair_available.notify_one();
    }

    /// Marks the partition's frames through `sequence` searchable after a rebuild.
    pub(crate) fn finish_rebuild(&self, partition: PartitionId, sequence: u64) {
        let mut tracking = self.lock_tracking();
        tracking.in_progress.remove(&partition);
        tracking.rebuild_reasons.remove(&partition);
        tracking.quarantined.remove(&partition);
        self.metrics
            .search_projection_partitions_needing_rebuild
            .set(i64::try_from(tracking.rebuild_reasons.len()).expect("partition count fits i64"));
        tracking.finish_through(Some(partition), sequence);
        tracking.repairs.finish_through(Some(partition), sequence);
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
        tracking.repairs.finish_through(None, sequence);
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
        self.metrics.search_projection_permanent_failures.inc();
        self.permanent_failure.send_replace(Some(reason));
    }

    pub(crate) fn record_global_failure(&self, error: &str) {
        tracing::error!(%error, "the search projection stopped");
        self.metrics.search_projection_permanent_failures.inc();
        self.permanent_failure
            .send_replace(Some(format!("SQL visibility projection failed: {error}")));
    }

    pub(crate) fn is_partition_idle(&self, partition: PartitionId) -> bool {
        let tracking = self.lock_tracking();
        !tracking.open_frames.contains_key(&partition)
            && !tracking
                .unprojected
                .values()
                .any(|frame| frame.partition == partition)
    }

    fn drop_priority(
        &self,
        tracking: &mut FrameTracking,
        frame: &mut QueuedSearchFrame,
        priority: ChangePriority,
    ) {
        let identities: Vec<_> = frame
            .changes
            .iter()
            .filter(|(_, change)| change.priority == priority)
            .map(|(identity, _)| *identity)
            .collect();
        for identity in identities {
            let change = frame
                .changes
                .remove(&identity)
                .expect("selected change exists");
            self.record_dropped(tracking, frame.sequence, frame.partition, identity, &change);
        }
    }

    fn reclaim_queued_priority(
        &self,
        tracking: &mut FrameTracking,
        incoming: &QueuedSearchFrame,
        priority: ChangePriority,
    ) {
        let sequences: Vec<_> = tracking.queued.keys().copied().collect();
        for sequence in sequences {
            if !tracking.exceeds_hard_limit(incoming, &self.cfg) {
                break;
            }
            let frame = tracking
                .queued
                .get_mut(&sequence)
                .expect("queued frame exists");
            let partition = frame.partition;
            let identities: Vec<_> = frame
                .changes
                .iter()
                .filter(|(_, change)| change.priority == priority)
                .map(|(identity, _)| *identity)
                .collect();
            for identity in identities {
                let change = tracking
                    .queued
                    .get_mut(&sequence)
                    .expect("queued frame exists")
                    .changes
                    .remove(&identity)
                    .expect("selected change exists");
                tracking.queued_bytes -= change_size_bytes(&change.change);
                tracking.queued_upserts.remove(&identity);
                self.record_dropped(tracking, sequence, partition, identity, &change);
                if tracking.queued[&sequence].changes.is_empty() {
                    tracking.queued.remove(&sequence);
                    tracking.remove_unprojected(sequence);
                    break;
                }
                if !tracking.exceeds_hard_limit(incoming, &self.cfg) {
                    break;
                }
            }
        }
    }

    fn record_dropped(
        &self,
        tracking: &mut FrameTracking,
        _sequence: u64,
        partition: PartitionId,
        identity: (TenantId, RunId),
        change: &PrioritizedSearchChange,
    ) {
        self.metrics
            .search_projection_dropped_updates
            .record(change.priority.drop_reason());
        let priority = match change.priority {
            ChangePriority::Start => RepairPriority::Start,
            ChangePriority::Close => RepairPriority::Close,
            ChangePriority::Intermediate => RepairPriority::Intermediate,
            ChangePriority::Delete => panic!("Deletes are never shed"),
        };
        let repair = ProjectionRepair::new(
            identity.0,
            identity.1,
            change
                .flow_hash
                .expect("Upserts retain their committed Flow hash"),
            partition,
            priority,
        );
        if !tracking
            .repairs
            .record(repair, tracking.last_sequence, self.cfg.max_repair_entries)
        {
            self.metrics.search_projection_repair_overflow.inc();
            tracking.require_diff(partition);
        }
        self.repair_available.notify_one();
    }

    pub(crate) fn schedule_rebuilds(&self, partitions: &[PartitionId]) -> Vec<PartitionId> {
        let mut tracking = self.lock_tracking();
        let sequence = tracking.last_sequence;
        let mut scheduled = Vec::new();
        for partition in partitions {
            if tracking.rebuild_requests.contains_key(partition) {
                continue;
            }
            tracking.rebuild_requests.insert(
                *partition,
                RebuildRequest {
                    first_sequence: sequence + 1,
                    last_sequence: sequence,
                },
            );
            self.metrics.search_projection_rebuild_requests.inc();
            scheduled.push(*partition);
        }
        self.rebuild_requested.notify_one();
        scheduled
    }

    pub(crate) fn rebuild_status(
        &self,
    ) -> (
        BTreeMap<PartitionId, BTreeSet<ProjectionRebuildReason>>,
        BTreeSet<PartitionId>,
        BTreeSet<PartitionId>,
    ) {
        let tracking = self.lock_tracking();
        (
            tracking.rebuild_reasons.clone(),
            tracking.rebuild_requests.keys().copied().collect(),
            tracking.in_progress.clone(),
        )
    }

    pub(crate) fn fail_rebuild(&self, partition: PartitionId) {
        let mut tracking = self.lock_tracking();
        tracking.in_progress.remove(&partition);
        tracking.rebuild_requests.remove(&partition);
        self.mark_needed(
            &mut tracking,
            partition,
            ProjectionRebuildReason::Unreadable,
        );
        tracking.quarantined.insert(partition);
    }

    pub(crate) fn start_rebuild(&self, partition: PartitionId) {
        self.lock_tracking().in_progress.insert(partition);
    }

    pub(crate) fn has_live_frames(&self) -> bool {
        let tracking = self.lock_tracking();
        !tracking.queued.is_empty()
            || tracking.queued_bytes != 0
            || !tracking.open_frames.is_empty()
    }

    pub(crate) fn quarantined_partitions(&self) -> BTreeSet<PartitionId> {
        self.lock_tracking().quarantined.clone()
    }

    pub(crate) fn is_quarantined(&self, partition: PartitionId) -> bool {
        self.lock_tracking().quarantined.contains(&partition)
    }

    pub(crate) fn quarantine(&self, partition: PartitionId) {
        self.lock_tracking().quarantined.insert(partition);
    }

    pub(crate) fn take_staged_repairs(&self) -> Vec<ProjectionRepair> {
        self.lock_tracking().repairs.drain()
    }

    pub(crate) fn has_pending_staging(&self, partition: PartitionId) -> bool {
        let tracking = self.lock_tracking();
        tracking.repairs.has_partition(partition) || tracking.diff_needed.contains_key(&partition)
    }

    pub(crate) fn diff_partitions(&self) -> Vec<(PartitionId, u64)> {
        self.lock_tracking()
            .diff_needed
            .iter()
            .map(|(partition, generation)| (*partition, *generation))
            .collect()
    }

    pub(crate) fn require_diff(&self, partition: PartitionId) {
        self.lock_tracking().require_diff(partition);
    }

    pub(crate) fn finish_diff(&self, partition: PartitionId, generation: u64) {
        let mut tracking = self.lock_tracking();
        if tracking.diff_needed.get(&partition) == Some(&generation) {
            tracking.diff_needed.remove(&partition);
        }
    }

    pub(crate) fn mark_rebuild_needed(
        &self,
        partition: PartitionId,
        reason: ProjectionRebuildReason,
    ) {
        self.mark_needed(&mut self.lock_tracking(), partition, reason);
    }

    fn mark_needed(
        &self,
        tracking: &mut FrameTracking,
        partition: PartitionId,
        reason: ProjectionRebuildReason,
    ) {
        if tracking
            .rebuild_reasons
            .entry(partition)
            .or_default()
            .insert(reason)
        {
            self.metrics.search_projection_rebuild_needed.record(reason);
            tracing::warn!(
                partition = partition.0,
                ?reason,
                "search projection partition needs an operator rebuild"
            );
            self.metrics
                .search_projection_partitions_needing_rebuild
                .set(
                    i64::try_from(tracking.rebuild_reasons.len())
                        .expect("partition count fits i64"),
                );
        }
    }

    fn lock_tracking(&self) -> MutexGuard<'_, FrameTracking> {
        self.tracking
            .lock()
            .expect("frame tracking lock is poisoned only if a holder panicked")
    }
}

impl FrameTracking {
    fn require_diff(&mut self, partition: PartitionId) {
        let generation = self.diff_needed.entry(partition).or_default();
        *generation = generation
            .checked_add(1)
            .expect("diff generation cannot exhaust u64");
    }

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
    flow_hash: Option<FlowHash>,
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

fn change_flow_hash(
    committed: &CommittedFlowCommand,
    change: &SearchDocumentChange,
) -> Option<FlowHash> {
    let SearchDocumentChange::Upsert { run_id, .. } = change else {
        return None;
    };
    committed.committed.outcome.writes.iter().find_map(|write| {
        if let StateWrite::Put {
            column_family: ColumnFamily::Flow,
            key,
            ..
        } = write
            && let Ok(parsed) = parse_run_key(key)
            && parsed.table == RunTable::SearchDocument
            && parsed.run_id == *run_id
        {
            return Some(parsed.flow_hash);
        }
        None
    })
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
