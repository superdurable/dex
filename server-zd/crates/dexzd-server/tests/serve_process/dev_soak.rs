// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Soak of `dex-server-zd dev` as a long-running local server with restarts and cold restores.

use std::fs;
use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowStartOptions, FlowStatus, GetFlowSummaryRequest,
    SetAttributesRequest, StartFlowRequest, StopFlowRequest, StopType, Value, WorkerTarget, value,
};
use tempfile::TempDir;
use tonic::transport::Channel;

use super::harness::{PullWorker, ServeProcess, free_loopback_address};
use super::{FLOW_TYPE, run_greeting_flow, start_greeting_worker, string_value};

const SOAK_SECONDS_VARIABLE: &str = "DEX_ZD_DEV_SOAK_SECONDS";
const DEFAULT_SOAK_SECONDS: u64 = 120;
/// Shorter soaks only smoke-test restarts; disk use needs time for expiry and snapshot rotation.
const MINIMUM_SOAK_FOR_DISK_ASSERTION: Duration = Duration::from_secs(15 * 60);
const CLOSED_FLOW_RETENTION: &str = "2min";
const FLOWS_PER_CYCLE: usize = 20;
const GREETING_FLOWS_PER_CYCLE: usize = 2;
const REWRITES_PER_FLOW: usize = 4;
const LARGE_VALUE_BYTES: usize = 8 * 1024;
/// Restarts are spaced in time so snapshots, log deletion, and retention run between them.
const RESTART_INTERVAL: Duration = Duration::from_secs(60);
const COLD_RESTORE_INTERVAL: Duration = Duration::from_secs(5 * 60);
/// Server log lines counted across every incarnation to explain disk use.
const RECLAMATION_LOG_LINES: [&str; 5] = [
    "snapshot complete",
    "deleted log batches covered by snapshots",
    "deleted snapshots beyond the retained count",
    "deleted unreferenced snapshot files",
    "RunPurge was rejected",
];
const STOP_BUDGET: Duration = Duration::from_secs(15);
/// Peak disk use in the last quarter may exceed the warm-up peak by this factor plus the slack.
const DISK_GROWTH_FACTOR: u64 = 2;
const DISK_GROWTH_SLACK_BYTES: u64 = 64 * 1024 * 1024;

/// `dev` serves for the whole soak, stops cleanly, recovers an empty local store, and bounds disk use.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "long-running; the Server ZD Dev Soak Nightly workflow runs it with DEX_ZD_DEV_SOAK_SECONDS"]
async fn dev_serves_for_the_soak_with_bounded_disk_usage() {
    let soak = soak_duration();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");
    let config_path = directory.path().join("dev.yaml");
    fs::write(&config_path, dev_config()).expect("the dev configuration is written");
    let started_at = tokio::time::Instant::now();
    let mut incarnation = 0;
    let mut server = DevServer::start(
        &data_directory,
        &config_path,
        log_path(directory.path(), incarnation),
    )
    .await;
    let mut disk_samples = Vec::new();
    let mut stop_durations = Vec::new();
    let mut logs = Vec::new();
    let mut cycle = 0;
    let mut last_restart_at = started_at;
    let mut last_cold_restore_at = started_at;
    while started_at.elapsed() < soak {
        write_cycle(&mut server.flow_client, cycle).await;
        disk_samples.push(DiskSample {
            elapsed: started_at.elapsed(),
            object_store_bytes: directory_bytes(&data_directory.join("s3")),
            local_store_bytes: directory_bytes(&data_directory.join("store")),
        });
        cycle += 1;
        let is_cold_restore = last_cold_restore_at.elapsed() >= COLD_RESTORE_INTERVAL;
        if is_cold_restore || last_restart_at.elapsed() >= RESTART_INTERVAL {
            let (stop_duration, log) = server.stop().await;
            stop_durations.push(stop_duration);
            logs.push(log);
            if is_cold_restore {
                fs::remove_dir_all(data_directory.join("store"))
                    .expect("the local store directory is removed");
                last_cold_restore_at = tokio::time::Instant::now();
            }
            last_restart_at = tokio::time::Instant::now();
            incarnation += 1;
            server = DevServer::start(
                &data_directory,
                &config_path,
                log_path(directory.path(), incarnation),
            )
            .await;
            assert_latest_cycle_is_readable(&mut server.flow_client, cycle - 1).await;
        }
    }
    let first_flow_summary = server
        .flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: soak_flow_id(0, 0),
            run_id: String::new(),
        })
        .await;
    let (stop_duration, log) = server.stop().await;
    stop_durations.push(stop_duration);
    logs.push(log);
    eprintln!(
        "dev soak: {cycle} cycles, {incarnation} restarts in {:?}; slowest graceful stop {:?}",
        started_at.elapsed(),
        stop_durations
            .iter()
            .max()
            .expect("the soak stops at least once")
    );
    for line in RECLAMATION_LOG_LINES {
        let count: usize = logs.iter().map(|log| log.matches(line).count()).sum();
        eprintln!("dev soak log: {count:>6} x {line}");
    }
    for sample in &disk_samples {
        eprintln!(
            "dev soak disk: {:>6}s object store {} bytes, local store {} bytes",
            sample.elapsed.as_secs(),
            sample.object_store_bytes,
            sample.local_store_bytes
        );
    }
    if soak >= MINIMUM_SOAK_FOR_DISK_ASSERTION {
        // Retention is two minutes, so the first cycle's Flows must be purged by now.
        let status = first_flow_summary
            .expect_err("the first cycle's Flow is purged")
            .code();
        assert_eq!(status, tonic::Code::NotFound);
        assert_disk_use_is_bounded(&disk_samples, soak);
    }
}

/// Disk use of the embedded object store and the local store after one cycle.
#[derive(Debug)]
struct DiskSample {
    elapsed: Duration,
    object_store_bytes: u64,
    local_store_bytes: u64,
}

/// A running `dev` process with a greeting Worker and a FlowService client.
struct DevServer {
    process: ServeProcess,
    worker: PullWorker,
    flow_client: FlowServiceClient<Channel>,
}

impl DevServer {
    async fn start(data_directory: &Path, config_path: &Path, log_path: PathBuf) -> Self {
        let address = free_loopback_address();
        let mut process = ServeProcess::spawn_dev(data_directory, config_path, address, log_path);
        let channel = process.wait_until_serving().await;
        let worker = start_greeting_worker(channel.clone()).await;
        Self {
            process,
            worker,
            flow_client: FlowServiceClient::new(channel),
        }
    }

    /// Sends SIGTERM, requires a clean exit, and returns the graceful stop time and the process log.
    async fn stop(mut self) -> (Duration, String) {
        self.worker.stop().await;
        let stopping_at = tokio::time::Instant::now();
        let status = self.process.terminate(STOP_BUDGET).await;
        let stop_duration = stopping_at.elapsed();
        let log = self.process.log();
        // Issue #873 tracks the embedded object store missing its shutdown budget.
        assert!(
            status.code() == Some(0) && !log.contains("ShutdownIncomplete"),
            "dev did not stop cleanly with {status} after {stop_duration:?}:\n{log}"
        );
        (stop_duration, log)
    }
}

/// Writes, rewrites, hard-deletes, and stops Flows without a start Step; greeting Flows exercise dispatch.
async fn write_cycle(flow_client: &mut FlowServiceClient<Channel>, cycle: usize) {
    for index in 0..FLOWS_PER_CYCLE {
        let flow_id = soak_flow_id(cycle, index);
        flow_client
            .start_flow(StartFlowRequest {
                flow_id: flow_id.clone(),
                flow_type: FLOW_TYPE.to_owned(),
                flow_start_options: Some(FlowStartOptions {
                    flow_config_override: Some(FlowConfig {
                        worker_target: Some(WorkerTarget {
                            is_pull_mode: true,
                            ..WorkerTarget::default()
                        }),
                        ..FlowConfig::default()
                    }),
                    attributes: vec![
                        AttributeWrite {
                            key: "large".to_owned(),
                            value: Some(large_value(cycle, 0)),
                            ..AttributeWrite::default()
                        },
                        AttributeWrite {
                            key: "secret".to_owned(),
                            value: Some(string_value("secret")),
                            is_hard_delete_on_null: true,
                            ..AttributeWrite::default()
                        },
                    ],
                    ..FlowStartOptions::default()
                }),
                request_id: format!("{flow_id}-start"),
                ..StartFlowRequest::default()
            })
            .await
            .expect("StartFlow succeeds");
        for rewrite in 1..=REWRITES_PER_FLOW {
            set_attribute(
                flow_client,
                &flow_id,
                AttributeWrite {
                    key: "large".to_owned(),
                    value: Some(large_value(cycle, rewrite)),
                    ..AttributeWrite::default()
                },
                format!("{flow_id}-rewrite-{rewrite}"),
            )
            .await;
        }
        set_attribute(
            flow_client,
            &flow_id,
            AttributeWrite {
                key: "secret".to_owned(),
                value: Some(Value {
                    kind: Some(value::Kind::NullValue(0)),
                }),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            },
            format!("{flow_id}-delete-secret"),
        )
        .await;
        flow_client
            .stop_flow(StopFlowRequest {
                flow_id,
                reason: "dev soak cycle finished".to_owned(),
                stop_type: StopType::Terminate.into(),
                ..StopFlowRequest::default()
            })
            .await
            .expect("StopFlow succeeds");
    }
    for index in 0..GREETING_FLOWS_PER_CYCLE {
        run_greeting_flow(flow_client, &format!("dev-soak-greeting-{cycle}-{index}")).await;
    }
}

async fn set_attribute(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    attribute: AttributeWrite,
    request_id: String,
) {
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: flow_id.to_owned(),
            attributes: vec![attribute],
            request_id,
            ..SetAttributesRequest::default()
        })
        .await
        .expect("SetAttributes succeeds");
}

/// The latest cycle stopped moments ago, so retention has not purged it yet.
async fn assert_latest_cycle_is_readable(
    flow_client: &mut FlowServiceClient<Channel>,
    cycle: usize,
) {
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: soak_flow_id(cycle, FLOWS_PER_CYCLE - 1),
            run_id: String::new(),
        })
        .await
        .expect("GetFlowSummary succeeds after the restart")
        .into_inner();
    assert_eq!(summary.flow_status, FlowStatus::Terminated as i32);
}

/// Fails when peak disk use in the last quarter outgrows the warm-up peak.
fn assert_disk_use_is_bounded(disk_samples: &[DiskSample], soak: Duration) {
    let peak_between = |from: Duration, to: Duration| {
        disk_samples
            .iter()
            .filter(|sample| sample.elapsed >= from && sample.elapsed <= to)
            .map(|sample| sample.object_store_bytes + sample.local_store_bytes)
            .max()
            .expect("every quarter of the soak has disk samples")
    };
    let warm_up_peak = peak_between(Duration::ZERO, soak / 4);
    let final_peak = peak_between(soak * 3 / 4, soak * 2);
    assert!(
        final_peak <= warm_up_peak * DISK_GROWTH_FACTOR + DISK_GROWTH_SLACK_BYTES,
        "dev disk use grew from a warm-up peak of {warm_up_peak} bytes to {final_peak} bytes: {disk_samples:?}"
    );
}

/// Returns the total size of the regular files under `path`.
fn directory_bytes(path: &Path) -> u64 {
    let mut total = 0;
    let mut pending = vec![path.to_path_buf()];
    while let Some(directory) = pending.pop() {
        let entries = match fs::read_dir(&directory) {
            Ok(entries) => entries,
            // Snapshot retention and log deletion may remove a directory between listing and reading.
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
            Err(error) => panic!("cannot list {}: {error}", directory.display()),
        };
        for entry in entries {
            let entry = match entry {
                Ok(entry) => entry,
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
                Err(error) => panic!("cannot read an entry of {}: {error}", directory.display()),
            };
            match entry.metadata() {
                Ok(metadata) if metadata.is_dir() => pending.push(entry.path()),
                Ok(metadata) => total += metadata.len(),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                Err(error) => panic!("cannot stat {}: {error}", entry.path().display()),
            }
        }
    }
    total
}

fn dev_config() -> String {
    format!(
        "cluster:\n  partitionCount: 16\nlease:\n  duration: 5s\nretention:\n  closedFlowDuration: {CLOSED_FLOW_RETENTION}\nsnapshot:\n  logThresholdBytes: 262144\n  interval: 10s\n  retainedCount: 2\n  logDeletionDelay: 5s\nsearch:\n  backend: builtin\n"
    )
}

fn soak_duration() -> Duration {
    let seconds = match std::env::var(SOAK_SECONDS_VARIABLE) {
        Ok(text) => text.parse().unwrap_or_else(|error| {
            panic!("{SOAK_SECONDS_VARIABLE}={text:?} is not seconds: {error}")
        }),
        Err(std::env::VarError::NotPresent) => DEFAULT_SOAK_SECONDS,
        Err(error) => panic!("{SOAK_SECONDS_VARIABLE} is unreadable: {error}"),
    };
    Duration::from_secs(seconds)
}

fn log_path(directory: &Path, incarnation: usize) -> PathBuf {
    directory.join(format!("dev-{incarnation}.log"))
}

fn soak_flow_id(cycle: usize, index: usize) -> String {
    format!("dev-soak-{cycle}-{index}")
}

fn large_value(cycle: usize, rewrite: usize) -> Value {
    let mut text = format!("cycle {cycle} rewrite {rewrite} ");
    text.extend(std::iter::repeat_n('x', LARGE_VALUE_BYTES));
    string_value(&text)
}
