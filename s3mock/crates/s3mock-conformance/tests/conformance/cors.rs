// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Bucket CORS configuration and browser requests against s3mock or AWS.

use std::time::Duration;

use aws_sdk_s3::error::ProvideErrorMetadata;
use aws_sdk_s3::presigning::PresigningConfig;
use aws_sdk_s3::primitives::ByteStream;
use aws_sdk_s3::types::{CorsConfiguration, CorsRule};

use super::http::{send_presigned_with_headers, send_unsigned};
use super::support::ConformanceTarget;

const ORIGIN: &str = "https://browser.example.test";

#[tokio::test]
async fn cors_configuration_round_trips_survives_restart_and_deletes() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    put_browser_cors(&target, &bucket).await;
    let first = target
        .client
        .get_bucket_cors()
        .bucket(&bucket)
        .send()
        .await
        .expect("CORS configuration is readable");
    assert_eq!(first.cors_rules().len(), 1);
    assert_eq!(first.cors_rules()[0].allowed_origins(), &[ORIGIN]);
    assert_eq!(first.cors_rules()[0].max_age_seconds(), Some(600));
    if target.is_s3mock() {
        target.restart_s3mock().await;
        let persisted = target
            .client
            .get_bucket_cors()
            .bucket(&bucket)
            .send()
            .await
            .expect("CORS configuration survives restart");
        assert_eq!(persisted.cors_rules()[0].allowed_origins(), &[ORIGIN]);
    }
    target
        .client
        .delete_bucket_cors()
        .bucket(&bucket)
        .send()
        .await
        .expect("CORS configuration deletes");
    let missing = target
        .client
        .get_bucket_cors()
        .bucket(&bucket)
        .send()
        .await
        .expect_err("deleted CORS configuration is absent");
    assert_eq!(
        missing.as_service_error().expect("S3 error").code(),
        Some("NoSuchCORSConfiguration")
    );
    target.cleanup().await;
}

#[tokio::test]
async fn browser_preflight_and_presigned_put_get_head_use_bucket_cors() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    put_browser_cors(&target, &bucket).await;
    let expiry = PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry");
    let put_request = target
        .client
        .put_object()
        .bucket(&bucket)
        .key("browser-transfer")
        .content_type("text/plain")
        .metadata("sha256", "example")
        .presigned(expiry.clone())
        .await
        .expect("PUT is presigned");
    let preflight = send_unsigned(
        "OPTIONS",
        put_request.uri(),
        &[
            ("Origin", ORIGIN),
            ("Access-Control-Request-Method", "PUT"),
            (
                "Access-Control-Request-Headers",
                "Content-Type, IF-NONE-MATCH, X-Amz-Checksum-Sha256, x-amz-meta-sha256",
            ),
        ],
    )
    .await;
    assert_eq!(preflight.status, 200);
    assert_eq!(
        preflight.header("access-control-allow-origin"),
        Some(ORIGIN)
    );
    assert!(
        preflight
            .header("access-control-allow-methods")
            .is_some_and(|methods| methods.contains("PUT"))
    );
    assert!(
        preflight
            .header("access-control-allow-headers")
            .is_some_and(|headers| headers
                .to_ascii_lowercase()
                .contains("x-amz-checksum-sha256"))
    );
    assert_eq!(
        preflight.header("access-control-expose-headers"),
        Some("ETag")
    );
    assert_eq!(preflight.header("access-control-max-age"), Some("600"));
    assert_eq!(
        preflight.header("access-control-allow-credentials"),
        Some("true")
    );
    assert!(
        preflight
            .header("vary")
            .is_some_and(|vary| vary.contains("Origin"))
    );
    let put = send_presigned_with_headers(
        &put_request,
        put_request.uri(),
        Some(b"cors write"),
        &[("Origin", ORIGIN)],
    )
    .await;
    assert_eq!(put.status, 200);
    assert_eq!(put.header("access-control-allow-origin"), Some(ORIGIN));
    assert_eq!(put.header("access-control-expose-headers"), Some("ETag"));
    assert_eq!(put.header("access-control-allow-credentials"), Some("true"));
    let get_request = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("browser-transfer")
        .presigned(expiry.clone())
        .await
        .expect("GET is presigned");
    let get =
        send_presigned_with_headers(&get_request, get_request.uri(), None, &[("Origin", ORIGIN)])
            .await;
    assert_eq!(get.status, 200);
    assert_eq!(get.body, b"cors write");
    assert_eq!(get.header("access-control-allow-origin"), Some(ORIGIN));
    let head_request = target
        .client
        .head_object()
        .bucket(&bucket)
        .key("browser-transfer")
        .presigned(expiry)
        .await
        .expect("HEAD is presigned");
    let head = send_presigned_with_headers(
        &head_request,
        head_request.uri(),
        None,
        &[("Origin", ORIGIN)],
    )
    .await;
    assert_eq!(head.status, 200);
    assert_eq!(head.header("access-control-allow-origin"), Some(ORIGIN));
    target.cleanup().await;
}

#[tokio::test]
async fn cors_preflight_rejects_missing_configuration_and_unmatched_rules() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    let request = target
        .client
        .put_object()
        .bucket(&bucket)
        .key("rejected")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("PUT is presigned");
    let base_headers = [("Origin", ORIGIN), ("Access-Control-Request-Method", "PUT")];
    assert_eq!(
        send_unsigned("OPTIONS", request.uri(), &base_headers)
            .await
            .status,
        403
    );
    let unconfigured_actual = send_presigned_with_headers(
        &request,
        request.uri(),
        Some(b"unconfigured"),
        &[("Origin", ORIGIN)],
    )
    .await;
    assert_eq!(unconfigured_actual.status, 200);
    assert!(
        unconfigured_actual
            .header("access-control-allow-origin")
            .is_none()
    );
    put_browser_cors(&target, &bucket).await;
    for rejected in [
        vec![
            ("Origin", "https://elsewhere.example.test"),
            ("Access-Control-Request-Method", "PUT"),
        ],
        vec![
            ("Origin", ORIGIN),
            ("Access-Control-Request-Method", "DELETE"),
        ],
        vec![
            ("Origin", ORIGIN),
            ("Access-Control-Request-Method", "PUT"),
            ("Access-Control-Request-Headers", "x-forbidden"),
        ],
    ] {
        let response = send_unsigned("OPTIONS", request.uri(), &rejected).await;
        assert_eq!(response.status, 403);
        assert!(response.header("access-control-allow-origin").is_none());
    }
    let rejected_actual = send_presigned_with_headers(
        &request,
        request.uri(),
        Some(b"rejected"),
        &[("Origin", "https://elsewhere.example.test")],
    )
    .await;
    assert_eq!(rejected_actual.status, 200);
    assert!(
        rejected_actual
            .header("access-control-allow-origin")
            .is_none()
    );
    target.cleanup().await;
}

#[tokio::test]
async fn cors_wildcards_match_origin_and_requested_headers() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    let rule = CorsRule::builder()
        .allowed_origins("https://*.example.test")
        .allowed_methods("GET")
        .allowed_headers("x-amz-*")
        .build()
        .expect("wildcard CORS rule is valid");
    target
        .client
        .put_bucket_cors()
        .bucket(&bucket)
        .cors_configuration(
            CorsConfiguration::builder()
                .cors_rules(rule)
                .build()
                .expect("CORS configuration is valid"),
        )
        .send()
        .await
        .expect("wildcard CORS configuration is stored");
    let request = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("wildcard")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("GET is presigned");
    let response = send_unsigned(
        "OPTIONS",
        request.uri(),
        &[
            ("Origin", "https://app.example.test"),
            ("Access-Control-Request-Method", "GET"),
            ("Access-Control-Request-Headers", "X-Amz-Meta-Sha256"),
        ],
    )
    .await;
    assert_eq!(response.status, 200);
    assert_eq!(
        response.header("access-control-allow-origin"),
        Some("https://app.example.test")
    );
    target.cleanup().await;
}

#[tokio::test]
async fn any_origin_rule_returns_star_without_credentials() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    let rule = CorsRule::builder()
        .allowed_origins("*")
        .allowed_methods("GET")
        .build()
        .expect("any-origin CORS rule is valid");
    target
        .client
        .put_bucket_cors()
        .bucket(&bucket)
        .cors_configuration(
            CorsConfiguration::builder()
                .cors_rules(rule)
                .build()
                .expect("CORS configuration is valid"),
        )
        .send()
        .await
        .expect("any-origin CORS configuration is stored");
    target
        .client
        .put_object()
        .bucket(&bucket)
        .key("any-origin")
        .body(ByteStream::from_static(b"public"))
        .send()
        .await
        .expect("object is stored");
    let request = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("any-origin")
        .presigned(PresigningConfig::expires_in(Duration::from_secs(300)).expect("valid expiry"))
        .await
        .expect("GET is presigned");
    let preflight = send_unsigned(
        "OPTIONS",
        request.uri(),
        &[("Origin", ORIGIN), ("Access-Control-Request-Method", "GET")],
    )
    .await;
    assert_eq!(preflight.status, 200);
    assert_eq!(preflight.header("access-control-allow-origin"), Some("*"));
    assert!(
        preflight
            .header("access-control-allow-credentials")
            .is_none()
    );
    let actual =
        send_presigned_with_headers(&request, request.uri(), None, &[("Origin", ORIGIN)]).await;
    assert_eq!(actual.status, 200);
    assert_eq!(actual.body, b"public");
    assert_eq!(actual.header("access-control-allow-origin"), Some("*"));
    assert!(actual.header("access-control-allow-credentials").is_none());
    target.cleanup().await;
}

async fn put_browser_cors(target: &ConformanceTarget, bucket: &str) {
    let rule = CorsRule::builder()
        .allowed_origins(ORIGIN)
        .allowed_methods("PUT")
        .allowed_methods("GET")
        .allowed_methods("HEAD")
        .allowed_headers("content-type")
        .allowed_headers("if-none-match")
        .allowed_headers("x-amz-checksum-sha256")
        .allowed_headers("x-amz-meta-sha256")
        .expose_headers("ETag")
        .max_age_seconds(600)
        .build()
        .expect("CORS rule is valid");
    let configuration = CorsConfiguration::builder()
        .cors_rules(rule)
        .build()
        .expect("CORS configuration is valid");
    target
        .client
        .put_bucket_cors()
        .bucket(bucket)
        .cors_configuration(configuration)
        .send()
        .await
        .expect("bucket CORS is stored");
}
