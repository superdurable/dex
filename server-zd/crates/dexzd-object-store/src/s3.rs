// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::Path;
use std::time::Duration;
use tokio::io::AsyncReadExt;

use async_trait::async_trait;
use aws_sdk_s3::Client;
use aws_sdk_s3::config::{
    BehaviorVersion, Credentials, Region, retry::RetryConfig, timeout::TimeoutConfig,
};
use aws_sdk_s3::operation::put_object::builders::PutObjectFluentBuilder;
use aws_sdk_s3::primitives::ByteStream;
use aws_sdk_s3::types::{
    ChecksumAlgorithm, CompletedMultipartUpload, CompletedPart, Delete, ObjectIdentifier,
};
use bytes::Bytes;

use crate::file_download::TemporaryDownloadFile;
use crate::s3_error::{is_conflict_error, map_sdk_error, required};
use crate::{
    ByteRange, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, WriteCondition, WriteOutcome,
};

mod api_dialect;
mod credentials;
mod file_read;
mod optional_read;
mod store_config;
mod tencent_cos;

pub use api_dialect::S3ApiDialect;
use api_dialect::{
    BodyMd5, atomic_replacement_unsupported, insert_body_content_md5,
    insert_forbid_overwrite_header, send_upload_part_with_content_md5,
};
pub use credentials::S3Credentials;
use file_read::read_single_put_file;
use optional_read::OptionalReadError;
pub use store_config::S3ObjectStoreConfig;
use tencent_cos::{AppendSource, TencentCosAppendClient};
pub use tencent_cos::{
    MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES, TENCENT_COS_STALE_LISTING_BOUND,
    TencentCosBucketCheckError, check_tencent_cos_bucket_versioning,
    check_tencent_cos_create_only_writes, check_tencent_cos_holds_no_data_of_another_provider,
};

const DEFAULT_PART_SIZE: usize = 8 * 1024 * 1024;
const MAX_SINGLE_PUT_FILE_BYTES: usize = 16 * 1024 * 1024;
const MAX_CONFLICT_RETRIES: u32 = 2;

/// S3 implementation of [`ObjectStore`] with conditional writes and multipart uploads.
pub struct S3ObjectStore {
    client: Client,
    read_client: Client,
    bucket: String,
    key_prefix: String,
    multipart_threshold: usize,
    multipart_part_size: usize,
    request_timeout: Duration,
    api_dialect: S3ApiDialect,
    /// Sends create-only writes in the Tencent COS dialect, and is `None` in the others.
    tencent_cos_append_client: Option<TencentCosAppendClient>,
    stale_listing_retry_window: Duration,
}

impl S3ObjectStore {
    /// Builds a client with the configured endpoint, credentials, timeout, and multipart policy.
    pub async fn new(config: S3ObjectStoreConfig) -> ObjectStoreResult<Self> {
        for (field, is_invalid) in [
            ("region", config.region.is_empty()),
            ("bucket", config.bucket.is_empty()),
            ("multipart_threshold", config.multipart_threshold == 0),
            (
                "multipart_part_size",
                config.multipart_part_size < 5 * 1024 * 1024,
            ),
            ("request_timeout", config.request_timeout.is_zero()),
        ] {
            if !is_invalid {
                continue;
            }
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Put,
                "",
                ObjectStoreErrorKind::Permanent,
                format!("invalid object-store config field: {field}"),
            ));
        }
        let mut loader = aws_config::defaults(BehaviorVersion::latest())
            .region(Region::new(config.region.clone()));
        if matches!(config.credentials, S3Credentials::Anonymous) {
            loader = loader.no_credentials();
        }
        let shared = loader.load().await;
        let mut builder = aws_sdk_s3::Config::from(&shared)
            .to_builder()
            .force_path_style(config.force_path_style)
            .timeout_config(
                TimeoutConfig::builder()
                    .connect_timeout(config.request_timeout)
                    .read_timeout(config.request_timeout)
                    .operation_timeout(config.request_timeout)
                    .build(),
            )
            .retry_config(RetryConfig::disabled());
        if let Some(endpoint) = &config.endpoint {
            builder = builder.endpoint_url(endpoint);
        }
        if let S3Credentials::Static {
            access_key_id,
            secret_access_key,
            session_token,
        } = &config.credentials
        {
            builder = builder.credentials_provider(Credentials::new(
                access_key_id.clone(),
                secret_access_key.clone(),
                session_token.clone(),
                None,
                "dexzd-object-store",
            ));
        }
        let write_config = builder.build();
        let read_config = write_config
            .to_builder()
            .retry_config(RetryConfig::standard().with_max_attempts(3))
            .build();
        let read_client = Client::from_conf(read_config);
        let tencent_cos_append_client = TencentCosAppendClient::for_dialect(&config, &shared)?;
        let store = Self {
            client: Client::from_conf(write_config),
            read_client,
            bucket: config.bucket,
            key_prefix: if config.key_prefix.is_empty() {
                String::new()
            } else {
                format!("{}/", config.key_prefix.trim_end_matches('/'))
            },
            multipart_threshold: config.multipart_threshold,
            multipart_part_size: config.multipart_part_size,
            request_timeout: config.request_timeout,
            api_dialect: config.api_dialect,
            tencent_cos_append_client,
            stale_listing_retry_window: config.stale_listing_retry_window,
        };
        if !store.key_prefix.is_empty() {
            store
                .read_client
                .list_objects_v2()
                .bucket(&store.bucket)
                .prefix(store.full_key("cluster"))
                .max_keys(1)
                .send()
                .await
                .map_err(|error| {
                    let is_denied = error
                        .raw_response()
                        .is_some_and(|response| response.status().as_u16() == 403);
                    let mapped = map_sdk_error(ObjectStoreOperation::List, "cluster", false, error);
                    if is_denied {
                        ObjectStoreError::new(
                            ObjectStoreOperation::List,
                            "cluster",
                            mapped.kind,
                            format!(
                                "startup requires s3:ListBucket with StringLike s3:prefix \"{}*\": {mapped}",
                                store.key_prefix
                            ),
                        )
                    } else {
                        mapped
                    }
                })?;
        }
        Ok(store)
    }

    fn full_key(&self, key: &str) -> String {
        format!("{}{}", self.key_prefix, key)
    }

    fn reject_unsupported_condition(
        &self,
        key: &str,
        condition: &WriteCondition,
    ) -> ObjectStoreResult<()> {
        match (self.api_dialect, condition) {
            (S3ApiDialect::AliyunOss, WriteCondition::ReplaceIfEtagMatches { .. }) => {
                Err(atomic_replacement_unsupported(key))
            }
            (S3ApiDialect::TencentCos, WriteCondition::ReplaceIfEtagMatches { .. }) => {
                Err(tencent_cos::atomic_replacement_unsupported(key))
            }
            _ => Ok(()),
        }
    }

    async fn put_single(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let content_md5 = self.content_md5(&body);
        let request = self
            .client
            .put_object()
            .bucket(&self.bucket)
            .key(self.full_key(key))
            .body(ByteStream::from(body))
            .set_content_md5(content_md5);
        self.send_put_object(key, request, condition).await
    }

    /// Returns the body's `Content-MD5` in dialects that require it, and `None` otherwise.
    fn content_md5(&self, body: &[u8]) -> Option<String> {
        (self.api_dialect.requires_content_md5()).then(|| BodyMd5::of(body).content_md5())
    }

    async fn send_put_object(
        &self,
        key: &str,
        request: PutObjectFluentBuilder,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let sent = match (self.api_dialect, &condition) {
            (_, WriteCondition::Unconditional) => request.send().await,
            (S3ApiDialect::AmazonS3, WriteCondition::CreateOnly) => {
                request.if_none_match("*").send().await
            }
            (S3ApiDialect::AmazonS3, WriteCondition::ReplaceIfEtagMatches { etag }) => {
                request.if_match(etag).send().await
            }
            (S3ApiDialect::AliyunOss, WriteCondition::CreateOnly) => {
                request
                    .customize()
                    .mutate_request(insert_forbid_overwrite_header)
                    .send()
                    .await
            }
            (S3ApiDialect::AliyunOss, WriteCondition::ReplaceIfEtagMatches { .. }) => {
                return Err(atomic_replacement_unsupported(key));
            }
            (S3ApiDialect::TencentCos, _) => {
                return Err(tencent_cos::conditional_put_object_unsupported(key));
            }
        };
        let response = match sent {
            Ok(response) => response,
            Err(error) if self.api_dialect.is_precondition_failure(&error, &condition) => {
                return Ok(WriteOutcome::PreconditionFailed);
            }
            Err(error) => return Err(self.api_dialect.map_write_error(key, error)),
        };
        Ok(WriteOutcome::Written {
            etag: required(response.e_tag(), ObjectStoreOperation::Put, key, "ETag")?.to_owned(),
        })
    }

    async fn put_multipart(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let full_key = self.full_key(key);
        let started = self
            .client
            .create_multipart_upload()
            .bucket(&self.bucket)
            .key(&full_key)
            .send()
            .await
            .map_err(|error| map_sdk_error(ObjectStoreOperation::Put, key, false, error))?;
        let upload_id = required(
            started.upload_id(),
            ObjectStoreOperation::Put,
            key,
            "upload ID",
        )?
        .to_owned();
        let result = self
            .upload_and_complete(key, &full_key, &upload_id, body, condition)
            .await;
        if (result.is_err() || matches!(result, Ok(WriteOutcome::PreconditionFailed)))
            && let Err(abort_error) = self
                .client
                .abort_multipart_upload()
                .bucket(&self.bucket)
                .key(&full_key)
                .upload_id(&upload_id)
                .send()
                .await
        {
            tracing::warn!(key, error = %abort_error, "failed to abort multipart upload");
        }
        result
    }

    async fn upload_and_complete(
        &self,
        key: &str,
        full_key: &str,
        upload_id: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let mut parts = Vec::new();
        for (index, chunk) in body.chunks(self.multipart_part_size).enumerate() {
            let part_number = i32::try_from(index + 1).map_err(|error| {
                ObjectStoreError::new(
                    ObjectStoreOperation::Put,
                    key,
                    ObjectStoreErrorKind::Permanent,
                    error.to_string(),
                )
            })?;
            let part_md5 = self.part_md5(chunk);
            let request = self
                .client
                .upload_part()
                .bucket(&self.bucket)
                .key(full_key)
                .upload_id(upload_id)
                .part_number(part_number)
                .body(ByteStream::from(Bytes::copy_from_slice(chunk)));
            let response = match &part_md5 {
                None => request.send().await,
                Some(part_md5) => send_upload_part_with_content_md5(request, part_md5).await,
            }
            .map_err(|error| map_sdk_error(ObjectStoreOperation::Put, key, false, error))?;
            let etag = required(
                response.e_tag(),
                ObjectStoreOperation::Put,
                key,
                "part ETag",
            )?;
            if let Some(part_md5) = &part_md5 {
                part_md5.verify_part_etag(key, part_number, etag)?;
            }
            parts.push(
                CompletedPart::builder()
                    .part_number(part_number)
                    .e_tag(etag)
                    .build(),
            );
        }
        self.complete_upload(key, full_key, upload_id, parts, condition)
            .await
    }

    /// Returns the part's MD5 in dialects that require `Content-MD5`, and `None` otherwise.
    fn part_md5(&self, part: &[u8]) -> Option<BodyMd5> {
        (self.api_dialect.requires_content_md5()).then(|| BodyMd5::of(part))
    }

    async fn put_multipart_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let full_key = self.full_key(key);
        // Aliyun OSS omits part CRC32 checksums, so a composite CRC32 cannot be completed.
        let checksum_algorithm =
            (self.api_dialect == S3ApiDialect::AmazonS3).then_some(ChecksumAlgorithm::Crc32);
        let started = self
            .client
            .create_multipart_upload()
            .bucket(&self.bucket)
            .key(&full_key)
            .set_checksum_algorithm(checksum_algorithm)
            .send()
            .await
            .map_err(|error| map_sdk_error(ObjectStoreOperation::Put, key, false, error))?;
        let upload_id = required(
            started.upload_id(),
            ObjectStoreOperation::Put,
            key,
            "upload ID",
        )?
        .to_owned();
        let result = self
            .upload_file_and_complete(key, &full_key, &upload_id, path, condition)
            .await;
        if (result.is_err() || matches!(result, Ok(WriteOutcome::PreconditionFailed)))
            && let Err(abort_error) = self
                .client
                .abort_multipart_upload()
                .bucket(&self.bucket)
                .key(&full_key)
                .upload_id(&upload_id)
                .send()
                .await
        {
            tracing::warn!(key, error = %abort_error, "failed to abort multipart upload");
        }
        result
    }

    async fn upload_file_and_complete(
        &self,
        key: &str,
        full_key: &str,
        upload_id: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let mut file = tokio::fs::File::open(path).await.map_err(|error| {
            ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::Permanent,
                format!("opening {}: {error}", path.display()),
            )
        })?;
        let mut parts = Vec::new();
        let mut part_number = 1;
        loop {
            let mut buffer = vec![0; self.multipart_part_size];
            let mut filled = 0;
            while filled < buffer.len() {
                let read = file.read(&mut buffer[filled..]).await.map_err(|error| {
                    ObjectStoreError::new(
                        ObjectStoreOperation::Put,
                        key,
                        ObjectStoreErrorKind::Permanent,
                        format!("reading {}: {error}", path.display()),
                    )
                })?;
                if read == 0 {
                    break;
                }
                filled += read;
            }
            if filled == 0 {
                break;
            }
            buffer.truncate(filled);
            let part_md5 = self.part_md5(&buffer);
            let request = self
                .client
                .upload_part()
                .bucket(&self.bucket)
                .key(full_key)
                .upload_id(upload_id)
                .part_number(part_number)
                .body(ByteStream::from(buffer));
            let response = match &part_md5 {
                None => {
                    request
                        .checksum_algorithm(ChecksumAlgorithm::Crc32)
                        .send()
                        .await
                }
                Some(part_md5) => send_upload_part_with_content_md5(request, part_md5).await,
            }
            .map_err(|error| map_sdk_error(ObjectStoreOperation::Put, key, false, error))?;
            let etag = required(
                response.e_tag(),
                ObjectStoreOperation::Put,
                key,
                "part ETag",
            )?;
            let part = CompletedPart::builder()
                .part_number(part_number)
                .e_tag(etag);
            let part = match &part_md5 {
                None => {
                    let checksum_crc32 = required(
                        response.checksum_crc32(),
                        ObjectStoreOperation::Put,
                        key,
                        "part CRC32 checksum",
                    )?;
                    part.checksum_crc32(checksum_crc32)
                }
                Some(part_md5) => {
                    part_md5.verify_part_etag(key, part_number, etag)?;
                    part
                }
            };
            parts.push(part.build());
            part_number += 1;
        }
        self.complete_upload(key, full_key, upload_id, parts, condition)
            .await
    }

    async fn complete_upload(
        &self,
        key: &str,
        full_key: &str,
        upload_id: &str,
        parts: Vec<CompletedPart>,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let upload = CompletedMultipartUpload::builder()
            .set_parts(Some(parts))
            .build();
        let request = self
            .client
            .complete_multipart_upload()
            .bucket(&self.bucket)
            .key(full_key)
            .upload_id(upload_id)
            .multipart_upload(upload);
        let sent = match (self.api_dialect, &condition) {
            (_, WriteCondition::Unconditional) => request.send().await,
            (S3ApiDialect::AmazonS3, WriteCondition::CreateOnly) => {
                request.if_none_match("*").send().await
            }
            (S3ApiDialect::AmazonS3, WriteCondition::ReplaceIfEtagMatches { etag }) => {
                request.if_match(etag).send().await
            }
            (S3ApiDialect::AliyunOss, WriteCondition::CreateOnly) => {
                request
                    .customize()
                    .mutate_request(insert_forbid_overwrite_header)
                    .send()
                    .await
            }
            (S3ApiDialect::AliyunOss, WriteCondition::ReplaceIfEtagMatches { .. }) => {
                return Err(atomic_replacement_unsupported(key));
            }
            (S3ApiDialect::TencentCos, _) => {
                return Err(tencent_cos::conditional_put_object_unsupported(key));
            }
        };
        let response = match sent {
            Ok(response) => response,
            Err(error) if self.api_dialect.is_precondition_failure(&error, &condition) => {
                return Ok(WriteOutcome::PreconditionFailed);
            }
            Err(error) => return Err(self.api_dialect.map_write_error(key, error)),
        };
        Ok(WriteOutcome::Written {
            etag: required(response.e_tag(), ObjectStoreOperation::Put, key, "ETag")?.to_owned(),
        })
    }

    async fn put_file_once(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let size = tokio::fs::metadata(path)
            .await
            .map_err(|error| {
                ObjectStoreError::new(
                    ObjectStoreOperation::Put,
                    key,
                    ObjectStoreErrorKind::Permanent,
                    format!("reading {}: {error}", path.display()),
                )
            })?
            .len();
        self.api_dialect.reject_empty_body(key, size)?;
        let single_put_cap = self.multipart_threshold.min(MAX_SINGLE_PUT_FILE_BYTES);
        if size >= self.multipart_threshold as u64 || size > single_put_cap as u64 {
            return self.put_multipart_file(key, path, condition).await;
        }
        // Anonymous HTTP uploads cannot supply the SDK's deferred streaming signer.
        let Some(body) =
            read_single_put_file(path, single_put_cap, self.request_timeout, key).await?
        else {
            return self.put_multipart_file(key, path, condition).await;
        };
        let content_md5 = self.content_md5(&body);
        let request = self
            .client
            .put_object()
            .bucket(&self.bucket)
            .key(self.full_key(key))
            .body(ByteStream::from(body))
            .checksum_algorithm(ChecksumAlgorithm::Crc32)
            .set_content_md5(content_md5);
        self.send_put_object(key, request, condition).await
    }

    /// Lists one ListObjectsV2 page; `next_start_after` names its last key while truncated.
    async fn list_page(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        let mut request = self
            .read_client
            .list_objects_v2()
            .bucket(&self.bucket)
            .prefix(self.full_key(prefix))
            .max_keys(max_keys as i32);
        if let Some(after) = start_after {
            request = request.start_after(self.full_key(after));
        }
        let response = request
            .send()
            .await
            .map_err(|error| map_sdk_error(ObjectStoreOperation::List, prefix, false, error))?;
        let objects = response
            .contents()
            .iter()
            .map(|object| {
                let full_key = required(
                    object.key(),
                    ObjectStoreOperation::List,
                    prefix,
                    "object key",
                )?;
                let key = full_key.strip_prefix(&self.key_prefix).ok_or_else(|| {
                    ObjectStoreError::new(
                        ObjectStoreOperation::List,
                        prefix,
                        ObjectStoreErrorKind::Permanent,
                        "listed key escaped configured prefix",
                    )
                })?;
                let size = u64::try_from(object.size().unwrap_or(-1)).map_err(|error| {
                    ObjectStoreError::new(
                        ObjectStoreOperation::List,
                        prefix,
                        ObjectStoreErrorKind::Permanent,
                        error.to_string(),
                    )
                })?;
                Ok(ObjectMetadata {
                    key: key.to_owned(),
                    size,
                    etag: required(object.e_tag(), ObjectStoreOperation::List, prefix, "ETag")?
                        .to_owned(),
                    last_modified_unix_micros: object.last_modified().map(|modified| {
                        modified
                            .secs()
                            .saturating_mul(1_000_000)
                            .saturating_add(i64::from(modified.subsec_nanos() / 1_000))
                    }),
                })
            })
            .collect::<ObjectStoreResult<Vec<_>>>()?;
        let next_start_after = if response.is_truncated().unwrap_or(false) {
            objects.last().map(|object| object.key.clone())
        } else {
            None
        };
        Ok(ObjectListing {
            objects,
            next_start_after,
        })
    }
}

#[async_trait]
impl ObjectStore for S3ObjectStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.reject_unsupported_condition(key, &condition)?;
        if let (Some(appender), WriteCondition::CreateOnly) =
            (&self.tencent_cos_append_client, &condition)
        {
            let source = AppendSource::Bytes(body);
            return appender
                .create_by_append(key, &self.full_key(key), source)
                .await;
        }
        self.api_dialect.reject_empty_body(key, body.len() as u64)?;
        let mut conflict_retries = 0;
        loop {
            let result = if body.len() >= self.multipart_threshold {
                self.put_multipart(key, body.clone(), condition.clone())
                    .await
            } else {
                self.put_single(key, body.clone(), condition.clone()).await
            };
            match result {
                Err(error)
                    if is_conflict_error(&error) && conflict_retries < MAX_CONFLICT_RETRIES =>
                {
                    tokio::time::sleep(conflict_backoff(conflict_retries)).await;
                    conflict_retries += 1;
                }
                other => return other,
            }
        }
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.reject_unsupported_condition(key, &condition)?;
        if let (Some(appender), WriteCondition::CreateOnly) =
            (&self.tencent_cos_append_client, &condition)
        {
            let source = AppendSource::File(path);
            return appender
                .create_by_append(key, &self.full_key(key), source)
                .await;
        }
        let mut conflict_retries = 0;
        loop {
            let result = self.put_file_once(key, path, condition.clone()).await;
            match result {
                Err(error)
                    if is_conflict_error(&error) && conflict_retries < MAX_CONFLICT_RETRIES =>
                {
                    tokio::time::sleep(conflict_backoff(conflict_retries)).await;
                    conflict_retries += 1;
                }
                other => return other,
            }
        }
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.get_inner(key, None).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.get_inner(key, Some(range)).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        let mut has_retried_after_listing = false;
        let response = loop {
            match self
                .read_client
                .get_object()
                .bucket(&self.bucket)
                .key(self.full_key(key))
                .send()
                .await
            {
                Ok(response) => break response,
                Err(error) => match self
                    .classify_optional_read_error(
                        key,
                        ObjectStoreOperation::Get,
                        error,
                        has_retried_after_listing,
                    )
                    .await?
                {
                    OptionalReadError::Absent => return Ok(None),
                    OptionalReadError::Retry => has_retried_after_listing = true,
                    OptionalReadError::Failed(mapped) => return Err(mapped),
                },
            }
        };
        let size = u64::try_from(response.content_length().unwrap_or(-1)).map_err(|error| {
            ObjectStoreError::new(
                ObjectStoreOperation::Get,
                key,
                ObjectStoreErrorKind::Permanent,
                error.to_string(),
            )
        })?;
        if size == 0 && self.api_dialect.reads_empty_objects_as_absent() {
            return Ok(None);
        }
        let mut download = TemporaryDownloadFile::create(key, path).await?;
        let mut body = response.body;
        while let Some(chunk) = self.read_body_chunk(&mut body, key).await? {
            download.write_all(&chunk).await?;
        }
        download.commit().await?;
        Ok(Some(size))
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        let mut has_retried_after_listing = false;
        let response = loop {
            match self
                .read_client
                .head_object()
                .bucket(&self.bucket)
                .key(self.full_key(key))
                .send()
                .await
            {
                Ok(response) => break response,
                Err(error) => match self
                    .classify_optional_read_error(
                        key,
                        ObjectStoreOperation::Head,
                        error,
                        has_retried_after_listing,
                    )
                    .await?
                {
                    OptionalReadError::Absent => return Ok(None),
                    OptionalReadError::Retry => has_retried_after_listing = true,
                    OptionalReadError::Failed(mapped) => return Err(mapped),
                },
            }
        };
        let size = u64::try_from(response.content_length().unwrap_or(-1)).map_err(|error| {
            ObjectStoreError::new(
                ObjectStoreOperation::Head,
                key,
                ObjectStoreErrorKind::Permanent,
                error.to_string(),
            )
        })?;
        if size == 0 && self.api_dialect.reads_empty_objects_as_absent() {
            return Ok(None);
        }
        Ok(Some(ObjectMetadata {
            key: key.to_owned(),
            size,
            etag: required(response.e_tag(), ObjectStoreOperation::Head, key, "ETag")?.to_owned(),
            last_modified_unix_micros: response.last_modified().map(|modified| {
                modified
                    .secs()
                    .saturating_mul(1_000_000)
                    .saturating_add(i64::from(modified.subsec_nanos() / 1_000))
            }),
        }))
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.client
            .delete_object()
            .bucket(&self.bucket)
            .key(self.full_key(key))
            .send()
            .await
            .map_err(|error| map_sdk_error(ObjectStoreOperation::Delete, key, true, error))?;
        Ok(())
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        if keys.is_empty() {
            return Ok(());
        }
        if keys.len() > 1000 {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Delete,
                "",
                ObjectStoreErrorKind::Permanent,
                "delete_many accepts at most 1000 keys",
            ));
        }
        let objects = keys
            .iter()
            .map(|key| {
                ObjectIdentifier::builder()
                    .key(self.full_key(key))
                    .build()
                    .map_err(|error| {
                        ObjectStoreError::new(
                            ObjectStoreOperation::Delete,
                            key,
                            ObjectStoreErrorKind::Permanent,
                            error.to_string(),
                        )
                    })
            })
            .collect::<ObjectStoreResult<Vec<_>>>()?;
        let delete = Delete::builder()
            .set_objects(Some(objects))
            .quiet(true)
            .build()
            .map_err(|error| {
                ObjectStoreError::new(
                    ObjectStoreOperation::Delete,
                    "",
                    ObjectStoreErrorKind::Permanent,
                    error.to_string(),
                )
            })?;
        let request = self
            .client
            .delete_objects()
            .bucket(&self.bucket)
            .delete(delete);
        let sent = match self.api_dialect {
            S3ApiDialect::AmazonS3 => request.send().await,
            // Aliyun OSS requires Content-MD5, and the SDK sends only a CRC32 checksum.
            S3ApiDialect::AliyunOss | S3ApiDialect::TencentCos => {
                request
                    .customize()
                    .mutate_request(insert_body_content_md5)
                    .send()
                    .await
            }
        };
        let response =
            sent.map_err(|error| map_sdk_error(ObjectStoreOperation::Delete, "", true, error))?;
        if let Some(error) = response.errors().first() {
            let kind = match error.code().unwrap_or("") {
                "SlowDown" | "Throttling" | "ThrottlingException" => {
                    ObjectStoreErrorKind::Transient
                }
                "InternalError" | "ServiceUnavailable" => ObjectStoreErrorKind::Transient,
                _ => ObjectStoreErrorKind::Permanent,
            };
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Delete,
                error.key().unwrap_or(""),
                kind,
                error.message().unwrap_or("batch deletion failed"),
            ));
        }
        Ok(())
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        if !(1..=1000).contains(&max_keys) {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::List,
                prefix,
                ObjectStoreErrorKind::Permanent,
                "max_keys must be 1..=1000",
            ));
        }
        if self.api_dialect.reads_empty_objects_as_absent() {
            return tencent_cos::list_non_empty_objects(self, prefix, start_after, max_keys).await;
        }
        self.list_page(prefix, start_after, max_keys).await
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        match self.api_dialect {
            S3ApiDialect::AmazonS3 => true,
            S3ApiDialect::AliyunOss | S3ApiDialect::TencentCos => false,
        }
    }
}

fn conflict_backoff(conflict_retries: u32) -> Duration {
    Duration::from_millis(10u64 << conflict_retries)
}
