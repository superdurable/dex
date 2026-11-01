// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_apply::{ApplyContext, FlowStateReader};
use dexzd_engine::{
    CommandCommitter, CommitError, CommitTicket, CommitUrgency, PartitionEffectRebuildError,
    PartitionEffectRebuilder,
};
use dexzd_keyspace::row_keys::search_projection_mode_key;
use dexzd_keyspace::{ColumnFamily, PartitionHasher, PartitionId, TenantId};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, SearchProjectionModeRecord, SetSearchProjectionModeCommand, command_reply,
    flow_command,
};
use futures::future::BoxFuture;
use prost::Message;
use tokio::sync::OnceCell;

/// Submissions retry while the commit backlog is full, for at most about five seconds.
const MODE_SUBMIT_ATTEMPTS: u32 = 50;
const MODE_SUBMIT_RETRY_DELAY: std::time::Duration = std::time::Duration::from_millis(100);

/// Commits the search projection mode before partitions rebuild work, so no speculative command sees it change.
pub(crate) struct SearchProjectionModeRebuilder {
    effects: Arc<dyn PartitionEffectRebuilder>,
    committer: CommandCommitter,
    partition_hasher: PartitionHasher,
    is_enabled: bool,
    /// Holds the outcome of proposing every partition's mode; each engine start builds a new rebuilder.
    proposed: OnceCell<Result<(), String>>,
}

impl SearchProjectionModeRebuilder {
    pub(crate) fn new(
        effects: Arc<dyn PartitionEffectRebuilder>,
        committer: CommandCommitter,
        partition_hasher: PartitionHasher,
        is_enabled: bool,
    ) -> Self {
        Self {
            effects,
            committer,
            partition_hasher,
            is_enabled,
            proposed: OnceCell::new(),
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
            partitions_with_other_mode(&view, partition_count, is_enabled)
        })
        .await
        .map_err(|error| format!("reading search projection modes failed: {error}"))??;
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
                command: Some(flow_command::Command::SetSearchProjectionMode(
                    SetSearchProjectionModeCommand {
                        enabled: is_enabled,
                    },
                )),
                ..FlowCommand::default()
            };
            if let Some(ticket) = self.submit_mode(*partition, context, command).await? {
                tickets.push((*partition, ticket));
            }
        }
        for (partition, ticket) in tickets {
            let committed = ticket.wait().await.map_err(|error| {
                format!(
                    "the search projection mode of partition {} did not commit: {error}",
                    partition.0
                )
            })?;
            if let Some(command_reply::Reply::Error(error)) = committed.outcome.reply.reply {
                return Err(format!(
                    "apply rejected the search projection mode of partition {}: {}",
                    partition.0, error.message
                ));
            }
        }
        tracing::info!(
            partitions = changed_partitions.len(),
            is_enabled,
            "recorded the external search projection mode"
        );
        Ok(())
    }
}

impl SearchProjectionModeRebuilder {
    /// Submits one mode command, retrying while the commit backlog is full.
    ///
    /// Returns `None` for a stopped partition, which applies no command and keeps its mode.
    async fn submit_mode(
        &self,
        partition: PartitionId,
        context: ApplyContext,
        command: FlowCommand,
    ) -> Result<Option<CommitTicket>, String> {
        let mut attempt = 1;
        loop {
            match self
                .committer
                .submit(context, command.clone(), CommitUrgency::Prompt)
            {
                Ok((_, ticket)) => return Ok(Some(ticket)),
                Err(CommitError::Unavailable { detail })
                    if detail.ends_with("stopped applying commands") =>
                {
                    tracing::warn!(
                        partition = partition.0,
                        %detail,
                        "a stopped partition keeps its previous search projection mode"
                    );
                    return Ok(None);
                }
                Err(CommitError::Unavailable { detail }) if attempt < MODE_SUBMIT_ATTEMPTS => {
                    tracing::debug!(partition = partition.0, %detail, "retrying a search projection mode");
                    attempt += 1;
                    tokio::time::sleep(MODE_SUBMIT_RETRY_DELAY).await;
                }
                Err(error) => {
                    return Err(format!(
                        "submitting the search projection mode of partition {} failed: {error}",
                        partition.0
                    ));
                }
            }
        }
    }
}

impl PartitionEffectRebuilder for SearchProjectionModeRebuilder {
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
fn partitions_with_other_mode(
    view: &dyn FlowStateReader,
    partition_count: u32,
    is_enabled: bool,
) -> Result<Vec<PartitionId>, String> {
    let mut partitions = Vec::new();
    for partition in 0..partition_count {
        let partition =
            PartitionId(u16::try_from(partition).expect("the manifest limits partitions to u16"));
        let stored = view
            .get(
                ColumnFamily::PartitionIndex,
                &search_projection_mode_key(partition),
            )
            .map_err(|error| error.to_string())?
            .map(|bytes| SearchProjectionModeRecord::decode(bytes.as_slice()))
            .transpose()
            .map_err(|error| format!("decode the search projection mode: {error}"))?
            .is_some_and(|mode| mode.enabled);
        if stored != is_enabled {
            partitions.push(partition);
        }
    }
    Ok(partitions)
}
