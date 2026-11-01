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
use dexzd_keyspace::row_keys::{search_outbox_range_after, search_projection_mode_key};
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

use super::outbox_acks::OutboxAcks;
use super::outbox_watermarks::{SearchOutboxWatermarks, SearchProjectionFailure};

const FIRST_RETRY_DELAY: Duration = Duration::from_millis(100);
const MAX_RETRY_DELAY: Duration = Duration::from_secs(10);

/// Settings of the external search projection, from the `search` section.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct SearchOutboxConfig {
    /// Most SearchOutbox entries or loaded documents in one SQL transaction; `search.outboxBatchSize`.
    pub(crate) batch_size: usize,
    /// How often an idle projection looks for new entries; `search.outboxPollInterval`.
    pub(crate) poll_interval: Duration,
    /// Least time between two acks of one partition; `search.outboxAckInterval`.
    pub(crate) ack_interval: Duration,
}

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

/// Projects every partition's SearchOutbox into SQL visibility, then acks the projected entries.
///
/// Only the lease holder may run it. It keeps no state outside itself, so a node can stop and
/// later restart it.
pub(crate) struct SearchOutboxConsumer {
    config: SearchOutboxConfig,
    projector: SqlVisibilityProjector,
    committer: CommandCommitter,
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
    /// SQL visibility finished this partition's initial load.
    is_loaded: bool,
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
    pub(crate) fn new(config: &SearchOutboxConfig, dependencies: SearchOutboxDependencies) -> Self {
        let partition_count = dependencies.partition_hasher.partition_count() as usize;
        Self {
            config: SearchOutboxConfig {
                batch_size: config.batch_size.max(1),
                ..config.clone()
            },
            acks: OutboxAcks::new(
                dependencies.committer.clone(),
                dependencies.partition_hasher,
                config.ack_interval,
                dependencies.metrics.clone(),
            ),
            projector: dependencies.projector,
            committer: dependencies.committer,
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
        while let Err(error) = self.read_progress().await {
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
            let ack_due = self.acks.next_due_in().unwrap_or(self.config.poll_interval);
            tokio::select! {
                () = watermarks.consumer_wake() => {}
                () = tokio::time::sleep(self.config.poll_interval.min(ack_due)) => {}
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

    async fn read_progress(&mut self) -> Result<(), OutboxConsumerError> {
        let progress = self.projector.read_all_progress().await?;
        for (partition, recorded) in progress {
            let Some(state) = self.partitions.get_mut(usize::from(partition.0)) else {
                tracing::warn!(
                    partition = partition.0,
                    "SQL visibility has progress for a partition outside the cluster"
                );
                continue;
            };
            let ProjectionProgress {
                applied_position,
                applied_epoch,
                outbox_through_seq,
            } = recorded;
            if let Some(stored_epoch) = applied_epoch.filter(|epoch| *epoch > self.node_epoch) {
                return Err(ProjectionError::Fenced {
                    partition_id: partition,
                    stored_epoch,
                    epoch: self.node_epoch,
                }
                .into());
            }
            state.is_loaded = applied_position.is_some();
            state.projected_through = outbox_through_seq;
            self.watermarks
                .publish_projected(usize::from(partition.0), outbox_through_seq);
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
            let is_loaded = state.is_loaded;
            if !head.is_enabled {
                continue;
            }
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
        let (mut start, end) = search_outbox_range_after(partition, projected_through);
        let page_size = self.config.batch_size;
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
                        true,
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
                true,
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

    /// Loads every searchable document of the view into a partition that SQL visibility never finished.
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
        tracing::info!(
            partition = partition.0,
            "loading the partition into an empty SQL visibility projection"
        );
        let page_size = self.config.batch_size;
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
        let (range_start, range_end) = search_outbox_range_after(partition, 0);
        let has_entries = blocking_read(view, move |view| {
            read_outbox_entries(view, partition, &range_start, &range_end, 1)
                .map(|entries| !entries.is_empty())
                .map_err(OutboxConsumerError::from)
        })
        .await?;
        batch.add_initial_load(partition, Vec::new(), is_first_page);
        batch.add(
            partition,
            Vec::new(),
            head.last_seq,
            Some(applied_position),
            has_entries,
        );
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
            }
            self.watermarks
                .publish_projected(usize::from(partition.0), state.projected_through);
            if written.covers_entries {
                self.acks.cover(partition, written.batch.outbox_through_seq);
            }
        }
        Ok(())
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
    /// The covered seq range holds queued entries, so it is worth an ack.
    covers_entries: bool,
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
                covers_entries: false,
            })
    }

    /// Adds changes in queue order; a run already in this transaction keeps its first, identical change.
    fn add(
        &mut self,
        partition: PartitionId,
        changes: Vec<SearchDocumentChange>,
        through_seq: u64,
        applied_position: Option<LogPosition>,
        covers_entries: bool,
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
        write.covers_entries |= covers_entries;
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
