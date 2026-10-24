// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The writes that produce a recorded store: every table and record shape gets at least one row.

use std::collections::BTreeMap;

use s3mock::{
    ChecksumAlgorithm, CompleteMultipartUploadRequest, CompletedPart, ContentHeaders,
    CorsConfiguration, CorsRule, CreateMultipartUploadRequest, CredentialRecord, CredentialStatus,
    DefaultRetention, DeleteObjectRequest, LifecycleConfiguration, LifecycleRule,
    ObjectLockConfiguration, PartRecord, PutObjectRequest, RetentionMode, Store,
    UploadChecksumExpectations, UploadRecord, VersioningState,
};
use serde::{Deserialize, Serialize};

pub(crate) const UNVERSIONED_BUCKET: &str = "compat-unversioned";
pub(crate) const VERSIONED_BUCKET: &str = "compat-versioned";
pub(crate) const SUSPENDED_BUCKET: &str = "compat-suspended";
pub(crate) const OBJECT_LOCK_BUCKET: &str = "compat-object-lock";
pub(crate) const CREDENTIAL_PARENT: &str = "compat-owner";
/// Versioned key with three versions under a delete marker.
pub(crate) const HISTORY_KEY: &str = "history.txt";
/// Checksummed upload with one part, which the compatibility test completes.
pub(crate) const PENDING_CHECKSUMMED_UPLOAD_KEY: &str = "multipart/pending-sha256.txt";
pub(crate) const PENDING_CHECKSUMMED_UPLOAD_BODY: &str = "pending sha256 part one";
/// Upload without a checksum algorithm and with two parts, left in progress.
pub(crate) const PENDING_PLAIN_UPLOAD_KEY: &str = "multipart/pending-plain.txt";
pub(crate) const ABORTED_UPLOAD_KEY: &str = "multipart/aborted.txt";

/// An upload that ListMultipartUploads no longer shows, identified for a repeated abort.
#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct UploadAddress {
    pub(crate) bucket: String,
    pub(crate) key: String,
    pub(crate) upload_id: String,
}

/// Writes the recorded workload into an empty store and returns the aborted uploads.
pub(crate) fn write_workload(store: &Store) -> Vec<UploadAddress> {
    let mut aborted_uploads = Vec::new();
    write_unversioned_bucket(store, &mut aborted_uploads);
    write_versioned_bucket(store);
    write_suspended_bucket(store);
    write_object_lock_bucket(store);
    write_credentials(store);
    aborted_uploads
}

fn write_unversioned_bucket(store: &Store, aborted_uploads: &mut Vec<UploadAddress>) {
    let bucket = UNVERSIONED_BUCKET;
    store
        .create_bucket(bucket, false)
        .expect("the bucket name is valid");
    put(
        store,
        bucket,
        "documents/readme.txt",
        "s3mock store format 1\n",
        ChecksumAlgorithm::Crc32,
        PutObjectRequest {
            content_headers: ContentHeaders {
                content_type: Some("text/plain; charset=utf-8".to_owned()),
                content_encoding: Some("identity".to_owned()),
                content_disposition: Some("attachment; filename=\"readme.txt\"".to_owned()),
                content_language: Some("en-US".to_owned()),
                cache_control: Some("max-age=60".to_owned()),
                expires: Some("Wed, 21 Oct 2026 07:28:00 GMT".to_owned()),
            },
            user_metadata: metadata(&[("owner", "compat"), ("purpose", "store-format")]),
            ..PutObjectRequest::default()
        },
    );
    for body in ["first body", "second body"] {
        put(
            store,
            bucket,
            "documents/overwritten.txt",
            body,
            ChecksumAlgorithm::Sha1,
            PutObjectRequest::default(),
        );
    }
    write_completed_upload(store, bucket);
    let checksummed = store
        .create_multipart_upload(
            bucket,
            PENDING_CHECKSUMMED_UPLOAD_KEY,
            CreateMultipartUploadRequest {
                content_headers: ContentHeaders {
                    content_type: Some("text/plain".to_owned()),
                    ..ContentHeaders::default()
                },
                user_metadata: metadata(&[("stage", "pending")]),
                checksum_algorithm: Some(ChecksumAlgorithm::Sha256),
                checksum_type: None,
            },
        )
        .expect("the upload is created");
    upload_part(
        store,
        bucket,
        &checksummed,
        1,
        PENDING_CHECKSUMMED_UPLOAD_BODY,
        ChecksumAlgorithm::Sha256,
    );
    let plain = store
        .create_multipart_upload(
            bucket,
            PENDING_PLAIN_UPLOAD_KEY,
            CreateMultipartUploadRequest::default(),
        )
        .expect("the upload is created");
    upload_part(
        store,
        bucket,
        &plain,
        1,
        "plain part one",
        ChecksumAlgorithm::Crc32,
    );
    upload_part(
        store,
        bucket,
        &plain,
        2,
        "plain part two",
        ChecksumAlgorithm::Crc64Nvme,
    );
    let aborted = store
        .create_multipart_upload(
            bucket,
            ABORTED_UPLOAD_KEY,
            CreateMultipartUploadRequest::default(),
        )
        .expect("the upload is created");
    upload_part(
        store,
        bucket,
        &aborted,
        1,
        "aborted part",
        ChecksumAlgorithm::Crc32,
    );
    store
        .abort_multipart_upload(bucket, &aborted.key, &aborted.upload_id)
        .expect("the upload exists");
    aborted_uploads.push(UploadAddress {
        bucket: bucket.to_owned(),
        key: aborted.key,
        upload_id: aborted.upload_id,
    });
    store
        .put_cors_configuration(
            bucket,
            Some(CorsConfiguration {
                rules: vec![
                    CorsRule {
                        id: Some("browser-uploads".to_owned()),
                        allowed_origins: vec!["https://app.example.com".to_owned()],
                        allowed_methods: vec![
                            "PUT".to_owned(),
                            "GET".to_owned(),
                            "HEAD".to_owned(),
                        ],
                        allowed_headers: vec!["content-type".to_owned(), "x-amz-meta-*".to_owned()],
                        expose_headers: vec!["ETag".to_owned()],
                        max_age_seconds: Some(600),
                    },
                    CorsRule {
                        id: None,
                        allowed_origins: vec!["*".to_owned()],
                        allowed_methods: vec!["GET".to_owned()],
                        allowed_headers: Vec::new(),
                        expose_headers: Vec::new(),
                        max_age_seconds: None,
                    },
                ],
            }),
        )
        .expect("the bucket exists");
    store
        .put_lifecycle_configuration(
            bucket,
            Some(LifecycleConfiguration {
                rules: vec![LifecycleRule {
                    id: Some("expire-temporary".to_owned()),
                    is_enabled: true,
                    prefix: "temporary/".to_owned(),
                    expiration_days: Some(30),
                    abort_incomplete_upload_days: Some(7),
                    ..LifecycleRule::default()
                }],
            }),
        )
        .expect("the bucket exists");
}

/// Completes a one-part CRC32C upload, so the object has a composite checksum and a part count.
fn write_completed_upload(store: &Store, bucket: &str) {
    let upload = store
        .create_multipart_upload(
            bucket,
            "multipart/assembled.txt",
            CreateMultipartUploadRequest {
                content_headers: ContentHeaders {
                    content_type: Some("text/plain".to_owned()),
                    ..ContentHeaders::default()
                },
                user_metadata: metadata(&[("assembled", "true")]),
                checksum_algorithm: Some(ChecksumAlgorithm::Crc32c),
                checksum_type: None,
            },
        )
        .expect("the upload is created");
    let part = upload_part(
        store,
        bucket,
        &upload,
        1,
        "assembled from one part",
        ChecksumAlgorithm::Crc32c,
    );
    store
        .complete_multipart_upload(
            bucket,
            &upload.key,
            &upload.upload_id,
            CompleteMultipartUploadRequest {
                parts: vec![CompletedPart {
                    part_number: part.part_number,
                    etag: part.etag,
                    checksum_base64: Some(part.checksum.base64_value),
                }],
                ..CompleteMultipartUploadRequest::default()
            },
        )
        .expect("the single part completes the upload");
}

fn write_versioned_bucket(store: &Store) {
    let bucket = VERSIONED_BUCKET;
    store
        .create_bucket(bucket, false)
        .expect("the bucket name is valid");
    store
        .put_bucket_versioning(bucket, VersioningState::Enabled)
        .expect("the bucket exists");
    for revision in ["1", "2", "3"] {
        put(
            store,
            bucket,
            HISTORY_KEY,
            &format!("history {revision}"),
            ChecksumAlgorithm::Crc64Nvme,
            PutObjectRequest {
                user_metadata: metadata(&[("revision", revision)]),
                ..PutObjectRequest::default()
            },
        );
    }
    store
        .delete_object(bucket, HISTORY_KEY, &DeleteObjectRequest::default())
        .expect("a delete marker is added");
    for body in ["kept 1", "kept 2"] {
        put(
            store,
            bucket,
            "kept.txt",
            body,
            ChecksumAlgorithm::Sha256,
            PutObjectRequest::default(),
        );
    }
    store
        .put_lifecycle_configuration(
            bucket,
            Some(LifecycleConfiguration {
                rules: vec![
                    LifecycleRule {
                        id: Some("noncurrent".to_owned()),
                        is_enabled: true,
                        prefix: String::new(),
                        noncurrent_expiration_days: Some(90),
                        newer_noncurrent_versions: Some(2),
                        ..LifecycleRule::default()
                    },
                    LifecycleRule {
                        id: None,
                        is_enabled: false,
                        prefix: "markers/".to_owned(),
                        expired_object_delete_marker: Some(true),
                        ..LifecycleRule::default()
                    },
                ],
            }),
        )
        .expect("the bucket exists");
}

/// Writes one versioned version, then a null version after suspending versioning.
fn write_suspended_bucket(store: &Store) {
    let bucket = SUSPENDED_BUCKET;
    store
        .create_bucket(bucket, false)
        .expect("the bucket name is valid");
    store
        .put_bucket_versioning(bucket, VersioningState::Enabled)
        .expect("the bucket exists");
    put(
        store,
        bucket,
        "mixed.txt",
        "versioned before suspension",
        ChecksumAlgorithm::Crc32,
        PutObjectRequest::default(),
    );
    store
        .put_bucket_versioning(bucket, VersioningState::Suspended)
        .expect("the bucket exists");
    put(
        store,
        bucket,
        "mixed.txt",
        "null after suspension",
        ChecksumAlgorithm::Crc32,
        PutObjectRequest::default(),
    );
}

fn write_object_lock_bucket(store: &Store) {
    let bucket = OBJECT_LOCK_BUCKET;
    store
        .create_bucket(bucket, true)
        .expect("the bucket name is valid");
    store
        .put_object_lock_configuration(
            bucket,
            ObjectLockConfiguration {
                default_retention: Some(DefaultRetention {
                    mode: RetentionMode::Governance,
                    days: Some(1),
                    years: None,
                }),
            },
        )
        .expect("the bucket has Object Lock enabled");
    put(
        store,
        bucket,
        "locked.txt",
        "retained",
        ChecksumAlgorithm::Crc32,
        PutObjectRequest::default(),
    );
}

fn write_credentials(store: &Store) {
    let credentials = [
        (
            "compat-reader",
            "compat reader secret",
            "reads documents",
            CredentialStatus::Enabled,
        ),
        (
            "compat-retired",
            "compat retired secret",
            "a disabled key",
            CredentialStatus::Disabled,
        ),
    ];
    for (access_key_id, secret, description, status) in credentials {
        store
            .create_credential(&CredentialRecord {
                access_key_id: access_key_id.to_owned(),
                secret_access_key: secret.to_owned(),
                parent: CREDENTIAL_PARENT.to_owned(),
                description: description.to_owned(),
                policy: serde_json::json!({
                    "Version": "2012-10-17",
                    "Statement": [
                        {
                            "Sid": "ReadDocuments",
                            "Effect": "Allow",
                            "Action": ["s3:GetObject"],
                            "Resource": [format!("arn:aws:s3:::{UNVERSIONED_BUCKET}/documents/*")]
                        },
                        {
                            "Effect": "Allow",
                            "Action": "s3:ListBucket",
                            "Resource": format!("arn:aws:s3:::{UNVERSIONED_BUCKET}"),
                            "Condition": {"StringLike": {"s3:prefix": "documents/*"}}
                        }
                    ]
                }),
                status,
            })
            .expect("the access key ID is new");
    }
}

fn put(
    store: &Store,
    bucket: &str,
    key: &str,
    body: &str,
    algorithm: ChecksumAlgorithm,
    request: PutObjectRequest,
) {
    let mut writer = store
        .begin_put(bucket, key, algorithm)
        .expect("the bucket exists");
    writer
        .write_all(body.as_bytes())
        .expect("the temporary file is writable");
    let staged = writer
        .finish(&UploadChecksumExpectations::default())
        .expect("no digests are declared");
    store
        .commit_put(bucket, key, staged, request)
        .expect("the unconditional write commits");
}

fn upload_part(
    store: &Store,
    bucket: &str,
    upload: &UploadRecord,
    part_number: u32,
    body: &str,
    algorithm: ChecksumAlgorithm,
) -> PartRecord {
    let mut writer = store
        .begin_upload_part(
            bucket,
            &upload.key,
            &upload.upload_id,
            part_number,
            algorithm,
        )
        .expect("the upload exists");
    writer
        .write_all(body.as_bytes())
        .expect("the temporary file is writable");
    let staged = writer
        .finish(&UploadChecksumExpectations::default())
        .expect("no digests are declared");
    store
        .commit_upload_part(bucket, &upload.key, &upload.upload_id, part_number, staged)
        .expect("the part commits")
}

fn metadata(entries: &[(&str, &str)]) -> BTreeMap<String, String> {
    entries
        .iter()
        .map(|(name, value)| ((*name).to_owned(), (*value).to_owned()))
        .collect()
}
