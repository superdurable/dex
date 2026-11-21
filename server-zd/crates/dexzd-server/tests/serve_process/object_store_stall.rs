// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dex-server-zd dev` rides out a short stall of its embedded object store and a short process
//! pause, and still stops when a stall outlasts `commit.batchWriteDeadline`.

use std::fs;
use std::net::SocketAddr;
use std::path::Path;
use std::time::{Duration, Instant};

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    FlowConfig, FlowStartOptions, FlowStatus, StartFlowRequest, StepOptions, WaitForFlowRequest,
    WorkerTarget,
};
use tempfile::TempDir;
use tokio::process::Command;
use tokio::task::JoinHandle;
use tonic::transport::Channel;

use super::harness::{ServeProcess, free_loopback_address, s3local_admin_request};
use super::{FLOW_TYPE, SHUTDOWN_BUDGET, run_greeting_flow, start_greeting_worker, string_value};

/// The longest delay s3local accepts; the tests release it by deleting the rule.
const HOLD_EVERY_REQUEST: &str = r#"{"rules":[{"action":{"type":"delay","milliseconds":600000}}]}"#;
/// Longer than the 10 s log batch write deadline that earlier releases used by default.
const STALL: Duration = Duration::from_secs(15);
const FLOWS_DURING_STALL: usize = 3;
/// Process scheduling and probe intervals on a loaded CI machine.
const TIMING_SLACK: Duration = Duration::from_secs(5);
const EMULATOR_ADDRESS_MARKER: &str = "embedded S3 emulator serving address=";
const STOP_LINE: &str = "object storage did not accept a log batch before its deadline";

/// With the dev defaults, a 15 s stall holds commits until the store answers again, and the node
/// keeps serving: every Flow started during the stall is acknowledged only after it, and runs.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_fifteen_second_object_store_stall_holds_commits_and_dev_keeps_serving() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let (mut process, channel) = start_dev(directory.path(), "").await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    run_greeting_flow(&mut flow_client, "stall-before").await;
    let emulator = embedded_emulator_address(&process);

    hold_every_request(emulator).await;
    let stall_started_at = Instant::now();
    let mut starts: Vec<(String, JoinHandle<Result<Instant, tonic::Status>>)> = Vec::new();
    for number in 0..FLOWS_DURING_STALL {
        let flow_id = format!("stall-during-{number}");
        let start = tokio::spawn(start_greeting_flow(flow_client.clone(), flow_id.clone()));
        starts.push((flow_id, start));
    }
    while stall_started_at.elapsed() < STALL {
        assert!(
            !process.has_exited(),
            "dev exited {:?} into the stall:\n{}",
            stall_started_at.elapsed(),
            process.log()
        );
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    assert!(
        starts.iter().all(|(_, start)| !start.is_finished()),
        "a StartFlow was acknowledged while object storage held every request"
    );
    release_every_request(emulator).await;
    let released_at = Instant::now();

    for (flow_id, start) in starts {
        let acknowledged_at = start
            .await
            .expect("the StartFlow task completes")
            .unwrap_or_else(|status| panic!("StartFlow {flow_id} failed: {status}"));
        assert!(
            acknowledged_at >= released_at,
            "{flow_id} was acknowledged before the release"
        );
        wait_until_completed(&mut flow_client, &flow_id).await;
    }
    run_greeting_flow(&mut flow_client, "stall-after").await;
    let log = process.log();
    assert!(!log.contains("engine node stopping"), "{log}");
    worker.stop().await;
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
}

/// A stall longer than an explicit `commit.batchWriteDeadline` still stops the node and exits 1,
/// and the commit that waited is not acknowledged.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_stall_longer_than_the_batch_write_deadline_stops_dev_and_exits_one() {
    const DEADLINE: Duration = Duration::from_secs(2);
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let (mut process, channel) =
        start_dev(directory.path(), "commit:\n  batchWriteDeadline: 2s\n").await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    run_greeting_flow(&mut flow_client, "long-stall-before").await;
    let emulator = embedded_emulator_address(&process);

    hold_every_request(emulator).await;
    let start = tokio::spawn(start_greeting_flow(
        flow_client.clone(),
        "long-stall-during".to_owned(),
    ));
    // The deadline, then the final attempt of at most the same length.
    let status = process.wait_for_exit(DEADLINE * 2 + TIMING_SLACK).await;
    let log = process.log();
    assert_eq!(status.code(), Some(1), "{log}");
    assert!(log.contains(STOP_LINE), "{log}");
    assert!(
        start.await.expect("the StartFlow task completes").is_err(),
        "a commit that never reached object storage must not be acknowledged"
    );
    worker.stop().await;
}

/// A 15 s SIGSTOP while a log batch write is in flight passes a 10 s deadline. After SIGCONT the
/// store answers at once, so the final attempt commits the batch and the node keeps serving.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_process_pause_past_the_deadline_with_a_log_write_in_flight_does_not_stop_dev() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let (mut process, channel) =
        start_dev(directory.path(), "commit:\n  batchWriteDeadline: 10s\n").await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    run_greeting_flow(&mut flow_client, "pause-before").await;
    let emulator = embedded_emulator_address(&process);

    // Holding requests keeps a log batch write in flight at the freeze.
    hold_every_request(emulator).await;
    let start = tokio::spawn(start_greeting_flow(
        flow_client.clone(),
        "pause-during".to_owned(),
    ));
    tokio::time::sleep(Duration::from_millis(500)).await;
    send_signal(&process, "STOP").await;
    tokio::time::sleep(STALL).await;
    send_signal(&process, "CONT").await;
    release_every_request(emulator).await;

    start
        .await
        .expect("the StartFlow task completes")
        .unwrap_or_else(|status| panic!("StartFlow failed after the pause: {status}"));
    wait_until_completed(&mut flow_client, "pause-during").await;
    run_greeting_flow(&mut flow_client, "pause-after").await;
    let log = process.log();
    assert!(!log.contains("engine node stopping"), "{log}");
    worker.stop().await;
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
}

/// Starts `dev` with the `overrides` YAML and waits until it serves.
async fn start_dev(directory: &Path, overrides: &str) -> (ServeProcess, Channel) {
    let config_path = directory.join("dev.yaml");
    fs::write(
        &config_path,
        format!("cluster:\n  partitionCount: 16\n{overrides}"),
    )
    .expect("the dev configuration is written");
    let mut process = ServeProcess::spawn_dev(
        &directory.join("data"),
        &config_path,
        free_loopback_address(),
        directory.join("dev.log"),
    );
    let channel = process.wait_until_serving().await;
    (process, channel)
}

/// Reads the embedded s3local address from the dev process log.
fn embedded_emulator_address(process: &ServeProcess) -> SocketAddr {
    let log = process.log();
    let (_, after_marker) = log
        .split_once(EMULATOR_ADDRESS_MARKER)
        .unwrap_or_else(|| panic!("dev logs its embedded S3 address:\n{log}"));
    let address = after_marker
        .split_whitespace()
        .next()
        .expect("the address follows the marker");
    address
        .parse()
        .unwrap_or_else(|error| panic!("{address:?} is a socket address: {error}"))
}

async fn hold_every_request(emulator: SocketAddr) {
    let response =
        s3local_admin_request(emulator, "PUT", "/_s3local/faults", HOLD_EVERY_REQUEST).await;
    assert!(response.starts_with("HTTP/1.1 200"), "{response}");
}

async fn release_every_request(emulator: SocketAddr) {
    let response = s3local_admin_request(emulator, "DELETE", "/_s3local/faults", "").await;
    assert!(response.starts_with("HTTP/1.1 2"), "{response}");
}

async fn send_signal(process: &ServeProcess, signal_name: &str) {
    let status = Command::new("kill")
        .arg(format!("-{signal_name}"))
        .arg(process.process_id().to_string())
        .status()
        .await
        .expect("kill runs");
    assert!(status.success(), "kill -{signal_name} failed");
}

/// Starts `flow_id` on the pull Worker without waiting for it and returns when it was acknowledged.
async fn start_greeting_flow(
    mut flow_client: FlowServiceClient<Channel>,
    flow_id: String,
) -> Result<Instant, tonic::Status> {
    let request_id = format!("{flow_id}-request");
    flow_client
        .start_flow(StartFlowRequest {
            flow_id,
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: "GreetStep".to_owned(),
            step_input: Some(string_value("hello")),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget {
                        is_pull_mode: true,
                        ..WorkerTarget::default()
                    }),
                    ..FlowConfig::default()
                }),
                ..FlowStartOptions::default()
            }),
            request_id,
            ..StartFlowRequest::default()
        })
        .await
        .map(|_| Instant::now())
}

async fn wait_until_completed(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) {
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .unwrap_or_else(|status| panic!("WaitForFlow {flow_id} failed: {status}"))
        .into_inner();
    assert_eq!(
        result.flow_status,
        FlowStatus::Completed as i32,
        "{flow_id}"
    );
}
