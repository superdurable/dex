// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A SIGKILLed `dex-server-zd dev` restarts on its data directory without waiting for its lease.

use std::fs;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{FlowStatus, GetFlowSummaryRequest};
use tempfile::TempDir;

use super::harness::{ServeProcess, free_loopback_address};
use super::{SHUTDOWN_BUDGET, run_greeting_flow, start_greeting_worker};

const FLOW_ID: &str = "dev-restart-flow";
/// Well under the 30-second dev lease, so a restart that waits for its expiry fails.
const RESTART_SERVING_BUDGET: Duration = Duration::from_secs(10);
const INTRUDER_EXIT_BUDGET: Duration = Duration::from_secs(10);

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_sigkilled_dev_restarts_without_waiting_for_its_lease() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");
    let config_path = directory.path().join("dev.yaml");
    // No lease key, so dev uses its 30-second default; relaxed object writes do not survive SIGKILL.
    fs::write(
        &config_path,
        "cluster:\n  partitionCount: 16\ndev:\n  durabilityMode: strict\n",
    )
    .expect("the dev configuration is written");
    let mut first = ServeProcess::spawn_dev(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("first.log"),
    );
    let channel = first.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    let run_id = run_greeting_flow(&mut FlowServiceClient::new(channel), FLOW_ID).await;
    worker.stop().await;

    // The takeover is safe only while one process at a time can serve a data directory.
    let mut intruder = ServeProcess::spawn_dev(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("intruder.log"),
    );
    let intruder_status = intruder.wait_for_exit(INTRUDER_EXIT_BUDGET).await;
    assert!(
        !intruder_status.success() && intruder.log().contains("starting the embedded S3 emulator"),
        "a second dev process on the same data directory must fail to open its object store; \
         it exited with {intruder_status}:\n{}",
        intruder.log()
    );

    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");
    let restarting_at = tokio::time::Instant::now();
    let mut second = ServeProcess::spawn_dev(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("second.log"),
    );
    let channel = second.wait_until_serving().await;
    let restart_duration = restarting_at.elapsed();
    assert!(
        restart_duration < RESTART_SERVING_BUDGET,
        "the restart served after {restart_duration:?}:\n{}",
        second.log()
    );
    assert!(
        second
            .log()
            .contains("taking over the node lease of a stopped process"),
        "{}",
        second.log()
    );
    let summary = FlowServiceClient::new(channel)
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: FLOW_ID.to_owned(),
            ..GetFlowSummaryRequest::default()
        })
        .await
        .expect("GetFlowSummary succeeds after the restart")
        .into_inner();
    assert_eq!(summary.flow_status, FlowStatus::Completed as i32);
    assert_eq!(summary.first_run_id, run_id);
    let stopped = second.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", second.log());
}
