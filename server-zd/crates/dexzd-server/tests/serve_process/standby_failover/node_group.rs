// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `serve` processes of one node ID, each with its own directory and object store endpoint.

use std::fmt;
use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::process::ExitStatus;
use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dexzd_api::HOT_STANDBY_HEALTH_SERVICE_NAME;
use dexzd_protocol::dex::admin_service_client::AdminServiceClient;
use dexzd_protocol::dex::{
    DescribeActiveNodeRequest, DescribeActiveNodeResponse, StepDownActiveNodeRequest,
    StepDownActiveNodeResponse,
};
use tokio::sync::{OwnedSemaphorePermit, Semaphore};
use tokio::time::Instant;
use tonic::transport::Channel;
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

use super::lease_epochs::{LeaseHistory, LeaseReport};
use super::object_store_endpoints::{ObjectStoreEndpoint, ObjectStoreFault};
use super::readiness_router::{ReadinessRouter, RouterBackend, ServingReport};
use crate::harness::free_loopback_address;
use crate::metrics::{sample_with_label, try_scrape};
use crate::robustness::inspection::ObjectStoreInspector;
use crate::robustness::processes::{
    DurableEmulator, PROCESS_SHUTDOWN_BUDGET, RobustnessProcess, RobustnessServeConfig,
    STALE_BATCH_STOP_REASON, ServeFleet,
};
use crate::robustness::workload::{VerificationReport, Workload};

/// No snapshot or log cleanup by default, so recovery replays the log.
pub(crate) const SNAPSHOT_INTERVAL: &str = "1h";
pub(crate) const LOG_DELETION_DELAY: &str = "24h";
const HEALTH_CHECK_TIMEOUT: Duration = Duration::from_millis(500);
const SUPERVISION_POLL_INTERVAL: Duration = Duration::from_millis(50);
/// Log line of a failed lease renewal attempt that the process retries after a logged backoff.
const RENEWAL_RETRY_MARKER: &str = "node lease renewal attempt failed; retrying";
const RENEWAL_RETRY_BACKOFF_FIELD: &str = "retry_in_milliseconds=";
/// How far into a retried renewal attempt, which lasts at least a second, the process is frozen.
const RENEWAL_ATTEMPT_FREEZE_OFFSET: Duration = Duration::from_millis(250);
const LOG_POLL_INTERVAL: Duration = Duration::from_millis(20);
/// Covers several failovers, each up to a lease, the takeover grace, and a recovery.
const PROGRESS_BUDGET: Duration = Duration::from_secs(60);

/// The settings of one slot; every slot runs the same node ID.
pub(crate) struct SlotSettings<'a> {
    pub(crate) name: &'a str,
    /// The lease this slot's processes write when they hold it, such as `3s`.
    pub(crate) lease_duration: &'a str,
    pub(crate) standby_mode: SlotStandbyMode,
    pub(crate) snapshot_interval: &'a str,
    pub(crate) log_deletion_delay: &'a str,
}

/// Whether a slot's processes follow the log while they wait for the lease.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum SlotStandbyMode {
    Cold,
    Hot,
}

/// What happens to a slot's local directory before its next process starts.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum DirectoryFate {
    /// The process restarts on the same directory, like a container restarted in its pod.
    Keep,
    /// The directory is deleted, like a pod recreated with an empty `emptyDir`.
    Wipe,
}

/// A signal for one slot's process.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum ProcessSignal {
    /// SIGKILL; waits until the process is reaped.
    Kill,
    /// SIGTERM; waits for a clean exit.
    Terminate,
    /// SIGSTOP; waits until the kernel reports the process stopped.
    Stop,
    /// SIGCONT, without waiting for any effect.
    Continue,
}

/// One position in the group: a name, fixed addresses, a directory, and its current process.
struct NodeSlot {
    name: String,
    config_path: PathBuf,
    local_directory: PathBuf,
    address: SocketAddr,
    metrics_address: SocketAddr,
    object_store: ObjectStoreEndpoint,
    process_count: usize,
    process: Option<RobustnessProcess>,
    /// The test made the current process end by itself, such as by cutting its object storage.
    may_exit: bool,
}

impl NodeSlot {
    fn running(&mut self) -> &mut RobustnessProcess {
        self.process
            .as_mut()
            .unwrap_or_else(|| panic!("slot {} has no running process", self.name))
    }
}

/// Counts from a passing group, for the test's report line.
pub(crate) struct GroupReport {
    serving: ServingReport,
    lease: LeaseReport,
    process_starts: usize,
    stale_batch_restarts: usize,
}

impl fmt::Display for GroupReport {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{} process starts, {} stale-batch restarts; {} lease epochs raised, {} lease holders observed, longest takeover after availability {:?}; {} readiness probes, {} SERVING in {} serving periods, {} SERVING without a lease for at most {:?}, {} SERVING with an unread epoch",
            self.process_starts,
            self.stale_batch_restarts,
            self.lease.acquisitions,
            self.lease.observed_holders,
            self.lease.longest_takeover_after_availability,
            self.serving.observations,
            self.serving.serving_observations,
            self.serving.serving_periods,
            self.serving.serving_without_lease,
            self.serving.longest_serving_without_lease,
            self.serving.serving_with_unknown_epoch
        )
    }
}

impl GroupReport {
    /// Returns how many processes and lease epochs reported SERVING.
    pub(crate) fn serving_periods(&self) -> usize {
        self.serving.serving_periods
    }

    /// Returns how many distinct holders the lease object named.
    pub(crate) fn observed_lease_holders(&self) -> usize {
        self.lease.observed_holders
    }

    /// Returns the longest time a new holder took to appear after the lease became available.
    pub(crate) fn longest_takeover_after_availability(&self) -> Duration {
        self.lease
            .longest_takeover_after_availability
            .expect("a takeover test observed a handover")
    }
}

/// Node groups that may run at once; more starve each other's lease renewals on a small host.
static NODE_GROUP_PERMITS: LazyLock<Arc<Semaphore>> = LazyLock::new(|| {
    let cpus = std::thread::available_parallelism().map_or(2, std::num::NonZeroUsize::get);
    Arc::new(Semaphore::new((cpus / 4).clamp(2, 4)))
});

/// Serve processes of one node ID, signalled and restarted per slot, behind [`Self::router_address`].
pub(crate) struct NodeGroup {
    _running_permit: OwnedSemaphorePermit,
    emulator: DurableEmulator,
    fleet: ServeFleet,
    slots: Vec<NodeSlot>,
    router: ReadinessRouter,
    lease_history: LeaseHistory,
    /// Each SIGKILL or permitted exit lets one later process stop once on that process's late batch.
    permitted_stale_batch_stops: usize,
    stale_batch_restarts: usize,
    /// Whether every slot keeps the log for a day, so no EpochStart is cleaned up.
    is_log_complete: bool,
}

impl NodeGroup {
    /// Prepares one slot per entry of `settings`; no process runs until [`Self::spawn`].
    pub(crate) async fn start(
        directory: &Path,
        workload: &Workload,
        settings: &[SlotSettings<'_>],
    ) -> Self {
        let running_permit = Arc::clone(&NODE_GROUP_PERMITS)
            .acquire_owned()
            .await
            .expect("the node group semaphore is never closed");
        let emulator = DurableEmulator::start().await;
        let inspector = ObjectStoreInspector::connect(&emulator.endpoint()).await;
        let mut slots = Vec::new();
        for slot in settings {
            let object_store = ObjectStoreEndpoint::start(emulator.store()).await;
            let address = free_loopback_address();
            let metrics_address = free_loopback_address();
            let config_path = directory.join(format!("{}.yaml", slot.name));
            let local_directory = directory.join(format!("{}-store", slot.name));
            RobustnessServeConfig {
                primary_endpoint: &object_store.endpoint(),
                listen: address,
                local_directory: &local_directory,
                lease_duration: slot.lease_duration,
                snapshot_interval: slot.snapshot_interval,
                log_deletion_delay: slot.log_deletion_delay,
            }
            .write_with_metrics(&config_path, metrics_address);
            if slot.standby_mode == SlotStandbyMode::Hot {
                let mut contents =
                    std::fs::read_to_string(&config_path).expect("the configuration is readable");
                contents.push_str("standby:\n  mode: hot\n");
                std::fs::write(&config_path, contents).expect("the configuration is written");
            }
            slots.push(NodeSlot {
                name: slot.name.to_owned(),
                config_path,
                local_directory,
                address,
                metrics_address,
                object_store,
                process_count: 0,
                process: None,
                may_exit: false,
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
        Self {
            _running_permit: running_permit,
            emulator,
            fleet: ServeFleet::new(directory, workload.summary_source()),
            slots,
            router,
            lease_history: LeaseHistory::start(inspector),
            permitted_stale_batch_stops: 0,
            stale_batch_restarts: 0,
            is_log_complete: settings
                .iter()
                .all(|slot| slot.log_deletion_delay == LOG_DELETION_DELAY),
        }
    }

    /// Returns the Service address that routes to whichever process serves.
    pub(crate) fn router_address(&self) -> SocketAddr {
        self.router.address()
    }

    /// Returns a channel through the router.
    pub(crate) fn router_channel(&self) -> Channel {
        Channel::from_shared(format!("http://{}", self.router.address()))
            .expect("the router address is a valid URI")
            .connect_lazy()
    }

    /// Returns how many processes `slot` started so far.
    /// Returns everything `slot`'s current process logged.
    pub(crate) fn process_log(&mut self, slot: usize) -> String {
        self.slots[slot].running().log()
    }

    pub(crate) fn process_starts(&self, slot: usize) -> usize {
        self.slots[slot].process_count
    }

    /// Returns the address that `slot`'s processes serve the API on.
    pub(crate) fn api_address(&self, slot: usize) -> SocketAddr {
        self.slots[slot].address
    }

    /// Returns the first and the newest seq of the node's log batches in object storage.
    pub(crate) async fn log_seq_range(&self) -> (u64, u64) {
        let inspector = ObjectStoreInspector::connect(&self.emulator.endpoint()).await;
        let seqs: Vec<u64> = inspector
            .log_batch_keys()
            .await
            .iter()
            .map(|key| {
                key.rsplit('/')
                    .next()
                    .and_then(|seq| seq.parse().ok())
                    .unwrap_or_else(|| panic!("{key} does not end with a log seq"))
            })
            .collect();
        (
            seqs.iter().copied().min().unwrap_or(0),
            seqs.iter().copied().max().unwrap_or(0),
        )
    }

    /// Returns the object store endpoint that only `slot`'s processes use.
    pub(crate) fn object_store(&self, slot: usize) -> &ObjectStoreEndpoint {
        &self.slots[slot].object_store
    }

    /// Starts a process in `slot`, which must have none.
    pub(crate) fn spawn(&mut self, slot: usize) {
        let entry = &mut self.slots[slot];
        assert!(
            entry.process.is_none(),
            "slot {} already runs a process",
            entry.name
        );
        entry.process_count += 1;
        entry.may_exit = false;
        self.router.note_process_start(slot);
        let process = self.fleet.spawn(
            &entry.config_path,
            entry.address,
            &format!("{}-{}", entry.name, entry.process_count),
        );
        entry.process = Some(process);
    }

    /// Starts the next process of `slot` after its previous one ended, keeping or wiping its directory.
    pub(crate) fn restart(&mut self, slot: usize, fate: DirectoryFate) {
        let entry = &self.slots[slot];
        assert!(
            entry.process.is_none(),
            "slot {} still runs a process",
            entry.name
        );
        if fate == DirectoryFate::Wipe && entry.local_directory.exists() {
            std::fs::remove_dir_all(&entry.local_directory)
                .expect("the local directory is deleted");
        }
        self.spawn(slot);
    }

    /// Sends `signal` to `slot`'s process and waits for its effect.
    pub(crate) async fn signal(&mut self, slot: usize, signal: ProcessSignal) {
        let entry = &mut self.slots[slot];
        match signal {
            ProcessSignal::Kill => {
                entry.running().kill().await;
                entry.process = None;
                self.permitted_stale_batch_stops += 1;
            }
            ProcessSignal::Terminate => {
                self.lease_history.note_possible_release();
                let process = entry.running();
                let status = process.terminate(PROCESS_SHUTDOWN_BUDGET).await;
                assert_eq!(status.code(), Some(0), "{}", process.log());
                entry.process = None;
            }
            ProcessSignal::Stop => entry.running().freeze_and_wait_until_stopped().await,
            ProcessSignal::Continue => entry.running().send_signal("CONT").await,
        }
    }

    /// Clears `slot`'s object store faults; held writes then land, and may stop the active once.
    pub(crate) async fn heal_object_store(&mut self, slot: usize) {
        self.permitted_stale_batch_stops += 1;
        self.slots[slot].object_store.clear_faults().await;
    }

    /// Waits up to `budget` until a held PUT under `key_prefix` reached the store; returns every held PUT.
    pub(crate) async fn wait_for_held_puts(
        &mut self,
        slot: usize,
        key_prefix: &str,
        budget: Duration,
    ) -> Vec<(String, u64)> {
        let deadline = Instant::now() + budget;
        loop {
            self.supervise();
            let held_puts = self.slots[slot]
                .object_store
                .held_puts_that_completed()
                .await;
            if held_puts.iter().any(|(key, _)| key.starts_with(key_prefix)) {
                return held_puts;
            }
            assert!(
                Instant::now() < deadline,
                "no PUT under {key_prefix} held at slot {} reached the store within {budget:?}: {held_puts:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Blackholes `slot`'s object store, then freezes its process while a retried lease renewal is held.
    pub(crate) async fn blackhole_and_freeze_during_a_lease_renewal(
        &mut self,
        slot: usize,
        budget: Duration,
    ) {
        let earlier_retries = self.lease_renewal_retry_backoffs(slot).len();
        self.slots[slot]
            .object_store
            .inject(ObjectStoreFault::Blackhole)
            .await;
        let deadline = Instant::now() + budget;
        let backoff = loop {
            self.supervise();
            let backoffs = self.lease_renewal_retry_backoffs(slot);
            if backoffs.len() > earlier_retries {
                break *backoffs.last().expect("a new retry was just logged");
            }
            assert!(
                Instant::now() < deadline,
                "slot {} retried no lease renewal within {budget:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(LOG_POLL_INTERVAL).await;
        };
        // The retried attempt starts after its backoff, and the blackhole holds its PUT.
        tokio::time::sleep(backoff + RENEWAL_ATTEMPT_FREEZE_OFFSET).await;
        self.signal(slot, ProcessSignal::Stop).await;
    }

    /// Returns the backoff of every lease renewal retry that `slot`'s current process logged.
    fn lease_renewal_retry_backoffs(&mut self, slot: usize) -> Vec<Duration> {
        self.slots[slot]
            .running()
            .log()
            .lines()
            .filter(|line| line.contains(RENEWAL_RETRY_MARKER))
            .map(|line| {
                let milliseconds = line
                    .split_whitespace()
                    .find_map(|field| field.strip_prefix(RENEWAL_RETRY_BACKOFF_FIELD))
                    .and_then(|value| value.parse().ok())
                    .expect("a lease renewal retry logs its backoff");
                Duration::from_millis(milliseconds)
            })
            .collect()
    }

    /// Lets `slot`'s process exit by itself, as a test-made failure requires.
    pub(crate) fn permit_exit(&mut self, slot: usize) {
        self.slots[slot].may_exit = true;
    }

    /// Waits up to `budget` for `slot`'s process to exit after [`Self::permit_exit`].
    pub(crate) async fn wait_for_permitted_exit(
        &mut self,
        slot: usize,
        budget: Duration,
    ) -> ExitStatus {
        let entry = &mut self.slots[slot];
        assert!(
            entry.may_exit,
            "slot {} must permit the exit first",
            entry.name
        );
        let status = entry.running().wait_for_exit(budget).await;
        entry.process = None;
        self.permitted_stale_batch_stops += 1;
        status
    }

    /// Waits up to `budget` until some process serves and returns its slot.
    pub(crate) async fn wait_for_serving(&mut self, budget: Duration) -> usize {
        self.wait_for_serving_slot(None, budget).await
    }

    /// Waits up to `budget` until a process outside slot `previous` serves and returns its slot.
    pub(crate) async fn wait_for_takeover(&mut self, previous: usize, budget: Duration) -> usize {
        self.wait_for_serving_slot(Some(previous), budget).await
    }

    async fn wait_for_serving_slot(&mut self, excluded: Option<usize>, budget: Duration) -> usize {
        let deadline = Instant::now() + budget;
        loop {
            self.supervise();
            if let Some(slot) = (0..self.slots.len()).find(|slot| {
                Some(*slot) != excluded
                    && self.slots[*slot].process.is_some()
                    && self.router.is_serving(*slot)
            }) {
                return slot;
            }
            assert!(
                Instant::now() < deadline,
                "no process outside slot {excluded:?} served within {budget:?}"
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Waits until `slot`'s process answers NOT_SERVING to, or refuses, a later probe; timeouts never count.
    ///
    /// Returns how long the wait took, at most `budget`.
    pub(crate) async fn wait_for_not_serving(&mut self, slot: usize, budget: Duration) -> Duration {
        let started = Instant::now();
        while !self.router.has_stopped_serving_since(slot, started) {
            self.supervise();
            assert!(
                started.elapsed() < budget,
                "slot {} still served after {budget:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
        started.elapsed()
    }

    /// Waits up to `budget` until `slot`'s process is a standby: it runs and waits for the lease.
    pub(crate) async fn wait_for_standby(&mut self, slot: usize, budget: Duration) {
        let deadline = Instant::now() + budget;
        loop {
            self.supervise();
            let metrics_address = self.slots[slot].metrics_address;
            if self.slots[slot].process.is_some()
                && let Ok(metrics) = try_scrape(metrics_address, "/metrics").await
                && metrics.starts_with("HTTP/1.1 200")
                && sample_with_label(&metrics, "dexzd_node_role", "role", "standby") == 1.0
            {
                return;
            }
            assert!(
                Instant::now() < deadline,
                "slot {} did not wait for the lease within {budget:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Waits up to `budget` until `slot`'s process reports `dex.HotStandby` SERVING; returns the wait.
    pub(crate) async fn wait_for_hot_standby(&mut self, slot: usize, budget: Duration) -> Duration {
        let started = Instant::now();
        let channel = Channel::from_shared(format!("http://{}", self.slots[slot].address))
            .expect("the slot address is a valid URI")
            .connect_lazy();
        let mut health = HealthClient::new(channel);
        loop {
            self.supervise();
            let check = health.check(HealthCheckRequest {
                service: HOT_STANDBY_HEALTH_SERVICE_NAME.to_owned(),
            });
            if self.slots[slot].process.is_some()
                && let Ok(Ok(response)) = tokio::time::timeout(HEALTH_CHECK_TIMEOUT, check).await
                && response.get_ref().status == ServingStatus::Serving as i32
            {
                return started.elapsed();
            }
            assert!(
                started.elapsed() < budget,
                "slot {} did not become a hot standby within {budget:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Waits up to `budget` until `slot`'s process reports the `dexzd_node_role` `role`.
    pub(crate) async fn wait_for_role(&mut self, slot: usize, role: &str, budget: Duration) {
        let deadline = Instant::now() + budget;
        loop {
            self.supervise();
            if self
                .metric_sample(slot, "dexzd_node_role", "role", role)
                .await
                == Some(1.0)
            {
                return;
            }
            assert!(
                Instant::now() < deadline,
                "slot {} did not report role {role} within {budget:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Waits up to `budget` until `slot`'s process reports the hot standby `state`.
    pub(crate) async fn wait_for_hot_standby_state(
        &mut self,
        slot: usize,
        state: &str,
        budget: Duration,
    ) {
        let deadline = Instant::now() + budget;
        loop {
            self.supervise();
            if self
                .metric_sample(slot, "dexzd_hot_standby_state", "state", state)
                .await
                == Some(1.0)
            {
                return;
            }
            assert!(
                Instant::now() < deadline,
                "slot {} did not report hot standby state {state} within {budget:?}",
                self.slots[slot].name
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Returns a labelled sample of `slot`'s metrics, or `None` while its listener is down.
    async fn metric_sample(
        &self,
        slot: usize,
        name: &str,
        label: &str,
        value: &str,
    ) -> Option<f64> {
        let metrics = try_scrape(self.slots[slot].metrics_address, "/metrics")
            .await
            .ok()?;
        metrics
            .starts_with("HTTP/1.1 200")
            .then(|| sample_with_label(&metrics, name, label, value))
    }

    /// Asks the active process, through the router, to step down.
    pub(crate) async fn step_down_active(
        &self,
        expected_node_id: Option<String>,
        expected_node_epoch: Option<u64>,
    ) -> Result<StepDownActiveNodeResponse, tonic::Status> {
        AdminServiceClient::new(self.router_channel())
            .step_down_active_node(StepDownActiveNodeRequest {
                expected_node_id,
                expected_node_epoch,
            })
            .await
            .map(tonic::Response::into_inner)
    }

    /// Describes the active process of `slot` directly, without the router.
    pub(crate) async fn describe(
        &self,
        slot: usize,
    ) -> Result<DescribeActiveNodeResponse, tonic::Status> {
        let channel = Channel::from_shared(format!("http://{}", self.slots[slot].address))
            .expect("the slot address is a valid URI")
            .connect_lazy();
        AdminServiceClient::new(channel)
            .describe_active_node(DescribeActiveNodeRequest {})
            .await
            .map(tonic::Response::into_inner)
    }

    /// Waits until the load acknowledges `count` more calls and every counter one more increment.
    pub(crate) async fn wait_for_progress(&mut self, workload: &Workload, count: u64) {
        let target = workload.progress_target(count);
        let deadline = Instant::now() + PROGRESS_BUDGET;
        while !workload.has_reached(&target) {
            self.supervise();
            assert!(
                Instant::now() < deadline,
                "the load did not reach {target:?} within {PROGRESS_BUDGET:?}"
            );
            tokio::time::sleep(SUPERVISION_POLL_INTERVAL).await;
        }
    }

    /// Verifies every acknowledged write on the serving process.
    pub(crate) async fn verify(&mut self, workload: &Workload) -> VerificationReport {
        let slot = self.wait_for_serving(PROGRESS_BUDGET).await;
        let channel = Channel::from_shared(format!("http://{}", self.slots[slot].address))
            .expect("the slot address is a valid URI")
            .connect_lazy();
        workload.verify(channel, self.slots[slot].running()).await
    }

    /// Stops every process, standbys first, then checks the serving and lease invariants.
    pub(crate) async fn finish(mut self) -> GroupReport {
        let serving = self.router.serving_backend();
        let mut order: Vec<usize> = (0..self.slots.len())
            .filter(|slot| Some(*slot) != serving)
            .collect();
        order.extend(serving);
        for slot in order {
            if self.slots[slot].process.is_some() {
                self.signal(slot, ProcessSignal::Terminate).await;
            }
        }
        let Self {
            emulator,
            fleet: _fleet,
            slots,
            router,
            lease_history,
            stale_batch_restarts,
            is_log_complete,
            ..
        } = self;
        let serving = router.shutdown_and_check().await;
        let lease = lease_history.stop_and_check(is_log_complete).await;
        // Each serving period recovered under its own epoch; a late-batch stop may serve too briefly to see.
        assert!(
            (serving.serving_periods <= lease.acquisitions || !lease.is_log_complete)
                && lease.acquisitions <= serving.serving_periods + stale_batch_restarts,
            "{} EpochStarts in the log for {} observed serving periods and {stale_batch_restarts} late-batch restarts",
            lease.acquisitions,
            serving.serving_periods
        );
        let process_starts = slots.iter().map(|slot| slot.process_count).sum();
        for slot in slots {
            slot.object_store.shutdown().await;
        }
        emulator.shutdown().await;
        GroupReport {
            serving,
            lease,
            process_starts,
            stale_batch_restarts,
        }
    }

    /// Restarts a process that stopped on a late batch of a killed process; panics on any other exit.
    fn supervise(&mut self) {
        for slot in 0..self.slots.len() {
            let entry = &mut self.slots[slot];
            let Some(status) = entry
                .process
                .as_mut()
                .and_then(RobustnessProcess::exit_status)
            else {
                continue;
            };
            if entry.may_exit {
                continue;
            }
            let log = entry.running().log();
            assert!(
                self.permitted_stale_batch_stops > 0
                    && status.code() == Some(1)
                    && log.contains(STALE_BATCH_STOP_REASON),
                "{} process {} exited with {status}:\n{log}",
                entry.name,
                entry.process_count
            );
            eprintln!(
                "supervisor: {} process {} stopped on a late batch of an ended process; restarting it",
                entry.name, entry.process_count
            );
            entry.process = None;
            self.permitted_stale_batch_stops -= 1;
            self.stale_batch_restarts += 1;
            self.restart(slot, DirectoryFate::Keep);
        }
    }
}
