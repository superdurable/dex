// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#[path = "conformance/recording_s3_proxy.rs"]
mod recording_s3_proxy;

use std::sync::Arc;

use bytes::Bytes;
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ObjectStore, S3Credentials, S3ObjectStore, S3ObjectStoreConfig,
    WriteCondition, WriteOutcome, startup_self_check,
};
use s3mock::{Durability, EtagMode, Store, StoreConfig};
use tempfile::TempDir;

use recording_s3_proxy::RecordingS3Proxy;

async fn check_store(store: &dyn ObjectStore) {
    assert!(
        store
            .get("missing")
            .await
            .expect("the conformance operation succeeds")
            .is_none()
    );
    assert!(
        store
            .head("missing")
            .await
            .expect("the conformance operation succeeds")
            .is_none()
    );
    assert!(
        store
            .get_range(
                "missing",
                ByteRange {
                    start: 0,
                    end_inclusive: 2
                }
            )
            .await
            .expect("the conformance operation succeeds")
            .is_none()
    );
    assert_eq!(
        store
            .put(
                "missing",
                Bytes::from_static(b"body"),
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "\"absent\"".to_owned()
                }
            )
            .await
            .expect("a missing-key replacement reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    let initial = store
        .put(
            "objects/a",
            Bytes::from_static(b"abcdef"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the conformance operation succeeds");
    let WriteOutcome::Written { etag } = initial else {
        panic!("initial create must succeed")
    };
    assert_eq!(
        store
            .put(
                "objects/a",
                Bytes::from_static(b"wrong"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("the conformance operation succeeds"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .put(
                "objects/a",
                Bytes::from_static(b"wrong"),
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "wrong".to_owned()
                }
            )
            .await
            .expect("the conformance operation succeeds"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .get("objects/a")
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .bytes,
        Bytes::from_static(b"abcdef")
    );
    let range = store
        .get_range(
            "objects/a",
            ByteRange {
                start: 2,
                end_inclusive: 100,
            },
        )
        .await
        .expect("the conformance operation succeeds")
        .expect("the conformance operation succeeds");
    assert_eq!(range.bytes, Bytes::from_static(b"cdef"));
    assert_eq!(range.object_size, 6);
    assert_eq!(range.etag, etag);
    let beyond = store
        .get_range(
            "objects/a",
            ByteRange {
                start: 100,
                end_inclusive: 200,
            },
        )
        .await
        .expect("the conformance operation succeeds")
        .expect("the conformance operation succeeds");
    assert!(beyond.bytes.is_empty());
    assert_eq!(beyond.object_size, 6);
    let next = store
        .put(
            "objects/a",
            Bytes::from_static(b"next"),
            WriteCondition::ReplaceIfEtagMatches { etag },
        )
        .await
        .expect("the conformance operation succeeds");
    assert!(matches!(next, WriteOutcome::Written { .. }));
    assert_eq!(
        store
            .head("objects/a")
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .size,
        4
    );
    store
        .put(
            "objects/b",
            Bytes::from_static(b"b"),
            WriteCondition::Unconditional,
        )
        .await
        .expect("the conformance operation succeeds");
    store
        .put(
            "objects/c",
            Bytes::from_static(b"c"),
            WriteCondition::Unconditional,
        )
        .await
        .expect("the conformance operation succeeds");
    let first = store
        .list("objects/", None, 2)
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        first
            .objects
            .iter()
            .map(|object| object.key.as_str())
            .collect::<Vec<_>>(),
        ["objects/a", "objects/b"]
    );
    assert!(
        first
            .objects
            .iter()
            .all(|object| object.last_modified_unix_micros.is_some())
    );
    assert!(
        store
            .head("objects/a")
            .await
            .expect("head succeeds")
            .expect("object exists")
            .last_modified_unix_micros
            .is_some()
    );
    let second = store
        .list("objects/", first.next_start_after.as_deref(), 2)
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        second
            .objects
            .iter()
            .map(|object| object.key.as_str())
            .collect::<Vec<_>>(),
        ["objects/c"]
    );
    assert!(second.next_start_after.is_none());
    let directory = tempfile::tempdir().expect("the conformance operation succeeds");
    let input = directory.path().join("input");
    let output = directory.path().join("output");
    tokio::fs::write(&input, b"from-file")
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        store
            .put_file(
                "missing-file",
                &input,
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "\"absent\"".to_owned()
                }
            )
            .await
            .expect("a missing-key file replacement reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    store
        .put_file("file", &input, WriteCondition::CreateOnly)
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        store
            .get_to_file("file", &output)
            .await
            .expect("the conformance operation succeeds"),
        Some(9)
    );
    assert_eq!(
        tokio::fs::read(&output)
            .await
            .expect("the conformance operation succeeds"),
        b"from-file"
    );
    assert!(store.get_to_file("file", &output).await.is_err());
    assert_eq!(
        tokio::fs::read(&output)
            .await
            .expect("the existing download remains intact"),
        b"from-file"
    );
    assert_no_temporary_downloads(directory.path());
    let absent_path = directory.path().join("absent");
    assert_eq!(
        store
            .get_to_file("absent", &absent_path)
            .await
            .expect("the conformance operation succeeds"),
        None
    );
    assert!(!absent_path.exists());
    assert_no_temporary_downloads(directory.path());
    store
        .delete_many(&[
            "objects/a".to_owned(),
            "objects/b".to_owned(),
            "absent".to_owned(),
        ])
        .await
        .expect("the conformance operation succeeds");
    assert!(
        store
            .get("objects/a")
            .await
            .expect("the conformance operation succeeds")
            .is_none()
    );
    store
        .delete("objects/c")
        .await
        .expect("the conformance operation succeeds");
    store
        .delete_many(&[])
        .await
        .expect("an empty batch delete succeeds");
    store
        .delete("objects/c")
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        store
            .delete_many(&vec!["unused".to_owned(); 1001])
            .await
            .expect_err("the conformance operation fails")
            .kind,
        dexzd_object_store::ObjectStoreErrorKind::Permanent
    );
    let large = Bytes::from(vec![42; 6 * 1024 * 1024]);
    assert_eq!(
        store
            .put(
                "missing-large",
                large.clone(),
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "\"absent\"".to_owned()
                }
            )
            .await
            .expect("a missing-key multipart replacement reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    let created = store
        .put("large", large.clone(), WriteCondition::CreateOnly)
        .await
        .expect("the conformance operation succeeds");
    let WriteOutcome::Written { etag } = created else {
        panic!("multipart create must succeed")
    };
    assert_eq!(
        store
            .put("large", large.clone(), WriteCondition::CreateOnly)
            .await
            .expect("the conformance operation succeeds"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .put(
                "large",
                large.clone(),
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "wrong".to_owned()
                }
            )
            .await
            .expect("the conformance operation succeeds"),
        WriteOutcome::PreconditionFailed
    );
    assert!(matches!(
        store
            .put(
                "large",
                large,
                WriteCondition::ReplaceIfEtagMatches { etag }
            )
            .await
            .expect("the conformance operation succeeds"),
        WriteOutcome::Written { .. }
    ));
    let large_file = directory.path().join("large-file");
    tokio::fs::write(&large_file, vec![7; 6 * 1024 * 1024])
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        store
            .put_file(
                "missing-large-file",
                &large_file,
                WriteCondition::ReplaceIfEtagMatches {
                    etag: "\"absent\"".to_owned()
                }
            )
            .await
            .expect("a missing-key multipart file replacement reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    assert!(matches!(
        store
            .put_file("large-file", &large_file, WriteCondition::CreateOnly)
            .await
            .expect("the conformance operation succeeds"),
        WriteOutcome::Written { .. }
    ));
    assert_eq!(
        store
            .head("large-file")
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .size,
        6 * 1024 * 1024
    );
    startup_self_check(store)
        .await
        .expect("the conformance operation succeeds");
}

fn assert_no_temporary_downloads(directory: &std::path::Path) {
    for entry in std::fs::read_dir(directory).expect("the download directory is readable") {
        let entry = entry.expect("the download directory entry is readable");
        assert!(!entry.file_name().to_string_lossy().contains(".dexzd-"));
    }
}

#[tokio::test]
async fn in_memory_conformance() {
    check_store(&InMemoryObjectStore::new()).await;
}

async fn s3_conformance(durability: Durability) {
    let directory = TempDir::new().expect("the conformance operation succeeds");
    let external_endpoint = match std::env::var("DEX_ZD_S3_TEST_ENDPOINT") {
        Ok(endpoint) => Some(endpoint),
        Err(std::env::VarError::NotPresent) => None,
        Err(error) => panic!("DEX_ZD_S3_TEST_ENDPOINT is unreadable: {error}"),
    };
    let is_external_endpoint = external_endpoint.is_some();
    let (store, server, endpoint, bucket, credentials) = if let Some(endpoint) = external_endpoint {
        (
            None,
            None,
            endpoint,
            std::env::var("DEX_ZD_S3_TEST_BUCKET").expect("the external bucket is configured"),
            S3Credentials::Static {
                access_key_id: std::env::var("AWS_ACCESS_KEY_ID")
                    .expect("the external access key is configured"),
                secret_access_key: std::env::var("AWS_SECRET_ACCESS_KEY")
                    .expect("the external secret key is configured"),
                session_token: None,
            },
        )
    } else {
        let store = Store::open(&StoreConfig {
            root_directory: directory.path().to_path_buf(),
            durability,
            etag_mode: EtagMode::Opaque,
        })
        .expect("the conformance operation succeeds");
        store
            .create_bucket("object-store-conformance", false)
            .expect("the conformance operation succeeds");
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("the conformance operation succeeds");
        let store = Arc::new(store);
        let server = s3mock_server::start(Arc::clone(&store), listener)
            .expect("the conformance operation succeeds");
        let endpoint = format!("http://{}", server.local_address());
        (
            Some(store),
            Some(server),
            endpoint,
            "object-store-conformance".to_owned(),
            S3Credentials::Static {
                access_key_id: "s3mock".to_owned(),
                secret_access_key: "s3mock".to_owned(),
                session_token: None,
            },
        )
    };
    let mut config = S3ObjectStoreConfig::new("us-east-1", bucket.clone());
    config.endpoint = Some(endpoint);
    config.key_prefix = if is_external_endpoint {
        let run_directory_name = directory
            .path()
            .file_name()
            .and_then(|name| name.to_str())
            .expect("the temporary directory has a UTF-8 name");
        format!("dexzd/conformance/{run_directory_name}")
    } else {
        "dexzd/conformance".to_owned()
    };
    config.force_path_style = true;
    config.credentials = credentials;
    config.multipart_threshold = 5 * 1024 * 1024;
    config.multipart_part_size = 5 * 1024 * 1024;
    let object_store = S3ObjectStore::new(config)
        .await
        .expect("the conformance operation succeeds");
    check_store(&object_store).await;
    if let Some(server) = &server {
        let request_log = s3mock_request_log(server.local_address()).await;
        assert!(
            request_log.contains("\"operation\":\"ListObjectsV2\""),
            "{request_log}"
        );
        assert!(
            !request_log.contains("\"operation\":\"HeadBucket\""),
            "{request_log}"
        );
        assert!(
            request_log.contains("\"key\":\"dexzd/conformance/probe/"),
            "{request_log}"
        );
    }
    if let Some(store) = store {
        let uploads = store
            .list_multipart_uploads(
                &bucket,
                &s3mock::ListMultipartUploadsRequest {
                    prefix: "dexzd/conformance/".to_owned(),
                    delimiter: String::new(),
                    key_marker: None,
                    upload_id_marker: None,
                    max_uploads: 1000,
                },
            )
            .expect("multipart uploads can be inspected after failed conditions");
        assert!(
            uploads.uploads.is_empty(),
            "failed conditional uploads must be aborted"
        );
    }
    if let Some(server) = server {
        server.shutdown().await;
    }
}

#[tokio::test]
async fn strict_s3_conformance() {
    s3_conformance(Durability::Strict).await;
}

#[tokio::test]
async fn process_crash_safe_s3_conformance() {
    s3_conformance(Durability::ProcessCrashSafe).await;
}

#[tokio::test]
async fn relaxed_s3_conformance() {
    s3_conformance(Durability::Relaxed).await;
}

async fn start_anonymous_s3_store(
    directory: &TempDir,
    multipart_threshold: usize,
) -> (Arc<Store>, s3mock_server::ServerHandle, S3ObjectStore) {
    let store = Store::open(&StoreConfig {
        root_directory: directory.path().join("s3"),
        durability: Durability::Relaxed,
        etag_mode: EtagMode::Opaque,
    })
    .expect("the emulator store opens");
    store
        .create_bucket("anonymous-file-uploads", false)
        .expect("the bucket is created");
    let store = Arc::new(store);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the emulator binds");
    let server = s3mock_server::start(Arc::clone(&store), listener).expect("the emulator starts");
    let mut config = S3ObjectStoreConfig::new("us-east-1", "anonymous-file-uploads");
    config.endpoint = Some(format!("http://{}", server.local_address()));
    config.force_path_style = true;
    config.credentials = S3Credentials::Anonymous;
    config.multipart_threshold = multipart_threshold;
    config.multipart_part_size = 5 * 1024 * 1024;
    let object_store = S3ObjectStore::new(config)
        .await
        .expect("the anonymous client is configured");
    (store, server, object_store)
}

#[tokio::test]
async fn anonymous_s3_single_file_upload_preserves_crc32() {
    let directory = TempDir::new().expect("the temporary directory is created");
    let (store, server, object_store) = start_anonymous_s3_store(&directory, 5 * 1024 * 1024).await;
    let single_file = directory.path().join("single-file");
    let single_body = b"anonymous single-file upload";
    tokio::fs::write(&single_file, single_body)
        .await
        .expect("the single-part file is written");
    object_store
        .put_file("single", &single_file, WriteCondition::CreateOnly)
        .await
        .expect("the anonymous single-part upload succeeds");
    assert_eq!(
        object_store
            .get("single")
            .await
            .expect("the single-part download succeeds")
            .expect("the single-part object exists")
            .bytes,
        Bytes::from_static(single_body)
    );
    let single_checksum = store
        .head_object(
            "anonymous-file-uploads",
            "single",
            &s3mock::ObjectReadRequest::default(),
        )
        .expect("the single-part object is readable")
        .record
        .checksum
        .expect("the single-part object has a checksum");
    assert_eq!(single_checksum.algorithm, s3mock::ChecksumAlgorithm::Crc32);
    assert_eq!(
        single_checksum.base64_value,
        s3mock::ChecksumAlgorithm::Crc32.base64_checksum(single_body)
    );
    server.shutdown().await;
}

#[tokio::test]
async fn anonymous_s3_multipart_file_upload_preserves_composite_crc32() {
    let directory = TempDir::new().expect("the temporary directory is created");
    let (store, server, object_store) = start_anonymous_s3_store(&directory, 5 * 1024 * 1024).await;
    let multipart_file = directory.path().join("multipart-file");
    let multipart_body = vec![0x5a; 6 * 1024 * 1024];
    tokio::fs::write(&multipart_file, &multipart_body)
        .await
        .expect("the multipart file is written");
    object_store
        .put_file("multipart", &multipart_file, WriteCondition::CreateOnly)
        .await
        .expect("the anonymous multipart upload succeeds");
    assert_eq!(
        object_store
            .get("multipart")
            .await
            .expect("the multipart download succeeds")
            .expect("the multipart object exists")
            .bytes,
        Bytes::from(multipart_body.clone())
    );
    assert_multipart_crc32(&store, "multipart", &multipart_body, 5 * 1024 * 1024);
    server.shutdown().await;
}

#[tokio::test]
async fn anonymous_s3_multipart_file_upload_sends_crc32_for_every_part() {
    let directory = TempDir::new().expect("the temporary directory is created");
    let (_store, server, _direct_store) =
        start_anonymous_s3_store(&directory, 5 * 1024 * 1024).await;
    let proxy = RecordingS3Proxy::start(server.local_address()).await;
    let mut config = S3ObjectStoreConfig::new("us-east-1", "anonymous-file-uploads");
    config.endpoint = Some(format!("http://{}", proxy.address()));
    config.force_path_style = true;
    config.credentials = S3Credentials::Anonymous;
    config.multipart_threshold = 5 * 1024 * 1024;
    config.multipart_part_size = 5 * 1024 * 1024;
    let object_store = S3ObjectStore::new(config)
        .await
        .expect("the proxied anonymous client is configured");
    let file = directory.path().join("multipart-file-requests");
    let body = vec![0x5a; 6 * 1024 * 1024];
    tokio::fs::write(&file, &body)
        .await
        .expect("the multipart file is written");
    object_store
        .put_file("multipart", &file, WriteCondition::CreateOnly)
        .await
        .expect("the proxied multipart upload succeeds");

    let request_headers = proxy.multipart_request_headers();
    let create_requests: Vec<_> = request_headers
        .iter()
        .filter(|request| {
            request.starts_with("POST /anonymous-file-uploads/multipart?uploads HTTP/1.1")
        })
        .collect();
    assert_eq!(
        create_requests.len(),
        1,
        "one multipart upload is initiated"
    );
    assert!(create_requests[0].lines().any(|line| {
        line.split_once(':').is_some_and(|(name, value)| {
            name.eq_ignore_ascii_case("x-amz-checksum-algorithm") && value.trim() == "CRC32"
        })
    }));

    let upload_part_requests: Vec<_> = request_headers
        .iter()
        .filter(|request| {
            request.starts_with("PUT /anonymous-file-uploads/multipart?")
                && request
                    .lines()
                    .next()
                    .is_some_and(|line| line.contains("partNumber="))
        })
        .collect();
    assert_eq!(
        upload_part_requests.len(),
        2,
        "both file parts are uploaded"
    );
    for (index, part_body) in body.chunks(5 * 1024 * 1024).enumerate() {
        let part_number = index + 1;
        let request = upload_part_requests
            .iter()
            .find(|request| {
                request
                    .lines()
                    .next()
                    .is_some_and(|line| line.contains(&format!("partNumber={part_number}")))
            })
            .expect("each part has an UploadPart request");
        let expected_checksum = s3mock::ChecksumAlgorithm::Crc32.base64_checksum(part_body);
        assert!(
            request.lines().any(|line| {
                line.split_once(':').is_some_and(|(name, value)| {
                    name.eq_ignore_ascii_case("x-amz-checksum-crc32")
                        && value.trim() == expected_checksum
                })
            }),
            "part {part_number} must carry its CRC32 checksum"
        );
    }
    proxy.shutdown().await;
    server.shutdown().await;
}

#[tokio::test]
async fn high_threshold_file_upload_uses_bounded_multipart_reads() {
    let directory = TempDir::new().expect("the temporary directory is created");
    let (store, server, object_store) =
        start_anonymous_s3_store(&directory, 64 * 1024 * 1024).await;
    let file = directory.path().join("larger-than-single-put-cap");
    let body = vec![0x4b; 17 * 1024 * 1024];
    tokio::fs::write(&file, &body)
        .await
        .expect("the large file is written");
    object_store
        .put_file("high-threshold", &file, WriteCondition::CreateOnly)
        .await
        .expect("the file uses multipart despite the high threshold");
    assert_multipart_crc32(&store, "high-threshold", &body, 5 * 1024 * 1024);
    server.shutdown().await;
}

fn assert_multipart_crc32(store: &Store, key: &str, body: &[u8], part_size: usize) {
    let record = store
        .head_object(
            "anonymous-file-uploads",
            key,
            &s3mock::ObjectReadRequest::default(),
        )
        .expect("the multipart object is readable")
        .record;
    let part_count = body.chunks(part_size).count();
    assert_eq!(record.parts_count, Some(part_count as u32));
    let checksum = record
        .checksum
        .expect("the multipart object has a checksum");
    assert_eq!(checksum.algorithm, s3mock::ChecksumAlgorithm::Crc32);
    assert_eq!(checksum.checksum_type, s3mock::ChecksumType::Composite);
    let mut part_checksums = Vec::new();
    for part_body in body.chunks(part_size) {
        let part_checksum = s3mock::ChecksumAlgorithm::Crc32.base64_checksum(part_body);
        part_checksums.extend_from_slice(
            &s3mock::decode_base64(&part_checksum).expect("the part checksum is valid base64"),
        );
    }
    let expected = format!(
        "{}-{part_count}",
        s3mock::ChecksumAlgorithm::Crc32.base64_checksum(&part_checksums)
    );
    assert_eq!(checksum.base64_value, expected);
}

#[derive(Default)]
struct ScriptedFaults {
    actions: std::sync::Mutex<std::collections::VecDeque<dexzd_object_store::FaultAction>>,
}

impl ScriptedFaults {
    fn push(&self, action: dexzd_object_store::FaultAction) {
        self.actions
            .lock()
            .expect("the conformance operation succeeds")
            .push_back(action);
    }
}

impl dexzd_object_store::FaultInjector for ScriptedFaults {
    fn action(
        &self,
        _operation: dexzd_object_store::ObjectStoreOperation,
        _key: &str,
    ) -> dexzd_object_store::FaultAction {
        self.actions
            .lock()
            .expect("the conformance operation succeeds")
            .pop_front()
            .unwrap_or(dexzd_object_store::FaultAction::Proceed)
    }
}

#[tokio::test]
async fn in_memory_faults_and_counters() {
    use dexzd_object_store::{FaultAction, ObjectStoreErrorKind, ObjectStoreOperation};
    let faults = Arc::new(ScriptedFaults::default());
    let store = InMemoryObjectStore::with_injector(faults.clone());
    faults.push(FaultAction::FailBeforeEffect);
    assert_eq!(
        store
            .put(
                "cost/a",
                Bytes::from_static(b"one"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    assert!(
        store
            .head("cost/a")
            .await
            .expect("the conformance operation succeeds")
            .is_none()
    );
    faults.push(FaultAction::ConditionalRequestConflict);
    assert_eq!(
        store
            .put(
                "cost/a",
                Bytes::from_static(b"one"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the injected concurrent write conflicts")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    assert!(
        store
            .head("cost/a")
            .await
            .expect("the key remains absent")
            .is_none()
    );
    faults.push(FaultAction::ApplyThenOutcomeUnknown);
    assert_eq!(
        store
            .put(
                "cost/a",
                Bytes::from_static(b"one"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::OutcomeUnknown
    );
    assert_eq!(
        store
            .get("cost/a")
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .bytes,
        Bytes::from_static(b"one")
    );
    faults.push(FaultAction::ApplyThenOutcomeUnknown);
    assert_eq!(
        store
            .put(
                "cost/a",
                Bytes::from_static(b"ignored"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the injected response loss is reported after a failed condition")
            .kind,
        ObjectStoreErrorKind::OutcomeUnknown
    );
    assert_eq!(
        store
            .get("cost/a")
            .await
            .expect("the unchanged object is readable")
            .expect("the unchanged object exists")
            .bytes,
        Bytes::from_static(b"one")
    );
    faults.push(FaultAction::CorruptOnRead);
    assert_ne!(
        store
            .get("cost/a")
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .bytes,
        Bytes::from_static(b"one")
    );
    assert_eq!(
        store
            .get("cost/a")
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .bytes,
        Bytes::from_static(b"one")
    );
    faults.push(FaultAction::Delay(std::time::Duration::from_millis(1)));
    assert!(
        tokio::time::timeout(std::time::Duration::from_secs(1), store.head("cost/a"))
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .is_some()
    );
    assert_eq!(store.call_count(ObjectStoreOperation::Put, "cost/"), 4);
    assert_eq!(store.call_count(ObjectStoreOperation::Put, "other/"), 0);
    assert_eq!(store.call_count(ObjectStoreOperation::Get, "cost/"), 4);
}

#[test]
fn static_credentials_debug_redacts_sensitive_values() {
    let mut config = S3ObjectStoreConfig::new("us-east-1", "bucket");
    config.credentials = S3Credentials::Static {
        access_key_id: "visible-key-identifier".to_owned(),
        secret_access_key: "visible-secret-key".to_owned(),
        session_token: Some("visible-session-token".to_owned()),
    };
    let debug = format!("{config:?}");
    assert!(!debug.contains("visible-key-identifier"));
    assert!(!debug.contains("visible-secret-key"));
    assert!(!debug.contains("visible-session-token"));
    assert!(debug.contains("[REDACTED]"));
}

/// Answers requests with 404 and returns the first lowercase request line and headers.
async fn capture_request_headers(credentials: S3Credentials) -> String {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the capture listener binds");
    let mut config = S3ObjectStoreConfig::new("us-east-1", "capture-bucket");
    config.endpoint = Some(format!(
        "http://{}",
        listener.local_addr().expect("the listener has an address")
    ));
    config.force_path_style = true;
    config.credentials = credentials;
    let store = S3ObjectStore::new(config)
        .await
        .expect("the capture store is configured");
    let (captured_sender, captured) = tokio::sync::mpsc::unbounded_channel();
    let server = tokio::spawn(async move {
        loop {
            let (mut stream, _) = listener.accept().await.expect("the client connects");
            let mut request = Vec::new();
            let mut buffer = [0_u8; 4096];
            while !request.windows(4).any(|window| window == b"\r\n\r\n") {
                let read = stream.read(&mut buffer).await.expect("the request is read");
                assert!(read > 0, "the client closed before sending headers");
                request.extend_from_slice(&buffer[..read]);
            }
            stream
                .write_all(
                    b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
                )
                .await
                .expect("the response is written");
            stream.shutdown().await.expect("the response is flushed");
            if captured_sender
                .send(String::from_utf8_lossy(&request).to_lowercase())
                .is_err()
            {
                return;
            }
        }
    });
    let head = store.head("object").await;
    server.abort();
    let mut captured = captured;
    let request = captured.recv().await.expect("one request was captured");
    assert!(
        !matches!(head, Ok(Some(_))),
        "the capture endpoint stores no object"
    );
    request
}

#[tokio::test]
async fn static_session_token_is_sent_and_anonymous_requests_are_unsigned() {
    let signed = capture_request_headers(S3Credentials::Static {
        access_key_id: "capture-key".to_owned(),
        secret_access_key: "capture-secret".to_owned(),
        session_token: Some("capture-session-token".to_owned()),
    })
    .await;
    assert!(signed.contains("\r\nx-amz-security-token: capture-session-token\r\n"));
    assert!(signed.contains("\r\nauthorization: aws4-hmac-sha256 credential=capture-key/"));
    let unsigned = capture_request_headers(S3Credentials::Anonymous).await;
    assert!(!unsigned.contains("\r\nauthorization:"), "{unsigned}");
    assert!(
        !unsigned.contains("\r\nx-amz-security-token:"),
        "{unsigned}"
    );
}

#[tokio::test]
async fn startup_check_names_failed_condition() {
    use dexzd_object_store::{FaultAction, StartupSelfCheckFailure};
    let faults = Arc::new(ScriptedFaults::default());
    faults.push(FaultAction::Proceed);
    faults.push(FaultAction::FailBeforeEffect);
    let store = InMemoryObjectStore::with_injector(faults);
    let error = startup_self_check(&store)
        .await
        .expect_err("the conformance operation fails");
    assert_eq!(error.check, StartupSelfCheckFailure::DuplicateCreate);
}

#[tokio::test(start_paused = true)]
async fn in_memory_delay_uses_tokio_time() {
    let faults = Arc::new(ScriptedFaults::default());
    faults.push(dexzd_object_store::FaultAction::Delay(
        std::time::Duration::from_secs(3600),
    ));
    let store = InMemoryObjectStore::with_injector(faults);
    let result = tokio::time::timeout(
        std::time::Duration::from_secs(3601),
        store.head("delayed/key"),
    )
    .await
    .expect("paused Tokio time advances through the injected delay");
    assert!(result.expect("the delayed read succeeds").is_none());
}

#[tokio::test]
async fn startup_check_names_failed_positive_replacement() {
    use dexzd_object_store::{FaultAction, StartupSelfCheckFailure};
    let faults = Arc::new(ScriptedFaults::default());
    for _ in 0..3 {
        faults.push(FaultAction::Proceed);
    }
    faults.push(FaultAction::FailBeforeEffect);
    let store = InMemoryObjectStore::with_injector(faults);
    let error = startup_self_check(&store)
        .await
        .expect_err("the correct-ETag check fails");
    assert_eq!(error.check, StartupSelfCheckFailure::CorrectEtagReplacement);
}

#[tokio::test]
async fn s3_missing_bucket_and_access_denied_are_permanent() {
    use dexzd_object_store::ObjectStoreErrorKind;
    use s3mock_server::{ServerConfig, SignatureVerification};

    let directory = TempDir::new().expect("the conformance operation succeeds");
    let store = Store::open(&StoreConfig {
        root_directory: directory.path().to_path_buf(),
        durability: Durability::Relaxed,
        etag_mode: EtagMode::Opaque,
    })
    .expect("the conformance operation succeeds");
    store
        .create_bucket("present-bucket", false)
        .expect("the conformance operation succeeds");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the conformance operation succeeds");
    let server = s3mock_server::start_with_config(
        Arc::new(store),
        listener,
        ServerConfig {
            signature_verification: SignatureVerification::Strict,
            ..ServerConfig::default()
        },
    )
    .expect("the conformance operation succeeds");
    let endpoint = format!("http://{}", server.local_address());
    let mut missing = S3ObjectStoreConfig::new("us-east-1", "missing-bucket");
    missing.endpoint = Some(endpoint.clone());
    missing.force_path_style = true;
    missing.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: None,
    };
    let missing = S3ObjectStore::new(missing)
        .await
        .expect("the conformance operation succeeds");
    let missing_get_error = missing
        .get("object")
        .await
        .expect_err("a missing bucket fails the read request");
    assert_eq!(missing_get_error.kind, ObjectStoreErrorKind::Permanent);
    assert!(missing_get_error.detail.contains("NoSuchBucket"));
    let missing_head_error = missing
        .head("object")
        .await
        .expect_err("a missing bucket fails the head request");
    assert_eq!(missing_head_error.kind, ObjectStoreErrorKind::Permanent);
    assert!(missing_head_error.detail.contains("NoSuchBucket"));
    let startup_error = startup_self_check(&missing)
        .await
        .expect_err("startup fails for a missing bucket");
    assert!(startup_error.to_string().contains("InitialCreate"));
    assert!(
        startup_error
            .source
            .expect("the startup error retains its S3 cause")
            .detail
            .contains("NoSuchBucket")
    );
    assert_eq!(
        missing
            .put(
                "object",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Permanent
    );
    assert_eq!(
        missing
            .list("", None, 10)
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Permanent
    );
    let request_log = s3mock_request_log(server.local_address()).await;
    assert!(
        !request_log.contains("\"operation\":\"HeadBucket\""),
        "{request_log}"
    );

    let mut session = S3ObjectStoreConfig::new("us-east-1", "present-bucket");
    session.endpoint = Some(endpoint.clone());
    session.force_path_style = true;
    session.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: Some("temporary-session".to_owned()),
    };
    let session = S3ObjectStore::new(session)
        .await
        .expect("the conformance operation succeeds");
    assert!(matches!(
        session
            .put(
                "session-object",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("a request signed with a session token verifies"),
        WriteOutcome::Written { .. }
    ));

    let mut anonymous = S3ObjectStoreConfig::new("us-east-1", "present-bucket");
    anonymous.endpoint = Some(endpoint.clone());
    anonymous.force_path_style = true;
    anonymous.credentials = S3Credentials::Anonymous;
    let anonymous = S3ObjectStore::new(anonymous)
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        anonymous
            .get("session-object")
            .await
            .expect_err("strict verification rejects unsigned requests")
            .kind,
        ObjectStoreErrorKind::Permanent
    );

    let mut forbidden = S3ObjectStoreConfig::new("us-east-1", "present-bucket");
    forbidden.endpoint = Some(endpoint);
    forbidden.force_path_style = true;
    forbidden.credentials = S3Credentials::Static {
        access_key_id: "wrong".to_owned(),
        secret_access_key: "wrong".to_owned(),
        session_token: None,
    };
    let forbidden = S3ObjectStore::new(forbidden)
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        forbidden
            .get("object")
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Permanent
    );
    assert_eq!(
        forbidden
            .put(
                "object",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Permanent
    );
    server.shutdown().await;
}

#[tokio::test]
async fn s3_prefix_confined_reads_disambiguate_access_denied() {
    use s3mock_server::{ServerConfig, SignatureVerification};

    let directory = TempDir::new().expect("the test directory opens");
    let store = Store::open(&StoreConfig {
        root_directory: directory.path().join("s3"),
        durability: Durability::Relaxed,
        etag_mode: EtagMode::Opaque,
    })
    .expect("the S3 store opens");
    store
        .create_bucket("confined-bucket", false)
        .expect("the bucket is created");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the listener binds");
    let server = s3mock_server::start_with_config(
        Arc::new(store),
        listener,
        ServerConfig {
            signature_verification: SignatureVerification::Strict,
            ..ServerConfig::default()
        },
    )
    .expect("the S3 server starts");
    let mut config = S3ObjectStoreConfig::new("us-east-1", "confined-bucket");
    config.endpoint = Some(format!("http://{}", server.local_address()));
    config.key_prefix = "confined".to_owned();
    config.force_path_style = true;
    config.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: None,
    };
    let startup_config = config.clone();
    let object_store = S3ObjectStore::new(config)
        .await
        .expect("the S3 client opens");
    startup_self_check(&object_store)
        .await
        .expect("startup works on an empty configured prefix");
    object_store
        .put(
            "existing",
            Bytes::from_static(b"existing"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the existing key is written");
    object_store
        .put(
            "missing-sibling",
            Bytes::from_static(b"sibling"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the sibling key is written");
    startup_self_check(&object_store)
        .await
        .expect("startup works with existing keys under the configured prefix");
    set_s3mock_fault(
        server.local_address(),
        r#"{"operation":"GetObject","key_prefix":"confined/missing","action":{"type":"error","status":403}},{"operation":"HeadObject","key_prefix":"confined/missing","action":{"type":"error","status":403}},{"operation":"GetObject","key_prefix":"confined/existing","action":{"type":"error","status":403}},{"operation":"HeadObject","key_prefix":"confined/existing","action":{"type":"error","status":403}}"#,
    )
    .await;
    assert!(
        object_store
            .get("missing")
            .await
            .expect("missing GET is resolved")
            .is_none()
    );
    assert!(
        object_store
            .head("missing")
            .await
            .expect("missing HEAD is resolved")
            .is_none()
    );
    assert!(
        object_store
            .get_range(
                "missing",
                ByteRange {
                    start: 0,
                    end_inclusive: 0
                }
            )
            .await
            .expect("missing range GET is resolved")
            .is_none()
    );
    let download_path = directory.path().join("missing-download");
    assert!(
        object_store
            .get_to_file("missing", &download_path)
            .await
            .expect("missing file GET is resolved")
            .is_none()
    );
    assert!(!download_path.exists());
    assert!(
        object_store
            .get("existing")
            .await
            .expect_err("a denied existing key remains an error")
            .detail
            .contains("AccessDenied")
    );
    assert!(
        object_store
            .head("existing")
            .await
            .expect_err("a denied existing key remains an error")
            .detail
            .contains("status=Some(403)")
    );
    let request_log = s3mock_request_log(server.local_address()).await;
    assert!(
        !request_log.contains("\"operation\":\"HeadBucket\""),
        "{request_log}"
    );
    assert!(
        request_log.contains("\"operation\":\"ListObjectsV2\""),
        "{request_log}"
    );
    set_s3mock_fault(
        server.local_address(),
        r#"{"operation":"GetObject","key_prefix":"confined/missing","action":{"type":"error","status":403}},{"operation":"HeadObject","key_prefix":"confined/missing","action":{"type":"error","status":403}},{"operation":"ListObjectsV2","bucket":"confined-bucket","action":{"type":"error","status":403}}"#,
    )
    .await;
    assert!(
        object_store
            .head("missing")
            .await
            .expect_err("a denied listing remains an error")
            .detail
            .contains("AccessDenied")
    );
    let denied_get = object_store
        .get("missing")
        .await
        .expect_err("a denied listing also fails GET");
    assert!(
        denied_get
            .detail
            .contains("disambiguating ListObjectsV2 failed")
    );
    assert!(denied_get.detail.contains("original read"));
    assert!(
        object_store
            .get("existing")
            .await
            .expect("object reads still work")
            .is_some()
    );
    let startup_error = S3ObjectStore::new(startup_config)
        .await
        .err()
        .expect("a denied child listing prevents startup");
    assert!(
        startup_error
            .detail
            .contains("s3:ListBucket with StringLike s3:prefix \"confined/*\""),
        "{startup_error}"
    );
    server.shutdown().await;
}

#[tokio::test]
async fn s3_denied_read_retries_when_key_appears_before_listing() {
    use s3mock_server::{ServerConfig, SignatureVerification};

    let directory = TempDir::new().expect("the test directory opens");
    let store = Store::open(&StoreConfig {
        root_directory: directory.path().join("s3"),
        durability: Durability::Relaxed,
        etag_mode: EtagMode::Opaque,
    })
    .expect("the S3 store opens");
    store
        .create_bucket("race-bucket", false)
        .expect("the bucket is created");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the listener binds");
    let server = s3mock_server::start_with_config(
        Arc::new(store),
        listener,
        ServerConfig {
            signature_verification: SignatureVerification::Strict,
            ..ServerConfig::default()
        },
    )
    .expect("the S3 server starts");
    let mut config = S3ObjectStoreConfig::new("us-east-1", "race-bucket");
    config.endpoint = Some(format!("http://{}", server.local_address()));
    config.key_prefix = "confined".to_owned();
    config.force_path_style = true;
    config.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: None,
    };
    let object_store = Arc::new(S3ObjectStore::new(config).await.expect("the client opens"));
    set_s3mock_fault(
        server.local_address(),
        r#"{"operation":"GetObject","key_prefix":"confined/race","action":{"type":"error","status":403}},{"operation":"ListObjectsV2","action":{"type":"delay","milliseconds":5000}}"#,
    )
    .await;
    let reading_store = object_store.clone();
    let read = tokio::spawn(async move { reading_store.get("race").await });
    tokio::time::timeout(std::time::Duration::from_secs(5), async {
        loop {
            let requests = s3mock_request_log(server.local_address()).await;
            if requests.contains("\"key\":\"confined/race\"") && requests.contains("\"status\":403")
            {
                break;
            }
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("the first GET is denied before the key is created");
    object_store
        .put(
            "race",
            Bytes::from_static(b"created"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the key is created before the listing completes");
    set_s3mock_fault(server.local_address(), "").await;
    let content = read
        .await
        .expect("the read task completes")
        .expect("the read succeeds")
        .expect("the created key is found");
    assert_eq!(content.bytes, Bytes::from_static(b"created"));
    server.shutdown().await;
}

async fn set_s3mock_fault(address: std::net::SocketAddr, rule: &str) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let body = format!("{{\"rules\":[{rule}]}}");
    let mut stream = tokio::net::TcpStream::connect(address)
        .await
        .expect("the conformance operation succeeds");
    let request = format!(
        "PUT /_s3mock/faults HTTP/1.1\r\nHost: {address}\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{body}",
        body.len()
    );
    stream
        .write_all(request.as_bytes())
        .await
        .expect("the conformance operation succeeds");
    let mut response = Vec::new();
    stream
        .read_to_end(&mut response)
        .await
        .expect("the conformance operation succeeds");
    assert!(
        response.starts_with(b"HTTP/1.1 200"),
        "{}",
        String::from_utf8_lossy(&response)
    );
}

async fn s3mock_request_log(address: std::net::SocketAddr) -> String {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let mut stream = tokio::net::TcpStream::connect(address)
        .await
        .expect("the admin endpoint is reachable");
    stream
        .write_all(
            format!(
                "GET /_s3mock/requests HTTP/1.1\r\nHost: {address}\r\nConnection: close\r\n\r\n"
            )
            .as_bytes(),
        )
        .await
        .expect("the admin request is sent");
    let mut response = Vec::new();
    stream
        .read_to_end(&mut response)
        .await
        .expect("the admin response is complete");
    String::from_utf8(response).expect("the admin response is UTF-8")
}

#[tokio::test]
async fn s3_conflicts_and_ambiguous_writes() {
    use dexzd_object_store::ObjectStoreErrorKind;
    let directory = TempDir::new().expect("the conformance operation succeeds");
    let store = Arc::new(
        Store::open(&StoreConfig {
            root_directory: directory.path().to_path_buf(),
            durability: Durability::Relaxed,
            etag_mode: EtagMode::Opaque,
        })
        .expect("the conformance operation succeeds"),
    );
    store
        .create_bucket("fault-bucket", false)
        .expect("the conformance operation succeeds");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the conformance operation succeeds");
    let server = s3mock_server::start_with_config(
        Arc::clone(&store),
        listener,
        s3mock_server::ServerConfig {
            fault_seed: 3,
            ..s3mock_server::ServerConfig::default()
        },
    )
    .expect("the conformance operation succeeds");
    let mut config = S3ObjectStoreConfig::new("us-east-1", "fault-bucket");
    config.endpoint = Some(format!("http://{}", server.local_address()));
    config.force_path_style = true;
    config.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: None,
    };
    let object_store = S3ObjectStore::new(config)
        .await
        .expect("the conformance operation succeeds");

    set_s3mock_fault(server.local_address(), r#"{"operation":"PutObject","bucket":"fault-bucket","key_prefix":"conflict-once/","has_conditional_header":true,"probability":0.5,"action":{"type":"error","status":409}}"#).await;
    assert!(matches!(
        object_store
            .put(
                "conflict-once/key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("a one-time conflict is retried"),
        WriteOutcome::Written { .. }
    ));
    let once_log = s3mock_request_log(server.local_address()).await;
    assert_eq!(
        once_log
            .matches("\"key\":\"conflict-once/key\",\"operation\":\"PutObject\",\"status\":409")
            .count(),
        1
    );

    set_s3mock_fault(server.local_address(), r#"{"operation":"PutObject","bucket":"fault-bucket","key_prefix":"conflict/","has_conditional_header":true,"action":{"type":"error","status":409}}"#).await;
    assert_eq!(
        object_store
            .put(
                "conflict/key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("persistent HTTP 409 remains retryable")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    assert!(
        object_store
            .head("conflict/key")
            .await
            .expect("the key can be read")
            .is_none()
    );
    let conflict_request_log = s3mock_request_log(server.local_address()).await;
    assert_eq!(
        conflict_request_log
            .matches("\"key\":\"conflict/key\",\"operation\":\"PutObject\",\"status\":409")
            .count(),
        3,
        "{conflict_request_log}"
    );
    set_s3mock_fault(server.local_address(), r#"{"operation":"CompleteMultipartUpload","bucket":"fault-bucket","key_prefix":"multipart-conflict/","has_conditional_header":true,"action":{"type":"error","status":409}}"#).await;
    assert_eq!(
        object_store
            .put(
                "multipart-conflict/bytes",
                Bytes::from(vec![42; 9 * 1024 * 1024]),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("persistent multipart HTTP 409 remains retryable")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    let file_directory = TempDir::new().expect("the multipart fixture directory is available");
    let file_path = file_directory.path().join("payload");
    tokio::fs::write(&file_path, vec![7; 9 * 1024 * 1024])
        .await
        .expect("the multipart fixture is written");
    assert_eq!(
        object_store
            .put_file(
                "multipart-conflict/file",
                &file_path,
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("persistent multipart file HTTP 409 remains retryable")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    let uploads = store
        .list_multipart_uploads(
            "fault-bucket",
            &s3mock::ListMultipartUploadsRequest {
                prefix: "multipart-conflict/".to_owned(),
                delimiter: String::new(),
                key_marker: None,
                upload_id_marker: None,
                max_uploads: 1000,
            },
        )
        .expect("multipart uploads can be inspected");
    assert!(uploads.uploads.is_empty());
    let request_log = s3mock_request_log(server.local_address()).await;
    for key in ["multipart-conflict/bytes", "multipart-conflict/file"] {
        assert_eq!(
            request_log
                .matches(&format!(
                    "\"key\":\"{key}\",\"operation\":\"CompleteMultipartUpload\",\"status\":409"
                ))
                .count(),
            3
        );
    }
    set_s3mock_fault(server.local_address(), r#"{"operation":"PutObject","bucket":"fault-bucket","key_prefix":"failed/","action":{"type":"error","status":500}}"#).await;
    assert_eq!(
        object_store
            .put(
                "failed/key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::OutcomeUnknown
    );
    set_s3mock_fault(server.local_address(), r#"{"operation":"PutObject","bucket":"fault-bucket","key_prefix":"throttled/","action":{"type":"error","status":503}}"#).await;
    assert_eq!(
        object_store
            .put(
                "throttled/key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("a 503 SlowDown write is retryable")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    set_s3mock_fault(server.local_address(), r#"{"operation":"UploadPart","bucket":"fault-bucket","key_prefix":"part-failure/","action":{"type":"error","status":500}}"#).await;
    assert_eq!(
        object_store
            .put(
                "part-failure/key",
                Bytes::from(vec![7; 9 * 1024 * 1024]),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("a failed part is retryable before completion")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    let unfinished = store
        .list_multipart_uploads(
            "fault-bucket",
            &s3mock::ListMultipartUploadsRequest {
                prefix: "part-failure/".to_owned(),
                delimiter: String::new(),
                key_marker: None,
                upload_id_marker: None,
                max_uploads: 1000,
            },
        )
        .expect("failed part uploads can be inspected");
    assert!(unfinished.uploads.is_empty());

    let mut timeout_config = S3ObjectStoreConfig::new("us-east-1", "fault-bucket");
    timeout_config.endpoint = Some(format!("http://{}", server.local_address()));
    timeout_config.force_path_style = true;
    timeout_config.request_timeout = std::time::Duration::from_millis(40);
    timeout_config.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: None,
    };
    let timeout_store = S3ObjectStore::new(timeout_config)
        .await
        .expect("the timeout store is configured");
    set_s3mock_fault(server.local_address(), r#"{"operation":"PutObject","bucket":"fault-bucket","key_prefix":"delayed/","action":{"type":"delay","milliseconds":250}}"#).await;
    assert_eq!(
        timeout_store
            .put(
                "delayed/key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("a timed-out write has an unknown outcome")
            .kind,
        ObjectStoreErrorKind::OutcomeUnknown
    );
    set_s3mock_fault(server.local_address(), r#"{"operation":"PutObject","bucket":"fault-bucket","key_prefix":"committed/","action":{"type":"commit_then_drop"}}"#).await;
    let committed_error = object_store
        .put(
            "committed/key",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("the conformance operation fails");
    assert_eq!(
        committed_error.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{committed_error}"
    );
    assert_eq!(
        object_store
            .get("committed/key")
            .await
            .expect("the conformance operation succeeds")
            .expect("the conformance operation succeeds")
            .bytes,
        Bytes::from_static(b"body")
    );
    set_s3mock_fault(server.local_address(), r#"{"operation":"GetObject","bucket":"fault-bucket","key_prefix":"committed/","action":{"type":"error","status":500}}"#).await;
    assert_eq!(
        object_store
            .get("committed/key")
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    set_s3mock_fault(server.local_address(), r#"{"operation":"GetObject","bucket":"fault-bucket","key_prefix":"committed/","action":{"type":"truncate_response"}}"#).await;
    let download_directory = TempDir::new().expect("the download fixture directory is available");
    let target = download_directory.path().join("incomplete");
    assert_eq!(
        object_store
            .get_to_file("committed/key", &target)
            .await
            .expect_err("a truncated download fails")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    assert!(!target.exists());
    assert_no_temporary_downloads(download_directory.path());
    set_s3mock_fault(server.local_address(), r#"{"operation":"GetObject","bucket":"fault-bucket","key_prefix":"unused/","action":{"type":"truncate_response"}}"#).await;
    assert_eq!(
        object_store
            .get_to_file("committed/key", &target)
            .await
            .expect("the download retry succeeds"),
        Some(4)
    );
    assert_eq!(
        tokio::fs::read(&target)
            .await
            .expect("the completed download is readable"),
        b"body"
    );
    assert_no_temporary_downloads(download_directory.path());
    server.shutdown().await;
}

#[tokio::test]
async fn s3_connection_refused_is_transient() {
    use dexzd_object_store::ObjectStoreErrorKind;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the conformance operation succeeds");
    let address = listener
        .local_addr()
        .expect("the conformance operation succeeds");
    drop(listener);
    let mut config = S3ObjectStoreConfig::new("us-east-1", "unreachable-bucket");
    config.endpoint = Some(format!("http://{address}"));
    config.force_path_style = true;
    config.request_timeout = std::time::Duration::from_secs(2);
    config.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: None,
    };
    let store = S3ObjectStore::new(config)
        .await
        .expect("the conformance operation succeeds");
    assert_eq!(
        store
            .get("key")
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Transient
    );
    assert_eq!(
        store
            .put(
                "key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("the conformance operation fails")
            .kind,
        ObjectStoreErrorKind::Transient
    );
}

#[tokio::test]
async fn s3_requests_to_a_server_that_stops_reading_time_out() {
    use dexzd_object_store::ObjectStoreErrorKind;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the stalled endpoint binds");
    let address = listener
        .local_addr()
        .expect("the stalled endpoint has an address");
    // Accepts connections and never reads them, like a server that stopped mid-request.
    let stalled_endpoint = tokio::spawn(async move {
        let mut connections = Vec::new();
        while let Ok((connection, _)) = listener.accept().await {
            connections.push(connection);
        }
    });
    let mut config = S3ObjectStoreConfig::new("us-east-1", "stalled-bucket");
    config.endpoint = Some(format!("http://{address}"));
    config.force_path_style = true;
    config.request_timeout = std::time::Duration::from_secs(1);
    config.multipart_threshold = 5 * 1024 * 1024;
    config.credentials = S3Credentials::Static {
        access_key_id: "s3mock".to_owned(),
        secret_access_key: "s3mock".to_owned(),
        session_token: None,
    };
    let store = S3ObjectStore::new(config)
        .await
        .expect("the stalled store is configured");
    let bound = std::time::Duration::from_secs(10);
    let failure_bound = std::time::Duration::from_secs(3);
    let started = std::time::Instant::now();
    let small_put = tokio::time::timeout(
        bound,
        store.put(
            "small",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        ),
    )
    .await
    .expect("a write without a response ends")
    .expect_err("a write without a response fails");
    assert_eq!(
        small_put.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{small_put}"
    );
    assert!(
        started.elapsed() < failure_bound,
        "small put exceeded the failure bound"
    );
    // Larger than the loopback socket buffers, so the client stalls while sending the body.
    let started = std::time::Instant::now();
    let large_put = tokio::time::timeout(
        bound,
        store.put(
            "large",
            Bytes::from(vec![7; 4 * 1024 * 1024]),
            WriteCondition::CreateOnly,
        ),
    )
    .await
    .expect("a write whose body is never read ends")
    .expect_err("a write whose body is never read fails");
    assert_eq!(
        large_put.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{large_put}"
    );
    assert!(
        started.elapsed() < failure_bound,
        "large put exceeded the failure bound"
    );
    let started = std::time::Instant::now();
    let multipart_put = tokio::time::timeout(
        bound,
        store.put(
            "multipart",
            Bytes::from(vec![7; 6 * 1024 * 1024]),
            WriteCondition::CreateOnly,
        ),
    )
    .await
    .expect("a multipart write without a response ends")
    .expect_err("a multipart write without a response fails");
    assert_eq!(
        multipart_put.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{multipart_put}"
    );
    assert!(
        started.elapsed() < failure_bound,
        "multipart put exceeded the failure bound"
    );
    let directory = TempDir::new().expect("the stalled upload fixture directory is available");
    let small_file = directory.path().join("small-upload");
    tokio::fs::write(&small_file, b"body")
        .await
        .expect("the small upload fixture is written");
    let started = std::time::Instant::now();
    let small_file_put = tokio::time::timeout(
        bound,
        store.put_file("small-file", &small_file, WriteCondition::CreateOnly),
    )
    .await
    .expect("a file write without a response ends")
    .expect_err("a file write without a response fails");
    assert_eq!(
        small_file_put.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{small_file_put}"
    );
    assert!(
        started.elapsed() < failure_bound,
        "small file put exceeded the failure bound"
    );
    let multipart_file = directory.path().join("multipart-upload");
    tokio::fs::write(&multipart_file, vec![7; 6 * 1024 * 1024])
        .await
        .expect("the multipart upload fixture is written");
    let started = std::time::Instant::now();
    let multipart_file_put = tokio::time::timeout(
        bound,
        store.put_file(
            "multipart-file",
            &multipart_file,
            WriteCondition::CreateOnly,
        ),
    )
    .await
    .expect("a multipart file write without a response ends")
    .expect_err("a multipart file write without a response fails");
    assert_eq!(
        multipart_file_put.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{multipart_file_put}"
    );
    assert!(
        started.elapsed() < failure_bound,
        "multipart file put exceeded the failure bound"
    );
    let head = tokio::time::timeout(bound, store.head("small"))
        .await
        .expect("a read without a response ends")
        .expect_err("a read without a response fails");
    assert_eq!(head.kind, ObjectStoreErrorKind::Transient, "{head}");
    stalled_endpoint.abort();
}
