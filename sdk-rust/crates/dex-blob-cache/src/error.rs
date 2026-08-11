// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::error::Error;
use std::fmt::{Display, Formatter};
use std::io;

#[derive(Debug)]
/// Reports configuration, lifecycle, storage, or policy failures from [`crate::BlobCache`].
pub enum BlobCacheError {
    /// The operation requires an open cache, but [`crate::BlobCache::close`] already completed.
    Closed,
    /// Cache construction received an invalid directory, byte limit, or frequency counter count.
    InvalidConfig(String),
    /// A blob ID is empty, too large, or otherwise invalid.
    InvalidBlob(String),
    /// Existing content for a blob ID differs from the new immutable payload.
    ContentMismatch(String),
    /// A committed cache entry is malformed or fails integrity validation.
    Corrupt(String),
    /// On-disk state and the in-memory eviction policy could not be reconciled.
    Reconciliation(String),
    /// The admission or eviction policy failed.
    Policy(String),
    /// A filesystem operation failed.
    Io {
        /// Describes the filesystem operation that failed.
        operation: String,
        /// Preserves the underlying I/O error.
        source: io::Error,
    },
}

impl BlobCacheError {
    pub(crate) fn io(operation: impl Into<String>, source: io::Error) -> Self {
        Self::Io {
            operation: operation.into(),
            source,
        }
    }

    pub(crate) fn is_missing_or_corrupt(&self) -> bool {
        matches!(self, Self::Corrupt(_))
            || matches!(self, Self::Io { source, .. } if source.kind() == io::ErrorKind::NotFound)
    }
}

impl Display for BlobCacheError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Closed => formatter.write_str("blob cache is closed"),
            Self::InvalidConfig(message) => {
                write!(formatter, "invalid blob cache configuration: {message}")
            }
            Self::InvalidBlob(message) => write!(formatter, "invalid blob: {message}"),
            Self::ContentMismatch(blob_id) => {
                write!(formatter, "blob ID content mismatch: {blob_id:?}")
            }
            Self::Corrupt(message) => write!(formatter, "corrupt blob cache entry: {message}"),
            Self::Reconciliation(message) => {
                write!(formatter, "blob cache reconciliation failed: {message}")
            }
            Self::Policy(message) => write!(formatter, "blob cache policy failed: {message}"),
            Self::Io { operation, source } => write!(formatter, "{operation}: {source}"),
        }
    }
}

impl Error for BlobCacheError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Io { source, .. } => Some(source),
            _ => None,
        }
    }
}
