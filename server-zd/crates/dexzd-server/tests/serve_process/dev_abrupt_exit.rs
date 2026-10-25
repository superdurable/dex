// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dex-server-zd dev` after SIGKILL: strict durability recovers, and relaxed durability refuses to start.

use std::fs;
use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{FlowStatus, GetFlowSummaryRequest};
use tempfile::TempDir;

use super::harness::{ServeProcess, free_loopback_address};
use super::{run_greeting_flow, start_greeting_worker};

/// A SIGKILL leaves the lease held, so a short lease keeps the restart quick.
const DEFAULT_MODE_OVERRIDES: &str = "lease:\n  duration: 5s\n";
const RELAXED_MODE_OVERRIDES: &str = "lease:\n  duration: 5s\ndev:\n  durabilityMode: relaxed\n";
/// Covers more than one renewal at a 5-second lease, so the engine overwrites its lease object.
const LEASE_OVERWRITE_WAIT: Duration = Duration::from_secs(4);
const EXIT_BUDGET: Duration = Duration::from_secs(30);
const STOP_BUDGET: Duration = Duration::from_secs(15);
const UNCLEAN_RELAXED_SESSION_TEXT: &str =
    "did not stop cleanly, so the embedded object store lost recent writes";

/// In the default mode, a SIGKILL loses nothing: the restart serves the earlier Flow.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn default_dev_restarts_after_sigkill_with_the_earlier_flow() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path(), "default", DEFAULT_MODE_OVERRIDES);
    let data_directory = directory.path().join("data");
    let mut first = spawn_dev(directory.path(), &data_directory, &config_path, "first");
    run_flow_then_wait_for_lease_overwrite(&mut first, "dev-sigkill-default").await;
    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");

    let mut second = spawn_dev(directory.path(), &data_directory, &config_path, "second");
    let channel = second.wait_until_serving().await;
    assert_flow_completed(
        &mut FlowServiceClient::new(channel.clone()),
        "dev-sigkill-default",
    )
    .await;
    let worker = start_greeting_worker(channel.clone()).await;
    run_greeting_flow(
        &mut FlowServiceClient::new(channel),
        "dev-sigkill-default-after",
    )
    .await;
    worker.stop().await;
    let status = second.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", second.log());
}

/// Relaxed mode cannot recover from SIGKILL, so every later start refuses with a clear error.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn relaxed_dev_refuses_every_restart_after_sigkill() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path(), "relaxed", RELAXED_MODE_OVERRIDES);
    let data_directory = directory.path().join("data");
    let mut first = spawn_dev(directory.path(), &data_directory, &config_path, "first");
    run_flow_then_wait_for_lease_overwrite(&mut first, "dev-sigkill-relaxed").await;
    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");

    for attempt in ["second", "third"] {
        let mut restarted = spawn_dev(directory.path(), &data_directory, &config_path, attempt);
        let status = restarted.wait_for_exit(EXIT_BUDGET).await;
        let log = restarted.log();
        assert!(!status.success(), "the {attempt} start must fail:\n{log}");
        assert!(
            log.contains(UNCLEAN_RELAXED_SESSION_TEXT)
                && log.contains("delete the whole data directory"),
            "the {attempt} start must name the unclean relaxed session:\n{log}"
        );
    }
    let strict_config_path = write_overrides(directory.path(), "default", DEFAULT_MODE_OVERRIDES);
    let mut strict = spawn_dev(
        directory.path(),
        &data_directory,
        &strict_config_path,
        "strict",
    );
    let status = strict.wait_for_exit(EXIT_BUDGET).await;
    let log = strict.log();
    assert!(
        !status.success() && log.contains(UNCLEAN_RELAXED_SESSION_TEXT),
        "strict mode must not serve the diverged data either:\n{log}"
    );
}

/// After a clean stop, relaxed mode restarts and serves the earlier Flow.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn relaxed_dev_restarts_after_sigterm_with_the_earlier_flow() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path(), "relaxed", RELAXED_MODE_OVERRIDES);
    let data_directory = directory.path().join("data");
    let mut first = spawn_dev(directory.path(), &data_directory, &config_path, "first");
    run_flow_then_wait_for_lease_overwrite(&mut first, "dev-sigterm-relaxed").await;
    let status = first.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", first.log());

    let mut second = spawn_dev(directory.path(), &data_directory, &config_path, "second");
    let channel = second.wait_until_serving().await;
    assert_flow_completed(&mut FlowServiceClient::new(channel), "dev-sigterm-relaxed").await;
    let status = second.terminate(STOP_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", second.log());
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

async fn run_flow_then_wait_for_lease_overwrite(process: &mut ServeProcess, flow_id: &str) {
    let channel = process.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    run_greeting_flow(&mut FlowServiceClient::new(channel), flow_id).await;
    worker.stop().await;
    tokio::time::sleep(LEASE_OVERWRITE_WAIT).await;
}

async fn assert_flow_completed(
    flow_client: &mut FlowServiceClient<tonic::transport::Channel>,
    flow_id: &str,
) {
    let summary = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowSummaryRequest::default()
        })
        .await
        .unwrap_or_else(|status| panic!("{flow_id} survives the restart: {status}"))
        .into_inner();
    assert_eq!(
        summary.flow_status,
        FlowStatus::Completed as i32,
        "{flow_id} is still completed: {summary:?}"
    );
}
