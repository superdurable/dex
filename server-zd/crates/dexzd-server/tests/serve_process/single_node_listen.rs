// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dev` and `local` bind `--listen` after the data directory lock and before the embedded S3 emulator.

use std::fs::{self, File};
use std::net::{SocketAddr, TcpListener};
use std::path::{Path, PathBuf};
use std::time::Duration;

use tempfile::TempDir;

use super::harness::ServeProcess;

const SINGLE_NODE_COMMANDS: [&str; 2] = ["dev", "local"];
const SINGLE_NODE_OVERRIDES: &str = "cluster:\n  partitionCount: 16\n";
const EXIT_BUDGET: Duration = Duration::from_secs(20);
const ADDRESS_IN_USE_TEXT: &str = "Address already in use";
const EMULATOR_SERVING_TEXT: &str = "embedded S3 emulator serving";
const LOCK_HELD_TEXT: &str = "another dex-server-zd dev or local process holds";

/// The emulator's port 0 bind could take a just-freed `--listen` port, so it must come after `--listen`.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn an_occupied_listen_address_fails_before_the_embedded_s3_emulator_starts() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path());
    let occupied = occupy_loopback_port();
    let occupied_address = occupied
        .local_addr()
        .expect("the occupied listener has an address");
    for command in SINGLE_NODE_COMMANDS {
        let data_directory = directory.path().join(format!("{command}-data"));
        let mut server = ServeProcess::spawn_single_node_with_arguments(
            command,
            &data_directory,
            &config_path,
            occupied_address,
            directory.path().join(format!("{command}.log")),
            &[],
        );
        let status = server.wait_for_exit(EXIT_BUDGET).await;
        let log = server.log();
        assert!(
            !status.success()
                && log.contains(&format!("dex-server-zd: listening on {occupied_address}: "))
                && log.contains(ADDRESS_IN_USE_TEXT),
            "{command} on an occupied --listen address must fail with the listen error; it \
             exited with {status}:\n{log}"
        );
        assert!(
            !log.contains(EMULATOR_SERVING_TEXT) && !data_directory.join("s3").exists(),
            "{command} must bind --listen before the embedded S3 emulator opens its store and \
             binds its port:\n{log}"
        );
    }
    drop(occupied);
}

/// A second process on a served data directory still names the lock, even when `--listen` is also taken.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_held_data_directory_lock_fails_before_an_occupied_listen_address() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let config_path = write_overrides(directory.path());
    let occupied = occupy_loopback_port();
    let occupied_address = occupied
        .local_addr()
        .expect("the occupied listener has an address");
    for command in SINGLE_NODE_COMMANDS {
        let data_directory = directory.path().join(format!("{command}-data"));
        let held_lock = hold_data_directory_lock(&data_directory);
        let mut intruder = ServeProcess::spawn_single_node_with_arguments(
            command,
            &data_directory,
            &config_path,
            occupied_address,
            directory.path().join(format!("{command}.log")),
            &[],
        );
        let status = intruder.wait_for_exit(EXIT_BUDGET).await;
        let log = intruder.log();
        assert!(
            !status.success() && log.contains(LOCK_HELD_TEXT) && !log.contains(ADDRESS_IN_USE_TEXT),
            "{command} on a locked data directory must fail on its lock before binding --listen; \
             it exited with {status}:\n{log}"
        );
        drop(held_lock);
    }
    drop(occupied);
}

/// Returns a listener that holds a loopback port, so a server cannot bind it.
fn occupy_loopback_port() -> TcpListener {
    TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0))).expect("a loopback port is free")
}

/// Locks `dev.lock` in `data_directory` as a serving dev or local process does, until the file drops.
fn hold_data_directory_lock(data_directory: &Path) -> File {
    fs::create_dir_all(data_directory).expect("the data directory is created");
    let lock_file = File::create(data_directory.join("dev.lock")).expect("the lock file opens");
    lock_file
        .try_lock()
        .expect("the test takes the data directory lock first");
    lock_file
}

fn write_overrides(directory: &Path) -> PathBuf {
    let config_path = directory.join("overrides.yaml");
    fs::write(&config_path, SINGLE_NODE_OVERRIDES).expect("the overrides are written");
    config_path
}
