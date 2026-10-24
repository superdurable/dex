// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Presigned GET and PUT behavior shared by strict s3mock and AWS targets.

use std::time::{Duration, SystemTime};

use aws_sdk_s3::presigning::PresigningConfig;

use super::http::{send_presigned, send_presigned_uri};
use super::support::{ConformanceTarget, read_body};

#[tokio::test]
async fn presigned_get_reads_an_object() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    target
        .client
        .put_object()
        .bucket(&bucket)
        .key("presigned-get")
        .body(b"signed read".to_vec().into())
        .send()
        .await
        .expect("fixture object is stored");
    let request = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("presigned-get")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("GET is presigned");
    let response = send_presigned(&request, None).await;
    assert_eq!(response.status, 200);
    assert_eq!(response.body, b"signed read");
    target.cleanup().await;
}

#[tokio::test]
async fn presigned_put_stores_an_object() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    let request = target
        .client
        .put_object()
        .bucket(&bucket)
        .key("presigned-put")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("PUT is presigned");
    let response = send_presigned(&request, Some(b"signed write")).await;
    assert_eq!(response.status, 200);
    let stored = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("presigned-put")
        .send()
        .await
        .expect("presigned PUT committed");
    assert_eq!(read_body(stored).await, b"signed write");
    target.cleanup().await;
}

#[tokio::test]
async fn tampered_presigned_signature_is_forbidden() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    let request = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("tampered")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("GET is presigned");
    let mut tampered = request.uri().to_owned();
    let signature_start = tampered
        .find("X-Amz-Signature=")
        .expect("presigned URL carries a signature")
        + "X-Amz-Signature=".len();
    let replacement = if &tampered[signature_start..signature_start + 1] == "0" {
        "1"
    } else {
        "0"
    };
    tampered.replace_range(signature_start..signature_start + 1, replacement);
    let response = send_presigned_uri(&request, &tampered, None).await;
    assert_eq!(response.status, 403);
    target.cleanup().await;
}

#[tokio::test]
async fn expired_presigned_url_is_forbidden() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    let config = PresigningConfig::builder()
        .start_time(SystemTime::now() - Duration::from_secs(3600))
        .expires_in(Duration::from_secs(1))
        .build()
        .expect("valid expiry");
    let request = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("expired")
        .presigned(config)
        .await
        .expect("GET is presigned");
    let response = send_presigned(&request, None).await;
    assert_eq!(response.status, 403);
    target.cleanup().await;
}
