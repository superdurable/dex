// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use aws_sdk_s3::error::{ProvideErrorMetadata, SdkError};
use aws_sdk_s3::primitives::ByteStream;
use bytes::Bytes;

use crate::s3_error::{map_sdk_error, required};
use crate::{
    ByteRange, ObjectContent, ObjectStore, ObjectStoreError, ObjectStoreErrorKind,
    ObjectStoreOperation, ObjectStoreResult,
};

use super::S3ObjectStore;

pub(super) enum OptionalReadError {
    Absent,
    Retry,
    Failed(ObjectStoreError),
}

impl S3ObjectStore {
    async fn key_is_absent(&self, key: &str) -> ObjectStoreResult<bool> {
        let full_key = self.full_key(key);
        let response = self
            .read_client
            .list_objects_v2()
            .bucket(&self.bucket)
            .prefix(&full_key)
            .max_keys(1)
            .send()
            .await
            .map_err(|error| map_sdk_error(ObjectStoreOperation::List, key, false, error))?;
        Ok(response.contents().first().and_then(|object| object.key()) != Some(full_key.as_str()))
    }

    pub(super) async fn classify_optional_read_error<E>(
        &self,
        key: &str,
        operation: ObjectStoreOperation,
        error: SdkError<E>,
        retried_after_listing: bool,
    ) -> ObjectStoreResult<OptionalReadError>
    where
        E: ProvideErrorMetadata + std::fmt::Display,
    {
        let status = error
            .raw_response()
            .map(|response| response.status().as_u16());
        let code = error
            .as_service_error()
            .and_then(ProvideErrorMetadata::code)
            .map(str::to_owned);
        let mapped = map_sdk_error(operation, key, false, error);
        if status == Some(404)
            && code.as_deref() != Some("NoSuchBucket")
            && (retried_after_listing
                || (operation == ObjectStoreOperation::Get && code.as_deref() == Some("NoSuchKey")))
        {
            return Ok(OptionalReadError::Absent);
        }
        if retried_after_listing {
            return Ok(OptionalReadError::Failed(mapped));
        }
        if code.as_deref() == Some("NoSuchBucket") || !matches!(status, Some(403 | 404)) {
            return Ok(OptionalReadError::Failed(mapped));
        }
        let is_absent = self.key_is_absent(key).await.map_err(|list_error| {
            ObjectStoreError::new(
                operation,
                key,
                list_error.kind,
                format!(
                    "original read: {mapped}; disambiguating ListObjectsV2 failed: {list_error}"
                ),
            )
        })?;
        Ok(if is_absent {
            OptionalReadError::Absent
        } else {
            OptionalReadError::Retry
        })
    }

    pub(super) async fn read_body_chunk(
        &self,
        body: &mut ByteStream,
        key: &str,
    ) -> ObjectStoreResult<Option<Bytes>> {
        tokio::time::timeout(self.request_timeout, body.try_next())
            .await
            .map_err(|error| {
                ObjectStoreError::new(
                    ObjectStoreOperation::Get,
                    key,
                    ObjectStoreErrorKind::Transient,
                    format!("body read timed out: {error}"),
                )
            })?
            .map_err(|error| {
                ObjectStoreError::new(
                    ObjectStoreOperation::Get,
                    key,
                    ObjectStoreErrorKind::Transient,
                    error.to_string(),
                )
            })
    }

    pub(super) async fn get_inner(
        &self,
        key: &str,
        range: Option<ByteRange>,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        if let Some(range) = range {
            if range.start > range.end_inclusive {
                return Err(ObjectStoreError::new(
                    ObjectStoreOperation::Get,
                    key,
                    ObjectStoreErrorKind::Permanent,
                    "range start exceeds end",
                ));
            }
        }
        let mut retried_after_listing = false;
        let response = loop {
            let mut request = self
                .read_client
                .get_object()
                .bucket(&self.bucket)
                .key(self.full_key(key));
            if let Some(range) = range {
                request = request.range(format!("bytes={}-{}", range.start, range.end_inclusive));
            }
            match request.send().await {
                Ok(response) => break response,
                Err(error) => {
                    let status = error
                        .raw_response()
                        .map(|response| response.status().as_u16());
                    if status == Some(416) && range.is_some() {
                        return Ok(self.head(key).await?.map(|metadata| ObjectContent {
                            bytes: Bytes::new(),
                            etag: metadata.etag,
                            object_size: metadata.size,
                        }));
                    }
                    match self
                        .classify_optional_read_error(
                            key,
                            ObjectStoreOperation::Get,
                            error,
                            retried_after_listing,
                        )
                        .await?
                    {
                        OptionalReadError::Absent => return Ok(None),
                        OptionalReadError::Retry => retried_after_listing = true,
                        OptionalReadError::Failed(mapped) => return Err(mapped),
                    }
                }
            }
        };
        let etag = required(response.e_tag(), ObjectStoreOperation::Get, key, "ETag")?.to_owned();
        let object_size = if range.is_some() {
            parse_content_range_size(
                required(
                    response.content_range(),
                    ObjectStoreOperation::Get,
                    key,
                    "Content-Range",
                )?,
                key,
            )?
        } else {
            u64::try_from(response.content_length().unwrap_or(-1)).map_err(|error| {
                ObjectStoreError::new(
                    ObjectStoreOperation::Get,
                    key,
                    ObjectStoreErrorKind::Permanent,
                    format!("invalid object size: {error}"),
                )
            })?
        };
        let mut body = response.body;
        let mut buffer = Vec::new();
        while let Some(chunk) = self.read_body_chunk(&mut body, key).await? {
            buffer.extend_from_slice(&chunk);
        }
        let bytes = Bytes::from(buffer);
        Ok(Some(ObjectContent {
            bytes,
            etag,
            object_size,
        }))
    }
}

fn parse_content_range_size(value: &str, key: &str) -> ObjectStoreResult<u64> {
    let invalid = || {
        ObjectStoreError::new(
            ObjectStoreOperation::Get,
            key,
            ObjectStoreErrorKind::Permanent,
            format!("invalid Content-Range: {value}"),
        )
    };
    let (bounds, total) = value
        .strip_prefix("bytes ")
        .and_then(|value| value.split_once('/'))
        .ok_or_else(invalid)?;
    let (start, end) = bounds.split_once('-').ok_or_else(invalid)?;
    let start = start.parse::<u64>().map_err(|_| invalid())?;
    let end = end.parse::<u64>().map_err(|_| invalid())?;
    let total = total.parse::<u64>().map_err(|_| invalid())?;
    if start <= end && end < total {
        Ok(total)
    } else {
        Err(invalid())
    }
}
