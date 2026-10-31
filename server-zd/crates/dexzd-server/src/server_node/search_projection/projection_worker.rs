// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use dexzd_apply::FlowStateReader;
use dexzd_engine::{CommandCommitter, CommittedStateView};
use dexzd_keyspace::row_keys::run_singleton_key;
use dexzd_keyspace::{ColumnFamily, PartitionId, RunId, RunTable, TenantId, partition_meta_key};
use dexzd_protocol::dexzd::v1::{LogPosition, PartitionMeta, RunRecord};
use dexzd_search::sql_visibility::{
    PartitionCheckpoint, PartitionDocumentRepairs, ProjectionError, SearchDocumentChange,
    SqlVisibilityProjector,
};
use prost::Message;

use super::committed_search_frames::{CommittedSearchFrame, CommittedSearchFrames};
use super::pending_store::{PendingRunStore, PendingStoreError};
use super::projection_repairs::ProjectionRepair;
use crate::config::SearchProjectionQueueConfig;
use dexzd_metrics::{NodeMetrics, ProjectionRebuildReason};

const FIRST_RETRY_DELAY: Duration = Duration::from_millis(100);
const MAX_RETRY_DELAY: Duration = Duration::from_secs(10);

pub(crate) struct PendingProjectionState {
    pub(crate) runs: Arc<tokio::sync::Mutex<PendingRunStore>>,
    pub(crate) ready: tokio_util::sync::CancellationToken,
}

/// Projects committed frames into SQL visibility in commit order and quarantines partitions it cannot follow.
pub(crate) struct SearchProjectionWorker {
    projector: SqlVisibilityProjector,
    committer: CommandCommitter,
    frames: Arc<CommittedSearchFrames>,
    partition_count: u32,
    pending: Arc<tokio::sync::Mutex<PendingRunStore>>,
    cfg: Arc<SearchProjectionQueueConfig>,
    metrics: Arc<NodeMetrics>,
    pending_sync_ready: tokio_util::sync::CancellationToken,
    processed_positions: BTreeMap<PartitionId, LogPosition>,
    /// The last position projected for each partition; the previous position of its next frame.
    projected_positions: BTreeMap<PartitionId, LogPosition>,
}

#[derive(Debug, thiserror::Error)]
pub(super) enum SearchProjectionError {
    #[error("read committed partition state: {detail}")]
    CommittedState { detail: String },
    #[error(transparent)]
    Projection(#[from] ProjectionError),
    #[error(transparent)]
    PartitionVersionRead(#[from] super::partition_diff::PartitionVersionReadError),
    #[error(transparent)]
    StateRead(#[from] dexzd_apply::StateReadError),
    #[error(transparent)]
    Pending(#[from] PendingStoreError),
}

#[derive(Debug, thiserror::Error)]
#[error("read committed search repair documents: {detail}")]
struct CommittedRepairReadError {
    detail: String,
}

impl SearchProjectionError {
    fn is_fenced(&self) -> bool {
        matches!(
            self,
            Self::Pending(PendingStoreError::Fenced { .. })
                | Self::Projection(ProjectionError::OwnerFenced { .. })
        )
    }

    fn is_retryable(&self) -> bool {
        match self {
            Self::CommittedState { .. } | Self::StateRead(_) => true,
            Self::PartitionVersionRead(_) => false,
            Self::Projection(error) => error.is_retryable(),
            Self::Pending(
                PendingStoreError::Fenced { .. }
                | PendingStoreError::Invalid(_)
                | PendingStoreError::Decode(_),
            ) => false,
            Self::Pending(_) => true,
        }
    }
}

impl SearchProjectionWorker {
    pub(crate) fn new(
        projector: SqlVisibilityProjector,
        committer: CommandCommitter,
        frames: Arc<CommittedSearchFrames>,
        partition_count: u32,
        pending: PendingProjectionState,
        cfg: Arc<SearchProjectionQueueConfig>,
        metrics: Arc<NodeMetrics>,
    ) -> Self {
        Self {
            projector,
            committer,
            frames,
            partition_count,
            pending: pending.runs,
            cfg,
            metrics,
            pending_sync_ready: pending.ready,
            processed_positions: BTreeMap::new(),
            projected_positions: BTreeMap::new(),
        }
    }

    /// Catches up every partition, then projects queued frames until the queue closes.
    pub(crate) async fn run(mut self, stop: tokio_util::sync::CancellationToken) {
        let mut retry_delay = RetryDelay::default();
        loop {
            match self.pending.lock().await.load_objects().await {
                Ok(()) => break,
                Err(error) => {
                    self.metrics.search_projection_pending_sync_failures.inc();
                    if matches!(
                        error,
                        PendingStoreError::Fenced { .. }
                            | PendingStoreError::Invalid(_)
                            | PendingStoreError::Decode(_)
                    ) {
                        self.frames.record_global_failure(&error.to_string());
                        return;
                    }
                    tracing::warn!(%error, "loading search pending objects failed; retrying");
                    retry_delay.wait().await;
                }
            }
        }
        if let Err(error) = self.restore_rebuild_markers().await {
            self.frames.record_global_failure(&error.to_string());
            return;
        }
        self.metrics
            .search_projection_unknown_pending_objects
            .inc_by(self.pending.lock().await.unknown_objects);
        if !self.catch_up_partitions().await {
            return;
        }
        self.pending_sync_ready.cancel();
        let mut sync = tokio::time::interval(self.cfg.pending_sync_interval);
        sync.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut live_frames_since_repair = 0;
        loop {
            let should_repair = !self.frames.has_live_frames()
                || live_frames_since_repair >= self.cfg.repair_every_frames;
            let repairs = {
                let pending = self.pending.lock().await;
                if pending.count() == 0 || !should_repair {
                    Vec::new()
                } else {
                    match pending.take_batch_excluding(
                        self.cfg.repair_batch_runs,
                        &self.frames.quarantined_partitions(),
                    ) {
                        Ok(repairs) => repairs,
                        Err(error) => {
                            self.frames.record_global_failure(&error.to_string());
                            return;
                        }
                    }
                }
            };
            tokio::select! {
                biased;
                () = stop.cancelled() => {
                    while let Some(frame) = self.frames.try_next_frame() {
                        if !self.project_frame(frame).await { return; }
                    }
                    if let Err(error) = self.checkpoint_before_shutdown().await {
                        self.frames.record_global_failure(&error.to_string());
                    }
                    return;
                }
                _ = sync.tick() => {
                    if let Err(error) = self.synchronize_pending().await {
                        self.metrics.search_projection_pending_sync_failures.inc();
                        tracing::warn!(%error, "syncing search pending runs failed; checkpoints remain held");
                        if let SearchProjectionError::Projection(ProjectionError::PositionMismatch { partition_id, .. } | ProjectionError::RepairPositionMismatch { partition_id, .. }) = &error {
                            self.frames.mark_rebuild_needed(*partition_id, ProjectionRebuildReason::Divergence);
                            self.frames.quarantine(*partition_id);
                        } else if !error.is_retryable() {
                            self.frames.record_global_failure(&error.to_string());
                            return;
                        }
                    }
                }
                () = std::future::ready(()), if !repairs.is_empty() => {
                    if !self.repair_runs(repairs).await { return; }
                    live_frames_since_repair = 0;
                }
                frame = self.frames.next_frame() => {
                    if !self.project_frame(frame).await { return; }
                    live_frames_since_repair += 1;
                }
                () = self.frames.rebuild_requested().notified() => {}
            }
            if let Some(partition) = self.frames.rebuild_requests().into_iter().next()
                && !self.rebuild_partition(partition).await
            {
                return;
            }
            self.metrics.search_projection_repair_pending.set(
                i64::try_from(self.pending.lock().await.count()).expect("pending count fits i64"),
            );
            self.frames.publish_projected_through();
        }
    }

    async fn checkpoint_before_shutdown(&mut self) -> Result<(), SearchProjectionError> {
        let repairs = self.frames.take_staged_repairs();
        {
            let mut pending = self.pending.lock().await;
            pending.record(&repairs)?;
            pending.store_rebuild_markers(&self.frames.rebuild_status().0)?;
        }
        // Unsynced partitions retain their checkpoint guard; shutdown cannot drain an unbounded object backlog.
        super::sync_pending_objects(&self.pending, self.cfg.pending_sync_partitions).await?;
        self.synchronize_pending().await
    }

    async fn restore_rebuild_markers(&self) -> Result<(), PendingStoreError> {
        for (partition, reasons) in self.pending.lock().await.load_rebuild_markers()? {
            for reason in reasons {
                self.frames.mark_rebuild_needed(partition, reason);
                if matches!(
                    reason,
                    ProjectionRebuildReason::Divergence | ProjectionRebuildReason::Unreadable
                ) {
                    self.frames.quarantine(partition);
                }
            }
        }
        Ok(())
    }

    async fn synchronize_pending(&mut self) -> Result<(), SearchProjectionError> {
        for (partition, generation) in self.frames.diff_partitions() {
            if let Err(error) = self.diff_partition(partition).await {
                if error.is_fenced() {
                    return Err(error);
                }
                if error.is_retryable() {
                    return Err(error);
                }
                if !self.frames.is_quarantined(partition) {
                    self.frames
                        .mark_rebuild_needed(partition, ProjectionRebuildReason::Unreadable);
                    self.frames.quarantine(partition);
                }
                tracing::error!(partition = partition.0, %error, "search version comparison requires manual rebuilding");
            }
            self.frames.finish_diff(partition, generation);
        }
        for (partition, position) in self.read_applied_positions().await? {
            if self.frames.is_partition_idle(partition)
                && !self.pending.lock().await.has_pending_runs(partition)?
                && !self.is_projected(partition, &position)
            {
                self.processed_positions.insert(partition, position);
            }
        }
        let mut checkpoints = Vec::new();
        for (partition, position) in self.processed_positions.clone() {
            let can_checkpoint = {
                let pending = self.pending.lock().await;
                !self.frames.is_quarantined(partition)
                    && !pending.has_unsynced(partition)
                    && !self.frames.has_pending_staging(partition)
            };
            if !can_checkpoint {
                continue;
            }
            if self.projected_positions.get(&partition) != Some(&position) {
                checkpoints.push(PartitionCheckpoint {
                    partition_id: partition,
                    previous_position: self.projected_positions.get(&partition).cloned(),
                    applied_position: position,
                });
            }
        }
        let mut retry_delay = RetryDelay::default();
        loop {
            match self.projector.checkpoint_partitions(&checkpoints).await {
                Ok(()) => break,
                Err(error) if error.is_retryable() => {
                    tracing::warn!(%error, "confirming search checkpoints failed; retrying the same positions");
                    retry_delay.wait().await;
                }
                Err(error) => return Err(error.into()),
            }
        }
        for checkpoint in checkpoints {
            self.projected_positions
                .insert(checkpoint.partition_id, checkpoint.applied_position);
        }
        Ok(())
    }

    async fn read_applied_positions(
        &self,
    ) -> Result<BTreeMap<PartitionId, LogPosition>, SearchProjectionError> {
        let committer = self.committer.clone();
        let partition_count = self.partition_count;
        tokio::task::spawn_blocking(move || {
            let view = committer
                .committed_view()
                .map_err(|error| error.to_string())?;
            let mut positions = BTreeMap::new();
            for partition_number in 0..partition_count {
                let partition = PartitionId(
                    u16::try_from(partition_number).expect("validated partition count"),
                );
                let metadata = view
                    .get(ColumnFamily::PartitionMeta, &partition_meta_key(partition))
                    .map_err(|error| format!("read partition {partition}: {error}"))?;
                if let Some(encoded) = metadata {
                    let metadata = PartitionMeta::decode(encoded.as_slice())
                        .map_err(|error| format!("decode partition {partition}: {error}"))?;
                    if let Some(position) = metadata.applied {
                        positions.insert(partition, position);
                    }
                }
            }
            Ok::<_, String>(positions)
        })
        .await
        .map_err(|error| SearchProjectionError::CommittedState {
            detail: format!("join applied position read: {error}"),
        })?
        .map_err(|detail| SearchProjectionError::CommittedState { detail })
    }

    /// Frames lost before startup leave SQL visibility behind the local store, so differing partitions compare versions.
    async fn catch_up_partitions(&mut self) -> bool {
        let caught_up_sequence = self.frames.last_sequence();
        let mut compared_partitions = 0_u32;
        for partition in (0..self.partition_count).map(|partition| {
            PartitionId(u16::try_from(partition).expect("validation limits partitions to 65536"))
        }) {
            let mut retry_delay = RetryDelay::default();
            if self.frames.is_quarantined(partition) {
                continue;
            }
            loop {
                match self.catch_up_partition(partition).await {
                    Ok(was_compared) => {
                        compared_partitions += u32::from(was_compared);
                        break;
                    }
                    Err(error) => {
                        if error.is_fenced() {
                            self.frames
                                .record_permanent_failure(partition, &error.to_string());
                            return false;
                        }
                        if !error.is_retryable() {
                            self.frames.mark_rebuild_needed(
                                partition,
                                ProjectionRebuildReason::Unreadable,
                            );
                            self.frames.quarantine(partition);
                            tracing::error!(partition = partition.0, %error, "startup search version comparison requires operator repair");
                            break;
                        }
                        tracing::warn!(
                            partition = partition.0,
                            %error,
                            "catching up the search projection failed; retrying"
                        );
                        retry_delay.wait().await;
                    }
                }
            }
        }
        self.frames.finish_catch_up(caught_up_sequence);
        self.frames.publish_projected_through();
        tracing::info!(
            partitions = self.partition_count,
            compared_partitions,
            "the SQL visibility search projection caught up"
        );
        true
    }

    /// Returns whether the partition required version comparison.
    async fn catch_up_partition(
        &mut self,
        partition: PartitionId,
    ) -> Result<bool, SearchProjectionError> {
        let progress = self.projector.read_progress(partition).await?;
        self.projector
            .repair_partition_documents(partition, progress.as_ref(), &[])
            .await?;
        let (view, committed_position) = self.open_committed_view(partition).await?;
        if let Some(progress) = progress.clone() {
            self.projected_positions.insert(partition, progress);
        }
        match (progress, committed_position) {
            (None, None) => Ok(false),
            (Some(_), None) => {
                self.frames
                    .mark_rebuild_needed(partition, ProjectionRebuildReason::Divergence);
                self.frames.quarantine(partition);
                Ok(false)
            }
            (Some(progress), Some(committed)) if progress == committed => Ok(false),
            (Some(progress), Some(committed))
                if progress.node_id != committed.node_id
                    || (progress.incarnation, progress.seq)
                        > (committed.incarnation, committed.seq) =>
            {
                self.frames
                    .mark_rebuild_needed(partition, ProjectionRebuildReason::Divergence);
                self.frames.quarantine(partition);
                Ok(false)
            }
            (_, Some(committed)) => {
                self.diff_view(partition, view).await?;
                while self.pending.lock().await.has_unsynced(partition) {
                    let synced = super::sync_pending_objects(
                        &self.pending,
                        self.cfg.pending_sync_partitions,
                    )
                    .await?;
                    self.metrics
                        .search_projection_pending_objects_synced
                        .inc_by(u64::try_from(synced).expect("partition count fits u64"));
                }
                self.projector
                    .apply_batch(
                        partition,
                        self.projected_positions.get(&partition),
                        &committed,
                        &[],
                    )
                    .await?;
                self.projected_positions.insert(partition, committed);
                Ok(true)
            }
        }
    }

    async fn diff_partition(
        &mut self,
        partition: PartitionId,
    ) -> Result<(), SearchProjectionError> {
        let (view, _) = self.open_committed_view(partition).await?;
        self.diff_view(partition, view).await
    }

    async fn diff_view(
        &mut self,
        partition: PartitionId,
        view: CommittedStateView,
    ) -> Result<(), SearchProjectionError> {
        let (local_store, prefix) = self.pending.lock().await.version_spool(partition);
        let mut local = tokio::task::spawn_blocking(move || {
            super::partition_diff::read_local_versions(view, partition, local_store, prefix)
        })
        .await
        .map_err(|error| SearchProjectionError::CommittedState {
            detail: format!("join partition version scan: {error}"),
        })??;
        let mut next_local = local.next()?;
        let mut cursor: Option<(String, String)> = None;
        let mut repairs = Vec::with_capacity(256);
        loop {
            let rows = self
                .projector
                .read_run_versions(
                    partition,
                    cursor
                        .as_ref()
                        .map(|(tenant, run)| (tenant.as_str(), run.as_str())),
                    256,
                )
                .await?;
            if rows.is_empty() {
                break;
            }
            for row in rows {
                let identity = (row.tenant_id.0.to_string(), row.run_id.to_string());
                cursor = Some(identity.clone());
                while next_local
                    .as_ref()
                    .is_some_and(|local| local.identity < identity)
                {
                    repairs.push(next_local.take().expect("checked local row").repair);
                    next_local = local.next()?;
                    if repairs.len() == 256 {
                        self.pending.lock().await.record(&repairs)?;
                        repairs.clear();
                    }
                }
                if next_local
                    .as_ref()
                    .is_some_and(|local| local.identity == identity)
                {
                    let local_version = next_local.take().expect("checked local row");
                    if local_version.version > row.version {
                        repairs.push(local_version.repair);
                    } else if local_version.version < row.version {
                        self.frames
                            .mark_rebuild_needed(partition, ProjectionRebuildReason::Divergence);
                        self.frames.quarantine(partition);
                        return Err(ProjectionError::InvalidState(format!(
                            "projected run {} is newer than committed version {}",
                            row.run_id, local_version.version
                        ))
                        .into());
                    }
                    next_local = local.next()?;
                } else {
                    repairs.push(ProjectionRepair::new(
                        row.tenant_id,
                        row.run_id,
                        dexzd_keyspace::FlowHash(0),
                        partition,
                        super::projection_repairs::RepairPriority::Delete,
                    ));
                }
                if repairs.len() == 256 {
                    self.pending.lock().await.record(&repairs)?;
                    repairs.clear();
                }
            }
        }
        while let Some(version) = next_local.take() {
            repairs.push(version.repair);
            next_local = local.next()?;
            if repairs.len() == 256 {
                self.pending.lock().await.record(&repairs)?;
                repairs.clear();
            }
        }
        self.pending.lock().await.record(&repairs)?;
        local.clear()?;
        Ok(())
    }

    async fn project_frame(&mut self, frame: CommittedSearchFrame) -> bool {
        if self.frames.is_quarantined(frame.partition) {
            self.frames.finish_projected_frame(&frame);
            return true;
        }
        if !self.is_projected(frame.partition, &frame.position) && !self.apply_frame(&frame).await {
            return false;
        }
        self.frames.finish_projected_frame(&frame);
        true
    }

    async fn apply_frame(&mut self, frame: &CommittedSearchFrame) -> bool {
        let mut retry_delay = RetryDelay::default();
        let mut should_checkpoint = false;
        loop {
            let can_checkpoint = {
                let pending = self.pending.lock().await;
                !pending.has_unsynced(frame.partition)
                    && !self.frames.has_pending_staging(frame.partition)
            };
            should_checkpoint |= can_checkpoint;
            let applied = if should_checkpoint {
                self.projector
                    .apply_batch(
                        frame.partition,
                        self.projected_positions.get(&frame.partition),
                        &frame.position,
                        &frame.changes,
                    )
                    .await
            } else {
                self.projector
                    .repair_partition_documents(
                        frame.partition,
                        self.projected_positions.get(&frame.partition),
                        &frame.changes,
                    )
                    .await
            };
            match applied {
                Ok(()) => {
                    self.processed_positions
                        .insert(frame.partition, frame.position.clone());
                    if should_checkpoint {
                        self.projected_positions
                            .insert(frame.partition, frame.position.clone());
                    }
                    return true;
                }
                Err(
                    error @ (ProjectionError::PositionMismatch { .. }
                    | ProjectionError::RepairPositionMismatch { .. }),
                ) => {
                    self.frames
                        .mark_rebuild_needed(frame.partition, ProjectionRebuildReason::Divergence);
                    self.frames.quarantine(frame.partition);
                    tracing::debug!(partition = frame.partition.0, %error, "search projection partition is quarantined until manual rebuilding");
                    return true;
                }
                Err(error) => {
                    if !error.is_retryable() {
                        self.frames
                            .record_permanent_failure(frame.partition, &error.to_string());
                        return false;
                    }
                    tracing::warn!(
                        partition = frame.partition.0,
                        %error,
                        "projecting a committed frame into SQL visibility failed; retrying"
                    );
                    retry_delay.wait().await;
                }
            }
        }
    }

    async fn repair_runs(&mut self, mut repairs: Vec<ProjectionRepair>) -> bool {
        repairs.retain(|repair| !self.frames.is_quarantined(repair.partition));
        if repairs.is_empty() {
            return true;
        }
        match self.try_repair_runs(&repairs).await {
            Ok(projected) => {
                if let Err(error) = self.pending.lock().await.finish(&repairs) {
                    self.frames.record_global_failure(&error.to_string());
                    return false;
                }
                for repair in &repairs {
                    if repair.priority != super::projection_repairs::RepairPriority::Delete
                        && projected.contains(&(repair.tenant_id, repair.run_id))
                    {
                        self.metrics
                            .search_projection_repaired_runs
                            .record(repair.priority.reason());
                    }
                }
                true
            }
            Err(SearchProjectionError::Projection(ProjectionError::RepairPositionMismatch {
                partition_id,
                ..
            })) => {
                self.frames
                    .mark_rebuild_needed(partition_id, ProjectionRebuildReason::Divergence);
                self.frames.quarantine(partition_id);
                true
            }
            Err(error) => {
                if error.is_fenced() {
                    self.frames.record_global_failure(&error.to_string());
                    return false;
                }
                if !error.is_retryable() {
                    for repair in &repairs {
                        self.frames.mark_rebuild_needed(
                            repair.partition,
                            ProjectionRebuildReason::Unreadable,
                        );
                        self.frames.quarantine(repair.partition);
                    }
                    tracing::error!(%error, "search repair partitions require manual rebuilding");
                    return true;
                }
                tracing::warn!(%error, "repairing search documents failed; retrying after live frames");
                tokio::time::sleep(FIRST_RETRY_DELAY).await;
                true
            }
        }
    }

    async fn try_repair_runs(
        &self,
        repairs: &[ProjectionRepair],
    ) -> Result<BTreeSet<(TenantId, RunId)>, SearchProjectionError> {
        let committer = self.committer.clone();
        let requested = repairs.to_vec();
        let documents =
            tokio::task::spawn_blocking(move || read_repair_documents(&committer, &requested))
                .await
                .map_err(|error| SearchProjectionError::CommittedState {
                    detail: format!("join search repair document read: {error}"),
                })?
                .map_err(|error| SearchProjectionError::CommittedState {
                    detail: error.to_string(),
                })?;
        let mut projected = BTreeSet::new();
        let partitions: Vec<_> = documents
            .into_iter()
            .map(|(partition_id, changes)| {
                for change in &changes {
                    if let SearchDocumentChange::Upsert {
                        tenant_id, run_id, ..
                    } = change
                    {
                        projected.insert((*tenant_id, *run_id));
                    }
                }
                PartitionDocumentRepairs {
                    partition_id,
                    previous_position: self.projected_positions.get(&partition_id).cloned(),
                    changes,
                }
            })
            .collect();
        self.projector.repair_documents(&partitions).await?;
        Ok(projected)
    }

    async fn rebuild_partition(&mut self, partition: PartitionId) -> bool {
        self.frames.start_rebuild(partition);
        match retry_partition_rebuild(self, partition).await {
            Ok(()) => {
                let stored = self
                    .pending
                    .lock()
                    .await
                    .store_rebuild_markers(&self.frames.rebuild_status().0);
                if let Err(error) = stored {
                    self.frames
                        .record_permanent_failure(partition, &error.to_string());
                    return false;
                }
                if let Err(error) =
                    super::sync_pending_objects(&self.pending, self.cfg.pending_sync_partitions)
                        .await
                {
                    if matches!(error, PendingStoreError::Fenced { .. }) {
                        self.frames
                            .record_permanent_failure(partition, &error.to_string());
                        return false;
                    }
                    tracing::warn!(partition = partition.0, %error, "syncing rebuilt partition markers failed; retrying on next interval");
                }
                true
            }
            Err(error) => {
                if error.is_fenced() {
                    self.frames
                        .record_permanent_failure(partition, &error.to_string());
                    return false;
                }
                self.frames.fail_rebuild(partition);
                tracing::error!(partition = partition.0, %error, "manual search rebuild failed; partition remains quarantined");
                true
            }
        }
    }

    async fn try_rebuild_partition(
        &mut self,
        partition: PartitionId,
    ) -> Result<(), SearchProjectionError> {
        let (view, committed_position) = self.open_committed_view(partition).await?;
        let Some(committed_position) = committed_position else {
            self.projector.clear_empty_partition(partition).await?;
            self.pending.lock().await.finish_manual_rebuild(partition);
            self.projected_positions.remove(&partition);
            self.processed_positions.remove(&partition);
            return Ok(());
        };
        self.projector
            .rebuild_partition(partition, &committed_position, Arc::new(view))
            .await?;
        self.pending.lock().await.finish_manual_rebuild(partition);
        self.processed_positions
            .insert(partition, committed_position.clone());
        self.projected_positions
            .insert(partition, committed_position);
        Ok(())
    }

    /// Returns a committed view and the partition's applied position in it, if any.
    async fn open_committed_view(
        &self,
        partition: PartitionId,
    ) -> Result<(CommittedStateView, Option<LogPosition>), SearchProjectionError> {
        let committer = self.committer.clone();
        let opened = tokio::task::spawn_blocking(move || {
            let view = committer
                .committed_view()
                .map_err(|error| error.to_string())?;
            let applied_position = view
                .get(ColumnFamily::PartitionMeta, &partition_meta_key(partition))
                .map_err(|error| error.to_string())?
                .map(|bytes| PartitionMeta::decode(bytes.as_slice()))
                .transpose()
                .map_err(|error| format!("decode partition metadata: {error}"))?
                .and_then(|meta| meta.applied);
            Ok((view, applied_position))
        })
        .await
        .map_err(|error| error.to_string())
        .and_then(|opened| opened);
        opened.map_err(|detail| SearchProjectionError::CommittedState { detail })
    }

    /// Positions of one node order by incarnation, then seq; other nodes' positions never compare.
    fn is_projected(&self, partition: PartitionId, position: &LogPosition) -> bool {
        self.projected_positions
            .get(&partition)
            .is_some_and(|projected| {
                projected.node_id == position.node_id
                    && (projected.incarnation, projected.seq)
                        >= (position.incarnation, position.seq)
            })
    }
}

fn read_repair_documents(
    committer: &CommandCommitter,
    repairs: &[ProjectionRepair],
) -> Result<BTreeMap<PartitionId, Vec<SearchDocumentChange>>, CommittedRepairReadError> {
    let view = committer
        .committed_view()
        .map_err(|error| CommittedRepairReadError {
            detail: format!("open committed search repair view: {error}"),
        })?;
    let mut documents: BTreeMap<_, Vec<_>> = BTreeMap::new();
    for repair in repairs {
        if repair.priority == super::projection_repairs::RepairPriority::Delete {
            documents
                .entry(repair.partition)
                .or_default()
                .push(SearchDocumentChange::Delete {
                    tenant_id: repair.tenant_id,
                    run_id: repair.run_id,
                });
            continue;
        }
        let run_key = run_singleton_key(repair.flow_hash, repair.run_id, RunTable::RunRecord);
        let Some(encoded_run) =
            view.get(ColumnFamily::Flow, &run_key)
                .map_err(|error| CommittedRepairReadError {
                    detail: format!(
                        "read committed run {} for search repair: {error}",
                        repair.run_id
                    ),
                })?
        else {
            documents
                .entry(repair.partition)
                .or_default()
                .push(SearchDocumentChange::Delete {
                    tenant_id: repair.tenant_id,
                    run_id: repair.run_id,
                });
            continue;
        };
        let record = RunRecord::decode(encoded_run.as_slice()).map_err(|error| {
            CommittedRepairReadError {
                detail: format!(
                    "decode committed run {} for search repair: {error}",
                    repair.run_id
                ),
            }
        })?;
        if record.is_purging {
            documents
                .entry(repair.partition)
                .or_default()
                .push(SearchDocumentChange::Delete {
                    tenant_id: repair.tenant_id,
                    run_id: repair.run_id,
                });
            continue;
        }
        let document_key =
            run_singleton_key(repair.flow_hash, repair.run_id, RunTable::SearchDocument);
        let encoded_document = view
            .get(ColumnFamily::Flow, &document_key)
            .map_err(|error| CommittedRepairReadError {
                detail: format!(
                    "read committed search document {} for repair: {error}",
                    repair.run_id
                ),
            })?
            .ok_or_else(|| CommittedRepairReadError {
                detail: format!(
                    "missing committed search document {} for repair",
                    repair.run_id
                ),
            })?;
        documents
            .entry(repair.partition)
            .or_default()
            .push(SearchDocumentChange::Upsert {
                tenant_id: repair.tenant_id,
                run_id: repair.run_id,
                encoded_document,
            });
    }
    Ok(documents)
}

#[async_trait]
trait PartitionRebuilder: Send {
    fn last_sequence(&self) -> u64;
    async fn attempt_rebuild(
        &mut self,
        partition: PartitionId,
    ) -> Result<(), SearchProjectionError>;
    fn finish_rebuild(&self, partition: PartitionId, covered_sequence: u64);
}

#[async_trait]
impl PartitionRebuilder for SearchProjectionWorker {
    fn last_sequence(&self) -> u64 {
        self.frames.last_sequence()
    }

    async fn attempt_rebuild(
        &mut self,
        partition: PartitionId,
    ) -> Result<(), SearchProjectionError> {
        self.try_rebuild_partition(partition).await
    }

    fn finish_rebuild(&self, partition: PartitionId, covered_sequence: u64) {
        self.frames.finish_rebuild(partition, covered_sequence);
    }
}

async fn retry_partition_rebuild(
    rebuilder: &mut impl PartitionRebuilder,
    partition: PartitionId,
) -> Result<(), SearchProjectionError> {
    let mut retry_delay = RetryDelay::default();
    loop {
        // Every frame through this sequence is committed before the view opens.
        let covered_sequence = rebuilder.last_sequence();
        match rebuilder.attempt_rebuild(partition).await {
            Ok(()) => {
                rebuilder.finish_rebuild(partition, covered_sequence);
                return Ok(());
            }
            Err(error) => {
                if !error.is_retryable() {
                    return Err(error);
                }
                tracing::warn!(
                    partition = partition.0,
                    %error,
                    "rebuilding the search projection of a partition failed; retrying"
                );
                retry_delay.wait().await;
            }
        }
    }
}

/// Doubles the wait after each failed attempt, up to [`MAX_RETRY_DELAY`].
struct RetryDelay {
    next: Duration,
}

impl Default for RetryDelay {
    fn default() -> Self {
        Self {
            next: FIRST_RETRY_DELAY,
        }
    }
}

impl RetryDelay {
    async fn wait(&mut self) {
        tokio::time::sleep(self.next).await;
        self.next = (self.next * 2).min(MAX_RETRY_DELAY);
    }
}

#[cfg(test)]
#[path = "projection_worker_tests.rs"]
mod tests;
