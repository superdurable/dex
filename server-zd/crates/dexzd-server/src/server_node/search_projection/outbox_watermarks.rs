// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::{AtomicU64, Ordering};

use async_trait::async_trait;
use dexzd_apply::StateWrite;
use dexzd_engine::CommittedFlowCommand;
use dexzd_keyspace::ColumnFamily;
use dexzd_keyspace::row_keys::decode_search_outbox_key;
use dexzd_metrics::SearchOutboxBacklog;
use dexzd_search::sql_visibility::ProjectionVisibilityWait;
use dexzd_search::{SearchError, SearchRequest};
use tokio::sync::{Notify, watch};

/// Per-partition SearchOutbox seqs that committed commands appended and that SQL visibility covers.
///
/// Read-your-writes compares them: a SearchFlows request waits until every partition covers
/// what was appended before the request.
pub(crate) struct SearchOutboxWatermarks {
    appended: Vec<AtomicU64>,
    projected: Vec<AtomicU64>,
    /// Receives each partition's oldest unprojected seq, for the lag metric.
    backlog: SearchOutboxBacklog,
    /// Bumped whenever a projected seq rises, so waiters recheck.
    projected_version: watch::Sender<u64>,
    consumer_wake: Notify,
    failure: watch::Sender<Option<SearchProjectionFailure>>,
}

/// Why the external search projection stopped for good.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) enum SearchProjectionFailure {
    /// A newer lease holder writes SQL visibility; this node is about to stop.
    Fenced(String),
    /// Committed state or SQL visibility cannot be projected; SearchFlows fails until restart.
    Permanent(String),
}

impl SearchOutboxWatermarks {
    pub(crate) fn new(partition_count: u32, backlog: SearchOutboxBacklog) -> Self {
        let partition_count = partition_count as usize;
        Self {
            appended: (0..partition_count).map(|_| AtomicU64::new(0)).collect(),
            projected: (0..partition_count).map(|_| AtomicU64::new(0)).collect(),
            backlog,
            projected_version: watch::Sender::new(0),
            consumer_wake: Notify::new(),
            failure: watch::Sender::new(None),
        }
    }

    /// Records the SearchOutbox entries that one committed command appended, and wakes the projection.
    pub(crate) fn record_committed(&self, committed: &CommittedFlowCommand) {
        let partition = committed.context.partition;
        let appended_seq = committed
            .committed
            .outcome
            .writes
            .iter()
            .filter_map(|write| match write {
                StateWrite::Put {
                    column_family: ColumnFamily::PartitionIndex,
                    key,
                    ..
                } => decode_search_outbox_key(partition, key).map(|entry| entry.seq),
                _ => None,
            })
            .max();
        let Some(appended_seq) = appended_seq else {
            return;
        };
        let index = usize::from(partition.0);
        let (Some(appended), Some(projected)) =
            (self.appended.get(index), self.projected.get(index))
        else {
            return;
        };
        let previous = appended.fetch_max(appended_seq, Ordering::SeqCst);
        if previous <= projected.load(Ordering::SeqCst) {
            self.backlog
                .set_oldest_unprojected(index, i64::try_from(appended_seq).ok());
        }
        self.consumer_wake.notify_one();
    }

    /// Returns whether a partition has appended entries that SQL visibility does not cover yet.
    pub(crate) fn has_unprojected_entries(&self, partition: usize) -> bool {
        match (self.appended.get(partition), self.projected.get(partition)) {
            (Some(appended), Some(projected)) => {
                appended.load(Ordering::SeqCst) > projected.load(Ordering::SeqCst)
            }
            _ => false,
        }
    }

    /// Records that SQL visibility covers every entry of `partition` through `through_seq`.
    pub(crate) fn publish_projected(&self, partition: usize, through_seq: u64) {
        let (Some(appended), Some(projected)) =
            (self.appended.get(partition), self.projected.get(partition))
        else {
            return;
        };
        if projected.fetch_max(through_seq, Ordering::SeqCst) >= through_seq {
            return;
        }
        // Entries arrive in seq order, so the oldest unprojected one follows the projected seq.
        let oldest = (appended.load(Ordering::SeqCst) > through_seq)
            .then(|| i64::try_from(through_seq.saturating_add(1)).ok())
            .flatten();
        self.backlog.set_oldest_unprojected(partition, oldest);
        self.projected_version
            .send_modify(|version| *version = version.wrapping_add(1));
    }

    /// Returns a future that completes at the next wake-up request after this call.
    pub(crate) fn consumer_wake(&self) -> tokio::sync::futures::Notified<'_> {
        self.consumer_wake.notified()
    }

    pub(crate) fn record_failure(&self, failure: SearchProjectionFailure) {
        self.failure.send_replace(Some(failure));
    }

    /// Returns `(partition, appended seq)` for every partition that SQL visibility does not cover yet.
    fn unprojected_partitions(&self) -> Vec<(usize, u64)> {
        self.appended
            .iter()
            .zip(&self.projected)
            .enumerate()
            .filter_map(|(partition, (appended, projected))| {
                let appended = appended.load(Ordering::SeqCst);
                (appended > projected.load(Ordering::SeqCst)).then_some((partition, appended))
            })
            .collect()
    }

    fn failure_error(&self) -> Option<SearchError> {
        self.failure.borrow().as_ref().map(|failure| match failure {
            SearchProjectionFailure::Fenced(reason) => SearchError::Unavailable(reason.clone()),
            SearchProjectionFailure::Permanent(reason) => SearchError::Internal(reason.clone()),
        })
    }
}

#[async_trait]
impl ProjectionVisibilityWait for SearchOutboxWatermarks {
    async fn wait_until_visible(&self, _request: SearchRequest<'_>) -> Result<(), SearchError> {
        if let Some(error) = self.failure_error() {
            return Err(error);
        }
        let mut projected_version = self.projected_version.subscribe();
        let mut failure = self.failure.subscribe();
        let mut required = self.unprojected_partitions();
        if required.is_empty() {
            return Ok(());
        }
        self.consumer_wake.notify_one();
        loop {
            required.retain(|(partition, appended)| {
                self.projected
                    .get(*partition)
                    .is_some_and(|projected| projected.load(Ordering::SeqCst) < *appended)
            });
            if required.is_empty() {
                return Ok(());
            }
            tokio::select! {
                changed = projected_version.changed() => {
                    changed.map_err(|_| SearchError::Unavailable("the search projection stopped".to_owned()))?;
                }
                failed = failure.wait_for(Option::is_some) => {
                    failed.map_err(|_| SearchError::Unavailable("the search projection stopped".to_owned()))?;
                }
            }
            if let Some(error) = self.failure_error() {
                return Err(error);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::time::Duration;

    use dexzd_apply::{ApplyContext, ApplyOutcome};
    use dexzd_engine::CommittedCommand;
    use dexzd_keyspace::row_keys::search_outbox_key;
    use dexzd_keyspace::{FlowHash, PartitionId, RunId};
    use dexzd_protocol::dexzd::v1::{CommandReply, FlowCommand, LogPosition};

    use super::*;

    fn committed_append(partition: PartitionId, seqs: &[u64]) -> CommittedFlowCommand {
        CommittedFlowCommand {
            command: FlowCommand::default(),
            context: ApplyContext {
                partition,
                flow_hash: FlowHash(1),
            },
            committed: CommittedCommand {
                outcome: ApplyOutcome {
                    writes: seqs
                        .iter()
                        .map(|seq| StateWrite::Put {
                            column_family: ColumnFamily::PartitionIndex,
                            key: search_outbox_key(partition, *seq, FlowHash(1), RunId([1; 16])),
                            value: Vec::new(),
                        })
                        .collect(),
                    effects: Vec::new(),
                    reply: CommandReply::default(),
                    run_id: None,
                },
                position: LogPosition::default(),
            },
        }
    }

    async fn wait(watermarks: &SearchOutboxWatermarks) -> Result<(), SearchError> {
        let registry = dexzd_visibility_query::FieldRegistry::new();
        watermarks
            .wait_until_visible(SearchRequest {
                tenant_id: dexzd_keyspace::TenantId(0),
                query: "",
                page_size: 1,
                page_token: None,
                field_registry: &registry,
            })
            .await
    }

    #[tokio::test(start_paused = true)]
    async fn a_search_waits_until_every_appended_seq_is_projected() {
        let watermarks = Arc::new(SearchOutboxWatermarks::new(
            4,
            dexzd_metrics::NodeMetrics::unregistered().search_outbox_backlog,
        ));
        assert!(wait(&watermarks).await.is_ok(), "nothing appended");
        watermarks.record_committed(&committed_append(PartitionId(1), &[10, 12]));
        watermarks.record_committed(&committed_append(PartitionId(3), &[7]));
        assert!(watermarks.has_unprojected_entries(1));
        assert!(!watermarks.has_unprojected_entries(2));
        let waiting = tokio::spawn({
            let watermarks = watermarks.clone();
            async move { wait(&watermarks).await }
        });
        tokio::time::sleep(Duration::from_millis(10)).await;
        watermarks.publish_projected(1, 12);
        tokio::time::sleep(Duration::from_millis(10)).await;
        assert!(!waiting.is_finished(), "partition 3 is not projected yet");
        watermarks.publish_projected(3, 6);
        tokio::time::sleep(Duration::from_millis(10)).await;
        assert!(
            !waiting.is_finished(),
            "a lower seq does not cover the append"
        );
        watermarks.publish_projected(3, 100);
        assert!(waiting.await.expect("the waiter finishes").is_ok());
        watermarks.publish_projected(1, 5);
        assert!(
            !watermarks.has_unprojected_entries(1),
            "progress never decreases"
        );
    }

    #[tokio::test]
    async fn failures_end_waits_with_their_status() {
        let watermarks = SearchOutboxWatermarks::new(
            1,
            dexzd_metrics::NodeMetrics::unregistered().search_outbox_backlog,
        );
        watermarks.record_committed(&committed_append(PartitionId(0), &[3]));
        watermarks.record_failure(SearchProjectionFailure::Fenced("newer owner".into()));
        assert!(matches!(
            wait(&watermarks).await,
            Err(SearchError::Unavailable(reason)) if reason == "newer owner"
        ));
        watermarks.record_failure(SearchProjectionFailure::Permanent("bad row".into()));
        assert!(matches!(
            wait(&watermarks).await,
            Err(SearchError::Internal(reason)) if reason == "bad row"
        ));
    }
}
