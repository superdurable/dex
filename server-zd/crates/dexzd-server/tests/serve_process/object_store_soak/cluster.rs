// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Two `serve` processes of one node ID on the soak's prefix, an active and a hot standby,
//! behind a router that sends clients to whichever one serves.

use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_api::HOT_STANDBY_HEALTH_SERVICE_NAME;
use tokio::time::Instant;
use tonic::transport::Channel;
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

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
/// Long enough to ride out object storage latency spikes of a real bucket.
const LEASE_DURATION: &str = "10s";
/// Outlasts a killed process's lease and the one-second takeover grace.
pub(crate) const KILLED_LEASE_EXPIRY_WAIT: Duration = Duration::from_secs(12);
/// Longer than any standby lag, so a hot standby never misses a deleted log batch.
const LOG_DELETION_DELAY: &str = "10m";
const HEALTH_CHECK_TIMEOUT: Duration = Duration::from_secs(1);
/// The error line of a process whose start failed after it took the lease.
const FAILED_START_MARKER: &str = "dex-server-zd: starting the engine: ";
/// Failed starts of one slot in a row before the soak gives up; a supervisor restarts the rest.
const MAX_CONSECUTIVE_FAILED_STARTS: usize = 3;
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
}

/// What the cluster reports when it stops.
pub(crate) struct ClusterReport {
    pub(crate) serving: ServingReport,
    pub(crate) tally: MetricsTally,
    pub(crate) process_starts: usize,
    pub(crate) stale_batch_restarts: usize,
    /// The error line of every start that failed and was restarted.
    pub(crate) failed_starts: Vec<String>,
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
    tally: MetricsTally,
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
                    "cluster:\n  nodeId: {NODE_ID}\n  partitionCount: 16\n{object_store}localStorage:\n  directory: {local}\n  blockCacheBytes: 33554432\nlease:\n  duration: {LEASE_DURATION}\n  failIfHeld: false\nsnapshot:\n  interval: {snapshot_interval}s\n  retainedCount: 2\n  logDeletionDelay: {LOG_DELETION_DELAY}\nstandby:\n  mode: {standby_mode}\nstreamStore:\n  backend: memory\napi:\n  listen: \"{address}\"\nmetrics:\n  listen: '{metrics_address}'\nsearch:\n  backend: builtin\n",
                    object_store = target.object_store_yaml(),
                    local = local_directory.display(),
                    snapshot_interval = snapshot_interval.as_secs(),
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
            tally: MetricsTally::default(),
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

    /// Sends SIGKILL to `slot`'s process after a last metrics scrape.
    pub(crate) async fn kill(&mut self, slot: usize) {
        self.scrape(slot).await;
        let entry = &mut self.slots[slot];
        let mut process = entry
            .process
            .take()
            .unwrap_or_else(|| panic!("slot {} has no process to kill", entry.name));
        process.kill().await;
        if let Some(scrape) = entry.last_scrape.take() {
            self.tally.add_process(&scrape);
        }
        self.permitted_stale_batch_stops += 1;
    }

    /// Sends SIGTERM to `slot`'s process and requires a clean exit.
    pub(crate) async fn terminate(&mut self, slot: usize) {
        self.scrape(slot).await;
        let entry = &mut self.slots[slot];
        let mut process = entry
            .process
            .take()
            .unwrap_or_else(|| panic!("slot {} has no process to stop", entry.name));
        let status = process.terminate(PROCESS_SHUTDOWN_BUDGET).await;
        assert_eq!(
            status.code(),
            Some(0),
            "{} process {} did not stop cleanly:\n{}",
            entry.name,
            entry.process_count,
            process.log()
        );
        if let Some(scrape) = entry.last_scrape.take() {
            self.tally.add_process(&scrape);
        }
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

    /// Restarts a process that stopped on a late batch of a killed process, or whose start
    /// failed, as a container supervisor would; panics on any other exit and on a slot whose
    /// starts keep failing.
    pub(crate) fn supervise(&mut self) {
        for slot in 0..self.slots.len() {
            let entry = &mut self.slots[slot];
            let Some(status) = entry
                .process
                .as_mut()
                .and_then(RobustnessProcess::exit_status)
            else {
                continue;
            };
            let log = entry
                .process
                .as_ref()
                .map(RobustnessProcess::log)
                .unwrap_or_default();
            let failed_start = log
                .lines()
                .find(|line| line.starts_with(FAILED_START_MARKER))
                .map(str::to_owned);
            if self.permitted_stale_batch_stops > 0
                && status.code() == Some(1)
                && log.contains(STALE_BATCH_STOP_REASON)
            {
                eprintln!(
                    "soak supervisor: {} process {} stopped on a late batch of a killed process; restarting it",
                    entry.name, entry.process_count
                );
                self.permitted_stale_batch_stops -= 1;
                self.stale_batch_restarts += 1;
            } else if let Some(failed_start) = failed_start.filter(|_| status.code() == Some(1)) {
                entry.consecutive_failed_starts += 1;
                assert!(
                    entry.consecutive_failed_starts < MAX_CONSECUTIVE_FAILED_STARTS,
                    "{} failed to start {} times in a row:\n{log}",
                    entry.name,
                    entry.consecutive_failed_starts
                );
                eprintln!(
                    "soak supervisor: FAILED START of {} process {}, restarting it: {failed_start}",
                    entry.name, entry.process_count
                );
                self.failed_starts.push(format!(
                    "{} process {}: {failed_start}",
                    entry.name, entry.process_count
                ));
            } else {
                panic!(
                    "{} process {} exited with {status}:\n{log}",
                    entry.name, entry.process_count
                );
            }
            entry.process = None;
            if let Some(scrape) = entry.last_scrape.take() {
                self.tally.add_process(&scrape);
            }
            self.spawn(slot);
        }
    }

    /// Scrapes every running process, keeping each one's latest metrics.
    pub(crate) async fn scrape_all(&mut self) {
        for slot in 0..self.slots.len() {
            self.scrape(slot).await;
        }
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
            fleet: _fleet,
            ..
        } = self;
        ClusterReport {
            serving: router.shutdown_and_check().await,
            tally,
            process_starts,
            stale_batch_restarts,
            failed_starts,
        }
    }
}
