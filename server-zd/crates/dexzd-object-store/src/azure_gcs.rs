// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;
use std::path::Path;
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use futures::StreamExt;
use object_store::azure::MicrosoftAzure;
use object_store::list::{PaginatedListOptions, PaginatedListStore};
use object_store::path::Path as ObjectPath;
use object_store::{
    GetOptions, GetRange, ObjectStoreExt, PutMode, PutOptions, PutPayload, PutResult,
    WriteMultipart,
};
use tokio::io::AsyncReadExt;

use crate::file_download::TemporaryDownloadFile;
use crate::{
    ByteRange, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, WriteCondition, WriteOutcome,
};

mod azure_gcs_config;
mod cloud_clients;
mod error_mapping;
mod google_cloud_storage_listing;
mod listing_accumulator;
mod response_status_classifier;
mod version_token;

pub use azure_gcs_config::{
    AzureBlobCredentials, AzureGcsObjectStoreConfig, AzureGcsProvider,
    GoogleCloudStorageCredentials,
};
use cloud_clients::{CloudClients, config_error};
use error_mapping::{RequestEffect, classified_status, is_throttled, map_object_store_error};
use google_cloud_storage_listing::GoogleCloudStorageListing;
use listing_accumulator::ListingAccumulator;
use version_token::{update_version, version_token};

/// Key prefix of the unconditional uploads that large create-only writes copy into place.
pub const STAGING_KEY_PREFIX: &str = "staging/";

const MIN_PART_SIZE: usize = 5 * 1024 * 1024;
const PARTS_IN_FLIGHT: usize = 4;
const THROTTLED_WRITE_ATTEMPTS: u32 = 4;
const MAX_LISTING_PAGES_PER_CALL: usize = 64;
const ABANDONED_STAGING_AGE_MICROS: i64 = 60 * 60 * 1_000_000;

/// Which cloud an [`AzureGcsObjectStore`] serves.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum AzureGcsCloud {
    AzureBlob,
    GoogleCloudStorage,
}

/// Where a large upload reads its body.
#[derive(Clone, Copy)]
enum LargeUploadSource<'a> {
    Bytes(&'a Bytes),
    File(&'a Path),
}

/// [`ObjectStore`] on Azure Blob Storage or Google Cloud Storage through the `object_store` crate.
pub struct AzureGcsObjectStore {
    clients: CloudClients,
    cloud: AzureGcsCloud,
    key_prefix: String,
    single_put_max_bytes: usize,
    multipart_part_size: usize,
    throttled_write_initial_backoff: Duration,
}

impl AzureGcsObjectStore {
    /// Builds the clients; no request is sent until the first operation.
    pub fn new(config: AzureGcsObjectStoreConfig) -> ObjectStoreResult<Self> {
        let (location_name, location) = match &config.provider {
            AzureGcsProvider::AzureBlob { container, .. } => ("container", container),
            AzureGcsProvider::GoogleCloudStorage { bucket, .. } => ("bucket", bucket),
        };
        for (field, is_invalid) in [
            (location_name, location.is_empty()),
            (
                "account",
                matches!(&config.provider, AzureGcsProvider::AzureBlob { account, .. } if account.is_empty()),
            ),
            ("request_timeout", config.request_timeout.is_zero()),
            ("single_put_max_bytes", config.single_put_max_bytes == 0),
            (
                "multipart_part_size",
                config.multipart_part_size < MIN_PART_SIZE,
            ),
        ] {
            if is_invalid {
                return Err(config_error(format!(
                    "invalid object-store config field: {field}"
                )));
            }
        }
        let (cloud, clients) = CloudClients::build(&config)?;
        Ok(Self {
            clients,
            cloud,
            key_prefix: if config.key_prefix.is_empty() {
                String::new()
            } else {
                format!("{}/", config.key_prefix.trim_end_matches('/'))
            },
            single_put_max_bytes: config.single_put_max_bytes,
            multipart_part_size: config.multipart_part_size,
            throttled_write_initial_backoff: config.throttled_write_initial_backoff,
        })
    }

    /// Deletes staging objects modified over an hour ago by store time, and returns their count.
    pub async fn delete_abandoned_staging_objects(&self) -> ObjectStoreResult<usize> {
        let cutoff = self.store_time_unix_micros().await? - ABANDONED_STAGING_AGE_MICROS;
        let mut start_after: Option<String> = None;
        let mut deleted = 0;
        loop {
            let listing = self
                .list(STAGING_KEY_PREFIX, start_after.as_deref(), 1000)
                .await?;
            let abandoned: Vec<String> = listing
                .objects
                .iter()
                .filter(|object| {
                    object
                        .last_modified_unix_micros
                        .is_some_and(|modified| modified < cutoff)
                })
                .map(|object| object.key.clone())
                .collect();
            if !abandoned.is_empty() {
                self.delete_many(&abandoned).await?;
                deleted += abandoned.len();
            }
            match listing.next_start_after {
                Some(next) => start_after = Some(next),
                None => return Ok(deleted),
            }
        }
    }

    async fn store_time_unix_micros(&self) -> ObjectStoreResult<i64> {
        let key = format!(
            "{STAGING_KEY_PREFIX}clock-{}",
            uuid::Uuid::new_v4().simple()
        );
        self.put(&key, Bytes::new(), WriteCondition::Unconditional)
            .await?;
        let metadata = self.head(&key).await;
        if let Err(error) = self.delete(&key).await {
            tracing::warn!(key, %error, "failed to delete the store clock probe");
        }
        metadata?
            .and_then(|metadata| metadata.last_modified_unix_micros)
            .ok_or_else(|| {
                ObjectStoreError::new(
                    ObjectStoreOperation::Head,
                    key,
                    ObjectStoreErrorKind::Permanent,
                    "store clock probe has no modification time",
                )
            })
    }

    fn object_path(
        &self,
        key: &str,
        operation: ObjectStoreOperation,
    ) -> ObjectStoreResult<ObjectPath> {
        let full_key = format!("{}{}", self.key_prefix, key);
        match ObjectPath::parse(&full_key) {
            Ok(path) if !key.is_empty() && path.as_ref() == full_key => Ok(path),
            Ok(_) => Err(ObjectStoreError::new(
                operation,
                key,
                ObjectStoreErrorKind::Permanent,
                "key must not be empty or end with a slash",
            )),
            Err(error) => Err(ObjectStoreError::new(
                operation,
                key,
                ObjectStoreErrorKind::Permanent,
                format!("key is not a valid object path: {error}"),
            )),
        }
    }

    /// Sends a write, retrying while the service throttles it; a throttled request did nothing.
    async fn send_retrying_throttled<T, SendWrite, WriteFuture>(
        &self,
        mut send: SendWrite,
    ) -> object_store::Result<T>
    where
        SendWrite: FnMut() -> WriteFuture,
        WriteFuture: Future<Output = object_store::Result<T>>,
    {
        let mut backoff = self.throttled_write_initial_backoff;
        for _ in 1..THROTTLED_WRITE_ATTEMPTS {
            match send().await {
                Err(error) if is_throttled(&error) => {
                    tokio::time::sleep(backoff).await;
                    backoff = backoff.saturating_mul(2);
                }
                result => return result,
            }
        }
        send().await
    }

    async fn put_body(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        if body.len() <= self.single_put_max_bytes {
            return self.put_single_request(key, body, condition).await;
        }
        match condition {
            WriteCondition::CreateOnly => {
                self.create_through_staging(key, LargeUploadSource::Bytes(&body))
                    .await
            }
            WriteCondition::Unconditional => {
                self.replace_through_multipart(key, LargeUploadSource::Bytes(&body))
                    .await
            }
            // The body is already in memory, so one conditional request is safe at any size.
            WriteCondition::ReplaceIfEtagMatches { .. } => {
                self.put_single_request(key, body, condition).await
            }
        }
    }

    async fn put_single_request(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let mode = match &condition {
            WriteCondition::Unconditional => PutMode::Overwrite,
            WriteCondition::CreateOnly => PutMode::Create,
            WriteCondition::ReplaceIfEtagMatches { etag } => {
                match update_version(self.cloud, etag) {
                    Some(version) => PutMode::Update(version),
                    None => return Ok(WriteOutcome::PreconditionFailed),
                }
            }
        };
        let path = self.object_path(key, ObjectStoreOperation::Put)?;
        let payload = PutPayload::from(body);
        let sent = self
            .send_retrying_throttled(|| {
                self.clients.writes().put_opts(
                    &path,
                    payload.clone(),
                    PutOptions::from(mode.clone()),
                )
            })
            .await;
        match (sent, &condition) {
            (Ok(result), _) => self.written(key, &result),
            (
                Err(
                    object_store::Error::AlreadyExists { .. }
                    | object_store::Error::Precondition { .. },
                ),
                WriteCondition::CreateOnly | WriteCondition::ReplaceIfEtagMatches { .. },
            )
            | (
                Err(object_store::Error::NotFound { .. }),
                WriteCondition::ReplaceIfEtagMatches { .. },
            ) => Ok(WriteOutcome::PreconditionFailed),
            (Err(error), _) => Err(map_object_store_error(
                ObjectStoreOperation::Put,
                key,
                RequestEffect::Write,
                &error,
            )),
        }
    }

    /// Reports a completed write. A missing version token leaves its outcome unknown to callers.
    fn written(&self, key: &str, result: &PutResult) -> ObjectStoreResult<WriteOutcome> {
        version_token(
            self.cloud,
            result.e_tag.as_deref(),
            result.version.as_deref(),
            ObjectStoreOperation::Put,
            key,
        )
        .map(|etag| WriteOutcome::Written { etag })
        .map_err(|error| ObjectStoreError {
            kind: ObjectStoreErrorKind::OutcomeUnknown,
            ..error
        })
    }

    /// Uploads to a unique staging key, then copies it to `key` only if `key` does not exist.
    async fn create_through_staging(
        &self,
        key: &str,
        source: LargeUploadSource<'_>,
    ) -> ObjectStoreResult<WriteOutcome> {
        let target = self.object_path(key, ObjectStoreOperation::Put)?;
        let created_unix_millis = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .expect("system clock is after the Unix epoch")
            .as_millis();
        let staging_key = format!(
            "{STAGING_KEY_PREFIX}{created_unix_millis:013}-{}",
            uuid::Uuid::new_v4().simple()
        );
        let staging = self.object_path(&staging_key, ObjectStoreOperation::Put)?;
        if let Err(error) = self.upload_multipart(key, &staging, source).await {
            self.delete_staging_object(&staging).await;
            // The staging upload never touches the target key.
            let kind = match error.kind {
                ObjectStoreErrorKind::OutcomeUnknown => ObjectStoreErrorKind::Transient,
                kind => kind,
            };
            return Err(ObjectStoreError {
                kind,
                detail: format!("staging upload failed: {}", error.detail),
                ..error
            });
        }
        let copied = self
            .send_retrying_throttled(|| self.clients.writes().copy_if_not_exists(&staging, &target))
            .await;
        let outcome = match copied {
            Ok(()) => None,
            Err(
                object_store::Error::AlreadyExists { .. }
                | object_store::Error::Precondition { .. },
            ) => Some(Ok(WriteOutcome::PreconditionFailed)),
            Err(object_store::Error::NotFound { .. }) => Some(Err(ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::Transient,
                "staging object disappeared before it was copied",
            ))),
            Err(error) => Some(Err(map_object_store_error(
                ObjectStoreOperation::Put,
                key,
                RequestEffect::Write,
                &error,
            ))),
        };
        // An unfinished copy may still read the staging object, so keep it for the cleanup.
        if !matches!(&outcome, Some(Err(error)) if error.kind == ObjectStoreErrorKind::OutcomeUnknown)
        {
            self.delete_staging_object(&staging).await;
        }
        if let Some(outcome) = outcome {
            return outcome;
        }
        match self.clients.reads().head(&target).await {
            Ok(metadata) => self.written(
                key,
                &PutResult {
                    e_tag: metadata.e_tag,
                    version: metadata.version,
                    extensions: Default::default(),
                },
            ),
            Err(error) => Err(ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::OutcomeUnknown,
                format!("created by copy, but reading its ETag failed: {error}"),
            )),
        }
    }

    async fn delete_staging_object(&self, staging: &ObjectPath) {
        match self.clients.writes().delete(staging).await {
            Ok(()) | Err(object_store::Error::NotFound { .. }) => {}
            Err(error) => {
                tracing::warn!(key = %staging, %error, "failed to delete a staging object");
            }
        }
    }

    async fn replace_through_multipart(
        &self,
        key: &str,
        source: LargeUploadSource<'_>,
    ) -> ObjectStoreResult<WriteOutcome> {
        let target = self.object_path(key, ObjectStoreOperation::Put)?;
        let result = self.upload_multipart(key, &target, source).await?;
        self.written(key, &result)
    }

    /// Uploads `source` to `path` in parts without a condition.
    async fn upload_multipart(
        &self,
        key: &str,
        path: &ObjectPath,
        source: LargeUploadSource<'_>,
    ) -> ObjectStoreResult<PutResult> {
        let map_store_error = |error: object_store::Error| {
            map_object_store_error(ObjectStoreOperation::Put, key, RequestEffect::Write, &error)
        };
        let upload = self
            .clients
            .writes()
            .put_multipart(path)
            .await
            .map_err(map_store_error)?;
        let mut writer = WriteMultipart::new_with_chunk_size(upload, self.multipart_part_size);
        let written = match source {
            LargeUploadSource::Bytes(body) => self.write_body_parts(&mut writer, key, body).await,
            LargeUploadSource::File(file_path) => {
                self.write_file_parts(&mut writer, key, file_path).await
            }
        };
        if let Err(error) = written {
            if let Err(abort_error) = writer.abort().await {
                tracing::warn!(key, error = %abort_error, "failed to abort a multipart upload");
            }
            return Err(error);
        }
        writer.finish().await.map_err(map_store_error)
    }

    async fn write_body_parts(
        &self,
        writer: &mut WriteMultipart,
        key: &str,
        body: &Bytes,
    ) -> ObjectStoreResult<()> {
        let mut offset = 0;
        while offset < body.len() {
            let end = body.len().min(offset + self.multipart_part_size);
            writer
                .wait_for_capacity(PARTS_IN_FLIGHT)
                .await
                .map_err(|error| {
                    map_object_store_error(
                        ObjectStoreOperation::Put,
                        key,
                        RequestEffect::Write,
                        &error,
                    )
                })?;
            writer.put(body.slice(offset..end));
            offset = end;
        }
        Ok(())
    }

    async fn write_file_parts(
        &self,
        writer: &mut WriteMultipart,
        key: &str,
        file_path: &Path,
    ) -> ObjectStoreResult<()> {
        let file_error = |action: &str, error: std::io::Error| {
            ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::Permanent,
                format!("{action} {}: {error}", file_path.display()),
            )
        };
        let mut file = tokio::fs::File::open(file_path)
            .await
            .map_err(|error| file_error("opening", error))?;
        loop {
            let mut buffer = vec![0; self.multipart_part_size];
            let mut filled = 0;
            while filled < buffer.len() {
                let read = file
                    .read(&mut buffer[filled..])
                    .await
                    .map_err(|error| file_error("reading", error))?;
                if read == 0 {
                    break;
                }
                filled += read;
            }
            if filled == 0 {
                return Ok(());
            }
            buffer.truncate(filled);
            writer
                .wait_for_capacity(PARTS_IN_FLIGHT)
                .await
                .map_err(|error| {
                    map_object_store_error(
                        ObjectStoreOperation::Put,
                        key,
                        RequestEffect::Write,
                        &error,
                    )
                })?;
            writer.put(Bytes::from(buffer));
        }
    }

    async fn get_inner(
        &self,
        key: &str,
        range: Option<ByteRange>,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        if let Some(range) = range
            && range.start > range.end_inclusive
        {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Get,
                key,
                ObjectStoreErrorKind::Permanent,
                "range start exceeds end",
            ));
        }
        let path = self.object_path(key, ObjectStoreOperation::Get)?;
        let options = GetOptions {
            range: range
                .map(|range| GetRange::Bounded(range.start..range.end_inclusive.saturating_add(1))),
            ..GetOptions::default()
        };
        let map_read_error = |error: &object_store::Error| {
            map_object_store_error(ObjectStoreOperation::Get, key, RequestEffect::Read, error)
        };
        let result = match self.clients.reads().get_opts(&path, options).await {
            Ok(result) => result,
            Err(object_store::Error::NotFound { .. }) => return Ok(None),
            // A range that starts past the end returns no bytes, as on S3.
            Err(error) if range.is_some() && classified_status(&error) == Some(416) => {
                return Ok(self.head(key).await?.map(|metadata| ObjectContent {
                    bytes: Bytes::new(),
                    etag: metadata.etag,
                    object_size: metadata.size,
                }));
            }
            Err(error) => return Err(map_read_error(&error)),
        };
        let etag = version_token(
            self.cloud,
            result.meta.e_tag.as_deref(),
            result.meta.version.as_deref(),
            ObjectStoreOperation::Get,
            key,
        )?;
        let object_size = result.meta.size;
        let bytes = result
            .bytes()
            .await
            .map_err(|error| map_read_error(&error))?;
        Ok(Some(ObjectContent {
            bytes,
            etag,
            object_size,
        }))
    }

    async fn list_azure_blob(
        &self,
        reads: &MicrosoftAzure,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        let full_prefix = format!("{}{}", self.key_prefix, prefix);
        let full_start_after = start_after.map(|after| format!("{}{}", self.key_prefix, after));
        let mut accumulator = ListingAccumulator::new(
            self.cloud,
            &self.key_prefix,
            prefix,
            &full_prefix,
            full_start_after,
        );
        // object_store drops a blob named exactly the prefix, so read that blob separately.
        let may_hold_prefix_object = !prefix.is_empty()
            && !prefix.ends_with('/')
            && start_after.is_none_or(|after| after < prefix)
            && self.object_path(prefix, ObjectStoreOperation::List).is_ok();
        if may_hold_prefix_object && let Some(metadata) = self.head(prefix).await? {
            accumulator.accept(
                &full_prefix,
                metadata.size,
                Some(&metadata.etag),
                None,
                metadata.last_modified_unix_micros,
            )?;
            if accumulator.is_full(max_keys) {
                return Ok(accumulator.finish(true));
            }
        }
        let mut page_token = None;
        for _ in 0..MAX_LISTING_PAGES_PER_CALL {
            let options = PaginatedListOptions {
                offset: page_token
                    .is_none()
                    .then(|| accumulator.start_after.clone())
                    .flatten(),
                max_keys: Some(accumulator.remaining(max_keys)),
                page_token: page_token.take(),
                ..PaginatedListOptions::default()
            };
            let page = reads
                .list_paginated(Some(&full_prefix), options)
                .await
                .map_err(|error| {
                    map_object_store_error(
                        ObjectStoreOperation::List,
                        prefix,
                        RequestEffect::Read,
                        &error,
                    )
                })?;
            for object in page.result.objects {
                accumulator.accept(
                    object.location.as_ref(),
                    object.size,
                    object.e_tag.as_deref(),
                    None,
                    Some(object.last_modified.timestamp_micros()),
                )?;
            }
            match page.page_token {
                None => return Ok(accumulator.finish(false)),
                Some(_) if !accumulator.is_empty() => return Ok(accumulator.finish(true)),
                Some(token) => page_token = Some(token),
            }
        }
        Err(too_many_empty_pages(prefix))
    }

    async fn list_google_cloud_storage(
        &self,
        listing: &GoogleCloudStorageListing,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        let full_prefix = format!("{}{}", self.key_prefix, prefix);
        let full_start_after = start_after.map(|after| format!("{}{}", self.key_prefix, after));
        let mut accumulator = ListingAccumulator::new(
            self.cloud,
            &self.key_prefix,
            prefix,
            &full_prefix,
            full_start_after,
        );
        let mut page_token: Option<String> = None;
        for _ in 0..MAX_LISTING_PAGES_PER_CALL {
            // startOffset is inclusive; the accumulator drops `start_after` itself.
            let start_offset = accumulator.start_after.clone();
            let page = listing
                .list_page(
                    &full_prefix,
                    start_offset.as_deref(),
                    page_token.as_deref(),
                    u32::try_from(accumulator.remaining(max_keys) + 1).unwrap_or(u32::MAX),
                )
                .await?;
            for object in &page.items {
                let last_modified = object.updated_unix_micros(prefix)?;
                accumulator.accept(
                    &object.name,
                    object.size,
                    None,
                    Some(&object.generation),
                    last_modified,
                )?;
                if accumulator.is_full(max_keys) {
                    return Ok(accumulator.finish(true));
                }
            }
            match page.next_page_token {
                None => return Ok(accumulator.finish(false)),
                Some(_) if !accumulator.is_empty() => return Ok(accumulator.finish(true)),
                Some(token) => page_token = Some(token),
            }
        }
        Err(too_many_empty_pages(prefix))
    }
}

#[async_trait]
impl ObjectStore for AzureGcsObjectStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.put_body(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let file_error = |error: std::io::Error| {
            ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::Permanent,
                format!("reading {}: {error}", path.display()),
            )
        };
        let size = tokio::fs::metadata(path).await.map_err(file_error)?.len();
        if size <= self.single_put_max_bytes as u64 {
            let body = tokio::fs::read(path).await.map_err(file_error)?;
            return self
                .put_single_request(key, Bytes::from(body), condition)
                .await;
        }
        match condition {
            WriteCondition::CreateOnly => {
                self.create_through_staging(key, LargeUploadSource::File(path))
                    .await
            }
            WriteCondition::Unconditional => {
                self.replace_through_multipart(key, LargeUploadSource::File(path))
                    .await
            }
            WriteCondition::ReplaceIfEtagMatches { .. } => Err(ObjectStoreError::new(
                ObjectStoreOperation::Put,
                key,
                ObjectStoreErrorKind::Permanent,
                format!(
                    "replacing by ETag supports files of at most {} bytes",
                    self.single_put_max_bytes
                ),
            )),
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
        let object_path = self.object_path(key, ObjectStoreOperation::Get)?;
        let map_read_error = |error: &object_store::Error| {
            map_object_store_error(ObjectStoreOperation::Get, key, RequestEffect::Read, error)
        };
        let result = match self.clients.reads().get(&object_path).await {
            Ok(result) => result,
            Err(object_store::Error::NotFound { .. }) => return Ok(None),
            Err(error) => return Err(map_read_error(&error)),
        };
        let size = result.meta.size;
        let mut download = TemporaryDownloadFile::create(key, path).await?;
        let mut body = result.into_stream();
        while let Some(chunk) = body.next().await {
            download
                .write_all(&chunk.map_err(|error| map_read_error(&error))?)
                .await?;
        }
        download.commit().await?;
        Ok(Some(size))
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        let path = self.object_path(key, ObjectStoreOperation::Head)?;
        let metadata = match self.clients.reads().head(&path).await {
            Ok(metadata) => metadata,
            Err(object_store::Error::NotFound { .. }) => return Ok(None),
            Err(error) => {
                return Err(map_object_store_error(
                    ObjectStoreOperation::Head,
                    key,
                    RequestEffect::Read,
                    &error,
                ));
            }
        };
        Ok(Some(ObjectMetadata {
            key: key.to_owned(),
            size: metadata.size,
            etag: version_token(
                self.cloud,
                metadata.e_tag.as_deref(),
                metadata.version.as_deref(),
                ObjectStoreOperation::Head,
                key,
            )?,
            last_modified_unix_micros: Some(metadata.last_modified.timestamp_micros()),
        }))
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        let path = self.object_path(key, ObjectStoreOperation::Delete)?;
        match self
            .send_retrying_throttled(|| self.clients.writes().delete(&path))
            .await
        {
            Ok(()) | Err(object_store::Error::NotFound { .. }) => Ok(()),
            Err(error) => Err(map_object_store_error(
                ObjectStoreOperation::Delete,
                key,
                RequestEffect::Write,
                &error,
            )),
        }
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        if keys.len() > 1000 {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Delete,
                "",
                ObjectStoreErrorKind::Permanent,
                "delete_many accepts at most 1000 keys",
            ));
        }
        let paths = keys
            .iter()
            .map(|key| self.object_path(key, ObjectStoreOperation::Delete))
            .collect::<ObjectStoreResult<Vec<_>>>()?;
        if paths.is_empty() {
            return Ok(());
        }
        let mut results = self
            .clients
            .writes()
            .delete_stream(futures::stream::iter(paths.into_iter().map(Ok)).boxed());
        while let Some(result) = results.next().await {
            match result {
                Ok(_) | Err(object_store::Error::NotFound { .. }) => {}
                Err(error) => {
                    return Err(map_object_store_error(
                        ObjectStoreOperation::Delete,
                        "",
                        RequestEffect::Write,
                        &error,
                    ));
                }
            }
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
        match &self.clients {
            CloudClients::AzureBlob { reads, .. } => {
                self.list_azure_blob(reads, prefix, start_after, max_keys)
                    .await
            }
            CloudClients::GoogleCloudStorage { listing, .. } => {
                self.list_google_cloud_storage(listing, prefix, start_after, max_keys)
                    .await
            }
        }
    }
}

fn too_many_empty_pages(prefix: &str) -> ObjectStoreError {
    ObjectStoreError::new(
        ObjectStoreOperation::List,
        prefix,
        ObjectStoreErrorKind::Transient,
        "listing returned too many empty pages",
    )
}
