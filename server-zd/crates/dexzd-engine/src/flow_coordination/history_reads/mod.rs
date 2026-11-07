// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Cached reads of uploaded history segments; see the crate README.

mod header_cache;
mod read_cache;
mod segment_reader;

use std::path::PathBuf;

pub use segment_reader::HistorySegmentReader;
pub(crate) use segment_reader::{HistorySegmentReadError, SegmentLocation, SegmentReadPurpose};

/// Configuration section of history segment reads.
#[derive(Debug, Clone)]
pub struct HistoryReadsConfig {
    /// Directory of the disk read cache, which the cache owns, or `None` for no disk cache.
    /// Default `None`; the server uses the sibling `<localStorage.directory>-history-read-cache`.
    pub read_cache_directory: Option<PathBuf>,
    /// Most bytes of segment parts the disk read cache keeps. Default 1 GiB
    /// (`history.readCacheMaxBytes`); zero disables the disk cache.
    pub read_cache_max_bytes: u64,
    /// Most object storage reads of history segments that the node runs at once. Default 32
    /// (`history.objectReadConcurrency`); at least 1.
    pub object_read_concurrency: usize,
    /// Most committed view rounds of one GetHistoryEvents page. Default 8, not configurable;
    /// at least 1. The last round returns a shorter page with a next page token instead of
    /// fetching more segments, so every read makes progress with bounded work.
    pub max_page_rounds: usize,
}

impl Default for HistoryReadsConfig {
    fn default() -> Self {
        Self {
            read_cache_directory: None,
            read_cache_max_bytes: 1 << 30,
            object_read_concurrency: 32,
            max_page_rounds: 8,
        }
    }
}
