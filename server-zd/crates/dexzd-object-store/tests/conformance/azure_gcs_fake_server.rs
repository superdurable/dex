// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Pins the `AzureGcsObjectStore` error mapping and single-attempt writes against a scripted server.

use std::time::Duration;

use base64::Engine;
use bytes::Bytes;
use dexzd_object_store::{
    AzureBlobCredentials, AzureGcsObjectStore, AzureGcsObjectStoreConfig, AzureGcsProvider,
    GoogleCloudStorageCredentials, ObjectStore, ObjectStoreErrorKind, WriteCondition, WriteOutcome,
};

use super::fake_cloud_server::{FakeCloudServer, FakeResponse, RecordedRequest};

const AZURE_ETAG: &str = "\"0x8DEADBEEF\"";
const GCS_GENERATION: &str = "1700000000000001";
const LAST_MODIFIED: &str = "Wed, 11 Nov 2026 10:00:00 GMT";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum FakeCloud {
    AzureBlob,
    GoogleCloudStorage,
}

const CLOUDS: [FakeCloud; 2] = [FakeCloud::AzureBlob, FakeCloud::GoogleCloudStorage];

fn store_at(cloud: FakeCloud, endpoint: String) -> AzureGcsObjectStore {
    let provider = match cloud {
        FakeCloud::AzureBlob => AzureGcsProvider::AzureBlob {
            account: "devaccount".to_owned(),
            container: "dex".to_owned(),
            credentials: AzureBlobCredentials::AccountKey {
                account_key: base64::engine::general_purpose::STANDARD.encode("fake-account-key"),
            },
        },
        FakeCloud::GoogleCloudStorage => AzureGcsProvider::GoogleCloudStorage {
            bucket: "dex".to_owned(),
            credentials: GoogleCloudStorageCredentials::Anonymous,
        },
    };
    let mut config = AzureGcsObjectStoreConfig::new(provider);
    config.endpoint = Some(endpoint);
    config.key_prefix = "cluster-a".to_owned();
    config.request_timeout = Duration::from_secs(5);
    config.throttled_write_initial_backoff = Duration::from_millis(1);
    AzureGcsObjectStore::new(config).expect("the store builds")
}

fn store(cloud: FakeCloud, server: &FakeCloudServer) -> AzureGcsObjectStore {
    store_at(cloud, server.endpoint())
}

/// A token that a read of this cloud returns, so replacements send it.
fn current_token(cloud: FakeCloud) -> String {
    match cloud {
        FakeCloud::AzureBlob => AZURE_ETAG.to_owned(),
        FakeCloud::GoogleCloudStorage => format!("\"{GCS_GENERATION}\""),
    }
}

fn successful_write(cloud: FakeCloud) -> FakeResponse {
    match cloud {
        FakeCloud::AzureBlob => FakeResponse::with_headers(
            201,
            vec![
                ("etag", AZURE_ETAG.to_owned()),
                ("last-modified", LAST_MODIFIED.to_owned()),
            ],
        ),
        FakeCloud::GoogleCloudStorage => FakeResponse::with_headers(
            200,
            vec![
                ("etag", "\"d41d8cd98f00b204e9800998ecf8427e\"".to_owned()),
                ("x-goog-generation", GCS_GENERATION.to_owned()),
            ],
        ),
    }
}

fn object_metadata(cloud: FakeCloud, size: usize) -> FakeResponse {
    let mut headers = vec![
        ("content-length", size.to_string()),
        ("last-modified", LAST_MODIFIED.to_owned()),
    ];
    match cloud {
        FakeCloud::AzureBlob => headers.push(("etag", AZURE_ETAG.to_owned())),
        FakeCloud::GoogleCloudStorage => {
            headers.push(("etag", "\"d41d8cd98f00b204e9800998ecf8427e\"".to_owned()));
            headers.push(("x-goog-generation", GCS_GENERATION.to_owned()));
        }
    }
    FakeResponse::with_headers(200, headers)
}

fn listing(cloud: FakeCloud, key: &str, size: usize) -> FakeResponse {
    let body = match cloud {
        // Azure lists ETags without the quotes that its headers carry.
        FakeCloud::AzureBlob => format!(
            "<?xml version=\"1.0\" encoding=\"utf-8\"?><EnumerationResults ContainerName=\"dex\">\
             <Blobs><Blob><Name>{key}</Name><Properties><Last-Modified>{LAST_MODIFIED}</Last-Modified>\
             <Etag>{}</Etag><Content-Length>{size}</Content-Length>\
             <Content-Type>application/octet-stream</Content-Type><BlobType>BlockBlob</BlobType>\
             </Properties></Blob></Blobs><NextMarker /></EnumerationResults>",
            AZURE_ETAG.trim_matches('"')
        ),
        FakeCloud::GoogleCloudStorage => format!(
            "{{\"items\":[{{\"name\":\"{key}\",\"size\":\"{size}\",\"generation\":\"{GCS_GENERATION}\",\
             \"updated\":\"2026-11-11T10:00:00.000Z\"}}]}}"
        ),
    };
    FakeResponse::Reply {
        status: 200,
        headers: Vec::new(),
        body: body.into_bytes(),
    }
}

/// Answers an Azure Blob Batch delete with one sub-response status per blob.
fn azure_batch_delete(statuses: &[u16]) -> FakeResponse {
    let boundary = "batchresponse_fake";
    let mut body = String::new();
    for (index, status) in statuses.iter().enumerate() {
        body.push_str(&format!(
            "--{boundary}\r\nContent-Type: application/http\r\nContent-ID: {index}\r\n\r\n\
             HTTP/1.1 {status} Fake\r\nx-ms-version: 2018-11-09\r\n\r\n"
        ));
    }
    body.push_str(&format!("--{boundary}--\r\n"));
    FakeResponse::Reply {
        status: 202,
        headers: vec![(
            "content-type",
            format!("multipart/mixed; boundary={boundary}"),
        )],
        body: body.into_bytes(),
    }
}

fn azure_error(status: u16, code: &str) -> FakeResponse {
    FakeResponse::with_headers(status, vec![("x-ms-error-code", code.to_owned())])
}

fn gcs_error(status: u16, code: &str) -> FakeResponse {
    FakeResponse::Reply {
        status,
        headers: vec![("content-type", "application/xml".to_owned())],
        body: format!("<?xml version='1.0' encoding='UTF-8'?><Error><Code>{code}</Code></Error>")
            .into_bytes(),
    }
}

fn precondition_rejection(cloud: FakeCloud, condition: &WriteCondition) -> FakeResponse {
    match (cloud, condition) {
        (FakeCloud::AzureBlob, WriteCondition::CreateOnly) => azure_error(409, "BlobAlreadyExists"),
        (FakeCloud::AzureBlob, _) => azure_error(412, "ConditionNotMet"),
        (FakeCloud::GoogleCloudStorage, _) => gcs_error(412, "PreconditionFailed"),
    }
}

fn conditional_writes(cloud: FakeCloud) -> [WriteCondition; 2] {
    [
        WriteCondition::CreateOnly,
        WriteCondition::ReplaceIfEtagMatches {
            etag: current_token(cloud),
        },
    ]
}

fn assert_precondition_header(
    cloud: FakeCloud,
    condition: &WriteCondition,
    request: &RecordedRequest,
) {
    let (name, expected) = match (cloud, condition) {
        (FakeCloud::AzureBlob, WriteCondition::CreateOnly) => ("if-none-match", "*".to_owned()),
        (FakeCloud::AzureBlob, _) => ("if-match", AZURE_ETAG.to_owned()),
        (FakeCloud::GoogleCloudStorage, WriteCondition::CreateOnly) => {
            ("x-goog-if-generation-match", "0".to_owned())
        }
        (FakeCloud::GoogleCloudStorage, _) => {
            ("x-goog-if-generation-match", GCS_GENERATION.to_owned())
        }
    };
    assert_eq!(
        request.header(name),
        Some(expected.as_str()),
        "{cloud:?} {condition:?} sends {name}"
    );
}

#[tokio::test]
async fn azure_gcs_rejected_conditions_report_precondition_failed() {
    for cloud in CLOUDS {
        for condition in conditional_writes(cloud) {
            let rejection_condition = condition.clone();
            let server = FakeCloudServer::start(move |_| {
                precondition_rejection(cloud, &rejection_condition)
            })
            .await;
            let outcome = store(cloud, &server)
                .put("objects/a", Bytes::from_static(b"body"), condition.clone())
                .await
                .expect("a rejected condition is an outcome");
            assert_eq!(
                outcome,
                WriteOutcome::PreconditionFailed,
                "{cloud:?} {condition:?}"
            );
            let puts = server.requests_with_method("PUT");
            assert_eq!(puts.len(), 1, "{cloud:?} {condition:?} sends one request");
            let target = puts[0].target.replace("%2F", "/").replace("%2D", "-");
            assert!(target.contains("/cluster-a/objects/a"), "{target}");
            assert_precondition_header(cloud, &condition, &puts[0]);
        }
    }
}

#[tokio::test]
async fn azure_gcs_replacing_a_missing_object_reports_precondition_failed() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |_| match cloud {
            FakeCloud::AzureBlob => azure_error(404, "BlobNotFound"),
            FakeCloud::GoogleCloudStorage => gcs_error(404, "NoSuchKey"),
        })
        .await;
        let outcome = store(cloud, &server)
            .put(
                "objects/missing",
                Bytes::from_static(b"body"),
                WriteCondition::ReplaceIfEtagMatches {
                    etag: current_token(cloud),
                },
            )
            .await
            .expect("a missing object fails the condition");
        assert_eq!(outcome, WriteOutcome::PreconditionFailed, "{cloud:?}");
        assert_eq!(server.requests().len(), 1);
    }
}

#[tokio::test]
async fn azure_gcs_unknown_etags_fail_the_condition_without_a_request() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |_| successful_write(cloud)).await;
        let store = store(cloud, &server);
        let unknown_tokens: &[&str] = match cloud {
            FakeCloud::AzureBlob => &["0x8DEADBEEF", "\"\"", "\"a\nb\""],
            FakeCloud::GoogleCloudStorage => &["\"wrong-etag\"", "17", "\"d41d8cd98f00b204\"x"],
        };
        for token in unknown_tokens {
            let outcome = store
                .put(
                    "objects/a",
                    Bytes::from_static(b"body"),
                    WriteCondition::ReplaceIfEtagMatches {
                        etag: (*token).to_owned(),
                    },
                )
                .await
                .expect("an unknown ETag is an outcome");
            assert_eq!(
                outcome,
                WriteOutcome::PreconditionFailed,
                "{cloud:?} {token:?}"
            );
        }
        assert!(server.requests().is_empty(), "{cloud:?} sends no request");
    }
}

#[tokio::test]
async fn azure_gcs_throttled_writes_retry_a_bounded_number_of_times_then_are_transient() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(|_| FakeResponse::status(429)).await;
        let error = store(cloud, &server)
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect_err("persistent throttling fails");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::Transient,
            "{cloud:?}: {error}"
        );
        assert_eq!(
            server.requests().len(),
            4,
            "{cloud:?} retries throttling three times"
        );

        let mut is_first = true;
        let server = FakeCloudServer::start(move |_| {
            if std::mem::take(&mut is_first) {
                FakeResponse::status(429)
            } else {
                successful_write(cloud)
            }
        })
        .await;
        let outcome = store(cloud, &server)
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("a write succeeds after throttling");
        assert_eq!(
            outcome,
            WriteOutcome::Written {
                etag: current_token(cloud)
            }
        );
        assert_eq!(server.requests().len(), 2);
    }
}

#[tokio::test]
async fn azure_gcs_server_errors_on_writes_are_outcome_unknown_after_one_request() {
    for cloud in CLOUDS {
        for status in [500, 503] {
            for condition in conditional_writes(cloud) {
                let server = FakeCloudServer::start(move |_| FakeResponse::status(status)).await;
                let error = store(cloud, &server)
                    .put("objects/a", Bytes::from_static(b"body"), condition.clone())
                    .await
                    .expect_err("a server error fails the write");
                assert_eq!(
                    error.kind,
                    ObjectStoreErrorKind::OutcomeUnknown,
                    "{cloud:?} {status} {condition:?}: {error}"
                );
                assert_eq!(
                    server.requests().len(),
                    1,
                    "{cloud:?} {status} {condition:?} is never retried"
                );
            }
        }
    }
}

#[tokio::test]
async fn azure_gcs_dropped_connections_after_a_write_are_outcome_unknown_after_one_request() {
    for cloud in CLOUDS {
        for condition in conditional_writes(cloud) {
            let server = FakeCloudServer::start(|_| FakeResponse::DropConnection).await;
            let error = store(cloud, &server)
                .put("objects/a", Bytes::from_static(b"body"), condition.clone())
                .await
                .expect_err("a dropped connection fails the write");
            assert_eq!(
                error.kind,
                ObjectStoreErrorKind::OutcomeUnknown,
                "{cloud:?} {condition:?}: {error}"
            );
            assert_eq!(server.requests().len(), 1, "{cloud:?} {condition:?}");
        }
    }
}

#[tokio::test]
async fn azure_gcs_refused_connections_are_transient() {
    for cloud in CLOUDS {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("a port is free");
        let endpoint = format!("http://{}", listener.local_addr().expect("an address"));
        drop(listener);
        let error = store_at(cloud, endpoint)
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect_err("nothing listens");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::Transient,
            "{cloud:?}: {error}"
        );
    }
}

#[tokio::test]
async fn azure_gcs_deletes_of_missing_objects_succeed() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |_| match cloud {
            FakeCloud::AzureBlob => azure_batch_delete(&[404]),
            FakeCloud::GoogleCloudStorage => gcs_error(404, "NoSuchKey"),
        })
        .await;
        store(cloud, &server)
            .delete("objects/missing")
            .await
            .expect("deleting a missing object succeeds");
        assert_eq!(server.requests().len(), 1);

        let server = FakeCloudServer::start(move |_| match cloud {
            FakeCloud::AzureBlob => azure_batch_delete(&[202, 404, 202]),
            FakeCloud::GoogleCloudStorage => gcs_error(404, "NoSuchKey"),
        })
        .await;
        store(cloud, &server)
            .delete_many(&[
                "objects/a".to_owned(),
                "objects/missing".to_owned(),
                "objects/c".to_owned(),
            ])
            .await
            .expect("a batch with missing objects succeeds");

        let server = FakeCloudServer::start(|_| FakeResponse::status(503)).await;
        let error = store(cloud, &server)
            .delete("objects/a")
            .await
            .expect_err("a server error fails the delete");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::OutcomeUnknown,
            "{cloud:?}"
        );
        assert_eq!(server.requests().len(), 1);
    }
}

#[tokio::test]
async fn azure_gcs_missing_container_or_bucket_is_permanent() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |_| match cloud {
            FakeCloud::AzureBlob => azure_error(404, "ContainerNotFound"),
            FakeCloud::GoogleCloudStorage => gcs_error(404, "NoSuchBucket"),
        })
        .await;
        let error = store(cloud, &server)
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect_err("a missing bucket fails the write");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::Permanent,
            "{cloud:?}: {error}"
        );
    }
}

#[tokio::test]
async fn azure_gcs_server_errors_on_reads_retry_then_are_transient() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(|_| FakeResponse::status(503)).await;
        let error = store(cloud, &server)
            .head("objects/a")
            .await
            .expect_err("a server error fails the read");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::Transient,
            "{cloud:?}: {error}"
        );
        assert_eq!(
            server.requests().len(),
            3,
            "{cloud:?} reads try three times"
        );
    }
}

#[tokio::test]
async fn azure_gcs_etags_round_trip_through_writes_reads_listings_and_replacements() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |request| match request.method.as_str() {
            "PUT" => successful_write(cloud),
            "HEAD" => object_metadata(cloud, 4),
            "GET" => listing(cloud, "cluster-a/objects/a", 4),
            _ => FakeResponse::status(400),
        })
        .await;
        let store = store(cloud, &server);
        let WriteOutcome::Written { etag } = store
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("the write succeeds")
        else {
            panic!("the create succeeds");
        };
        assert_eq!(etag, current_token(cloud), "{cloud:?}");
        let head = store
            .head("objects/a")
            .await
            .expect("the head succeeds")
            .expect("the object exists");
        assert_eq!(head.etag, etag, "{cloud:?} head");
        let listed = store
            .list("objects/", None, 10)
            .await
            .expect("the listing succeeds");
        assert_eq!(listed.objects.len(), 1, "{cloud:?}");
        assert_eq!(listed.objects[0].key, "objects/a");
        assert_eq!(listed.objects[0].etag, etag, "{cloud:?} listing");
        assert!(listed.next_start_after.is_none());
        let replaced = store
            .put(
                "objects/a",
                Bytes::from_static(b"next"),
                WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() },
            )
            .await
            .expect("the replacement succeeds");
        assert!(matches!(replaced, WriteOutcome::Written { .. }));
        let puts = server.requests_with_method("PUT");
        assert_precondition_header(
            cloud,
            &WriteCondition::ReplaceIfEtagMatches { etag },
            &puts[1],
        );
    }
}
