// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Executes SDK-presigned requests against local HTTP or AWS HTTPS endpoints.

use aws_sdk_s3::presigning::PresignedRequest;
use tokio::process::Command;

pub(super) struct HttpResponse {
    pub(super) status: u16,
    pub(super) body: Vec<u8>,
}

pub(super) async fn send_presigned(
    request: &PresignedRequest,
    body: Option<&[u8]>,
) -> HttpResponse {
    send_presigned_uri(request, request.uri(), body).await
}

pub(super) async fn send_presigned_uri(
    request: &PresignedRequest,
    uri: &str,
    body: Option<&[u8]>,
) -> HttpResponse {
    let directory = tempfile::tempdir().expect("HTTP response directory is writable");
    let response_path = directory.path().join("response");
    let mut command = Command::new("curl");
    command
        .args([
            "--silent",
            "--show-error",
            "--path-as-is",
            "--max-time",
            "30",
        ])
        .args(["--request", request.method(), "--output"])
        .arg(&response_path)
        .args(["--write-out", "%{http_code}"]);
    for (name, value) in request.headers() {
        command.arg("--header").arg(format!("{name}: {value}"));
    }
    if let Some(body) = body {
        let body_path = directory.path().join("request");
        std::fs::write(&body_path, body).expect("HTTP request body is writable");
        command
            .arg("--data-binary")
            .arg(format!("@{}", body_path.display()));
    }
    let output = command
        .arg(uri)
        .output()
        .await
        .expect("curl is available for HTTP conformance");
    assert!(output.status.success(), "curl request failed");
    let status = std::str::from_utf8(&output.stdout)
        .expect("curl status is UTF-8")
        .parse()
        .expect("curl status is numeric");
    let body = std::fs::read(response_path).expect("HTTP response body is readable");
    HttpResponse { status, body }
}
