// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::Duration;

use dexzd_apply::ApplyContext;
use dexzd_engine::{CommandCommitter, CommitError, CommitUrgency, CommittedCommand};
use dexzd_keyspace::{PartitionHasher, PartitionId, TenantId};
use dexzd_metrics::{NodeMetrics, Outcome};
use dexzd_protocol::dexzd::v1::{FlowCommand, SearchOutboxAckCommand, command_reply, flow_command};
use futures::FutureExt;
use futures::future::BoxFuture;
use futures::stream::{FuturesUnordered, StreamExt};
use tokio::time::Instant;

/// Proposes SearchOutboxAckCommand once SQL visibility covers entries, at most once per interval and partition.
pub(super) struct OutboxAcks {
    committer: CommandCommitter,
    flow_ids: Vec<String>,
    partition_hasher: PartitionHasher,
    interval: Duration,
    metrics: Arc<NodeMetrics>,
    partitions: Vec<PartitionAcks>,
    in_flight: FuturesUnordered<BoxFuture<'static, AckCompletion>>,
}

#[derive(Default)]
struct PartitionAcks {
    /// Highest seq SQL visibility covers whose range still holds entries.
    target: u64,
    /// Highest seq an applied ack deleted through.
    acked: u64,
    is_in_flight: bool,
    last_proposed_at: Option<Instant>,
}

struct AckCompletion {
    partition: PartitionId,
    through_seq: u64,
    committed: Result<CommittedCommand, CommitError>,
}

impl OutboxAcks {
    pub(super) fn new(
        committer: CommandCommitter,
        partition_hasher: PartitionHasher,
        interval: Duration,
        metrics: Arc<NodeMetrics>,
    ) -> Self {
        let flow_ids = partition_hasher.partition_command_flow_ids();
        let partitions = flow_ids.iter().map(|_| PartitionAcks::default()).collect();
        Self {
            committer,
            flow_ids,
            partition_hasher,
            interval,
            metrics,
            partitions,
            in_flight: FuturesUnordered::new(),
        }
    }

    /// Records that SQL visibility covers queued entries of `partition` through `through_seq`.
    pub(super) fn cover(&mut self, partition: PartitionId, through_seq: u64) {
        if let Some(acks) = self.partitions.get_mut(usize::from(partition.0)) {
            acks.target = acks.target.max(through_seq);
        }
    }

    /// Collects finished proposals, then proposes every partition whose interval has passed.
    pub(super) fn propose_due(&mut self) {
        while let Some(Some(completion)) = self.in_flight.next().now_or_never() {
            self.finish(completion);
        }
        let now = Instant::now();
        for index in 0..self.partitions.len() {
            let acks = &self.partitions[index];
            let is_due = acks.target > acks.acked
                && !acks.is_in_flight
                && acks
                    .last_proposed_at
                    .is_none_or(|proposed_at| now.duration_since(proposed_at) >= self.interval);
            if is_due {
                self.propose(index, now);
            }
        }
    }

    /// Returns how long until a pending ack is due, or `None` when no ack waits.
    pub(super) fn next_due_in(&self) -> Option<Duration> {
        let now = Instant::now();
        self.partitions
            .iter()
            .filter(|acks| acks.target > acks.acked && !acks.is_in_flight)
            .map(|acks| {
                acks.last_proposed_at.map_or(Duration::ZERO, |proposed_at| {
                    self.interval
                        .saturating_sub(now.duration_since(proposed_at))
                })
            })
            .min()
    }

    /// Waits until a proposal finishes; never completes while none is in flight.
    pub(super) async fn wait_for_completion(&mut self) {
        match self.in_flight.next().await {
            Some(completion) => self.finish(completion),
            None => std::future::pending().await,
        }
    }

    fn propose(&mut self, index: usize, now: Instant) {
        let partition =
            PartitionId(u16::try_from(index).expect("the manifest limits partitions to u16"));
        let flow_id = self.flow_ids[index].clone();
        let through_seq = self.partitions[index].target;
        let context = ApplyContext {
            partition,
            flow_hash: self.partition_hasher.flow_hash(TenantId::DEFAULT, &flow_id),
        };
        let command = FlowCommand {
            flow_id,
            tenant_id: TenantId::DEFAULT.0,
            command: Some(flow_command::Command::SearchOutboxAck(
                SearchOutboxAckCommand {
                    partition: u32::from(partition.0),
                    through_seq,
                },
            )),
            ..FlowCommand::default()
        };
        let acks = &mut self.partitions[index];
        acks.last_proposed_at = Some(now);
        match self
            .committer
            .submit(context, command, CommitUrgency::Batched)
        {
            Ok((_, ticket)) => {
                acks.is_in_flight = true;
                self.in_flight.push(Box::pin(async move {
                    AckCompletion {
                        partition,
                        through_seq,
                        committed: ticket.wait().await,
                    }
                }));
            }
            Err(error) => {
                self.metrics.record_search_outbox_ack(Outcome::Error);
                tracing::debug!(partition = partition.0, %error, "a search outbox ack was not submitted; retrying");
            }
        }
    }

    fn finish(&mut self, completion: AckCompletion) {
        let AckCompletion {
            partition,
            through_seq,
            committed,
        } = completion;
        let Some(acks) = self.partitions.get_mut(usize::from(partition.0)) else {
            return;
        };
        acks.is_in_flight = false;
        match committed.map(|committed| committed.outcome.reply.reply) {
            Ok(Some(command_reply::Reply::Error(error))) => {
                self.metrics.record_search_outbox_ack(Outcome::Error);
                tracing::warn!(partition = partition.0, message = %error.message, "apply rejected a search outbox ack");
            }
            Ok(_) => {
                self.metrics.record_search_outbox_ack(Outcome::Ok);
                acks.acked = acks.acked.max(through_seq);
            }
            Err(error) => {
                self.metrics.record_search_outbox_ack(Outcome::Error);
                tracing::debug!(partition = partition.0, %error, "a search outbox ack did not commit; retrying");
            }
        }
    }
}
