// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Conformance of the Aliyun OSS dialect against s3local's Aliyun OSS emulation.

use std::net::SocketAddr;
use std::sync::Arc;

use bytes::Bytes;
use dexzd_object_store::{
    ByteRange, ObjectStore, ObjectStoreErrorKind, S3Credentials, S3ObjectStore,
    S3ObjectStoreConfig, WriteCondition, WriteOutcome,
};
use s3local::{Durability, EtagMode, Store, StoreConfig};
use tempfile::TempDir;

use super::recording_s3_proxy::{ProxyTampering, RecordingS3Proxy};
use super::{assert_no_temporary_downloads, s3local_request_log, set_s3local_fault};

const BUCKET: &str = "aliyun-oss-dialect";
const KEY_PREFIX: &str = "dexzd/aliyun";
const PART_SIZE: usize = 5 * 1024 * 1024;

struct AliyunOssEmulator {
    _directory: TempDir,
    store: Arc<Store>,
    server: s3local_server::ServerHandle,
}

impl AliyunOssEmulator {
    async fn start(etag_mode: EtagMode) -> Self {
        let directory = TempDir::new().expect("the emulator directory is created");
        let store = Store::open(&StoreConfig {
            root_directory: directory.path().to_path_buf(),
            durability: Durability::Relaxed,
            etag_mode,
        })
        .expect("the emulator store opens");
        store
            .create_bucket(BUCKET, false)
            .expect("the emulator bucket is created");
        let store = Arc::new(store);
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("the emulator binds");
        let server = s3local_server::start_with_config(
            Arc::clone(&store),
            listener,
            s3local_server::ServerConfig {
                api_dialect: s3local_server::S3ApiDialect::AliyunOss,
                ..s3local_server::ServerConfig::default()
            },
        )
        .expect("the emulator starts");
        Self {
            _directory: directory,
            store,
            server,
        }
    }

    fn address(&self) -> SocketAddr {
        self.server.local_address()
    }

    async fn object_store(&self) -> S3ObjectStore {
        object_store_at(self.address()).await
    }

    fn unfinished_upload_count(&self) -> usize {
        self.store
            .list_multipart_uploads(
                BUCKET,
                &s3local::ListMultipartUploadsRequest {
                    prefix: String::new(),
                    delimiter: String::new(),
                    key_marker: None,
                    upload_id_marker: None,
                    max_uploads: 1000,
                },
            )
            .expect("multipart uploads can be inspected")
            .uploads
            .len()
    }

    async fn shutdown(self) {
        self.server.shutdown().await;
    }
}

async fn object_store_at(address: SocketAddr) -> S3ObjectStore {
    let mut config = S3ObjectStoreConfig::new("us-east-1", BUCKET);
    config.endpoint = Some(format!("http://{address}"));
    config.key_prefix = KEY_PREFIX.to_owned();
    config.force_path_style = true;
    config.credentials = S3Credentials::Static {
        access_key_id: "s3local".to_owned(),
        secret_access_key: "s3local".to_owned(),
        session_token: None,
    };
    config.multipart_threshold = PART_SIZE;
    config.multipart_part_size = PART_SIZE;
    config.api_dialect = dexzd_object_store::S3ApiDialect::AliyunOss;
    S3ObjectStore::new(config)
        .await
        .expect("the Aliyun OSS dialect store is configured")
}

fn replace_condition() -> WriteCondition {
    WriteCondition::ReplaceIfEtagMatches {
        etag: "\"any\"".to_owned(),
    }
}

/// Runs every case of the shared conformance suite that needs no atomic replacement.
pub(super) async fn check_store_without_atomic_replacement(store: &dyn ObjectStore) {
    assert!(store.get("missing").await.expect("get succeeds").is_none());
    assert!(
        store
            .head("missing")
            .await
            .expect("head succeeds")
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
            .expect("get_range succeeds")
            .is_none()
    );
    let WriteOutcome::Written { etag } = store
        .put(
            "objects/a",
            Bytes::from_static(b"abcdef"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the initial create succeeds")
    else {
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
            .expect("a duplicate create reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    let content = store
        .get("objects/a")
        .await
        .expect("get succeeds")
        .expect("the object exists");
    assert_eq!(content.bytes, Bytes::from_static(b"abcdef"));
    assert_eq!(content.etag, etag);
    let range = store
        .get_range(
            "objects/a",
            ByteRange {
                start: 2,
                end_inclusive: 100,
            },
        )
        .await
        .expect("get_range succeeds")
        .expect("the object exists");
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
        .expect("get_range succeeds")
        .expect("the object exists");
    assert!(beyond.bytes.is_empty());
    assert_eq!(beyond.object_size, 6);
    for key in ["objects/b", "objects/c"] {
        assert!(matches!(
            store
                .put(key, Bytes::from_static(b"b"), WriteCondition::Unconditional)
                .await
                .expect("an unconditional put succeeds"),
            WriteOutcome::Written { .. }
        ));
    }
    assert!(matches!(
        store
            .put(
                "objects/b",
                Bytes::from_static(b"b2"),
                WriteCondition::Unconditional
            )
            .await
            .expect("an unconditional put replaces the object"),
        WriteOutcome::Written { .. }
    ));
    let first = store
        .list("objects/", None, 2)
        .await
        .expect("list succeeds");
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
    let head = store
        .head("objects/a")
        .await
        .expect("head succeeds")
        .expect("the object exists");
    assert_eq!(head.size, 6);
    assert_eq!(head.etag, etag);
    assert!(head.last_modified_unix_micros.is_some());
    let second = store
        .list("objects/", first.next_start_after.as_deref(), 2)
        .await
        .expect("list succeeds");
    assert_eq!(
        second
            .objects
            .iter()
            .map(|object| object.key.as_str())
            .collect::<Vec<_>>(),
        ["objects/c"]
    );
    assert!(second.next_start_after.is_none());

    let directory = tempfile::tempdir().expect("the file directory is created");
    let input = directory.path().join("input");
    let output = directory.path().join("output");
    tokio::fs::write(&input, b"from-file")
        .await
        .expect("the input file is written");
    assert!(matches!(
        store
            .put_file("file", &input, WriteCondition::CreateOnly)
            .await
            .expect("a file create succeeds"),
        WriteOutcome::Written { .. }
    ));
    assert_eq!(
        store
            .put_file("file", &input, WriteCondition::CreateOnly)
            .await
            .expect("a duplicate file create reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .get_to_file("file", &output)
            .await
            .expect("get_to_file succeeds"),
        Some(9)
    );
    assert_eq!(
        tokio::fs::read(&output)
            .await
            .expect("the download is readable"),
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
            .expect("get_to_file succeeds"),
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
        .expect("a batch delete succeeds");
    assert!(
        store
            .get("objects/a")
            .await
            .expect("get succeeds")
            .is_none()
    );
    assert!(
        store
            .get("objects/b")
            .await
            .expect("get succeeds")
            .is_none()
    );
    store.delete("objects/c").await.expect("a delete succeeds");
    store
        .delete("objects/c")
        .await
        .expect("deleting a missing object succeeds");
    store
        .delete_many(&[])
        .await
        .expect("an empty batch delete succeeds");
    assert_eq!(
        store
            .delete_many(&vec!["unused".to_owned(); 1001])
            .await
            .expect_err("an oversized batch delete fails")
            .kind,
        ObjectStoreErrorKind::Permanent
    );

    let large = Bytes::from(vec![42; 6 * 1024 * 1024]);
    assert!(matches!(
        store
            .put("large", large.clone(), WriteCondition::CreateOnly)
            .await
            .expect("a multipart create succeeds"),
        WriteOutcome::Written { .. }
    ));
    assert_eq!(
        store
            .put("large", large.clone(), WriteCondition::CreateOnly)
            .await
            .expect("a duplicate multipart create reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .get("large")
            .await
            .expect("get succeeds")
            .expect("the object exists")
            .bytes,
        large
    );
    let large_file = directory.path().join("large-file");
    tokio::fs::write(&large_file, vec![7; 6 * 1024 * 1024])
        .await
        .expect("the large file is written");
    assert!(matches!(
        store
            .put_file("large-file", &large_file, WriteCondition::CreateOnly)
            .await
            .expect("a multipart file create succeeds"),
        WriteOutcome::Written { .. }
    ));
    assert_eq!(
        store
            .put_file("large-file", &large_file, WriteCondition::CreateOnly)
            .await
            .expect("a duplicate multipart file create reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(
        store
            .head("large-file")
            .await
            .expect("head succeeds")
            .expect("the object exists")
            .size,
        6 * 1024 * 1024
    );
}

#[tokio::test]
async fn aliyun_oss_conformance() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let store = emulator.object_store().await;
    check_store_without_atomic_replacement(&store).await;
    assert_eq!(emulator.unfinished_upload_count(), 0);
    emulator.shutdown().await;
}

#[tokio::test]
async fn aliyun_oss_replace_if_etag_matches_fails_before_any_request() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let store = emulator.object_store().await;
    store
        .put(
            "replace/existing",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the object is created");
    let file_directory = TempDir::new().expect("the file directory is created");
    let small_file = file_directory.path().join("small");
    let large_file = file_directory.path().join("large");
    tokio::fs::write(&small_file, b"small")
        .await
        .expect("the small file is written");
    tokio::fs::write(&large_file, vec![3; 6 * 1024 * 1024])
        .await
        .expect("the large file is written");
    let attempts = [
        (
            "replace/existing",
            store
                .put(
                    "replace/existing",
                    Bytes::from_static(b"next"),
                    replace_condition(),
                )
                .await,
        ),
        (
            "replace/missing",
            store
                .put(
                    "replace/missing",
                    Bytes::from_static(b"next"),
                    replace_condition(),
                )
                .await,
        ),
        (
            "replace/large",
            store
                .put(
                    "replace/large",
                    Bytes::from(vec![1; 6 * 1024 * 1024]),
                    replace_condition(),
                )
                .await,
        ),
        (
            "replace/small-file",
            store
                .put_file("replace/small-file", &small_file, replace_condition())
                .await,
        ),
        (
            "replace/large-file",
            store
                .put_file("replace/large-file", &large_file, replace_condition())
                .await,
        ),
    ];
    let request_log = s3local_request_log(emulator.address()).await;
    for (key, result) in attempts {
        let error = result.expect_err("atomic replacement is unsupported");
        assert_eq!(error.kind, ObjectStoreErrorKind::Permanent, "{error}");
        assert_eq!(error.key, key);
        assert!(error.to_string().contains(key), "{error}");
        assert!(error.detail.contains("ReplaceIfEtagMatches"), "{error}");
        if key != "replace/existing" {
            assert!(
                !request_log.contains(&format!("\"key\":\"{KEY_PREFIX}/{key}\"")),
                "{request_log}"
            );
        }
    }
    assert_eq!(
        request_log
            .matches(&format!(
                "\"key\":\"{KEY_PREFIX}/replace/existing\",\"operation\":\"PutObject\""
            ))
            .count(),
        1,
        "{request_log}"
    );
    assert_eq!(
        store
            .get("replace/existing")
            .await
            .expect("get succeeds")
            .expect("the object remains")
            .bytes,
        Bytes::from_static(b"body")
    );
    assert_eq!(emulator.unfinished_upload_count(), 0);
    emulator.shutdown().await;
}

#[tokio::test]
async fn aliyun_oss_duplicate_create_is_one_unretried_rejection_on_every_path() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let proxy = RecordingS3Proxy::start(emulator.address()).await;
    let store = object_store_at(proxy.address()).await;
    let directory = TempDir::new().expect("the file directory is created");
    let small_file = directory.path().join("small");
    let large_file = directory.path().join("large");
    tokio::fs::write(&small_file, b"small file")
        .await
        .expect("the small file is written");
    let large_body = vec![9; 6 * 1024 * 1024];
    tokio::fs::write(&large_file, &large_body)
        .await
        .expect("the large file is written");
    for key in ["paths/put", "paths/put-multipart"] {
        let body = if key == "paths/put" {
            Bytes::from_static(b"single")
        } else {
            Bytes::from(vec![5; 6 * 1024 * 1024])
        };
        let created = store
            .put(key, body.clone(), WriteCondition::CreateOnly)
            .await
            .expect("the create succeeds");
        let WriteOutcome::Written { etag } = created else {
            panic!("the first create of {key} must succeed")
        };
        assert_eq!(
            store
                .put(
                    key,
                    Bytes::from_static(b"other"),
                    WriteCondition::CreateOnly
                )
                .await
                .expect("a small duplicate reports the condition"),
            WriteOutcome::PreconditionFailed
        );
        assert_eq!(
            store
                .put(key, body.clone(), WriteCondition::CreateOnly)
                .await
                .expect("a duplicate reports the condition"),
            WriteOutcome::PreconditionFailed
        );
        let content = store
            .get(key)
            .await
            .expect("get succeeds")
            .expect("the object exists");
        assert_eq!(content.bytes, body);
        assert_eq!(content.etag, etag);
    }
    for (key, path) in [
        ("paths/put-file", &small_file),
        ("paths/put-file-multipart", &large_file),
    ] {
        assert!(matches!(
            store
                .put_file(key, path, WriteCondition::CreateOnly)
                .await
                .expect("the file create succeeds"),
            WriteOutcome::Written { .. }
        ));
        assert_eq!(
            store
                .put_file(key, path, WriteCondition::CreateOnly)
                .await
                .expect("a duplicate file create reports the condition"),
            WriteOutcome::PreconditionFailed
        );
    }
    assert_eq!(
        store
            .get("paths/put-file-multipart")
            .await
            .expect("get succeeds")
            .expect("the object exists")
            .bytes,
        Bytes::from(large_body)
    );
    let request_log = s3local_request_log(emulator.address()).await;
    // One 409 per duplicate shows that FileAlreadyExists is never retried.
    for (key, operation, rejections) in [
        ("paths/put", "PutObject", 2),
        ("paths/put-multipart", "PutObject", 1),
        ("paths/put-multipart", "CompleteMultipartUpload", 1),
        ("paths/put-file", "PutObject", 1),
        ("paths/put-file-multipart", "CompleteMultipartUpload", 1),
    ] {
        assert_eq!(
            request_log
                .matches(&format!(
                    "\"key\":\"{KEY_PREFIX}/{key}\",\"operation\":\"{operation}\",\"status\":409"
                ))
                .count(),
            rejections,
            "{key} {operation}: {request_log}"
        );
    }
    assert_eq!(emulator.unfinished_upload_count(), 0);

    let writes = proxy.request_headers(&[
        format!("PUT /{BUCKET}/{KEY_PREFIX}/paths/").as_bytes(),
        format!("POST /{BUCKET}/{KEY_PREFIX}/paths/").as_bytes(),
    ]);
    let header_value = |request: &str, name: &str| {
        request.lines().find_map(|line| {
            line.split_once(':')
                .filter(|(header, _)| header.trim().eq_ignore_ascii_case(name))
                .map(|(_, value)| value.trim().to_owned())
        })
    };
    let mut creates = 0;
    let mut parts = 0;
    for request in &writes {
        let request_line = request.lines().next().unwrap_or_default();
        assert!(
            header_value(request, "if-none-match").is_none(),
            "{request}"
        );
        assert!(header_value(request, "if-match").is_none(), "{request}");
        let is_upload_part = request_line.contains("partNumber=");
        let is_create_upload = request_line.contains("?uploads");
        let is_completion = request_line.starts_with("POST") && request_line.contains("uploadId=");
        if is_upload_part {
            parts += 1;
            assert!(header_value(request, "content-md5").is_some(), "{request}");
            assert!(
                header_value(request, "x-amz-checksum-crc32").is_none(),
                "{request}"
            );
        } else if is_create_upload {
            assert!(
                header_value(request, "x-amz-checksum-algorithm").is_none(),
                "{request}"
            );
        } else {
            creates += 1;
            assert_eq!(
                header_value(request, "x-oss-forbid-overwrite").as_deref(),
                Some("true"),
                "{request}"
            );
            if !is_completion {
                assert!(header_value(request, "content-md5").is_some(), "{request}");
            }
        }
    }
    assert_eq!(
        creates, 10,
        "every create and duplicate is a forbid-overwrite write"
    );
    assert_eq!(
        parts, 8,
        "each of the four multipart attempts uploads two parts"
    );

    let keys = ["paths/put".to_owned(), "paths/put-file".to_owned()];
    store
        .delete_many(&keys)
        .await
        .expect("the batch delete succeeds");
    let deletes: Vec<String> = proxy
        .request_headers(&[format!("POST /{BUCKET}").as_bytes()])
        .into_iter()
        .filter(|request| {
            request
                .lines()
                .next()
                .is_some_and(|line| line.contains("delete"))
        })
        .collect();
    assert_eq!(deletes.len(), 1);
    assert!(
        header_value(&deletes[0], "content-md5").is_some(),
        "{}",
        deletes[0]
    );
    proxy.shutdown().await;
    emulator.shutdown().await;
}

#[tokio::test]
async fn aliyun_oss_concurrent_creators_have_exactly_one_winner() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let store = Arc::new(emulator.object_store().await);
    for round in 0..10 {
        let key = format!("race/{round}");
        let mut creators = tokio::task::JoinSet::new();
        for creator in 0..32 {
            let store = Arc::clone(&store);
            let key = key.clone();
            creators.spawn(async move {
                let body = Bytes::from(format!("creator-{creator}"));
                let outcome = store
                    .put(&key, body.clone(), WriteCondition::CreateOnly)
                    .await
                    .expect("a concurrent create reaches a definite outcome");
                (body, outcome)
            });
        }
        let mut winners = Vec::new();
        while let Some(joined) = creators.join_next().await {
            let (body, outcome) = joined.expect("a creator task completes");
            match outcome {
                WriteOutcome::Written { etag } => winners.push((body, etag)),
                WriteOutcome::PreconditionFailed => {}
            }
        }
        assert_eq!(winners.len(), 1, "round {round} has exactly one winner");
        let content = store
            .get(&key)
            .await
            .expect("get succeeds")
            .expect("the winner's object exists");
        assert_eq!(content.bytes, winners[0].0);
        assert_eq!(content.etag, winners[0].1);
    }
    emulator.shutdown().await;
}

#[tokio::test]
async fn aliyun_oss_large_file_and_large_batch_delete_round_trip() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let store = emulator.object_store().await;
    let directory = TempDir::new().expect("the file directory is created");
    let input = directory.path().join("snapshot");
    let body: Vec<u8> = (0..20 * 1024 * 1024)
        .map(|index: usize| (index % 251) as u8)
        .collect();
    tokio::fs::write(&input, &body)
        .await
        .expect("the 20 MiB file is written");
    assert!(matches!(
        store
            .put_file("snapshot/file", &input, WriteCondition::CreateOnly)
            .await
            .expect("the 20 MiB multipart upload succeeds"),
        WriteOutcome::Written { .. }
    ));
    let output = directory.path().join("downloaded");
    assert_eq!(
        store
            .get_to_file("snapshot/file", &output)
            .await
            .expect("the download succeeds"),
        Some(body.len() as u64)
    );
    assert!(
        tokio::fs::read(&output)
            .await
            .expect("the download is readable")
            == body,
        "the downloaded file equals the uploaded file"
    );
    let uploaded = emulator
        .store
        .head_object(
            BUCKET,
            &format!("{KEY_PREFIX}/snapshot/file"),
            &s3local::ObjectReadRequest::default(),
        )
        .expect("the uploaded object is readable")
        .record;
    assert_eq!(uploaded.parts_count, Some(4));

    let keys: Vec<String> = (0..1000).map(|index| format!("batch/{index:04}")).collect();
    let mut writers = tokio::task::JoinSet::new();
    let store = Arc::new(store);
    for chunk in keys.chunks(100) {
        let store = Arc::clone(&store);
        let chunk = chunk.to_vec();
        writers.spawn(async move {
            for key in chunk {
                store
                    .put(&key, Bytes::from_static(b"x"), WriteCondition::CreateOnly)
                    .await
                    .expect("a batch object is created");
            }
        });
    }
    while let Some(joined) = writers.join_next().await {
        joined.expect("a writer task completes");
    }
    assert_eq!(
        store
            .list("batch/", None, 1000)
            .await
            .expect("list succeeds")
            .objects
            .len(),
        1000
    );
    store
        .delete_many(&keys)
        .await
        .expect("a 1000-key batch delete succeeds");
    assert!(
        store
            .list("batch/", None, 1000)
            .await
            .expect("list succeeds")
            .objects
            .is_empty()
    );
    emulator.shutdown().await;
}

#[tokio::test]
async fn aliyun_oss_rejects_parts_that_fail_their_md5() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let proxy = RecordingS3Proxy::start_with_forwarding(
        emulator.address(),
        ProxyTampering::CorruptFirstUploadPartBody,
    )
    .await;
    let store = object_store_at(proxy.address()).await;
    let directory = TempDir::new().expect("the file directory is created");
    let input = directory.path().join("corrupted-in-transit");
    tokio::fs::write(&input, vec![0x33; 6 * 1024 * 1024])
        .await
        .expect("the file is written");
    let error = store
        .put_file("corrupt/transit", &input, WriteCondition::CreateOnly)
        .await
        .expect_err("a part corrupted in transit is rejected");
    assert_eq!(error.kind, ObjectStoreErrorKind::Permanent, "{error}");
    assert!(error.detail.contains("code=BadDigest"), "{error}");
    // The rejected part carried Content-MD5 and no other checksum, so only the MD5 check failed it.
    let part_requests =
        proxy.request_headers(&[format!("PUT /{BUCKET}/{KEY_PREFIX}/corrupt/transit?").as_bytes()]);
    assert_eq!(part_requests.len(), 1, "{part_requests:?}");
    let part_header_names: Vec<String> = part_requests[0]
        .lines()
        .skip(1)
        .filter_map(|line| line.split_once(':'))
        .map(|(name, _)| name.trim().to_ascii_lowercase())
        .collect();
    assert!(
        part_header_names.iter().any(|name| name == "content-md5"),
        "{}",
        part_requests[0]
    );
    assert!(
        part_header_names
            .iter()
            .all(|name| !name.starts_with("x-amz-checksum-") && name != "x-amz-trailer"),
        "{}",
        part_requests[0]
    );
    assert!(
        store
            .head("corrupt/transit")
            .await
            .expect("head succeeds")
            .is_none()
    );
    assert_eq!(emulator.unfinished_upload_count(), 0);
    proxy.shutdown().await;

    let bytes_body = Bytes::from(vec![4; 6 * 1024 * 1024]);
    for key in ["corrupt/etag-file", "corrupt/etag-bytes"] {
        let proxy = RecordingS3Proxy::start_with_forwarding(
            emulator.address(),
            ProxyTampering::CorruptFirstResponseEtag,
        )
        .await;
        let store = object_store_at(proxy.address()).await;
        let result = if key == "corrupt/etag-file" {
            store
                .put_file(key, &input, WriteCondition::CreateOnly)
                .await
        } else {
            store
                .put(key, bytes_body.clone(), WriteCondition::CreateOnly)
                .await
        };
        let error = result.expect_err("a part ETag that is not its MD5 is rejected");
        assert_eq!(error.kind, ObjectStoreErrorKind::Corrupt, "{error}");
        assert!(error.detail.contains("part 1 ETag"), "{error}");
        assert!(store.head(key).await.expect("head succeeds").is_none());
        assert_eq!(emulator.unfinished_upload_count(), 0);
        proxy.shutdown().await;
    }
    emulator.shutdown().await;
}

#[tokio::test]
async fn aliyun_oss_conflicts_and_ambiguous_writes() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let store = emulator.object_store().await;
    let address = emulator.address();

    set_s3local_fault(address, &format!(r#"{{"operation":"PutObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/conflict/","action":{{"type":"error","status":409}}}}"#)).await;
    let conflict = store
        .put(
            "conflict/key",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a persistent conflict is not a failed condition");
    assert_eq!(conflict.kind, ObjectStoreErrorKind::Transient, "{conflict}");
    assert!(
        store
            .head("conflict/key")
            .await
            .expect("head succeeds")
            .is_none()
    );
    let conflict_log = s3local_request_log(address).await;
    assert_eq!(
        conflict_log
            .matches(&format!(
                "\"key\":\"{KEY_PREFIX}/conflict/key\",\"operation\":\"PutObject\",\"status\":409"
            ))
            .count(),
        3,
        "{conflict_log}"
    );

    set_s3local_fault(address, &format!(r#"{{"operation":"PutObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/failed/","action":{{"type":"error","status":500}}}}"#)).await;
    assert_eq!(
        store
            .put(
                "failed/key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect_err("a server error on a write is ambiguous")
            .kind,
        ObjectStoreErrorKind::OutcomeUnknown
    );

    set_s3local_fault(address, &format!(r#"{{"operation":"PutObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/committed/","action":{{"type":"commit_then_drop"}}}}"#)).await;
    let dropped = store
        .put(
            "committed/key",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a dropped response is ambiguous");
    assert_eq!(
        dropped.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{dropped}"
    );
    set_s3local_fault(address, &format!(r#"{{"operation":"PutObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/unused/","action":{{"type":"error","status":500}}}}"#)).await;
    assert_eq!(
        store
            .get("committed/key")
            .await
            .expect("get succeeds")
            .expect("the ambiguous write committed")
            .bytes,
        Bytes::from_static(b"body")
    );
    assert_eq!(
        store
            .put(
                "committed/key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("a retried create reports the condition"),
        WriteOutcome::PreconditionFailed
    );

    set_s3local_fault(address, &format!(r#"{{"operation":"CompleteMultipartUpload","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/committed-multipart/","action":{{"type":"commit_then_drop"}}}}"#)).await;
    let directory = TempDir::new().expect("the file directory is created");
    let input = directory.path().join("multipart");
    tokio::fs::write(&input, vec![8; 6 * 1024 * 1024])
        .await
        .expect("the file is written");
    let dropped = store
        .put_file(
            "committed-multipart/file",
            &input,
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a dropped completion is ambiguous");
    assert_eq!(
        dropped.kind,
        ObjectStoreErrorKind::OutcomeUnknown,
        "{dropped}"
    );
    set_s3local_fault(address, &format!(r#"{{"operation":"PutObject","bucket":"{BUCKET}","key_prefix":"{KEY_PREFIX}/unused/","action":{{"type":"error","status":500}}}}"#)).await;
    assert_eq!(
        store
            .head("committed-multipart/file")
            .await
            .expect("head succeeds")
            .expect("the ambiguous completion committed")
            .size,
        6 * 1024 * 1024
    );
    assert_eq!(
        store
            .put_file(
                "committed-multipart/file",
                &input,
                WriteCondition::CreateOnly
            )
            .await
            .expect("a retried multipart create reports the condition"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(emulator.unfinished_upload_count(), 0);
    emulator.shutdown().await;
}

/// Answers every request with HTTP 409 `FileAlreadyExists` and counts the requests.
async fn start_file_already_exists_server() -> (
    SocketAddr,
    Arc<std::sync::atomic::AtomicUsize>,
    tokio::task::JoinHandle<()>,
) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the fake server binds");
    let address = listener
        .local_addr()
        .expect("the fake server has an address");
    let requests = Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let counted_requests = Arc::clone(&requests);
    let accept_task = tokio::spawn(async move {
        loop {
            let (mut stream, _) = listener.accept().await.expect("the fake server accepts");
            let mut received = Vec::new();
            let mut buffer = [0_u8; 4096];
            let body_start = loop {
                let count = stream
                    .read(&mut buffer)
                    .await
                    .expect("the request is readable");
                assert!(count > 0, "the client closed before sending headers");
                received.extend_from_slice(&buffer[..count]);
                if let Some(end) = received.windows(4).position(|window| window == b"\r\n\r\n") {
                    break end + 4;
                }
            };
            let headers = String::from_utf8_lossy(&received[..body_start]).to_ascii_lowercase();
            let content_length: usize = headers
                .lines()
                .find_map(|line| line.strip_prefix("content-length:"))
                .map_or(0, |value| {
                    value.trim().parse().expect("the length is numeric")
                });
            while received.len() < body_start + content_length {
                let count = stream
                    .read(&mut buffer)
                    .await
                    .expect("the body is readable");
                assert!(count > 0, "the client closed before sending the body");
                received.extend_from_slice(&buffer[..count]);
            }
            counted_requests.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            let body = "<?xml version=\"1.0\" encoding=\"UTF-8\"?><Error><Code>FileAlreadyExists</Code><Message>The object you specified already exists and can not be overwritten.</Message><RequestId>fake</RequestId></Error>";
            let response = format!(
                "HTTP/1.1 409 Conflict\r\ncontent-type: application/xml\r\ncontent-length: {}\r\nconnection: close\r\n\r\n{body}",
                body.len()
            );
            stream
                .write_all(response.as_bytes())
                .await
                .expect("the response is written");
            stream.shutdown().await.expect("the connection closes");
        }
    });
    (address, requests, accept_task)
}

#[tokio::test]
async fn file_already_exists_maps_by_dialect() {
    use std::sync::atomic::Ordering;
    let (address, requests, accept_task) = start_file_already_exists_server().await;
    let store_for = |api_dialect| async move {
        let mut config = S3ObjectStoreConfig::new("us-east-1", BUCKET);
        config.endpoint = Some(format!("http://{address}"));
        config.force_path_style = true;
        config.credentials = S3Credentials::Static {
            access_key_id: "s3local".to_owned(),
            secret_access_key: "s3local".to_owned(),
            session_token: None,
        };
        config.api_dialect = api_dialect;
        S3ObjectStore::new(config)
            .await
            .expect("the store is configured")
    };

    // Amazon S3 never sends this code; any 409 stays a retried conflict, as before the dialect.
    let amazon_s3 = store_for(dexzd_object_store::S3ApiDialect::AmazonS3).await;
    let conflict = amazon_s3
        .put(
            "key",
            Bytes::from_static(b"body"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect_err("a 409 is a conflict under Amazon S3");
    assert_eq!(conflict.kind, ObjectStoreErrorKind::Transient, "{conflict}");
    assert!(
        conflict
            .detail
            .starts_with("HTTP 409 ConditionalRequestConflict:"),
        "{conflict}"
    );
    assert_eq!(requests.swap(0, Ordering::SeqCst), 3);

    let aliyun_oss = store_for(dexzd_object_store::S3ApiDialect::AliyunOss).await;
    assert_eq!(
        aliyun_oss
            .put(
                "key",
                Bytes::from_static(b"body"),
                WriteCondition::CreateOnly
            )
            .await
            .expect("a forbid-overwrite rejection is a failed condition"),
        WriteOutcome::PreconditionFailed
    );
    assert_eq!(requests.swap(0, Ordering::SeqCst), 1);
    let unexpected = aliyun_oss
        .put(
            "key",
            Bytes::from_static(b"body"),
            WriteCondition::Unconditional,
        )
        .await
        .expect_err("an unexpected FileAlreadyExists fails");
    assert_eq!(
        unexpected.kind,
        ObjectStoreErrorKind::Permanent,
        "{unexpected}"
    );
    assert!(
        unexpected.detail.starts_with("HTTP 409 FileAlreadyExists:"),
        "{unexpected}"
    );
    assert_eq!(requests.swap(0, Ordering::SeqCst), 1);
    accept_task.abort();
}

#[tokio::test]
async fn every_layer_of_an_aliyun_oss_stack_reports_no_atomic_replacement() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let inner = Arc::new(emulator.object_store().await);
    assert!(!inner.supports_replace_if_etag_matches_on_every_key());
    let adapter = Arc::new(dexzd_object_store::AliyunOssObjectStore::new(inner));
    let instrumented = dexzd_object_store::InstrumentedObjectStore::new(
        adapter,
        dexzd_metrics::NodeMetrics::unregistered(),
    );
    assert!(!instrumented.supports_replace_if_etag_matches_on_every_key());
    emulator.shutdown().await;
}

#[tokio::test]
async fn the_create_only_check_passes_and_leaves_no_probe_object() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let store = emulator.object_store().await;
    dexzd_object_store::check_aliyun_oss_create_only_writes(&store)
        .await
        .expect("an unversioned bucket honors x-oss-forbid-overwrite");
    let listing = store.list("", None, 1000).await.expect("list");
    assert!(listing.objects.is_empty(), "{:?}", listing.objects);
    emulator.shutdown().await;
}

#[tokio::test]
async fn the_create_only_check_rejects_a_bucket_that_ever_had_versioning() {
    for versioning in [
        s3local::VersioningState::Enabled,
        s3local::VersioningState::Suspended,
    ] {
        let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
        emulator
            .store
            .put_bucket_versioning(BUCKET, versioning)
            .expect("the bucket versioning changes");
        let store = emulator.object_store().await;
        let error = dexzd_object_store::check_aliyun_oss_create_only_writes(&store)
            .await
            .expect_err("a versioned bucket ignores x-oss-forbid-overwrite");
        assert!(
            matches!(
                error,
                dexzd_object_store::AliyunOssBucketCheckError::CreateOnlyIgnored { .. }
            ),
            "{versioning:?}: {error:?}"
        );
        assert!(error.to_string().contains("versioning"), "{error}");
        emulator.shutdown().await;
    }
}

#[tokio::test]
async fn the_provider_check_rejects_metadata_written_in_place() {
    let emulator = AliyunOssEmulator::start(EtagMode::Md5).await;
    let store = emulator.object_store().await;
    dexzd_object_store::check_no_data_of_another_provider(&store)
        .await
        .expect("an empty prefix holds no other provider's data");
    for key in [
        dexzd_object_store::MANIFEST_OBJECT_KEY,
        dexzd_object_store::ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY,
    ] {
        store
            .put(key, Bytes::from_static(b"s3"), WriteCondition::CreateOnly)
            .await
            .expect("the other provider's object is written");
        let error = dexzd_object_store::check_no_data_of_another_provider(&store)
            .await
            .expect_err("an object at a mutable key came from another provider");
        assert!(
            matches!(
                &error,
                dexzd_object_store::AliyunOssBucketCheckError::WrittenByAnotherProvider {
                    key: found,
                } if found == key
            ),
            "{error:?}"
        );
        store.delete(key).await.expect("the object is deleted");
    }
    emulator.shutdown().await;
}
