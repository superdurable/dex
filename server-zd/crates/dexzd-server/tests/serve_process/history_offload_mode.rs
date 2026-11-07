// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! At start, the node commits `history.offloadEnabled` to every partition whose stored mode differs.

use std::fs;
use std::path::{Path, PathBuf};

use dexzd_keyspace::row_keys::history_offload_mode_key;
use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_protocol::dexzd::v1::HistoryOffloadModeRecord;
use dexzd_storage::{LocalReadView, RocksDbLocalStore, RocksDbStoreOptions};
use prost::Message;
use tempfile::TempDir;

use super::SHUTDOWN_BUDGET;
use super::harness::{ServeProcess, free_loopback_address};

const DEFAULT_OVERRIDES: &str = "dev:\n  durabilityMode: processCrashSafe\n";
const ENABLED_OVERRIDES: &str =
    "dev:\n  durabilityMode: processCrashSafe\nhistory:\n  offloadEnabled: true\n";
const DISABLED_OVERRIDES: &str =
    "dev:\n  durabilityMode: processCrashSafe\nhistory:\n  offloadEnabled: false\n";
/// The partition count of a new `dev` data directory.
const DEV_PARTITION_COUNT: u16 = 32;
const MODE_LOG_TEXT: &str = "history offload mode";

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn startup_commits_the_configured_history_offload_mode_to_every_partition() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");

    let log = serve_once(
        directory.path(),
        &data_directory,
        "default",
        DEFAULT_OVERRIDES,
    )
    .await;
    assert!(!log.contains(MODE_LOG_TEXT), "{log}");
    assert_eq!(
        stored_modes(&data_directory),
        vec![None; usize::from(DEV_PARTITION_COUNT)]
    );

    let log = serve_once(
        directory.path(),
        &data_directory,
        "enabled",
        ENABLED_OVERRIDES,
    )
    .await;
    assert!(log.contains(MODE_LOG_TEXT), "{log}");
    assert_eq!(
        stored_modes(&data_directory),
        vec![Some(true); usize::from(DEV_PARTITION_COUNT)]
    );

    let log = serve_once(
        directory.path(),
        &data_directory,
        "enabled-again",
        ENABLED_OVERRIDES,
    )
    .await;
    assert!(
        !log.contains(MODE_LOG_TEXT),
        "an unchanged mode proposes nothing:\n{log}"
    );
    assert_eq!(
        stored_modes(&data_directory),
        vec![Some(true); usize::from(DEV_PARTITION_COUNT)]
    );

    let log = serve_once(
        directory.path(),
        &data_directory,
        "disabled",
        DISABLED_OVERRIDES,
    )
    .await;
    assert!(log.contains(MODE_LOG_TEXT), "{log}");
    assert_eq!(
        stored_modes(&data_directory),
        vec![None; usize::from(DEV_PARTITION_COUNT)]
    );
}

/// Starts `dev` with `overrides` until it serves, stops it, and returns its log.
async fn serve_once(
    directory: &Path,
    data_directory: &Path,
    name: &str,
    overrides: &str,
) -> String {
    let config_path = directory.join(format!("{name}.yaml"));
    fs::write(&config_path, overrides).expect("the overrides are written");
    let mut server = ServeProcess::spawn_dev(
        data_directory,
        &config_path,
        free_loopback_address(),
        PathBuf::from(directory).join(format!("{name}.log")),
    );
    server.wait_until_serving().await;
    let stopped = server.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", server.log());
    server.log()
}

/// Returns every partition's stored mode record of the stopped process; `None` means disabled.
fn stored_modes(data_directory: &Path) -> Vec<Option<bool>> {
    let store =
        RocksDbLocalStore::open(data_directory.join("store"), RocksDbStoreOptions::default())
            .expect("the stopped process's store opens");
    (0..DEV_PARTITION_COUNT)
        .map(|partition| {
            store
                .get(
                    ColumnFamily::PartitionIndex,
                    &history_offload_mode_key(PartitionId(partition)),
                )
                .expect("the store reads")
                .map(|bytes| {
                    HistoryOffloadModeRecord::decode(bytes.as_slice())
                        .expect("the mode record decodes")
                        .enabled
                })
        })
        .collect()
}
