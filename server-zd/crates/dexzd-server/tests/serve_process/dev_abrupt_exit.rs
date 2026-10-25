// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dex-server-zd dev` after SIGKILL: the default and strict modes recover, and relaxed refuses to start.

use std::collections::HashSet;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    FlowConfig, FlowStartOptions, FlowStatus, GetFlowSummaryRequest, StartFlowRequest, StepOptions,
    WaitForFlowRequest, WorkerTarget,
};
use tempfile::TempDir;
use tonic::transport::Channel;

use super::harness::{ServeProcess, free_loopback_address};
use super::{FLOW_TYPE, run_greeting_flow, start_greeting_worker, string_value};

/// A SIGKILL leaves the lease held, so a short lease keeps the restart quick.
const DEFAULT_MODE_OVERRIDES: &str = "lease:\n  duration: 5s\n";
const STRICT_MODE_OVERRIDES: &str = "lease:\n  duration: 5s\ndev:\n  durabilityMode: strict\n";
const RELAXED_MODE_OVERRIDES: &str = "lease:\n  duration: 5s\ndev:\n  durabilityMode: relaxed\n";
/// Longer than two renewals of a 5-second lease.
const OBJECT_WRITE_BUDGET: Duration = Duration::from_secs(15);
const EXIT_BUDGET: Duration = Duration::from_secs(30);
const STOP_BUDGET: Duration = Duration::from_secs(15);
const HEAVY_WRITE_CLIENTS: usize = 16;
const ACKNOWLEDGED_FLOWS_BEFORE_KILL: usize = 100;
const HEAVY_WRITE_BUDGET: Duration = Duration::from_secs(60);
const UNCLEAN_RELAXED_SESSION_TEXT: &str =
    "did not stop cleanly, so the embedded object store lost recent writes";
const UNCLEAN_SESSION_WITHOUT_MARKERS_TEXT: &str =
    "from a dex-server-zd release that defaulted to relaxed durability, did not stop cleanly";
/// Files that releases before relaxed session markers never wrote.
const RELAXED_SESSION_FILES: [&str; 2] = ["relaxed-session-open", "relaxed-session-markers"];

/// In the default mode, a SIGKILL loses nothing: the restart serves the earlier Flow.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn default_dev_restarts_after_sigkill_with_the_earlier_flow() {
    restarts_after_sigkill_with_the_earlier_flow(DEFAULT_MODE_OVERRIDES).await;
}

/// Strict mode also recovers after a SIGKILL.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn strict_dev_restarts_after_sigkill_with_the_earlier_flow() {
    restarts_after_sigkill_with_the_earlier_flow(STRICT_MODE_OVERRIDES).await;
}

/// A SIGKILL during heavy writes keeps every acknowledged StartFlow and completion.
#[tokio::test(flavor = "multi_thread", worker_threads = 8)]
async fn default_dev_keeps_every_acknowledged_write_after_sigkill_during_heavy_writes() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path(), "default", DEFAULT_MODE_OVERRIDES);
    let data_directory = directory.path().join("data");
    let mut first = spawn_dev(directory.path(), &data_directory, &config_path, "first");
    let channel = first.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    let acknowledged = Arc::new(Mutex::new(AcknowledgedFlows::default()));
    let mut clients = Vec::new();
    for client in 0..HEAVY_WRITE_CLIENTS {
        let client_task = tokio::spawn(write_greeting_flows_until_failure(
            FlowServiceClient::new(channel.clone()),
            client,
            Arc::clone(&acknowledged),
        ));
        clients.push(client_task);
    }
    let deadline = tokio::time::Instant::now() + HEAVY_WRITE_BUDGET;
    while lock(&acknowledged).completed.len() < ACKNOWLEDGED_FLOWS_BEFORE_KILL {
        assert!(
            tokio::time::Instant::now() < deadline,
            "the heavy writes did not complete {ACKNOWLEDGED_FLOWS_BEFORE_KILL} Flows:\n{}",
            first.log()
        );
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");
    for client in clients {
        tokio::time::timeout(EXIT_BUDGET, client)
            .await
            .expect("every client stops after the kill")
            .expect("no client panics");
    }
    worker.stop().await;

    let acknowledged = Arc::try_unwrap(acknowledged)
        .expect("every client released the acknowledgements")
        .into_inner()
        .expect("no client panicked while holding the acknowledgements");
    let mut second = spawn_dev(directory.path(), &data_directory, &config_path, "second");
    let mut flow_client = FlowServiceClient::new(second.wait_until_serving().await);
    for flow_id in &acknowledged.started {
        let status = flow_status(&mut flow_client, flow_id).await;
        if acknowledged.completed.contains(flow_id) {
            assert_eq!(
                status,
                FlowStatus::Completed as i32,
                "{flow_id} completed before the kill"
            );
        }
    }
    eprintln!(
        "heavy writes: {} Flows started and {} completed before SIGKILL, all readable after restart",
        acknowledged.started.len(),
        acknowledged.completed.len()
    );
    let status = second.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", second.log());
}

/// Relaxed mode cannot recover from SIGKILL, so every later start refuses with a clear error.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn relaxed_dev_refuses_every_restart_after_sigkill() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let relaxed_config_path = write_overrides(directory.path(), "relaxed", RELAXED_MODE_OVERRIDES);
    let data_directory = directory.path().join("data");
    let mut first = spawn_dev(
        directory.path(),
        &data_directory,
        &relaxed_config_path,
        "first",
    );
    run_flow_then_wait_for_object_write(&mut first, &data_directory, "dev-sigkill-relaxed").await;
    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");

    let default_config_path = write_overrides(directory.path(), "default", DEFAULT_MODE_OVERRIDES);
    for (attempt, config_path) in [
        ("second", &relaxed_config_path),
        ("third", &relaxed_config_path),
        ("default", &default_config_path),
    ] {
        let mut restarted = spawn_dev(directory.path(), &data_directory, config_path, attempt);
        let status = restarted.wait_for_exit(EXIT_BUDGET).await;
        let log = restarted.log();
        assert!(!status.success(), "the {attempt} start must fail:\n{log}");
        assert!(
            log.contains(UNCLEAN_RELAXED_SESSION_TEXT)
                && log.contains("delete the whole data directory"),
            "the {attempt} start must name the unclean relaxed session:\n{log}"
        );
    }
}

/// After a clean stop, relaxed mode restarts and serves the earlier Flow.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn relaxed_dev_restarts_after_sigterm_with_the_earlier_flow() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path(), "relaxed", RELAXED_MODE_OVERRIDES);
    let data_directory = directory.path().join("data");
    let mut first = spawn_dev(directory.path(), &data_directory, &config_path, "first");
    run_flow_then_wait_for_object_write(&mut first, &data_directory, "dev-sigterm-relaxed").await;
    let status = first.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", first.log());

    let mut second = spawn_dev(directory.path(), &data_directory, &config_path, "second");
    let channel = second.wait_until_serving().await;
    assert_flow_completed(&mut FlowServiceClient::new(channel), "dev-sigterm-relaxed").await;
    let status = second.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", second.log());
}

/// A relaxed directory from a release without markers serves after a clean stop and is refused after a kill.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn default_dev_checks_a_relaxed_directory_from_a_release_without_markers() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let relaxed_config_path = write_overrides(directory.path(), "relaxed", RELAXED_MODE_OVERRIDES);
    let default_config_path = write_overrides(directory.path(), "default", DEFAULT_MODE_OVERRIDES);

    let clean_directory = directory.path().join("clean");
    let mut clean = spawn_dev(
        directory.path(),
        &clean_directory,
        &relaxed_config_path,
        "clean",
    );
    run_flow_then_wait_for_object_write(&mut clean, &clean_directory, "dev-clean-upgrade").await;
    let status = clean.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", clean.log());
    remove_relaxed_session_files(&clean_directory);
    let mut upgraded = spawn_dev(
        directory.path(),
        &clean_directory,
        &default_config_path,
        "clean-upgraded",
    );
    let channel = upgraded.wait_until_serving().await;
    assert_flow_completed(&mut FlowServiceClient::new(channel), "dev-clean-upgrade").await;
    let status = upgraded.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", upgraded.log());

    let killed_directory = directory.path().join("killed");
    let mut killed = spawn_dev(
        directory.path(),
        &killed_directory,
        &relaxed_config_path,
        "killed",
    );
    run_flow_then_wait_for_object_write(&mut killed, &killed_directory, "dev-killed-upgrade").await;
    let status = killed.kill().await;
    assert!(!status.success(), "SIGKILL stops the relaxed process");
    remove_relaxed_session_files(&killed_directory);
    for attempt in ["killed-upgraded", "killed-upgraded-again"] {
        let mut upgraded = spawn_dev(
            directory.path(),
            &killed_directory,
            &default_config_path,
            attempt,
        );
        let status = upgraded.wait_for_exit(EXIT_BUDGET).await;
        let log = upgraded.log();
        assert!(
            !status.success()
                && log.contains(UNCLEAN_SESSION_WITHOUT_MARKERS_TEXT)
                && log.contains("delete the whole data directory"),
            "the {attempt} start must refuse the unmarked killed directory:\n{log}"
        );
    }
}

async fn restarts_after_sigkill_with_the_earlier_flow(overrides: &str) {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path(), "overrides", overrides);
    let data_directory = directory.path().join("data");
    let mut first = spawn_dev(directory.path(), &data_directory, &config_path, "first");
    run_flow_then_wait_for_object_write(&mut first, &data_directory, "dev-sigkill").await;
    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");

    let mut second = spawn_dev(directory.path(), &data_directory, &config_path, "second");
    let channel = second.wait_until_serving().await;
    assert_flow_completed(&mut FlowServiceClient::new(channel.clone()), "dev-sigkill").await;
    let worker = start_greeting_worker(channel.clone()).await;
    run_greeting_flow(&mut FlowServiceClient::new(channel), "dev-sigkill-after").await;
    worker.stop().await;
    let status = second.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", second.log());
}

/// Flow IDs whose StartFlow, or whose completion, a client saw acknowledged.
#[derive(Debug, Default)]
struct AcknowledgedFlows {
    started: Vec<String>,
    completed: HashSet<String>,
}

fn lock(acknowledged: &Mutex<AcknowledgedFlows>) -> std::sync::MutexGuard<'_, AcknowledgedFlows> {
    acknowledged
        .lock()
        .expect("no client panics while holding the acknowledgements")
}

/// Starts and waits for greeting Flows until a call fails, which happens once the server dies.
async fn write_greeting_flows_until_failure(
    mut flow_client: FlowServiceClient<Channel>,
    client: usize,
    acknowledged: Arc<Mutex<AcknowledgedFlows>>,
) {
    for index in 0.. {
        let flow_id = format!("dev-heavy-{client}-{index}");
        if flow_client
            .start_flow(greeting_start_request(&flow_id))
            .await
            .is_err()
        {
            return;
        }
        lock(&acknowledged).started.push(flow_id.clone());
        let Ok(result) = flow_client
            .wait_for_flow(WaitForFlowRequest {
                flow_id: flow_id.clone(),
                wait_time_seconds: 30,
                ..WaitForFlowRequest::default()
            })
            .await
        else {
            return;
        };
        if result.into_inner().flow_status == FlowStatus::Completed as i32 {
            lock(&acknowledged).completed.insert(flow_id);
        }
    }
}

fn greeting_start_request(flow_id: &str) -> StartFlowRequest {
    StartFlowRequest {
        flow_id: flow_id.to_owned(),
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
        request_id: format!("{flow_id}-request"),
        ..StartFlowRequest::default()
    }
}

fn write_overrides(directory: &Path, name: &str, overrides: &str) -> PathBuf {
    let path = directory.join(format!("{name}.yaml"));
    fs::write(&path, overrides).expect("the dev overrides are written");
    path
}

fn spawn_dev(
    directory: &Path,
    data_directory: &Path,
    config_path: &Path,
    incarnation: &str,
) -> ServeProcess {
    ServeProcess::spawn_dev(
        data_directory,
        config_path,
        free_loopback_address(),
        directory.join(format!("{incarnation}.log")),
    )
}

/// Runs a Flow, then waits until the engine writes another object, such as its renewed lease.
async fn run_flow_then_wait_for_object_write(
    process: &mut ServeProcess,
    data_directory: &Path,
    flow_id: &str,
) {
    let channel = process.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    run_greeting_flow(&mut FlowServiceClient::new(channel), flow_id).await;
    worker.stop().await;
    let written_before = object_data_files(data_directory);
    let deadline = tokio::time::Instant::now() + OBJECT_WRITE_BUDGET;
    while object_data_files(data_directory).is_subset(&written_before) {
        assert!(
            tokio::time::Instant::now() < deadline,
            "the engine wrote no object within {OBJECT_WRITE_BUDGET:?}:\n{}",
            process.log()
        );
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
}

/// Lists the embedded object store's data files, whose names change on every object write.
fn object_data_files(data_directory: &Path) -> HashSet<PathBuf> {
    fn collect(directory: &Path, files: &mut HashSet<PathBuf>) {
        let Ok(entries) = fs::read_dir(directory) else {
            return;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_dir() {
                collect(&path, files);
            } else {
                files.insert(path);
            }
        }
    }
    let mut files = HashSet::new();
    collect(&data_directory.join("s3").join("data"), &mut files);
    files
}

/// Makes a directory look as if a release without relaxed session markers wrote it.
fn remove_relaxed_session_files(data_directory: &Path) {
    for name in RELAXED_SESSION_FILES {
        let path = data_directory.join("s3").join(name);
        if path.exists() {
            fs::remove_file(&path).expect("the relaxed session file is removed");
        }
    }
}

async fn flow_status(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) -> i32 {
    flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowSummaryRequest::default()
        })
        .await
        .unwrap_or_else(|status| panic!("{flow_id} survives the restart: {status}"))
        .into_inner()
        .flow_status
}

async fn assert_flow_completed(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) {
    assert_eq!(
        flow_status(flow_client, flow_id).await,
        FlowStatus::Completed as i32,
        "{flow_id} is still completed"
    );
}
