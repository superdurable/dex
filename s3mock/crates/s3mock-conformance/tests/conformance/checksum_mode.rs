// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! `x-amz-checksum-mode` on GET and HEAD, signed as a header or carried by a presigned URL.

use std::time::Duration;

use aws_sdk_s3::config::http::HttpRequest;
use aws_sdk_s3::presigning::{PresignedRequest, PresigningConfig};
use aws_sdk_s3::types::{ChecksumAlgorithm, ChecksumMode, ChecksumType};

use super::http::send_presigned;
use super::support::{ConformanceTarget, read_body};

const BODY: &[u8] = b"checksummed body";

#[tokio::test]
async fn checksum_mode_header_returns_the_stored_checksum_on_get_and_head() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    let stored = put_with_sha256(&target, &bucket).await;
    let get = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("object")
        .checksum_mode(ChecksumMode::Enabled)
        .send()
        .await
        .expect("GET with checksum mode succeeds");
    assert_eq!(get.checksum_sha256(), Some(stored.as_str()));
    assert_eq!(get.checksum_type(), Some(&ChecksumType::FullObject));
    assert_eq!(read_body(get).await, BODY);
    let head = target
        .client
        .head_object()
        .bucket(&bucket)
        .key("object")
        .checksum_mode(ChecksumMode::Enabled)
        .send()
        .await
        .expect("HEAD with checksum mode succeeds");
    assert_eq!(head.checksum_sha256(), Some(stored.as_str()));
    assert_eq!(head.checksum_type(), Some(&ChecksumType::FullObject));
    target.cleanup().await;
}

/// The AWS SDK for Go v2 hoists checksum mode into presigned query parameters.
#[tokio::test]
async fn presigned_checksum_mode_query_is_accepted_without_checksum_headers() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    put_with_sha256(&target, &bucket).await;
    for parameter in ["X-Amz-Checksum-Mode=ENABLED", "x-amz-checksum-mode=ENABLED"] {
        let append_parameter = move |request: &mut HttpRequest| {
            let separator = if request.uri().contains('?') {
                '&'
            } else {
                '?'
            };
            let uri = format!("{}{separator}{parameter}", request.uri());
            request
                .set_uri(uri)
                .expect("appending a query parameter keeps the URI valid");
        };
        let get = target
            .client
            .get_object()
            .bucket(&bucket)
            .key("object")
            .customize()
            .mutate_request(append_parameter)
            .presigned(presigning_config())
            .await
            .expect("GET is presigned");
        let head = target
            .client
            .head_object()
            .bucket(&bucket)
            .key("object")
            .customize()
            .mutate_request(append_parameter)
            .presigned(presigning_config())
            .await
            .expect("HEAD is presigned");
        for request in [get, head] {
            assert!(request.uri().contains(parameter));
            let response = send_presigned(&request, None).await;
            assert_eq!(
                response.status,
                200,
                "{} with {parameter}",
                request.method()
            );
            // S3 returns checksums only when the mode is a request header.
            assert_eq!(response.header("x-amz-checksum-sha256"), None);
            if request.method() == "GET" {
                assert_eq!(response.body, BODY);
            }
        }
    }
    target.cleanup().await;
}

/// The Rust SDK sends a presigned checksum mode as an unsigned header, which S3 refuses.
#[tokio::test]
async fn presigned_checksum_mode_header_must_be_signed() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    put_with_sha256(&target, &bucket).await;
    let request = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("object")
        .checksum_mode(ChecksumMode::Enabled)
        .presigned(presigning_config())
        .await
        .expect("GET is presigned");
    assert_unsigned_checksum_mode_header(&request);
    assert_eq!(send_presigned(&request, None).await.status, 403);
    target.cleanup().await;
}

async fn put_with_sha256(target: &ConformanceTarget, bucket: &str) -> String {
    target
        .client
        .put_object()
        .bucket(bucket)
        .key("object")
        .checksum_algorithm(ChecksumAlgorithm::Sha256)
        .body(BODY.to_vec().into())
        .send()
        .await
        .expect("the object is stored")
        .checksum_sha256()
        .expect("a requested checksum is echoed")
        .to_owned()
}

fn presigning_config() -> PresigningConfig {
    PresigningConfig::expires_in(Duration::from_secs(300)).expect("the expiry is valid")
}

fn assert_unsigned_checksum_mode_header(request: &PresignedRequest) {
    assert!(
        request
            .headers()
            .any(|(name, value)| name == "x-amz-checksum-mode" && value == "ENABLED")
    );
    let signed_headers = request
        .uri()
        .split_once('?')
        .map(|(_, query)| query)
        .unwrap_or_default()
        .split('&')
        .find_map(|pair| pair.strip_prefix("X-Amz-SignedHeaders="));
    assert_eq!(signed_headers, Some("host"));
}
