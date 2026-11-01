// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The engine node: startup and recovery, serving, and shutdown.

mod node_start;

use std::future::Future;
use std::sync::Arc;
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_cluster_metadata::{
    ClusterConfig, LeaseConfig, LeaseHandle, LeaseReleaseError, LeaseStatus, LeaseValidityDeadline,
    NodeConfig,
};
use dexzd_keyspace::PartitionId;
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dexzd::v1::SnapshotReference;
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::blocking_work::BlockingWorkPlacement;
use crate::command_applier::CommandApplier;
use crate::commit::{
    AppliedPositionAdvancer, CommandCommitter, CommitConfig, CommitState, NodeStopReason,
    PartitionApplierPool,
};
use crate::engine_id_source::EngineIdSource;
use crate::engine_node::node_start::{LocalStoreSource, StartOutcome, start_engine_node};
use crate::hot_standby::HotStandby;
use crate::recovery::{
    EngineCommandAndEffectHooks, EngineServices, EngineStartError, LocalStoreConfig,
};
use crate::snapshots::{SnapshotConfig, SnapshotError, SnapshotService};

/// Longest wait for engine tasks and applier lanes to stop before the lease is released anyway.
/// It includes [`FINAL_ADVANCE_BUDGET`].
const TASK_STOP_BUDGET: Duration = Duration::from_secs(3);

/// Longest wait at shutdown for advancing applied positions, so the next start replays little.
const FINAL_ADVANCE_BUDGET: Duration = Duration::from_secs(1);

/// Configuration sections of an engine node.
#[derive(Debug, Clone)]
pub struct EngineNodeConfig {
    /// This node's identity in the manifest and its log stream.
    /// No default; the caller supplies this section.
    pub node: NodeConfig,
    /// Immutable cluster settings, checked against the cluster object.
    /// No default; the caller supplies this section.
    pub cluster: ClusterConfig,
    /// Node lease timing.
    /// No default; the caller supplies this section.
    pub lease: LeaseConfig,
    /// Local state store.
    /// No default; the caller supplies this section.
    pub local_store: LocalStoreConfig,
    /// Commit path.
    /// No default; the caller supplies this section.
    pub commit: CommitConfig,
    /// Snapshots and log cleanup.
    /// No default; the caller supplies this section.
    pub snapshots: SnapshotConfig,
    /// Log replay read-ahead and compaction after a bulk replay.
    /// No default; the caller supplies this section.
    pub recovery: crate::recovery::RecoveryConfig,
    /// Where frame applies, applied-position writes, and snapshot uploads run.
    /// [`BlockingWorkPlacement::BlockingThreadPool`] in production.
    /// No default; the caller chooses the execution placement.
    pub blocking_work: BlockingWorkPlacement,
}

/// Shared services the node runs on.
pub struct EngineNodeDependencies {
    /// Process-local metric handles for this node.
    pub metrics: Arc<dexzd_metrics::NodeMetrics>,
    /// Object storage that holds the cluster metadata, logs, and snapshots.
    pub object_store: Arc<dyn ObjectStore>,
    /// Wall clock for command timestamps and lease expiry.
    pub clock: Arc<dyn WallClock>,
    /// Applies committed commands.
    pub applier: Arc<dyn CommandApplier>,
    /// Creates the command, lineage, and EpochStart writer IDs.
    pub id_source: Arc<dyn EngineIdSource>,
    /// Armed with the lease's local validity; commits are refused and unacknowledged after it passes.
    pub lease_validity_deadline: LeaseValidityDeadline,
}

/// How a takeover from a hot standby ended, apart from errors after the lease.
#[non_exhaustive]
pub enum HotStandbyTakeover {
    /// This process holds the node lease and serves.
    Serving(EngineNode),
    /// Cancellation arrived before the lease; the standby still follows the log.
    Cancelled(HotStandby),
    /// The start failed before the lease; the standby still follows the log.
    FailedBeforeLease {
        /// Why the start failed.
        error: EngineStartError,
        /// The standby, which keeps following.
        standby: HotStandby,
    },
}

/// A node serving its log stream. After a fatal failure it stops serving; then exit non-zero.
pub struct EngineNode {
    metrics: Arc<dexzd_metrics::NodeMetrics>,
    committer: CommandCommitter,
    state: Arc<CommitState>,
    appliers: Arc<PartitionApplierPool>,
    applied_positions: Arc<AppliedPositionAdvancer>,
    snapshots: Arc<SnapshotService>,
    lease: LeaseHandle,
    tasks: RunningTasks,
    cluster_id: [u8; 16],
    /// Whether the node serves from the store a hot standby handed over.
    #[cfg(feature = "test-util")]
    is_serving_hot_standby_store: bool,
}

impl EngineNode {
    /// Longest [`EngineNode::shutdown`] runs: the task stop budget plus the lease release budget.
    pub const SHUTDOWN_BUDGET: Duration =
        TASK_STOP_BUDGET.saturating_add(LeaseHandle::RELEASE_BUDGET);

    /// Restores state, replays the log, starts serving, and rebuilds every running partition's runtime.
    pub async fn start<F>(
        config: &EngineNodeConfig,
        dependencies: EngineNodeDependencies,
        command_and_effect_hooks_factory: F,
    ) -> Result<Self, EngineStartError>
    where
        F: FnOnce(EngineServices) -> EngineCommandAndEffectHooks,
    {
        let (_cancellation_sender, mut cancellation) = watch::channel(false);
        let started = Self::start_until_cancelled(
            config,
            dependencies,
            command_and_effect_hooks_factory,
            &mut cancellation,
        )
        .await?;
        Ok(started.expect("startup without cancellation must return an engine node"))
    }

    /// Starts the node like [`EngineNode::start`], and stops early when cancellation arrives.
    ///
    /// Returns the serving node, or `None` when cancellation arrives before the node holds its
    /// lease, including while it waits for the lease. Returns [`EngineStartError::Stopped`] with
    /// [`NodeStopReason::Shutdown`] when cancellation arrives after acquisition, once the lease is
    /// released. Returns the start error when startup fails for another reason. Returns
    /// [`EngineStartError::FailedStartLeaseRelease`] when the lease release after a cancelled or
    /// failed start fails.
    pub async fn start_until_cancelled<F>(
        config: &EngineNodeConfig,
        dependencies: EngineNodeDependencies,
        command_and_effect_hooks_factory: F,
        cancellation: &mut watch::Receiver<bool>,
    ) -> Result<Option<Self>, EngineStartError>
    where
        F: FnOnce(EngineServices) -> EngineCommandAndEffectHooks,
    {
        let started = start_engine_node(
            config,
            dependencies,
            command_and_effect_hooks_factory,
            cancellation,
            LocalStoreSource::Configured,
        )
        .await?;
        Ok(match started {
            StartOutcome::Serving(node) => Some(node),
            StartOutcome::Cancelled(_) => None,
            StartOutcome::FailedBeforeLease { error, .. } => return Err(error),
        })
    }

    /// Takes the lease and serves from a hot standby's store when the log, rechecked under the
    /// lease, updates it.
    ///
    /// Returns [`HotStandbyTakeover::Cancelled`] or [`HotStandbyTakeover::FailedBeforeLease`]
    /// with the standby, still following, when cancellation or an error arrives before the
    /// lease. Otherwise returns like
    /// [`EngineNode::start_until_cancelled`]; a stale or missing standby store makes the start
    /// open or restore the configured store instead.
    pub async fn take_over_from_hot_standby_until_cancelled<F>(
        config: &EngineNodeConfig,
        dependencies: EngineNodeDependencies,
        command_and_effect_hooks_factory: F,
        standby: HotStandby,
        cancellation: &mut watch::Receiver<bool>,
    ) -> Result<HotStandbyTakeover, EngineStartError>
    where
        F: FnOnce(EngineServices) -> EngineCommandAndEffectHooks,
    {
        let started = start_engine_node(
            config,
            dependencies,
            command_and_effect_hooks_factory,
            cancellation,
            LocalStoreSource::HotStandby(standby),
        )
        .await?;
        Ok(match started {
            StartOutcome::Serving(node) => HotStandbyTakeover::Serving(node),
            StartOutcome::Cancelled(source) => HotStandbyTakeover::Cancelled(
                source
                    .into_hot_standby()
                    .expect("a cancelled takeover returns the hot standby it was given"),
            ),
            StartOutcome::FailedBeforeLease { error, source } => {
                HotStandbyTakeover::FailedBeforeLease {
                    error,
                    standby: source
                        .into_hot_standby()
                        .expect("a takeover that failed before the lease returns its standby"),
                }
            }
        })
    }

    /// Returns the committer that Flow runtimes submit commands through.
    pub fn committer(&self) -> &CommandCommitter {
        &self.committer
    }

    /// Returns the ID of the cluster object, which never changes after the cluster is created.
    pub fn cluster_id(&self) -> [u8; 16] {
        self.cluster_id
    }

    /// Returns whether the node serves from the store that a hot standby handed over.
    #[cfg(feature = "test-util")]
    pub fn is_serving_hot_standby_store_for_test(&self) -> bool {
        self.is_serving_hot_standby_store
    }

    /// Returns the node epoch this process raised when it took the lease.
    pub fn node_epoch(&self) -> u64 {
        self.lease.epoch()
    }

    /// Returns the validity of this node's lease, for work that must end before the lease does.
    pub fn lease_status(&self) -> watch::Receiver<LeaseStatus> {
        self.lease.subscribe()
    }

    /// Waits until the node stops serving and returns why.
    pub fn wait_for_stop(&self) -> impl Future<Output = NodeStopReason> + Send + 'static {
        let state = self.state.clone();
        async move { state.wait_stopped().await }
    }

    /// Returns why the node stopped, or `None` while it serves.
    pub fn stop_reason(&self) -> Option<NodeStopReason> {
        self.state.stop_reason()
    }

    /// Returns partitions that stopped applying commands after an apply failure.
    pub fn halted_partitions(&self) -> Vec<PartitionId> {
        self.state.halted_partitions()
    }

    /// Returns verified and snapshot-only time-travel target counts, in that order.
    pub fn time_travel_verification_counts(&self) -> (u64, u64) {
        self.appliers.time_travel_verification_counts()
    }

    /// Takes a snapshot now, records it in the manifest, and deletes covered objects.
    pub async fn take_snapshot(&self) -> Result<SnapshotReference, SnapshotError> {
        self.snapshots.take_snapshot().await
    }

    /// Stops serving, fails waiting tickets, stops every task, and releases the lease.
    ///
    /// It first records the last routed batch as every running partition's position, for at most
    /// 1 second.
    ///
    /// Tasks and applier lanes that do not stop within 3 seconds are abandoned, so the lease
    /// release is always attempted. The 3 seconds include that first second. Finishes within
    /// [`EngineNode::SHUTDOWN_BUDGET`].
    ///
    /// Returns `Ok` once this process no longer holds the lease, and [`LeaseReleaseError`] when the
    /// release was rejected or timed out; the lease then stays held until it expires.
    pub async fn shutdown(self) -> Result<(), LeaseReleaseError> {
        self.metrics.set_role(dexzd_metrics::NodeRole::Draining);
        let metrics = self.metrics.clone();
        let Self {
            state,
            appliers,
            applied_positions,
            lease,
            tasks,
            ..
        } = self;
        let released = LeasedParts {
            state,
            tasks,
            appliers: Some(appliers),
            standby: None,
            lease,
        }
        .stop_and_release_lease(Some(&applied_positions))
        .await;
        if released.is_ok() {
            metrics.lease_epoch.set(0);
        }
        tracing::info!("engine node stopped");
        released
    }
}

/// Everything that runs under the node lease, stopped before the lease is released.
struct LeasedParts {
    state: Arc<CommitState>,
    tasks: RunningTasks,
    appliers: Option<Arc<PartitionApplierPool>>,
    /// A takeover's standby until its store is handed over; it stops before the lease release.
    standby: Option<HotStandby>,
    lease: LeaseHandle,
}

impl LeasedParts {
    /// Stops serving, gives tasks and applier lanes [`TASK_STOP_BUDGET`] to stop, then releases the
    /// lease.
    ///
    /// A serving node passes `final_advance` to advance applied positions before it stops.
    ///
    /// Returns the outcome of the lease release.
    async fn stop_and_release_lease(
        self,
        final_advance: Option<&AppliedPositionAdvancer>,
    ) -> Result<(), LeaseReleaseError> {
        let Self {
            state,
            tasks,
            appliers,
            standby,
            lease,
        } = self;
        let stop_deadline = tokio::time::Instant::now() + TASK_STOP_BUDGET;
        if let Some(advancer) = final_advance
            && state.stop_reason().is_none()
        {
            // The stop blocks every later store write, so the advance must finish first.
            match tokio::time::timeout(
                FINAL_ADVANCE_BUDGET,
                advancer.advance_to_last_queued_batch(),
            )
            .await
            {
                Ok(Ok(())) => {}
                Ok(Err(error)) => {
                    tracing::warn!(%error, "applied positions were not advanced before shutdown");
                }
                Err(_) => tracing::warn!(
                    budget_seconds = FINAL_ADVANCE_BUDGET.as_secs(),
                    "applied positions were not advanced in time; the next start replays more of \
                     the log"
                ),
            }
        }
        state.stop(NodeStopReason::Shutdown);
        let stopping = async {
            tasks.shutdown().await;
            if let Some(appliers) = &appliers {
                appliers.shutdown().await;
            }
        };
        if tokio::time::timeout_at(stop_deadline, stopping)
            .await
            .is_err()
        {
            // Dropping the tasks aborts them; a lane blocked in apply ends with the process.
            tracing::error!(
                budget_seconds = TASK_STOP_BUDGET.as_secs(),
                "engine tasks did not stop in time; releasing the lease anyway"
            );
        }
        // A successor may reopen the store's directory once it holds the lease.
        if let Some(standby) = standby {
            standby.shutdown().await;
        }
        lease.shutdown().await
    }
}

pub(crate) fn validate_config(config: &EngineNodeConfig) -> Result<(), EngineStartError> {
    let commit = &config.commit;
    let snapshots = &config.snapshots;
    let problem = if commit.applier_lanes == 0 {
        Some("commit.applier_lanes must be positive")
    } else if commit.applier_queue_frames == 0 {
        Some("commit.applier_queue_frames must be positive")
    } else if commit.max_unapplied_bytes == 0 {
        Some("commit.max_unapplied_bytes must be positive")
    } else if snapshots.retained_count == 0 {
        Some("snapshots.retained_count must be positive")
    } else if snapshots.check_interval.is_zero() {
        Some("snapshots.check_interval must be positive")
    } else if config.recovery.log_read_ahead.max_read_ahead_batches == 0 {
        Some("recovery.log_read_ahead.max_read_ahead_batches must be positive")
    } else if config.recovery.log_read_ahead.max_buffered_bytes == 0 {
        Some("recovery.log_read_ahead.max_buffered_bytes must be positive")
    } else if config.recovery.startup_read_retry_budget.is_zero() {
        Some("recovery.startup_read_retry_budget must be positive")
    } else {
        None
    };
    match problem {
        Some(detail) => Err(EngineStartError::InvalidConfig {
            detail: detail.to_owned(),
        }),
        None => Ok(()),
    }
}

/// Background tasks of a node; dropping it cancels and aborts every task.
#[derive(Default)]
struct RunningTasks {
    cancellation: CancellationToken,
    handles: Vec<JoinHandle<()>>,
}

impl RunningTasks {
    fn spawn(&mut self, task: impl Future<Output = ()> + Send + 'static) {
        let handle = tokio::spawn(task);
        self.handles.push(handle);
    }

    async fn shutdown(mut self) {
        self.cancellation.cancel();
        for handle in std::mem::take(&mut self.handles) {
            if let Err(error) = handle.await
                && !error.is_cancelled()
            {
                tracing::error!(error = %error, "engine task failed during shutdown");
            }
        }
    }
}

impl Drop for RunningTasks {
    fn drop(&mut self) {
        self.cancellation.cancel();
        for handle in &self.handles {
            handle.abort();
        }
    }
}
