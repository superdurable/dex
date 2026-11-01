// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Checks that lease epochs only grow, that each is raised once, and how fast holders hand over.

use std::collections::BTreeSet;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::robustness::inspection::ObjectStoreInspector;

const LEASE_POLL_INTERVAL: Duration = Duration::from_millis(100);
/// The server's wait after an unreleased lease expires before another process takes it.
const TAKEOVER_GRACE: Duration = Duration::from_secs(1);
/// The expiry that a released lease records.
const RELEASED_LEASE_EXPIRY_UNIX_MICROS: i64 = i64::MIN;
/// Clock reads on one host; a takeover earlier than this before availability is a split brain.
const EARLY_TAKEOVER_TOLERANCE: Duration = Duration::from_millis(50);
/// A process told to stop or step down releases its lease within this long.
const POSSIBLE_RELEASE_WINDOW: Duration = Duration::from_secs(20);

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
    /// Whether log cleanup was off, so the log still holds every EpochStart.
    pub(crate) is_log_complete: bool,
    /// The longest time a new holder took to appear after the previous lease became available.
    pub(crate) longest_takeover_after_availability: Option<Duration>,
}

/// Reads the node lease object in the background through an endpoint without faults.
pub(crate) struct LeaseHistory {
    stop: CancellationToken,
    task: Option<JoinHandle<(Vec<LeaseObservation>, ObjectStoreInspector)>>,
    /// Unix microseconds at which a process was told to stop or step down.
    possible_releases: Arc<Mutex<Vec<i64>>>,
}

impl LeaseHistory {
    pub(crate) fn start(inspector: ObjectStoreInspector) -> Self {
        let stop = CancellationToken::new();
        let task = tokio::spawn(read_leases(inspector, stop.clone()));
        Self {
            stop,
            task: Some(task),
            possible_releases: Arc::new(Mutex::new(Vec::new())),
        }
    }

    /// Records that a process may release the lease from now on, such as after SIGTERM.
    pub(crate) fn note_possible_release(&self) {
        self.possible_releases
            .lock()
            .expect("the release lock is not poisoned")
            .push(unix_micros_now());
    }

    /// Stops reading, then panics if an epoch fell or the log raised one twice.
    pub(crate) async fn stop_and_check(mut self, is_log_complete: bool) -> LeaseReport {
        self.stop.cancel();
        let (observations, inspector) = self
            .task
            .take()
            .expect("the lease reader runs until this check")
            .await
            .expect("the lease reader does not panic");
        let possible_releases = self
            .possible_releases
            .lock()
            .expect("the release lock is not poisoned")
            .clone();
        let (observed_holders, takeovers) =
            check_lease_observations(&observations, &possible_releases);
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
            is_log_complete,
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
        match inspector.try_node_lease().await {
            Ok(Some(lease)) => observations.push(LeaseObservation {
                observed_at_unix_micros: unix_micros_now(),
                holder_process_id: lease.holder_process_id,
                epoch: lease.epoch,
                expires_at_unix_micros: lease.expires_at_unix_micros,
            }),
            Ok(None) => {}
            // A loaded host can fail one read; the next poll reads the lease again.
            Err(error) => eprintln!("lease history: a lease read failed: {error}"),
        }
        tokio::select! {
            () = stop.cancelled() => return (observations, inspector),
            () = tokio::time::sleep(LEASE_POLL_INTERVAL) => {}
        }
    }
}

/// Panics on a falling epoch, a returning holder, or an early takeover; returns holders and handover delays.
fn check_lease_observations(
    observations: &[LeaseObservation],
    possible_releases: &[i64],
) -> (usize, Vec<Duration>) {
    let mut holders = BTreeSet::new();
    let mut replaced_holders = BTreeSet::new();
    let mut takeovers = Vec::new();
    // A new holder first writes the previous epoch, so one epoch can name two holders.
    for pair in observations.windows(2) {
        let (previous, next) = (&pair[0], &pair[1]);
        assert!(
            next.epoch >= previous.epoch,
            "the lease epoch fell from {previous:?} to {next:?}"
        );
        if next.holder_process_id != previous.holder_process_id {
            // Every acquisition names a fresh holder ID, so a replaced one never holds again.
            assert!(
                !replaced_holders.contains(next.holder_process_id.as_str()),
                "a replaced holder held the lease again: {previous:?} then {next:?}"
            );
            replaced_holders.insert(previous.holder_process_id.as_str());
            takeovers.push(takeover_delay(previous, next, possible_releases));
        }
    }
    holders.extend(
        observations
            .iter()
            .map(|observation| observation.holder_process_id.as_str()),
    );
    (holders.len(), takeovers)
}

/// Returns how long after availability `next` held the lease; panics if earlier without a possible release.
fn takeover_delay(
    previous: &LeaseObservation,
    next: &LeaseObservation,
    possible_releases: &[i64],
) -> Duration {
    let available_at = if previous.expires_at_unix_micros == RELEASED_LEASE_EXPIRY_UNIX_MICROS {
        previous.observed_at_unix_micros
    } else {
        previous.expires_at_unix_micros + micros(TAKEOVER_GRACE)
    };
    let delay = next.observed_at_unix_micros - available_at;
    if delay >= -micros(EARLY_TAKEOVER_TOLERANCE) {
        return Duration::from_micros(u64::try_from(delay).unwrap_or(0));
    }
    let may_have_been_released = possible_releases.iter().any(|&noted_at| {
        noted_at <= next.observed_at_unix_micros
            && noted_at + micros(POSSIBLE_RELEASE_WINDOW) >= previous.observed_at_unix_micros
    });
    assert!(
        may_have_been_released,
        "a holder took the lease {}ms before it was available, with no release possible: {previous:?} then {next:?}",
        -delay / 1000
    );
    // The release came after the earlier sample, so this bounds the delay from above.
    Duration::from_micros(
        u64::try_from(next.observed_at_unix_micros - previous.observed_at_unix_micros)
            .expect("lease samples are read in order"),
    )
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
    let (holders, takeovers) = check_lease_observations(
        &[
            observation(0, "first", 1, 3_000),
            observation(4_500, "second", 1, 19_500),
            observation(4_600, "second", 2, 19_600),
            observation(5_000, "second", 2, i64::MIN),
            observation(5_200, "third", 2, 20_200),
        ],
        &[],
    );
    assert_eq!(holders, 3);
    assert_eq!(
        takeovers,
        vec![Duration::from_millis(500), Duration::from_millis(200)]
    );
}

/// A release between two samples lets the next holder appear before the sampled expiry.
#[test]
fn a_takeover_after_an_unsampled_release_passes_the_check() {
    let (_, takeovers) = check_lease_observations(
        &[
            observation(10_000, "first", 1, 13_000),
            observation(10_100, "second", 1, 25_100),
        ],
        &[9_000_000],
    );
    assert_eq!(takeovers, vec![Duration::from_millis(100)]);
}

/// A holder that took the lease before it expired, with no release possible, fails the check.
#[test]
#[should_panic(expected = "before it was available, with no release possible")]
fn a_takeover_before_the_lease_was_available_fails_the_check() {
    check_lease_observations(
        &[
            observation(10_000, "first", 1, 13_000),
            observation(10_100, "second", 1, 25_100),
        ],
        &[],
    );
}

/// A replaced holder that holds the lease again, with a rising epoch, fails the check.
#[test]
#[should_panic(expected = "a replaced holder held the lease again")]
fn a_replaced_holder_that_holds_the_lease_again_fails_the_check() {
    check_lease_observations(
        &[
            observation(0, "first", 1, i64::MIN),
            observation(100, "second", 2, 15_100),
            observation(200, "second", 2, i64::MIN),
            observation(300, "first", 3, 15_300),
        ],
        &[],
    );
}

/// A falling lease epoch fails the check.
#[test]
#[should_panic(expected = "the lease epoch fell")]
fn a_falling_lease_epoch_fails_the_check() {
    check_lease_observations(
        &[
            observation(0, "first", 2, 3_000),
            observation(100, "second", 1, 3_100),
        ],
        &[],
    );
}

/// An epoch that two recoveries raised fails the check.
#[test]
#[should_panic(expected = "every recovery must raise a new epoch")]
fn a_repeated_epoch_start_fails_the_check() {
    check_epoch_starts(&[1, 2, 2]);
}
