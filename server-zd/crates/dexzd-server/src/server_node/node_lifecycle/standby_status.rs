// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hot standby status as metrics and the `dex.HotStandby` health name, and the takeover delay.

use std::fmt;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_api::HealthStatus;
use dexzd_cluster_metadata::LeaseAcquisitionDelay;
use dexzd_engine::{HotStandbyState, HotStandbyStatus, HotStandbyStatusReader};
use dexzd_metrics::{HotStandbyMetricState, NodeMetrics};
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

/// The reader is pull-only, so the status is sampled this often.
const STATUS_PUBLISH_INTERVAL: Duration = Duration::from_millis(500);
/// Keeps several hot standbys from writing an available lease in step.
const HOT_TAKEOVER_JITTER_LIMIT: Duration = Duration::from_millis(50);
/// A standby that is not hot waits its configured delay times a factor in this range.
const LAGGING_TAKEOVER_JITTER: std::ops::RangeInclusive<f64> = 0.8..=1.2;

/// Samples hot standby status into metrics and `dex.HotStandby` health until [`Self::stop`], which every exit path calls.
pub(super) struct StandbyStatusPublisher {
    stop: CancellationToken,
    task: JoinHandle<()>,
    metrics: Arc<NodeMetrics>,
    health: HealthStatus,
}

impl StandbyStatusPublisher {
    pub(super) fn start(
        reader: HotStandbyStatusReader,
        metrics: Arc<NodeMetrics>,
        health: HealthStatus,
    ) -> Self {
        let stop = CancellationToken::new();
        let task = tokio::spawn(publish_until_stopped(
            reader,
            metrics.clone(),
            health.clone(),
            stop.clone(),
        ));
        Self {
            stop,
            task,
            metrics,
            health,
        }
    }

    /// Stops sampling, drops the reader, and reports that this process is no hot standby.
    pub(super) async fn stop(self) {
        self.stop.cancel();
        if let Err(error) = self.task.await {
            tracing::error!(%error, "the hot standby status publisher failed");
        }
        publish_inactive(&self.metrics, &self.health).await;
    }
}

async fn publish_until_stopped(
    reader: HotStandbyStatusReader,
    metrics: Arc<NodeMetrics>,
    health: HealthStatus,
    stop: CancellationToken,
) {
    let mut logged_error: Option<String> = None;
    loop {
        let status = reader.status();
        publish(&status, &metrics, &health).await;
        if status.last_error != logged_error {
            if let Some(error) = &status.last_error {
                tracing::warn!(%error, state = ?status.state, "the hot standby restarts after an error");
            }
            logged_error.clone_from(&status.last_error);
        }
        if let Some(failure) = &status.failure {
            tracing::error!(error = %failure, "the hot standby stopped after an error it does not retry");
        }
        tokio::select! {
            () = stop.cancelled() => return,
            () = tokio::time::sleep(STATUS_PUBLISH_INTERVAL) => {}
        }
    }
}

async fn publish(status: &HotStandbyStatus, metrics: &NodeMetrics, health: &HealthStatus) {
    metrics.hot_standby_states.set(metric_state(status.state));
    let lag = status.lag.as_ref();
    metrics
        .hot_standby_lag_batches
        .set(lag.map_or(0, |lag| i64::try_from(lag.batches).unwrap_or(i64::MAX)));
    metrics
        .hot_standby_lag_seconds
        .set(lag.map_or(0.0, |lag| lag.duration.as_secs_f64()));
    metrics.hot_standby_error.set(i64::from(
        status.last_error.is_some() || status.failure.is_some(),
    ));
    health.set_hot_standby(is_hot(status)).await;
}

/// Reports that this process is no hot standby, such as while it serves.
pub(super) async fn publish_inactive(metrics: &NodeMetrics, health: &HealthStatus) {
    metrics
        .hot_standby_states
        .set(HotStandbyMetricState::Inactive);
    metrics.hot_standby_lag_batches.set(0);
    metrics.hot_standby_lag_seconds.set(0.0);
    metrics.hot_standby_error.set(0);
    health.set_hot_standby(false).await;
}

fn is_hot(status: &HotStandbyStatus) -> bool {
    status.state == HotStandbyState::Following
}

fn metric_state(state: HotStandbyState) -> HotStandbyMetricState {
    match state {
        HotStandbyState::Bootstrapping => HotStandbyMetricState::Bootstrapping,
        HotStandbyState::Following => HotStandbyMetricState::Following,
        HotStandbyState::Behind => HotStandbyMetricState::Behind,
        HotStandbyState::Restarting => HotStandbyMetricState::Restarting,
        HotStandbyState::Failed => HotStandbyMetricState::Failed,
        HotStandbyState::Stopped => HotStandbyMetricState::Inactive,
        // A state added later is not hot; it reads as restarting until this mapping learns it.
        _ => HotStandbyMetricState::Restarting,
    }
}

/// Lets a hot standby write an available lease first; a lagging one waits `lagging_delay`.
pub(super) struct StandbyAcquisitionDelay {
    reader: HotStandbyStatusReader,
    lagging_delay: Duration,
    first_asked_at: Mutex<Option<Instant>>,
}

impl StandbyAcquisitionDelay {
    pub(super) fn new(reader: HotStandbyStatusReader, lagging_delay: Duration) -> Self {
        Self {
            reader,
            lagging_delay,
            first_asked_at: Mutex::new(None),
        }
    }

    /// Returns when the lease first became available to this takeover.
    pub(super) fn first_asked_at(&self) -> Option<Instant> {
        *self
            .first_asked_at
            .lock()
            .expect("the delay lock is poisoned only if a holder panicked")
    }
}

impl LeaseAcquisitionDelay for StandbyAcquisitionDelay {
    fn delay_before_acquiring(&self) -> Duration {
        self.first_asked_at
            .lock()
            .expect("the delay lock is poisoned only if a holder panicked")
            .get_or_insert_with(Instant::now);
        if is_hot(&self.reader.status()) {
            rand::random_range(Duration::ZERO..=HOT_TAKEOVER_JITTER_LIMIT)
        } else {
            self.lagging_delay
                .mul_f64(rand::random_range(LAGGING_TAKEOVER_JITTER))
        }
    }
}

impl fmt::Debug for StandbyAcquisitionDelay {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("StandbyAcquisitionDelay")
            .field("lagging_delay", &self.lagging_delay)
            .finish_non_exhaustive()
    }
}
