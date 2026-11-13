// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dev` and `local` report their effective configuration in one startup log line.

use std::fs;
use std::path::Path;

use tempfile::TempDir;

use super::SHUTDOWN_BUDGET;
use super::harness::{ServeProcess, free_loopback_address};

const EFFECTIVE_CONFIG_LOG_TEXT: &str = "effective configuration";

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn local_reports_the_configured_retention_durability_and_limits() {
    let line = single_node_effective_config_line(
        "local",
        "retention:\n  closedFlowDuration: 30days\n\
         dev:\n  durabilityMode: strict\n\
         values:\n  maxValueBytes: 8388608\n\
         limits:\n  maxFlowStateBytes: 134217728\n\
         lifecycle:\n  attemptDrainTimeout: 1500ms\n\
         metrics:\n  level: basic\n",
        &[],
    )
    .await;
    assert_fields(
        &line,
        &[
            "closed_flow_retention_seconds=2592000.0",
            "max_value_bytes=8388608",
            "max_flow_state_bytes=134217728",
            "attempt_drain_timeout_seconds=1.5",
            "metrics_level=\"basic\"",
            "durability_mode=\"strict\"",
        ],
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn local_reports_the_defaults_of_unset_keys() {
    let line = single_node_effective_config_line("local", "", &[]).await;
    assert_fields(
        &line,
        &[
            "closed_flow_retention_seconds=604800.0",
            "max_value_bytes=16777216",
            "max_flow_state_bytes=67108864",
            "attempt_drain_timeout_seconds=30.0",
            "metrics_level=\"advanced\"",
            "durability_mode=\"strict\"",
        ],
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn dev_reports_its_profile_drain_default_and_a_configured_durability_mode() {
    let line =
        single_node_effective_config_line("dev", "dev:\n  durabilityMode: processCrashSafe\n", &[])
            .await;
    assert_fields(
        &line,
        &[
            "closed_flow_retention_seconds=604800.0",
            "max_value_bytes=16777216",
            "max_flow_state_bytes=67108864",
            "attempt_drain_timeout_seconds=2.0",
            "metrics_level=\"advanced\"",
            "durability_mode=\"processCrashSafe\"",
        ],
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn local_reports_the_durability_mode_of_the_durability_flag() {
    let line = single_node_effective_config_line("local", "", &["--durability", "relaxed"]).await;
    assert_fields(
        &line,
        &[
            "closed_flow_retention_seconds=604800.0",
            "max_value_bytes=16777216",
            "max_flow_state_bytes=67108864",
            "attempt_drain_timeout_seconds=30.0",
            "metrics_level=\"advanced\"",
            "durability_mode=\"relaxed\"",
        ],
    );
}

/// Starts `command` with `overrides` and `extra_arguments`, then returns its only effective
/// configuration log line.
async fn single_node_effective_config_line(
    command: &str,
    overrides: &str,
    extra_arguments: &[&str],
) -> String {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = directory.path().join("single-node.yaml");
    fs::write(
        &config_path,
        format!("cluster:\n  partitionCount: 16\n{overrides}"),
    )
    .expect("the overrides are written");
    let mut process = spawn_single_node(command, directory.path(), &config_path, extra_arguments);
    process.wait_until_serving().await;
    let log = process.log();
    let effective_config_lines = log
        .lines()
        .filter(|line| line.contains(EFFECTIVE_CONFIG_LOG_TEXT))
        .collect::<Vec<_>>();
    assert_eq!(effective_config_lines.len(), 1, "{log}");
    let line = effective_config_lines[0].to_owned();
    let stopped = process.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", process.log());
    line
}

fn assert_fields(line: &str, fields: &[&str]) {
    for field in fields {
        assert!(line.contains(field), "missing {field}: {line}");
    }
}

fn spawn_single_node(
    command: &str,
    directory: &Path,
    config_path: &Path,
    extra_arguments: &[&str],
) -> ServeProcess {
    ServeProcess::spawn_single_node_with_arguments(
        command,
        &directory.join("data"),
        config_path,
        free_loopback_address(),
        directory.join(format!("{command}.log")),
        extra_arguments,
    )
}
