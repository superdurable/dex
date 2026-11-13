// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The Tencent COS request dialect: AppendObject with an atomic position check, ignored
//! conditional headers, and DeleteObjects that requires Content-MD5.

use std::sync::Arc;

use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::support::RunningServer;

const TENCENT_COS_DIALECT: [&str; 2] = ["--api-dialect", "tencent-cos"];

fn status_line(response: &[u8]) -> String {
    let text = String::from_utf8_lossy(response);
    text.lines().next().unwrap_or_default().to_owned()
}

/// Returns the value of the response header `name`, which must be lowercase.
fn response_header(response: &[u8], name: &str) -> Option<String> {
    let text = String::from_utf8_lossy(response);
    let head = text.split("\r\n\r\n").next().unwrap_or_default();
    head.lines().skip(1).find_map(|line| {
        let (header, value) = line.split_once(':')?;
        header
            .eq_ignore_ascii_case(name)
            .then(|| value.trim().to_owned())
    })
}

fn has_error_code(response: &[u8], code: &str) -> bool {
    String::from_utf8_lossy(response).contains(&format!("<Code>{code}</Code>"))
}

async fn append(server: &RunningServer, key: &str, position: u64, body: &[u8]) -> Vec<u8> {
    server
        .raw(
            "POST",
            &format!("/testing/{key}?append&position={position}"),
            "",
            body,
        )
        .await
}

#[tokio::test]
async fn append_creates_once_extends_at_the_object_length_and_reports_the_next_position() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    let created = append(&server, "log", 0, b"hello").await;
    assert_eq!(status_line(&created), "HTTP/1.1 200 OK");
    assert_eq!(
        response_header(&created, "x-cos-next-append-position").as_deref(),
        Some("5")
    );
    assert_eq!(response_header(&created, "etag"), None);
    let duplicate = append(&server, "log", 0, b"again").await;
    assert_eq!(status_line(&duplicate), "HTTP/1.1 409 Conflict");
    assert!(has_error_code(&duplicate, "AppendPositionErr"));
    let extended = append(&server, "log", 5, b" world").await;
    assert_eq!(status_line(&extended), "HTTP/1.1 200 OK");
    assert_eq!(
        response_header(&extended, "x-cos-next-append-position").as_deref(),
        Some("11")
    );
    let behind = append(&server, "log", 5, b"late").await;
    assert_eq!(status_line(&behind), "HTTP/1.1 409 Conflict");
    assert!(has_error_code(&behind, "AppendPositionErr"));
    assert_eq!(server.get("log").await, b"hello world");
    let absent = append(&server, "absent", 3, b"body").await;
    assert!(has_error_code(&absent, "AppendPositionErr"));

    server.put("plain", "plain").await;
    let at_start = append(&server, "plain", 0, b"body").await;
    assert_eq!(status_line(&at_start), "HTTP/1.1 409 Conflict");
    assert!(has_error_code(&at_start, "AppendPositionErr"));
    let at_end = append(&server, "plain", 5, b"body").await;
    assert_eq!(status_line(&at_end), "HTTP/1.1 409 Conflict");
    assert!(has_error_code(&at_end, "ObjectNotAppendable"));
    assert_eq!(server.get("plain").await, b"plain");

    server.put("log", "replaced").await;
    let after_put = append(&server, "log", 8, b"more").await;
    assert!(has_error_code(&after_put, "ObjectNotAppendable"));
    assert_eq!(server.get("log").await, b"replaced");
    server.server.shutdown().await;
}

/// Real COS accepts position 0 twice on an empty appendable object; the emulation keeps that.
#[tokio::test]
async fn an_empty_appendable_object_accepts_position_zero_again() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    let empty = append(&server, "empty", 0, b"").await;
    assert_eq!(status_line(&empty), "HTTP/1.1 200 OK");
    assert_eq!(
        response_header(&empty, "x-cos-next-append-position").as_deref(),
        Some("0")
    );
    let second = append(&server, "empty", 0, b"second").await;
    assert_eq!(status_line(&second), "HTTP/1.1 200 OK");
    assert_eq!(server.get("empty").await, b"second");
    let third = append(&server, "empty", 0, b"third").await;
    assert!(has_error_code(&third, "AppendPositionErr"));
    server.server.shutdown().await;
}

#[tokio::test]
async fn append_without_content_length_is_rejected() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    let mut stream = tokio::net::TcpStream::connect(server.server.local_address())
        .await
        .expect("the emulator accepts a connection");
    let request = format!(
        "POST /testing/no-length?append&position=0 HTTP/1.1\r\nhost: {}\r\nconnection: close\r\n\r\n",
        server.server.local_address()
    );
    stream
        .write_all(request.as_bytes())
        .await
        .expect("the request is sent");
    let mut response = Vec::new();
    stream
        .read_to_end(&mut response)
        .await
        .expect("the response is read");
    assert_eq!(status_line(&response), "HTTP/1.1 411 Length Required");
    assert!(has_error_code(&response, "MissingContentLength"));
    server.server.shutdown().await;
}

#[tokio::test]
async fn concurrent_appends_at_position_zero_have_exactly_one_winner() {
    let server = Arc::new(RunningServer::start(&TENCENT_COS_DIALECT).await);
    for round in 0..8 {
        let key = format!("race/{round}");
        let mut creators = tokio::task::JoinSet::new();
        for creator in 0..32 {
            let server = Arc::clone(&server);
            let key = key.clone();
            creators.spawn(async move {
                let body = format!("creator-{creator}");
                let response = append(&server, &key, 0, body.as_bytes()).await;
                (body, response)
            });
        }
        let mut winners = Vec::new();
        while let Some(joined) = creators.join_next().await {
            let (body, response) = joined.expect("a creator task completes");
            if status_line(&response) == "HTTP/1.1 200 OK" {
                winners.push(body);
            } else {
                assert!(has_error_code(&response, "AppendPositionErr"));
            }
        }
        assert_eq!(winners.len(), 1, "round {round}");
        assert_eq!(server.get(&key).await, winners[0].as_bytes());
    }
    let server = Arc::try_unwrap(server)
        .unwrap_or_else(|_| panic!("every creator task released the server"));
    server.server.shutdown().await;
}

#[tokio::test]
async fn put_object_overwrites_despite_conditional_and_forbid_overwrite_headers() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    for (headers, body) in [
        ("if-none-match: *\r\n", b"first".as_slice()),
        ("if-none-match: *\r\n", b"second".as_slice()),
        ("if-match: \"wrong\"\r\n", b"third".as_slice()),
        ("x-cos-forbid-overwrite: true\r\n", b"fourth".as_slice()),
    ] {
        let response = server.raw("PUT", "/testing/overwrite", headers, body).await;
        assert_eq!(status_line(&response), "HTTP/1.1 200 OK", "{headers}");
        assert_eq!(server.get("overwrite").await, body, "{headers}");
    }
    server.server.shutdown().await;
}

#[tokio::test]
async fn delete_objects_requires_content_md5() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    server.put("delete/one", "body").await;
    let body = b"<Delete><Object><Key>delete/one</Key></Object></Delete>";
    let missing = server.raw("POST", "/testing?delete", "", body).await;
    assert_eq!(status_line(&missing), "HTTP/1.1 400 Bad Request");
    assert!(has_error_code(&missing, "InvalidRequest"));
    assert_eq!(server.get("delete/one").await, b"body");
    let deleted = server
        .raw(
            "POST",
            "/testing?delete",
            &format!("content-md5: {}\r\n", s3local::md5_base64(body)),
            body,
        )
        .await;
    assert_eq!(status_line(&deleted), "HTTP/1.1 200 OK");
    server.server.shutdown().await;
}

#[tokio::test]
async fn other_dialects_do_not_serve_append() {
    for dialect in ["amazon-s3", "aliyun-oss"] {
        let server = RunningServer::start(&["--api-dialect", dialect]).await;
        let response = append(&server, "log", 0, b"body").await;
        assert_eq!(
            status_line(&response),
            "HTTP/1.1 501 Not Implemented",
            "{dialect}"
        );
        server.server.shutdown().await;
    }
}

#[tokio::test]
async fn append_keeps_cos_user_metadata_and_commit_then_error_reports_after_committing() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    let created = server
        .raw(
            "POST",
            "/testing/meta?append&position=0",
            "x-cos-meta-dex-append-id: 0123abcd\r\n",
            b"body",
        )
        .await;
    assert_eq!(status_line(&created), "HTTP/1.1 200 OK");
    let head = server
        .client
        .head_object()
        .bucket("testing")
        .key("meta")
        .send()
        .await
        .expect("the appended object heads");
    assert_eq!(
        head.metadata()
            .and_then(|metadata| metadata.get("dex-append-id"))
            .map(String::as_str),
        Some("0123abcd")
    );

    server
        .rules(serde_json::json!([{
            "operation": "AppendObject",
            "action": {"type": "commit_then_error", "status": 503}
        }]))
        .await;
    let failed = append(&server, "after-commit", 0, b"committed").await;
    assert_eq!(status_line(&failed), "HTTP/1.1 503 Service Unavailable");
    assert!(has_error_code(&failed, "SlowDown"));
    assert_eq!(server.get("after-commit").await, b"committed");
    server.server.shutdown().await;
}

/// Sends an append and reads whatever arrives, because an interrupted append drops the connection.
async fn append_until_closed(server: &RunningServer, key: &str, body: &[u8]) -> Vec<u8> {
    let mut stream = tokio::net::TcpStream::connect(server.server.local_address())
        .await
        .expect("the emulator accepts a connection");
    let request = format!(
        "POST /testing/{key}?append&position=0 HTTP/1.1\r\nhost: {}\r\nconnection: close\r\ncontent-length: {}\r\n\r\n",
        server.server.local_address(),
        body.len()
    );
    stream
        .write_all(request.as_bytes())
        .await
        .expect("the request head is sent");
    // The server may close before it reads the body.
    if let Err(error) = stream.write_all(body).await {
        assert_eq!(error.kind(), std::io::ErrorKind::ConnectionReset, "{error}");
    }
    let mut response = Vec::new();
    if let Err(error) = stream.read_to_end(&mut response).await {
        assert_eq!(error.kind(), std::io::ErrorKind::ConnectionReset, "{error}");
    }
    response
}

/// Returns the size and user metadata of `key`, as HEAD reports them.
async fn head_size_and_metadata(
    server: &RunningServer,
    key: &str,
) -> (i64, std::collections::HashMap<String, String>) {
    let head = server
        .client
        .head_object()
        .bucket("testing")
        .key(key)
        .send()
        .await
        .expect("the object heads");
    (
        head.content_length().unwrap_or(-1),
        head.metadata().cloned().unwrap_or_default(),
    )
}

#[tokio::test]
async fn an_append_in_flight_shows_an_empty_placeholder_and_the_commit_picks_one_winner() {
    let server = Arc::new(RunningServer::start(&TENCENT_COS_DIALECT).await);
    server
        .rules(serde_json::json!([{
            "operation": "AppendObject",
            "key_prefix": "placeholder/",
            "action": {"type": "append_placeholder", "milliseconds": 500}
        }]))
        .await;
    let mut appends = tokio::task::JoinSet::new();
    for writer in ["first", "second"] {
        let server = Arc::clone(&server);
        appends.spawn(async move {
            let response = append(&server, "placeholder/key", 0, writer.as_bytes()).await;
            (writer, response)
        });
    }
    tokio::time::sleep(std::time::Duration::from_millis(200)).await;
    let (size, metadata) = head_size_and_metadata(&server, "placeholder/key").await;
    assert_eq!(size, 0);
    assert!(metadata.is_empty(), "{metadata:?}");
    assert_eq!(server.get("placeholder/key").await, b"");
    let listing = server
        .client
        .list_objects_v2()
        .bucket("testing")
        .prefix("placeholder/")
        .send()
        .await
        .expect("the prefix lists");
    let sizes: Vec<_> = listing
        .contents()
        .iter()
        .map(|object| (object.key().unwrap_or_default(), object.size()))
        .collect();
    assert_eq!(sizes, [("placeholder/key", Some(0))]);
    let ranged = server
        .raw(
            "GET",
            "/testing/placeholder/key",
            "range: bytes=0-37\r\n",
            b"",
        )
        .await;
    assert!(
        has_error_code(&ranged, "InvalidRange"),
        "{}",
        status_line(&ranged)
    );
    let mut winners = Vec::new();
    while let Some(joined) = appends.join_next().await {
        let (writer, response) = joined.expect("an append task completes");
        if status_line(&response) == "HTTP/1.1 200 OK" {
            winners.push(writer);
        } else {
            assert!(has_error_code(&response, "AppendPositionErr"));
        }
    }
    assert_eq!(winners.len(), 1, "{winners:?}");
    assert_eq!(server.get("placeholder/key").await, winners[0].as_bytes());
    let server =
        Arc::try_unwrap(server).unwrap_or_else(|_| panic!("every append task released the server"));
    server.server.shutdown().await;
}

#[tokio::test]
async fn an_interrupted_append_leaves_an_empty_object_that_a_later_append_creates_over() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    server.put("interrupted/existing", "kept").await;
    server
        .rules(serde_json::json!([{
            "operation": "AppendObject",
            "key_prefix": "interrupted/",
            "action": {"type": "interrupted_append"}
        }]))
        .await;
    let response = append_until_closed(&server, "interrupted/key", b"body").await;
    assert!(response.is_empty(), "{}", status_line(&response));
    let (size, metadata) = head_size_and_metadata(&server, "interrupted/key").await;
    assert_eq!(size, 0);
    assert!(metadata.is_empty(), "{metadata:?}");
    append_until_closed(&server, "interrupted/existing", b"body").await;
    assert_eq!(server.get("interrupted/existing").await, b"kept");
    server.rules(serde_json::json!([])).await;
    let created = append(&server, "interrupted/key", 0, b"body").await;
    assert_eq!(status_line(&created), "HTTP/1.1 200 OK");
    assert_eq!(server.get("interrupted/key").await, b"body");
    let duplicate = append(&server, "interrupted/key", 0, b"again").await;
    assert!(has_error_code(&duplicate, "AppendPositionErr"));
    server.server.shutdown().await;
}

#[tokio::test]
async fn append_placeholder_faults_require_append_object() {
    let server = RunningServer::start(&TENCENT_COS_DIALECT).await;
    for action in [
        serde_json::json!({"type": "append_placeholder", "milliseconds": 10}),
        serde_json::json!({"type": "interrupted_append"}),
    ] {
        let (status, _) = server
            .admin(
                "PUT",
                "faults",
                serde_json::json!({"rules": [{"operation": "PutObject", "action": action}]}),
            )
            .await;
        assert_eq!(status, 400, "{action}");
    }
    let (status, _) = server
        .admin(
            "PUT",
            "faults",
            serde_json::json!({"rules": [{
                "operation": "AppendObject",
                "action": {"type": "append_placeholder", "milliseconds": 600_001}
            }]}),
        )
        .await;
    assert_eq!(status, 400);
    server.server.shutdown().await;
}
