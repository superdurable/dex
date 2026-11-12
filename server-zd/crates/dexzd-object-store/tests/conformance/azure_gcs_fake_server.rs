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
    store_with(cloud, endpoint, |_| {})
}

fn store_with(
    cloud: FakeCloud,
    endpoint: String,
    configure: impl FnOnce(&mut AzureGcsObjectStoreConfig),
) -> AzureGcsObjectStore {
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
    configure(&mut config);
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
        assert_contacted_only(&store, &server);
    }
}

/// Builds a store whose writes above one byte take the multipart path.
fn large_write_store(cloud: FakeCloud, server: &FakeCloudServer) -> AzureGcsObjectStore {
    store_with(cloud, server.endpoint(), |config| {
        config.single_put_max_bytes = 1;
        config.pending_copy_timeout = Duration::from_secs(2);
    })
}

/// Answers the requests of a multipart upload, delegating the completion request to `complete`.
fn multipart_response(
    cloud: FakeCloud,
    request: &RecordedRequest,
    complete: impl FnOnce() -> FakeResponse,
) -> FakeResponse {
    let target = request.decoded_target();
    match cloud {
        FakeCloud::AzureBlob if target.contains("comp=blocklist") => complete(),
        FakeCloud::AzureBlob => FakeResponse::with_headers(201, Vec::new()),
        FakeCloud::GoogleCloudStorage if request.method == "POST" && target.contains("uploads") => {
            FakeResponse::Reply {
                status: 200,
                headers: Vec::new(),
                body: b"<InitiateMultipartUploadResult><Bucket>dex</Bucket><Key>k</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>".to_vec(),
            }
        }
        FakeCloud::GoogleCloudStorage if request.method == "POST" => complete(),
        FakeCloud::GoogleCloudStorage if request.method == "DELETE" => {
            gcs_error(404, "NoSuchUpload")
        }
        FakeCloud::GoogleCloudStorage => {
            FakeResponse::with_headers(200, vec![("etag", "\"part\"".to_owned())])
        }
    }
}

#[tokio::test]
async fn azure_gcs_lost_multipart_completion_is_outcome_unknown() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |request| {
            multipart_response(cloud, request, || FakeResponse::DropConnection)
        })
        .await;
        let store = large_write_store(cloud, &server);
        let error = store
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::Unconditional,
            )
            .await
            .expect_err("a lost completion fails the write");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::OutcomeUnknown,
            "{cloud:?}: {error}"
        );
        assert_multipart_requests(cloud, &server, 1, 0);
        assert_contacted_only(&store, &server);
    }
}

/// Asserts the completion and abort requests that a one-part upload sent.
fn assert_multipart_requests(
    cloud: FakeCloud,
    server: &FakeCloudServer,
    completions: usize,
    aborts: usize,
) {
    let requests = server.requests();
    let count = |predicate: &dyn Fn(&RecordedRequest) -> bool| {
        requests.iter().filter(|request| predicate(request)).count()
    };
    match cloud {
        FakeCloud::AzureBlob => {
            let blocks = count(&|request| request.decoded_target().contains("comp=block&"));
            assert_eq!(blocks, 1, "{cloud:?} block uploads");
            let block_lists = count(&|request| request.decoded_target().contains("comp=blocklist"));
            assert_eq!(block_lists, completions, "{cloud:?} completions");
        }
        FakeCloud::GoogleCloudStorage => {
            let initiations = count(&|request| {
                request.method == "POST" && request.decoded_target().contains("uploads")
            });
            assert_eq!(initiations, 1, "{cloud:?} initiations");
            let parts = count(&|request| request.method == "PUT");
            assert_eq!(parts, 1, "{cloud:?} part uploads");
            let gcs_completions = count(&|request| {
                request.method == "POST" && request.decoded_target().contains("uploadId")
            });
            assert_eq!(gcs_completions, completions, "{cloud:?} completions");
            let gcs_aborts = count(&|request| request.method == "DELETE");
            assert_eq!(gcs_aborts, aborts, "{cloud:?} aborts");
        }
    }
}

#[tokio::test]
async fn azure_gcs_rejected_multipart_completion_is_permanent_and_aborted() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |request| {
            multipart_response(cloud, request, || match cloud {
                FakeCloud::AzureBlob => azure_error(403, "AuthorizationPermissionMismatch"),
                FakeCloud::GoogleCloudStorage => gcs_error(403, "AccessDenied"),
            })
        })
        .await;
        let error = large_write_store(cloud, &server)
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::Unconditional,
            )
            .await
            .expect_err("a rejected completion fails the write");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::Permanent,
            "{cloud:?}: {error}"
        );
        assert_multipart_requests(cloud, &server, 1, 1);
    }
}

#[tokio::test]
async fn azure_gcs_failed_multipart_parts_leave_the_target_untouched() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |request| {
            let is_part = match cloud {
                FakeCloud::AzureBlob => request.decoded_target().contains("comp=block&"),
                FakeCloud::GoogleCloudStorage => request.method == "PUT",
            };
            if is_part {
                FakeResponse::status(503)
            } else {
                multipart_response(cloud, request, || FakeResponse::status(500))
            }
        })
        .await;
        let error = large_write_store(cloud, &server)
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::Unconditional,
            )
            .await
            .expect_err("a failed part fails the write");
        assert_eq!(
            error.kind,
            ObjectStoreErrorKind::Transient,
            "{cloud:?}: {error}"
        );
        assert_multipart_requests(cloud, &server, 0, 1);
    }
}

#[tokio::test]
async fn azure_gcs_generation_zero_and_non_canonical_tokens_never_create() {
    let server = FakeCloudServer::start(|_| successful_write(FakeCloud::GoogleCloudStorage)).await;
    let store = store(FakeCloud::GoogleCloudStorage, &server);
    for token in ["\"0\"", "\"00\"", "\"017\"", "\"18446744073709551616\""] {
        let outcome = store
            .put(
                "objects/missing",
                Bytes::from_static(b"body"),
                WriteCondition::ReplaceIfEtagMatches {
                    etag: token.to_owned(),
                },
            )
            .await
            .expect("a non-canonical generation is an outcome");
        assert_eq!(outcome, WriteOutcome::PreconditionFailed, "{token}");
    }
    assert!(server.requests().is_empty());
}

fn redirect(status: u16) -> FakeResponse {
    FakeResponse::with_headers(status, vec![("location", "/dex/redirected".to_owned())])
}

#[tokio::test]
async fn azure_gcs_redirected_writes_are_outcome_unknown_after_one_request() {
    for cloud in CLOUDS {
        for status in [301, 303, 307, 308] {
            let server = FakeCloudServer::start(move |_| redirect(status)).await;
            let error = store(cloud, &server)
                .put(
                    "objects/a",
                    Bytes::from_static(b"body"),
                    WriteCondition::CreateOnly,
                )
                .await
                .expect_err("a redirected put fails");
            assert_eq!(
                error.kind,
                ObjectStoreErrorKind::OutcomeUnknown,
                "{cloud:?} {status}"
            );
            assert_eq!(server.requests().len(), 1, "{cloud:?} {status} put");

            let server = FakeCloudServer::start(move |_| redirect(status)).await;
            let error = store(cloud, &server)
                .delete("objects/a")
                .await
                .expect_err("a redirected delete fails");
            assert_eq!(
                error.kind,
                ObjectStoreErrorKind::OutcomeUnknown,
                "{cloud:?} {status}"
            );
            assert_eq!(server.requests().len(), 1, "{cloud:?} {status} delete");

            let server = FakeCloudServer::start(move |_| redirect(status)).await;
            let error = store(cloud, &server)
                .head("objects/a")
                .await
                .expect_err("a redirected read fails");
            assert_eq!(
                error.kind,
                ObjectStoreErrorKind::Permanent,
                "{cloud:?} {status}"
            );
            assert_eq!(server.requests().len(), 1, "{cloud:?} {status} head");
        }
    }
}

fn is_copy(request: &RecordedRequest) -> bool {
    request.header("x-ms-copy-source").is_some() || request.header("x-goog-copy-source").is_some()
}

fn is_staging_target(request: &RecordedRequest) -> bool {
    request.decoded_target().contains("/staging/")
}

/// Answers a staged create: the staging upload and its cleanup succeed; `copy` answers the copy.
fn staged_create_response(
    cloud: FakeCloud,
    request: &RecordedRequest,
    copy: impl FnOnce() -> FakeResponse,
) -> FakeResponse {
    if is_copy(request) {
        return copy();
    }
    if cloud == FakeCloud::AzureBlob && request.method == "POST" {
        return azure_batch_delete(&[202]);
    }
    if request.method == "DELETE" || request.method == "GET" || request.method == "HEAD" {
        return FakeResponse::status(404);
    }
    multipart_response(cloud, request, || completed_upload(cloud))
}

fn completed_upload(cloud: FakeCloud) -> FakeResponse {
    match cloud {
        FakeCloud::AzureBlob => successful_write(cloud),
        FakeCloud::GoogleCloudStorage => FakeResponse::Reply {
            status: 200,
            headers: vec![("x-goog-generation", GCS_GENERATION.to_owned())],
            body: b"<CompleteMultipartUploadResult><Location>l</Location><Bucket>dex</Bucket><Key>k</Key><ETag>\"done\"</ETag></CompleteMultipartUploadResult>".to_vec(),
        },
    }
}

#[tokio::test]
async fn azure_gcs_redirected_copies_are_outcome_unknown_after_one_copy() {
    for cloud in CLOUDS {
        for status in [303, 307, 308] {
            let server = FakeCloudServer::start(move |request| {
                staged_create_response(cloud, request, || redirect(status))
            })
            .await;
            let error = large_write_store(cloud, &server)
                .put(
                    "objects/a",
                    Bytes::from_static(b"body"),
                    WriteCondition::CreateOnly,
                )
                .await
                .expect_err("a redirected copy fails");
            assert_eq!(
                error.kind,
                ObjectStoreErrorKind::OutcomeUnknown,
                "{cloud:?} {status}"
            );
            let copies = server.requests().into_iter().filter(is_copy).count();
            assert_eq!(copies, 1, "{cloud:?} {status} sends one copy");
        }
    }
}

fn azure_copy_status(copy_status: &str) -> FakeResponse {
    FakeResponse::with_headers(
        200,
        vec![
            ("content-length", "4".to_owned()),
            ("last-modified", LAST_MODIFIED.to_owned()),
            ("etag", AZURE_ETAG.to_owned()),
            ("x-ms-copy-status", copy_status.to_owned()),
        ],
    )
}

#[tokio::test]
async fn azure_pending_copies_are_polled_until_they_end() {
    let mut target_reads = 0;
    let server = FakeCloudServer::start(move |request| {
        if request.method == "HEAD" && !is_staging_target(request) {
            target_reads += 1;
            return azure_copy_status(if target_reads == 1 {
                "pending"
            } else {
                "success"
            });
        }
        staged_create_response(FakeCloud::AzureBlob, request, || {
            FakeResponse::with_headers(202, vec![("x-ms-copy-status", "pending".to_owned())])
        })
    })
    .await;
    let outcome = large_write_store(FakeCloud::AzureBlob, &server)
        .put(
            "objects/a",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the pending copy succeeds");
    assert_eq!(
        outcome,
        WriteOutcome::Written {
            etag: AZURE_ETAG.to_owned()
        }
    );
    assert!(
        server
            .requests_with_method("POST")
            .iter()
            .any(|request| request.body_contains("/staging/0")
                || request.body_contains("/staging/1")),
        "the staging object is deleted after the copy succeeds"
    );

    let server = FakeCloudServer::start(move |request| {
        if request.method == "HEAD" && !is_staging_target(request) {
            return azure_copy_status("pending");
        }
        staged_create_response(FakeCloud::AzureBlob, request, || {
            FakeResponse::with_headers(202, vec![("x-ms-copy-status", "pending".to_owned())])
        })
    })
    .await;
    let error = large_write_store(FakeCloud::AzureBlob, &server)
        .put(
            "objects/a",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a copy pending past the deadline fails");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown, "{error}");
    assert!(
        !server.requests_with_method("POST").iter().any(|request| {
            request.body_contains("/staging/0") || request.body_contains("/staging/1")
        }),
        "a copy that may still read the staging object keeps it"
    );

    let server = FakeCloudServer::start(move |request| {
        if request.method == "HEAD" && !is_staging_target(request) {
            return azure_copy_status("failed");
        }
        staged_create_response(FakeCloud::AzureBlob, request, || {
            FakeResponse::with_headers(202, vec![("x-ms-copy-status", "pending".to_owned())])
        })
    })
    .await;
    let error = large_write_store(FakeCloud::AzureBlob, &server)
        .put(
            "objects/a",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a failed copy fails the create");
    assert_eq!(error.kind, ObjectStoreErrorKind::Corrupt, "{error}");
}

#[tokio::test]
async fn azure_reads_of_a_pending_copy_are_transient_and_of_a_failed_copy_corrupt() {
    for (copy_status, expected) in [
        ("pending", ObjectStoreErrorKind::Transient),
        ("failed", ObjectStoreErrorKind::Corrupt),
        ("aborted", ObjectStoreErrorKind::Corrupt),
    ] {
        let server = FakeCloudServer::start(move |_| azure_copy_status(copy_status)).await;
        let error = store(FakeCloud::AzureBlob, &server)
            .head("objects/a")
            .await
            .expect_err("an unfinished copy fails the read");
        assert_eq!(error.kind, expected, "{copy_status}: {error}");
    }
}

#[tokio::test]
async fn azure_gcs_staging_sweep_keeps_objects_a_pending_azure_copy_may_read() {
    for cloud in CLOUDS {
        let server = FakeCloudServer::start(move |request| {
            let target = request.decoded_target();
            match (request.method.as_str(), cloud) {
                ("HEAD", _) => object_metadata(cloud, 0),
                ("GET", FakeCloud::AzureBlob) if target.contains("comp=list") => FakeResponse::Reply {
                    status: 200,
                    headers: Vec::new(),
                    body: format!(
                        "<?xml version=\"1.0\" encoding=\"utf-8\"?><EnumerationResults ContainerName=\"dex\"><Blobs>{}{}</Blobs><NextMarker /></EnumerationResults>",
                        azure_listed_blob("cluster-a/staging/sixteen-days-old", "Mon, 26 Oct 2026 10:00:00 GMT"),
                        azure_listed_blob("cluster-a/staging/two-days-old", "Mon, 09 Nov 2026 10:00:00 GMT"),
                    )
                    .into_bytes(),
                },
                ("GET", FakeCloud::GoogleCloudStorage) => FakeResponse::Reply {
                    status: 200,
                    headers: Vec::new(),
                    body: format!(
                        "{{\"items\":[{},{}]}}",
                        gcs_listed_object("cluster-a/staging/sixteen-days-old", "2026-10-26T10:00:00Z"),
                        gcs_listed_object("cluster-a/staging/two-days-old", "2026-11-09T10:00:00Z"),
                    )
                    .into_bytes(),
                },
                ("POST", FakeCloud::AzureBlob) => azure_batch_delete(
                    &vec![202; String::from_utf8_lossy(&request.body).matches("Content-ID").count()],
                ),
                ("DELETE", _) => FakeResponse::status(204),
                _ => successful_write(cloud),
            }
        })
        .await;
        let deleted = store(cloud, &server)
            .delete_abandoned_staging_objects()
            .await
            .expect("the sweep succeeds");
        let deleted_keys: Vec<&str> = ["sixteen-days-old", "two-days-old"]
            .into_iter()
            .filter(|name| {
                server.requests().iter().any(|request| {
                    (request.method == "DELETE" && request.decoded_target().ends_with(name))
                        || (request.method == "POST" && request.body_contains(name))
                })
            })
            .collect();
        match cloud {
            FakeCloud::AzureBlob => {
                assert_eq!(deleted, 1);
                assert_eq!(deleted_keys, ["sixteen-days-old"]);
            }
            FakeCloud::GoogleCloudStorage => {
                assert_eq!(deleted, 2);
                assert_eq!(deleted_keys, ["sixteen-days-old", "two-days-old"]);
            }
        }
    }
}

fn azure_listed_blob(name: &str, last_modified: &str) -> String {
    format!(
        "<Blob><Name>{name}</Name><Properties><Last-Modified>{last_modified}</Last-Modified><Etag>0x1</Etag><Content-Length>1</Content-Length><Content-Type>application/octet-stream</Content-Type><BlobType>BlockBlob</BlobType></Properties></Blob>"
    )
}

fn gcs_listed_object(name: &str, updated: &str) -> String {
    format!("{{\"name\":\"{name}\",\"size\":\"1\",\"generation\":\"7\",\"updated\":\"{updated}\"}}")
}

#[tokio::test]
async fn azure_gcs_head_on_a_missing_bucket() {
    let server = FakeCloudServer::start(|_| azure_error(404, "ContainerNotFound")).await;
    let error = store(FakeCloud::AzureBlob, &server)
        .head("objects/a")
        .await
        .expect_err("Azure names a missing container on HEAD");
    assert_eq!(error.kind, ObjectStoreErrorKind::Permanent, "{error}");
    // A GCS HEAD response has no body to name the missing bucket, so it reads as a missing object.
    let server = FakeCloudServer::start(|_| FakeResponse::status(404)).await;
    assert!(
        store(FakeCloud::GoogleCloudStorage, &server)
            .head("objects/a")
            .await
            .expect("a GCS HEAD 404 reads as absent")
            .is_none()
    );
}

#[tokio::test]
async fn azure_pending_copy_deadline_bounds_a_slow_poll() {
    let server = FakeCloudServer::start(move |request| {
        if request.method == "HEAD" && !is_staging_target(request) {
            return FakeResponse::Delayed(
                Duration::from_secs(3),
                Box::new(azure_copy_status("success")),
            );
        }
        staged_create_response(FakeCloud::AzureBlob, request, || {
            FakeResponse::with_headers(202, vec![("x-ms-copy-status", "pending".to_owned())])
        })
    })
    .await;
    let store = store_with(FakeCloud::AzureBlob, server.endpoint(), |config| {
        config.single_put_max_bytes = 1;
        config.pending_copy_timeout = Duration::from_millis(800);
    });
    let started = std::time::Instant::now();
    let error = store
        .put(
            "objects/a",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a poll past the deadline fails the create");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown, "{error}");
    assert!(
        started.elapsed() < Duration::from_millis(2500),
        "the deadline bounds the slow poll: {:?}",
        started.elapsed()
    );
    assert!(
        !server.requests_with_method("POST").iter().any(|request| {
            request.body_contains("/staging/0") || request.body_contains("/staging/1")
        }),
        "a copy that may still read the staging object keeps it"
    );
}

/// Builds a store that sends its requests to the given endpoint.
type StoreBuilder = fn(String) -> AzureGcsObjectStore;

/// Asserts that every client of `store`, credential clients included, contacted only `server`.
fn assert_contacted_only(store: &AzureGcsObjectStore, server: &FakeCloudServer) {
    assert_eq!(store.requested_origins_for_test(), [server.endpoint()]);
}

#[tokio::test]
async fn azure_gcs_fake_server_tests_need_no_credentials_or_metadata_server() {
    let stores: [(FakeCloud, StoreBuilder); 3] = [
        (FakeCloud::AzureBlob, |endpoint| {
            store_at(FakeCloud::AzureBlob, endpoint)
        }),
        (FakeCloud::AzureBlob, |endpoint| {
            let mut config = AzureGcsObjectStoreConfig::new(AzureGcsProvider::AzureBlob {
                account: "devaccount".to_owned(),
                container: "dex".to_owned(),
                credentials: AzureBlobCredentials::Anonymous,
            });
            config.endpoint = Some(endpoint);
            config.key_prefix = "cluster-a".to_owned();
            AzureGcsObjectStore::new(config).expect("the store builds")
        }),
        (FakeCloud::GoogleCloudStorage, |endpoint| {
            store_at(FakeCloud::GoogleCloudStorage, endpoint)
        }),
    ];
    for (cloud, build) in stores {
        let server = FakeCloudServer::start(move |request| match request.method.as_str() {
            "PUT" => successful_write(cloud),
            "HEAD" => object_metadata(cloud, 4),
            "GET" => listing(cloud, "cluster-a/objects/a", 4),
            "DELETE" => FakeResponse::status(404),
            _ => azure_batch_delete(&[404]),
        })
        .await;
        let store = build(server.endpoint());
        store
            .put(
                "objects/a",
                Bytes::from_static(b"body"),
                WriteCondition::Unconditional,
            )
            .await
            .expect("the write succeeds");
        store
            .head("objects/a")
            .await
            .expect("the head succeeds")
            .expect("the object exists");
        store
            .list("objects/", None, 10)
            .await
            .expect("the listing succeeds");
        store
            .delete("objects/missing")
            .await
            .expect("the delete succeeds");
        assert_contacted_only(&store, &server);
    }
}
