// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `serve` accepts the keys of removed history switches with a warning, except a disabled offload,
//! and each run chooses history offload when it starts.

use std::fs;
use std::path::Path;
use std::time::Duration;

use dexzd_keyspace::row_keys::history_offload_mode_key;
use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_storage::{LocalReadView, RocksDbLocalStore, RocksDbStoreOptions};
use s3local_server::SignatureVerification;
use tempfile::TempDir;

use super::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use super::{
    HELD_LEASE_DURATION, PRIMARY_BUCKET, PRIMARY_PREFIX, SHUTDOWN_BUDGET, run_greeting_flow,
    run_greeting_flow_with_offload, start_greeting_worker,
};

const REMOVED_HISTORY_KEYS: [&str; 3] = [
    "history.offloadEnabled",
    "history.deleteExpiredObjects",
    "history.maxRetainedSupersededBytes",
];
const REMOVED_KEY_WARNING: &str = "configuration uses a removed key that has no effect";
const DISABLED_OFFLOAD_ERROR: &str = "the server can no longer turn history offload off";
/// The partition count that the serve harness configures.
const PARTITION_COUNT: u16 = 16;
const UPLOAD_BUDGET: Duration = Duration::from_secs(60);
const EXIT_BUDGET: Duration = Duration::from_secs(30);

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn serve_warns_about_removed_history_keys_and_still_offloads_history() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let local_directory = directory.path().join("store");
    let listen = free_loopback_address();
    let config_path = write_serve_config(
        directory.path(),
        &primary.endpoint(),
        &local_directory,
        listen,
        "history:\n  offloadEnabled: true\n  deleteExpiredObjects: false\n  \
         maxRetainedSupersededBytes: 268435456\n",
    );
    let mut server = ServeProcess::spawn(&config_path, listen, directory.path().join("serve.log"));
    let channel = server.wait_until_serving().await;
    let log = server.log();
    assert!(log.contains(REMOVED_KEY_WARNING), "{log}");
    for key in REMOVED_HISTORY_KEYS {
        assert!(log.contains(key), "no warning names {key}:\n{log}");
    }

    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    let local_run_id =
        run_greeting_flow_with_offload(&mut flow_client, "local-history-flow", Some(false)).await;
    run_greeting_flow(&mut flow_client, "removed-history-keys-flow").await;
    let history_prefix = format!("{PRIMARY_PREFIX}/history/");
    let uploaded = tokio::time::timeout(UPLOAD_BUDGET, async {
        while primary
            .keys_under(PRIMARY_BUCKET, &history_prefix)
            .is_empty()
        {
            tokio::time::sleep(Duration::from_millis(100)).await;
        }
    })
    .await;
    assert!(
        uploaded.is_ok(),
        "the closed run's history was not uploaded:\n{}",
        server.log()
    );
    let history_keys = primary.keys_under(PRIMARY_BUCKET, &history_prefix);
    assert!(
        history_keys.iter().all(|key| !key.contains(&local_run_id)),
        "a run without offload wrote history objects: {history_keys:?}"
    );
    worker.stop().await;
    let status = server.terminate(SHUTDOWN_BUDGET).await;
    assert!(status.success(), "{}", server.log());

    let store = RocksDbLocalStore::open(&local_directory, RocksDbStoreOptions::default())
        .expect("the stopped process's store opens");
    for partition in 0..PARTITION_COUNT {
        assert!(
            store
                .get(
                    ColumnFamily::PartitionIndex,
                    &history_offload_mode_key(PartitionId(partition)),
                )
                .expect("the store reads")
                .is_none(),
            "startup commits no history offload mode to partition {partition}"
        );
    }
    primary.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn serve_refuses_to_start_when_the_configuration_disables_history_offload() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let listen = free_loopback_address();
    // Configuration parsing fails before the node contacts this endpoint.
    let config_path = write_serve_config(
        directory.path(),
        "http://127.0.0.1:9",
        &directory.path().join("store"),
        listen,
        "history:\n  offloadEnabled: false\n",
    );
    let mut server = ServeProcess::spawn(&config_path, listen, directory.path().join("serve.log"));
    let status = server.wait_for_exit(EXIT_BUDGET).await;
    let log = server.log();
    assert!(!status.success(), "{log}");
    assert!(
        log.contains(DISABLED_OFFLOAD_ERROR) && log.contains("remove history.offloadEnabled"),
        "{log}"
    );
}

/// Writes a `serve` configuration followed by `history_section`, and returns its path.
fn write_serve_config(
    directory: &Path,
    primary_endpoint: &str,
    local_directory: &Path,
    listen: std::net::SocketAddr,
    history_section: &str,
) -> std::path::PathBuf {
    let path = directory.join("serve.yaml");
    ServeConfig {
        primary_endpoint,
        hard_delete_endpoint: None,
        listen,
        local_directory,
        lease_duration: HELD_LEASE_DURATION,
        fail_if_held: false,
        parade_db: None,
    }
    .write(&path);
    let mut contents = fs::read_to_string(&path).expect("the serve configuration is readable");
    contents.push_str(history_section);
    fs::write(&path, contents).expect("the serve configuration is written");
    path
}
