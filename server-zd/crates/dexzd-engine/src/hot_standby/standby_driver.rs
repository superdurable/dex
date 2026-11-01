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
use xxhash_rust::xxh3::xxh3_64_with_seed;

use crate::command_applier::CommandApplier;
use crate::commit::{
    CommitState, EngineLocalStore, FrameApplier, FrameApplierParts, NodeStopReason,
    PartitionApplierPool,
};
use crate::engine_id_source::EngineIdSource;
use crate::engine_node::EngineNodeConfig;
use crate::flow_coordination::CommittedFlowCommand;
use crate::hot_standby::log_follower::{
    FOLLOWER_STOP_BUDGET, FollowEnd, FollowPacing, FollowProgress, FollowSource, LogFollower,
    LogFollowerParts,
};
use crate::hot_standby::{FollowingHandles, HotStandbyConfig, StandbyPhase, StandbyStatusCell};
use crate::recovery::{
    ClusterMetadata, CommittedCommandSink, EngineStartError, LocalStoreRequirements,
    OpenedLocalStore, delete_older_set_aside_stores, find_unreplayable_gap, open_local_store,
    rebuild_search_order_indexes,
};

/// First wait before the standby restarts after an error; it doubles up to the maximum.
const RESTART_DELAY_INITIAL: Duration = Duration::from_secs(1);
const RESTART_DELAY_MAX: Duration = Duration::from_secs(60);

/// Longest wait for the last handle of a stopped store, so its directory can be reopened.
const STORE_RELEASE_BUDGET: Duration = Duration::from_secs(5);

/// Longest wait for the standby's applier lanes to stop.
const APPLIER_STOP_BUDGET: Duration = Duration::from_secs(3);

/// Longest a driver takes to stop after cancellation: its teardown budgets plus a second of slack.
pub(super) const DRIVER_STOP_BUDGET: Duration = FOLLOWER_STOP_BUDGET
    .saturating_add(APPLIER_STOP_BUDGET)
    .saturating_add(STORE_RELEASE_BUDGET)
    .saturating_add(Duration::from_secs(1));

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
    /// Spreads restart delays of different processes apart.
    pub(super) restart_jitter_seed: u64,
    /// Receives the commands the standby applies; it runs no effects.
    pub(super) committed_commands: Arc<dyn CommittedCommandSink>,
    /// The write fence and store of the current following period, for the handle to reach.
    pub(super) following: Arc<FollowingHandles>,
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
    /// Bootstrap again: log cleanup outran the store, or an error that a restart may fix.
    Restart { error: Option<String> },
    /// A configuration error that no restart fixes.
    Failed(EngineStartError),
    /// Carries the open store when its write fence stopped only for this cancellation.
    Cancelled {
        store: Option<Arc<EngineLocalStore>>,
    },
}

/// A period of following and the furthest log position it read to.
struct FollowedPeriod {
    outcome: FollowingOutcome,
    reached: FollowPosition,
    /// Whether it read past every position that earlier periods reached, or reached the log end
    /// there.
    is_progressing: bool,
}

/// An incarnation and the next seq to read in its stream, ordered by the log.
type FollowPosition = (u32, u64);

/// Exponential restart delays with jitter that the process's seed spreads apart.
struct RestartDelays {
    jitter_seed: u64,
    restarts: u32,
}

impl StandbyDriver {
    /// Runs until cancelled or until a configuration error, and reports `Stopped` after a
    /// cancellation.
    ///
    /// Returns the open store when cancellation stopped following with a clean write fence.
    pub(super) async fn run(self) -> Option<Arc<EngineLocalStore>> {
        let store = self.run_until_stopped().await;
        if self.cancellation.is_cancelled() {
            self.status.set(StandbyPhase::Stopped);
        }
        store
    }

    /// Returns the open store when cancellation stopped following with a clean write fence.
    async fn run_until_stopped(&self) -> Option<Arc<EngineLocalStore>> {
        let mut delays = RestartDelays {
            jitter_seed: self.restart_jitter_seed,
            restarts: 0,
        };
        let mut furthest: Option<FollowPosition> = None;
        loop {
            self.status.set(StandbyPhase::Bootstrapping);
            let bootstrapped = tokio::select! {
                biased;
                () = self.cancellation.cancelled() => return None,
                bootstrapped = self.bootstrap() => bootstrapped,
            };
            let following = match bootstrapped {
                Ok(Some(following)) => following,
                Ok(None) => {
                    tracing::info!("the hot standby waits for the cluster object and manifest");
                    // Polls without backoff, so a standby of a new cluster follows it promptly.
                    if !self.sleep_unless_cancelled(RESTART_DELAY_INITIAL).await {
                        return None;
                    }
                    continue;
                }
                Err(error) if is_configuration_error(&error) => {
                    self.fail(error);
                    return None;
                }
                Err(error) => {
                    tracing::warn!(%error, "the hot standby could not bootstrap; retrying");
                    self.status
                        .set_last_error(Some(describe_bootstrap_error(&error)));
                    if !self.wait_to_restart(&mut delays).await {
                        return None;
                    }
                    continue;
                }
            };
            let period = self.follow(following, furthest).await;
            if period.is_progressing {
                delays.restarts = 0;
            }
            furthest =
                Some(furthest.map_or(period.reached, |furthest| furthest.max(period.reached)));
            match period.outcome {
                FollowingOutcome::Restart { error } => {
                    match error {
                        Some(error) => {
                            tracing::warn!(error, "the hot standby restarts after an error");
                            self.status.set_last_error(Some(error));
                        }
                        None => tracing::info!("the hot standby restarts from a newer snapshot"),
                    }
                    // A standby that log cleanup keeps outrunning backs off between snapshots too.
                    if !self.wait_to_restart(&mut delays).await {
                        return None;
                    }
                }
                FollowingOutcome::Failed(error) => {
                    self.fail(error);
                    return None;
                }
                FollowingOutcome::Cancelled { store } => return store,
            }
        }
    }

    /// Opens or restores the local store and starts following after it.
    ///
    /// Returns `None` until the cluster manifest exists.
    async fn bootstrap(&self) -> Result<Option<FollowingStandby>, EngineStartError> {
        let config = &self.config;
        let read_at = Instant::now();
        let Some(metadata) = ClusterMetadata::read_existing(
            self.object_store.as_ref(),
            config.cluster,
            &config.node,
        )
        .await?
        else {
            return Ok(None);
        };
        let partition_count = metadata.partition_count();
        let cluster_id = metadata.cluster_id()?;
        self.following.set_cluster_id(cluster_id);
        // The gap check sets aside a store that applied a batch past a closed stream's end.
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
        self.following.set_store(Arc::downgrade(&store));
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
        let frame_applier = FrameApplier::new(FrameApplierParts {
            metrics: self.metrics.clone(),
            store: store.clone(),
            applier: self.applier.clone(),
            hasher: metadata.cluster.partition_hasher.clone(),
            state: write_fence.clone(),
            committed_commands: Arc::clone(&self.committed_commands),
            is_checking_replay: config.commit.debug_replay_checks,
            blocking_work: config.blocking_work,
        })?;
        // Recorded before any lane exists, so dropping the handle fences every lane.
        self.following.set_write_fence(Some(write_fence.clone()));
        let appliers = Arc::new(PartitionApplierPool::start(
            frame_applier,
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
                read_at,
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
                self.following.set_write_fence(None);
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
    ///
    /// Clears the last error once following reads past `furthest`, where earlier periods stopped.
    async fn follow(
        &self,
        mut following: FollowingStandby,
        furthest: Option<FollowPosition>,
    ) -> FollowedPeriod {
        let progress = following.follower.progress();
        let started_at = position_of(&progress.borrow());
        let progress_threshold = furthest.map_or(started_at, |furthest| furthest.max(started_at));
        self.status.set(StandbyPhase::Following {
            progress: progress.clone(),
            started_at_micros: self.clock.now_unix_micros(),
            listing: None,
        });
        let refresh_interval = self.standby_config.manifest_refresh_interval;
        let mut next_refresh = Instant::now() + refresh_interval;
        let mut is_progressing = false;
        let outcome = loop {
            tokio::select! {
                biased;
                () = self.cancellation.cancelled() => {
                    break FollowingOutcome::Cancelled { store: None };
                }
                end = following.follower.wait_for_end() => break outcome_of_follow_end(end),
                () = tokio::time::sleep_until(next_refresh) => {
                    next_refresh = Instant::now() + refresh_interval;
                    // Headroom past the advance's own timeout leaves time for the reads around it.
                    let refreshed = tokio::select! {
                        biased;
                        () = self.cancellation.cancelled() => {
                            break FollowingOutcome::Cancelled { store: None };
                        }
                        refreshed = tokio::time::timeout(
                            refresh_interval.saturating_mul(2),
                            self.refresh(&mut following),
                        ) => refreshed,
                    };
                    match refreshed {
                        Ok(Some(outcome)) => break outcome,
                        Ok(None) => {}
                        Err(_) => tracing::warn!("rereading the manifest took too long; retrying"),
                    }
                    if !is_progressing && self.has_progressed(&progress.borrow(), progress_threshold) {
                        is_progressing = true;
                        self.status.set_last_error(None);
                    }
                }
            }
        };
        if !matches!(outcome, FollowingOutcome::Cancelled { .. }) {
            self.status
                .set(StandbyPhase::Restarting { restart_at: None });
        }
        let FollowingStandby {
            store,
            write_fence,
            appliers,
            follower,
            ..
        } = following;
        let followed_store = follower.stop().await;
        stop_appliers(&write_fence, &appliers).await;
        let is_cleanly_fenced = write_fence.stop_reason() == Some(NodeStopReason::Shutdown);
        self.following.set_write_fence(None);
        drop((appliers, followed_store, write_fence));
        let outcome = match outcome {
            FollowingOutcome::Cancelled { .. } if is_cleanly_fenced => {
                FollowingOutcome::Cancelled { store: Some(store) }
            }
            // A failed store write leaves the store untrusted, so a takeover opens it cold.
            FollowingOutcome::Cancelled { .. } => FollowingOutcome::Cancelled { store: None },
            // The next bootstrap reopens the store's directory, which its last handle locks.
            outcome @ (FollowingOutcome::Restart { .. } | FollowingOutcome::Failed(_)) => {
                release_store(store).await;
                outcome
            }
        };
        let reached = position_of(&progress.borrow());
        FollowedPeriod {
            outcome,
            reached,
            is_progressing: is_progressing || reached > progress_threshold,
        }
    }

    /// Returns whether following read past `threshold`, or waits at the log end there with no
    /// later batch listed, so not at a hole.
    fn has_progressed(&self, progress: &FollowProgress, threshold: FollowPosition) -> bool {
        let position = position_of(progress);
        position > threshold
            || (position == threshold
                && progress.is_caught_up
                && self.status.listing() == Some((progress.next_seq, 0)))
    }

    /// Rereads the manifest, checks the store against it, and passes it to the follower.
    ///
    /// Returns how following ends when the manifest no longer retains what the store needs.
    async fn refresh(&self, following: &mut FollowingStandby) -> Option<FollowingOutcome> {
        let read_at = Instant::now();
        let current = match following
            .metadata
            .read_current_manifest(self.object_store.as_ref(), &self.config.node)
            .await
        {
            Ok(current) => current,
            Err(error) if is_configuration_error(&error) => {
                return Some(FollowingOutcome::Failed(error));
            }
            Err(error) => {
                tracing::warn!(%error, "the hot standby could not reread the manifest");
                return None;
            }
        };
        following.follower.update_source(FollowSource {
            streams: current.streams.clone(),
            read_at,
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
                    return Some(FollowingOutcome::Restart { error: None });
                }
                Err(error) => {
                    return Some(FollowingOutcome::Restart {
                        error: Some(error.to_string()),
                    });
                }
            }
        } else {
            tracing::debug!("applied positions did not advance; checking the store next time");
        }
        following.metadata = current;
        self.list_unapplied_batches(following).await;
        None
    }

    /// Records how many batches the stream holds from the follower's next seq on.
    async fn list_unapplied_batches(&self, following: &FollowingStandby) {
        let progress = following.follower.progress().borrow().clone();
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
            Ok(listing) => self
                .status
                .set_listing(progress.next_seq, listing.objects.len() as u64),
            Err(error) => {
                tracing::warn!(%error, "the hot standby could not list its log stream for lag");
            }
        }
    }

    fn fail(&self, error: EngineStartError) {
        tracing::error!(%error, "the hot standby stopped following");
        self.status.set(StandbyPhase::Failed(Arc::new(error)));
    }

    /// Reports [`StandbyPhase::Restarting`] and waits for the next restart delay.
    ///
    /// Returns `false` when cancellation arrives first.
    async fn wait_to_restart(&self, delays: &mut RestartDelays) -> bool {
        let delay = delays.next_delay();
        self.status.set(StandbyPhase::Restarting {
            restart_at: Some(Instant::now() + delay),
        });
        self.sleep_unless_cancelled(delay).await
    }

    /// Returns `false` when cancellation arrives before `delay` passes.
    async fn sleep_unless_cancelled(&self, delay: Duration) -> bool {
        tokio::select! {
            biased;
            () = self.cancellation.cancelled() => false,
            () = tokio::time::sleep(delay) => true,
        }
    }
}

impl RestartDelays {
    /// Returns the delay before the next restart: 1 second doubling to 60, within plus or minus
    /// 20 percent.
    fn next_delay(&mut self) -> Duration {
        let base = RESTART_DELAY_INITIAL
            .saturating_mul(1_u32 << self.restarts.min(6))
            .min(RESTART_DELAY_MAX);
        let jitter_permille = 800
            + u32::try_from(
                xxh3_64_with_seed(&self.restarts.to_le_bytes(), self.jitter_seed) % 401,
            )
            .expect("a value below 401 fits u32");
        self.restarts = self.restarts.saturating_add(1);
        base * jitter_permille / 1000
    }
}

/// Returns the follow position of `progress`.
fn position_of(progress: &FollowProgress) -> FollowPosition {
    (progress.incarnation, progress.next_seq)
}

/// Maps how the follower ended to how following ends.
fn outcome_of_follow_end(end: FollowEnd) -> FollowingOutcome {
    match end {
        FollowEnd::LogDeleted => {
            tracing::warn!(
                "log cleanup deleted batches the hot standby needs; restoring the newest snapshot"
            );
            FollowingOutcome::Restart { error: None }
        }
        FollowEnd::StoreDiverged(error) => {
            tracing::error!(%error, "the hot standby's store diverged from the log; restoring the newest snapshot");
            // The next bootstrap finds the position past the recorded end and restores the store.
            FollowingOutcome::Restart {
                error: Some(error.to_string()),
            }
        }
        FollowEnd::Failed(error) if is_configuration_error(&error) => {
            FollowingOutcome::Failed(error)
        }
        FollowEnd::Failed(error) => FollowingOutcome::Restart {
            error: Some(error.to_string()),
        },
        FollowEnd::TaskFailed { detail } => FollowingOutcome::Restart {
            error: Some(detail),
        },
        FollowEnd::Stopped => FollowingOutcome::Restart {
            error: Some("the log follower stopped unexpectedly".to_owned()),
        },
    }
}

/// Waits up to [`STORE_RELEASE_BUDGET`] until `store` holds the last handle, then drops it.
async fn release_store(store: Arc<EngineLocalStore>) {
    let deadline = Instant::now() + STORE_RELEASE_BUDGET;
    while Arc::strong_count(&store) > 1 && Instant::now() < deadline {
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    if Arc::strong_count(&store) > 1 {
        tracing::warn!("another task still holds the hot standby's local store");
    }
}

/// Stops writes through `write_fence`, then waits for the applier lanes.
///
/// A fence that its owner stopped already ends following quickly: a lane that outlives it can
/// no longer write, and the store's last handle closes it.
async fn stop_appliers(write_fence: &CommitState, appliers: &PartitionApplierPool) {
    if write_fence.stop_reason().is_some() {
        return;
    }
    write_fence.stop(NodeStopReason::Shutdown);
    if tokio::time::timeout(APPLIER_STOP_BUDGET, appliers.shutdown())
        .await
        .is_err()
    {
        tracing::error!("the hot standby's applier lanes did not stop in time");
    }
}

/// Returns how the status reports a bootstrap `error`; a locked store directory says who may hold it.
fn describe_bootstrap_error(error: &EngineStartError) -> String {
    match error {
        EngineStartError::LocalStoreUnavailable { directory, source } => format!(
            "the local store directory {} is unavailable, as when another process or a lingering \
             handle of this one holds its lock; retrying: {source}",
            directory.display()
        ),
        error => error.to_string(),
    }
}

/// Returns whether `error` is a configuration error that no restart fixes.
fn is_configuration_error(error: &EngineStartError) -> bool {
    match error {
        EngineStartError::NodeIdentityMismatch { .. }
        | EngineStartError::IncarnationRegressed { .. }
        | EngineStartError::UnsupportedBehaviorVersion { .. }
        | EngineStartError::InvalidConfig { .. } => true,
        EngineStartError::Metadata(error) => matches!(
            error,
            MetadataError::ClusterMismatch { .. }
                | MetadataError::InvalidNodeIdentifier(_)
                | MetadataError::InvalidConfig { .. }
        ),
        _ => false,
    }
}

/// Drops committed commands: a standby runs no effects, and the serving start rebuilds them.
pub(super) struct IgnoredCommittedCommands;

impl CommittedCommandSink for IgnoredCommittedCommands {
    fn apply_committed(&self, _committed: CommittedFlowCommand) {}

    fn finish_frame(&self, _partition: PartitionId, _position: &LogPosition) {}
}
