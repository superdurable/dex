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
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_object_store::ObjectStore;
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::command_applier::CommandApplier;
use crate::engine_id_source::EngineIdSource;
use crate::engine_node::{EngineNodeConfig, validate_config};
use crate::hot_standby::log_follower::FollowProgress;
use crate::hot_standby::read_only_object_store::ReadOnlyObjectStore;
use crate::hot_standby::standby_driver::StandbyDriver;
use crate::recovery::EngineStartError;

/// Longest wait in [`HotStandby::shutdown`] for following to stop.
const SHUTDOWN_BUDGET: Duration = Duration::from_secs(10);

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
}

/// Follows the node's log from object storage without the node lease, so a later takeover
/// replays little.
pub struct HotStandby {
    status: Arc<StandbyStatusCell>,
    /// Writes the read-only object store rejected; always zero unless the standby has a bug.
    #[cfg(feature = "test-util")]
    rejected_writes: Arc<AtomicU64>,
    clock: Arc<dyn WallClock>,
    behind_lag_threshold: Duration,
    cancellation: CancellationToken,
    driver: Option<JoinHandle<()>>,
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
                cancellation: cancellation.clone(),
            }
            .run(),
        );
        Ok(Self {
            status,
            #[cfg(feature = "test-util")]
            rejected_writes: observed_rejected_writes,
            clock,
            behind_lag_threshold: standby_config.behind_lag_threshold,
            cancellation,
            driver: Some(driver),
        })
    }

    /// Returns how many writes the standby attempted and its read-only object store rejected.
    #[cfg(feature = "test-util")]
    pub fn rejected_writes_for_test(&self) -> u64 {
        self.rejected_writes.load(Ordering::SeqCst)
    }

    /// Returns the standby's state and lag now.
    pub fn status(&self) -> HotStandbyStatus {
        self.status
            .status_at(self.clock.now_unix_micros(), self.behind_lag_threshold)
    }

    /// Stops following and waits up to 10 seconds for the local store to close.
    pub async fn shutdown(mut self) {
        self.cancellation.cancel();
        let Some(driver) = self.driver.take() else {
            return;
        };
        let abort = driver.abort_handle();
        match tokio::time::timeout(SHUTDOWN_BUDGET, driver).await {
            Ok(Ok(())) => {}
            Ok(Err(error)) if error.is_cancelled() => {}
            Ok(Err(error)) => tracing::error!(%error, "the hot standby task failed"),
            Err(_) => {
                abort.abort();
                tracing::error!("the hot standby did not stop in time and was aborted");
            }
        }
    }
}

impl Drop for HotStandby {
    fn drop(&mut self) {
        self.cancellation.cancel();
        if let Some(driver) = self.driver.take() {
            driver.abort();
        }
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
}

impl StandbyStatusCell {
    fn set(&self, phase: StandbyPhase) {
        *self
            .phase
            .lock()
            .expect("standby status lock is not poisoned") = phase;
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
            },
            StandbyPhase::Restarting { restart_at } => HotStandbyStatus {
                state: HotStandbyState::Restarting,
                lag: None,
                failure: None,
                last_error,
                restart_in: restart_at.map(|restart_at| {
                    restart_at.saturating_duration_since(tokio::time::Instant::now())
                }),
            },
            StandbyPhase::Failed(error) => HotStandbyStatus {
                state: HotStandbyState::Failed,
                lag: None,
                failure: Some(error.clone()),
                last_error,
                restart_in: None,
            },
            StandbyPhase::Following {
                progress,
                started_at_micros,
                listing,
            } => {
                let progress = *progress.borrow();
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
