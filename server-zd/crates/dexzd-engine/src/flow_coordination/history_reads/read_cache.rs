// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The disposable disk read cache of immutable history data, on `dex-blob-cache`.

use std::fmt::Write as _;
use std::path::PathBuf;
use std::sync::Arc;

use bytes::Bytes;
use dex_blob_cache::{BlobCache, BlobCacheConfig, BlobCacheError};
use tokio::sync::OnceCell;

use crate::blocking_work::BlockingWorkPlacement;

/// Value references of hard-delete values start with this; their content is never cached.
const HARD_DELETE_REFERENCE_PREFIX: &str = "hd1|";

/// Key of one immutable entry of the read cache.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct HistoryReadCacheKey(String);

/// A part of a history segment object that the cache keeps.
#[derive(Debug, Clone, Copy)]
pub(super) enum SegmentPart {
    /// The prefix and header bytes.
    Header,
    /// The stored records block.
    Records,
    /// The stored checkpoints block.
    Checkpoints,
}

impl HistoryReadCacheKey {
    /// Returns the key of an immutable entry named by `text`, such as a value reference.
    ///
    /// Returns `None` for a hard-delete value reference, whose value is never cached.
    pub(crate) fn from_text(text: String) -> Option<Self> {
        (!text.starts_with(HARD_DELETE_REFERENCE_PREFIX)).then_some(Self(text))
    }

    /// Returns the key of an offloaded value: its value reference text and the content checksum
    /// of the segment object that holds it, or `None` for a hard-delete value reference.
    ///
    /// The checksum keeps a value of a restored older timeline, which reuses value IDs, from
    /// matching.
    pub(crate) fn offloaded_value(reference_text: &str, content_checksum: &[u8]) -> Option<Self> {
        let mut text = format!("{reference_text}|");
        for byte in content_checksum {
            write!(text, "{byte:02x}").expect("writing to a String never fails");
        }
        Self::from_text(text)
    }

    /// Returns the key of a part of the segment object at `object_key` with `content_checksum`.
    ///
    /// The checksum keeps an object that a restored older timeline replaced from matching.
    pub(super) fn segment_part(
        object_key: &str,
        content_checksum: &[u8; 16],
        part: SegmentPart,
    ) -> Self {
        let mut text = format!("{object_key}|");
        for byte in content_checksum {
            write!(text, "{byte:02x}").expect("writing to a String never fails");
        }
        text.push_str(match part {
            SegmentPart::Header => "|header",
            SegmentPart::Records => "|records",
            SegmentPart::Checkpoints => "|checkpoints",
        });
        Self::from_text(text).expect("object keys start with history/, never with hd1|")
    }

    pub(crate) fn text(&self) -> &str {
        &self.0
    }
}

/// The disk read cache, or nothing when it is disabled or could not open.
pub(super) struct HistoryReadCache {
    settings: Option<(PathBuf, i64)>,
    blocking_work: BlockingWorkPlacement,
    cache: OnceCell<Option<Arc<BlobCache>>>,
}

impl HistoryReadCache {
    /// A cache in `directory` of at most `max_bytes`; no directory or zero bytes disables it.
    pub(super) fn new(
        directory: Option<PathBuf>,
        max_bytes: u64,
        blocking_work: BlockingWorkPlacement,
    ) -> Self {
        let settings = directory
            .filter(|_| max_bytes > 0)
            .map(|directory| (directory, i64::try_from(max_bytes).unwrap_or(i64::MAX)));
        Self {
            settings,
            blocking_work,
            cache: OnceCell::new(),
        }
    }

    /// Returns the cached bytes of `key`, or `None` on a miss or any cache failure.
    pub(super) async fn get(&self, key: &HistoryReadCacheKey) -> Option<Vec<u8>> {
        let cache = self.opened().await?;
        let blob_id = key.text().to_owned();
        let result = self.blocking_work.run(move || cache.get(&blob_id)).await;
        match result {
            Ok(Ok(bytes)) => bytes,
            Ok(Err(error)) => {
                tracing::warn!(%error, key = key.text(), "reading the history read cache failed");
                None
            }
            Err(failure) => {
                tracing::warn!(%failure, key = key.text(), "reading the history read cache failed");
                None
            }
        }
    }

    /// Stores `bytes` under `key`; the cache may decline them, and a failure is only logged.
    pub(super) async fn put(&self, key: &HistoryReadCacheKey, bytes: Bytes) {
        let Some(cache) = self.opened().await else {
            return;
        };
        let blob_id = key.text().to_owned();
        let result = self
            .blocking_work
            .run(move || cache.put(&blob_id, &bytes))
            .await;
        log_write_failure("storing", key, result);
    }

    /// Removes the entry of `key`, as after it failed to decode.
    pub(super) async fn remove(&self, key: &HistoryReadCacheKey) {
        let Some(cache) = self.opened().await else {
            return;
        };
        let blob_id = key.text().to_owned();
        let result = self
            .blocking_work
            .run(move || cache.delete(&blob_id).map(|()| true))
            .await;
        log_write_failure("removing", key, result);
    }

    async fn opened(&self) -> Option<Arc<BlobCache>> {
        let (directory, max_bytes) = self.settings.clone()?;
        self.cache
            .get_or_init(|| async move {
                let opened = self
                    .blocking_work
                    .run(move || {
                        BlobCacheConfig::new(&directory, max_bytes, 0)
                            .and_then(BlobCache::open)
                            .map_err(|error| (directory, error))
                    })
                    .await;
                match opened {
                    Ok(Ok(cache)) => Some(Arc::new(cache)),
                    Ok(Err((directory, error))) => {
                        tracing::warn!(
                            %error,
                            directory = %directory.display(),
                            "the history read cache cannot open; reads go to object storage"
                        );
                        None
                    }
                    Err(failure) => {
                        tracing::warn!(%failure, "the history read cache cannot open");
                        None
                    }
                }
            })
            .await
            .clone()
    }
}

fn log_write_failure(
    action: &str,
    key: &HistoryReadCacheKey,
    result: Result<Result<bool, BlobCacheError>, crate::blocking_work::BlockingWorkFailure>,
) {
    match result {
        Ok(Ok(_)) => {}
        Ok(Err(error)) => {
            tracing::warn!(%error, action, key = key.text(), "writing the history read cache failed");
        }
        Err(failure) => {
            tracing::warn!(%failure, action, key = key.text(), "writing the history read cache failed");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{HistoryReadCacheKey, SegmentPart};

    #[test]
    fn hard_delete_value_references_are_never_cache_keys() {
        let hard_delete = "hd1|0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6|42".to_owned();
        assert_eq!(HistoryReadCacheKey::from_text(hard_delete), None);
        let reference = "0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6/7/0".to_owned();
        assert_eq!(
            HistoryReadCacheKey::from_text(reference.clone()).map(|key| key.text().to_owned()),
            Some(reference)
        );
    }

    #[test]
    fn offloaded_value_keys_name_the_reference_and_the_segment_checksum() {
        let reference = "0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6/7/0";
        let key = HistoryReadCacheKey::offloaded_value(reference, &[0xab; 16])
            .expect("a value reference is a cache key");
        assert_eq!(key.text(), format!("{reference}|{}", "ab".repeat(16)));
        assert_ne!(
            Some(key),
            HistoryReadCacheKey::offloaded_value(reference, &[0xac; 16]),
            "a restored timeline's object never matches"
        );
        assert_eq!(
            HistoryReadCacheKey::offloaded_value("hd1|0192f3a4|42", &[0xab; 16]),
            None
        );
    }

    #[test]
    fn segment_part_keys_name_the_object_checksum_and_part() {
        let key =
            HistoryReadCacheKey::segment_part("history/a/b", &[0xab; 16], SegmentPart::Records);
        assert_eq!(
            key.text(),
            format!("history/a/b|{}|records", "ab".repeat(16))
        );
        assert_ne!(
            key,
            HistoryReadCacheKey::segment_part("history/a/b", &[0xac; 16], SegmentPart::Records)
        );
    }
}
