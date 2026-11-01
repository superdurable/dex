// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Checks that lease epochs only grow, that each is raised once, and how fast holders hand over.

use std::collections::BTreeSet;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::robustness::inspection::ObjectStoreInspector;

const LEASE_POLL_INTERVAL: Duration = Duration::from_millis(100);
/// The server's wait after an unreleased lease expires before another process takes it.
const TAKEOVER_GRACE: Duration = Duration::from_secs(1);
/// The expiry that a released lease records.
const RELEASED_LEASE_EXPIRY_UNIX_MICROS: i64 = i64::MIN;

/// One read of the node lease object.
#[derive(Clone, Debug)]
struct LeaseObservation {
    observed_at_unix_micros: i64,
    holder_process_id: String,
    epoch: u64,
    expires_at_unix_micros: i64,
}

/// Counts from a passing lease check.
#[derive(Debug)]
pub(crate) struct LeaseReport {
    /// Distinct holders the lease object named.
    pub(crate) observed_holders: usize,
    /// EpochStart batches in the log, one for each process that recovered under the lease.
    pub(crate) acquisitions: usize,
    /// The longest time a new holder took to appear after the previous lease became available.
    pub(crate) longest_takeover_after_availability: Option<Duration>,
}

/// Reads the node lease object in the background through an endpoint without faults.
pub(crate) struct LeaseHistory {
    stop: CancellationToken,
    task: Option<JoinHandle<(Vec<LeaseObservation>, ObjectStoreInspector)>>,
}

impl LeaseHistory {
    pub(crate) fn start(inspector: ObjectStoreInspector) -> Self {
        let stop = CancellationToken::new();
        let task = tokio::spawn(read_leases(inspector, stop.clone()));
        Self {
            stop,
            task: Some(task),
        }
    }

    /// Stops reading, then panics if an epoch fell or the log raised one twice.
    pub(crate) async fn stop_and_check(mut self) -> LeaseReport {
        self.stop.cancel();
        let (observations, inspector) = self
            .task
            .take()
            .expect("the lease reader runs until this check")
            .await
            .expect("the lease reader does not panic");
        let (observed_holders, takeovers) = check_lease_observations(&observations);
        // The log is the complete record: every recovery writes an EpochStart with its new epoch.
        let epoch_starts: Vec<u64> = inspector
            .log_batch_headers()
            .await
            .iter()
            .filter_map(|header| header.epoch_start.as_ref().map(|start| start.epoch))
            .collect();
        check_epoch_starts(&epoch_starts);
        LeaseReport {
            observed_holders,
            acquisitions: epoch_starts.len(),
            longest_takeover_after_availability: takeovers.into_iter().max(),
        }
    }
}

impl Drop for LeaseHistory {
    fn drop(&mut self) {
        self.stop.cancel();
        if let Some(task) = &self.task {
            task.abort();
        }
    }
}

async fn read_leases(
    inspector: ObjectStoreInspector,
    stop: CancellationToken,
) -> (Vec<LeaseObservation>, ObjectStoreInspector) {
    let mut observations = Vec::new();
    loop {
        if let Some(lease) = inspector.node_lease_if_present().await {
            observations.push(LeaseObservation {
                observed_at_unix_micros: unix_micros_now(),
                holder_process_id: lease.holder_process_id,
                epoch: lease.epoch,
                expires_at_unix_micros: lease.expires_at_unix_micros,
            });
        }
        tokio::select! {
            () = stop.cancelled() => return (observations, inspector),
            () = tokio::time::sleep(LEASE_POLL_INTERVAL) => {}
        }
    }
}

/// Panics if the epoch fell; returns the distinct holders and each handover's delay after availability.
fn check_lease_observations(observations: &[LeaseObservation]) -> (usize, Vec<Duration>) {
    let mut holders = BTreeSet::new();
    let mut takeovers = Vec::new();
    // A new holder first writes the previous epoch, so one epoch can name two holders.
    for pair in observations.windows(2) {
        let (previous, next) = (&pair[0], &pair[1]);
        assert!(
            next.epoch >= previous.epoch,
            "the lease epoch fell from {previous:?} to {next:?}"
        );
        if next.holder_process_id != previous.holder_process_id {
            let available_at =
                if previous.expires_at_unix_micros == RELEASED_LEASE_EXPIRY_UNIX_MICROS {
                    previous.observed_at_unix_micros
                } else {
                    previous.expires_at_unix_micros + micros(TAKEOVER_GRACE)
                };
            let delay = next.observed_at_unix_micros.saturating_sub(available_at);
            takeovers.push(Duration::from_micros(u64::try_from(delay).unwrap_or(0)));
        }
    }
    holders.extend(
        observations
            .iter()
            .map(|observation| observation.holder_process_id.as_str()),
    );
    (holders.len(), takeovers)
}

/// Panics unless the EpochStart epochs, in log order, strictly increase.
fn check_epoch_starts(epoch_starts: &[u64]) {
    assert!(
        epoch_starts.windows(2).all(|pair| pair[0] < pair[1]),
        "every recovery must raise a new epoch, but the log has EpochStarts {epoch_starts:?}"
    );
}

fn unix_micros_now() -> i64 {
    let elapsed = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("the wall clock is after the Unix epoch");
    i64::try_from(elapsed.as_micros()).expect("the wall clock fits in i64 microseconds")
}

fn micros(duration: Duration) -> i64 {
    i64::try_from(duration.as_micros()).expect("test durations fit in i64 microseconds")
}

#[cfg(test)]
fn observation(
    observed_at_millis: i64,
    holder: &str,
    epoch: u64,
    expires_at_millis: i64,
) -> LeaseObservation {
    LeaseObservation {
        observed_at_unix_micros: observed_at_millis * 1000,
        holder_process_id: holder.to_owned(),
        epoch,
        expires_at_unix_micros: if expires_at_millis == i64::MIN {
            RELEASED_LEASE_EXPIRY_UNIX_MICROS
        } else {
            expires_at_millis * 1000
        },
    }
}

/// An expiry handover and a release handover report their delays after availability.
#[test]
fn handovers_report_their_delay_after_the_lease_became_available() {
    let (holders, takeovers) = check_lease_observations(&[
        observation(0, "first", 1, 3_000),
        observation(4_500, "second", 1, 19_500),
        observation(4_600, "second", 2, 19_600),
        observation(5_000, "second", 2, i64::MIN),
        observation(5_200, "third", 2, 20_200),
    ]);
    assert_eq!(holders, 3);
    assert_eq!(
        takeovers,
        vec![Duration::from_millis(500), Duration::from_millis(200)]
    );
}

/// A falling lease epoch fails the check.
#[test]
#[should_panic(expected = "the lease epoch fell")]
fn a_falling_lease_epoch_fails_the_check() {
    check_lease_observations(&[
        observation(0, "first", 2, 3_000),
        observation(100, "second", 1, 3_100),
    ]);
}

/// An epoch that two recoveries raised fails the check.
#[test]
#[should_panic(expected = "every recovery must raise a new epoch")]
fn a_repeated_epoch_start_fails_the_check() {
    check_epoch_starts(&[1, 2, 2]);
}
