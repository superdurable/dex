// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Library defaults remain independent of command-line environment settings.

use clap::Parser;

#[test]
fn debug_configuration_redacts_the_root_signing_secret() {
    let config = s3local_server::ServerConfig::parse_from([
        "s3local",
        "--secret-access-key",
        "private-root-signing-secret",
    ]);
    let output = format!("{config:?}");
    assert!(output.contains("<redacted>"));
    assert!(!output.contains("private-root-signing-secret"));
}

#[test]
fn library_defaults_ignore_process_environment() {
    const MARKER: &str = "S3LOCAL_LIBRARY_DEFAULTS_CHILD";
    if s3local::read_environment_setting(MARKER).as_deref() != Some(std::ffi::OsStr::new("1")) {
        let output = std::process::Command::new(
            std::env::current_exe().expect("the test executable exists"),
        )
        .args([
            "--exact",
            "configuration::library_defaults_ignore_process_environment",
            "--nocapture",
        ])
        .env(MARKER, "1")
        .env("S3LOCAL_SIGNATURE_VERIFICATION", "strict")
        .env("S3LOCAL_ACCESS_KEY_ID", "unexpected")
        .env("S3LOCAL_FAULT_SEED", "invalid-integer")
        .output()
        .expect("the child test starts");
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        return;
    }
    let config = s3local_server::ServerConfig::default();
    assert_eq!(
        config.signature_verification,
        s3local_server::SignatureVerification::Off
    );
    assert_eq!(config.access_key_id, "s3local");
    assert_eq!(config.fault_seed, 0);
    tokio::runtime::Runtime::new()
        .expect("the runtime starts")
        .block_on(async {
            let directory = tempfile::tempdir().expect("the store directory exists");
            let store = s3local::Store::open(&s3local::StoreConfig {
                root_directory: directory.path().to_path_buf(),
                durability: s3local::Durability::Relaxed,
                etag_mode: s3local::EtagMode::Md5,
            })
            .expect("the store opens");
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
                .await
                .expect("the listener binds");
            let server = s3local_server::start(std::sync::Arc::new(store), listener)
                .expect("the server ignores invalid environment configuration");
            super::support::client(&server, "any", "credentials")
                .list_buckets()
                .send()
                .await
                .expect("default library servers accept unsigned requests");
            server.shutdown().await;
        });
}
