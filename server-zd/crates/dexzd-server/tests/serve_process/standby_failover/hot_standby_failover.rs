// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Failover tests with `standby.mode: hot`, numbered after the failure matrix of the hot standby plan.

use std::time::Duration;

use tempfile::TempDir;
use tokio::time::Instant;

use super::node_group::{
    DirectoryFate, LOG_DELETION_DELAY, NodeGroup, ProcessSignal, SNAPSHOT_INTERVAL, SlotSettings,
    SlotStandbyMode,
};
use super::object_store_endpoints::ObjectStoreFault;
use super::{
    ACKNOWLEDGEMENTS_PER_PHASE, FIRST, FailoverScenario, HOT_SLOTS, SECOND, STANDBY_BUDGET,
    STARTUP_BUDGET, TAKEOVER_BUDGET, assert_hot_takeover,
};
use crate::robustness::workload::Workload;

const THIRD: usize = 2;
/// Slows each object storage request of one process, so a bootstrap or takeover lasts seconds.
const SLOW_STANDBY_LATENCY: Duration = Duration::from_millis(400);
/// How long a fault burst lasts at a standby's endpoint.
const FAULT_BURST: Duration = Duration::from_secs(3);
/// Holds a process's log reads until the fault is cleared; s3mock accepts no longer delay.
const HELD_LOG_READS: Duration = Duration::from_secs(600);
/// The default 10 s `behindLagThreshold` and time for a loaded host to report it.
const BEHIND_BUDGET: Duration = Duration::from_secs(60);
/// What a lease waiter logs before it defers to a readier process, with the deferral.
const LEASE_DEFERRAL_MARKER: &str =
    "delaying the node lease acquisition so a readier process can take it first";
const LEASE_DEFERRAL_FIELD: &str = "delay_milliseconds=";
/// The default 1.5 s lagging takeover delay less its 20% jitter, rounded down.
const LAGGING_DEFERRAL_FLOOR: Duration = Duration::from_millis(1000);
const DEFERRAL_LOG_BUDGET: Duration = Duration::from_secs(5);
/// A graceful stop of a loaded active and a hot takeover after its release.
const SERVED_AFTER_SIGTERM_BOUND: Duration = Duration::from_secs(5);
/// Snapshots and log cleanup run every second, so cleanup soon passes a stopped standby.
const FAST_SNAPSHOT_INTERVAL: &str = "1s";
const FAST_LOG_DELETION_DELAY: &str = "1s";
const LOG_CLEANUP_BUDGET: Duration = Duration::from_secs(60);

/// P1, hot: SIGKILL of the active under load hands the node to the hot standby.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn hot_standby_takes_over_when_the_active_is_killed_under_load() {
    let mut scenario = FailoverScenario::start("hot-killed", &HOT_SLOTS).await;
    scenario
        .group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(FIRST, ProcessSignal::Kill).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    assert_hot_takeover(group, SECOND);
    let served_after = killed_at.elapsed();
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.restart(FIRST, DirectoryFate::Keep);
    let hot_after = group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    scenario
        .finish(
            "hot_standby_takes_over_when_the_active_is_killed_under_load",
            killed_at,
            format!("served {served_after:?} after the kill; the restarted process was hot after {hot_after:?}"),
        )
        .await;
}

/// P2, hot: SIGTERM of the active releases the lease, and the hot standby takes it at once.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn hot_standby_takes_over_when_the_active_is_terminated() {
    let mut scenario = FailoverScenario::start("hot-terminated", &HOT_SLOTS).await;
    scenario
        .group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let terminated_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(FIRST, ProcessSignal::Terminate).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    assert_hot_takeover(group, SECOND);
    let served_after = terminated_at.elapsed();
    assert!(
        served_after <= SERVED_AFTER_SIGTERM_BOUND,
        "the hot standby served {served_after:?} after SIGTERM"
    );
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    scenario
        .finish(
            "hot_standby_takes_over_when_the_active_is_terminated",
            terminated_at,
            format!("served {served_after:?} after SIGTERM"),
        )
        .await;
}

/// P4, hot: a standby killed while following, and again while bootstrapping, becomes hot again.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn killed_hot_standby_restarts_hot_while_following_and_while_bootstrapping() {
    let mut scenario = FailoverScenario::start("hot-standby-killed", &HOT_SLOTS).await;
    let group = &mut scenario.group;
    group.signal(SECOND, ProcessSignal::Kill).await;
    group.restart(SECOND, DirectoryFate::Keep);
    let hot_after_following_kill = group.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    // A slow endpoint keeps a standby with an empty directory bootstrapping for seconds.
    group.signal(SECOND, ProcessSignal::Kill).await;
    group
        .object_store(SECOND)
        .inject(ObjectStoreFault::Latency(SLOW_STANDBY_LATENCY))
        .await;
    group.restart(SECOND, DirectoryFate::Wipe);
    group
        .wait_for_hot_standby_state(SECOND, "bootstrapping", STANDBY_BUDGET)
        .await;
    group.signal(SECOND, ProcessSignal::Kill).await;
    group.heal_object_store(SECOND).await;
    group.restart(SECOND, DirectoryFate::Keep);
    let hot_after_bootstrap_kill = group.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await;
    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(FIRST, ProcessSignal::Kill).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    scenario
        .finish(
            "killed_hot_standby_restarts_hot_while_following_and_while_bootstrapping",
            killed_at,
            format!(
                "hot {hot_after_following_kill:?} after a kill while following and {hot_after_bootstrap_kill:?} after a kill while bootstrapping"
            ),
        )
        .await;
}

/// P5, hot: the standby dies after it took the lease and before it served; a restart recovers.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn standby_killed_during_its_takeover_is_replaced_by_a_restart() {
    let mut scenario = FailoverScenario::start("killed-in-takeover", &HOT_SLOTS).await;
    scenario
        .group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    // A slow endpoint stretches the takeover, so the kill lands between the lease and serving.
    group
        .object_store(SECOND)
        .inject(ObjectStoreFault::Latency(SLOW_STANDBY_LATENCY))
        .await;
    group.signal(FIRST, ProcessSignal::Kill).await;
    group
        .wait_for_role(SECOND, "recovering", TAKEOVER_BUDGET)
        .await;
    group.signal(SECOND, ProcessSignal::Kill).await;
    group.heal_object_store(SECOND).await;
    group.restart(FIRST, DirectoryFate::Keep);
    group.restart(SECOND, DirectoryFate::Keep);
    let serving = group.wait_for_serving(TAKEOVER_BUDGET).await;
    let standby = if serving == FIRST { SECOND } else { FIRST };
    group.wait_for_hot_standby(standby, STANDBY_BUDGET).await;
    scenario
        .finish(
            "standby_killed_during_its_takeover_is_replaced_by_a_restart",
            killed_at,
            format!("slot {serving} recovered after the takeover was killed"),
        )
        .await;
}

/// P6, hot: the active and the hot standby die together; after both restart, one recovers.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn killing_active_and_hot_standby_together_recovers_after_both_restart() {
    let mut scenario = FailoverScenario::start("hot-both-killed", &HOT_SLOTS).await;
    scenario
        .group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(SECOND, ProcessSignal::Kill).await;
    group.signal(FIRST, ProcessSignal::Kill).await;
    group.restart(FIRST, DirectoryFate::Keep);
    group.restart(SECOND, DirectoryFate::Wipe);
    let serving = group.wait_for_serving(TAKEOVER_BUDGET).await;
    let standby = if serving == FIRST { SECOND } else { FIRST };
    group.wait_for_hot_standby(standby, STANDBY_BUDGET).await;
    scenario
        .finish(
            "killing_active_and_hot_standby_together_recovers_after_both_restart",
            killed_at,
            format!("slot {serving} recovered"),
        )
        .await;
}

/// P8, hot: 5xx bursts and timeouts at the standby's object storage leave it hot afterwards.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn hot_standby_recovers_from_object_storage_faults_and_still_takes_over() {
    let mut scenario = FailoverScenario::start("standby-faults", &HOT_SLOTS).await;
    let group = &mut scenario.group;
    let mut recoveries = Vec::new();
    for fault in [
        ObjectStoreFault::ServiceUnavailable { probability: 1.0 },
        ObjectStoreFault::Timeouts,
        ObjectStoreFault::ServiceUnavailable { probability: 0.5 },
    ] {
        group.object_store(SECOND).inject(fault).await;
        // The fault window under test.
        tokio::time::sleep(FAULT_BURST).await;
        group.heal_object_store(SECOND).await;
        recoveries.push(group.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await);
        group
            .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
            .await;
    }
    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(FIRST, ProcessSignal::Kill).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    assert_hot_takeover(group, SECOND);
    scenario
        .finish(
            "hot_standby_recovers_from_object_storage_faults_and_still_takes_over",
            killed_at,
            format!("hot again after each fault burst: {recoveries:?}"),
        )
        .await;
}

/// P10, hot: with one hot standby and one that lags behind, the hot one takes over.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn hot_standby_wins_the_takeover_over_a_lagging_one() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let workload = Workload::new();
    let slots = three_hot_slots(SNAPSHOT_INTERVAL, LOG_DELETION_DELAY);
    let mut group = NodeGroup::start(directory.path(), &workload, &slots).await;
    group.spawn(FIRST);
    assert_eq!(group.wait_for_serving(STARTUP_BUDGET).await, FIRST);
    let worker = workload.start_reconnecting_worker(group.router_address());
    workload
        .start_counters(group.router_channel(), super::COUNTER_COUNT)
        .await;
    let load =
        workload.start_shaped_load(group.router_address(), "three-slots", &super::LOAD_SHAPE);
    group.spawn(SECOND);
    group.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    // Held log reads make it fall behind, while its lease reads and writes run at full speed.
    group
        .object_store(THIRD)
        .inject(ObjectStoreFault::LogLatency(HELD_LOG_READS))
        .await;
    group.spawn(THIRD);
    group
        .wait_for_hot_standby_state(THIRD, "behind", BEHIND_BUDGET)
        .await;
    group.signal(FIRST, ProcessSignal::Kill).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    assert_hot_takeover(&mut group, SECOND);
    let deferral = wait_for_lease_deferral(&mut group, THIRD).await;
    assert!(
        deferral >= LAGGING_DEFERRAL_FLOOR,
        "a standby that is not hot must defer by the lagging takeover delay, deferred {deferral:?}"
    );
    group.heal_object_store(THIRD).await;
    group.wait_for_hot_standby(THIRD, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    load.stop().await;
    let verification = group.verify(&workload).await;
    let worker_registrations = worker.stop().await;
    let report = group.finish().await;
    assert!(report.observed_lease_holders() >= 2, "{report}");
    eprintln!(
        "hot_standby_wins_the_takeover_over_a_lagging_one: the lagging standby deferred {deferral:?}; {worker_registrations}; {verification}; {report}"
    );
}

/// Waits until `slot`'s process logs a lease deferral; returns the longest it logged.
async fn wait_for_lease_deferral(group: &mut NodeGroup, slot: usize) -> Duration {
    let started = Instant::now();
    loop {
        let longest = group
            .process_log(slot)
            .lines()
            .filter(|line| line.contains(LEASE_DEFERRAL_MARKER))
            .filter_map(|line| {
                line.split_whitespace()
                    .find_map(|field| field.strip_prefix(LEASE_DEFERRAL_FIELD))
                    .and_then(|value| value.parse().ok())
            })
            .max()
            .map(Duration::from_millis);
        if let Some(longest) = longest {
            return longest;
        }
        assert!(
            started.elapsed() < DEFERRAL_LOG_BUDGET,
            "slot {slot} logged no lease deferral within {DEFERRAL_LOG_BUDGET:?}"
        );
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
}

/// P11, hot: log cleanup passes a stopped standby; on restart it bootstraps again and takes over.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn standby_restarted_after_log_cleanup_passed_it_bootstraps_again_and_takes_over() {
    let slots = fast_cleanup_hot_slots();
    let mut scenario = FailoverScenario::start("cleanup-passed", &slots).await;
    let group = &mut scenario.group;
    group.signal(SECOND, ProcessSignal::Kill).await;
    let (_, newest_seq_at_kill) = group.log_seq_range().await;
    let cleanup_started = Instant::now();
    while group.log_seq_range().await.0 <= newest_seq_at_kill {
        group
            .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
            .await;
        assert!(
            cleanup_started.elapsed() < LOG_CLEANUP_BUDGET,
            "log cleanup did not pass seq {newest_seq_at_kill} within {LOG_CLEANUP_BUDGET:?}"
        );
    }
    group.restart(SECOND, DirectoryFate::Keep);
    let hot_after = group.wait_for_hot_standby(SECOND, STANDBY_BUDGET).await;
    let killed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.signal(FIRST, ProcessSignal::Kill).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    scenario
        .finish(
            "standby_restarted_after_log_cleanup_passed_it_bootstraps_again_and_takes_over",
            killed_at,
            format!("the restarted standby was hot after {hot_after:?}"),
        )
        .await;
}

/// P12, hot: a stalled active's held writes land after the hot standby took over, and are fenced.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn late_writes_of_a_stalled_active_are_fenced_after_a_hot_takeover() {
    let mut scenario = FailoverScenario::start("hot-late-writes", &HOT_SLOTS).await;
    scenario
        .group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let lost_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    // The fault window under test: the load keeps the active writing into the blackhole.
    group
        .blackhole_and_freeze_during_a_lease_renewal(FIRST, super::RENEWAL_FREEZE_BUDGET)
        .await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    assert_hot_takeover(group, SECOND);
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.heal_object_store(FIRST).await;
    let late_writes = group
        .wait_for_held_puts(FIRST, &super::lease_key_prefix(), super::LATE_WRITES_BUDGET)
        .await;
    super::assert_late_writes_were_fenced(&late_writes);
    group.signal(FIRST, ProcessSignal::Kill).await;
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    scenario
        .finish(
            "late_writes_of_a_stalled_active_are_fenced_after_a_hot_takeover",
            lost_at,
            format!("held writes that landed after the takeover: {late_writes:?}"),
        )
        .await;
}

fn three_hot_slots(
    snapshot_interval: &'static str,
    log_deletion_delay: &'static str,
) -> [SlotSettings<'static>; 3] {
    ["first", "second", "third"].map(|name| SlotSettings {
        name,
        lease_duration: if name == "first" { "6s" } else { "15s" },
        standby_mode: SlotStandbyMode::Hot,
        snapshot_interval,
        log_deletion_delay,
    })
}

fn fast_cleanup_hot_slots() -> [SlotSettings<'static>; 2] {
    let [first, second, _] = three_hot_slots(FAST_SNAPSHOT_INTERVAL, FAST_LOG_DELETION_DELAY);
    [first, second]
}
