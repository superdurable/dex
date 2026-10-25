// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Soak of `dex-server-zd dev` as a long-running local server with restarts and cold restores.

use std::collections::HashSet;
use std::fs;
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowStartOptions, FlowStatus, GetFlowSummaryRequest,
    GetFlowSummaryResponse, SetAttributesRequest, StartFlowRequest, StopFlowRequest, StopType,
    Value, WorkerTarget, value,
};
use tempfile::TempDir;
use tonic::transport::Channel;

use super::harness::{PullWorker, ServeProcess, free_loopback_address};
use super::{FLOW_TYPE, run_greeting_flow, start_greeting_worker, string_value};

const SOAK_SECONDS_VARIABLE: &str = "DEX_ZD_DEV_SOAK_SECONDS";
const DEFAULT_SOAK_SECONDS: u64 = 120;
/// Process logs go here so they outlive a failed run; the default is the test's temporary directory.
const LOG_DIRECTORY_VARIABLE: &str = "DEX_ZD_DEV_SOAK_LOG_DIR";
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
const RECLAMATION_LOG_LINES: [&str; 6] = [
    "snapshot complete",
    "deleted log batches covered by snapshots",
    "deleted snapshots beyond the retained count",
    "deleted unreferenced snapshot files",
    "set aside the previous local store",
    "RunPurge was rejected",
];
const STOP_BUDGET: Duration = Duration::from_secs(15);
/// Client calls carry no deadline, so a stalled server would otherwise hang the soak.
const CLIENT_CALLS_BUDGET: Duration = Duration::from_secs(120);
const LOG_TAIL_LINES: usize = 200;
/// The Display text that `main` prints for `ServerError::ShutdownIncomplete`.
const SHUTDOWN_INCOMPLETE_TEXT: &str = "shutdown did not finish";
/// Peak disk use in the last quarter may exceed the warm-up peak by this factor plus the slack.
const DISK_GROWTH_FACTOR: u64 = 2;
const DISK_GROWTH_SLACK_BYTES: u64 = 64 * 1024 * 1024;

/// `dev` serves for the whole soak, stops cleanly, recovers an empty local store, and bounds disk use.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "long-running; the Server ZD Dev Soak Nightly workflow runs it with DEX_ZD_DEV_SOAK_SECONDS"]
async fn dev_serves_for_the_soak_with_bounded_disk_usage() {
    let soak = soak_duration();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let log_directory = log_directory(directory.path());
    eprintln!("dev soak: process logs in {}", log_directory.display());
    let data_directory = directory.path().join("data");
    let config_path = directory.path().join("dev.yaml");
    fs::write(&config_path, dev_config()).expect("the dev configuration is written");
    let started_at = tokio::time::Instant::now();
    let mut incarnation = 0;
    let mut server = DevServer::start(
        &data_directory,
        &config_path,
        log_path(&log_directory, incarnation),
    )
    .await;
    let mut disk_samples = Vec::new();
    let mut stop_durations = Vec::new();
    let mut reclamation_counts = [0; RECLAMATION_LOG_LINES.len()];
    let mut cycle = 0;
    let mut last_restart_at = started_at;
    let mut last_cold_restore_at = started_at;
    while started_at.elapsed() < soak {
        server.write_cycle(cycle).await;
        disk_samples.push(measure_disk_use(started_at.elapsed(), &data_directory));
        cycle += 1;
        let is_cold_restore = last_cold_restore_at.elapsed() >= COLD_RESTORE_INTERVAL;
        if is_cold_restore || last_restart_at.elapsed() >= RESTART_INTERVAL {
            let (stop_duration, log) = server.stop().await;
            stop_durations.push(stop_duration);
            count_reclamation_lines(&log, &mut reclamation_counts);
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
                log_path(&log_directory, incarnation),
            )
            .await;
            assert_latest_cycle_is_readable(&mut server, cycle - 1).await;
        }
    }
    let first_flow_summary = server.get_flow_summary(soak_flow_id(0, 0)).await;
    let (stop_duration, log) = server.stop().await;
    stop_durations.push(stop_duration);
    count_reclamation_lines(&log, &mut reclamation_counts);
    eprintln!(
        "dev soak: {cycle} cycles, {incarnation} restarts in {:?}; slowest graceful stop {:?}",
        started_at.elapsed(),
        stop_durations
            .iter()
            .max()
            .expect("the soak stops at least once")
    );
    for (line, count) in RECLAMATION_LOG_LINES.iter().zip(reclamation_counts) {
        eprintln!("dev soak log: {count:>6} x {line}");
    }
    let peaks = quarter_peaks(&disk_samples, soak);
    eprintln!(
        "dev soak disk peaks over {} samples:\n{}",
        disk_samples.len(),
        describe_quarter_peaks(&peaks)
    );
    if soak >= MINIMUM_SOAK_FOR_DISK_ASSERTION {
        // Retention is two minutes, so the first cycle's Flows must be purged by now.
        let status = first_flow_summary
            .expect_err("the first cycle's Flow is purged")
            .code();
        assert_eq!(status, tonic::Code::NotFound);
        assert_disk_use_is_bounded(&peaks);
    }
}

/// Disk use under the data directory after one cycle.
struct DiskSample {
    elapsed: Duration,
    /// The embedded object store under `s3/`.
    object_store_bytes: u64,
    /// The local store, snapshot staging directories, and set-aside local stores.
    local_bytes: u64,
}

impl DiskSample {
    fn total_bytes(&self) -> u64 {
        self.object_store_bytes + self.local_bytes
    }
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
        let Ok(worker) =
            tokio::time::timeout(CLIENT_CALLS_BUDGET, start_greeting_worker(channel.clone())).await
        else {
            panic_with_log_tail(&process, "the greeting Worker did not register");
        };
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
        // Issue #873 tracked the embedded object store missing its shutdown budget; its close now uses the unused total.
        assert!(
            status.code() == Some(0) && !log.contains(SHUTDOWN_INCOMPLETE_TEXT),
            "dev did not stop cleanly with {status} after {stop_duration:?}; log tail:\n{}",
            log_tail(&log)
        );
        (stop_duration, log)
    }

    async fn write_cycle(&mut self, cycle: usize) {
        if tokio::time::timeout(
            CLIENT_CALLS_BUDGET,
            write_cycle_flows(&mut self.flow_client, cycle),
        )
        .await
        .is_err()
        {
            panic_with_log_tail(&self.process, &format!("cycle {cycle} did not finish"));
        }
    }

    async fn get_flow_summary(
        &mut self,
        flow_id: String,
    ) -> Result<GetFlowSummaryResponse, tonic::Status> {
        let request = GetFlowSummaryRequest {
            flow_id,
            run_id: String::new(),
        };
        match tokio::time::timeout(
            CLIENT_CALLS_BUDGET,
            self.flow_client.get_flow_summary(request),
        )
        .await
        {
            Ok(result) => result.map(tonic::Response::into_inner),
            Err(_) => panic_with_log_tail(&self.process, "GetFlowSummary did not answer"),
        }
    }
}

fn panic_with_log_tail(process: &ServeProcess, failure: &str) -> ! {
    panic!(
        "{failure} within {CLIENT_CALLS_BUDGET:?}; dev log tail:\n{}",
        log_tail(&process.log())
    )
}

/// Writes, rewrites, hard-deletes, and stops Flows without a start Step; greeting Flows exercise dispatch.
async fn write_cycle_flows(flow_client: &mut FlowServiceClient<Channel>, cycle: usize) {
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
async fn assert_latest_cycle_is_readable(server: &mut DevServer, cycle: usize) {
    let summary = server
        .get_flow_summary(soak_flow_id(cycle, FLOWS_PER_CYCLE - 1))
        .await
        .expect("GetFlowSummary succeeds after the restart");
    assert_eq!(summary.flow_status, FlowStatus::Terminated as i32);
}

/// Returns the sample with the largest total in each quarter of the soak; later samples join the last.
fn quarter_peaks(disk_samples: &[DiskSample], soak: Duration) -> [Option<&DiskSample>; 4] {
    [1, 2, 3, 4].map(|quarter: u32| {
        let from = soak * (quarter - 1) / 4;
        let to = if quarter == 4 {
            Duration::MAX
        } else {
            soak * quarter / 4
        };
        disk_samples
            .iter()
            .filter(|sample| sample.elapsed >= from && sample.elapsed < to)
            .max_by_key(|sample| sample.total_bytes())
    })
}

fn describe_quarter_peaks(peaks: &[Option<&DiskSample>; 4]) -> String {
    let lines: Vec<String> = (1..)
        .zip(peaks)
        .map(|(quarter, peak)| match peak {
            Some(sample) => format!(
                "dev soak disk: quarter {quarter} peak {} bytes at {}s (object store {} bytes, local {} bytes)",
                sample.total_bytes(),
                sample.elapsed.as_secs(),
                sample.object_store_bytes,
                sample.local_bytes
            ),
            None => format!("dev soak disk: quarter {quarter} has no samples"),
        })
        .collect();
    lines.join("\n")
}

/// Fails when peak disk use in the last quarter outgrows the warm-up peak.
fn assert_disk_use_is_bounded(peaks: &[Option<&DiskSample>; 4]) {
    let warm_up_peak = peaks[0]
        .expect("the first quarter of the soak has disk samples")
        .total_bytes();
    let final_peak = peaks[3]
        .expect("the last quarter of the soak has disk samples")
        .total_bytes();
    assert!(
        final_peak <= warm_up_peak * DISK_GROWTH_FACTOR + DISK_GROWTH_SLACK_BYTES,
        "dev disk use grew from a warm-up peak of {warm_up_peak} bytes to {final_peak} bytes:\n{}",
        describe_quarter_peaks(peaks)
    );
}

/// Sums regular file sizes under `data_directory`, counting each inode once.
fn measure_disk_use(elapsed: Duration, data_directory: &Path) -> DiskSample {
    let object_store_directory = data_directory.join("s3");
    let mut sample = DiskSample {
        elapsed,
        object_store_bytes: 0,
        local_bytes: 0,
    };
    // Snapshot checkpoints hard-link the local store's SST files.
    let mut counted_files = HashSet::new();
    let mut pending = vec![data_directory.to_path_buf()];
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
                Ok(metadata) => {
                    if !counted_files.insert((metadata.dev(), metadata.ino())) {
                        continue;
                    }
                    if entry.path().starts_with(&object_store_directory) {
                        sample.object_store_bytes += metadata.len();
                    } else {
                        sample.local_bytes += metadata.len();
                    }
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                Err(error) => panic!("cannot stat {}: {error}", entry.path().display()),
            }
        }
    }
    sample
}

fn count_reclamation_lines(log: &str, counts: &mut [usize; RECLAMATION_LOG_LINES.len()]) {
    for (line, count) in RECLAMATION_LOG_LINES.iter().zip(counts.iter_mut()) {
        *count += log.matches(line).count();
    }
}

/// Returns the last `LOG_TAIL_LINES` lines of `log`.
fn log_tail(log: &str) -> &str {
    let start = log
        .trim_end()
        .rmatch_indices('\n')
        .nth(LOG_TAIL_LINES - 1)
        .map_or(0, |(index, _)| index + 1);
    &log[start..]
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

fn log_directory(temporary_directory: &Path) -> PathBuf {
    let directory = match std::env::var_os(LOG_DIRECTORY_VARIABLE) {
        Some(directory) if !directory.is_empty() => PathBuf::from(directory),
        _ => temporary_directory.to_path_buf(),
    };
    fs::create_dir_all(&directory).unwrap_or_else(|error| {
        panic!(
            "cannot create the log directory {}: {error}",
            directory.display()
        )
    });
    directory
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
