// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::path::{Path, PathBuf};

use super::BlobCacheError;

pub(crate) const MAX_BLOB_ID_BYTES: usize = 1 << 20;
const DEFAULT_FREQUENCY_COUNTERS: i64 = 10_000;

#[derive(Clone, Debug)]
/// Configures one persistent [`crate::BlobCache`].
///
/// The directory contains cache-owned files and must not be shared with unrelated data. The byte
/// limit bounds admitted payloads. Frequency counters tune admission accuracy; pass `0` to use the
/// default of 10,000.
pub struct BlobCacheConfig {
    directory: PathBuf,
    max_bytes: i64,
    frequency_counters: usize,
}

impl BlobCacheConfig {
    /// Validates and creates a cache configuration.
    ///
    /// # Arguments
    ///
    /// * `directory` - Cache-owned filesystem directory.
    /// * `max_bytes` - Positive maximum number of payload bytes admitted to the cache.
    /// * `frequency_counters` - Admission-policy counters, or `0` for the 10,000 default.
    ///
    /// # Errors
    ///
    /// Returns [`BlobCacheError::InvalidConfig`] when the directory is empty, the byte limit is not
    /// positive, the counter count is negative, or the count cannot fit in [`usize`].
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_blob_cache::BlobCacheConfig;
    ///
    /// let config = BlobCacheConfig::new("/tmp/dex-blobs", 64 * 1024 * 1024, 0)?;
    /// assert_eq!(config.max_bytes(), 64 * 1024 * 1024);
    /// assert_eq!(config.frequency_counters(), 10_000);
    /// # Ok::<(), dex_blob_cache::BlobCacheError>(())
    /// ```
    pub fn new(
        directory: impl Into<PathBuf>,
        max_bytes: i64,
        frequency_counters: i64,
    ) -> Result<Self, BlobCacheError> {
        let directory = directory.into();
        if directory.as_os_str().is_empty() {
            return Err(BlobCacheError::InvalidConfig(
                "directory must not be empty".to_owned(),
            ));
        }
        if max_bytes <= 0 {
            return Err(BlobCacheError::InvalidConfig(
                "max_bytes must be positive".to_owned(),
            ));
        }
        if frequency_counters < 0 {
            return Err(BlobCacheError::InvalidConfig(
                "frequency_counters must not be negative".to_owned(),
            ));
        }
        let frequency_counters = if frequency_counters == 0 {
            DEFAULT_FREQUENCY_COUNTERS
        } else {
            frequency_counters
        };
        let frequency_counters = usize::try_from(frequency_counters).map_err(|_| {
            BlobCacheError::InvalidConfig("frequency_counters overflows usize".to_owned())
        })?;

        Ok(Self {
            directory,
            max_bytes,
            frequency_counters,
        })
    }

    /// Returns the cache-owned filesystem directory.
    pub fn directory(&self) -> &Path {
        &self.directory
    }

    /// Returns the maximum admitted payload bytes.
    pub fn max_bytes(&self) -> i64 {
        self.max_bytes
    }

    /// Returns the admission-policy frequency counter count.
    pub fn frequency_counters(&self) -> usize {
        self.frequency_counters
    }
}

pub(crate) fn validate_blob_id(blob_id: &str) -> Result<(), BlobCacheError> {
    if blob_id.is_empty() {
        return Err(BlobCacheError::InvalidBlob(
            "blob ID must not be empty".to_owned(),
        ));
    }
    if blob_id.len() > MAX_BLOB_ID_BYTES {
        return Err(BlobCacheError::InvalidBlob(format!(
            "blob ID exceeds {MAX_BLOB_ID_BYTES} bytes"
        )));
    }
    Ok(())
}
