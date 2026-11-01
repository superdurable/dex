// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Checks that lease epochs only grow, that each was raised once, and that holders never return.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::path::PathBuf;
use std::time::Duration;

use tokio::task::JoinHandle;
use tokio_util::sync::CancellationToken;

use crate::robustness::inspection::ObjectStoreInspector;

const LEASE_POLL_INTERVAL: Duration = Duration::from_millis(100);
/// Logged once by each process that took the lease, with the epoch it raised.
const LEASE_ACQUIRED_LOG_LINE: &str = "engine node recovering";

/// One read of the node lease object.
#[derive(Debug)]
struct LeaseObservation {
    holder_process_id: String,
    epoch: u64,
}

/// Counts from a passing lease check.
#[derive(Debug)]
pub(crate) struct LeaseReport {
    pub(crate) observed_holders: usize,
    pub(crate) acquisitions: usize,
}

/// Reads the node lease object in the background through an endpoint without faults.
pub(crate) struct LeaseHistory {
    stop: CancellationToken,
    task: Option<JoinHandle<Vec<LeaseObservation>>>,
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

    /// Stops reading, then panics unless epochs never fell, replaced holders never returned, and no
    /// epoch was raised twice.
    ///
    /// A new holder first writes the previous epoch and raises it afterwards, so one epoch can
    /// name two holders.
    pub(crate) async fn stop_and_check(mut self, process_logs: &[PathBuf]) -> LeaseReport {
        self.stop.cancel();
        let observations = self
            .task
            .take()
            .expect("the lease reader runs until this check")
            .await
            .expect("the lease reader does not panic");
        let mut previous: Option<&LeaseObservation> = None;
        let mut replaced_holders: BTreeSet<&str> = BTreeSet::new();
        for observation in &observations {
            if let Some(previous) = previous {
                assert!(
                    observation.epoch >= previous.epoch,
                    "the lease epoch fell from {previous:?} to {observation:?}"
                );
                if observation.holder_process_id != previous.holder_process_id {
                    replaced_holders.insert(&previous.holder_process_id);
                }
            }
            assert!(
                !replaced_holders.contains(observation.holder_process_id.as_str()),
                "a replaced lease holder held the lease again: {observation:?}"
            );
            previous = Some(observation);
        }
        let mut acquiring_logs: BTreeMap<u64, Vec<String>> = BTreeMap::new();
        for path in process_logs {
            let log = fs::read_to_string(path)
                .unwrap_or_else(|error| panic!("cannot read {}: {error}", path.display()));
            for epoch in acquired_epochs(&log) {
                acquiring_logs
                    .entry(epoch)
                    .or_default()
                    .push(path.display().to_string());
            }
        }
        for (epoch, logs) in &acquiring_logs {
            assert_eq!(
                logs.len(),
                1,
                "lease epoch {epoch} was raised more than once: {logs:?}"
            );
        }
        LeaseReport {
            observed_holders: replaced_holders.len() + usize::from(previous.is_some()),
            acquisitions: acquiring_logs.len(),
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
) -> Vec<LeaseObservation> {
    let mut observations = Vec::new();
    loop {
        if let Some(lease) = inspector.node_lease_if_present().await {
            observations.push(LeaseObservation {
                holder_process_id: lease.holder_process_id,
                epoch: lease.epoch,
            });
        }
        tokio::select! {
            () = stop.cancelled() => return observations,
            () = tokio::time::sleep(LEASE_POLL_INTERVAL) => {}
        }
    }
}

/// Returns the epoch of every lease acquisition that `log` records.
fn acquired_epochs(log: &str) -> Vec<u64> {
    log.lines()
        .filter(|line| line.contains(LEASE_ACQUIRED_LOG_LINE))
        .map(|line| {
            line.split_whitespace()
                .find_map(|token| token.strip_prefix("epoch="))
                .and_then(|epoch| epoch.parse().ok())
                .unwrap_or_else(|| panic!("the lease acquisition line has no epoch: {line}"))
        })
        .collect()
}
