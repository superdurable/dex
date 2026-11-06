// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dex-server-zd local` restarts gracefully and serves the data directory of `dev` under the same lock.

use std::fs;
use std::path::Path;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{FlowStatus, GetFlowSummaryRequest};
use tempfile::TempDir;
use tonic::transport::Channel;

use super::harness::{ServeProcess, free_loopback_address};
use super::{SHUTDOWN_BUDGET, run_greeting_flow, start_greeting_worker};

const SINGLE_NODE_OVERRIDES: &str = "cluster:\n  partitionCount: 16\n";
const INTRUDER_EXIT_BUDGET: Duration = Duration::from_secs(10);
const LOCK_HELD_TEXT: &str = "another dex-server-zd dev or local process holds";
const LEASE_TAKEOVER_TEXT: &str = "taking over the node lease of a stopped process";

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn local_serves_a_flow_stops_gracefully_and_restarts_on_its_data_directory() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");
    let config_path = write_overrides(directory.path());
    let mut first = ServeProcess::spawn_local(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("first.log"),
    );
    let channel = first.wait_until_serving().await;
    let run_id = run_flow_on(channel, "local-first-flow").await;
    let stopped = first.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", first.log());

    let mut second = ServeProcess::spawn_local(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("second.log"),
    );
    let channel = second.wait_until_serving().await;
    assert!(
        !second.log().contains(LEASE_TAKEOVER_TEXT),
        "a graceful stop releases the lease:\n{}",
        second.log()
    );
    assert_completed_with_run(channel.clone(), "local-first-flow", &run_id).await;
    run_flow_on(channel, "local-second-flow").await;
    let stopped = second.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", second.log());
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_dev_data_directory_opens_under_local_and_back_under_dev() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");
    let config_path = write_overrides(directory.path());
    let mut dev = ServeProcess::spawn_dev(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("dev.log"),
    );
    let dev_run_id = run_flow_on(dev.wait_until_serving().await, "written-by-dev").await;
    let stopped = dev.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", dev.log());

    let mut local = ServeProcess::spawn_local(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("local.log"),
    );
    let channel = local.wait_until_serving().await;
    assert_completed_with_run(channel.clone(), "written-by-dev", &dev_run_id).await;
    let local_run_id = run_flow_on(channel, "written-by-local").await;

    // Both commands use one lock file, so they never serve one data directory at once.
    let mut intruder = ServeProcess::spawn_dev(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("intruder.log"),
    );
    let intruder_status = intruder.wait_for_exit(INTRUDER_EXIT_BUDGET).await;
    assert!(
        !intruder_status.success() && intruder.log().contains(LOCK_HELD_TEXT),
        "dev on the data directory of a running local must fail on its lock; \
         it exited with {intruder_status}:\n{}",
        intruder.log()
    );
    let stopped = local.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", local.log());

    let mut dev_again = ServeProcess::spawn_dev(
        &data_directory,
        &config_path,
        free_loopback_address(),
        directory.path().join("dev-again.log"),
    );
    let channel = dev_again.wait_until_serving().await;
    assert_completed_with_run(channel.clone(), "written-by-dev", &dev_run_id).await;
    assert_completed_with_run(channel, "written-by-local", &local_run_id).await;
    let stopped = dev_again.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", dev_again.log());
}

fn write_overrides(directory: &Path) -> std::path::PathBuf {
    let config_path = directory.join("single-node.yaml");
    fs::write(&config_path, SINGLE_NODE_OVERRIDES).expect("the overrides are written");
    config_path
}

/// Runs the greeting Flow `flow_id` with a pull Worker on `channel` and returns its run ID.
async fn run_flow_on(channel: Channel, flow_id: &str) -> String {
    let worker = start_greeting_worker(channel.clone()).await;
    let run_id = run_greeting_flow(&mut FlowServiceClient::new(channel), flow_id).await;
    worker.stop().await;
    run_id
}

async fn assert_completed_with_run(channel: Channel, flow_id: &str, run_id: &str) {
    let summary = FlowServiceClient::new(channel)
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowSummaryRequest::default()
        })
        .await
        .expect("GetFlowSummary reads a Flow from the shared data directory")
        .into_inner();
    assert_eq!(
        summary.flow_status,
        FlowStatus::Completed as i32,
        "{flow_id}"
    );
    assert_eq!(summary.first_run_id, run_id, "{flow_id}");
}
