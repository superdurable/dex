// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Committed partition modes that the lease holder sets from configuration before rebuilding work.

use std::marker::PhantomData;
use std::sync::Arc;

use dexzd_apply::{ApplyContext, FlowStateReader};
use dexzd_engine::{
    CommandCommitter, CommitError, CommitTicket, CommitUrgency, PartitionEffectRebuildError,
    PartitionEffectRebuilder,
};
use dexzd_keyspace::{PartitionHasher, PartitionId, TenantId};
use dexzd_metrics::NodeMetrics;
use dexzd_protocol::dexzd::v1::{FlowCommand, command_reply, flow_command};
use futures::future::BoxFuture;
use tokio::sync::OnceCell;

use super::search_projection::SearchProjectionModeRebuilder;

/// Submissions retry while the commit backlog is full, for at most about five seconds.
const MODE_SUBMIT_ATTEMPTS: u32 = 50;
const MODE_SUBMIT_RETRY_DELAY: std::time::Duration = std::time::Duration::from_millis(100);

/// A per-partition mode that apply stores in committed state; an absent record means disabled.
pub(crate) trait CommittedPartitionMode: Send + Sync + 'static {
    /// Names the mode in logs and errors.
    const NAME: &'static str;

    /// Returns whether `view` stores the mode of `partition` as enabled.
    fn is_stored_enabled(
        view: &dyn FlowStateReader,
        partition: PartitionId,
    ) -> Result<bool, String>;

    /// Returns the command body that sets the mode of `partition`.
    fn set_mode_command(partition: PartitionId, is_enabled: bool) -> flow_command::Command;

    /// Counts a stopped partition that kept its previous mode.
    fn record_skipped_partition(metrics: &NodeMetrics);
}

/// Commits a partition mode before partitions rebuild work, so no speculative command sees it change.
pub(crate) struct PartitionModeRebuilder<Mode> {
    effects: Arc<dyn PartitionEffectRebuilder>,
    committer: CommandCommitter,
    metrics: Arc<NodeMetrics>,
    partition_hasher: PartitionHasher,
    is_enabled: bool,
    /// Holds the outcome of proposing every partition's mode; each engine start builds a new rebuilder.
    proposed: OnceCell<Result<(), String>>,
    mode: PhantomData<Mode>,
}

impl<Mode: CommittedPartitionMode> PartitionModeRebuilder<Mode> {
    pub(crate) fn new(
        effects: Arc<dyn PartitionEffectRebuilder>,
        committer: CommandCommitter,
        metrics: Arc<NodeMetrics>,
        partition_hasher: PartitionHasher,
        is_enabled: bool,
    ) -> Self {
        Self {
            effects,
            committer,
            metrics,
            partition_hasher,
            is_enabled,
            proposed: OnceCell::new(),
            mode: PhantomData,
        }
    }

    /// Proposes the mode to every partition whose stored mode differs, and waits until they apply.
    async fn propose_modes(&self) -> Result<(), String> {
        let committer = self.committer.clone();
        let partition_count = self.partition_hasher.partition_count();
        let is_enabled = self.is_enabled;
        let changed_partitions = tokio::task::spawn_blocking(move || {
            let view = committer
                .committed_view()
                .map_err(|error| error.to_string())?;
            partitions_with_other_mode::<Mode>(&view, partition_count, is_enabled)
        })
        .await
        .map_err(|error| format!("reading the {}s failed: {error}", Mode::NAME))??;
        if changed_partitions.is_empty() {
            return Ok(());
        }
        let flow_ids = self.partition_hasher.partition_command_flow_ids();
        let mut tickets: Vec<(PartitionId, CommitTicket)> = Vec::new();
        for partition in &changed_partitions {
            let flow_id = flow_ids
                .get(usize::from(partition.0))
                .expect("the hasher returns one Flow ID per partition")
                .clone();
            let context = ApplyContext {
                partition: *partition,
                flow_hash: self.partition_hasher.flow_hash(TenantId::DEFAULT, &flow_id),
            };
            let command = FlowCommand {
                flow_id,
                tenant_id: TenantId::DEFAULT.0,
                command: Some(Mode::set_mode_command(*partition, is_enabled)),
                ..FlowCommand::default()
            };
            if let Some(ticket) = self.submit_mode(*partition, context, command).await? {
                tickets.push((*partition, ticket));
            }
        }
        for (partition, ticket) in tickets {
            let committed = match ticket.wait().await {
                Ok(committed) => committed,
                Err(_) if self.committer.is_partition_halted(partition) => {
                    self.record_skipped_partition(partition);
                    continue;
                }
                Err(error) => {
                    return Err(format!(
                        "the {} of partition {} did not commit: {error}",
                        Mode::NAME,
                        partition.0
                    ));
                }
            };
            if let Some(command_reply::Reply::Error(error)) = committed.outcome.reply.reply {
                return Err(format!(
                    "apply rejected the {} of partition {}: {}",
                    Mode::NAME,
                    partition.0,
                    error.message
                ));
            }
        }
        tracing::info!(
            mode = Mode::NAME,
            partitions = changed_partitions.len(),
            is_enabled,
            "recorded a partition mode"
        );
        Ok(())
    }

    /// Submits one mode command, retrying while the commit backlog is full; each attempt first checks for a halt.
    ///
    /// Returns the commit ticket. Returns `None` for a stopped partition, which keeps its mode and
    /// is counted as skipped. Returns an error when the backlog stays full for every attempt, or
    /// when the committer rejects the command for another reason.
    async fn submit_mode(
        &self,
        partition: PartitionId,
        context: ApplyContext,
        command: FlowCommand,
    ) -> Result<Option<CommitTicket>, String> {
        let mut attempt = 1;
        loop {
            if self.committer.is_partition_halted(partition) {
                self.record_skipped_partition(partition);
                return Ok(None);
            }
            match self
                .committer
                .submit(context, command.clone(), CommitUrgency::Prompt)
            {
                Ok((_, ticket)) => return Ok(Some(ticket)),
                Err(CommitError::Unavailable { detail }) if attempt < MODE_SUBMIT_ATTEMPTS => {
                    tracing::debug!(mode = Mode::NAME, partition = partition.0, %detail, "retrying a partition mode");
                    attempt += 1;
                    tokio::time::sleep(MODE_SUBMIT_RETRY_DELAY).await;
                }
                Err(error) => {
                    return Err(format!(
                        "submitting the {} of partition {} failed: {error}",
                        Mode::NAME,
                        partition.0
                    ));
                }
            }
        }
    }

    fn record_skipped_partition(&self, partition: PartitionId) {
        tracing::error!(
            mode = Mode::NAME,
            partition = partition.0,
            is_enabled = self.is_enabled,
            "a stopped partition keeps its previous partition mode"
        );
        Mode::record_skipped_partition(&self.metrics);
    }
}

impl<Mode: CommittedPartitionMode> PartitionEffectRebuilder for PartitionModeRebuilder<Mode> {
    fn rebuild_partition(
        &self,
        partition: PartitionId,
    ) -> BoxFuture<'_, Result<usize, PartitionEffectRebuildError>> {
        Box::pin(async move {
            self.proposed
                .get_or_init(|| self.propose_modes())
                .await
                .clone()
                .map_err(|detail| PartitionEffectRebuildError { partition, detail })?;
            self.effects.rebuild_partition(partition).await
        })
    }
}

/// Returns the partitions whose stored mode is not `is_enabled`; an absent record means disabled.
fn partitions_with_other_mode<Mode: CommittedPartitionMode>(
    view: &dyn FlowStateReader,
    partition_count: u32,
    is_enabled: bool,
) -> Result<Vec<PartitionId>, String> {
    let mut partitions = Vec::new();
    for partition in 0..partition_count {
        let partition =
            PartitionId(u16::try_from(partition).expect("the manifest limits partitions to u16"));
        if Mode::is_stored_enabled(view, partition)? != is_enabled {
            partitions.push(partition);
        }
    }
    Ok(partitions)
}

/// Returns the rebuilder that commits the search projection mode, then delegates to `effects` for
/// each partition.
pub(crate) fn partition_mode_rebuilder(
    effects: Arc<dyn PartitionEffectRebuilder>,
    committer: &CommandCommitter,
    metrics: &Arc<NodeMetrics>,
    partition_hasher: &PartitionHasher,
    is_search_projection_enabled: bool,
) -> SearchProjectionModeRebuilder {
    SearchProjectionModeRebuilder::new(
        effects,
        committer.clone(),
        metrics.clone(),
        partition_hasher.clone(),
        is_search_projection_enabled,
    )
}
