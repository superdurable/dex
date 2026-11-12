// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The Aliyun OSS request dialect: forbid-overwrite creates, refused conditional headers,
//! part responses without checksums, and DeleteObjects that requires Content-MD5.

use aws_sdk_s3::config::http::HttpRequest;
use aws_sdk_s3::error::ProvideErrorMetadata;
use aws_sdk_s3::types::{ChecksumAlgorithm, CompletedMultipartUpload, CompletedPart};

use super::support::RunningServer;

const ALIYUN_OSS_DIALECT: [&str; 2] = ["--api-dialect", "aliyun-oss"];

fn status_line(response: &[u8]) -> String {
    let text = String::from_utf8_lossy(response);
    text.lines().next().unwrap_or_default().to_owned()
}

fn insert_forbid_overwrite(request: &mut HttpRequest) {
    if let Some(previous) = request
        .headers_mut()
        .insert("x-oss-forbid-overwrite", "true")
    {
        panic!("the request already carried x-oss-forbid-overwrite: {previous}");
    }
}

#[tokio::test]
async fn put_object_refuses_conditional_headers_and_honors_forbid_overwrite() {
    let server = RunningServer::start(&ALIYUN_OSS_DIALECT).await;
    for condition in ["if-none-match: *\r\n", "if-match: \"etag\"\r\n"] {
        let response = server
            .raw("PUT", "/testing/conditional", condition, b"body")
            .await;
        assert_eq!(status_line(&response), "HTTP/1.1 400 Bad Request");
        assert!(String::from_utf8_lossy(&response).contains("<Code>NotImplemented</Code>"));
    }
    let missing = server
        .client
        .head_object()
        .bucket("testing")
        .key("conditional")
        .send()
        .await
        .expect_err("a refused conditional write stores nothing");
    assert_eq!(
        missing
            .raw_response()
            .map(|response| response.status().as_u16()),
        Some(404)
    );

    let forbid = "x-oss-forbid-overwrite: true\r\n";
    let created = server
        .raw("PUT", "/testing/forbidden", forbid, b"first")
        .await;
    assert_eq!(status_line(&created), "HTTP/1.1 200 OK");
    let duplicate = server
        .raw("PUT", "/testing/forbidden", forbid, b"second")
        .await;
    assert_eq!(status_line(&duplicate), "HTTP/1.1 409 Conflict");
    assert!(String::from_utf8_lossy(&duplicate).contains("<Code>FileAlreadyExists</Code>"));
    assert_eq!(server.get("forbidden").await, b"first");

    let replaced = server
        .raw(
            "PUT",
            "/testing/forbidden",
            "x-oss-forbid-overwrite: false\r\n",
            b"third",
        )
        .await;
    assert_eq!(status_line(&replaced), "HTTP/1.1 200 OK");
    assert_eq!(server.get("forbidden").await, b"third");

    let wrong_digest = server
        .raw(
            "PUT",
            "/testing/digest",
            &format!("content-md5: {}\r\n", s3local::md5_base64(b"other")),
            b"body",
        )
        .await;
    assert_eq!(status_line(&wrong_digest), "HTTP/1.1 400 Bad Request");
    assert!(String::from_utf8_lossy(&wrong_digest).contains("<Code>BadDigest</Code>"));
    let matching_digest = server
        .raw(
            "PUT",
            "/testing/digest",
            &format!("content-md5: {}\r\n", s3local::md5_base64(b"body")),
            b"body",
        )
        .await;
    assert_eq!(status_line(&matching_digest), "HTTP/1.1 200 OK");
    server.server.shutdown().await;
}

#[tokio::test]
async fn concurrent_forbid_overwrite_creates_have_exactly_one_winner() {
    let server = RunningServer::start(&ALIYUN_OSS_DIALECT).await;
    for round in 0..8 {
        let key = format!("race/{round}");
        let mut creators = tokio::task::JoinSet::new();
        for creator in 0..32 {
            let client = server.client.clone();
            let key = key.clone();
            creators.spawn(async move {
                let body = format!("creator-{creator}");
                let result = client
                    .put_object()
                    .bucket("testing")
                    .key(&key)
                    .body(body.clone().into_bytes().into())
                    .customize()
                    .mutate_request(insert_forbid_overwrite)
                    .send()
                    .await;
                (body, result)
            });
        }
        let mut winners = Vec::new();
        while let Some(joined) = creators.join_next().await {
            let (body, result) = joined.expect("a creator task completes");
            match result {
                Ok(_) => winners.push(body),
                Err(error) => {
                    assert_eq!(
                        error
                            .raw_response()
                            .map(|response| response.status().as_u16()),
                        Some(409)
                    );
                    assert_eq!(error.code(), Some("FileAlreadyExists"));
                }
            }
        }
        assert_eq!(winners.len(), 1, "round {round} has exactly one winner");
        assert_eq!(server.get(&key).await, winners[0].as_bytes());
    }
    server.server.shutdown().await;
}

#[tokio::test]
async fn multipart_completion_honors_forbid_overwrite_and_parts_omit_checksums() {
    let server = RunningServer::start(&ALIYUN_OSS_DIALECT).await;
    let mut completions = Vec::new();
    for fill in *b"ab" {
        let upload_id = server
            .client
            .create_multipart_upload()
            .bucket("testing")
            .key("multipart")
            .send()
            .await
            .expect("the upload starts")
            .upload_id()
            .expect("the upload has an ID")
            .to_owned();
        let part = server
            .client
            .upload_part()
            .bucket("testing")
            .key("multipart")
            .upload_id(&upload_id)
            .part_number(1)
            .checksum_algorithm(ChecksumAlgorithm::Crc32)
            .body(vec![fill; 1024].into())
            .send()
            .await
            .expect("the part uploads");
        assert_eq!(part.checksum_crc32(), None);
        let etag = part.e_tag().expect("the part has an ETag").to_owned();
        let completion = || {
            CompletedMultipartUpload::builder()
                .parts(
                    CompletedPart::builder()
                        .part_number(1)
                        .e_tag(etag.clone())
                        .build(),
                )
                .build()
        };
        let refused = server
            .client
            .complete_multipart_upload()
            .bucket("testing")
            .key("multipart")
            .upload_id(&upload_id)
            .multipart_upload(completion())
            .if_none_match("*")
            .send()
            .await
            .expect_err("If-None-Match is not implemented");
        assert_eq!(refused.code(), Some("NotImplemented"));
        assert_eq!(
            refused
                .raw_response()
                .map(|response| response.status().as_u16()),
            Some(400)
        );
        completions.push(
            server
                .client
                .complete_multipart_upload()
                .bucket("testing")
                .key("multipart")
                .upload_id(&upload_id)
                .multipart_upload(completion())
                .customize()
                .mutate_request(insert_forbid_overwrite)
                .send()
                .await,
        );
    }
    assert!(completions[0].is_ok(), "{:?}", completions[0]);
    let duplicate = completions[1]
        .as_ref()
        .expect_err("the second completion is refused");
    assert_eq!(
        duplicate
            .raw_response()
            .map(|response| response.status().as_u16()),
        Some(409)
    );
    assert_eq!(duplicate.code(), Some("FileAlreadyExists"));
    assert_eq!(server.get("multipart").await, vec![b'a'; 1024]);
    server.server.shutdown().await;
}

#[tokio::test]
async fn delete_objects_requires_a_matching_content_md5() {
    let server = RunningServer::start(&ALIYUN_OSS_DIALECT).await;
    server.put("delete/one", "body").await;
    let body = b"<Delete><Object><Key>delete/one</Key></Object></Delete>";
    let crc32 = s3local::ChecksumAlgorithm::Crc32.base64_checksum(body);
    for headers in [String::new(), format!("x-amz-checksum-crc32: {crc32}\r\n")] {
        let response = server.raw("POST", "/testing?delete", &headers, body).await;
        assert_eq!(status_line(&response), "HTTP/1.1 400 Bad Request");
        assert!(String::from_utf8_lossy(&response).contains("<Code>MissingArgument</Code>"));
    }
    let wrong = server
        .raw(
            "POST",
            "/testing?delete",
            &format!("content-md5: {}\r\n", s3local::md5_base64(b"other")),
            body,
        )
        .await;
    assert_eq!(status_line(&wrong), "HTTP/1.1 400 Bad Request");
    assert!(String::from_utf8_lossy(&wrong).contains("<Code>BadDigest</Code>"));
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
    let missing = server
        .client
        .head_object()
        .bucket("testing")
        .key("delete/one")
        .send()
        .await
        .expect_err("the deleted object is gone");
    assert_eq!(
        missing
            .raw_response()
            .map(|response| response.status().as_u16()),
        Some(404)
    );
    server.server.shutdown().await;
}

#[tokio::test]
async fn amazon_s3_dialect_ignores_forbid_overwrite_and_echoes_part_checksums() {
    let server = RunningServer::start(&[]).await;
    let forbid = "x-oss-forbid-overwrite: true\r\n";
    for body in [b"first".as_slice(), b"second".as_slice()] {
        let response = server.raw("PUT", "/testing/plain", forbid, body).await;
        assert_eq!(status_line(&response), "HTTP/1.1 200 OK");
    }
    assert_eq!(server.get("plain").await, b"second");
    let upload_id = server
        .client
        .create_multipart_upload()
        .bucket("testing")
        .key("multipart")
        .send()
        .await
        .expect("the upload starts")
        .upload_id()
        .expect("the upload has an ID")
        .to_owned();
    let part = server
        .client
        .upload_part()
        .bucket("testing")
        .key("multipart")
        .upload_id(&upload_id)
        .part_number(1)
        .checksum_algorithm(ChecksumAlgorithm::Crc32)
        .body(vec![b'a'; 1024].into())
        .send()
        .await
        .expect("the part uploads");
    assert!(part.checksum_crc32().is_some());
    server.server.shutdown().await;
}
