// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Two `serve` processes of one node ID on the soak's prefix, an active and a hot standby,
//! behind a router that sends clients to whichever one serves.

use std::collections::VecDeque;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_api::HOT_STANDBY_HEALTH_SERVICE_NAME;
use tokio::time::Instant;
use tonic::transport::Channel;
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

use super::log_tally::LogTally;
use super::metrics_tally::MetricsTally;
use super::target::SoakTarget;
use crate::harness::free_loopback_address;
use crate::metrics::try_scrape;
use crate::robustness::processes::{
    PROCESS_SHUTDOWN_BUDGET, RobustnessProcess, STALE_BATCH_STOP_REASON, ServeFleet,
};
use crate::robustness::workload::{VerificationReport, Workload};
use crate::standby_failover::readiness_router::{ReadinessRouter, RouterBackend, ServingReport};

pub(crate) const NODE_ID: &str = "soak-node";

/// Added to the lease duration: the one-second takeover grace plus a margin.
const KILLED_LEASE_EXPIRY_MARGIN: Duration = Duration::from_secs(2);
/// Longer than any standby lag, so a hot standby never misses a deleted log batch.
const LOG_DELETION_DELAY: &str = "10m";
const HEALTH_CHECK_TIMEOUT: Duration = Duration::from_secs(1);
/// The error line of a process whose start failed after it took the lease.
const FAILED_START_MARKER: &str = "dex-server-zd: starting the engine: ";
/// Failed starts of one slot in a row before the soak gives up; a supervisor restarts the rest.
const MAX_CONSECUTIVE_FAILED_STARTS: usize = 3;
/// More unplanned exits and failed starts of one slot within the window fail the soak.
const MAX_EXITS_IN_WINDOW: usize = 3;
const EXIT_WINDOW: Duration = Duration::from_secs(10 * 60);
/// The prefix of the line that a process prints to stderr when it stops on an error.
const PROCESS_STOP_MARKER: &str = "dex-server-zd: ";
const SUPERVISION_POLL_INTERVAL: Duration = Duration::from_millis(100);

/// One process position: fixed addresses, a local directory, and its current process.
struct SoakSlot {
    name: String,
    config_path: PathBuf,
    /// The same configuration with `standby.mode: cold`.
    cold_standby_config_path: PathBuf,
    local_directory: PathBuf,
    address: SocketAddr,
    metrics_address: SocketAddr,
    process: Option<RobustnessProcess>,
    process_count: usize,
    /// The current process's latest metrics, added to the tally when it ends.
    last_scrape: Option<String>,
    /// Starts that failed since the slot last served or followed as a hot standby.
    consecutive_failed_starts: usize,
    /// When each unplanned exit and failed start of the slot within [`EXIT_WINDOW`] happened.
    recent_exits: VecDeque<Instant>,
    /// The current process's log up to this byte is in the log tally.
    scanned_log_bytes: usize,
}

/// What the cluster reports when it stops.
pub(crate) struct ClusterReport {
    pub(crate) serving: ServingReport,
    pub(crate) tally: MetricsTally,
    pub(crate) process_starts: usize,
    pub(crate) stale_batch_restarts: usize,
    /// The error line of every start that failed and was restarted.
    pub(crate) failed_starts: Vec<String>,
    /// The time, process, exit status, and stop reason of every other exit that was restarted.
    pub(crate) unplanned_exits: Vec<String>,
    pub(crate) log_tally: LogTally,
}

/// The soak's serve processes and the router in front of them.
pub(crate) struct SoakCluster {
    fleet: ServeFleet,
    slots: Vec<SoakSlot>,
    router: ReadinessRouter,
    /// Each SIGKILL lets one later process stop once on the killed process's late batch.
    permitted_stale_batch_stops: usize,
    stale_batch_restarts: usize,
    failed_starts: Vec<String>,
    unplanned_exits: Vec<String>,
    tally: MetricsTally,
    log_tally: LogTally,
    created_at: Instant,
    lease_duration: Duration,
}

impl SoakCluster {
    /// Writes one configuration per slot under `directory`; no process runs until [`Self::spawn`].
    pub(crate) async fn start(
        directory: &Path,
        log_directory: &Path,
        target: &SoakTarget,
        workload: &Workload,
        slot_names: &[&str],
        snapshot_interval: Duration,
        lease_duration: Duration,
    ) -> Self {
        let mut slots = Vec::new();
        for name in slot_names {
            let address = free_loopback_address();
            let metrics_address = free_loopback_address();
            let config_path = directory.join(format!("{name}.yaml"));
            let cold_standby_config_path = directory.join(format!("{name}-cold-standby.yaml"));
            let local_directory = directory.join(format!("{name}-store"));
            let contents = |standby_mode: &str| {
                format!(
                    "cluster:\n  nodeId: {NODE_ID}\n  partitionCount: 16\n{object_store}localStorage:\n  directory: {local}\n  blockCacheBytes: 33554432\nlease:\n  duration: {lease_duration}s\n  failIfHeld: false\nsnapshot:\n  interval: {snapshot_interval}s\n  retainedCount: 2\n  logDeletionDelay: {LOG_DELETION_DELAY}\nstandby:\n  mode: {standby_mode}\nstreamStore:\n  backend: memory\napi:\n  listen: \"{address}\"\nmetrics:\n  listen: '{metrics_address}'\nsearch:\n  backend: builtin\n",
                    object_store = target.object_store_yaml(),
                    local = local_directory.display(),
                    snapshot_interval = snapshot_interval.as_secs(),
                    lease_duration = lease_duration.as_secs(),
                )
            };
            std::fs::write(&config_path, contents("hot"))
                .expect("the soak configuration is written");
            std::fs::write(&cold_standby_config_path, contents("cold"))
                .expect("the soak configuration is written");
            slots.push(SoakSlot {
                name: (*name).to_owned(),
                config_path,
                cold_standby_config_path,
                local_directory,
                address,
                metrics_address,
                process: None,
                process_count: 0,
                last_scrape: None,
                consecutive_failed_starts: 0,
                recent_exits: VecDeque::new(),
                scanned_log_bytes: 0,
            });
        }
        let router = ReadinessRouter::start(
            slots
                .iter()
                .map(|slot| RouterBackend {
                    name: slot.name.clone(),
                    api_address: slot.address,
                    metrics_address: slot.metrics_address,
                })
                .collect(),
        )
        .await;
        let (access_key_id, secret_access_key) = target.credentials();
        Self {
            fleet: ServeFleet::new(log_directory, workload.summary_source())
                .with_object_store_credentials(access_key_id, secret_access_key),
            slots,
            router,
            permitted_stale_batch_stops: 0,
            stale_batch_restarts: 0,
            failed_starts: Vec::new(),
            unplanned_exits: Vec::new(),
            tally: MetricsTally::default(),
            log_tally: LogTally::default(),
            created_at: Instant::now(),
            lease_duration,
        }
    }

    pub(crate) fn router_address(&self) -> SocketAddr {
        self.router.address()
    }

    pub(crate) fn router_channel(&self) -> Channel {
        Channel::from_shared(format!("http://{}", self.router.address()))
            .expect("the router address is a valid URI")
            .connect_lazy()
    }

    pub(crate) fn slot_name(&self, slot: usize) -> &str {
        &self.slots[slot].name
    }

    pub(crate) fn slot_count(&self) -> usize {
        self.slots.len()
    }

    /// Returns the slot that the router sends new connections to.
    pub(crate) fn serving_slot(&self) -> Option<usize> {
        self.router.serving_backend()
    }

    /// Starts a process in `slot`, which must have none.
    pub(crate) fn spawn(&mut self, slot: usize) {
        self.spawn_with_standby_mode(slot, false);
    }

    /// Starts a process in `slot` that waits for the lease without following the log.
    pub(crate) fn spawn_as_cold_standby(&mut self, slot: usize) {
        self.spawn_with_standby_mode(slot, true);
    }

    fn spawn_with_standby_mode(&mut self, slot: usize, is_cold_standby: bool) {
        let entry = &mut self.slots[slot];
        assert!(
            entry.process.is_none(),
            "slot {} already runs a process",
            entry.name
        );
        entry.process_count += 1;
        entry.last_scrape = None;
        self.router.note_process_start(slot);
        let name = format!("{}-{}", entry.name, entry.process_count);
        let config_path = if is_cold_standby {
            &entry.cold_standby_config_path
        } else {
            &entry.config_path
        };
        entry.process = Some(self.fleet.spawn(config_path, entry.address, &name));
    }

    /// Deletes `slot`'s local directory, so its next process restores from object storage.
    pub(crate) fn wipe(&mut self, slot: usize) {
        let entry = &self.slots[slot];
        assert!(entry.process.is_none(), "slot {} still runs", entry.name);
        if entry.local_directory.exists() {
            std::fs::remove_dir_all(&entry.local_directory)
                .expect("the soak's local directory is deleted");
        }
    }

    /// Sends SIGKILL to `slot`'s process after a last metrics scrape; a process that already
    /// exited is recorded as an exit instead.
    pub(crate) async fn kill(&mut self, slot: usize) {
        self.scrape(slot).await;
        if self.reap_exited_process(slot) {
            return;
        }
        let entry = &mut self.slots[slot];
        let mut process = entry
            .process
            .take()
            .unwrap_or_else(|| panic!("slot {} has no process to kill", entry.name));
        process.kill().await;
        self.retire_process(slot, &process.log());
        self.permitted_stale_batch_stops += 1;
    }

    /// Sends SIGTERM to `slot`'s process and requires a clean exit; a process that already
    /// exited is recorded as an exit instead.
    pub(crate) async fn terminate(&mut self, slot: usize) {
        self.scrape(slot).await;
        if self.reap_exited_process(slot) {
            return;
        }
        let entry = &mut self.slots[slot];
        let mut process = entry
            .process
            .take()
            .unwrap_or_else(|| panic!("slot {} has no process to stop", entry.name));
        let status = process.terminate(PROCESS_SHUTDOWN_BUDGET).await;
        let log = process.log();
        assert_eq!(
            status.code(),
            Some(0),
            "{} process {} did not stop cleanly:\n{log}",
            entry.name,
            entry.process_count,
        );
        self.retire_process(slot, &log);
    }

    /// Waits up to `budget` until a slot other than `excluded` serves; returns it.
    pub(crate) async fn wait_for_serving(
        &mut self,
        excluded: Option<usize>,
        budget: Duration,
    ) -> usize {
        let deadline = Instant::now() + budget;
        loop {
            self.supervise();
            if let Some(slot) = (0..self.slots.len()).find(|slot| {
                Some(*slot) != excluded
                    && self.slots[*slot].process.is_some()
                    && self.router.is_serving(*slot)
            }) {
                self.slots[slot].consecutive_failed_starts = 0;
                return slot;
            }
            assert!(
                Instant::now() < deadline,
                "no process outside slot {excluded:?} served within {budget:?}"
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Waits up to `budget` until `slot`'s process reports `dex.HotStandby` SERVING.
    pub(crate) async fn wait_for_hot_standby(&mut self, slot: usize, budget: Duration) {
        let channel = Channel::from_shared(format!("http://{}", self.slots[slot].address))
            .expect("the slot address is a valid URI")
            .connect_lazy();
        let mut health = HealthClient::new(channel);
        let deadline = Instant::now() + budget;
        loop {
            self.supervise();
            let check = health.check(HealthCheckRequest {
                service: HOT_STANDBY_HEALTH_SERVICE_NAME.to_owned(),
            });
            if self.slots[slot].process.is_some()
                && let Ok(Ok(response)) = tokio::time::timeout(HEALTH_CHECK_TIMEOUT, check).await
                && response.get_ref().status == ServingStatus::Serving as i32
            {
                self.slots[slot].consecutive_failed_starts = 0;
                return;
            }
            assert!(
                Instant::now() < deadline,
                "slot {} did not become a hot standby within {budget:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Restarts every process that exited, as a container supervisor would.
    ///
    /// Panics when one slot exits or fails to start too often; see [`Self::reap_exited_process`].
    pub(crate) fn supervise(&mut self) {
        for slot in 0..self.slots.len() {
            if self.reap_exited_process(slot) {
                self.spawn(slot);
            }
        }
    }

    /// Returns the number of unplanned exits so far.
    pub(crate) fn unplanned_exit_count(&self) -> usize {
        self.unplanned_exits.len()
    }

    /// Records the exit of `slot`'s process when it exited; returns whether it did.
    ///
    /// A stop on a late batch of a killed or exited process is expected. A start that failed
    /// after the lease is a FAILED START, and every other exit is an UNPLANNED EXIT. Panics after
    /// [`MAX_CONSECUTIVE_FAILED_STARTS`] failed starts in a row, or after more than
    /// [`MAX_EXITS_IN_WINDOW`] of both within [`EXIT_WINDOW`].
    fn reap_exited_process(&mut self, slot: usize) -> bool {
        let entry = &mut self.slots[slot];
        let Some(status) = entry
            .process
            .as_mut()
            .and_then(RobustnessProcess::exit_status)
        else {
            return false;
        };
        let process = entry
            .process
            .take()
            .expect("an exited slot still holds its process");
        let log = process.log();
        let process_name = format!("{} process {}", entry.name, entry.process_count);
        let at = super::elapsed(self.created_at);
        let failed_start = log
            .lines()
            .find(|line| line.starts_with(FAILED_START_MARKER))
            .filter(|_| status.code() == Some(1))
            .map(str::to_owned);
        if self.permitted_stale_batch_stops > 0
            && status.code() == Some(1)
            && log.contains(STALE_BATCH_STOP_REASON)
        {
            eprintln!(
                "soak supervisor +{at}: {process_name} stopped on a late batch of an earlier process; restarting it"
            );
            self.permitted_stale_batch_stops -= 1;
            self.stale_batch_restarts += 1;
        } else {
            self.count_exit_in_window(slot, &process_name);
            let entry = &mut self.slots[slot];
            if let Some(failed_start) = failed_start {
                entry.consecutive_failed_starts += 1;
                assert!(
                    entry.consecutive_failed_starts < MAX_CONSECUTIVE_FAILED_STARTS,
                    "{} failed to start {} times in a row:\n{log}",
                    entry.name,
                    entry.consecutive_failed_starts
                );
                eprintln!(
                    "soak supervisor +{at}: FAILED START of {process_name}, restarting it: {failed_start}"
                );
                self.failed_starts
                    .push(format!("{process_name}: {failed_start}"));
            } else {
                let reason = stop_reason(&log);
                eprintln!(
                    "soak supervisor +{at}: UNPLANNED EXIT of {process_name} with {status}, restarting it as standby: {reason}"
                );
                self.unplanned_exits
                    .push(format!("+{at} {process_name} ({status}): {reason}"));
                // Its unacknowledged batches may land late, like those of a killed process.
                self.permitted_stale_batch_stops += 1;
            }
        }
        self.retire_process(slot, &log);
        true
    }

    /// Panics when `slot` exits more than [`MAX_EXITS_IN_WINDOW`] times within [`EXIT_WINDOW`].
    fn count_exit_in_window(&mut self, slot: usize, process_name: &str) {
        let now = Instant::now();
        let entry = &mut self.slots[slot];
        entry.recent_exits.push_back(now);
        while entry
            .recent_exits
            .front()
            .is_some_and(|exit| now.duration_since(*exit) > EXIT_WINDOW)
        {
            entry.recent_exits.pop_front();
        }
        assert!(
            entry.recent_exits.len() <= MAX_EXITS_IN_WINDOW,
            "slot {} had {} unplanned exits and failed starts within {EXIT_WINDOW:?}, the last of {process_name}; unplanned exits so far: {:#?}",
            entry.name,
            entry.recent_exits.len(),
            self.unplanned_exits
        );
    }

    /// Adds the rest of an ended process's log and its last metrics scrape to the tallies.
    fn retire_process(&mut self, slot: usize, log: &str) {
        let entry = &mut self.slots[slot];
        self.log_tally
            .add_lines(log.get(entry.scanned_log_bytes..).unwrap_or_default());
        entry.scanned_log_bytes = 0;
        if let Some(scrape) = entry.last_scrape.take() {
            self.tally.add_process(&scrape);
        }
    }

    /// Scrapes every running process, keeping each one's latest metrics, and adds the new
    /// complete lines of its log to the log tally.
    pub(crate) async fn scrape_all(&mut self) {
        for slot in 0..self.slots.len() {
            self.scrape(slot).await;
            self.scan_log(slot);
        }
    }

    fn scan_log(&mut self, slot: usize) {
        let entry = &mut self.slots[slot];
        let Some(process) = &entry.process else {
            return;
        };
        let log = process.log();
        let complete = log.rfind('\n').map_or(0, |newline| newline + 1);
        if let Some(new_lines) = log.get(entry.scanned_log_bytes..complete) {
            self.log_tally.add_lines(new_lines);
            entry.scanned_log_bytes = complete;
        }
    }

    /// Returns the log tally of every ended process and the scanned logs of running ones.
    pub(crate) fn log_tally_so_far(&self) -> &LogTally {
        &self.log_tally
    }

    async fn scrape(&mut self, slot: usize) {
        let entry = &mut self.slots[slot];
        if entry.process.is_none() {
            return;
        }
        if let Ok(body) = try_scrape(entry.metrics_address, "/metrics").await
            && body.starts_with("HTTP/1.1 200")
        {
            entry.last_scrape = Some(body);
        }
    }

    /// Returns the tally of every ended process plus each running process's latest scrape.
    pub(crate) fn tally_so_far(&self) -> MetricsTally {
        let mut tally = self.tally.clone();
        for slot in &self.slots {
            if let Some(scrape) = &slot.last_scrape {
                tally.add_process(scrape);
            }
        }
        tally
    }

    /// Returns everything `slot`'s current process logged.
    pub(crate) fn process_log(&self, slot: usize) -> String {
        self.slots[slot]
            .process
            .as_ref()
            .map(RobustnessProcess::log)
            .unwrap_or_default()
    }

    /// Verifies every acknowledged write of `workload` on the serving process within `budget`.
    pub(crate) async fn verify_workload(
        &mut self,
        workload: &Workload,
        serving_budget: Duration,
        verification_budget: Duration,
    ) -> VerificationReport {
        let slot = self.wait_for_serving(None, serving_budget).await;
        let channel = Channel::from_shared(format!("http://{}", self.slots[slot].address))
            .expect("the slot address is a valid URI")
            .connect_lazy();
        let process = self.slots[slot]
            .process
            .as_mut()
            .expect("a serving slot runs a process");
        workload
            .verify_within(channel, process, verification_budget)
            .await
    }

    /// Stops every process, standbys first, then checks that no two processes served at once.
    pub(crate) async fn finish(mut self) -> ClusterReport {
        let serving = self.router.serving_backend();
        let mut order: Vec<usize> = (0..self.slots.len())
            .filter(|slot| Some(*slot) != serving)
            .collect();
        order.extend(serving);
        for slot in order {
            if self.slots[slot].process.is_some() {
                self.terminate(slot).await;
            }
        }
        let process_starts = self.slots.iter().map(|slot| slot.process_count).sum();
        let Self {
            router,
            tally,
            stale_batch_restarts,
            failed_starts,
            unplanned_exits,
            log_tally,
            fleet: _fleet,
            ..
        } = self;
        ClusterReport {
            serving: router.shutdown_and_check().await,
            tally,
            process_starts,
            stale_batch_restarts,
            failed_starts,
            unplanned_exits,
            log_tally,
        }
    }
}

/// Returns the last stop line of a process log, else its last ERROR line, else its last line.
fn stop_reason(log: &str) -> String {
    let lines: Vec<&str> = log.lines().map(str::trim).collect();
    lines
        .iter()
        .rev()
        .find(|line| line.starts_with(PROCESS_STOP_MARKER))
        .or_else(|| lines.iter().rev().find(|line| line.contains(" ERROR ")))
        .or_else(|| lines.iter().rev().find(|line| !line.is_empty()))
        .map_or_else(
            || "<the log is empty>".to_owned(),
            |line| (*line).to_owned(),
        )
}

impl SoakCluster {
    /// Returns how long a killed process's lease can still block a takeover.
    pub(crate) fn killed_lease_expiry_wait(&self) -> Duration {
        self.lease_duration + KILLED_LEASE_EXPIRY_MARGIN
    }
}
