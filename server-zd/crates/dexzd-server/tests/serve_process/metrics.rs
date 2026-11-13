// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::fs;
use std::net::SocketAddr;
use std::path::Path;

use dexzd_metrics::{MetricsLevel, exported_family_names_for_test};
use dexzd_protocol::dex::{RetryPolicy, StepMovement};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::*;

/// Scenario series caps without process series; the scenario measured 68, 153, and 205.
const BASIC_SERIES_CAP: usize = 72;
const ADVANCED_SERIES_CAP: usize = 158;
const VERBOSE_SERIES_CAP: usize = 210;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn basic_metrics_level_exports_only_basic_families() {
    metrics_level_scenario(MetricsLevel::Basic).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn advanced_metrics_level_exports_basic_and_advanced_families() {
    metrics_level_scenario(MetricsLevel::Advanced).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn verbose_metrics_level_tracks_one_completed_flow_and_worker_poll() {
    metrics_level_scenario(MetricsLevel::Verbose).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn omitted_metrics_level_exports_advanced_families() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("temporary directory exists");
    let metrics_address = free_loopback_address();
    let mut process = spawn_serve_with_metrics(
        &primary.endpoint(),
        directory.path(),
        "default-level-metrics",
        free_loopback_address(),
        metrics_address,
        None,
        "",
    );
    process.wait_until_serving().await;
    let response = scrape(metrics_address, "/metrics").await;
    assert_exported_families(http_body(&response), MetricsLevel::Advanced);
    assert!(
        effective_config_line(&process).contains("metrics_level=\"advanced\""),
        "{}",
        process.log()
    );
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
}

/// Runs two Flows and a pull Worker at `level`; value and recovery checks need verbose.
async fn metrics_level_scenario(level: MetricsLevel) {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("temporary directory exists");
    let api_address = free_loopback_address();
    let metrics_address = free_loopback_address();
    let mut process = spawn_serve_with_metrics(
        &primary.endpoint(),
        directory.path(),
        "metrics",
        api_address,
        metrics_address,
        Some(level),
        "matching:\n  dispatchWait: 1s\n  stickyWait: 1s\n",
    );
    let channel = process.wait_until_serving().await;
    let before = scrape(metrics_address, "/metrics").await;
    assert!(before.starts_with("HTTP/1.1 200 OK\r\n"));
    assert!(
        before.contains("Content-Type: application/openmetrics-text; version=1.0.0; charset=utf-8")
    );
    assert!(
        scrape(metrics_address, "/other")
            .await
            .starts_with("HTTP/1.1 404")
    );

    let worker = PullWorker::start(
        WorkerTaskServiceClient::new(channel.clone()),
        FLOW_TYPE,
        Box::new(|request| {
            if request.step_type == "FailStep" {
                // A Step decision without a Step type is an invalid response.
                return InvokeExecuteMethodResponse {
                    step_decision: Some(StepDecision {
                        next_steps: vec![StepMovement::default()],
                        ..StepDecision::default()
                    }),
                    ..InvokeExecuteMethodResponse::default()
                };
            }
            assert_eq!(request.step_type, "GreetStep");
            InvokeExecuteMethodResponse {
                step_decision: Some(StepDecision {
                    close_decision: Some(CloseDecision {
                        close_decision_type: CloseDecisionType::ForceComplete.into(),
                        close_input: Some(string_value("completed")),
                        ..CloseDecision::default()
                    }),
                    ..StepDecision::default()
                }),
                ..InvokeExecuteMethodResponse::default()
            }
        }),
    )
    .await;
    let mut client = FlowServiceClient::new(channel);
    client
        .start_flow(StartFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: "GreetStep".to_owned(),
            step_input: Some(string_value("start")),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget { is_pull_mode: true }),
                    ..FlowConfig::default()
                }),
                ..FlowStartOptions::default()
            }),
            request_id: "metrics-request".to_owned(),
            ..StartFlowRequest::default()
        })
        .await
        .expect("Flow starts");
    let result = client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            needs_results: true,
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("Flow completes")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Completed as i32);

    client
        .start_flow(StartFlowRequest {
            flow_id: "metrics-failed-flow".to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: "FailStep".to_owned(),
            step_input: Some(string_value("start")),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                execute_timeout_seconds: 2,
                execute_retry_policy: Some(RetryPolicy {
                    maximum_attempts: 1,
                    ..RetryPolicy::default()
                }),
                ..StepOptions::default()
            }),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget { is_pull_mode: true }),
                    ..FlowConfig::default()
                }),
                ..FlowStartOptions::default()
            }),
            request_id: "metrics-failed-request".to_owned(),
            ..StartFlowRequest::default()
        })
        .await
        .expect("second Flow starts");
    let failed = client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: "metrics-failed-flow".to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("second Flow closes")
        .into_inner();
    assert_eq!(failed.flow_status, FlowStatus::Failed as i32);
    worker.stop().await;

    let after = scrape(metrics_address, "/metrics").await;
    let body = http_body(&after);
    let before_body = http_body(&before);
    assert_exported_families(body, level);
    assert_every_family_has_a_sample(body);
    let effective_config_line = effective_config_line(&process);
    assert!(
        effective_config_line.contains("closed_flow_retention_seconds=604800.0")
            && effective_config_line.contains(&format!("metrics_level=\"{}\"", level.yaml_name()))
            && !effective_config_line.contains("durability_mode"),
        "{effective_config_line}"
    );
    assert!(!body.contains("_total_total"));
    let series_count = body
        .lines()
        .filter(|line| !line.starts_with('#') && !line.is_empty())
        .count();
    let series_cap = series_cap(level);
    assert!(
        series_count <= series_cap,
        "a {} scrape has {series_count} series, above its cap of {series_cap}",
        level.yaml_name()
    );
    eprintln!(
        "{} metrics scenario series count: {series_count}",
        level.yaml_name()
    );
    if level != MetricsLevel::Verbose {
        let status = process.terminate(SHUTDOWN_BUDGET).await;
        assert_eq!(status.code(), Some(0), "{}", process.log());
        return;
    }
    assert!(
        sample_sum(body, "dexzd_commit_records_total")
            > sample_sum(before_body, "dexzd_commit_records_total")
    );
    assert!(
        sample_sum(body, "dexzd_commit_bytes_total")
            > sample_sum(before_body, "dexzd_commit_bytes_total")
    );
    assert!(
        sample_sum(body, "dexzd_object_store_requests_total{operation=\"put\"")
            > sample_sum(
                before_body,
                "dexzd_object_store_requests_total{operation=\"put\""
            )
    );
    assert!(
        sample_sum(body, "dexzd_object_store_put_bytes_total")
            > sample_sum(before_body, "dexzd_object_store_put_bytes_total")
    );
    assert!(
        sample_sum(body, "dexzd_api_requests_total")
            > sample_sum(before_body, "dexzd_api_requests_total")
    );
    assert!(body.contains("rpc_class=\"worker_poll\""));
    assert!(!body.contains("dexzd_api_request_latency_seconds_count{rpc_class=\"worker_poll\""));
    for close_status in ["completed", "failed"] {
        assert!(
            sample_with_label(
                body,
                "dexzd_flow_closes_total",
                "close_status",
                close_status
            ) > 0.0,
            "{close_status} Flow close did not move"
        );
    }
    for event in ["delivered", "closed"] {
        assert!(
            sample_with_label(body, "dexzd_worker_stream_events_total", "event", event) > 0.0,
            "{event} did not move"
        );
    }
    for line in body
        .lines()
        .filter(|line| line.starts_with("dexzd_step_attempt_failures_total{"))
    {
        let failure_kind = line
            .split("failure_kind=\"")
            .nth(1)
            .and_then(|suffix| suffix.split('"').next())
            .expect("Step failure kind label exists");
        assert!(matches!(
            failure_kind,
            "worker_error"
                | "method_timeout"
                | "heartbeat_timeout"
                | "dispatch_error"
                | "invalid_response"
                | "unsupported_feature"
                | "value_too_large"
                | "request_too_large"
                | "retry_deadline"
                | "started_attempt_interrupted"
        ));
    }
    for name in [
        "dexzd_api_request_latency_seconds_count",
        "dexzd_step_attempt_failures_total",
        "dexzd_step_retries_exhausted_total",
        "dexzd_flow_closes_total",
        "dexzd_flow_end_to_end_latency_seconds_count",
        "dexzd_step_schedule_to_start_latency_seconds_count",
        "dexzd_worker_stream_events_total",
    ] {
        assert!(
            sample_sum(body, name) > sample_sum(before_body, name),
            "{name} did not move"
        );
    }
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    assert_recovery_replays_without_counting_again(&primary.endpoint(), directory.path()).await;
}

/// Checks that a fresh node replaying the scenario's log reports replayed bytes and does not
/// count the replayed Flow closes and Step failures again.
async fn assert_recovery_replays_without_counting_again(primary_endpoint: &str, directory: &Path) {
    let recovered_address = free_loopback_address();
    let recovered_metrics_address = free_loopback_address();
    let mut recovered = spawn_serve_with_metrics(
        primary_endpoint,
        directory,
        "recovered-metrics",
        recovered_address,
        recovered_metrics_address,
        Some(MetricsLevel::Verbose),
        "",
    );
    recovered.wait_until_serving().await;
    let replay_scrape = scrape(recovered_metrics_address, "/metrics").await;
    let replay_body = replay_scrape
        .split_once("\r\n\r\n")
        .expect("recovery scrape has a body")
        .1;
    assert!(sample_sum(replay_body, "dexzd_recovery_last_replayed_bytes") > 0.0);
    for name in [
        "dexzd_flow_closes_total",
        "dexzd_flow_end_to_end_latency_seconds_count",
        "dexzd_step_attempt_failures_total",
        "dexzd_step_retries_exhausted_total",
    ] {
        assert_eq!(sample_sum(replay_body, name), 0.0, "replay counted {name}");
    }
    let status = recovered.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", recovered.log());
}

/// Starts a serve process with a metrics listener at `level`, or without `metrics.level` when
/// `level` is `None`, and appends `extra_sections` to its configuration.
fn spawn_serve_with_metrics(
    primary_endpoint: &str,
    directory: &Path,
    name: &str,
    api_address: SocketAddr,
    metrics_address: SocketAddr,
    level: Option<MetricsLevel>,
    extra_sections: &str,
) -> ServeProcess {
    let configuration_path = directory.join(format!("{name}.yaml"));
    ServeConfig {
        primary_endpoint,
        hard_delete_endpoint: None,
        listen: api_address,
        local_directory: &directory.join(format!("{name}-store")),
        lease_duration: HELD_LEASE_DURATION,
        fail_if_held: true,
        parade_db: None,
    }
    .write(&configuration_path);
    let mut configuration =
        fs::read_to_string(&configuration_path).expect("configuration is readable");
    configuration.push_str(&format!("metrics:\n  listen: '{metrics_address}'\n"));
    if let Some(level) = level {
        configuration.push_str(&format!("  level: {}\n", level.yaml_name()));
    }
    configuration.push_str(extra_sections);
    fs::write(&configuration_path, configuration).expect("configuration is writable");
    ServeProcess::spawn(
        &configuration_path,
        api_address,
        directory.join(format!("{name}.log")),
    )
}

/// Asserts that `body` exports exactly the families of `level`.
fn assert_exported_families(body: &str, level: MetricsLevel) {
    let exported = body
        .lines()
        .filter_map(|line| line.strip_prefix("# TYPE "))
        .filter_map(|line| line.split_whitespace().next())
        .collect::<BTreeSet<_>>();
    assert_eq!(exported, exported_family_names_for_test(level), "{body}");
    // These anchors do not read the tier table, so they catch a wrong table row.
    let has_family = |name: &str| exported.contains(name);
    assert!(has_family("dexzd_node_role") && has_family("dexzd_commit_latency_seconds"));
    assert!(!has_family("dexzd_commit_batches"));
    assert_eq!(
        has_family("dexzd_local_storage_bytes"),
        level >= MetricsLevel::Advanced
    );
    assert_eq!(
        has_family("dexzd_commit_records"),
        level >= MetricsLevel::Advanced
    );
    assert_eq!(
        has_family("dexzd_takeover_duration_seconds"),
        level == MetricsLevel::Verbose
    );
    assert_eq!(
        has_family("process_cpu_seconds"),
        cfg!(target_os = "linux") && level == MetricsLevel::Verbose
    );
}

/// Asserts that every family in `body` has a sample; some families add samples only on use.
fn assert_every_family_has_a_sample(body: &str) {
    for family in body
        .lines()
        .filter_map(|line| line.strip_prefix("# TYPE "))
        .filter_map(|line| line.split_whitespace().next())
    {
        assert!(
            body.lines().any(|line| {
                line.strip_prefix(family).is_some_and(|tail| {
                    tail.starts_with([' ', '{'])
                        || ["_total", "_bucket", "_count", "_sum"]
                            .iter()
                            .any(|suffix| tail.starts_with(suffix))
                })
            }),
            "{family} has no sample: {body}"
        );
    }
}

/// Returns the most series a scenario scrape may have at `level`.
fn series_cap(level: MetricsLevel) -> usize {
    let dexzd_series_cap = if level == MetricsLevel::Basic {
        BASIC_SERIES_CAP
    } else if level == MetricsLevel::Advanced {
        ADVANCED_SERIES_CAP
    } else {
        VERBOSE_SERIES_CAP
    };
    // Linux adds one series for each of the four process families at verbose.
    if cfg!(target_os = "linux") && level == MetricsLevel::Verbose {
        dexzd_series_cap + 4
    } else {
        dexzd_series_cap
    }
}

fn effective_config_line(process: &ServeProcess) -> String {
    let log = process.log();
    log.lines()
        .find(|line| line.contains("effective configuration"))
        .unwrap_or_else(|| panic!("missing the effective configuration line:\n{log}"))
        .to_owned()
}

fn http_body(response: &str) -> &str {
    response.split_once("\r\n\r\n").expect("HTTP body exists").1
}

async fn scrape(address: SocketAddr, path: &str) -> String {
    try_scrape(address, path)
        .await
        .expect("the metrics listener answers")
}

/// Returns the raw HTTP response to `GET path`, or the error of a listener that is not up yet.
pub(crate) async fn try_scrape(address: SocketAddr, path: &str) -> std::io::Result<String> {
    let mut stream = tokio::net::TcpStream::connect(address).await?;
    stream
        .write_all(
            format!("GET {path} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
                .as_bytes(),
        )
        .await?;
    let mut response = Vec::new();
    stream.read_to_end(&mut response).await?;
    Ok(String::from_utf8(response).expect("metrics text is UTF-8"))
}

fn sample_sum(body: &str, name: &str) -> f64 {
    body.lines()
        .filter(|line| line.starts_with(name))
        .map(|line| {
            line.split_whitespace()
                .last()
                .expect("sample has value")
                .parse::<f64>()
                .expect("numeric sample")
        })
        .sum()
}

pub(crate) fn sample_with_label(body: &str, name: &str, label: &str, value: &str) -> f64 {
    let prefix = format!("{name}{{{label}=\"{value}\"}}");
    body.lines()
        .find(|line| line.starts_with(&prefix))
        .expect("labelled sample exists")
        .split_whitespace()
        .last()
        .expect("sample has value")
        .parse::<f64>()
        .expect("numeric sample")
}
