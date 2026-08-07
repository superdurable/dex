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
pub struct BlobCacheConfig {
    directory: PathBuf,
    max_bytes: i64,
    frequency_counters: usize,
}

impl BlobCacheConfig {
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

    pub fn directory(&self) -> &Path {
        &self.directory
    }

    pub fn max_bytes(&self) -> i64 {
        self.max_bytes
    }

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
