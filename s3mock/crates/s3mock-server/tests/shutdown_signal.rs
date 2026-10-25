// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The s3mock binary finishes in-flight requests and exits 0 on SIGTERM and SIGINT.

#![cfg(unix)]

use std::process::Stdio;
use std::time::Duration;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio::process::{Child, Command};

const STARTUP_DEADLINE: Duration = Duration::from_secs(30);
const EXIT_DEADLINE: Duration = Duration::from_secs(30);

#[tokio::test]
async fn sigterm_and_sigint_finish_in_flight_requests_and_exit_zero() {
    for signal in ["TERM", "INT"] {
        let data_directory = tempfile::tempdir().expect("the temporary directory is writable");
        let port = free_port();
        let mut server = Command::new(env!("CARGO_BIN_EXE_s3mock"))
            .arg("--listen-address")
            .arg(format!("127.0.0.1:{port}"))
            .arg("--data-directory")
            .arg(data_directory.path())
            .arg("--lifecycle-scan-interval-seconds")
            .arg("0")
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .kill_on_drop(true)
            .spawn()
            .expect("the s3mock binary starts");
        let created = request_when_ready(port, b"PUT /signals HTTP/1.1\r\nhost: s3mock\r\nconnection: close\r\ncontent-length: 0\r\n\r\n").await;
        assert!(created.starts_with("HTTP/1.1 200"), "{created}");
        let mut in_flight = TcpStream::connect(("127.0.0.1", port))
            .await
            .expect("the server accepts connections");
        in_flight
            .write_all(b"PUT /signals/object HTTP/1.1\r\nhost: s3mock\r\nconnection: close\r\ncontent-length: 10\r\n\r\nfirst")
            .await
            .expect("the request starts");
        send_signal(&server, signal).await;
        in_flight
            .write_all(b"-half")
            .await
            .expect("the in-flight request may finish after the signal");
        let mut response = String::new();
        in_flight
            .read_to_string(&mut response)
            .await
            .expect("the in-flight response arrives");
        assert!(
            response.starts_with("HTTP/1.1 200"),
            "SIG{signal}: {response}"
        );
        let status = tokio::time::timeout(EXIT_DEADLINE, server.wait())
            .await
            .expect("the server exits after the signal")
            .expect("the exit status is readable");
        assert_eq!(status.code(), Some(0), "SIG{signal}");
    }
}

/// Retries until the server listens, then returns the response to `request`.
async fn request_when_ready(port: u16, request: &[u8]) -> String {
    let deadline = tokio::time::Instant::now() + STARTUP_DEADLINE;
    loop {
        match TcpStream::connect(("127.0.0.1", port)).await {
            Ok(mut stream) => {
                stream
                    .write_all(request)
                    .await
                    .expect("the request is sent");
                let mut response = String::new();
                stream
                    .read_to_string(&mut response)
                    .await
                    .expect("the response arrives");
                return response;
            }
            Err(error) => {
                assert!(
                    tokio::time::Instant::now() < deadline,
                    "s3mock did not listen on port {port}: {error}"
                );
                tokio::time::sleep(Duration::from_millis(50)).await;
            }
        }
    }
}

async fn send_signal(server: &Child, signal: &str) {
    let process_id = server.id().expect("the server is running");
    let status = Command::new("kill")
        .arg(format!("-{signal}"))
        .arg(process_id.to_string())
        .status()
        .await
        .expect("kill runs");
    assert!(status.success(), "kill -{signal} {process_id}");
}

fn free_port() -> u16 {
    std::net::TcpListener::bind("127.0.0.1:0")
        .and_then(|listener| listener.local_addr())
        .expect("a free loopback port exists")
        .port()
}
