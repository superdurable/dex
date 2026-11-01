// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hot standby: a process of the node that keeps its local store current from object storage.

mod log_follower;
mod read_only_object_store;
mod standby_driver;

use std::sync::atomic::AtomicU64;
#[cfg(feature = "test-util")]
use std::sync::atomic::Ordering;
use std::sync::{Arc, Mutex, Weak};
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_object_store::ObjectStore;
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::command_applier::CommandApplier;
use crate::commit::{CommitState, EngineLocalStore, NodeStopReason};
use crate::engine_id_source::EngineIdSource;
use crate::engine_node::{EngineNodeConfig, LocalStoreCloseWaiter, validate_config};
use crate::hot_standby::log_follower::FollowProgress;
use crate::hot_standby::read_only_object_store::ReadOnlyObjectStore;
use crate::hot_standby::standby_driver::{
    DRIVER_STOP_BUDGET, IgnoredCommittedCommands, StandbyDriver,
};
use crate::recovery::{CommittedCommandSink, EngineStartError, LocalStoreConfig};

/// Longest wait in [`HotStandby::shutdown`] for the local store to close once following stopped.
const SHUTDOWN_STORE_CLOSE_BUDGET: Duration = Duration::from_secs(5);

/// Failed log reads in a row after which [`HotStandbyStatus::read_error`] reports the last one.
const REPORTED_FAILED_READS: u32 = 3;

/// Pacing of a hot standby's log following.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HotStandbyConfig {
    /// Defaults to 50 milliseconds; must be positive. The first wait after a read finds no next
    /// batch. The wait doubles while no batch arrives.
    pub initial_poll_backoff: Duration,
    /// Defaults to 1 second; must be at least `initial_poll_backoff`. The longest wait between
    /// reads while no batch arrives.
    pub max_poll_backoff: Duration,
    /// Defaults to 5 seconds; must be positive. How often the standby rereads the manifest to
    /// learn closed log segments, new incarnations, and log cleanup.
    pub manifest_refresh_interval: Duration,
    /// Defaults to 20 seconds; must exceed `manifest_refresh_interval`. How long the standby waits
    /// at one seq before it checks whether log cleanup deleted that seq. A deleted seq makes the
    /// standby restore the newest snapshot.
    pub stalled_seq_deletion_check_after: Duration,
    /// Defaults to 10 seconds; must be positive. Lag above which the standby reports
    /// [`HotStandbyState::Behind`] instead of [`HotStandbyState::Following`].
    pub behind_lag_threshold: Duration,
}

impl Default for HotStandbyConfig {
    fn default() -> Self {
        Self {
            initial_poll_backoff: Duration::from_millis(50),
            max_poll_backoff: Duration::from_secs(1),
            manifest_refresh_interval: Duration::from_secs(5),
            stalled_seq_deletion_check_after: Duration::from_secs(20),
            behind_lag_threshold: Duration::from_secs(10),
        }
    }
}

/// Shared services a hot standby runs on.
pub struct HotStandbyDependencies {
    /// Process-local metric handles.
    pub metrics: Arc<dexzd_metrics::NodeMetrics>,
    /// Object storage holding cluster metadata, logs, and snapshots; the standby only reads it.
    pub object_store: Arc<dyn ObjectStore>,
    /// Wall clock that lag is measured with.
    pub clock: Arc<dyn WallClock>,
    /// Applies followed commands; pass the applier the node serves with.
    pub applier: Arc<dyn CommandApplier>,
    /// Creates the lineage ID of a local store that the standby creates or restores.
    pub id_source: Arc<dyn EngineIdSource>,
    /// Spreads the restart delays of standbys apart.
    ///
    /// Pass a random value, such as one from `rand::random`. A test may pass a fixed value for
    /// reproducible delays.
    pub restart_jitter_seed: u64,
}

/// What a hot standby is doing.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum HotStandbyState {
    /// Reading the cluster metadata and opening the local store, or restoring the newest
    /// snapshot.
    Bootstrapping,
    /// Following the log with lag at most the configured threshold.
    Following,
    /// Following the log with lag above the configured threshold.
    Behind,
    /// Stopped following and waits to bootstrap again, after an error or after log cleanup outran
    /// its store; [`HotStandbyStatus::restart_in`] says how long.
    Restarting,
    /// Stopped after an error it cannot recover from; [`HotStandbyStatus::failure`] says why.
    Failed,
    /// Stopped following for good after a takeover's handover, a shutdown, or a drop.
    Stopped,
}

/// How far a hot standby trails the log it follows.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub struct HotStandbyLag {
    /// Incarnation of the log stream that the lagging follow group reads.
    pub incarnation: u32,
    /// Next seq that group reads in that stream.
    pub next_seq: u64,
    /// Batches from `next_seq` on in the stream listing of the last manifest refresh, at most 1000.
    pub batches: u64,
    /// Zero when caught up at the end of the log; otherwise how long the standby has trailed it.
    pub duration: Duration,
}

/// A snapshot of a hot standby's state and lag.
#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct HotStandbyStatus {
    /// What the standby is doing.
    pub state: HotStandbyState,
    /// Lag of the follow group furthest behind; `None` unless following.
    pub lag: Option<HotStandbyLag>,
    /// Why the standby stopped, in [`HotStandbyState::Failed`].
    pub failure: Option<Arc<EngineStartError>>,
    /// Why the standby last restarted; it retries every error except a configuration error.
    pub last_error: Option<String>,
    /// Time left before the next bootstrap, in [`HotStandbyState::Restarting`]; `None` while
    /// following still stops.
    pub restart_in: Option<Duration>,
    /// Why at least three consecutive log reads failed, as when storage is unreachable; `None`
    /// after a read succeeds.
    pub read_error: Option<String>,
}

/// Reads a hot standby's status, also while a takeover owns the standby.
///
/// Clone it from [`HotStandby::status_reader`] before a takeover, to keep reporting the standby's
/// state and lag while the takeover waits for the node lease.
#[derive(Clone)]
pub struct HotStandbyStatusReader {
    status: Arc<StandbyStatusCell>,
    clock: Arc<dyn WallClock>,
    behind_lag_threshold: Duration,
}

impl HotStandbyStatusReader {
    /// Returns the standby's state and lag now.
    pub fn status(&self) -> HotStandbyStatus {
        self.status
            .status_at(self.clock.now_unix_micros(), self.behind_lag_threshold)
    }
}

/// Follows the node's log from object storage without the node lease, so a later takeover
/// replays little.
pub struct HotStandby {
    status_reader: HotStandbyStatusReader,
    /// Writes the read-only object store rejected; always zero unless the standby has a bug.
    #[cfg(feature = "test-util")]
    rejected_writes: Arc<AtomicU64>,
    /// The store the standby follows into, checked against the node's at takeover.
    local_store: LocalStoreConfig,
    node_id: String,
    following: Arc<FollowingHandles>,
    cancellation: CancellationToken,
    /// Returns the open store when cancellation stopped following cleanly.
    driver: Option<JoinHandle<Option<Arc<EngineLocalStore>>>>,
}

/// The write fence and store of the current following period, which outlive a cancelled call.
#[derive(Default)]
struct FollowingHandles {
    write_fence: Mutex<Option<Arc<CommitState>>>,
    store: Arc<Mutex<Option<Weak<EngineLocalStore>>>>,
    /// The cluster the standby read at its last bootstrap.
    cluster_id: Mutex<Option<[u8; 16]>>,
    /// The newest write fence, kept after following stops so a test can read its refusals.
    #[cfg(feature = "test-util")]
    last_write_fence: Mutex<Option<Arc<CommitState>>>,
    #[cfg(feature = "test-util")]
    is_waiting_for_store_close: std::sync::atomic::AtomicBool,
    /// Following periods that read into the next incarnation's stream without a restart.
    #[cfg(feature = "test-util")]
    stream_crossings: AtomicU64,
}

impl HotStandby {
    /// Starts following in the background; call it inside a Tokio runtime.
    ///
    /// Returns [`EngineStartError::InvalidConfig`] for an invalid node or standby configuration.
    /// Later failures appear in [`Self::status`].
    pub fn start(
        config: EngineNodeConfig,
        standby_config: HotStandbyConfig,
        dependencies: HotStandbyDependencies,
    ) -> Result<Self, EngineStartError> {
        Self::start_with_committed_commands(
            config,
            standby_config,
            dependencies,
            Arc::new(IgnoredCommittedCommands),
        )
    }

    /// Starts following like [`Self::start`], passing every applied command to
    /// `committed_commands`.
    #[cfg(feature = "test-util")]
    pub fn start_with_committed_commands_for_test(
        config: EngineNodeConfig,
        standby_config: HotStandbyConfig,
        dependencies: HotStandbyDependencies,
        committed_commands: Arc<dyn CommittedCommandSink>,
    ) -> Result<Self, EngineStartError> {
        Self::start_with_committed_commands(
            config,
            standby_config,
            dependencies,
            committed_commands,
        )
    }

    fn start_with_committed_commands(
        config: EngineNodeConfig,
        standby_config: HotStandbyConfig,
        dependencies: HotStandbyDependencies,
        committed_commands: Arc<dyn CommittedCommandSink>,
    ) -> Result<Self, EngineStartError> {
        validate_config(&config)?;
        validate_standby_config(&standby_config)?;
        let HotStandbyDependencies {
            metrics,
            object_store,
            clock,
            applier,
            id_source,
            restart_jitter_seed,
        } = dependencies;
        let status = Arc::new(StandbyStatusCell::default());
        let rejected_writes = Arc::new(AtomicU64::new(0));
        #[cfg(feature = "test-util")]
        let observed_rejected_writes = Arc::clone(&rejected_writes);
        let local_store = config.local_store.clone();
        let node_id = config.node.node_id.clone();
        let following = Arc::new(FollowingHandles::default());
        let cancellation = CancellationToken::new();
        let driver = tokio::spawn(
            StandbyDriver {
                config,
                standby_config,
                metrics,
                object_store: Arc::new(ReadOnlyObjectStore::new(object_store, rejected_writes)),
                clock: clock.clone(),
                applier,
                id_source,
                status: status.clone(),
                restart_jitter_seed,
                committed_commands,
                following: following.clone(),
                cancellation: cancellation.clone(),
            }
            .run(),
        );
        Ok(Self {
            status_reader: HotStandbyStatusReader {
                status,
                clock,
                behind_lag_threshold: standby_config.behind_lag_threshold,
            },
            #[cfg(feature = "test-util")]
            rejected_writes: observed_rejected_writes,
            local_store,
            node_id,
            following,
            cancellation,
            driver: Some(driver),
        })
    }

    /// Returns how many writes the standby attempted and its read-only object store rejected.
    #[cfg(feature = "test-util")]
    pub fn rejected_writes_for_test(&self) -> u64 {
        self.rejected_writes.load(Ordering::SeqCst)
    }

    /// Returns the store the standby follows into now, which a restore may have replaced.
    #[cfg(feature = "test-util")]
    pub fn local_store_for_test(&self) -> Option<Arc<EngineLocalStore>> {
        self.following
            .store
            .lock()
            .expect("store handle lock is not poisoned")
            .as_ref()
            .and_then(Weak::upgrade)
    }

    /// Returns a waiter of the standby's local store, which stays valid after a takeover or a
    /// shutdown; see [`LocalStoreCloseWaiter`].
    pub fn local_store_close_waiter(&self) -> LocalStoreCloseWaiter {
        LocalStoreCloseWaiter::new(Arc::clone(&self.following.store))
    }

    /// Returns a handle that observes and fails the standby, which works after a takeover owns
    /// it.
    #[cfg(feature = "test-util")]
    pub fn probe_for_test(&self) -> HotStandbyProbeForTest {
        HotStandbyProbeForTest {
            following: Arc::clone(&self.following),
            cancellation: self.cancellation.clone(),
        }
    }

    /// Returns the standby's state and lag now.
    pub fn status(&self) -> HotStandbyStatus {
        self.status_reader.status()
    }

    /// Returns a reader of [`Self::status`] that stays valid while a takeover owns the standby.
    pub fn status_reader(&self) -> HotStandbyStatusReader {
        self.status_reader.clone()
    }

    /// Stops following and waits for the local store to close, for at most 17 seconds.
    ///
    /// Returns whether the store closed, so another process may reopen its directory. A task
    /// that still applies a frame can hold it open past the wait.
    pub async fn shutdown(mut self) -> bool {
        drop(
            self.stop_following(tokio::time::Instant::now() + DRIVER_STOP_BUDGET)
                .await,
        );
        self.wait_until_store_closed(tokio::time::Instant::now() + SHUTDOWN_STORE_CLOSE_BUDGET)
            .await
    }

    /// Stops a standby whose takeover failed or was cancelled, by `deadline`, so the lease
    /// release waits little. Fencing first lets its driver skip frames it may no longer write.
    ///
    /// Returns whether the store closed by `deadline`.
    pub(crate) async fn stop_until(mut self, deadline: tokio::time::Instant) -> bool {
        self.following.stop_current_write_fence();
        drop(self.stop_following(deadline).await);
        self.wait_until_store_closed(deadline).await
    }

    /// Returns whether the standby follows into the store that `config` names.
    pub(crate) fn has_local_store(&self, config: &LocalStoreConfig) -> bool {
        self.local_store.is_same_store(config)
    }

    /// Returns whether the standby follows the log of `node_id`.
    pub(crate) fn is_following_node(&self, node_id: &str) -> bool {
        self.node_id == node_id
    }

    /// Returns whether the standby follows the cluster `cluster_id`, or has not read it yet.
    pub(crate) fn is_following_cluster(&self, cluster_id: [u8; 16]) -> bool {
        self.following
            .cluster_id
            .lock()
            .expect("cluster ID lock is not poisoned")
            .is_none_or(|followed| followed == cluster_id)
    }

    /// Stops reading, applies the queued frames, advances applied positions, fences the
    /// standby's store writes, and joins its tasks. Cancel safe: a later call or drop finishes it.
    ///
    /// Returns the open store when its write fence stopped only for this handover, and `None`
    /// otherwise, such as while the standby bootstrapped or after a store write failed.
    pub(crate) async fn hand_over_store(&mut self) -> Option<Arc<EngineLocalStore>> {
        self.stop_following(tokio::time::Instant::now() + DRIVER_STOP_BUDGET)
            .await
    }

    /// Waits until no handle keeps the last store open, so its directory reopens, or `deadline`.
    ///
    /// Returns whether the store closed; `true` when the standby never opened one.
    pub(crate) async fn wait_until_store_closed(&self, deadline: tokio::time::Instant) -> bool {
        #[cfg(feature = "test-util")]
        self.following
            .is_waiting_for_store_close
            .store(true, Ordering::SeqCst);
        let is_closed = self
            .local_store_close_waiter()
            .wait_until_closed_by(deadline)
            .await;
        #[cfg(feature = "test-util")]
        self.following
            .is_waiting_for_store_close
            .store(false, Ordering::SeqCst);
        if !is_closed {
            tracing::error!("a task still holds the hot standby's local store open");
        }
        is_closed
    }

    /// Cancels the driver and waits for it until `deadline`, then fences and aborts it. Cancel
    /// safe.
    ///
    /// Returns the driver's output: the open store after a clean stop.
    async fn stop_following(
        &mut self,
        deadline: tokio::time::Instant,
    ) -> Option<Arc<EngineLocalStore>> {
        self.cancellation.cancel();
        let driver = self.driver.as_mut()?;
        let stopped = tokio::time::timeout_at(deadline, driver).await;
        let driver = self.driver.take()?;
        match stopped {
            Ok(Ok(store)) => store,
            Ok(Err(error)) => {
                if !error.is_cancelled() {
                    tracing::error!(%error, "the hot standby task failed");
                }
                None
            }
            Err(_) => {
                self.following.stop_current_write_fence();
                driver.abort();
                // The aborted driver can no longer report its own stop.
                self.status_reader.status.set(StandbyPhase::Stopped);
                tracing::error!("the hot standby did not stop in time and was aborted");
                None
            }
        }
    }
}

impl Drop for HotStandby {
    fn drop(&mut self) {
        // Fencing first keeps a lane that outlives the abort from writing the store.
        self.following.stop_current_write_fence();
        if self.driver.is_some() {
            self.status_reader.status.set(StandbyPhase::Stopped);
        }
        self.cancellation.cancel();
        if let Some(driver) = self.driver.take() {
            driver.abort();
        }
    }
}

/// Observes a hot standby's stop and fails its store writes from a test.
#[cfg(feature = "test-util")]
pub struct HotStandbyProbeForTest {
    following: Arc<FollowingHandles>,
    cancellation: CancellationToken,
}

#[cfg(feature = "test-util")]
impl HotStandbyProbeForTest {
    /// Stops the current write fence as a failed store write would; does nothing between periods.
    pub fn fail_store_writes_for_test(&self) {
        if let Some(write_fence) = self.following.current_write_fence() {
            write_fence.stop(NodeStopReason::LocalStoreFailed {
                detail: "a test failed the hot standby's store writes".to_owned(),
            });
        }
    }

    /// Returns whether a handover, shutdown, or drop began to stop the standby.
    pub fn is_stopping_for_test(&self) -> bool {
        self.cancellation.is_cancelled()
    }

    /// Returns how many store writes the newest write fence refused after it stopped.
    pub fn refused_store_writes_for_test(&self) -> u64 {
        self.following
            .last_write_fence
            .lock()
            .expect("write fence lock is not poisoned")
            .as_ref()
            .map_or(0, |write_fence| write_fence.refused_store_writes_for_test())
    }

    /// Returns how many following periods read into the next incarnation's stream.
    pub fn stream_crossings_for_test(&self) -> u64 {
        self.following.stream_crossings.load(Ordering::SeqCst)
    }

    /// Returns whether a caller waits for a task to close the standby's store.
    pub fn is_waiting_for_store_close_for_test(&self) -> bool {
        self.following
            .is_waiting_for_store_close
            .load(Ordering::SeqCst)
    }
}

impl FollowingHandles {
    fn current_write_fence(&self) -> Option<Arc<CommitState>> {
        self.write_fence
            .lock()
            .expect("write fence lock is not poisoned")
            .clone()
    }

    /// Stops the current write fence, so no lane of the following period writes the store again.
    fn stop_current_write_fence(&self) {
        if let Some(write_fence) = self.current_write_fence() {
            write_fence.stop(NodeStopReason::Shutdown);
        }
    }

    fn set_write_fence(&self, write_fence: Option<Arc<CommitState>>) {
        #[cfg(feature = "test-util")]
        if let Some(write_fence) = &write_fence {
            *self
                .last_write_fence
                .lock()
                .expect("write fence lock is not poisoned") = Some(Arc::clone(write_fence));
        }
        *self
            .write_fence
            .lock()
            .expect("write fence lock is not poisoned") = write_fence;
    }

    fn set_cluster_id(&self, cluster_id: [u8; 16]) {
        *self
            .cluster_id
            .lock()
            .expect("cluster ID lock is not poisoned") = Some(cluster_id);
    }

    /// Counts a following period that read into the next incarnation's stream.
    fn record_stream_crossing(&self) {
        #[cfg(feature = "test-util")]
        self.stream_crossings.fetch_add(1, Ordering::SeqCst);
    }

    /// Records the newest store, which a later wait for a closed store watches.
    fn set_store(&self, store: Weak<EngineLocalStore>) {
        *self
            .store
            .lock()
            .expect("store handle lock is not poisoned") = Some(store);
    }
}

fn validate_standby_config(config: &HotStandbyConfig) -> Result<(), EngineStartError> {
    let problem = if config.initial_poll_backoff.is_zero() {
        Some("hot standby initial_poll_backoff must be positive")
    } else if config.max_poll_backoff < config.initial_poll_backoff {
        Some("hot standby max_poll_backoff must be at least initial_poll_backoff")
    } else if config.manifest_refresh_interval.is_zero() {
        Some("hot standby manifest_refresh_interval must be positive")
    } else if config.stalled_seq_deletion_check_after <= config.manifest_refresh_interval {
        Some("hot standby stalled_seq_deletion_check_after must exceed manifest_refresh_interval")
    } else if config.behind_lag_threshold.is_zero() {
        Some("hot standby behind_lag_threshold must be positive")
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

/// The standby's phase and last restart error, shared by its driver and [`HotStandby::status`].
#[derive(Default)]
struct StandbyStatusCell {
    phase: Mutex<StandbyPhase>,
    /// Why the standby last restarted; cleared once following passes where it stopped.
    last_error: Mutex<Option<String>>,
}

#[derive(Default)]
enum StandbyPhase {
    #[default]
    Bootstrapping,
    Following {
        progress: watch::Receiver<FollowProgress>,
        /// Wall-clock start of following, the lag base before the first accepted batch.
        started_at_micros: i64,
        /// Next seq at the last listing, and the batches it showed from that seq on.
        listing: Option<(u64, u64)>,
    },
    /// Following stopped; `restart_at` is set once the delay before the next bootstrap starts.
    Restarting {
        restart_at: Option<tokio::time::Instant>,
    },
    Failed(Arc<EngineStartError>),
    Stopped,
}

impl StandbyStatusCell {
    /// Sets the phase, unless the standby already stopped or failed for good.
    fn set(&self, phase: StandbyPhase) {
        let mut current = self
            .phase
            .lock()
            .expect("standby status lock is not poisoned");
        // An aborted driver can still run briefly, and a drop must keep the failure reason.
        if !matches!(*current, StandbyPhase::Stopped | StandbyPhase::Failed(_)) {
            *current = phase;
        }
    }

    fn set_listing(&self, next_seq: u64, batches: u64) {
        if let StandbyPhase::Following { listing, .. } = &mut *self
            .phase
            .lock()
            .expect("standby status lock is not poisoned")
        {
            *listing = Some((next_seq, batches));
        }
    }

    /// Returns the next seq at the last listing and the batches it showed from that seq on.
    fn listing(&self) -> Option<(u64, u64)> {
        match &*self
            .phase
            .lock()
            .expect("standby status lock is not poisoned")
        {
            StandbyPhase::Following { listing, .. } => *listing,
            StandbyPhase::Bootstrapping
            | StandbyPhase::Restarting { .. }
            | StandbyPhase::Failed(_)
            | StandbyPhase::Stopped => None,
        }
    }

    fn set_last_error(&self, error: Option<String>) {
        *self
            .last_error
            .lock()
            .expect("standby status lock is not poisoned") = error;
    }

    fn status_at(&self, now_micros: i64, behind_lag_threshold: Duration) -> HotStandbyStatus {
        let last_error = self
            .last_error
            .lock()
            .expect("standby status lock is not poisoned")
            .clone();
        match &*self
            .phase
            .lock()
            .expect("standby status lock is not poisoned")
        {
            StandbyPhase::Bootstrapping => HotStandbyStatus {
                state: HotStandbyState::Bootstrapping,
                lag: None,
                failure: None,
                last_error,
                restart_in: None,
                read_error: None,
            },
            StandbyPhase::Restarting { restart_at } => HotStandbyStatus {
                state: HotStandbyState::Restarting,
                lag: None,
                failure: None,
                last_error,
                restart_in: restart_at.map(|restart_at| {
                    restart_at.saturating_duration_since(tokio::time::Instant::now())
                }),
                read_error: None,
            },
            StandbyPhase::Stopped => HotStandbyStatus {
                state: HotStandbyState::Stopped,
                lag: None,
                failure: None,
                last_error,
                restart_in: None,
                read_error: None,
            },
            StandbyPhase::Failed(error) => HotStandbyStatus {
                state: HotStandbyState::Failed,
                lag: None,
                failure: Some(error.clone()),
                last_error,
                restart_in: None,
                read_error: None,
            },
            StandbyPhase::Following {
                progress,
                started_at_micros,
                listing,
            } => {
                let progress = progress.borrow().clone();
                let duration = lag_duration(&progress, *listing, *started_at_micros, now_micros);
                let state = if duration > behind_lag_threshold {
                    HotStandbyState::Behind
                } else {
                    HotStandbyState::Following
                };
                HotStandbyStatus {
                    state,
                    lag: Some(HotStandbyLag {
                        incarnation: progress.incarnation,
                        next_seq: progress.next_seq,
                        batches: listing.map_or(0, |(_, batches)| batches),
                        duration,
                    }),
                    failure: None,
                    last_error,
                    restart_in: None,
                    read_error: (progress.failed_reads >= REPORTED_FAILED_READS)
                        .then(|| progress.last_read_error.as_deref().map(str::to_owned))
                        .flatten(),
                }
            }
        }
    }
}

/// Returns how far a follower trails the log; see [`HotStandbyLag::duration`].
fn lag_duration(
    progress: &FollowProgress,
    listing: Option<(u64, u64)>,
    started_at_micros: i64,
    now_micros: i64,
) -> Duration {
    if progress.is_caught_up {
        let is_waiting_at_hole = listing.is_some_and(|(listed_next_seq, batches)| {
            listed_next_seq == progress.next_seq && batches > 0
        });
        return match progress.waiting_since {
            Some(waiting_since) if is_waiting_at_hole => waiting_since.elapsed(),
            _ => Duration::ZERO,
        };
    }
    let base_micros = progress
        .last_accepted_sealed_at_micros
        .unwrap_or(started_at_micros);
    // A seal time ahead of this clock means clock skew, not lag.
    let data_age =
        Duration::from_micros(u64::try_from(now_micros.saturating_sub(base_micros)).unwrap_or(0));
    progress.caught_up_at.map_or(data_age, |caught_up_at| {
        data_age.min(caught_up_at.elapsed())
    })
}

#[cfg(test)]
mod tests {
    use std::future::pending;

    use dexzd_cluster_metadata::LeaseValidityDeadline;

    use super::*;

    /// Returns a standby whose driver ignores cancellation, like one stuck in a long apply.
    fn standby_with_stuck_driver() -> HotStandby {
        let driver = tokio::spawn(pending::<Option<Arc<EngineLocalStore>>>());
        HotStandby {
            status_reader: HotStandbyStatusReader {
                status: Arc::new(StandbyStatusCell::default()),
                clock: Arc::new(dexzd_clock::SystemWallClock),
                behind_lag_threshold: Duration::from_secs(10),
            },
            #[cfg(feature = "test-util")]
            rejected_writes: Arc::new(AtomicU64::new(0)),
            local_store: LocalStoreConfig::InMemory,
            node_id: "node-a".to_owned(),
            following: Arc::new(FollowingHandles::default()),
            cancellation: CancellationToken::new(),
            driver: Some(driver),
        }
    }

    #[test]
    fn a_stopped_or_failed_status_ignores_later_phases() {
        let failed = StandbyStatusCell::default();
        failed.set(StandbyPhase::Failed(Arc::new(
            EngineStartError::InvalidConfig {
                detail: "a test failure".to_owned(),
            },
        )));
        failed.set(StandbyPhase::Stopped);
        let status = failed.status_at(0, Duration::from_secs(10));
        assert_eq!(status.state, HotStandbyState::Failed);
        assert!(status.failure.is_some(), "the failure reason is kept");

        let stopped = StandbyStatusCell::default();
        stopped.set(StandbyPhase::Stopped);
        stopped.set(StandbyPhase::Bootstrapping);
        stopped.set(StandbyPhase::Restarting { restart_at: None });
        assert_eq!(
            stopped.status_at(0, Duration::from_secs(10)).state,
            HotStandbyState::Stopped
        );
    }

    #[tokio::test(start_paused = true)]
    async fn a_cancelled_handover_keeps_the_driver_for_a_later_stop() {
        let mut standby = standby_with_stuck_driver();
        let handover =
            tokio::time::timeout(Duration::from_millis(1), standby.hand_over_store()).await;
        assert!(
            handover.is_err(),
            "the stuck driver keeps the handover waiting"
        );
        assert!(
            standby.driver.is_some(),
            "the cancelled handover detached the driver"
        );
    }

    #[tokio::test(start_paused = true)]
    async fn a_stop_that_times_out_fences_and_aborts_the_driver_and_reports_stopped() {
        let mut standby = standby_with_stuck_driver();
        let write_fence = Arc::new(CommitState::new(4, LeaseValidityDeadline::disarmed()));
        standby
            .following
            .set_write_fence(Some(Arc::clone(&write_fence)));
        let reader = standby.status_reader();
        let deadline = tokio::time::Instant::now() + Duration::from_millis(1);
        assert!(standby.stop_following(deadline).await.is_none());
        assert_eq!(write_fence.stop_reason(), Some(NodeStopReason::Shutdown));
        assert!(standby.driver.is_none(), "the stuck driver was aborted");
        assert_eq!(reader.status().state, HotStandbyState::Stopped);
    }
}
