// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::process::Command;

#[test]
fn db9_rejects_missing_credentials_before_serving() {
    let directory = tempfile::tempdir().unwrap();
    let configuration = directory.path().join("server.yaml");
    std::fs::write(&configuration, format!(
        "cluster:\n  nodeId: test\nobjectStore:\n  provider: db9\n  bucket: test\nlocalStorage:\n  directory: {}\n",
        directory.path().join("cache").display(),
    )).unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_dex-server-zd"))
        .args(["serve", "--config"])
        .arg(configuration)
        .env_remove("DEX_ZD_DB9_CONNECTION_STRING")
        .output()
        .unwrap();
    assert!(!result.status.success());
    assert!(String::from_utf8_lossy(&result.stderr).contains("DEX_ZD_DB9_CONNECTION_STRING"));
}

#[test]
fn db9_rejects_embedded_mode_and_never_logs_connection_secrets() {
    let directory = tempfile::tempdir().unwrap();
    let configuration = directory.path().join("server.yaml");
    std::fs::write(&configuration, "objectStore:\n  provider: db9\n").unwrap();
    let secret = "postgresql://admin:do-not-log-this-password@invalid.example/db";
    let result = Command::new(env!("CARGO_BIN_EXE_dex-server-zd"))
        .args(["dev", "--config"])
        .arg(&configuration)
        .arg("--data-dir")
        .arg(directory.path().join("data"))
        .env("DEX_ZD_DB9_CONNECTION_STRING", secret)
        .output()
        .unwrap();
    assert!(!result.status.success());
    let stderr = String::from_utf8_lossy(&result.stderr);
    assert!(stderr.contains("db9 requires serve mode"));
    assert!(!stderr.contains("do-not-log-this-password"));
}
