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

use dexzd_apply::FlowStateReader;
use dexzd_engine::{CommandCommitter, CommittedStateView};
use dexzd_keyspace::row_keys::{
    search_outbox_range_after, search_outbox_range_through, search_projection_mode_key,
};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, PartitionId, RunId, partition_meta_key};
use dexzd_metrics::{NodeMetrics, Outcome};
use dexzd_protocol::dexzd::v1::{LogPosition, PartitionMeta, SearchProjectionModeRecord};
use dexzd_search::sql_visibility::{
    OutboxEntry, PartitionProjectionBatch, ProjectionError, ProjectionProgress,
    SearchDocumentChange, SqlVisibilityProjector, read_initial_load_page, read_outbox_entries,
    read_run_change,
};
use prost::Message;
use tokio_util::sync::CancellationToken;

use super::outbox_acks::{AckSegment, OutboxAcks};
use super::outbox_watermarks::{SearchOutboxWatermarks, SearchProjectionFailure};
use crate::config::SearchConfig;

const FIRST_RETRY_DELAY: Duration = Duration::from_millis(100);
const MAX_RETRY_DELAY: Duration = Duration::from_secs(10);

/// What the consumer uses, passed once to [`SearchOutboxConsumer::new`].
pub(crate) struct SearchOutboxDependencies {
    pub(crate) projector: SqlVisibilityProjector,
    pub(crate) committer: CommandCommitter,
    pub(crate) partition_hasher: PartitionHasher,
    /// This node's lease epoch; SQL visibility rejects writers with a lower one.
    pub(crate) node_epoch: u64,
    pub(crate) metrics: Arc<NodeMetrics>,
    pub(crate) watermarks: Arc<SearchOutboxWatermarks>,
}

/// Projects every partition's SearchOutbox into SQL visibility and acks it; only the lease holder runs it.
pub(crate) struct SearchOutboxConsumer {
    cfg: Arc<SearchConfig>,
    projector: SqlVisibilityProjector,
    committer: CommandCommitter,
    partition_count: u32,
    node_epoch: u64,
    metrics: Arc<NodeMetrics>,
    watermarks: Arc<SearchOutboxWatermarks>,
    acks: OutboxAcks,
    partitions: Vec<PartitionProjection>,
    /// Set until a pass reads every partition; later passes read only partitions with new entries.
    needs_full_pass: bool,
}

#[derive(Debug, Default)]
struct PartitionProjection {
    /// SQL progress when this node fenced the schema; checked once against a committed view.
    recorded: Option<ProjectionProgress>,
    has_checked_progress: bool,
    /// SQL visibility finished this partition's initial load.
    is_loaded: bool,
    /// Why the partition is loaded; set until the load finishes.
    load_reason: Option<InitialLoadReason>,
    /// SQL visibility holds every entry at or below this seq.
    projected_through: u64,
    /// The partition's mode was disabled in the last view, for example because it stopped.
    is_disabled: bool,
}

/// One partition's head in a committed view.
struct PartitionHead {
    applied_position: Option<LogPosition>,
    /// Every SearchOutbox entry of the view is at or below it, and every later one is above it.
    last_seq: u64,
    is_enabled: bool,
    /// Entries at or below this seq were deleted by acks.
    acked_through_seq: u64,
    /// Seq of the command that enabled the mode; progress below it misses earlier changes.
    enabled_at_seq: u64,
}

/// Why a partition is loaded from a committed view instead of following its SearchOutbox.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum InitialLoadReason {
    /// SQL visibility never finished a load of the partition.
    NoProgress,
    /// A server without the SearchOutbox wrote the progress.
    NoOutboxProgress,
    /// The mode was enabled after the progress, so changes made while disabled are missing.
    EnabledAfterProgress,
    /// Acks deleted entries that SQL visibility does not hold, as after a database restore.
    AckedAfterProgress,
    /// SQL visibility is ahead of committed state, as after an object storage restore.
    AheadOfCommittedState,
}

impl InitialLoadReason {
    fn as_str(self) -> &'static str {
        match self {
            Self::NoProgress => "no_progress",
            Self::NoOutboxProgress => "no_outbox_progress",
            Self::EnabledAfterProgress => "enabled_after_progress",
            Self::AckedAfterProgress => "acked_after_progress",
            Self::AheadOfCommittedState => "ahead_of_committed_state",
        }
    }
}

/// Returns why the recorded SQL progress cannot continue from the partition's queue, or `None` when it can.
fn initial_load_reason(
    recorded: Option<&ProjectionProgress>,
    head: &PartitionHead,
) -> Option<InitialLoadReason> {
    let Some(recorded) = recorded.filter(|recorded| recorded.applied_position.is_some()) else {
        return Some(InitialLoadReason::NoProgress);
    };
    let Some(through_seq) = recorded.outbox_through_seq else {
        return Some(InitialLoadReason::NoOutboxProgress);
    };
    if through_seq < head.enabled_at_seq {
        return Some(InitialLoadReason::EnabledAfterProgress);
    }
    if through_seq < head.acked_through_seq {
        return Some(InitialLoadReason::AckedAfterProgress);
    }
    if through_seq > head.last_seq {
        return Some(InitialLoadReason::AheadOfCommittedState);
    }
    None
}

#[derive(Debug, thiserror::Error)]
enum OutboxConsumerError {
    #[error("read committed search state: {detail}")]
    CommittedState { detail: String },
    #[error(transparent)]
    Projection(#[from] ProjectionError),
}

impl OutboxConsumerError {
    fn is_retryable(&self) -> bool {
        match self {
            Self::CommittedState { .. } => true,
            Self::Projection(error) => error.is_retryable(),
        }
    }
}

impl SearchOutboxConsumer {
    pub(crate) fn new(cfg: Arc<SearchConfig>, dependencies: SearchOutboxDependencies) -> Self {
        let partition_count = dependencies.partition_hasher.partition_count();
        Self {
            acks: OutboxAcks::new(
                cfg.clone(),
                dependencies.committer.clone(),
                dependencies.partition_hasher,
                dependencies.metrics.clone(),
            ),
            cfg,
            projector: dependencies.projector,
            committer: dependencies.committer,
            partition_count,
            node_epoch: dependencies.node_epoch,
            metrics: dependencies.metrics,
            watermarks: dependencies.watermarks,
            partitions: (0..partition_count)
                .map(|_| PartitionProjection::default())
                .collect(),
            needs_full_pass: true,
        }
    }

    /// Projects until `stop` is cancelled or a permanent failure; SearchFlows reports such a failure.
    pub(crate) async fn run(mut self, stop: CancellationToken) {
        tokio::select! {
            biased;
            () = stop.cancelled() => {}
            () = self.run_until_failure() => {}
        }
        self.metrics.search_outbox_backlog.clear();
    }

    async fn run_until_failure(&mut self) {
        let watermarks = self.watermarks.clone();
        let mut retry_delay = RetryDelay::default();
        while let Err(error) = self.seed_read_your_writes().await {
            if !self.handle_failure(error, &mut retry_delay).await {
                return;
            }
        }
        while let Err(error) = self.fence_progress().await {
            if !self.handle_failure(error, &mut retry_delay).await {
                return;
            }
        }
        retry_delay = RetryDelay::default();
        loop {
            match self.project_pass().await {
                Ok(()) => retry_delay = RetryDelay::default(),
                Err(error) => {
                    self.needs_full_pass = true;
                    if !self.handle_failure(error, &mut retry_delay).await {
                        return;
                    }
                    continue;
                }
            }
            self.acks.propose_due();
            if self.has_unprojected_entries() {
                continue;
            }
            let poll_interval = self.cfg.outbox_poll_interval;
            let ack_due = self.acks.next_due_in().unwrap_or(poll_interval);
            tokio::select! {
                () = watermarks.consumer_wake() => {}
                () = tokio::time::sleep(poll_interval.min(ack_due)) => {}
                () = self.acks.wait_for_completion() => {}
            }
        }
    }

    /// Returns whether to retry after waiting; a permanent failure is recorded for SearchFlows.
    async fn handle_failure(
        &mut self,
        error: OutboxConsumerError,
        retry_delay: &mut RetryDelay,
    ) -> bool {
        if let OutboxConsumerError::Projection(ProjectionError::Fenced { .. }) = &error {
            tracing::error!(%error, "a newer lease holder writes SQL visibility; the search projection stopped");
            self.watermarks
                .record_failure(SearchProjectionFailure::Fenced(error.to_string()));
            return false;
        }
        if !error.is_retryable() {
            tracing::error!(%error, "the search projection stopped");
            self.watermarks
                .record_failure(SearchProjectionFailure::Permanent(format!(
                    "SQL visibility projection failed: {error}"
                )));
            return false;
        }
        self.metrics.record_search_outbox_batch(Outcome::Error);
        tracing::warn!(%error, "projecting search outbox entries failed; retrying");
        retry_delay.wait().await;
        true
    }

    /// Raises every enabled partition's appended seq to its last command, so searches wait for
    /// entries recovered from the local store and for loads.
    async fn seed_read_your_writes(&mut self) -> Result<(), OutboxConsumerError> {
        let partitions: Vec<PartitionId> = (0..self.partitions.len()).map(partition_id).collect();
        let view = open_view(self.committer.clone()).await?;
        let seeds = blocking_read(&view, move |view| {
            let heads = read_partition_heads(view, &partitions)?;
            partitions
                .into_iter()
                .zip(heads)
                .filter(|(_, head)| head.is_enabled)
                .map(|(partition, head)| {
                    let (start, end) = search_outbox_range_after(partition, head.acked_through_seq);
                    let oldest = read_outbox_entries(view, partition, &start, &end, 1)?
                        .first()
                        .map(|entry| entry.seq);
                    Ok((partition, head.last_seq, oldest))
                })
                .collect::<Result<Vec<_>, OutboxConsumerError>>()
        })
        .await?;
        for (partition, last_seq, oldest) in seeds {
            self.watermarks
                .seed_appended(usize::from(partition.0), last_seq, oldest);
        }
        self.watermarks.finish_seeding();
        Ok(())
    }

    /// Fences every partition with this node's epoch, then keeps the earlier progress for checking.
    async fn fence_progress(&mut self) -> Result<(), OutboxConsumerError> {
        let progress = self
            .projector
            .fence_all_progress(self.node_epoch, self.partition_count)
            .await?;
        for (partition, recorded) in progress {
            match self.partitions.get_mut(usize::from(partition.0)) {
                Some(state) => state.recorded = Some(recorded),
                None => tracing::warn!(
                    partition = partition.0,
                    "SQL visibility has progress for a partition outside the cluster"
                ),
            }
        }
        Ok(())
    }

    /// Projects every partition from one committed view.
    async fn project_pass(&mut self) -> Result<(), OutboxConsumerError> {
        let partitions: Vec<PartitionId> = (0..self.partitions.len())
            .filter(|index| {
                let state = &self.partitions[*index];
                self.needs_full_pass
                    || (!state.is_disabled
                        && (!state.is_loaded || self.watermarks.has_unprojected_entries(*index)))
            })
            .map(partition_id)
            .collect();
        if partitions.is_empty() {
            return Ok(());
        }
        let view = open_view(self.committer.clone()).await?;
        let heads = blocking_read(&view, {
            let partitions = partitions.clone();
            move |view| read_partition_heads(view, &partitions)
        })
        .await?;
        let mut batch = OutboxBatch::default();
        for (partition, head) in partitions.into_iter().zip(heads) {
            let state = &mut self.partitions[usize::from(partition.0)];
            state.is_disabled = !head.is_enabled;
            if !head.is_enabled {
                continue;
            }
            if !state.has_checked_progress {
                state.has_checked_progress = true;
                let recorded = state.recorded.take();
                state.load_reason = initial_load_reason(recorded.as_ref(), &head);
                state.is_loaded = state.load_reason.is_none();
                state.projected_through = recorded
                    .and_then(|recorded| recorded.outbox_through_seq)
                    .filter(|_| state.is_loaded)
                    .unwrap_or(0);
            }
            let is_loaded = state.is_loaded;
            self.acks.record_acked(partition, head.acked_through_seq);
            if is_loaded {
                self.drain_partition(&view, partition, &head, &mut batch)
                    .await?;
            } else {
                self.load_partition(&view, partition, &head, &mut batch)
                    .await?;
            }
            self.acks.propose_due();
        }
        self.flush(&mut batch).await?;
        self.needs_full_pass = false;
        let pending_loads = self
            .partitions
            .iter()
            .filter(|state| !state.is_disabled && !state.is_loaded)
            .count();
        self.metrics
            .search_initial_load_pending_partitions
            .set(i64::try_from(pending_loads).unwrap_or(i64::MAX));
        Ok(())
    }

    /// Projects the partition's entries after its projected seq, in queue order and pages.
    async fn drain_partition(
        &mut self,
        view: &Arc<CommittedStateView>,
        partition: PartitionId,
        head: &PartitionHead,
        batch: &mut OutboxBatch,
    ) -> Result<(), OutboxConsumerError> {
        let projected_through = self.partitions[usize::from(partition.0)].projected_through;
        let page_size = self.batch_size();
        // Entries projected before a restart may still wait for their ack.
        if self.needs_full_pass && projected_through > head.acked_through_seq {
            let acked_through_seq = head.acked_through_seq;
            let segments = blocking_read(view, move |view| {
                read_ack_segments(
                    view,
                    partition,
                    acked_through_seq,
                    projected_through,
                    page_size,
                )
            })
            .await?;
            self.acks.cover(partition, &segments);
        }
        let (_, end) = search_outbox_range_after(partition, 0);
        let (mut start, _) = search_outbox_range_after(partition, projected_through);
        let mut entry_count: u64 = 0;
        loop {
            let page = blocking_read(view, {
                let (start, end) = (start.clone(), end.clone());
                move |view| read_outbox_page(view, partition, &start, &end, page_size)
            })
            .await?;
            let Some(last) = page.entries.last() else {
                if entry_count > 0 {
                    batch.add(
                        partition,
                        Vec::new(),
                        head.last_seq,
                        head.applied_position.clone(),
                        AckSegment {
                            through_seq: head.last_seq,
                            entry_count: 0,
                        },
                    );
                }
                break;
            };
            entry_count += page.entries.len() as u64;
            let is_last_page = page.entries.len() < page_size;
            // A full page may end inside one seq, so it covers only the seqs before its last one.
            let through = if is_last_page {
                head.last_seq
            } else {
                last.seq.saturating_sub(1)
            };
            let mut next_start = last.key.clone();
            next_start.push(0);
            batch.add(
                partition,
                page.changes,
                through,
                is_last_page
                    .then(|| head.applied_position.clone())
                    .flatten(),
                AckSegment {
                    through_seq: through,
                    entry_count: page.entries.len() as u64,
                },
            );
            batch.entries += page.entries.len() as u64;
            if batch.change_count() >= page_size {
                self.flush(batch).await?;
            }
            if is_last_page {
                break;
            }
            start = next_start;
        }
        self.metrics
            .search_outbox_backlog
            .set_entries(usize::from(partition.0), entry_count);
        if entry_count == 0 {
            // No entry of the view follows the projected seq, so the view's last seq is covered too.
            let state = &mut self.partitions[usize::from(partition.0)];
            state.projected_through = state.projected_through.max(head.last_seq);
            self.watermarks
                .publish_projected(usize::from(partition.0), state.projected_through);
        }
        Ok(())
    }

    /// Loads every searchable document of the view into a partition whose SQL progress cannot continue.
    async fn load_partition(
        &mut self,
        view: &Arc<CommittedStateView>,
        partition: PartitionId,
        head: &PartitionHead,
        batch: &mut OutboxBatch,
    ) -> Result<(), OutboxConsumerError> {
        let Some(applied_position) = head.applied_position.clone() else {
            return Err(OutboxConsumerError::CommittedState {
                detail: format!(
                    "partition {} enables the search projection without an applied position",
                    partition.0
                ),
            });
        };
        let reason = self.partitions[usize::from(partition.0)]
            .load_reason
            .unwrap_or(InitialLoadReason::NoProgress);
        tracing::info!(
            partition = partition.0,
            reason = reason.as_str(),
            "loading the partition into the SQL visibility projection"
        );
        self.metrics.search_initial_loads.inc();
        let page_size = self.batch_size();
        let mut start: Option<Vec<u8>> = None;
        let mut is_first_page = true;
        loop {
            let page = blocking_read(view, {
                let start = start.clone();
                move |view| {
                    read_initial_load_page(view, partition, start.as_deref(), page_size)
                        .map_err(OutboxConsumerError::from)
                }
            })
            .await?;
            let Some(page) = page else {
                break;
            };
            batch.load_documents += page.changes.len() as u64;
            batch.add_initial_load(partition, page.changes, is_first_page);
            is_first_page = false;
            if batch.change_count() >= page_size {
                self.flush(batch).await?;
            }
            start = Some(page.next_start);
        }
        // Entries of this view are covered by the load, so they are acked without being projected.
        let (acked_through_seq, last_seq) = (head.acked_through_seq, head.last_seq);
        let segments = blocking_read(view, move |view| {
            read_ack_segments(view, partition, acked_through_seq, last_seq, page_size)
        })
        .await?;
        batch.add_initial_load(partition, Vec::new(), is_first_page);
        let write = batch.partition(partition);
        write.batch.outbox_through_seq = write.batch.outbox_through_seq.max(head.last_seq);
        write.batch.applied_position = Some(applied_position);
        write.ack_segments.extend(segments);
        Ok(())
    }

    /// Writes the batch in one SQL transaction, then publishes and acks what it covered.
    async fn flush(&mut self, batch: &mut OutboxBatch) -> Result<(), OutboxConsumerError> {
        if batch.partitions.is_empty() {
            return Ok(());
        }
        let pending = std::mem::take(batch);
        let projection_batches: Vec<PartitionProjectionBatch> = pending
            .partitions
            .values()
            .map(|partition| partition.batch.clone())
            .collect();
        self.projector
            .apply_outbox_batch(self.node_epoch, &projection_batches)
            .await?;
        self.metrics.record_search_outbox_batch(Outcome::Ok);
        self.metrics
            .search_outbox_projected_entries
            .inc_by(pending.entries);
        self.metrics.search_outbox_projected_runs.inc_by(
            projection_batches
                .iter()
                .map(|batch| batch.changes.len() as u64)
                .sum::<u64>()
                .saturating_sub(pending.load_documents),
        );
        self.metrics
            .search_initial_load_documents
            .inc_by(pending.load_documents);
        for (partition, written) in pending.partitions {
            let state = &mut self.partitions[usize::from(partition.0)];
            state.projected_through = state
                .projected_through
                .max(written.batch.outbox_through_seq);
            if written.batch.applied_position.is_some() {
                state.is_loaded = true;
                state.load_reason = None;
                self.metrics
                    .search_outbox_backlog
                    .set_entries(usize::from(partition.0), 0);
            }
            // A partition publishes nothing until its load finished, so searches keep waiting.
            if state.is_loaded {
                self.watermarks
                    .publish_projected(usize::from(partition.0), state.projected_through);
            }
            self.acks.cover(partition, &written.ack_segments);
        }
        Ok(())
    }

    fn batch_size(&self) -> usize {
        usize::try_from(self.cfg.outbox_batch_size)
            .expect("a u32 setting fits in usize")
            .max(1)
    }

    fn has_unprojected_entries(&self) -> bool {
        self.partitions.iter().enumerate().any(|(index, state)| {
            !state.is_disabled && self.watermarks.has_unprojected_entries(index)
        })
    }
}

async fn open_view(
    committer: CommandCommitter,
) -> Result<Arc<CommittedStateView>, OutboxConsumerError> {
    tokio::task::spawn_blocking(move || committer.committed_view())
        .await
        .map_err(|error| OutboxConsumerError::CommittedState {
            detail: error.to_string(),
        })?
        .map(Arc::new)
        .map_err(|error| OutboxConsumerError::CommittedState {
            detail: error.to_string(),
        })
}

/// Changes and progress of the partitions in one SQL transaction.
#[derive(Default)]
struct OutboxBatch {
    partitions: BTreeMap<PartitionId, PartitionWrite>,
    /// SearchOutbox entries the batch covers.
    entries: u64,
    /// Documents of initial loads in the batch.
    load_documents: u64,
}

struct PartitionWrite {
    batch: PartitionProjectionBatch,
    runs: BTreeSet<RunId>,
    /// Queued entries that this write covers, acked once it commits.
    ack_segments: Vec<AckSegment>,
}

impl OutboxBatch {
    fn partition(&mut self, partition: PartitionId) -> &mut PartitionWrite {
        self.partitions
            .entry(partition)
            .or_insert_with(|| PartitionWrite {
                batch: PartitionProjectionBatch {
                    partition_id: partition,
                    is_initial_load: false,
                    changes: Vec::new(),
                    outbox_through_seq: 0,
                    applied_position: None,
                },
                runs: BTreeSet::new(),
                ack_segments: Vec::new(),
            })
    }

    /// Adds changes in queue order; a run already in this transaction keeps its first, identical change.
    fn add(
        &mut self,
        partition: PartitionId,
        changes: Vec<SearchDocumentChange>,
        through_seq: u64,
        applied_position: Option<LogPosition>,
        ack_segment: AckSegment,
    ) {
        let write = self.partition(partition);
        for change in changes {
            if write.runs.insert(change.run_id()) {
                write.batch.changes.push(change);
            }
        }
        write.batch.outbox_through_seq = write.batch.outbox_through_seq.max(through_seq);
        if applied_position.is_some() {
            write.batch.applied_position = applied_position;
        }
        write.ack_segments.push(ack_segment);
    }

    fn add_initial_load(
        &mut self,
        partition: PartitionId,
        changes: Vec<SearchDocumentChange>,
        is_first_page: bool,
    ) {
        let write = self.partition(partition);
        write.batch.is_initial_load |= is_first_page;
        write.batch.changes.extend(changes);
    }

    fn change_count(&self) -> usize {
        self.partitions
            .values()
            .map(|write| write.batch.changes.len())
            .sum()
    }
}

/// One page of SearchOutbox entries and the current change of each distinct run.
struct OutboxPage {
    entries: Vec<OutboxEntry>,
    changes: Vec<SearchDocumentChange>,
}

fn read_outbox_page(
    view: &dyn FlowStateReader,
    partition: PartitionId,
    start: &[u8],
    end: &[u8],
    limit: usize,
) -> Result<OutboxPage, OutboxConsumerError> {
    let entries = read_outbox_entries(view, partition, start, end, limit)?;
    let mut runs = BTreeSet::new();
    let mut changes = Vec::new();
    for entry in &entries {
        if runs.insert(entry.run_id) {
            changes.push(read_run_change(
                view,
                entry.tenant_id,
                entry.flow_hash,
                entry.run_id,
            )?);
        }
    }
    Ok(OutboxPage { entries, changes })
}

/// Returns ack segments of the queued entries above `after_seq` and through `through_seq`, one per page.
fn read_ack_segments(
    view: &dyn FlowStateReader,
    partition: PartitionId,
    after_seq: u64,
    through_seq: u64,
    page_size: usize,
) -> Result<Vec<AckSegment>, OutboxConsumerError> {
    let (mut start, _) = search_outbox_range_after(partition, after_seq);
    let (_, end) = search_outbox_range_through(partition, through_seq);
    let mut segments: Vec<AckSegment> = Vec::new();
    loop {
        let entries = read_outbox_entries(view, partition, &start, &end, page_size)?;
        let Some(last) = entries.last() else {
            if segments
                .last()
                .is_some_and(|segment| segment.through_seq < through_seq)
            {
                segments.push(AckSegment {
                    through_seq,
                    entry_count: 0,
                });
            }
            return Ok(segments);
        };
        let is_last_page = entries.len() < page_size;
        // A full page may end inside one seq, so it covers only the seqs before its last one.
        segments.push(AckSegment {
            through_seq: if is_last_page {
                through_seq
            } else {
                last.seq.saturating_sub(1)
            },
            entry_count: entries.len() as u64,
        });
        if is_last_page {
            return Ok(segments);
        }
        start = last.key.clone();
        start.push(0);
    }
}

fn read_partition_heads(
    view: &dyn FlowStateReader,
    partitions: &[PartitionId],
) -> Result<Vec<PartitionHead>, OutboxConsumerError> {
    partitions
        .iter()
        .map(|partition| {
            let meta = view
                .get(ColumnFamily::PartitionMeta, &partition_meta_key(*partition))
                .map_err(committed_state_error)?
                .map(|bytes| PartitionMeta::decode(bytes.as_slice()))
                .transpose()
                .map_err(|error| OutboxConsumerError::CommittedState {
                    detail: format!("decode partition metadata: {error}"),
                })?
                .unwrap_or_default();
            let mode = view
                .get(
                    ColumnFamily::PartitionIndex,
                    &search_projection_mode_key(*partition),
                )
                .map_err(committed_state_error)?
                .map(|bytes| SearchProjectionModeRecord::decode(bytes.as_slice()))
                .transpose()
                .map_err(|error| OutboxConsumerError::CommittedState {
                    detail: format!("decode the search projection mode: {error}"),
                })?
                .unwrap_or_default();
            Ok(PartitionHead {
                applied_position: meta.applied,
                last_seq: u64::try_from(meta.last_command_timestamp_micros).unwrap_or(0),
                is_enabled: mode.enabled,
                acked_through_seq: mode.acked_through_seq,
                enabled_at_seq: mode.enabled_at_seq,
            })
        })
        .collect()
}

/// Runs a blocking read of `view` on the blocking pool.
async fn blocking_read<T: Send + 'static>(
    view: &Arc<CommittedStateView>,
    read: impl FnOnce(&dyn FlowStateReader) -> Result<T, OutboxConsumerError> + Send + 'static,
) -> Result<T, OutboxConsumerError> {
    let view = view.clone();
    tokio::task::spawn_blocking(move || read(view.as_ref()))
        .await
        .map_err(|error| OutboxConsumerError::CommittedState {
            detail: format!("the committed view reader failed: {error}"),
        })?
}

fn committed_state_error(error: dexzd_apply::StateReadError) -> OutboxConsumerError {
    OutboxConsumerError::CommittedState {
        detail: error.to_string(),
    }
}

fn partition_id(index: usize) -> PartitionId {
    PartitionId(u16::try_from(index).expect("the manifest limits partitions to u16"))
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
#[path = "outbox_consumer_tests.rs"]
mod tests;
