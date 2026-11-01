// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Failover tests of `serve` processes of one node ID, checking writes, serving, leases, and deadlines.

mod lease_epochs;
mod node_group;
mod object_store_endpoints;
mod readiness_router;

use std::os::unix::process::ExitStatusExt;
use std::process::ExitStatus;
use std::time::Duration;

use tempfile::TempDir;
use tokio::time::Instant;

use crate::robustness::workload::{LoadShape, ReconnectingWorker, RunningLoad, Workload};
use node_group::{DirectoryFate, NodeGroup, ProcessSignal, SlotSettings};
use object_store_endpoints::ObjectStoreFault;

const FIRST: usize = 0;
const SECOND: usize = 1;
/// `first` starts active with a lease that rides out host stalls; `second` keeps a longer one.
const SLOTS: [SlotSettings<'static>; 2] = [
    SlotSettings {
        name: "first",
        lease_duration: "6s",
    },
    SlotSettings {
        name: "second",
        lease_duration: "15s",
    },
];
const FIRST_LEASE_DURATION: Duration = Duration::from_secs(6);
const LOAD_SHAPE: LoadShape = LoadShape {
    chain_starters: 4,
    timer_starters: 2,
    call_timeout: Duration::from_secs(3),
    increment_resend_budget: Some(Duration::from_secs(30)),
};
const COUNTER_COUNT: usize = 3;
/// New acknowledged calls between failures, so each failure lands mid-load.
const ACKNOWLEDGEMENTS_PER_PHASE: u64 = 60;
const STARTUP_BUDGET: Duration = Duration::from_secs(30);
/// Bounds the wait; the lease check below asserts the actual takeover time.
const TAKEOVER_BUDGET: Duration = Duration::from_secs(30);
/// A cold standby reads a held lease at most once a second; the rest is margin for loaded hosts.
const COLD_TAKEOVER_AFTER_AVAILABILITY_BOUND: Duration = Duration::from_secs(3);
const STANDBY_BUDGET: Duration = Duration::from_secs(30);
/// A slow object store that the active still serves through.
const SLOW_OBJECT_STORE_LATENCY: Duration = Duration::from_millis(50);
/// The lease validity, the watchdog's 100 ms poll, and margin for loaded hosts.
const LOST_OBJECT_STORE_NOT_SERVING_BOUND: Duration =
    FIRST_LEASE_DURATION.saturating_add(Duration::from_secs(2));
/// The lease, then the watchdog's two seconds before it aborts the process.
const LOST_OBJECT_STORE_EXIT_BUDGET: Duration = Duration::from_secs(20);
/// How long the active keeps sending writes into the blackhole before it stalls.
const HELD_WRITES_WINDOW: Duration = Duration::from_millis(500);
const LATE_WRITES_BUDGET: Duration = Duration::from_secs(10);
/// Timer Flows started right before each failure, which must fire after it.
const SPANNING_TIMER_COUNT: usize = 2;
/// Longer than the start of a failure, so these timers fire after it began.
const SPANNING_TIMER_DURATION: Duration = Duration::from_secs(3);

/// P1, crash failover: SIGKILL of the active under load hands the node to the waiting cold standby.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn killed_active_under_load_hands_over_to_the_cold_standby() {
    let mut scenario = FailoverScenario::start("killed-active").await;
    let group = &mut scenario.group;
    // A standby paused under load resumes waiting and never serves beside the live active.
    group.signal(SECOND, ProcessSignal::Stop).await;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.signal(SECOND, ProcessSignal::Continue).await;
    group.wait_for_standby(SECOND, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;

    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(FIRST, ProcessSignal::Kill).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    let served_after = killed_at.elapsed();
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    // The killed process returns on its directory as the new standby.
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_standby(FIRST, STANDBY_BUDGET).await;
    scenario
        .finish(
            "killed_active_under_load_hands_over_to_the_cold_standby",
            killed_at,
            format!("served {served_after:?} after the kill"),
        )
        .await;
}

/// P6, double failure: the active and the standby die together; after both restart, one recovers.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn killing_active_and_standby_together_recovers_after_both_restart() {
    let mut scenario = FailoverScenario::start("both-killed").await;
    let group = &mut scenario.group;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;

    // The standby dies first, so it cannot take over in between.
    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(SECOND, ProcessSignal::Kill).await;
    group.signal(FIRST, ProcessSignal::Kill).await;
    // The active's container restarts on its directory; the standby's pod returns empty.
    group.restart(FIRST, DirectoryFate::Keep);
    group.restart(SECOND, DirectoryFate::Wipe);
    let serving = group.wait_for_serving(TAKEOVER_BUDGET).await;
    let standby = if serving == FIRST { SECOND } else { FIRST };
    group.wait_for_standby(standby, STANDBY_BUDGET).await;
    scenario
        .finish(
            "killing_active_and_standby_together_recovers_after_both_restart",
            killed_at,
            format!("slot {serving} recovered"),
        )
        .await;
}

/// P12, lost object storage: the active stops serving at its validity end; the cold standby takes over.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn active_that_loses_object_storage_hands_over_to_the_cold_standby() {
    let mut scenario = FailoverScenario::start("lost-storage").await;
    let group = &mut scenario.group;
    group
        .object_store(FIRST)
        .inject(ObjectStoreFault::Latency(SLOW_OBJECT_STORE_LATENCY))
        .await;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;

    group.permit_exit(FIRST);
    let lost_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group
        .object_store(FIRST)
        .inject(ObjectStoreFault::Blackhole)
        .await;
    let stopped_serving_after = group
        .wait_for_not_serving(FIRST, LOST_OBJECT_STORE_NOT_SERVING_BOUND)
        .await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    let served_after = lost_at.elapsed();
    let status = group
        .wait_for_permitted_exit(FIRST, LOST_OBJECT_STORE_EXIT_BUDGET)
        .await;
    assert_exited_after_losing_the_lease(status);
    group.heal_object_store(FIRST).await;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_standby(FIRST, STANDBY_BUDGET).await;
    scenario
        .finish(
            "active_that_loses_object_storage_hands_over_to_the_cold_standby",
            lost_at,
            format!(
                "stopped serving {stopped_serving_after:?} and the standby served {served_after:?} after the loss; old active ended with {status}"
            ),
        )
        .await;
}

/// P12, late writes: a stalled active's held writes land after the takeover and are fenced.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn late_writes_of_a_stalled_active_are_fenced_after_the_takeover() {
    let mut scenario = FailoverScenario::start("late-writes").await;
    let group = &mut scenario.group;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;

    let lost_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group
        .object_store(FIRST)
        .inject(ObjectStoreFault::Blackhole)
        .await;
    // The fault window under test: the load keeps the active writing into the blackhole.
    tokio::time::sleep(HELD_WRITES_WINDOW).await;
    // A frozen active keeps its connections, so its held writes stay pending at the endpoint.
    group.signal(FIRST, ProcessSignal::Stop).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.heal_object_store(FIRST).await;
    let late_writes = group.wait_for_held_puts(FIRST, LATE_WRITES_BUDGET).await;
    // A late write either loses its condition or lands as a batch the new epoch voids.
    assert!(
        late_writes
            .iter()
            .all(|(_, status)| *status == 200 || *status == 412),
        "{late_writes:?}"
    );
    // The process ends without resuming, so only its late writes reach the new active.
    group.signal(FIRST, ProcessSignal::Kill).await;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_standby(FIRST, STANDBY_BUDGET).await;
    scenario
        .finish(
            "late_writes_of_a_stalled_active_are_fenced_after_the_takeover",
            lost_at,
            format!("held writes that landed after the takeover: {late_writes:?}"),
        )
        .await;
}

/// The active `first` under load with counters, a reconnecting Worker, and `second` waiting.
struct FailoverScenario {
    group: NodeGroup,
    workload: Workload,
    worker: ReconnectingWorker,
    load: RunningLoad,
    _directory: TempDir,
}

impl FailoverScenario {
    async fn start(phase: &str) -> Self {
        let directory = TempDir::new().expect("a temporary test directory can be created");
        let workload = Workload::new();
        let mut group = NodeGroup::start(directory.path(), &workload, &SLOTS).await;
        group.spawn(FIRST);
        assert_eq!(group.wait_for_serving(STARTUP_BUDGET).await, FIRST);
        let worker = workload.start_reconnecting_worker(group.router_address());
        workload
            .start_counters(group.router_channel(), COUNTER_COUNT)
            .await;
        let load = workload.start_shaped_load(group.router_address(), phase, &LOAD_SHAPE);
        group.spawn(SECOND);
        group.wait_for_standby(SECOND, STANDBY_BUDGET).await;
        Self {
            group,
            workload,
            worker,
            load,
            _directory: directory,
        }
    }

    /// Starts timer Flows that must fire after the failure that follows; returns the failure instant.
    async fn start_timers_spanning_the_failure(&self) -> Instant {
        self.workload
            .start_timers_spanning_a_failure(
                self.group.router_channel(),
                SPANNING_TIMER_COUNT,
                SPANNING_TIMER_DURATION,
            )
            .await;
        Instant::now()
    }

    /// Adds load after the recovery, verifies the ledger, stops the group, and checks the takeover.
    async fn finish(self, test: &str, failed_at: Instant, details: String) {
        // The directory stays in `self`, so a panic dumps process logs before it is deleted.
        let Self {
            mut group,
            workload,
            worker,
            load,
            ..
        } = self;
        group
            .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
            .await;
        load.stop().await;
        let verification = group.verify(&workload).await;
        let worker_registrations = worker.stop().await;
        let timers_across_the_failure = workload.timer_flows_spanning(failed_at);
        let report = group.finish().await;
        assert!(
            timers_across_the_failure >= SPANNING_TIMER_COUNT,
            "only {timers_across_the_failure} timer Flows were started before the failure and fired after it"
        );
        assert!(report.serving_periods() >= 2, "{report}");
        assert!(report.observed_lease_holders() >= 2, "{report}");
        assert!(
            report.longest_takeover_after_availability() <= COLD_TAKEOVER_AFTER_AVAILABILITY_BOUND,
            "{report}"
        );
        eprintln!(
            "{test}: {details}; {timers_across_the_failure} timer Flows across the failure; {worker_registrations}; {verification}; {report}"
        );
    }
}

/// Panics unless an active that lost its lease exited 1 or the watchdog aborted it.
fn assert_exited_after_losing_the_lease(status: ExitStatus) {
    assert!(
        status.code() == Some(1) || status.signal() == Some(libc::SIGABRT),
        "an active without its lease exits 1 or aborts, got {status}"
    );
}
