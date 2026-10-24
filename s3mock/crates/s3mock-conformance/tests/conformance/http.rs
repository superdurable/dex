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
    headers: Vec<(String, String)>,
}

impl HttpResponse {
    pub(super) fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(candidate, _)| candidate.eq_ignore_ascii_case(name))
            .map(|(_, value)| value.as_str())
    }
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
    send_presigned_with_headers(request, uri, body, &[]).await
}

pub(super) async fn send_presigned_with_headers(
    request: &PresignedRequest,
    uri: &str,
    body: Option<&[u8]>,
    additional_headers: &[(&str, &str)],
) -> HttpResponse {
    let mut headers = request
        .headers()
        .map(|(name, value)| (name.to_owned(), value.to_owned()))
        .collect::<Vec<_>>();
    headers.extend(
        additional_headers
            .iter()
            .map(|(name, value)| (name.to_string(), value.to_string())),
    );
    send_http(request.method(), uri, &headers, body).await
}

pub(super) async fn send_unsigned(
    method: &str,
    uri: &str,
    headers: &[(&str, &str)],
) -> HttpResponse {
    let headers = headers
        .iter()
        .map(|(name, value)| (name.to_string(), value.to_string()))
        .collect::<Vec<_>>();
    send_http(method, uri, &headers, None).await
}

async fn send_http(
    method: &str,
    uri: &str,
    headers: &[(String, String)],
    body: Option<&[u8]>,
) -> HttpResponse {
    let directory = tempfile::tempdir().expect("HTTP response directory is writable");
    let response_path = directory.path().join("response");
    let headers_path = directory.path().join("headers");
    let mut command = Command::new("curl");
    command
        .args([
            "--silent",
            "--show-error",
            "--path-as-is",
            "--max-time",
            "30",
        ])
        .args(["--output"])
        .arg(&response_path)
        .arg("--dump-header")
        .arg(&headers_path)
        .args(["--write-out", "%{http_code}"]);
    if method == "HEAD" {
        command.arg("--head");
    } else {
        command.args(["--request", method]);
    }
    for (name, value) in headers {
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
    assert!(
        output.status.success(),
        "curl request failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    let status = std::str::from_utf8(&output.stdout)
        .expect("curl status is UTF-8")
        .parse()
        .expect("curl status is numeric");
    let body = std::fs::read(response_path).expect("HTTP response body is readable");
    let raw_headers =
        std::fs::read_to_string(headers_path).expect("HTTP response headers are readable");
    let headers = raw_headers
        .split("\r\n\r\n")
        .filter(|block| block.starts_with("HTTP/"))
        .last()
        .expect("HTTP response has a status line")
        .lines()
        .skip(1)
        .filter_map(|line| line.split_once(':'))
        .map(|(name, value)| (name.to_owned(), value.trim().to_owned()))
        .collect();
    HttpResponse {
        status,
        body,
        headers,
    }
}
