// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Configuration of an S3 object store and its request dialect.

use std::time::Duration;

#[cfg(doc)]
use super::MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES;
use super::{DEFAULT_PART_SIZE, S3ApiDialect, S3Credentials};
#[cfg(doc)]
use crate::{ObjectStore, WriteCondition, WriteOutcome};

/// Configuration of an S3 object store. Keys passed to the store are relative to `key_prefix`.
#[derive(Debug, Clone)]
pub struct S3ObjectStoreConfig {
    /// S3 endpoint override, or `None` for the region's AWS endpoint.
    /// Defaults to `None`; the server connects by HTTP(S), rather than binding or advertising this address.
    pub endpoint: Option<String>,
    /// AWS region used for endpoint selection and request signing.
    /// No default; the caller supplies the AWS region.
    pub region: String,
    /// Existing bucket holding all objects.
    /// No default; the caller supplies the bucket.
    pub bucket: String,
    /// Prefix prepended to every relative key. An absent trailing slash is added.
    /// Defaults to an empty prefix.
    pub key_prefix: String,
    /// Whether to put the bucket in the URL path.
    /// Defaults to false.
    pub force_path_style: bool,
    /// Credentials provider.
    /// Defaults to the AWS provider chain.
    pub credentials: S3Credentials,
    /// SDK operation timeout, including retries, and maximum wait between streamed body chunks.
    /// Defaults to 30 seconds.
    pub request_timeout: Duration,
    /// Bodies at least this size use multipart upload; files over 16 MiB also use multipart. Default 8 MiB.
    pub multipart_threshold: usize,
    /// Multipart part size; default 8 MiB and must be at least 5 MiB.
    pub multipart_part_size: usize,
    /// Request dialect of the endpoint. Defaults to [`S3ApiDialect::AmazonS3`].
    ///
    /// [`S3ApiDialect::AliyunOss`] targets the S3-compatible endpoint of Aliyun OSS, which rejects
    /// `If-None-Match` and `If-Match` on writes. In that dialect, [`WriteCondition::CreateOnly`]
    /// sends `x-oss-forbid-overwrite: true` on PutObject and CompleteMultipartUpload, and HTTP 409
    /// `FileAlreadyExists` reports [`WriteOutcome::PreconditionFailed`].
    /// [`WriteCondition::ReplaceIfEtagMatches`] fails with a permanent error before any request.
    /// Every PutObject, UploadPart, and DeleteObjects request carries `Content-MD5`, and each part
    /// ETag must equal the part's MD5. The bucket must never have had versioning enabled or
    /// suspended, because OSS then ignores `x-oss-forbid-overwrite`.
    /// [`ObjectStore::supports_replace_if_etag_matches_on_every_key`] returns false in that
    /// dialect; wrap the store in [`crate::AliyunOssObjectStore`] to replace mutable object keys.
    ///
    /// [`S3ApiDialect::TencentCos`] needs an endpoint. Its [`WriteCondition::CreateOnly`] is one
    /// AppendObject at position 0 of 1 byte to [`MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES`];
    /// HTTP 409 `AppendPositionErr` or `ObjectNotAppendable` reports a failed condition. An object
    /// of 0 bytes reads as absent in GET, HEAD, and listings, because Tencent COS shows one while
    /// an append to a missing key is in flight and keeps it after an interrupted append. So every
    /// write of an empty body fails with a permanent error before any request. The rest works as
    /// in the Aliyun OSS dialect.
    pub api_dialect: S3ApiDialect,
}

impl S3ObjectStoreConfig {
    /// Creates configuration with 8 MiB multipart defaults and default-chain credentials.
    pub fn new(region: impl Into<String>, bucket: impl Into<String>) -> Self {
        Self {
            endpoint: None,
            region: region.into(),
            bucket: bucket.into(),
            key_prefix: String::new(),
            force_path_style: false,
            credentials: S3Credentials::DefaultChain,
            request_timeout: Duration::from_secs(30),
            multipart_threshold: DEFAULT_PART_SIZE,
            multipart_part_size: DEFAULT_PART_SIZE,
            api_dialect: S3ApiDialect::AmazonS3,
        }
    }
}
