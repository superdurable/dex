// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_cluster_metadata::{LeaseValidityDeadline, MetadataError};
use dexzd_keyspace::PartitionId;
use dexzd_log::StreamIdentity;
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dexzd::v1::LogPosition;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use crate::command_applier::CommandApplier;
use crate::commit::{
    CommitState, EngineLocalStore, FrameApplier, FrameApplierParts, NodeStopReason,
    PartitionApplierPool,
};
use crate::engine_id_source::EngineIdSource;
use crate::engine_node::EngineNodeConfig;
use crate::flow_coordination::CommittedFlowCommand;
use crate::hot_standby::log_follower::{
    FollowEnd, FollowPacing, FollowSource, LogFollower, LogFollowerParts,
};
use crate::hot_standby::{HotStandbyConfig, StandbyPhase, StandbyStatusCell};
use crate::recovery::{
    ClusterMetadata, CommittedCommandSink, EngineStartError, LocalStoreRequirements,
    OpenedLocalStore, delete_older_set_aside_stores, find_unreplayable_gap, open_local_store,
    rebuild_search_order_indexes,
};

/// First wait before a failed bootstrap retries; it doubles up to the maximum.
const BOOTSTRAP_RETRY_INITIAL: Duration = Duration::from_secs(1);
const BOOTSTRAP_RETRY_MAX: Duration = Duration::from_secs(30);

/// Longest wait for the standby's applier lanes to stop.
const APPLIER_STOP_BUDGET: Duration = Duration::from_secs(3);

/// Keys of one listing page, which bound [`crate::HotStandbyLag::batches`].
const LAG_LISTING_KEYS: u32 = 1_000;

/// Bootstraps the standby, follows the log, and starts over when log cleanup outruns it.
pub(super) struct StandbyDriver {
    pub(super) config: EngineNodeConfig,
    pub(super) standby_config: HotStandbyConfig,
    pub(super) metrics: Arc<dexzd_metrics::NodeMetrics>,
    /// Read-only: every write through it fails.
    pub(super) object_store: Arc<dyn ObjectStore>,
    pub(super) clock: Arc<dyn WallClock>,
    pub(super) applier: Arc<dyn CommandApplier>,
    pub(super) id_source: Arc<dyn EngineIdSource>,
    pub(super) status: Arc<StandbyStatusCell>,
    pub(super) cancellation: CancellationToken,
}

/// A bootstrapped standby and what it follows with.
struct FollowingStandby {
    metadata: ClusterMetadata,
    cluster_id: [u8; 16],
    store: Arc<EngineLocalStore>,
    write_fence: Arc<CommitState>,
    appliers: Arc<PartitionApplierPool>,
    follower: LogFollower,
}

/// How one period of following ended.
enum FollowingOutcome {
    Restart,
    Failed(EngineStartError),
    Cancelled,
}

impl StandbyDriver {
    /// Runs until cancelled or until an error it cannot recover from.
    pub(super) async fn run(self) {
        let mut retry_delay = BOOTSTRAP_RETRY_INITIAL;
        loop {
            self.status.set(StandbyPhase::Bootstrapping);
            let bootstrapped = tokio::select! {
                biased;
                () = self.cancellation.cancelled() => return,
                bootstrapped = self.bootstrap() => bootstrapped,
            };
            let following = match bootstrapped {
                Ok(Some(following)) => following,
                Ok(None) => {
                    tracing::info!("the hot standby waits for the cluster manifest");
                    if !self.wait_to_retry(&mut retry_delay).await {
                        return;
                    }
                    continue;
                }
                Err(error) if is_unrecoverable(&error) => {
                    self.fail(error);
                    return;
                }
                Err(error) => {
                    tracing::warn!(%error, "the hot standby could not bootstrap; retrying");
                    if !self.wait_to_retry(&mut retry_delay).await {
                        return;
                    }
                    continue;
                }
            };
            retry_delay = BOOTSTRAP_RETRY_INITIAL;
            match self.follow(following).await {
                FollowingOutcome::Restart => {}
                FollowingOutcome::Failed(error) => {
                    self.fail(error);
                    return;
                }
                FollowingOutcome::Cancelled => return,
            }
        }
    }

    /// Opens or restores the local store and starts following after it.
    ///
    /// Returns `None` until the cluster manifest exists.
    async fn bootstrap(&self) -> Result<Option<FollowingStandby>, EngineStartError> {
        let config = &self.config;
        let Some(metadata) = ClusterMetadata::read_existing(
            self.object_store.as_ref(),
            self.clock.as_ref(),
            config.cluster,
            &config.node,
        )
        .await?
        else {
            return Ok(None);
        };
        let partition_count = metadata.partition_count();
        let cluster_id = metadata.cluster_id()?;
        let OpenedLocalStore {
            store,
            set_aside_id,
        } = open_local_store(
            &config.local_store,
            self.object_store.as_ref(),
            &LocalStoreRequirements {
                snapshot: metadata.node_snapshot(&config.node.node_id),
                streams: &metadata.streams,
                partition_count,
                new_store_lineage_id: self.id_source.store_lineage_id(),
            },
        )
        .await?;
        let rebuilt_search_order_partitions = rebuild_search_order_indexes(&store)?;
        if rebuilt_search_order_partitions > 0 {
            tracing::info!(
                partition_count = rebuilt_search_order_partitions,
                "rebuilt search order indexes"
            );
        }
        delete_older_set_aside_stores(
            &config.local_store,
            config.recovery.retained_set_aside_store_count,
            set_aside_id,
            config.blocking_work,
        )
        .await;
        // The standby holds no lease, so its own deadline stays disarmed.
        let write_fence = Arc::new(CommitState::new(
            partition_count,
            LeaseValidityDeadline::disarmed(),
        ));
        let appliers = Arc::new(PartitionApplierPool::start(
            FrameApplier::new(FrameApplierParts {
                metrics: self.metrics.clone(),
                store: store.clone(),
                applier: self.applier.clone(),
                hasher: metadata.cluster.partition_hasher.clone(),
                state: write_fence.clone(),
                committed_commands: Arc::new(IgnoredCommittedCommands),
                is_checking_replay: config.commit.debug_replay_checks,
                blocking_work: config.blocking_work,
            })?,
            config.commit.applier_lanes,
            config.commit.applier_queue_frames,
        ));
        let started = LogFollower::start(LogFollowerParts {
            object_store: self.object_store.clone(),
            store: store.clone(),
            write_fence: write_fence.clone(),
            appliers: appliers.clone(),
            cluster_id,
            source: FollowSource {
                streams: metadata.streams.clone(),
            },
            pacing: FollowPacing {
                initial_poll_backoff: self.standby_config.initial_poll_backoff,
                max_poll_backoff: self.standby_config.max_poll_backoff,
                stalled_seq_deletion_check_after: self
                    .standby_config
                    .stalled_seq_deletion_check_after,
                read_ahead: config.recovery.log_read_ahead,
            },
        });
        let follower = match started {
            Ok(follower) => follower,
            Err(error) => {
                stop_appliers(&write_fence, &appliers).await;
                return Err(error);
            }
        };
        tracing::info!(node_id = %config.node.node_id, "the hot standby follows the log");
        Ok(Some(FollowingStandby {
            metadata,
            cluster_id,
            store,
            write_fence,
            appliers,
            follower,
        }))
    }

    /// Follows until log cleanup outruns the standby, following fails, or cancellation.
    async fn follow(&self, mut following: FollowingStandby) -> FollowingOutcome {
        self.status.set(StandbyPhase::Following {
            progress: following.follower.progress(),
            started_at_micros: self.clock.now_unix_micros(),
            listed_batches: 0,
        });
        let mut next_refresh = Instant::now() + self.standby_config.manifest_refresh_interval;
        let outcome = loop {
            tokio::select! {
                biased;
                () = self.cancellation.cancelled() => break FollowingOutcome::Cancelled,
                end = following.follower.wait_for_end() => break match end {
                    FollowEnd::LogDeleted => {
                        tracing::warn!(
                            "log cleanup deleted batches the hot standby needs; restoring the \
                             newest snapshot"
                        );
                        FollowingOutcome::Restart
                    }
                    FollowEnd::Failed(error) => FollowingOutcome::Failed(error),
                    FollowEnd::Stopped => FollowingOutcome::Failed(EngineStartError::Stopped {
                        reason: NodeStopReason::Shutdown,
                    }),
                },
                () = tokio::time::sleep_until(next_refresh) => {
                    next_refresh = Instant::now() + self.standby_config.manifest_refresh_interval;
                    if let Some(outcome) = self.refresh(&mut following).await {
                        break outcome;
                    }
                }
            }
        };
        let FollowingStandby {
            store,
            write_fence,
            appliers,
            follower,
            ..
        } = following;
        let followed_store = follower.stop().await;
        stop_appliers(&write_fence, &appliers).await;
        // Closing the store before a restart lets the next bootstrap reopen its directory.
        drop((appliers, followed_store, store));
        outcome
    }

    /// Rereads the manifest, checks the store against it, and passes it to the follower.
    ///
    /// Returns how following ends when the manifest no longer retains what the store needs.
    async fn refresh(&self, following: &mut FollowingStandby) -> Option<FollowingOutcome> {
        let current = match following
            .metadata
            .read_current_manifest(self.object_store.as_ref(), &self.config.node)
            .await
        {
            Ok(current) => current,
            Err(error) if is_unrecoverable(&error) => return Some(FollowingOutcome::Failed(error)),
            Err(error) => {
                tracing::warn!(%error, "the hot standby could not reread the manifest");
                return None;
            }
        };
        following.follower.update_source(FollowSource {
            streams: current.streams.clone(),
        });
        // The gap check reads stored positions, so idle partitions must catch up with the reads.
        let advanced = tokio::time::timeout(
            self.standby_config.manifest_refresh_interval,
            following.follower.advance_applied_positions(),
        )
        .await;
        if matches!(advanced, Ok(Ok(()))) {
            match find_unreplayable_gap(
                following.store.as_ref(),
                &current.streams,
                current.partition_count(),
            ) {
                Ok(None) => {}
                Ok(Some(gap)) => {
                    tracing::warn!(%gap, "the retained log cannot update the hot standby's store");
                    return Some(FollowingOutcome::Restart);
                }
                Err(error) => return Some(FollowingOutcome::Failed(error)),
            }
        } else {
            tracing::debug!("applied positions did not advance; checking the store next time");
        }
        following.metadata = current;
        self.list_unapplied_batches(following).await;
        None
    }

    /// Records how many batches the stream holds after the follower's next seq.
    async fn list_unapplied_batches(&self, following: &FollowingStandby) {
        let progress = *following.follower.progress().borrow();
        let stream = StreamIdentity {
            cluster_id: following.cluster_id,
            node_id: self.config.node.node_id.clone(),
            incarnation: progress.incarnation,
        };
        let last_read_key = stream.batch_key(progress.next_seq.saturating_sub(1));
        let prefix = &last_read_key[..=last_read_key
            .rfind('/')
            .expect("a batch key ends with /<seq>")];
        match self
            .object_store
            .list(prefix, Some(&last_read_key), LAG_LISTING_KEYS)
            .await
        {
            Ok(listing) => self.status.set_listed_batches(listing.objects.len() as u64),
            Err(error) => {
                tracing::warn!(%error, "the hot standby could not list its log stream for lag");
            }
        }
    }

    fn fail(&self, error: EngineStartError) {
        tracing::error!(%error, "the hot standby stopped following");
        self.status.set(StandbyPhase::Failed(Arc::new(error)));
    }

    /// Waits `retry_delay`, then doubles it up to the maximum.
    ///
    /// Returns `false` when cancellation arrives first.
    async fn wait_to_retry(&self, retry_delay: &mut Duration) -> bool {
        let delay = *retry_delay;
        *retry_delay = (delay * 2).min(BOOTSTRAP_RETRY_MAX);
        tokio::select! {
            biased;
            () = self.cancellation.cancelled() => false,
            () = tokio::time::sleep(delay) => true,
        }
    }
}

/// Stops writes through `write_fence`, then waits for the applier lanes.
async fn stop_appliers(write_fence: &CommitState, appliers: &PartitionApplierPool) {
    write_fence.stop(NodeStopReason::Shutdown);
    if tokio::time::timeout(APPLIER_STOP_BUDGET, appliers.shutdown())
        .await
        .is_err()
    {
        tracing::error!("the hot standby's applier lanes did not stop in time");
    }
}

/// Returns whether retrying cannot fix `error`, such as a configuration that contradicts the
/// cluster.
fn is_unrecoverable(error: &EngineStartError) -> bool {
    match error {
        EngineStartError::NodeIdentityMismatch { .. }
        | EngineStartError::IncarnationRegressed { .. }
        | EngineStartError::UnsupportedBehaviorVersion { .. }
        | EngineStartError::LocalStoreUnavailable { .. }
        | EngineStartError::InvalidConfig { .. }
        | EngineStartError::Stopped { .. } => true,
        EngineStartError::Metadata(error) => matches!(
            error,
            MetadataError::ClusterMismatch { .. }
                | MetadataError::InvalidNodeIdentifier(_)
                | MetadataError::InvalidConfig { .. }
                | MetadataError::UnsupportedVersionBarrier { .. }
        ),
        _ => false,
    }
}

/// Drops committed commands: a standby runs no effects, and the serving start rebuilds them.
struct IgnoredCommittedCommands;

impl CommittedCommandSink for IgnoredCommittedCommands {
    fn apply_committed(&self, _committed: CommittedFlowCommand) {}

    fn finish_frame(&self, _partition: PartitionId, _position: &LogPosition) {}
}
