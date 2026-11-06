// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `dev` and `local` create 32 partitions by default and reopen a 1024-partition data directory only when configured to.

use std::fs;
use std::path::{Path, PathBuf};
use std::time::Duration;

use tempfile::TempDir;

use super::SHUTDOWN_BUDGET;
use super::harness::{ServeProcess, free_loopback_address};
use super::local_command::{assert_completed_with_run, run_flow_on};

/// Sets no partition count, so the single-node default applies.
const DEFAULT_PARTITION_OVERRIDES: &str = "dev:\n  durabilityMode: processCrashSafe\n";
const EARLIER_DEFAULT_PARTITION_OVERRIDES: &str =
    "cluster:\n  partitionCount: 1024\ndev:\n  durabilityMode: processCrashSafe\n";
const DEFAULT_PARTITION_COUNT_LOG_TEXT: &str = "partition_count=32";
const MISMATCH_EXIT_BUDGET: Duration = Duration::from_secs(20);

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_new_dev_or_local_data_directory_has_32_partitions() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let default_config = write_overrides(
        directory.path(),
        "default.yaml",
        DEFAULT_PARTITION_OVERRIDES,
    );
    for (command, spawn) in [
        ("dev", ServeProcess::spawn_dev as SingleNodeSpawn),
        ("local", ServeProcess::spawn_local as SingleNodeSpawn),
    ] {
        let mut server = spawn(
            &directory.path().join(format!("{command}-data")),
            &default_config,
            free_loopback_address(),
            directory.path().join(format!("{command}.log")),
        );
        let channel = server.wait_until_serving().await;
        assert!(
            server.log().contains(DEFAULT_PARTITION_COUNT_LOG_TEXT),
            "a new {command} data directory must recover 32 partitions:\n{}",
            server.log()
        );
        run_flow_on(channel, &format!("{command}-default-partition-count-flow")).await;
        let stopped = server.terminate(SHUTDOWN_BUDGET).await;
        assert!(stopped.success(), "{}", server.log());
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_1024_partition_data_directory_fails_under_the_default_and_opens_with_its_count() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");
    let default_config = write_overrides(
        directory.path(),
        "default.yaml",
        DEFAULT_PARTITION_OVERRIDES,
    );
    let earlier_default_config = write_overrides(
        directory.path(),
        "earlier-default.yaml",
        EARLIER_DEFAULT_PARTITION_OVERRIDES,
    );
    let mut earlier = ServeProcess::spawn_dev(
        &data_directory,
        &earlier_default_config,
        free_loopback_address(),
        directory.path().join("earlier.log"),
    );
    let run_id = run_flow_on(earlier.wait_until_serving().await, "earlier-flow").await;
    let stopped = earlier.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", earlier.log());

    for (command, spawn) in [
        ("dev", ServeProcess::spawn_dev as SingleNodeSpawn),
        ("local", ServeProcess::spawn_local as SingleNodeSpawn),
    ] {
        let mut mismatched = spawn(
            &data_directory,
            &default_config,
            free_loopback_address(),
            directory.path().join(format!("{command}-mismatched.log")),
        );
        let status = mismatched.wait_for_exit(MISMATCH_EXIT_BUDGET).await;
        let log = mismatched.log();
        let expected_texts = [
            format!("opening data directory {}", data_directory.display()),
            "it records 1024 partitions, but cluster.partitionCount is 32".to_owned(),
            "set cluster.partitionCount: 1024 in the --config file".to_owned(),
            "server-zd before v0.1.20 and dexcli before 1.37.0 created 1024".to_owned(),
        ];
        let mentions_dexcli = log.contains("--zd-config");
        assert!(
            !status.success()
                && expected_texts.iter().all(|text| log.contains(text))
                && mentions_dexcli == (command == "dev"),
            "{command} with the default partition count must fail on a 1024-partition data \
             directory and explain the fix; it exited with {status}:\n{log}"
        );
    }

    let mut reopened = ServeProcess::spawn_dev(
        &data_directory,
        &earlier_default_config,
        free_loopback_address(),
        directory.path().join("reopened.log"),
    );
    let channel = reopened.wait_until_serving().await;
    assert_completed_with_run(channel, "earlier-flow", &run_id).await;
    let stopped = reopened.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", reopened.log());
}

type SingleNodeSpawn = fn(&Path, &Path, std::net::SocketAddr, PathBuf) -> ServeProcess;

fn write_overrides(directory: &Path, file_name: &str, contents: &str) -> PathBuf {
    let config_path = directory.join(file_name);
    fs::write(&config_path, contents).expect("the overrides are written");
    config_path
}
