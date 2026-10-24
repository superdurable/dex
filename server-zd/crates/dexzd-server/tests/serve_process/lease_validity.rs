// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A node serves only within its local lease validity, and renewal retries transient failures.

use std::os::unix::process::ExitStatusExt;
use std::path::Path;
use std::time::{Duration, Instant};

use s3mock_server::SignatureVerification;
use tempfile::TempDir;

use super::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use super::{PRIMARY_BUCKET, PRIMARY_PREFIX, SHUTDOWN_BUDGET};

/// The longest delay s3mock accepts, which outlasts every test here.
const HUNG_REQUEST_MILLISECONDS: u64 = 600_000;
/// The watchdog's graceful budget after the deadline, then the abort.
const LEASE_EXPIRY_ABORT_BUDGET: Duration = Duration::from_secs(2);
const SIGABRT: i32 = 6;
/// Process scheduling and probe intervals on a loaded CI machine.
const TIMING_SLACK: Duration = Duration::from_secs(3);
const READINESS_POLL_INTERVAL: Duration = Duration::from_millis(50);

/// A lease PUT that never returns turns health NOT_SERVING by the deadline, and the process ends.
///
/// The graceful path exits 1; the watchdog aborts with SIGABRT if that path is still running.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_hung_lease_renewal_stops_serving_by_the_deadline_and_exits_one() {
    const LEASE_DURATION: Duration = Duration::from_secs(4);
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let mut process = spawn_serving_process(&primary, directory.path(), "4s").await;
    primary
        .set_fault_rules(&format!(
            r#"[{{"operation":"PutObject","key_prefix":"{PRIMARY_PREFIX}/lease/","action":{{"type":"delay","milliseconds":{HUNG_REQUEST_MILLISECONDS}}}}}]"#
        ))
        .await;
    let hung_at = Instant::now();
    let not_serving_budget = LEASE_DURATION + TIMING_SLACK;
    while ServeProcess::readiness_channel(process.address())
        .await
        .is_some()
    {
        assert!(
            hung_at.elapsed() < not_serving_budget,
            "health stayed SERVING for {not_serving_budget:?} after lease renewals hung:\n{}",
            process.log()
        );
        tokio::time::sleep(READINESS_POLL_INTERVAL).await;
    }
    let exit_budget = LEASE_DURATION + LEASE_EXPIRY_ABORT_BUDGET + TIMING_SLACK;
    let status = process
        .wait_for_exit(exit_budget.saturating_sub(hung_at.elapsed()))
        .await;
    let log = process.log();
    assert!(
        status.code() == Some(1) || status.signal() == Some(SIGABRT),
        "{status:?}:\n{log}"
    );
    assert!(log.contains("node lease lost"), "{log}");
    assert!(
        log.contains("node lease renewal attempt failed; retrying"),
        "{log}"
    );
    primary.clear_fault_rules().await;
    primary.shutdown().await;
}

/// A renewal that fails with 503 is retried, so the node keeps serving and SIGTERM exits 0.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_transient_renewal_failure_is_retried_and_the_node_keeps_serving() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let mut process = spawn_serving_process(&primary, directory.path(), "3s").await;
    primary
        .set_fault_rules(&format!(
            r#"[{{"operation":"PutObject","key_prefix":"{PRIMARY_PREFIX}/lease/","action":{{"type":"error","status":503}}}}]"#
        ))
        .await;
    let failed_at = Instant::now();
    while !primary.request_log().await.contains("\"status\":503") {
        assert!(
            failed_at.elapsed() < Duration::from_secs(5),
            "no renewal reached the 503 rule:\n{}",
            process.log()
        );
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    primary.clear_fault_rules().await;
    assert_serving_for(&mut process, Duration::from_secs(4)).await;
    let log = process.log();
    assert!(
        log.contains("node lease renewal attempt failed; retrying"),
        "{log}"
    );
    assert!(!log.contains("node lease lost"), "{log}");
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
}

/// Renewals that each take most of an attempt's budget still keep the node serving.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn slow_renewals_within_the_attempt_budget_keep_the_node_serving() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let mut process = spawn_serving_process(&primary, directory.path(), "6s").await;
    // A 6 s lease renews every 2 s and gives each attempt 1 s; parallel test processes add latency.
    primary
        .set_fault_rules(&format!(
            r#"[{{"operation":"PutObject","key_prefix":"{PRIMARY_PREFIX}/lease/","action":{{"type":"delay","milliseconds":300}}}}]"#
        ))
        .await;
    assert_serving_for(&mut process, Duration::from_secs(7)).await;
    assert!(
        !process.log().contains("node lease lost"),
        "{}",
        process.log()
    );
    primary.clear_fault_rules().await;
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
}

async fn spawn_serving_process(
    primary: &S3Emulator,
    directory: &Path,
    lease_duration: &str,
) -> ServeProcess {
    let endpoint = primary.endpoint();
    let address = free_loopback_address();
    let config_path = directory.join("serve.yaml");
    ServeConfig {
        primary_endpoint: &endpoint,
        hard_delete_endpoint: None,
        listen: address,
        local_directory: &directory.join("store"),
        lease_duration,
        fail_if_held: false,
        parade_db: None,
    }
    .write(&config_path);
    let mut process = ServeProcess::spawn(&config_path, address, directory.join("serve.log"));
    let _serving_channel = process.wait_until_serving().await;
    process
}

/// Asserts that every readiness name reports SERVING throughout `duration`.
async fn assert_serving_for(process: &mut ServeProcess, duration: Duration) {
    let started = Instant::now();
    while started.elapsed() < duration {
        assert!(
            process.is_serving_within(Duration::from_secs(1)).await,
            "the node stopped serving after {:?}:\n{}",
            started.elapsed(),
            process.log()
        );
        assert!(
            !process.has_exited(),
            "the node exited after {:?}:\n{}",
            started.elapsed(),
            process.log()
        );
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
}
