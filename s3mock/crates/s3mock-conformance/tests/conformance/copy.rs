// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CopyObject: copied bytes and metadata, versions, conditions, errors, and refused features.

use std::collections::HashMap;
use std::time::{Duration, SystemTime};

use aws_sdk_s3::primitives::DateTime;
use aws_sdk_s3::types::{
    BucketVersioningStatus, ChecksumAlgorithm, ChecksumMode, ChecksumType,
    CompletedMultipartUpload, CompletedPart, MetadataDirective, ServerSideEncryption, StorageClass,
};

use crate::support::{ConformanceTarget, error_code_and_status, read_body};
use crate::versioning::set_versioning;

#[tokio::test]
async fn copy_keeps_bytes_content_type_metadata_and_checksum() {
    let mut target = ConformanceTarget::connect_presigned().await;
    let bucket = target.create_bucket().await;
    set_versioning(&target, &bucket, BucketVersioningStatus::Enabled).await;
    let source = target
        .client
        .put_object()
        .bucket(&bucket)
        .key("source")
        .content_type("text/plain")
        .metadata("sha256", "abc")
        .checksum_algorithm(ChecksumAlgorithm::Sha256)
        .body(b"copy me".to_vec().into())
        .send()
        .await
        .expect("the source object is stored");
    // COPY is the default directive and ignores metadata sent with the request: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html
    let copied = target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("copy")
        .copy_source(copy_source(&bucket, "source", None))
        .metadata("ignored", "yes")
        .send()
        .await
        .expect("the object is copied");
    let result = copied
        .copy_object_result()
        .expect("CopyObject returns a CopyObjectResult");
    assert_eq!(result.e_tag(), source.e_tag());
    assert!(result.last_modified().is_some());
    assert_eq!(result.checksum_type(), Some(&ChecksumType::FullObject));
    assert_eq!(result.checksum_sha256(), source.checksum_sha256());
    assert_eq!(copied.copy_source_version_id(), source.version_id());
    let copy_version = copied
        .version_id()
        .expect("a copy into a versioned bucket returns a version ID");
    assert_ne!(Some(copy_version), source.version_id());
    let head = target
        .client
        .head_object()
        .bucket(&bucket)
        .key("copy")
        .checksum_mode(ChecksumMode::Enabled)
        .send()
        .await
        .expect("the copy exists");
    assert_eq!(head.content_length(), Some(7));
    assert_eq!(head.content_type(), Some("text/plain"));
    assert_eq!(head.metadata(), Some(&metadata(&[("sha256", "abc")])));
    assert_eq!(head.checksum_sha256(), source.checksum_sha256());
    assert_eq!(head.e_tag(), source.e_tag());
    assert_eq!(head.version_id(), Some(copy_version));
    let body = target
        .client
        .get_object()
        .bucket(&bucket)
        .key("copy")
        .send()
        .await
        .expect("the copy is readable");
    assert_eq!(read_body(body).await, b"copy me");
    target.cleanup().await;
}

#[tokio::test]
async fn replace_directive_takes_content_headers_and_metadata_from_the_request() {
    let mut target = ConformanceTarget::connect().await;
    let bucket = target.create_bucket().await;
    target
        .client
        .put_object()
        .bucket(&bucket)
        .key("source")
        .content_type("text/plain")
        .cache_control("no-cache")
        .metadata("origin", "source")
        .body(b"{}".to_vec().into())
        .send()
        .await
        .expect("the source object is stored");
    target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("replaced")
        .copy_source(copy_source(&bucket, "source", None))
        .metadata_directive(MetadataDirective::Replace)
        .content_type("application/json")
        .metadata("origin", "request")
        .send()
        .await
        .expect("the object is copied with replaced metadata");
    let head = target
        .client
        .head_object()
        .bucket(&bucket)
        .key("replaced")
        .send()
        .await
        .expect("the copy exists");
    assert_eq!(head.content_type(), Some("application/json"));
    assert_eq!(head.cache_control(), None);
    assert_eq!(head.metadata(), Some(&metadata(&[("origin", "request")])));
    assert_eq!(head.content_length(), Some(2));
    target.cleanup().await;
}

#[tokio::test]
async fn copy_reads_a_noncurrent_version_by_version_id() {
    let mut target = ConformanceTarget::connect().await;
    let bucket = target.create_bucket().await;
    set_versioning(&target, &bucket, BucketVersioningStatus::Enabled).await;
    let first = put(&target, &bucket, "versioned", b"one").await;
    put(&target, &bucket, "versioned", b"two").await;
    let copied = target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("restored")
        .copy_source(copy_source(&bucket, "versioned", Some(&first)))
        .send()
        .await
        .expect("a noncurrent version is copied");
    assert_eq!(copied.copy_source_version_id(), Some(first.as_str()));
    assert_eq!(get_body(&target, &bucket, "restored").await, b"one");
    target.cleanup().await;
}

#[tokio::test]
async fn copy_across_buckets_follows_each_bucket_versioning() {
    let mut target = ConformanceTarget::connect().await;
    let versioned = target.create_bucket().await;
    let unversioned = target.create_bucket().await;
    set_versioning(&target, &versioned, BucketVersioningStatus::Enabled).await;
    let source_version = put(&target, &versioned, "dir/source", b"across").await;
    let into_unversioned = target
        .client
        .copy_object()
        .bucket(&unversioned)
        .key("copy")
        .copy_source(copy_source(&versioned, "dir/source", None))
        .send()
        .await
        .expect("a cross-bucket copy succeeds");
    assert_eq!(into_unversioned.version_id(), None);
    assert_eq!(
        into_unversioned.copy_source_version_id(),
        Some(source_version.as_str())
    );
    let into_versioned = target
        .client
        .copy_object()
        .bucket(&versioned)
        .key("back")
        .copy_source(copy_source(&unversioned, "copy", None))
        .send()
        .await
        .expect("a copy from an unversioned bucket succeeds");
    assert!(into_versioned.version_id().is_some());
    assert_eq!(into_versioned.copy_source_version_id(), None);
    assert_eq!(get_body(&target, &versioned, "back").await, b"across");
    target.cleanup().await;
}

#[tokio::test]
async fn copy_source_keys_are_url_decoded() {
    let mut target = ConformanceTarget::connect().await;
    let bucket = target.create_bucket().await;
    let key = "dir/a b?c+d%e\u{fc}";
    put(&target, &bucket, key, b"encoded").await;
    target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("decoded")
        .copy_source(copy_source(&bucket, key, None))
        .send()
        .await
        .expect("an encoded copy source is decoded");
    target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("leading-slash")
        .copy_source(format!("/{}", copy_source(&bucket, key, None)))
        .send()
        .await
        .expect("a copy source may start with a slash");
    assert_eq!(get_body(&target, &bucket, "decoded").await, b"encoded");
    assert_eq!(
        get_body(&target, &bucket, "leading-slash").await,
        b"encoded"
    );
    target.cleanup().await;
}

#[tokio::test]
async fn missing_or_invalid_sources_are_rejected() {
    let mut target = ConformanceTarget::connect().await;
    let bucket = target.create_bucket().await;
    set_versioning(&target, &bucket, BucketVersioningStatus::Enabled).await;
    let marker = target
        .client
        .delete_object()
        .bucket(&bucket)
        .key("deleted")
        .send()
        .await
        .expect("deleting creates a delete marker")
        .version_id()
        .expect("delete markers have version IDs")
        .to_owned();
    put(&target, &bucket, "present", b"present").await;
    let cases = [
        (copy_source(&bucket, "missing", None), "NoSuchKey", 404),
        (
            copy_source(&format!("{bucket}-missing"), "missing", None),
            "NoSuchBucket",
            404,
        ),
        (copy_source(&bucket, "deleted", None), "NoSuchKey", 404),
        // S3 refuses a copy source that names a delete marker: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html
        (
            copy_source(&bucket, "deleted", Some(&marker)),
            "InvalidRequest",
            400,
        ),
        (
            copy_source(&bucket, "present", Some("not-a-version")),
            "InvalidRequest",
            400,
        ),
        (bucket.clone(), "InvalidArgument", 400),
    ];
    for (source, code, status) in cases {
        let error = target
            .client
            .copy_object()
            .bucket(&bucket)
            .key("destination")
            .copy_source(&source)
            .send()
            .await
            .expect_err("the copy source is rejected");
        assert_eq!(
            error_code_and_status(&error),
            (Some(code.to_owned()), Some(status)),
            "copy source {source}"
        );
    }
    assert_missing(&target, &bucket, "destination").await;
    target.cleanup().await;
}

#[tokio::test]
async fn self_copy_needs_a_version_id_or_replaced_metadata() {
    let mut target = ConformanceTarget::connect().await;
    let bucket = target.create_bucket().await;
    set_versioning(&target, &bucket, BucketVersioningStatus::Enabled).await;
    let first = put(&target, &bucket, "self", b"one").await;
    let current = put(&target, &bucket, "self", b"two").await;
    for request in [
        target.client.copy_object(),
        target
            .client
            .copy_object()
            .checksum_algorithm(ChecksumAlgorithm::Sha256),
    ] {
        let error = request
            .bucket(&bucket)
            .key("self")
            .copy_source(copy_source(&bucket, "self", None))
            .send()
            .await
            .expect_err("copying an object onto itself without a change is refused");
        assert_eq!(
            error_code_and_status(&error),
            (Some("InvalidRequest".to_owned()), Some(400))
        );
    }
    for version_id in [&current, &first] {
        target
            .client
            .copy_object()
            .bucket(&bucket)
            .key("self")
            .copy_source(copy_source(&bucket, "self", Some(version_id)))
            .send()
            .await
            .expect("a version-addressed self-copy succeeds");
    }
    assert_eq!(get_body(&target, &bucket, "self").await, b"one");
    target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("self")
        .copy_source(copy_source(&bucket, "self", None))
        .metadata_directive(MetadataDirective::Replace)
        .metadata("changed", "yes")
        .send()
        .await
        .expect("a self-copy that replaces metadata succeeds");
    let head = target
        .client
        .head_object()
        .bucket(&bucket)
        .key("self")
        .send()
        .await
        .expect("the object exists");
    assert_eq!(head.metadata(), Some(&metadata(&[("changed", "yes")])));
    target.cleanup().await;
}

#[tokio::test]
async fn copy_conditions_check_the_source_and_the_destination() {
    let mut target = ConformanceTarget::connect().await;
    let bucket = target.create_bucket().await;
    let etag = target
        .client
        .put_object()
        .bucket(&bucket)
        .key("source")
        .body(b"conditional".to_vec().into())
        .send()
        .await
        .expect("the source object is stored")
        .e_tag()
        .expect("PutObject returns an ETag")
        .to_owned();
    let past = DateTime::from(SystemTime::now() - Duration::from_secs(86_400));
    let future = DateTime::from(SystemTime::now() + Duration::from_secs(86_400));
    let other_etag = "\"0123456789abcdef0123456789abcdef\"";
    let client = &target.client;
    // Conditional copy semantics: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html#API_CopyObject_RequestSyntax
    let cases = [
        (
            "if-match mismatch",
            client.copy_object().copy_source_if_match(other_etag),
            false,
        ),
        (
            "if-match",
            client.copy_object().copy_source_if_match(&etag),
            true,
        ),
        (
            "if-none-match match",
            client.copy_object().copy_source_if_none_match(&etag),
            false,
        ),
        (
            "if-none-match",
            client.copy_object().copy_source_if_none_match(other_etag),
            true,
        ),
        (
            "if-modified-since past",
            client.copy_object().copy_source_if_modified_since(past),
            true,
        ),
        // A date in the future is not a valid condition, so it is ignored.
        (
            "if-modified-since future",
            client.copy_object().copy_source_if_modified_since(future),
            true,
        ),
        (
            "if-unmodified-since past",
            client.copy_object().copy_source_if_unmodified_since(past),
            false,
        ),
        (
            "if-unmodified-since future",
            client.copy_object().copy_source_if_unmodified_since(future),
            true,
        ),
        (
            "if-match overrides if-unmodified-since",
            client
                .copy_object()
                .copy_source_if_match(&etag)
                .copy_source_if_unmodified_since(past),
            true,
        ),
    ];
    for (name, request, is_copied) in cases {
        let result = request
            .bucket(&bucket)
            .key("conditional")
            .copy_source(copy_source(&bucket, "source", None))
            .send()
            .await;
        match result {
            Ok(_) => assert!(is_copied, "{name} copies"),
            Err(error) => {
                assert!(!is_copied, "{name} fails: {error:?}");
                assert_eq!(
                    error_code_and_status(&error),
                    (Some("PreconditionFailed".to_owned()), Some(412)),
                    "{name}"
                );
            }
        }
    }
    let destination_cases = [
        (
            "If-None-Match * on an existing key",
            "conditional",
            client.copy_object().if_none_match("*"),
            Some(("PreconditionFailed", 412)),
        ),
        (
            "If-None-Match * on a new key",
            "fresh",
            client.copy_object().if_none_match("*"),
            None,
        ),
        (
            "If-Match mismatch",
            "conditional",
            client.copy_object().if_match(other_etag),
            Some(("PreconditionFailed", 412)),
        ),
        (
            "If-Match on a missing key",
            "absent",
            client.copy_object().if_match(other_etag),
            Some(("NoSuchKey", 404)),
        ),
        (
            "If-Match",
            "conditional",
            client.copy_object().if_match(&etag),
            None,
        ),
    ];
    for (name, key, request, expected_error) in destination_cases {
        let result = request
            .bucket(&bucket)
            .key(key)
            .copy_source(copy_source(&bucket, "source", None))
            .send()
            .await;
        match (result, expected_error) {
            (Ok(_), None) => {}
            (Err(error), Some((code, status))) => assert_eq!(
                error_code_and_status(&error),
                (Some(code.to_owned()), Some(status)),
                "{name}"
            ),
            (result, expected_error) => {
                panic!("{name}: expected {expected_error:?}, got {result:?}")
            }
        }
    }
    target.cleanup().await;
}

#[tokio::test]
async fn copy_of_a_multipart_object_is_a_single_part_object() {
    let mut target = ConformanceTarget::connect().await;
    let bucket = target.create_bucket().await;
    let body = b"multipart body".to_vec();
    let part_checksum = s3mock::ChecksumAlgorithm::Crc32.base64_checksum(&body);
    let created = target
        .client
        .create_multipart_upload()
        .bucket(&bucket)
        .key("multipart")
        .checksum_algorithm(ChecksumAlgorithm::Crc32)
        .checksum_type(ChecksumType::Composite)
        .send()
        .await
        .expect("the upload starts");
    let upload_id = created.upload_id().expect("the upload has an ID");
    let part = target
        .client
        .upload_part()
        .bucket(&bucket)
        .key("multipart")
        .upload_id(upload_id)
        .part_number(1)
        .checksum_algorithm(ChecksumAlgorithm::Crc32)
        .checksum_crc32(&part_checksum)
        .body(body.clone().into())
        .send()
        .await
        .expect("the part uploads");
    target
        .client
        .complete_multipart_upload()
        .bucket(&bucket)
        .key("multipart")
        .upload_id(upload_id)
        .checksum_type(ChecksumType::Composite)
        .multipart_upload(
            CompletedMultipartUpload::builder()
                .parts(
                    CompletedPart::builder()
                        .part_number(1)
                        .e_tag(part.e_tag().expect("parts have ETags"))
                        .checksum_crc32(&part_checksum)
                        .build(),
                )
                .build(),
        )
        .send()
        .await
        .expect("the upload completes");
    let copied = target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("copy")
        .copy_source(copy_source(&bucket, "multipart", None))
        .send()
        .await
        .expect("a multipart object is copied");
    let result = copied
        .copy_object_result()
        .expect("CopyObject returns a CopyObjectResult");
    let single_part_etag = format!("\"{}\"", crate::objects::hex_md5(&body));
    assert_eq!(result.e_tag(), Some(single_part_etag.as_str()));
    assert_eq!(result.checksum_type(), Some(&ChecksumType::FullObject));
    assert_eq!(result.checksum_crc32(), Some(part_checksum.as_str()));
    let with_sha256 = target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("copy-sha256")
        .copy_source(copy_source(&bucket, "multipart", None))
        .checksum_algorithm(ChecksumAlgorithm::Sha256)
        .send()
        .await
        .expect("a copy may choose another checksum algorithm");
    let sha256 = s3mock::ChecksumAlgorithm::Sha256.base64_checksum(&body);
    assert_eq!(
        with_sha256
            .copy_object_result()
            .and_then(|result| result.checksum_sha256()),
        Some(sha256.as_str())
    );
    let head = target
        .client
        .head_object()
        .bucket(&bucket)
        .key("copy-sha256")
        .checksum_mode(ChecksumMode::Enabled)
        .send()
        .await
        .expect("the copy exists");
    assert_eq!(head.checksum_sha256(), Some(sha256.as_str()));
    assert_eq!(head.checksum_crc32(), None);
    target.cleanup().await;
}

#[tokio::test]
async fn unsupported_copy_and_write_features_are_refused_without_writing() {
    let mut target = ConformanceTarget::connect().await;
    if !target.is_s3mock() {
        // S3 implements these features; the refusal is s3mock behavior.
        target.cleanup().await;
        return;
    }
    let bucket = target.create_bucket().await;
    put(&target, &bucket, "source", b"source").await;
    let upload_id = target
        .client
        .create_multipart_upload()
        .bucket(&bucket)
        .key("parts")
        .send()
        .await
        .expect("the upload starts")
        .upload_id()
        .expect("the upload has an ID")
        .to_owned();
    let part_copy = target
        .client
        .upload_part_copy()
        .bucket(&bucket)
        .key("parts")
        .upload_id(&upload_id)
        .part_number(1)
        .copy_source(copy_source(&bucket, "source", None))
        .send()
        .await
        .expect_err("UploadPartCopy is refused");
    assert_not_implemented(&part_copy);
    let range = target
        .client
        .copy_object()
        .bucket(&bucket)
        .key("ranged")
        .copy_source(copy_source(&bucket, "source", None))
        .customize()
        .mutate_request(|request| {
            request
                .headers_mut()
                .insert("x-amz-copy-source-range", "bytes=0-1");
        })
        .send()
        .await
        .expect_err("a copy-source range is refused");
    assert_not_implemented(&range);
    let copy_cases = [
        (
            "tagged-copy",
            target.client.copy_object().tagging("team=storage"),
        ),
        (
            "kms-copy",
            target
                .client
                .copy_object()
                .server_side_encryption(ServerSideEncryption::AwsKms),
        ),
        (
            "archived-copy",
            target
                .client
                .copy_object()
                .storage_class(StorageClass::Glacier),
        ),
    ];
    for (key, request) in copy_cases {
        let error = request
            .bucket(&bucket)
            .key(key)
            .copy_source(copy_source(&bucket, "source", None))
            .send()
            .await
            .expect_err("an unsupported copy header is refused");
        assert_not_implemented(&error);
        assert_missing(&target, &bucket, key).await;
    }
    let put_cases = [
        ("tagged", target.client.put_object().tagging("team=storage")),
        (
            "kms",
            target
                .client
                .put_object()
                .server_side_encryption(ServerSideEncryption::AwsKms),
        ),
        (
            "archived",
            target
                .client
                .put_object()
                .storage_class(StorageClass::DeepArchive),
        ),
        (
            "retained",
            target
                .client
                .put_object()
                .object_lock_mode(aws_sdk_s3::types::ObjectLockMode::Governance)
                .object_lock_retain_until_date(DateTime::from(
                    SystemTime::now() + Duration::from_secs(86_400),
                )),
        ),
        (
            "redirected",
            target
                .client
                .put_object()
                .website_redirect_location("/other"),
        ),
    ];
    for (key, request) in put_cases {
        let error = request
            .bucket(&bucket)
            .key(key)
            .body(b"refused".to_vec().into())
            .send()
            .await
            .expect_err("an unsupported write header is refused");
        assert_not_implemented(&error);
        assert_missing(&target, &bucket, key).await;
    }
    target
        .client
        .put_object()
        .bucket(&bucket)
        .key("defaults")
        .server_side_encryption(ServerSideEncryption::Aes256)
        .storage_class(StorageClass::Standard)
        .acl(aws_sdk_s3::types::ObjectCannedAcl::Private)
        .body(b"defaults".to_vec().into())
        .send()
        .await
        .expect("headers that match S3 defaults are accepted");
    target.cleanup().await;
}

/// Encodes a copy source; the SDK sends `x-amz-copy-source` without encoding it.
fn copy_source(bucket: &str, key: &str, version_id: Option<&str>) -> String {
    let encoded_key: String = key
        .bytes()
        .map(|byte| {
            if byte.is_ascii_alphanumeric() || b"-._~/".contains(&byte) {
                char::from(byte).to_string()
            } else {
                format!("%{byte:02X}")
            }
        })
        .collect();
    match version_id {
        Some(version_id) => format!("{bucket}/{encoded_key}?versionId={version_id}"),
        None => format!("{bucket}/{encoded_key}"),
    }
}

/// Stores `body` and returns the version ID, or `null` for an unversioned bucket.
async fn put(target: &ConformanceTarget, bucket: &str, key: &str, body: &[u8]) -> String {
    target
        .client
        .put_object()
        .bucket(bucket)
        .key(key)
        .body(body.to_vec().into())
        .send()
        .await
        .expect("the test object is stored")
        .version_id()
        .unwrap_or("null")
        .to_owned()
}

async fn get_body(target: &ConformanceTarget, bucket: &str, key: &str) -> Vec<u8> {
    read_body(
        target
            .client
            .get_object()
            .bucket(bucket)
            .key(key)
            .send()
            .await
            .expect("the object is readable"),
    )
    .await
}

async fn assert_missing(target: &ConformanceTarget, bucket: &str, key: &str) {
    let error = target
        .client
        .head_object()
        .bucket(bucket)
        .key(key)
        .send()
        .await
        .expect_err("the refused request wrote nothing");
    assert_eq!(error_code_and_status(&error).1, Some(404), "{key}");
}

fn assert_not_implemented<E, R>(error: &aws_sdk_s3::error::SdkError<E, R>)
where
    E: aws_sdk_s3::error::ProvideErrorMetadata,
    R: crate::support::HttpStatus,
{
    assert_eq!(
        error_code_and_status(error),
        (Some("NotImplemented".to_owned()), Some(501))
    );
}

fn metadata(pairs: &[(&str, &str)]) -> HashMap<String, String> {
    pairs
        .iter()
        .map(|(name, value)| ((*name).to_owned(), (*value).to_owned()))
        .collect()
}
