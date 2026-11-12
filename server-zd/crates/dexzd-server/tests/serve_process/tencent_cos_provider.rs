// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `objectStore.provider: tencentCOS` against s3local's Tencent COS emulation.

use std::net::SocketAddr;
use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use s3local::VersioningState;
use s3local_server::{S3ApiDialect, SignatureVerification};
use tempfile::TempDir;

use super::aliyun_oss_provider::{
    assert_completed, assert_versioned_layout, first_log_seq, snapshot_manifest_keys,
};
use super::harness::{
    S3Emulator, ServeConfig, ServeProcess, TestObjectStoreProvider, free_loopback_address,
};
use super::{
    HELD_LEASE_DURATION, PRIMARY_BUCKET, PRIMARY_PREFIX, SHUTDOWN_BUDGET, run_greeting_flow,
    start_greeting_worker,
};

/// Snapshots and log cleanup run every second, so retention and cleanup delete objects soon.
const SNAPSHOT_INTERVAL: &str = "1s";
const LOG_DELETION_DELAY: &str = "1s";
const CLEANUP_BUDGET: Duration = Duration::from_secs(60);
const FAILED_STARTUP_BUDGET: Duration = Duration::from_secs(30);

/// One serve process per call, all on `local_directory` and the emulator at `endpoint`.
struct TencentCosServer<'a> {
    directory: &'a Path,
    endpoint: &'a str,
    local_directory: PathBuf,
}

impl TencentCosServer<'_> {
    fn spawn(&self, name: &str) -> ServeProcess {
        let address = free_loopback_address();
        let config = self.write_config(name, address);
        ServeProcess::spawn(&config, address, self.directory.join(format!("{name}.log")))
    }

    fn write_config(&self, name: &str, listen: SocketAddr) -> PathBuf {
        let path = self.directory.join(format!("{name}.yaml"));
        ServeConfig {
            primary_endpoint: self.endpoint,
            hard_delete_endpoint: None,
            listen,
            local_directory: &self.local_directory,
            lease_duration: HELD_LEASE_DURATION,
            fail_if_held: true,
            parade_db: None,
        }
        .write_with_snapshot_schedule(&path, SNAPSHOT_INTERVAL, LOG_DELETION_DELAY);
        TestObjectStoreProvider::TencentCos.select_in_config(&path);
        path
    }
}

/// Serving appends every create-only object, snapshots without empty write-ahead logs, deletes
/// snapshots and log batches, and recovers every Flow after a warm and a cold restart.
///
/// Large appends run in the object-store conformance; full-suite load stalls s3local's strict spool.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn tencent_cos_serve_cleans_up_objects_and_recovers_after_warm_and_cold_restarts() {
    let primary = S3Emulator::start_with_dialect(
        PRIMARY_BUCKET,
        SignatureVerification::Strict,
        S3ApiDialect::TencentCos,
    )
    .await;
    let endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let server = TencentCosServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("store"),
    };

    let mut first = server.spawn("first");
    let channel = first.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    let mut acknowledged = Vec::new();
    let mut first_manifest: Option<String> = None;
    let deadline = tokio::time::Instant::now() + CLEANUP_BUDGET;
    loop {
        let flow_id = format!("tencent-flow-{}", acknowledged.len());
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
    for operation in ["AppendObject", "DeleteObjects"] {
        assert!(counters.contains(&format!("\"{operation}\"")), "{counters}");
    }
    // Appends cannot create empty objects, so snapshots must leave out empty write-ahead logs.
    let empty_objects = primary.empty_object_keys_under(PRIMARY_BUCKET, PRIMARY_PREFIX);
    assert!(empty_objects.is_empty(), "{empty_objects:?}");
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

/// A bucket with versioning enabled or suspended fails the startup check.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn tencent_cos_startup_rejects_a_versioned_bucket() {
    for versioning in [VersioningState::Enabled, VersioningState::Suspended] {
        let primary = S3Emulator::start_with_dialect(
            PRIMARY_BUCKET,
            SignatureVerification::Strict,
            S3ApiDialect::TencentCos,
        )
        .await;
        primary.set_bucket_versioning(PRIMARY_BUCKET, versioning);
        let endpoint = primary.endpoint();
        let directory = TempDir::new().expect("a temporary test directory can be created");
        let server = TencentCosServer {
            directory: directory.path(),
            endpoint: &endpoint,
            local_directory: directory.path().join("store"),
        };
        let mut process = server.spawn("versioned");
        let status = process.wait_for_exit(FAILED_STARTUP_BUDGET).await;
        let log = process.log();
        assert_ne!(status.code(), Some(0), "{versioning:?}: {log}");
        assert!(
            log.contains("tencentCOS startup check") && log.contains("versioning"),
            "{versioning:?}: {log}"
        );
        assert!(
            primary
                .keys_under(PRIMARY_BUCKET, PRIMARY_PREFIX)
                .is_empty(),
            "the check writes nothing to a versioned bucket"
        );
        primary.shutdown().await;
    }
}

/// Data that `provider: s3` wrote cannot be served with `provider: tencentCOS`.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn tencent_cos_startup_rejects_data_written_by_the_s3_provider() {
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

    let tencent_cos_endpoint = primary
        .start_second_endpoint(S3ApiDialect::TencentCos)
        .await;
    let endpoint = format!("http://{}", tencent_cos_endpoint.local_address());
    let server = TencentCosServer {
        directory: directory.path(),
        endpoint: &endpoint,
        local_directory: directory.path().join("tencent-cos-store"),
    };
    let mut process = server.spawn("tencent-cos");
    let status = process.wait_for_exit(FAILED_STARTUP_BUDGET).await;
    let log = process.log();
    assert_ne!(status.code(), Some(0), "{log}");
    assert!(
        log.contains("written by another objectStore.provider"),
        "{log}"
    );
    tencent_cos_endpoint.shutdown().await;
    primary.shutdown().await;
}
