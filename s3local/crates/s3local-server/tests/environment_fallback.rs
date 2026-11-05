// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Binary configuration preserves deprecated environment settings during the rename.

use std::process::Command;

#[test]
fn deprecated_environment_variable_is_used_with_one_warning() {
    let output = Command::new(env!("CARGO_BIN_EXE_s3local"))
        .env_clear()
        .env("S3MOCK_FAULT_SEED", "19")
        .arg("--help")
        .output()
        .expect("the server binary starts");
    assert!(output.status.success());
    let stdout = String::from_utf8(output.stdout).expect("help output is UTF-8");
    assert!(stdout.contains("[default: 19]"), "{stdout}");
    assert!(stdout.contains("S3MOCK_FAULT_SEED"), "{stdout}");
    assert!(stdout.contains("S3LOCAL_FAULT_SEED"), "{stdout}");
    assert_eq!(
        stdout.matches("deprecated environment variable;").count(),
        1
    );
}

#[test]
fn current_environment_variable_wins_without_a_warning() {
    let output = Command::new(env!("CARGO_BIN_EXE_s3local"))
        .env_clear()
        .env("S3MOCK_FAULT_SEED", "19")
        .env("S3LOCAL_FAULT_SEED", "23")
        .arg("--help")
        .output()
        .expect("the server binary starts");
    assert!(output.status.success());
    let stdout = String::from_utf8(output.stdout).expect("help output is UTF-8");
    assert!(stdout.contains("[default: 23]"), "{stdout}");
    assert!(!stdout.contains("S3MOCK_FAULT_SEED"), "{stdout}");
    assert!(
        !stdout.contains("deprecated environment variable;"),
        "{stdout}"
    );
}
