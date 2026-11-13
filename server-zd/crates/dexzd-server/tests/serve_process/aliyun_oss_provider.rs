// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `objectStore.provider: aliyunOSS` against s3local's Aliyun OSS emulation.

use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowDefinitionUpload, FlowDefinitionUploadOutcome,
    FlowStartOptions, FlowStatus, GetAttributesRequest, GetFlowDefinitionsRequest,
    GetFlowSummaryRequest, GetHistoryEventsRequest, SetAttributesRequest, StartFlowRequest,
    StepOptions, StopFlowRequest, StopType, UploadFlowDefinitionsRequest, Value,
    WaitForFlowRequest, WorkerTarget, value,
};
use s3local::VersioningState;
use s3local_server::{S3ApiDialect, SignatureVerification};
use tempfile::TempDir;
use tonic::transport::Channel;

use super::harness::{
    S3Emulator, ServeConfig, ServeProcess, TestObjectStoreProvider, free_loopback_address,
};
use super::metrics::try_scrape;
use super::{
    FLOW_TYPE, HARD_DELETE_BUCKET, HELD_LEASE_DURATION, NODE_ID, PRIMARY_BUCKET, PRIMARY_PREFIX,
    SHUTDOWN_BUDGET, run_greeting_flow, start_greeting_worker, string_value,
};

/// Snapshots and log cleanup run every second, so retention and cleanup delete objects soon.
const SNAPSHOT_INTERVAL: &str = "1s";
const LOG_DELETION_DELAY: &str = "1s";
/// The smallest multipart threshold the server accepts.
const MULTIPART_THRESHOLD_BYTES: u64 = 5 * 1024 * 1024;
/// Larger than the threshold after compression, so its log batch and snapshot file use multipart.
const LARGE_INPUT_BYTES: usize = 12 * 1024 * 1024;
const CLEANUP_BUDGET: Duration = Duration::from_secs(60);
const FAILED_STARTUP_BUDGET: Duration = Duration::from_secs(30);
const OFFLOAD_FLOW_ID: &str = "aliyun-offloaded-history";
const HARD_DELETE_FLOW_ID: &str = "aliyun-hard-delete";
/// Enough small writes to seal several 4 KiB history segments and fill more than one page.
const OFFLOAD_WRITES: usize = 1_100;
const UPLOAD_BUDGET: Duration = Duration::from_secs(60);
const OBJECT_STORE_PAGE_READS: &str =
    "dexzd_history_reads_total{kind=\"page\",source=\"object_store\"} ";
/// A Flow type with `.`, which a Flow definition object key keeps.
const DEFINITION_FLOW_TYPE: &str = "Order_Flow-v1.2";

/// One serve process per call, all on `local_directory` and the emulator at `endpoint`.
struct AliyunOssServer<'a> {
    directory: &'a Path,
    endpoint: &'a str,
    local_directory: PathBuf,
    hard_delete_endpoint: Option<&'a str>,
    /// YAML sections appended to the configuration.
    extra_config: &'a str,
    /// The `objectStore.versionDiscovery` value, or `None` for the default.
    version_discovery: Option<&'a str>,
}

impl AliyunOssServer<'_> {
    fn spawn(&self, name: &str) -> ServeProcess {
        let address = free_loopback_address();
        let config = self.write_config(name, address);
        ServeProcess::spawn(&config, address, self.directory.join(format!("{name}.log")))
    }

    fn write_config(&self, name: &str, listen: SocketAddr) -> PathBuf {
        let path = self.directory.join(format!("{name}.yaml"));
        ServeConfig {
            primary_endpoint: self.endpoint,
            hard_delete_endpoint: self.hard_delete_endpoint,
            listen,
            local_directory: &self.local_directory,
            lease_duration: HELD_LEASE_DURATION,
            fail_if_held: true,
            parade_db: None,
        }
        .write_with_snapshot_schedule(&path, SNAPSHOT_INTERVAL, LOG_DELETION_DELAY);
        let mut contents = std::fs::read_to_string(&path).expect("the configuration is readable");
        contents.push_str(&format!(
            "commit:\n  multipartThresholdBytes: {MULTIPART_THRESHOLD_BYTES}\n{}",
            self.extra_config
        ));
        std::fs::write(&path, contents).expect("the configuration is written");
        TestObjectStoreProvider::AliyunOss.select_in_config(&path);
        if let Some(version_discovery) = self.version_discovery {
            let contents = std::fs::read_to_string(&path).expect("the configuration is readable");
            let selected = contents.replacen(
                "  provider: aliyunOSS\n",
                &format!("  provider: aliyunOSS\n  versionDiscovery: {version_discovery}\n"),
                1,
            );
            assert_ne!(selected, contents, "the configuration selects aliyunOSS");
            std::fs::write(&path, selected).expect("the configuration is written");
        }
        path
    }
}

/// Serving writes multipart objects, deletes snapshots and log batches, and recovers every Flow
/// after a warm and a cold restart.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn aliyun_oss_serve_cleans_up_objects_and_recovers_after_warm_and_cold_restarts() {
    let primary = S3Emulator::start_with_dialect(
        PRIMARY_BUCKET,
        SignatureVerification::Strict,
        S3ApiDialect::AliyunOss,
    )
    .await;
    let endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let server = AliyunOssServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("store"),
        hard_delete_endpoint: None,
        extra_config: "",
        version_discovery: None,
    };

    let mut first = server.spawn("first");
    let channel = first.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    let large_input = pseudo_random_text(LARGE_INPUT_BYTES);
    let mut acknowledged = vec![(
        "aliyun-large-flow".to_owned(),
        run_flow_with_input(&mut flow_client, "aliyun-large-flow", &large_input).await,
    )];
    let mut first_manifest: Option<String> = None;
    let deadline = tokio::time::Instant::now() + CLEANUP_BUDGET;
    loop {
        let flow_id = format!("aliyun-flow-{}", acknowledged.len());
        let run_id = run_greeting_flow(&mut flow_client, &flow_id).await;
        acknowledged.push((flow_id, run_id));
        let manifests = snapshot_manifest_keys(&primary);
        if first_manifest.is_none() {
            first_manifest = manifests.first().cloned();
        }
        let has_deleted_snapshot = first_manifest
            .as_ref()
            .is_some_and(|key| !manifests.contains(key));
        if has_deleted_snapshot && first_log_seq(&primary) > 1 {
            break;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "snapshot retention and log cleanup did not delete objects within {CLEANUP_BUDGET:?}:\n{}",
            first.log()
        );
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    let counters = primary.counters().await;
    for operation in ["CompleteMultipartUpload", "DeleteObjects"] {
        assert!(counters.contains(&format!("\"{operation}\"")), "{counters}");
    }
    assert_versioned_layout(&primary);
    worker.stop().await;
    let status = first.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", first.log());

    for restart in ["warm", "cold"] {
        if restart == "cold" {
            std::fs::remove_dir_all(&server.local_directory)
                .expect("the warm local directory is deleted");
        }
        let mut process = server.spawn(restart);
        let channel = process.wait_until_serving().await;
        assert_completed(
            &mut FlowServiceClient::new(channel),
            &acknowledged,
            &process,
        )
        .await;
        let status = process.terminate(SHUTDOWN_BUDGET).await;
        assert_eq!(status.code(), Some(0), "{}", process.log());
    }
    primary.shutdown().await;
}

/// Processes with HEAD-probing and with listing version discovery serve the same bucket in turn,
/// and each recovers every Flow the others acknowledged.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn aliyun_oss_head_and_list_version_discovery_share_a_bucket_across_restarts() {
    let primary = S3Emulator::start_with_dialect(
        PRIMARY_BUCKET,
        SignatureVerification::Strict,
        S3ApiDialect::AliyunOss,
    )
    .await;
    let endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let server_with = |version_discovery| AliyunOssServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("store"),
        hard_delete_endpoint: None,
        extra_config: "",
        version_discovery: Some(version_discovery),
    };
    let head = server_with("head");
    let mut first = head.spawn("head-first");
    let channel = first.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    let mut acknowledged = Vec::new();
    for index in 0..5 {
        let flow_id = format!("aliyun-head-flow-{index}");
        let run_id = run_greeting_flow(&mut flow_client, &flow_id).await;
        acknowledged.push((flow_id, run_id));
    }
    assert_versioned_layout(&primary);
    worker.stop().await;
    let status = first.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", first.log());

    for (name, version_discovery) in [("list-warm", "list"), ("head-cold", "head")] {
        let server = server_with(version_discovery);
        if version_discovery == "head" {
            std::fs::remove_dir_all(&server.local_directory)
                .expect("the warm local directory is deleted");
        }
        let mut process = server.spawn(name);
        let channel = process.wait_until_serving().await;
        assert_completed(
            &mut FlowServiceClient::new(channel.clone()),
            &acknowledged,
            &process,
        )
        .await;
        let worker = start_greeting_worker(channel.clone()).await;
        let flow_id = format!("aliyun-{name}-flow");
        let run_id = run_greeting_flow(&mut FlowServiceClient::new(channel), &flow_id).await;
        acknowledged.push((flow_id, run_id));
        worker.stop().await;
        let status = process.terminate(SHUTDOWN_BUDGET).await;
        assert_eq!(status.code(), Some(0), "{}", process.log());
    }
    primary.shutdown().await;
}

/// A bucket that ever had versioning ignores `x-oss-forbid-overwrite`, so startup fails.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn aliyun_oss_startup_rejects_a_bucket_that_ever_had_versioning() {
    for versioning in [VersioningState::Enabled, VersioningState::Suspended] {
        let primary = S3Emulator::start_with_dialect(
            PRIMARY_BUCKET,
            SignatureVerification::Strict,
            S3ApiDialect::AliyunOss,
        )
        .await;
        primary.set_bucket_versioning(PRIMARY_BUCKET, versioning);
        let endpoint = primary.endpoint();
        let directory = TempDir::new().expect("a temporary test directory can be created");
        let server = AliyunOssServer {
            directory: directory.path(),
            endpoint: &endpoint,
            local_directory: directory.path().join("store"),
            hard_delete_endpoint: None,
            extra_config: "",
            version_discovery: None,
        };
        let mut process = server.spawn("versioned");
        let status = process.wait_for_exit(FAILED_STARTUP_BUDGET).await;
        let log = process.log();
        assert_ne!(status.code(), Some(0), "{versioning:?}: {log}");
        assert!(
            log.contains("x-oss-forbid-overwrite") && log.contains("versioning"),
            "{versioning:?}: {log}"
        );
        primary.shutdown().await;
    }
}

/// Data that `provider: s3` wrote cannot be served with `provider: aliyunOSS`.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn aliyun_oss_startup_rejects_data_written_by_the_s3_provider() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let s3_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let address = free_loopback_address();
    let s3_config = directory.path().join("s3.yaml");
    ServeConfig {
        primary_endpoint: &s3_endpoint,
        hard_delete_endpoint: None,
        listen: address,
        local_directory: &directory.path().join("s3-store"),
        lease_duration: HELD_LEASE_DURATION,
        fail_if_held: true,
        parade_db: None,
    }
    .write(&s3_config);
    let mut s3_process = ServeProcess::spawn(&s3_config, address, directory.path().join("s3.log"));
    let channel = s3_process.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    run_greeting_flow(&mut FlowServiceClient::new(channel), "s3-flow").await;
    worker.stop().await;
    let status = s3_process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", s3_process.log());

    let aliyun_oss_endpoint = primary.start_second_endpoint(S3ApiDialect::AliyunOss).await;
    let endpoint = format!("http://{}", aliyun_oss_endpoint.local_address());
    let server = AliyunOssServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("aliyun-oss-store"),
        hard_delete_endpoint: None,
        extra_config: "",
        version_discovery: None,
    };
    let mut process = server.spawn("aliyun-oss");
    let status = process.wait_for_exit(FAILED_STARTUP_BUDGET).await;
    let log = process.log();
    assert_ne!(status.code(), Some(0), "{log}");
    assert!(
        log.contains("written by another objectStore.provider"),
        "{log}"
    );
    assert!(
        primary.has_object_under(PRIMARY_BUCKET, &format!("{PRIMARY_PREFIX}/manifest")),
        "the s3 data stays in place"
    );
    aliyun_oss_endpoint.shutdown().await;
    primary.shutdown().await;
}

/// UploadFlowDefinitions replaces a definition for a newer generation time and when forced.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn aliyun_oss_replaces_flow_definitions() {
    let primary = S3Emulator::start_with_dialect(
        PRIMARY_BUCKET,
        SignatureVerification::Strict,
        S3ApiDialect::AliyunOss,
    )
    .await;
    let endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let server = AliyunOssServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("store"),
        hard_delete_endpoint: None,
        extra_config: "",
        version_discovery: None,
    };
    let mut process = server.spawn("definitions");
    let mut flow_client = FlowServiceClient::new(process.wait_until_serving().await);
    let uploads = [
        (1, 100, false, FlowDefinitionUploadOutcome::Created),
        (2, 101, false, FlowDefinitionUploadOutcome::Replaced),
        (1, 10, true, FlowDefinitionUploadOutcome::Replaced),
    ];
    for (revision, generated_at_seconds, is_forced, expected) in uploads {
        let response = flow_client
            .upload_flow_definitions(UploadFlowDefinitionsRequest {
                definitions: vec![FlowDefinitionUpload {
                    flow_type: DEFINITION_FLOW_TYPE.to_owned(),
                    content: flow_definition_document(revision),
                    generated_at: Some(prost_types::Timestamp {
                        seconds: generated_at_seconds,
                        nanos: 0,
                    }),
                }],
                uploader: "aliyun-oss-test/1.0".to_owned(),
                force: is_forced,
            })
            .await
            .unwrap_or_else(|status| {
                panic!(
                    "UploadFlowDefinitions of revision {revision} failed: {status}\n{}",
                    process.log()
                )
            })
            .into_inner();
        assert_eq!(response.results[0].outcome(), expected, "{revision}");
        assert_eq!(
            stored_flow_definition(&mut flow_client).await,
            flow_definition_document(revision)
        );
    }
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());

    let mut restarted = server.spawn("definitions-restarted");
    let mut flow_client = FlowServiceClient::new(restarted.wait_until_serving().await);
    assert_eq!(
        stored_flow_definition(&mut flow_client).await,
        flow_definition_document(1)
    );
    let status = restarted.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", restarted.log());
    primary.shutdown().await;
}

/// Offloaded history is uploaded as segment objects and read back from object storage.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn aliyun_oss_offloaded_history_is_uploaded_and_read_back() {
    let primary = S3Emulator::start_with_dialect(
        PRIMARY_BUCKET,
        SignatureVerification::Strict,
        S3ApiDialect::AliyunOss,
    )
    .await;
    let endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let metrics = free_loopback_address();
    let extra_config = format!(
        "history:\n  segmentTargetBytes: 4096\nmetrics:\n  listen: '{metrics}'\n  level: verbose\n"
    );
    let server = AliyunOssServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("store"),
        hard_delete_endpoint: None,
        extra_config: &extra_config,
        version_discovery: None,
    };
    let mut process = server.spawn("offload");
    let mut flow_client = FlowServiceClient::new(process.wait_until_serving().await);
    start_idle_flow(&mut flow_client, OFFLOAD_FLOW_ID).await;
    for write in 0..OFFLOAD_WRITES {
        flow_client
            .set_attributes(SetAttributesRequest {
                flow_id: OFFLOAD_FLOW_ID.to_owned(),
                attributes: vec![AttributeWrite {
                    key: "counter".to_owned(),
                    value: Some(string_value(&format!("write {write}"))),
                    ..AttributeWrite::default()
                }],
                request_id: format!("write-{write}"),
                ..SetAttributesRequest::default()
            })
            .await
            .expect("SetAttributes commits");
    }
    let history_prefix = format!("{PRIMARY_PREFIX}/history/");
    wait_until(UPLOAD_BUDGET, "three history segment objects exist", || {
        primary.keys_under(PRIMARY_BUCKET, &history_prefix).len() >= 3
    })
    .await;

    let mut event_ids = Vec::new();
    let mut next_page_token = Vec::new();
    loop {
        let page = flow_client
            .get_history_events(GetHistoryEventsRequest {
                flow_id: OFFLOAD_FLOW_ID.to_owned(),
                estimate_page_size: 5_000,
                next_page_token,
                ..GetHistoryEventsRequest::default()
            })
            .await
            .unwrap_or_else(|status| panic!("GetHistoryEvents failed: {status}\n{}", process.log()))
            .into_inner();
        event_ids.extend(page.events.iter().map(|event| event.event_id));
        next_page_token = page.next_page_token;
        if next_page_token.is_empty() {
            break;
        }
    }
    let expected: Vec<i64> = (1..=i64::try_from(event_ids.len()).expect("few events")).collect();
    assert_eq!(event_ids, expected, "every event once, in order");
    assert!(event_ids.len() > OFFLOAD_WRITES);
    let deadline = tokio::time::Instant::now() + UPLOAD_BUDGET;
    while object_store_page_reads(metrics).await == 0 {
        assert!(
            tokio::time::Instant::now() < deadline,
            "no history page was read from object storage:\n{}",
            process.log()
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
}

/// A separate hard-delete destination stores, reads, and deletes a value through its own
/// aliyunOSS stack.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn aliyun_oss_hard_delete_store_keeps_and_erases_a_value() {
    let primary = S3Emulator::start_with_dialect(
        PRIMARY_BUCKET,
        SignatureVerification::Strict,
        S3ApiDialect::AliyunOss,
    )
    .await;
    let hard_delete = S3Emulator::start_with_dialect(
        HARD_DELETE_BUCKET,
        SignatureVerification::Off,
        S3ApiDialect::AliyunOss,
    )
    .await;
    let endpoint = primary.endpoint();
    let hard_delete_endpoint = hard_delete.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let server = AliyunOssServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("store"),
        hard_delete_endpoint: Some(&hard_delete_endpoint),
        extra_config: "",
        version_discovery: None,
    };
    let mut process = server.spawn("hard-delete");
    let mut flow_client = FlowServiceClient::new(process.wait_until_serving().await);
    let run_id = start_idle_flow(&mut flow_client, HARD_DELETE_FLOW_ID).await;
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: HARD_DELETE_FLOW_ID.to_owned(),
            run_id: run_id.clone(),
            attributes: vec![AttributeWrite {
                key: "secret".to_owned(),
                value: Some(string_value("credential")),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            }],
            request_id: "set-secret".to_owned(),
            ..SetAttributesRequest::default()
        })
        .await
        .expect("the hard-delete Attribute is stored");
    let stored = secret_attribute(&mut flow_client, &run_id).await;
    assert_eq!(stored, Some(string_value("credential")));
    let value_objects = hard_delete_value_keys(&hard_delete);
    assert_eq!(value_objects.len(), 1, "{value_objects:?}");

    flow_client
        .stop_flow(StopFlowRequest {
            flow_id: HARD_DELETE_FLOW_ID.to_owned(),
            run_id: run_id.clone(),
            stop_type: StopType::Terminate as i32,
            ..StopFlowRequest::default()
        })
        .await
        .expect("StopFlow closes the run");
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: HARD_DELETE_FLOW_ID.to_owned(),
            run_id: run_id.clone(),
            attributes: vec![AttributeWrite {
                key: "secret".to_owned(),
                value: Some(Value {
                    kind: Some(value::Kind::NullValue(0)),
                }),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            }],
            request_id: "erase-secret".to_owned(),
            ..SetAttributesRequest::default()
        })
        .await
        .expect("the closed run accepts a hard-delete erase");
    assert_eq!(secret_attribute(&mut flow_client, &run_id).await, None);
    wait_until(
        UPLOAD_BUDGET,
        "the hard-delete value object is deleted",
        || hard_delete_value_keys(&hard_delete).is_empty(),
    )
    .await;
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    hard_delete.shutdown().await;
    primary.shutdown().await;
}

/// Starts `flow_id` on a pull Worker that never runs, so the run stays open; returns its run ID.
async fn start_idle_flow(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) -> String {
    flow_client
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: "GreetStep".to_owned(),
            step_input: Some(string_value("idle")),
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
            request_id: format!("{flow_id}-start"),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow succeeds")
        .into_inner()
        .run_id
}

async fn secret_attribute(
    flow_client: &mut FlowServiceClient<Channel>,
    run_id: &str,
) -> Option<Value> {
    flow_client
        .get_attributes(GetAttributesRequest {
            flow_id: HARD_DELETE_FLOW_ID.to_owned(),
            run_id: run_id.to_owned(),
            all_keys: true,
            ..GetAttributesRequest::default()
        })
        .await
        .expect("GetAttributes reads the hard-delete store")
        .into_inner()
        .attributes
        .into_iter()
        .find(|attribute| attribute.key == "secret")
        .and_then(|attribute| attribute.value)
}

/// Returns the hard-delete value objects, without startup probes.
fn hard_delete_value_keys(hard_delete: &S3Emulator) -> Vec<String> {
    hard_delete
        .keys_under(HARD_DELETE_BUCKET, "hard-delete/")
        .into_iter()
        .filter(|key| !key.starts_with("hard-delete/probe/"))
        .collect()
}

/// Returns the history pages that the node read from object storage, or 0 while its metrics
/// listener is down.
async fn object_store_page_reads(metrics: SocketAddr) -> u64 {
    let Ok(body) = try_scrape(metrics, "/metrics").await else {
        return 0;
    };
    body.lines()
        .find_map(|line| line.strip_prefix(OBJECT_STORE_PAGE_READS))
        .and_then(|value| value.trim().parse().ok())
        .unwrap_or(0)
}

/// Polls `condition` until it holds, failing after `budget`.
async fn wait_until(budget: Duration, what: &str, condition: impl Fn() -> bool) {
    let deadline = tokio::time::Instant::now() + budget;
    while !condition() {
        assert!(
            tokio::time::Instant::now() < deadline,
            "{what} did not happen within {budget:?}"
        );
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
}

/// Checks that only versions hold the mutable object keys, which the adapter manages.
pub(super) fn assert_versioned_layout(primary: &S3Emulator) {
    let manifest_keys = primary.keys_under(PRIMARY_BUCKET, &format!("{PRIMARY_PREFIX}/manifest"));
    assert!(
        !manifest_keys.is_empty()
            && manifest_keys
                .iter()
                .all(|key| key.starts_with(&format!("{PRIMARY_PREFIX}/manifest.v/"))),
        "{manifest_keys:?}"
    );
    let lease_keys =
        primary.keys_under(PRIMARY_BUCKET, &format!("{PRIMARY_PREFIX}/lease/{NODE_ID}"));
    assert!(
        !lease_keys.is_empty()
            && lease_keys
                .iter()
                .all(|key| key.starts_with(&format!("{PRIMARY_PREFIX}/lease/{NODE_ID}.v/"))),
        "{lease_keys:?}"
    );
}

pub(super) async fn assert_completed(
    flow_client: &mut FlowServiceClient<Channel>,
    acknowledged: &[(String, String)],
    process: &ServeProcess,
) {
    for (flow_id, run_id) in acknowledged {
        let summary = flow_client
            .get_flow_summary(GetFlowSummaryRequest {
                flow_id: flow_id.clone(),
                run_id: String::new(),
            })
            .await
            .unwrap_or_else(|status| {
                panic!(
                    "acknowledged Flow {flow_id} is missing: {status}\n{}",
                    process.log()
                )
            })
            .into_inner();
        assert_eq!(
            summary.flow_status,
            FlowStatus::Completed as i32,
            "{flow_id}"
        );
        assert_eq!(&summary.first_run_id, run_id, "{flow_id}");
    }
}

/// Starts `flow_id` with `input` on the pull Worker, waits until it completes, and returns its
/// run ID.
async fn run_flow_with_input(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    input: &str,
) -> String {
    let started = flow_client
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: "GreetStep".to_owned(),
            step_input: Some(string_value(input)),
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
            request_id: format!("{flow_id}-request"),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow succeeds")
        .into_inner();
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Completed as i32);
    started.run_id
}

/// Returns `length` letters and digits from a fixed linear congruential sequence.
fn pseudo_random_text(length: usize) -> String {
    const ALPHABET: &[u8] = b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
    let mut state: u64 = 0x2545_f491_4f6c_dd1d;
    (0..length)
        .map(|_| {
            state = state
                .wrapping_mul(6_364_136_223_846_793_005)
                .wrapping_add(1_442_695_040_888_963_407);
            let index = usize::try_from(state >> 33).expect("the index fits in usize");
            char::from(ALPHABET[index % ALPHABET.len()])
        })
        .collect()
}

pub(super) fn snapshot_manifest_keys(primary: &S3Emulator) -> Vec<String> {
    primary
        .keys_under(
            PRIMARY_BUCKET,
            &format!("{PRIMARY_PREFIX}/snapshot/{NODE_ID}/"),
        )
        .into_iter()
        .filter(|key| key.contains("/manifest/"))
        .collect()
}

/// Returns the seq of the oldest retained incarnation-1 log batch, or 0 without one.
pub(super) fn first_log_seq(primary: &S3Emulator) -> u64 {
    primary
        .keys_under(
            PRIMARY_BUCKET,
            &format!("{PRIMARY_PREFIX}/log/{NODE_ID}/0000000001/"),
        )
        .first()
        .map_or(0, |key| {
            key.rsplit('/')
                .next()
                .and_then(|seq| seq.parse().ok())
                .unwrap_or_else(|| panic!("log key {key} ends with a seq"))
        })
}

fn flow_definition_document(revision: u32) -> Vec<u8> {
    format!(
        r#"{{"schemaVersion":"2.0","flow":{{"name":"{DEFINITION_FLOW_TYPE}"}},"revision":{revision}}}"#
    )
    .into_bytes()
}

async fn stored_flow_definition(flow_client: &mut FlowServiceClient<Channel>) -> Vec<u8> {
    let response = flow_client
        .get_flow_definitions(GetFlowDefinitionsRequest {
            flow_types: vec![DEFINITION_FLOW_TYPE.to_owned()],
        })
        .await
        .expect("GetFlowDefinitions succeeds")
        .into_inner();
    response.definitions[0].content.clone()
}
