// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Graceful shutdown closes idle connections at once and returns after the store persists its commits.

use std::net::SocketAddr;
use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use s3mock::{Durability, Store, StoreConfig};
use s3mock_server::ServerHandle;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

/// Far below the ten-second grace period for in-flight requests.
const PROMPT_CLOSE_DEADLINE: Duration = Duration::from_secs(2);
/// Exceeds the grace period plus a slow fsyncing store close on a loaded host.
const SHUTDOWN_DEADLINE: Duration = Duration::from_secs(60);

#[tokio::test]
async fn shutdown_closes_a_connection_that_never_sent_a_request_at_once() {
    let directory = tempfile::tempdir().expect("the temporary directory is writable");
    let server = start_server(directory.path()).await;
    let address = server.local_address();
    let mut unused = TcpStream::connect(address)
        .await
        .expect("the server accepts connections");
    // The accept loop takes connections in order, so this response proves `unused` was accepted.
    let response = send_request(
        address,
        "GET / HTTP/1.1\r\nhost: s3mock\r\nconnection: close\r\n\r\n",
    )
    .await;
    assert!(response.starts_with("HTTP/1.1 200"), "{response}");

    stop_while_closing_promptly(server, &mut unused).await;
}

#[tokio::test]
async fn shutdown_closes_an_idle_keep_alive_connection_at_once() {
    let directory = tempfile::tempdir().expect("the temporary directory is writable");
    let server = start_server(directory.path()).await;
    let mut idle = TcpStream::connect(server.local_address())
        .await
        .expect("the server accepts connections");
    idle.write_all(b"GET / HTTP/1.1\r\nhost: s3mock\r\n\r\n")
        .await
        .expect("the request is sent");
    let mut response = vec![0; 4096];
    let length = idle
        .read(&mut response)
        .await
        .expect("the response arrives");
    let response = String::from_utf8_lossy(&response[..length]);
    assert!(response.starts_with("HTTP/1.1 200"), "{response}");

    stop_while_closing_promptly(server, &mut idle).await;
}

#[tokio::test]
async fn shutdown_returns_after_the_relaxed_store_persists_its_commits() {
    let directory = tempfile::tempdir().expect("the temporary directory is writable");
    let server = start_server(directory.path()).await;
    let created = send_request(
        server.local_address(),
        "PUT /persisted HTTP/1.1\r\nhost: s3mock\r\nconnection: close\r\ncontent-length: 0\r\n\r\n",
    )
    .await;
    assert!(created.starts_with("HTTP/1.1 200"), "{created}");
    let stored = send_request(
        server.local_address(),
        "PUT /persisted/object HTTP/1.1\r\nhost: s3mock\r\nconnection: close\r\ncontent-length: 5\r\n\r\nhello",
    )
    .await;
    assert!(stored.starts_with("HTTP/1.1 200"), "{stored}");
    stop(server).await;

    let restarted = start_server(directory.path()).await;
    let read = send_request(
        restarted.local_address(),
        "GET /persisted/object HTTP/1.1\r\nhost: s3mock\r\nconnection: close\r\n\r\n",
    )
    .await;
    assert!(read.starts_with("HTTP/1.1 200"), "{read}");
    assert!(read.ends_with("\r\n\r\nhello"), "{read}");
    stop(restarted).await;
}

/// Serves a relaxed store, whose commits reach disk only when the store closes.
async fn start_server(directory: &Path) -> ServerHandle {
    let store = Store::open(&StoreConfig {
        root_directory: directory.to_path_buf(),
        durability: Durability::Relaxed,
        etag_mode: s3mock::EtagMode::Md5,
    })
    .expect("the store opens");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("a loopback port is free");
    s3mock_server::start(Arc::new(store), listener).expect("the server starts")
}

/// Sends `request` on a new connection and returns the whole response.
async fn send_request(address: SocketAddr, request: &str) -> String {
    let mut stream = TcpStream::connect(address)
        .await
        .expect("the server accepts connections");
    stream
        .write_all(request.as_bytes())
        .await
        .expect("the request is sent");
    let mut response = String::new();
    stream
        .read_to_string(&mut response)
        .await
        .expect("the response arrives");
    response
}

/// Shuts `server` down and requires `stream` to close promptly while the shutdown still runs.
async fn stop_while_closing_promptly(server: ServerHandle, stream: &mut TcpStream) {
    let shutdown = tokio::spawn(server.shutdown());
    let mut remainder = Vec::new();
    tokio::time::timeout(PROMPT_CLOSE_DEADLINE, stream.read_to_end(&mut remainder))
        .await
        .expect("shutdown closes a connection without a request in flight at once")
        .expect("the connection closes without an error");
    assert!(
        remainder.is_empty(),
        "{}",
        String::from_utf8_lossy(&remainder)
    );
    tokio::time::timeout(SHUTDOWN_DEADLINE, shutdown)
        .await
        .expect("shutdown finishes")
        .expect("the shutdown task does not panic");
}

async fn stop(server: ServerHandle) {
    tokio::time::timeout(SHUTDOWN_DEADLINE, server.shutdown())
        .await
        .expect("shutdown finishes");
}
