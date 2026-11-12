// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::Path;

use bytes::Bytes;
use futures::StreamExt;
use futures::stream::FuturesUnordered;
use object_store::path::Path as ObjectPath;
use object_store::{MultipartUpload, ObjectStore, PutPayload, PutResult, UploadPart};
use tokio::io::AsyncReadExt;

use super::error_mapping::{RequestEffect, map_object_store_error};
use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation};

const PARTS_IN_FLIGHT: usize = 4;

/// Where a large upload reads its body.
#[derive(Clone, Copy)]
pub(super) enum LargeUploadSource<'a> {
    Bytes(&'a Bytes),
    File(&'a Path),
}

/// Why an unconditional multipart upload failed.
#[derive(Debug)]
pub(super) enum MultipartUploadFailure {
    /// The upload failed before its completion request, so the target key is unchanged.
    BeforeCompletion(ObjectStoreError),
    /// The completion request failed; with `OutcomeUnknown`, the target may hold the upload.
    Completion(ObjectStoreError),
}

/// Uploads `source` to `path` in parts without a condition, and returns the completed object.
///
/// A failed abort never replaces the completion result, unlike `WriteMultipart::finish`.
pub(super) async fn upload_in_parts(
    writes: &dyn ObjectStore,
    key: &str,
    path: &ObjectPath,
    source: LargeUploadSource<'_>,
    part_size: usize,
) -> Result<PutResult, MultipartUploadFailure> {
    let map_error = |error: &object_store::Error| {
        map_object_store_error(ObjectStoreOperation::Put, key, RequestEffect::Write, error)
    };
    let mut upload = writes
        .put_multipart_opts(path, Default::default())
        .await
        .map_err(|error| MultipartUploadFailure::BeforeCompletion(map_error(&error)))?;
    if let Err(error) = send_parts(upload.as_mut(), key, source, part_size).await {
        abort_upload(upload, key).await;
        return Err(MultipartUploadFailure::BeforeCompletion(error));
    }
    match upload.complete().await {
        Ok(result) => Ok(result),
        Err(error) => {
            let mapped = map_error(&error);
            // The completion may have applied; the bucket lifecycle rule removes a leftover upload.
            if mapped.kind != ObjectStoreErrorKind::OutcomeUnknown {
                abort_upload(upload, key).await;
            }
            Err(MultipartUploadFailure::Completion(mapped))
        }
    }
}

async fn abort_upload(mut upload: Box<dyn MultipartUpload>, key: &str) {
    if let Err(error) = upload.abort().await {
        tracing::warn!(key, %error, "failed to abort a multipart upload");
    }
}

async fn send_parts(
    upload: &mut dyn MultipartUpload,
    key: &str,
    source: LargeUploadSource<'_>,
    part_size: usize,
) -> Result<(), ObjectStoreError> {
    let mut in_flight: FuturesUnordered<UploadPart> = FuturesUnordered::new();
    match source {
        LargeUploadSource::Bytes(body) => {
            let mut offset = 0;
            while offset < body.len() {
                let end = body.len().min(offset + part_size);
                await_capacity(&mut in_flight, key).await?;
                in_flight.push(upload.put_part(PutPayload::from(body.slice(offset..end))));
                offset = end;
            }
        }
        LargeUploadSource::File(file_path) => {
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
                let mut buffer = vec![0; part_size];
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
                    break;
                }
                buffer.truncate(filled);
                await_capacity(&mut in_flight, key).await?;
                in_flight.push(upload.put_part(PutPayload::from(Bytes::from(buffer))));
            }
        }
    }
    while let Some(sent) = in_flight.next().await {
        sent.map_err(|error| part_error(key, &error))?;
    }
    Ok(())
}

async fn await_capacity(
    in_flight: &mut FuturesUnordered<UploadPart>,
    key: &str,
) -> Result<(), ObjectStoreError> {
    while in_flight.len() >= PARTS_IN_FLIGHT {
        if let Some(sent) = in_flight.next().await {
            sent.map_err(|error| part_error(key, &error))?;
        }
    }
    Ok(())
}

fn part_error(key: &str, error: &object_store::Error) -> ObjectStoreError {
    map_object_store_error(ObjectStoreOperation::Put, key, RequestEffect::Write, error)
}
