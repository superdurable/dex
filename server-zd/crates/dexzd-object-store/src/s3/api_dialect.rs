// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use aws_sdk_s3::config::RequestChecksumCalculation;
use aws_sdk_s3::config::http::{HttpRequest, HttpResponse};
use aws_sdk_s3::error::{ProvideErrorMetadata, SdkError};
use aws_sdk_s3::operation::upload_part::builders::UploadPartFluentBuilder;
use aws_sdk_s3::operation::upload_part::{UploadPartError, UploadPartOutput};
use base64::Engine;
use base64::engine::general_purpose::STANDARD as BASE64;
use md5::{Digest, Md5};

use crate::s3_error::{is_precondition_error, map_sdk_error};
use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, WriteCondition};

/// Header that makes an Aliyun OSS write fail when the key already exists.
const FORBID_OVERWRITE_HEADER: &str = "x-oss-forbid-overwrite";
const CONTENT_MD5_HEADER: &str = "content-md5";

/// Request dialect of the S3-compatible endpoint behind an [`S3ObjectStore`](super::S3ObjectStore).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
#[non_exhaustive]
pub enum S3ApiDialect {
    /// Amazon S3 and endpoints with its `If-None-Match` and `If-Match` conditional writes.
    #[default]
    AmazonS3,
    /// The S3-compatible endpoint of Aliyun OSS (Alibaba Cloud Object Storage Service).
    AliyunOss,
    /// The S3-compatible endpoint of Tencent COS (Tencent Cloud Object Storage), whose create-only
    /// writes use AppendObject at position 0.
    TencentCos,
}

impl S3ApiDialect {
    /// Returns true when `error` reports that `condition` was false and nothing was written.
    pub(super) fn is_precondition_failure<E: ProvideErrorMetadata>(
        self,
        error: &SdkError<E>,
        condition: &WriteCondition,
    ) -> bool {
        match (self, condition) {
            (_, WriteCondition::Unconditional) => false,
            (Self::AmazonS3, WriteCondition::CreateOnly) => is_precondition_error(error, false),
            (Self::AmazonS3, WriteCondition::ReplaceIfEtagMatches { .. }) => {
                is_precondition_error(error, true)
            }
            (Self::AliyunOss, WriteCondition::CreateOnly) => is_file_already_exists(error),
            (Self::AliyunOss, WriteCondition::ReplaceIfEtagMatches { .. }) => false,
            // Tencent COS conditional writes never use PutObject or CompleteMultipartUpload.
            (Self::TencentCos, _) => false,
        }
    }

    /// Returns whether uploads and DeleteObjects must carry `Content-MD5` instead of CRC32.
    pub(super) fn requires_content_md5(self) -> bool {
        match self {
            Self::AmazonS3 => false,
            Self::AliyunOss | Self::TencentCos => true,
        }
    }

    /// Maps a failed PutObject or CompleteMultipartUpload that was not a failed condition.
    pub(super) fn map_write_error<E>(self, key: &str, error: SdkError<E>) -> ObjectStoreError
    where
        E: ProvideErrorMetadata + std::fmt::Display,
    {
        // An unexpected Aliyun OSS forbid-overwrite rejection must never retry as a conflict.
        if self == Self::AliyunOss && is_file_already_exists(&error) {
            return ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::Permanent,
                format!("HTTP 409 FileAlreadyExists: {error}"),
            );
        }
        map_sdk_error(ObjectStoreOperation::Put, key, true, error)
    }
}

/// Only the forbid-overwrite rejection counts; any other 409 stays a retryable conflict.
fn is_file_already_exists<E: ProvideErrorMetadata>(error: &SdkError<E>) -> bool {
    let status = error
        .raw_response()
        .map(|response| response.status().as_u16());
    status == Some(409)
        && error
            .as_service_error()
            .and_then(ProvideErrorMetadata::code)
            == Some("FileAlreadyExists")
}

pub(super) fn atomic_replacement_unsupported(key: &str) -> ObjectStoreError {
    ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        ObjectStoreErrorKind::Permanent,
        "the Aliyun OSS dialect has no atomic replacement, so ReplaceIfEtagMatches is unsupported",
    )
}

/// MD5 digest of an upload body, sent as `Content-MD5` and compared with part ETags.
pub(super) struct BodyMd5 {
    digest: [u8; 16],
}

impl BodyMd5 {
    pub(super) fn of(bytes: &[u8]) -> Self {
        Self {
            digest: Md5::digest(bytes).into(),
        }
    }

    pub(super) fn content_md5(&self) -> String {
        BASE64.encode(self.digest)
    }

    fn hex(&self) -> String {
        self.digest
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    }

    /// Fails unless the part ETag is this digest in hex, ignoring quotes and case.
    pub(super) fn verify_part_etag(
        &self,
        key: &str,
        part_number: i32,
        part_etag: &str,
    ) -> Result<(), ObjectStoreError> {
        let expected = self.hex();
        if part_etag.trim_matches('"').eq_ignore_ascii_case(&expected) {
            return Ok(());
        }
        Err(ObjectStoreError::new(
            ObjectStoreOperation::Put,
            key,
            ObjectStoreErrorKind::Corrupt,
            format!("part {part_number} ETag {part_etag} differs from its Content-MD5 {expected}"),
        ))
    }
}

/// Uploads a part with `Content-MD5` as its only checksum, so the MD5 check alone guards it.
pub(super) async fn send_upload_part_with_content_md5(
    request: UploadPartFluentBuilder,
    part_md5: &BodyMd5,
) -> Result<UploadPartOutput, SdkError<UploadPartError, HttpResponse>> {
    request
        .content_md5(part_md5.content_md5())
        .customize()
        .config_override(
            aws_sdk_s3::config::Builder::default()
                .request_checksum_calculation(RequestChecksumCalculation::WhenRequired),
        )
        .send()
        .await
}

/// Marks a create-only write; runs before signing, so the header is signed.
pub(super) fn insert_forbid_overwrite_header(request: &mut HttpRequest) {
    if let Some(previous) = request
        .headers_mut()
        .insert(FORBID_OVERWRITE_HEADER, "true")
    {
        tracing::debug!(
            previous,
            "replaced an existing x-oss-forbid-overwrite header"
        );
    }
}

/// Adds `Content-MD5` of the serialized body; a streaming body gets none and OSS rejects it.
pub(super) fn insert_body_content_md5(request: &mut HttpRequest) {
    let Some(content_md5) = request
        .body()
        .bytes()
        .map(|body| BodyMd5::of(body).content_md5())
    else {
        tracing::warn!("request body is streamed, so Content-MD5 cannot be computed");
        return;
    };
    if let Some(previous) = request
        .headers_mut()
        .insert(CONTENT_MD5_HEADER, content_md5)
    {
        tracing::debug!(previous, "replaced an existing Content-MD5 header");
    }
}
