// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Decoded history segment headers kept in memory, least recently used first out.

use std::collections::BTreeMap;
use std::sync::Arc;

use dexzd_history_segment::HistorySegmentLayout;
use prost::Message;

/// Bytes of decoded headers kept in memory; a header is about 40 bytes per superseded value.
pub(super) const HEADER_CACHE_MAX_BYTES: usize = 64 << 20;

/// Decoded headers by cache key text, bounded by their encoded header sizes.
pub(super) struct SegmentHeaderCache {
    max_bytes: usize,
    used_bytes: usize,
    next_use: u64,
    entries: BTreeMap<String, HeaderEntry>,
    keys_by_use: BTreeMap<u64, String>,
}

struct HeaderEntry {
    layout: Arc<HistorySegmentLayout>,
    last_use: u64,
    bytes: usize,
}

impl SegmentHeaderCache {
    pub(super) fn new(max_bytes: usize) -> Self {
        Self {
            max_bytes,
            used_bytes: 0,
            next_use: 0,
            entries: BTreeMap::new(),
            keys_by_use: BTreeMap::new(),
        }
    }

    /// Returns the header cached under `key` and marks it used.
    pub(super) fn get(&mut self, key: &str) -> Option<Arc<HistorySegmentLayout>> {
        let next_use = self.next_use;
        let entry = self.entries.get_mut(key)?;
        self.keys_by_use.remove(&entry.last_use);
        entry.last_use = next_use;
        self.keys_by_use.insert(next_use, key.to_owned());
        self.next_use += 1;
        Some(Arc::clone(&entry.layout))
    }

    /// Caches `layout`, evicting least recently used headers; skips one larger than the budget.
    pub(super) fn insert(&mut self, key: String, layout: Arc<HistorySegmentLayout>) {
        let bytes = layout.header().encoded_len().saturating_add(key.len());
        if bytes > self.max_bytes {
            return;
        }
        self.remove(&key);
        while self.used_bytes + bytes > self.max_bytes {
            let Some((_, oldest_key)) = self.keys_by_use.pop_first() else {
                break;
            };
            if let Some(evicted) = self.entries.remove(&oldest_key) {
                self.used_bytes -= evicted.bytes;
            }
        }
        let last_use = self.next_use;
        self.next_use += 1;
        self.keys_by_use.insert(last_use, key.clone());
        self.used_bytes += bytes;
        self.entries.insert(
            key,
            HeaderEntry {
                layout,
                last_use,
                bytes,
            },
        );
    }

    fn remove(&mut self, key: &str) {
        if let Some(entry) = self.entries.remove(key) {
            self.keys_by_use.remove(&entry.last_use);
            self.used_bytes -= entry.bytes;
        }
    }
}
