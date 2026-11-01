// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Failover tests of several `serve` processes of one node ID under load.
//!
//! Every test checks that no acknowledged write is lost and at most one process served at once.
//!
//! It also checks lease epochs and that a process serves and a standby waits in time.

mod lease_epochs;
mod node_group;
mod object_store_endpoints;
mod readiness_router;

use std::os::unix::process::ExitStatusExt;
use std::time::Duration;

use tempfile::TempDir;
use tokio::time::Instant;

use crate::robustness::workload::{LoadShape, Workload};
use node_group::{DirectoryFate, NodeGroup, ProcessSignal, SlotSettings};
use object_store_endpoints::ObjectStoreFault;

const FIRST: usize = 0;
const SECOND: usize = 1;
/// The first slot starts as the active; its short lease makes a failover take about four seconds.
///
/// The second slot's long lease survives host stalls once it took over.
const SLOTS: [SlotSettings<'static>; 2] = [
    SlotSettings {
        name: "first",
        lease_duration: "3s",
    },
    SlotSettings {
        name: "second",
        lease_duration: "15s",
    },
];
const LOAD_SHAPE: LoadShape = LoadShape {
    chain_starters: 4,
    timer_starters: 2,
    call_timeout: Duration::from_secs(3),
    unavailable_retry_budget: Some(Duration::from_secs(20)),
};
const COUNTER_COUNT: usize = 3;
/// New acknowledged calls between failures, so each failure lands mid-load.
const ACKNOWLEDGEMENTS_PER_PHASE: u64 = 60;
const STARTUP_BUDGET: Duration = Duration::from_secs(30);
/// The killed active's lease, the takeover grace, a recovery, and one restart after a late batch.
const TAKEOVER_BUDGET: Duration = Duration::from_secs(30);
const STANDBY_BUDGET: Duration = Duration::from_secs(30);
/// A slow object store that the active still serves through.
const SLOW_OBJECT_STORE_LATENCY: Duration = Duration::from_millis(50);
/// The lease, then the watchdog's two seconds before it aborts the process.
const LOST_OBJECT_STORE_EXIT_BUDGET: Duration = Duration::from_secs(20);
const SIGABRT: i32 = 6;

/// P1 with a cold standby: SIGKILL of the active under load hands the node to the waiting standby.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn killed_active_under_load_hands_over_to_the_cold_standby() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let workload = Workload::new();
    let mut group = NodeGroup::start(directory.path(), &workload, &SLOTS).await;
    group.spawn(FIRST);
    assert_eq!(group.wait_for_serving(STARTUP_BUDGET).await, FIRST);
    let worker = workload.start_reconnecting_worker(group.router_address());
    workload
        .start_counters(group.router_channel(), COUNTER_COUNT)
        .await;
    let load = workload.start_shaped_load(group.router_address(), "killed-active", &LOAD_SHAPE);
    group.spawn(SECOND);
    group.wait_for_standby(SECOND, STANDBY_BUDGET).await;
    // A standby paused under load resumes waiting and never serves beside the live active.
    group.signal(SECOND, ProcessSignal::Stop).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.signal(SECOND, ProcessSignal::Continue).await;
    group.wait_for_standby(SECOND, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;

    let killed_at = Instant::now();
    group.signal(FIRST, ProcessSignal::Kill).await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    let takeover = killed_at.elapsed();
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    // The killed process returns on its directory as the new standby.
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_standby(FIRST, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    load.stop().await;

    let verification = group.verify(&workload).await;
    let worker_registrations = worker.stop().await;
    let report = group.finish().await;
    eprintln!(
        "killed_active_under_load_hands_over_to_the_cold_standby: served {takeover:?} after the kill; {worker_registrations} Worker registrations; {verification}; {report}"
    );
}

/// P6: the active and the standby die together; after both restart, one recovers the node.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn killing_active_and_standby_together_recovers_after_both_restart() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let workload = Workload::new();
    let mut group = NodeGroup::start(directory.path(), &workload, &SLOTS).await;
    group.spawn(FIRST);
    assert_eq!(group.wait_for_serving(STARTUP_BUDGET).await, FIRST);
    let worker = workload.start_reconnecting_worker(group.router_address());
    workload
        .start_counters(group.router_channel(), COUNTER_COUNT)
        .await;
    let load = workload.start_shaped_load(group.router_address(), "both-killed", &LOAD_SHAPE);
    group.spawn(SECOND);
    group.wait_for_standby(SECOND, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;

    // The standby dies first, so it cannot take over in between.
    group.signal(SECOND, ProcessSignal::Kill).await;
    group.signal(FIRST, ProcessSignal::Kill).await;
    // The active's container restarts on its directory; the standby's pod returns empty.
    group.restart(FIRST, DirectoryFate::Keep);
    group.restart(SECOND, DirectoryFate::Wipe);
    let serving = group.wait_for_serving(TAKEOVER_BUDGET).await;
    let standby = if serving == FIRST { SECOND } else { FIRST };
    group.wait_for_standby(standby, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    load.stop().await;

    let verification = group.verify(&workload).await;
    let worker_registrations = worker.stop().await;
    let report = group.finish().await;
    eprintln!(
        "killing_active_and_standby_together_recovers_after_both_restart: slot {serving} recovered; {worker_registrations} Worker registrations; {verification}; {report}"
    );
}

/// P12 with a cold standby: the active alone loses object storage, and the standby takes over.
///
/// The held writes land late after access returns.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn active_that_loses_object_storage_hands_over_to_the_cold_standby() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let workload = Workload::new();
    let mut group = NodeGroup::start(directory.path(), &workload, &SLOTS).await;
    group.spawn(FIRST);
    assert_eq!(group.wait_for_serving(STARTUP_BUDGET).await, FIRST);
    let worker = workload.start_reconnecting_worker(group.router_address());
    workload
        .start_counters(group.router_channel(), COUNTER_COUNT)
        .await;
    let load = workload.start_shaped_load(group.router_address(), "lost-storage", &LOAD_SHAPE);
    group.spawn(SECOND);
    group.wait_for_standby(SECOND, STANDBY_BUDGET).await;
    group
        .object_store(FIRST)
        .inject(ObjectStoreFault::Latency(SLOW_OBJECT_STORE_LATENCY))
        .await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;

    group.permit_exit(FIRST);
    let lost_at = Instant::now();
    group
        .object_store(FIRST)
        .inject(ObjectStoreFault::Blackhole)
        .await;
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    let takeover = lost_at.elapsed();
    let status = group
        .wait_for_permitted_exit(FIRST, LOST_OBJECT_STORE_EXIT_BUDGET)
        .await;
    assert!(
        status.code() == Some(1) || status.signal() == Some(SIGABRT),
        "the active without object storage exits 1 or aborts, got {status}"
    );
    // The held requests now run, like writes delivered after a partition heals.
    group.object_store(FIRST).clear_faults().await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_standby(FIRST, STANDBY_BUDGET).await;
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    load.stop().await;

    let verification = group.verify(&workload).await;
    let worker_registrations = worker.stop().await;
    let report = group.finish().await;
    eprintln!(
        "active_that_loses_object_storage_hands_over_to_the_cold_standby: served {takeover:?} after the loss; old active ended with {status}; {worker_registrations} Worker registrations; {verification}; {report}"
    );
}
