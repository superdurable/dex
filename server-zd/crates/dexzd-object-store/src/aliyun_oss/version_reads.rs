// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Reads of mutable object keys: list the newest version, then read that same object.

use bytes::Bytes;

use crate::aliyun_oss::version_body::{
    VERSION_HEADER_LENGTH, VERSION_HEADER_RANGE, VersionEtag, VersionHeader,
};
use crate::aliyun_oss::version_keys::{
    malformed_version_key, version_generation, version_object_key, version_prefix,
};
use crate::{
    ByteRange, ObjectContent, ObjectMetadata, ObjectStore, ObjectStoreError, ObjectStoreErrorKind,
    ObjectStoreOperation, ObjectStoreResult,
};

/// Bounds the listings of one read; each retry means a newer version appeared meanwhile.
const MAX_NEWEST_VERSION_LOOKUPS: usize = 16;

/// How much of the newest version a read needs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum VersionReadExtent {
    /// Only the header; the payload stays unread.
    HeaderOnly,
    /// The header and the payload.
    WholeBody,
}

/// The newest version of a mutable object key, which may be a tombstone.
#[derive(Debug, Clone)]
pub(super) struct NewestVersion {
    /// Generation of the version.
    pub(super) generation: u64,
    /// Header of the version.
    pub(super) header: VersionHeader,
    /// Payload, present only for [`VersionReadExtent::WholeBody`] reads.
    pub(super) payload: Option<Bytes>,
    /// Payload size in bytes.
    pub(super) payload_size: u64,
    /// Last write time of the version object, when the store reports one.
    pub(super) last_modified_unix_micros: Option<i64>,
}

impl NewestVersion {
    /// Returns the ETag of this version.
    pub(super) fn etag(&self) -> VersionEtag {
        VersionEtag {
            generation: self.generation,
            version_id: self.header.version_id,
        }
    }
}

/// Reads the newest version, or `None`; a read that finds another object lists again.
pub(super) async fn read_newest_version(
    inner: &dyn ObjectStore,
    operation: ObjectStoreOperation,
    logical_key: &str,
    extent: VersionReadExtent,
) -> ObjectStoreResult<Option<NewestVersion>> {
    let prefix = version_prefix(logical_key);
    for _ in 0..MAX_NEWEST_VERSION_LOOKUPS {
        let listing = inner
            .list(&prefix, None, 1)
            .await
            .map_err(|error| inner_error(operation, logical_key, &error))?;
        let Some(newest) = listing.objects.into_iter().next() else {
            return Ok(None);
        };
        let generation = version_generation(&newest.key, &prefix)
            .ok_or_else(|| malformed_version_key(operation, logical_key, &newest.key))?;
        let content = match extent {
            VersionReadExtent::HeaderOnly => {
                inner.get_range(&newest.key, VERSION_HEADER_RANGE).await
            }
            VersionReadExtent::WholeBody => inner.get(&newest.key).await,
        }
        .map_err(|error| inner_error(operation, logical_key, &error))?;
        // A resurrected object can replace the listed one, but it is never the newest.
        let Some(content) = content.filter(|content| is_listed_object(&newest, content)) else {
            continue;
        };
        let header = decode_header(operation, logical_key, &newest.key, &content)?;
        let payload = (extent == VersionReadExtent::WholeBody)
            .then(|| content.bytes.slice(VERSION_HEADER_LENGTH..));
        return Ok(Some(NewestVersion {
            generation,
            header,
            payload,
            payload_size: content
                .object_size
                .saturating_sub(VERSION_HEADER_LENGTH as u64),
            last_modified_unix_micros: newest.last_modified_unix_micros,
        }));
    }
    Err(ObjectStoreError::new(
        operation,
        logical_key,
        ObjectStoreErrorKind::Transient,
        "the newest version changed during every read attempt",
    ))
}

/// Reads the header of generation `generation` of `logical_key`, or `None` when it is absent.
pub(super) async fn read_version_header(
    inner: &dyn ObjectStore,
    operation: ObjectStoreOperation,
    logical_key: &str,
    generation: u64,
) -> ObjectStoreResult<Option<VersionHeader>> {
    read_header_at(
        inner,
        operation,
        logical_key,
        &version_object_key(logical_key, generation),
    )
    .await
}

/// Reads the header of the listed object `listed`, or `None` when it is gone or replaced.
pub(super) async fn read_listed_header(
    inner: &dyn ObjectStore,
    operation: ObjectStoreOperation,
    logical_key: &str,
    listed: &ObjectMetadata,
) -> ObjectStoreResult<Option<VersionHeader>> {
    let Some(content) = inner
        .get_range(&listed.key, VERSION_HEADER_RANGE)
        .await
        .map_err(|error| inner_error(operation, logical_key, &error))?
        .filter(|content| is_listed_object(listed, content))
    else {
        return Ok(None);
    };
    decode_header(operation, logical_key, &listed.key, &content).map(Some)
}

/// Reads the header of the version object at `physical_key`, or `None` when it is absent.
async fn read_header_at(
    inner: &dyn ObjectStore,
    operation: ObjectStoreOperation,
    logical_key: &str,
    physical_key: &str,
) -> ObjectStoreResult<Option<VersionHeader>> {
    let Some(content) = inner
        .get_range(physical_key, VERSION_HEADER_RANGE)
        .await
        .map_err(|error| inner_error(operation, logical_key, &error))?
    else {
        return Ok(None);
    };
    decode_header(operation, logical_key, physical_key, &content).map(Some)
}

/// Reads the current content of `logical_key`, or `None` when it is absent or deleted.
pub(super) async fn get_versioned(
    inner: &dyn ObjectStore,
    logical_key: &str,
) -> ObjectStoreResult<Option<ObjectContent>> {
    let Some(newest) = read_newest_version(
        inner,
        ObjectStoreOperation::Get,
        logical_key,
        VersionReadExtent::WholeBody,
    )
    .await?
    else {
        return Ok(None);
    };
    if newest.header.is_tombstone {
        return Ok(None);
    }
    let etag = newest.etag().to_string();
    let bytes = newest
        .payload
        .expect("a whole-body read returns the payload");
    Ok(Some(ObjectContent {
        object_size: bytes.len() as u64,
        bytes,
        etag,
    }))
}

/// Reads `range` of the current content of `logical_key`, slicing the whole payload.
pub(super) async fn get_versioned_range(
    inner: &dyn ObjectStore,
    logical_key: &str,
    range: ByteRange,
) -> ObjectStoreResult<Option<ObjectContent>> {
    if range.start > range.end_inclusive {
        return Err(ObjectStoreError::new(
            ObjectStoreOperation::Get,
            logical_key,
            ObjectStoreErrorKind::Permanent,
            "range start exceeds end",
        ));
    }
    let Some(mut content) = get_versioned(inner, logical_key).await? else {
        return Ok(None);
    };
    let length = content.bytes.len();
    let start = usize::try_from(range.start)
        .unwrap_or(usize::MAX)
        .min(length);
    let end = usize::try_from(range.end_inclusive)
        .unwrap_or(usize::MAX)
        .saturating_add(1)
        .min(length);
    content.bytes = content.bytes.slice(start..end.max(start));
    Ok(Some(content))
}

/// Returns the metadata of the current content of `logical_key`, or `None` when it is absent
/// or deleted.
pub(super) async fn head_versioned(
    inner: &dyn ObjectStore,
    operation: ObjectStoreOperation,
    logical_key: &str,
) -> ObjectStoreResult<Option<ObjectMetadata>> {
    let Some(newest) =
        read_newest_version(inner, operation, logical_key, VersionReadExtent::HeaderOnly).await?
    else {
        return Ok(None);
    };
    if newest.header.is_tombstone {
        return Ok(None);
    }
    Ok(Some(ObjectMetadata {
        key: logical_key.to_owned(),
        size: newest.payload_size,
        etag: newest.etag().to_string(),
        last_modified_unix_micros: newest.last_modified_unix_micros,
    }))
}

/// Returns `error` from the inner store as an error of the logical key, keeping its kind.
pub(super) fn inner_error(
    operation: ObjectStoreOperation,
    logical_key: &str,
    error: &ObjectStoreError,
) -> ObjectStoreError {
    ObjectStoreError::new(
        operation,
        logical_key,
        error.kind,
        format!("version object {:?}: {}", error.key, error.detail),
    )
}

/// Returns whether `content` came from `listed`; unique version bodies make physical ETags unique.
fn is_listed_object(listed: &ObjectMetadata, content: &ObjectContent) -> bool {
    // ETags are opaque and compare byte for byte; only surrounding quotes may differ.
    listed.size == content.object_size
        && listed.etag.trim_matches('"') == content.etag.trim_matches('"')
}

fn decode_header(
    operation: ObjectStoreOperation,
    logical_key: &str,
    physical_key: &str,
    content: &ObjectContent,
) -> ObjectStoreResult<VersionHeader> {
    VersionHeader::decode(&content.bytes, content.object_size).map_err(|error| {
        ObjectStoreError::new(
            operation,
            logical_key,
            ObjectStoreErrorKind::Corrupt,
            format!("version object {physical_key:?}: {error}"),
        )
    })
}
