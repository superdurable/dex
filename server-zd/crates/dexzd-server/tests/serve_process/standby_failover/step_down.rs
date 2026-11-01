// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! AdminService step down and describe against serve processes, numbered after the plan's P7 and P9.

use std::time::Duration;

use dexzd_protocol::dex::DescribeActiveNodeRequest;
use dexzd_protocol::dex::StepDownActiveNodeResponse;
use dexzd_protocol::dex::admin_service_client::AdminServiceClient;
use dexzd_protocol::dex::step_down_active_node_response::LeaseHolderAfterStepDown;
use s3mock_server::SignatureVerification;
use tempfile::TempDir;
use tokio::time::Instant;
use tonic::Code;
use tonic::metadata::MetadataValue;
use tonic::transport::Channel;

use super::node_group::{DirectoryFate, NodeGroup, ProcessSignal};
use super::object_store_endpoints::ObjectStoreFault;
use super::{
    ACKNOWLEDGEMENTS_PER_PHASE, FIRST, FailoverScenario, HOT_SLOTS, SECOND, STANDBY_BUDGET,
    STARTUP_BUDGET, TAKEOVER_BUDGET, assert_hot_takeover,
};
use crate::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use crate::robustness::workload::Workload;
use crate::{PRIMARY_BUCKET, SHUTDOWN_BUDGET};

/// The default `standby.stepDownHoldOff`.
const STEP_DOWN_HOLD_OFF: Duration = Duration::from_secs(5);
const SWITCHOVER_COUNT: usize = 10;
/// A standby whose every object storage request takes this long falls behind under load.
const BEHIND_STANDBY_LATENCY: Duration = Duration::from_millis(1500);
/// The 1.5 s lagging delay with 20% jitter, a lease read and write, and a loaded host.
const BEHIND_TAKEOVER_AFTER_RELEASE_BOUND: Duration = Duration::from_secs(3);
/// A hot standby polls a held lease every 250 ms; a 1 s poll fails this in most runs.
const STEP_DOWN_HANDOVER_BOUND: Duration = Duration::from_millis(600);
/// The lease history samples every 100 ms, so its first released sample may trail the release.
const RELEASE_SAMPLE_LAG: Duration = Duration::from_millis(250);
/// A take-back after the hold-off: one lease read and write, and a loaded host.
const TAKE_BACK_AFTER_HOLD_OFF_BOUND: Duration = Duration::from_millis(1500);
/// The default `standby.behindLagThreshold` and the time to fall that far behind.
const BEHIND_BUDGET: Duration = Duration::from_secs(60);
/// Lets the step down call reach the active before the crash.
const STEP_DOWN_SEND_WINDOW: Duration = Duration::from_millis(20);
const ADMIN_TOKEN: &str = "serve-test-static-token";

/// P9, hot: a step down hands over, the old active stays hot, and describe and the epoch precondition work.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_hands_over_to_the_hot_standby_and_the_old_active_stays_hot() {
    let mut scenario = FailoverScenario::start("step-down", &HOT_SLOTS).await;
    let group = &mut scenario.group;
    group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let active = group
        .describe(FIRST)
        .await
        .expect("the active describes itself");
    let standby_refusal = group
        .describe(SECOND)
        .await
        .expect_err("a standby is not the active");
    assert_eq!(
        standby_refusal.code(),
        Code::FailedPrecondition,
        "{standby_refusal}"
    );
    let other_node = group
        .step_down_active(Some(format!("{}-other", active.node_id)), None)
        .await
        .expect_err("a step down for another node ID changes nothing");
    assert_eq!(other_node.code(), Code::FailedPrecondition, "{other_node}");
    let process_starts = group.process_starts(FIRST);
    let stepped_down_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    // dexcli's default flow: describe first, then step down only that node lease.
    let outcome = group
        .step_down_active(Some(active.node_id.clone()), Some(active.node_epoch))
        .await
        .expect("the active steps down");
    assert_eq!(outcome.released_node_epoch, active.node_epoch);
    let successor = match &outcome.lease_holder_after_step_down {
        Some(LeaseHolderAfterStepDown::TakenOverByAnotherProcess(holder)) => holder.clone(),
        other => panic!("the hot standby must take over, got {other:?}"),
    };
    assert!(successor.node_epoch > active.node_epoch, "{outcome:?}");
    assert_ne!(successor.holder_process_id, active.holder_process_id);
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    assert_hot_takeover(group, SECOND);
    let answered_after = stepped_down_at.elapsed();
    assert!(
        answered_after < STEP_DOWN_HOLD_OFF,
        "a hot standby must take over before the hold-off ends: {answered_after:?}"
    );
    group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    assert_eq!(
        group.process_starts(FIRST),
        process_starts,
        "the stepped-down process stays the same process"
    );
    let new_active = group
        .describe(SECOND)
        .await
        .expect("the new active describes itself");
    assert_eq!(new_active.node_epoch, successor.node_epoch);
    assert_eq!(new_active.holder_process_id, successor.holder_process_id);
    // A retry with the old precondition must not step down the new active.
    let mismatch = group
        .step_down_active(Some(active.node_id.clone()), Some(active.node_epoch))
        .await
        .expect_err("a stale epoch precondition changes nothing");
    assert_eq!(mismatch.code(), Code::FailedPrecondition, "{mismatch}");
    assert_eq!(group.wait_for_serving(TAKEOVER_BUDGET).await, SECOND);
    let unchanged = group
        .describe(SECOND)
        .await
        .expect("the new active still describes itself");
    assert_eq!(
        unchanged, new_active,
        "the refused step down changed nothing"
    );
    scenario
        .finish(
            "step_down_hands_over_to_the_hot_standby_and_the_old_active_stays_hot",
            stepped_down_at,
            format!(
                "answered {answered_after:?} after the call: {}",
                describe_outcome(&outcome)
            ),
        )
        .await;
}

/// P9, no standby: the stepped-down process takes the lease back after the hold-off.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_without_a_standby_takes_the_lease_back_after_the_hold_off() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let workload = Workload::new();
    let [first, _] = HOT_SLOTS;
    let mut group = NodeGroup::start(directory.path(), &workload, &[first]).await;
    group.spawn(FIRST);
    assert_eq!(group.wait_for_serving(STARTUP_BUDGET).await, FIRST);
    let worker = workload.start_reconnecting_worker(group.router_address());
    workload
        .start_counters(group.router_channel(), super::COUNTER_COUNT)
        .await;
    let load = workload.start_shaped_load(group.router_address(), "taken-back", &super::LOAD_SHAPE);
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let stepped_down_at = Instant::now();
    let outcome = group
        .step_down_active(None, None)
        .await
        .expect("the active steps down");
    let answered_after = stepped_down_at.elapsed();
    let holder = match &outcome.lease_holder_after_step_down {
        Some(LeaseHolderAfterStepDown::TakenBackByThisProcess(holder)) => holder.clone(),
        other => panic!("without a standby the process takes the lease back, got {other:?}"),
    };
    assert!(
        holder.node_epoch > outcome.released_node_epoch,
        "{outcome:?}"
    );
    assert!(answered_after >= STEP_DOWN_HOLD_OFF, "{answered_after:?}");
    assert_eq!(group.wait_for_serving(TAKEOVER_BUDGET).await, FIRST);
    group
        .wait_for_progress(&workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    load.stop().await;
    let verification = group.verify(&workload).await;
    let worker_registrations = worker.stop().await;
    let report = group.finish().await;
    assert!(report.serving_periods() >= 2, "{report}");
    let take_back_window = STEP_DOWN_HOLD_OFF - RELEASE_SAMPLE_LAG
        ..=STEP_DOWN_HOLD_OFF + TAKE_BACK_AFTER_HOLD_OFF_BOUND;
    assert!(
        !report.takeovers_after_release().is_empty()
            && report
                .takeovers_after_release()
                .iter()
                .all(|delay| take_back_window.contains(delay)),
        "the take-back must follow the release by the hold-off: {report}"
    );
    eprintln!(
        "step_down_without_a_standby_takes_the_lease_back_after_the_hold_off: answered {answered_after:?} after the call: {}; {worker_registrations}; {verification}; {report}",
        describe_outcome(&outcome)
    );
}

/// P9, racing a crash: a step down and a simultaneous SIGKILL of the active still hand over.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_racing_an_active_crash_hands_over_to_the_hot_standby() {
    let mut scenario = FailoverScenario::start("step-down-crash", &HOT_SLOTS).await;
    scenario
        .group
        .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
        .await;
    let crashed_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.note_possible_release();
    let step_down = tokio::spawn(step_down_through(group.router_channel()));
    tokio::time::sleep(STEP_DOWN_SEND_WINDOW).await;
    group.signal(FIRST, ProcessSignal::Kill).await;
    let step_down_result = step_down.await.expect("the step down task does not panic");
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    group.restart(FIRST, DirectoryFate::Keep);
    group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    let result = match &step_down_result {
        Ok(outcome) => describe_outcome(outcome),
        Err(status) => format!("the call failed with {}", status.code()),
    };
    scenario
        .finish(
            "step_down_racing_an_active_crash_hands_over_to_the_hot_standby",
            crashed_at,
            result,
        )
        .await;
}

/// P9, behind: a standby that lags beyond its threshold still takes over after its delay.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_hands_over_to_a_standby_that_is_behind() {
    let mut scenario = FailoverScenario::start("step-down-behind", &HOT_SLOTS).await;
    scenario
        .expect_takeovers_after_release_within(Duration::ZERO, BEHIND_TAKEOVER_AFTER_RELEASE_BOUND);
    let group = &mut scenario.group;
    group
        .object_store(SECOND)
        .inject(ObjectStoreFault::LogLatency(BEHIND_STANDBY_LATENCY))
        .await;
    group
        .wait_for_hot_standby_state(SECOND, "behind", BEHIND_BUDGET)
        .await;
    let stepped_down_at = scenario.start_timers_spanning_the_failure().await;
    let group = &mut scenario.group;
    group.note_possible_release();
    let step_down = tokio::spawn(step_down_through(group.router_channel()));
    // The behind standby writes the lease after its delay; then its reads may speed up.
    group
        .wait_for_role(SECOND, "recovering", TAKEOVER_BUDGET)
        .await;
    group.heal_object_store(SECOND).await;
    let outcome = step_down
        .await
        .expect("the step down task does not panic")
        .expect("the active steps down");
    assert!(
        matches!(
            outcome.lease_holder_after_step_down,
            Some(LeaseHolderAfterStepDown::TakenOverByAnotherProcess(_))
        ),
        "{outcome:?}"
    );
    assert_eq!(
        group.wait_for_takeover(FIRST, TAKEOVER_BUDGET).await,
        SECOND
    );
    group.wait_for_hot_standby(FIRST, STANDBY_BUDGET).await;
    scenario
        .finish(
            "step_down_hands_over_to_a_standby_that_is_behind",
            stepped_down_at,
            describe_outcome(&outcome),
        )
        .await;
}

/// P7: ten switchovers under load, alternating step downs and SIGTERM restarts, lose no write.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn ten_switchovers_under_load_lose_no_acknowledged_write() {
    let mut scenario = FailoverScenario::start("switchovers", &HOT_SLOTS).await;
    // After a SIGTERM the successor may still wait out its own step down hold-off.
    scenario.expect_takeovers_after_release_within(
        Duration::ZERO,
        STEP_DOWN_HOLD_OFF + super::HOT_TAKEOVER_AFTER_RELEASE_BOUND,
    );
    let mut switchovers = Vec::new();
    let mut first_failure = None;
    for round in 0..SWITCHOVER_COUNT {
        let failed_at = scenario.start_timers_spanning_the_failure().await;
        first_failure.get_or_insert(failed_at);
        let group = &mut scenario.group;
        let active = group.wait_for_serving(TAKEOVER_BUDGET).await;
        let standby = if active == FIRST { SECOND } else { FIRST };
        if round % 2 == 0 {
            let outcome = group
                .step_down_active(None, None)
                .await
                .expect("the active steps down");
            assert!(
                matches!(
                    outcome.lease_holder_after_step_down,
                    Some(LeaseHolderAfterStepDown::TakenOverByAnotherProcess(_))
                ),
                "round {round}: {outcome:?}"
            );
        } else {
            group.signal(active, ProcessSignal::Terminate).await;
        }
        assert_eq!(
            group.wait_for_takeover(active, TAKEOVER_BUDGET).await,
            standby
        );
        assert_hot_takeover(group, standby);
        switchovers.push(failed_at.elapsed());
        if round % 2 == 1 {
            group.restart(active, DirectoryFate::Keep);
        }
        group.wait_for_hot_standby(active, STANDBY_BUDGET).await;
        group
            .wait_for_progress(&scenario.workload, ACKNOWLEDGEMENTS_PER_PHASE)
            .await;
    }
    let report = scenario
        .finish(
            "ten_switchovers_under_load_lose_no_acknowledged_write",
            first_failure.expect("at least one switchover ran"),
            format!("switchovers served after {switchovers:?}"),
        )
        .await;
    let quick_handovers = report
        .takeovers_after_release()
        .iter()
        .filter(|delay| **delay <= STEP_DOWN_HANDOVER_BOUND)
        .count();
    assert!(
        quick_handovers >= SWITCHOVER_COUNT / 2,
        "every step down must hand over within {STEP_DOWN_HANDOVER_BOUND:?} of its release: {report}"
    );
}

/// AdminService needs an admin credential; a public static token is not one.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn admin_calls_without_an_admin_credential_are_refused() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let token_file = directory.path().join("tokens.yaml");
    let token_hash = aws_lc_rs::digest::digest(&aws_lc_rs::digest::SHA256, ADMIN_TOKEN.as_bytes());
    let token_hex: String = token_hash
        .as_ref()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    std::fs::write(&token_file, format!("tokens:\n  - sha256: {token_hex}\n"))
        .expect("the token file is written");
    let address = free_loopback_address();
    let config = directory.path().join("serve.yaml");
    ServeConfig {
        primary_endpoint: &endpoint,
        hard_delete_endpoint: None,
        listen: address,
        local_directory: &directory.path().join("store"),
        lease_duration: "10s",
        fail_if_held: false,
        parade_db: None,
    }
    .write(&config);
    let mut contents = std::fs::read_to_string(&config).expect("the configuration is readable");
    contents.push_str(&format!(
        "auth:\n  authenticators:\n    - kind: staticTokens\n      file: {}\n",
        token_file.display()
    ));
    std::fs::write(&config, contents).expect("the configuration is written");
    let mut process = ServeProcess::spawn(&config, address, directory.path().join("serve.log"));
    let channel = process.wait_until_serving().await;
    let mut admin = AdminServiceClient::new(channel.clone());
    let missing = admin
        .describe_active_node(DescribeActiveNodeRequest {})
        .await
        .expect_err("a call without a credential is refused");
    assert_eq!(missing.code(), Code::Unauthenticated, "{missing}");
    let bearer: MetadataValue<_> = format!("Bearer {ADMIN_TOKEN}")
        .parse()
        .expect("the bearer header is valid");
    let mut with_public_token =
        AdminServiceClient::with_interceptor(channel, move |mut request: tonic::Request<()>| {
            request
                .metadata_mut()
                .insert("authorization", bearer.clone());
            Ok(request)
        });
    let public = with_public_token
        .step_down_active_node(dexzd_protocol::dex::StepDownActiveNodeRequest {
            expected_node_id: None,
            expected_node_epoch: None,
        })
        .await
        .expect_err("a public credential does not authorize a step down");
    assert_eq!(public.code(), Code::PermissionDenied, "{public}");
    assert!(
        process.is_serving_within(Duration::from_secs(1)).await,
        "a refused step down changes nothing"
    );
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
}

async fn step_down_through(channel: Channel) -> Result<StepDownActiveNodeResponse, tonic::Status> {
    AdminServiceClient::new(channel)
        .step_down_active_node(dexzd_protocol::dex::StepDownActiveNodeRequest {
            expected_node_id: None,
            expected_node_epoch: None,
        })
        .await
        .map(tonic::Response::into_inner)
}

fn describe_outcome(outcome: &StepDownActiveNodeResponse) -> String {
    format!(
        "released epoch {}, then {:?}",
        outcome.released_node_epoch, outcome.lease_holder_after_step_down
    )
}
