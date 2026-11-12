// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dev` and `local` report their effective configuration in `dexzd_config_*` metrics and one log line.

use std::fs;
use std::net::SocketAddr;
use std::path::Path;

use tempfile::TempDir;

use super::SHUTDOWN_BUDGET;
use super::harness::{ServeProcess, free_loopback_address};
use super::metrics::{sample_with_label, try_scrape};

const EFFECTIVE_CONFIG_LOG_TEXT: &str = "effective configuration";

/// The values one scrape reports, with the durability mode label when it is present.
#[derive(Debug, PartialEq)]
struct ReportedConfig {
    closed_flow_retention_seconds: f64,
    max_value_bytes: f64,
    max_flow_state_bytes: f64,
    attempt_drain_timeout_seconds: f64,
    durability_mode: Option<String>,
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn local_reports_the_configured_retention_durability_and_limits() {
    let reported = scrape_single_node_config(
        "local",
        "retention:\n  closedFlowDuration: 30days\n\
         dev:\n  durabilityMode: strict\n\
         values:\n  maxValueBytes: 8388608\n\
         limits:\n  maxFlowStateBytes: 134217728\n\
         lifecycle:\n  attemptDrainTimeout: 1500ms\n",
        &[],
    )
    .await;
    assert_eq!(
        reported,
        ReportedConfig {
            closed_flow_retention_seconds: 2_592_000.0,
            max_value_bytes: 8_388_608.0,
            max_flow_state_bytes: 134_217_728.0,
            attempt_drain_timeout_seconds: 1.5,
            durability_mode: Some("strict".to_owned()),
        }
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn local_reports_the_defaults_of_unset_keys() {
    let reported = scrape_single_node_config("local", "", &[]).await;
    assert_eq!(
        reported,
        ReportedConfig {
            closed_flow_retention_seconds: 604_800.0,
            max_value_bytes: 16_777_216.0,
            max_flow_state_bytes: 67_108_864.0,
            attempt_drain_timeout_seconds: 30.0,
            durability_mode: Some("strict".to_owned()),
        }
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn dev_reports_its_profile_drain_default_and_a_configured_durability_mode() {
    let reported =
        scrape_single_node_config("dev", "dev:\n  durabilityMode: processCrashSafe\n", &[]).await;
    assert_eq!(
        reported,
        ReportedConfig {
            closed_flow_retention_seconds: 604_800.0,
            max_value_bytes: 16_777_216.0,
            max_flow_state_bytes: 67_108_864.0,
            attempt_drain_timeout_seconds: 2.0,
            durability_mode: Some("processCrashSafe".to_owned()),
        }
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn local_reports_the_durability_mode_of_the_durability_flag() {
    let reported = scrape_single_node_config("local", "", &["--durability", "relaxed"]).await;
    assert_eq!(
        reported,
        ReportedConfig {
            closed_flow_retention_seconds: 604_800.0,
            max_value_bytes: 16_777_216.0,
            max_flow_state_bytes: 67_108_864.0,
            attempt_drain_timeout_seconds: 30.0,
            durability_mode: Some("relaxed".to_owned()),
        }
    );
}

/// Starts `command` with `overrides`, `extra_arguments`, and a metrics listener, then returns
/// what `/metrics` reports.
async fn scrape_single_node_config(
    command: &str,
    overrides: &str,
    extra_arguments: &[&str],
) -> ReportedConfig {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let metrics_address = free_loopback_address();
    let config_path = directory.path().join("single-node.yaml");
    fs::write(
        &config_path,
        format!(
            "cluster:\n  partitionCount: 16\nmetrics:\n  listen: '{metrics_address}'\n{overrides}"
        ),
    )
    .expect("the overrides are written");
    let mut process = spawn_single_node(command, directory.path(), &config_path, extra_arguments);
    process.wait_until_serving().await;
    let reported = reported_config(metrics_address).await;
    let log = process.log();
    let effective_config_lines = log
        .lines()
        .filter(|line| line.contains(EFFECTIVE_CONFIG_LOG_TEXT))
        .collect::<Vec<_>>();
    assert_eq!(effective_config_lines.len(), 1, "{log}");
    let durability_mode = reported
        .durability_mode
        .as_deref()
        .expect("dev and local report a durability mode");
    for field in [
        format!(
            "closed_flow_retention_seconds={:?}",
            reported.closed_flow_retention_seconds
        ),
        format!("max_value_bytes={}", reported.max_value_bytes),
        format!("max_flow_state_bytes={}", reported.max_flow_state_bytes),
        format!(
            "attempt_drain_timeout_seconds={:?}",
            reported.attempt_drain_timeout_seconds
        ),
        format!("durability_mode=\"{durability_mode}\""),
    ] {
        assert!(
            effective_config_lines[0].contains(&field),
            "missing {field}: {}",
            effective_config_lines[0]
        );
    }
    let stopped = process.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", process.log());
    reported
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

async fn reported_config(metrics_address: SocketAddr) -> ReportedConfig {
    let response = try_scrape(metrics_address, "/metrics")
        .await
        .expect("the metrics listener answers");
    let body = response
        .split_once("\r\n\r\n")
        .expect("the HTTP response has a body")
        .1;
    let durability_lines = body
        .lines()
        .filter(|line| line.starts_with("dexzd_config_durability_mode_info{"))
        .collect::<Vec<_>>();
    assert!(durability_lines.len() <= 1, "{body}");
    let durability_mode = durability_lines.first().map(|line| {
        let mode = line
            .split("mode=\"")
            .nth(1)
            .and_then(|suffix| suffix.split('"').next())
            .expect("the durability info series has a mode label");
        assert_eq!(
            sample_with_label(body, "dexzd_config_durability_mode_info", "mode", mode),
            1.0
        );
        mode.to_owned()
    });
    ReportedConfig {
        closed_flow_retention_seconds: unlabelled_sample(
            body,
            "dexzd_config_closed_flow_retention_seconds",
        ),
        max_value_bytes: unlabelled_sample(body, "dexzd_config_max_value_bytes"),
        max_flow_state_bytes: unlabelled_sample(body, "dexzd_config_max_flow_state_bytes"),
        attempt_drain_timeout_seconds: unlabelled_sample(
            body,
            "dexzd_config_attempt_drain_timeout_seconds",
        ),
        durability_mode,
    }
}

/// Returns the value of the series `name` without labels.
pub(crate) fn unlabelled_sample(body: &str, name: &str) -> f64 {
    let prefix = format!("{name} ");
    body.lines()
        .find_map(|line| line.strip_prefix(&prefix))
        .unwrap_or_else(|| panic!("missing {name}: {body}"))
        .parse::<f64>()
        .expect("numeric sample")
}
