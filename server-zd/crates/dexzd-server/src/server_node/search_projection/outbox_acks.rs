// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::VecDeque;
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

use crate::config::SearchConfig;

/// Projected entries of one partition through `through_seq`; acks combine whole segments.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) struct AckSegment {
    pub(super) through_seq: u64,
    /// Queued entries in the segment, counted when the consumer read them.
    pub(super) entry_count: u64,
}

/// Proposes SearchOutboxAckCommand once SQL visibility covers entries, at most once per interval and partition.
pub(super) struct OutboxAcks {
    cfg: Arc<SearchConfig>,
    committer: CommandCommitter,
    flow_ids: Vec<String>,
    partition_hasher: PartitionHasher,
    metrics: Arc<NodeMetrics>,
    partitions: Vec<PartitionAcks>,
    in_flight: FuturesUnordered<BoxFuture<'static, AckCompletion>>,
}

#[derive(Default)]
struct PartitionAcks {
    /// Covered but unacked entries in seq order.
    segments: VecDeque<AckSegment>,
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
        cfg: Arc<SearchConfig>,
        committer: CommandCommitter,
        partition_hasher: PartitionHasher,
        metrics: Arc<NodeMetrics>,
    ) -> Self {
        let flow_ids = partition_hasher.partition_command_flow_ids();
        let partitions = flow_ids.iter().map(|_| PartitionAcks::default()).collect();
        Self {
            cfg,
            committer,
            flow_ids,
            partition_hasher,
            metrics,
            partitions,
            in_flight: FuturesUnordered::new(),
        }
    }

    /// Records the partition's acked seq from a committed view, so later acks start after it.
    pub(super) fn record_acked(&mut self, partition: PartitionId, acked_through_seq: u64) {
        if let Some(acks) = self.partitions.get_mut(usize::from(partition.0)) {
            acks.acked = acks.acked.max(acked_through_seq);
        }
    }

    /// Records that SQL visibility covers `segments` of `partition`; segments already covered are ignored.
    pub(super) fn cover(&mut self, partition: PartitionId, segments: &[AckSegment]) {
        let Some(acks) = self.partitions.get_mut(usize::from(partition.0)) else {
            return;
        };
        for segment in segments {
            let covered_through = acks
                .segments
                .back()
                .map_or(acks.acked, |last| last.through_seq);
            if segment.through_seq > covered_through {
                acks.segments.push_back(*segment);
            }
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
            let is_due = !acks.segments.is_empty()
                && !acks.is_in_flight
                && acks.last_proposed_at.is_none_or(|proposed_at| {
                    now.duration_since(proposed_at) >= self.cfg.outbox_ack_interval
                });
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
            .filter(|acks| !acks.segments.is_empty() && !acks.is_in_flight)
            .map(|acks| {
                acks.last_proposed_at.map_or(Duration::ZERO, |proposed_at| {
                    self.cfg
                        .outbox_ack_interval
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
        let through_seq = capped_through_seq(
            &self.partitions[index].segments,
            u64::from(self.cfg.outbox_max_ack_entries),
        );
        let flow_id = self.flow_ids[index].clone();
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
                while acks
                    .segments
                    .front()
                    .is_some_and(|segment| segment.through_seq <= acks.acked)
                {
                    acks.segments.pop_front();
                }
            }
            Err(error) => {
                self.metrics.record_search_outbox_ack(Outcome::Error);
                tracing::debug!(partition = partition.0, %error, "a search outbox ack did not commit; retrying");
            }
        }
    }
}

/// Returns the seq that acks the leading segments holding at most `max_entries`, and always the first.
fn capped_through_seq(segments: &VecDeque<AckSegment>, max_entries: u64) -> u64 {
    let mut entries: u64 = 0;
    let mut through_seq = 0;
    for segment in segments {
        entries = entries.saturating_add(segment.entry_count);
        if entries > max_entries && through_seq > 0 {
            break;
        }
        through_seq = segment.through_seq;
    }
    through_seq
}

#[cfg(test)]
mod tests {
    use super::*;

    fn segments(pairs: &[(u64, u64)]) -> VecDeque<AckSegment> {
        pairs
            .iter()
            .map(|(through_seq, entry_count)| AckSegment {
                through_seq: *through_seq,
                entry_count: *entry_count,
            })
            .collect()
    }

    #[test]
    fn an_ack_stops_before_the_segment_that_exceeds_the_cap() {
        let queued = segments(&[(10, 400), (20, 400), (30, 400), (40, 0)]);
        assert_eq!(capped_through_seq(&queued, 1_000), 20);
        assert_eq!(capped_through_seq(&queued, 1_200), 40);
        assert_eq!(
            capped_through_seq(&segments(&[(10, 5_000), (20, 1)]), 1_000),
            10,
            "the first segment is acked even when it alone exceeds the cap"
        );
    }
}
