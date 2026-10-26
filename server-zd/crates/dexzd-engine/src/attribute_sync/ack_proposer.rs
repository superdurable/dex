// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Proposes Attribute Sync acks once their Attribute Store writes committed.

use std::sync::Arc;

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, FlowCommand, command_reply, flow_command,
};
use tokio_util::sync::CancellationToken;

use super::AttributeSyncRetryPolicy;
use super::projector::next_retry_delay;
use crate::effects::{FlowEffectOperationError, FlowEffectOperations, RunLocation};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// One run's ack, proposed once its write committed.
pub(super) struct PendingAck {
    pub(super) flow: FlowAddress,
    pub(super) run: RunLocation,
    pub(super) ack: AttributeSyncAckCommand,
}

/// One (partition, store) pair's acks, proposed once their writes committed.
pub(super) struct AckBatch {
    pub(super) partition: PartitionId,
    pub(super) store_name: String,
    pub(super) acks: Vec<PendingAck>,
}

/// How an ack batch ended.
pub(super) struct AckBatchResult {
    pub(super) partition: PartitionId,
    pub(super) store_name: String,
    /// Runs whose ack apply rejected; their entries stay pending.
    pub(super) rejected_runs: Vec<RunLocation>,
    pub(super) has_stopped: bool,
}

/// Proposes acks from a background task, so writes of later partitions need not wait for commits.
#[derive(Clone)]
pub(super) struct AckProposer {
    pub(super) flow_operations: Arc<dyn FlowEffectOperations>,
    pub(super) stop: CancellationToken,
    pub(super) retry_policy: AttributeSyncRetryPolicy,
}

impl AckProposer {
    /// Proposes every ack of `batch` concurrently, so one group commit batch carries them.
    pub(super) async fn propose_batch(self, batch: AckBatch) -> AckBatchResult {
        let AckBatch {
            partition,
            store_name,
            acks,
        } = batch;
        let proposer = &self;
        let proposals = acks.into_iter().map(|pending| async move {
            let command = FlowCommand {
                flow_id: pending.flow.flow_id.clone(),
                run_id: pending.run.run_id.as_bytes().to_vec(),
                tenant_id: pending.flow.tenant_id.0,
                command: Some(flow_command::Command::AttributeSyncAck(pending.ack)),
                ..FlowCommand::default()
            };
            let reply = proposer
                .propose_until_committed(&pending.flow, command)
                .await;
            (pending.flow, pending.run, reply)
        });
        let mut result = AckBatchResult {
            partition,
            store_name,
            rejected_runs: Vec::new(),
            has_stopped: false,
        };
        for (flow, run, reply) in futures::future::join_all(proposals).await {
            match reply {
                None => result.has_stopped = true,
                Some(command_reply::Reply::Error(error)) => {
                    tracing::warn!(
                        store_name = %result.store_name,
                        flow_id = %flow.flow_id,
                        message = %error.message,
                        "an Attribute Sync ack was rejected"
                    );
                    result.rejected_runs.push(run);
                }
                Some(_) => {}
            }
        }
        result
    }

    /// Retries while the runtime is unavailable; returns `None` when the projector or node stops.
    async fn propose_until_committed(
        &self,
        flow: &FlowAddress,
        command: FlowCommand,
    ) -> Option<command_reply::Reply> {
        let mut delay = self.retry_policy.initial_interval;
        loop {
            let proposal = self.flow_operations.propose_committed(
                flow,
                command.clone(),
                CommitUrgency::Batched,
            );
            let outcome = tokio::select! {
                biased;
                () = self.stop.cancelled() => return None,
                outcome = proposal => outcome,
            };
            match outcome {
                Ok(reply) => {
                    return Some(reply.reply.unwrap_or(command_reply::Reply::Ok(
                        dexzd_protocol::dexzd::v1::EmptyReply::default(),
                    )));
                }
                Err(FlowEffectOperationError::NodeStopping { .. }) => return None,
                Err(error @ FlowEffectOperationError::Unavailable { .. }) => {
                    tracing::debug!(%error, flow_id = %flow.flow_id, "retrying an Attribute Sync ack");
                    tokio::select! {
                        biased;
                        () = self.stop.cancelled() => return None,
                        () = tokio::time::sleep(delay) => {}
                    }
                    delay = next_retry_delay(&self.retry_policy, delay);
                }
            }
        }
    }
}
