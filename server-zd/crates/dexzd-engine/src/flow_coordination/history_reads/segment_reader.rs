// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Fetches the records of an uploaded history segment through the header and read caches.

use std::ops::Range;
use std::sync::{Arc, Mutex};

use bytes::Bytes;
use dexzd_history_segment::{
    HistorySegmentCodecError, HistorySegmentLayout, HistorySegmentRecordRow,
    decode_history_segment_header, decode_records_block, history_segment_object_key,
};
use dexzd_keyspace::{FlowHash, RunId, TenantId};
use dexzd_metrics::{HistoryReadKind, HistoryReadSource, NodeMetrics};
use dexzd_object_store::{ByteRange, ObjectContent, ObjectStore, ObjectStoreError};
use dexzd_protocol::dexzd::v1::{HistoryRecord, HistorySegmentRecord};
use prost::Message;
use tokio::sync::Semaphore;

use super::HistoryReadsConfig;
use super::header_cache::{HEADER_CACHE_MAX_BYTES, SegmentHeaderCache};
use super::read_cache::{HistoryReadCache, HistoryReadCacheKey, SegmentPart};
use crate::blocking_work::BlockingWorkPlacement;

/// Bytes the first read of an object takes; most headers fit, so one request usually serves.
const HEADER_READ_BYTES: u64 = 64 * 1024;

/// Why an uploaded history segment could not be read.
#[derive(Debug, thiserror::Error)]
pub(crate) enum HistorySegmentReadError {
    /// An object storage call failed.
    #[error("reading history segment object {key} failed: {source}")]
    ObjectStore {
        /// The object key.
        key: String,
        /// The failed call.
        source: ObjectStoreError,
    },
    /// The object differs from its catalog row or does not decode.
    #[error("history segment object {key} does not match its catalog row: {detail}")]
    Mismatch {
        /// The object key.
        key: String,
        /// What differs.
        detail: String,
    },
}

/// Names one uploaded segment of a run by its catalog row.
#[derive(Debug, Clone, Copy)]
pub(crate) struct SegmentLocation<'segment> {
    pub(crate) tenant_id: TenantId,
    pub(crate) flow_hash: FlowHash,
    pub(crate) run_id: RunId,
    pub(crate) segment: &'segment HistorySegmentRecord,
}

/// Reads uploaded history segments for one node, with a bounded number of object storage
/// reads at once.
pub struct HistorySegmentReader {
    object_store: Arc<dyn ObjectStore>,
    object_read_slots: Semaphore,
    headers: Mutex<SegmentHeaderCache>,
    read_cache: HistoryReadCache,
    metrics: Arc<NodeMetrics>,
    max_page_rounds: usize,
}

impl HistorySegmentReader {
    /// Creates the node's reader of the history segments in `object_store`.
    ///
    /// Create one per process: the disk read cache owns its directory.
    pub fn new(
        config: &HistoryReadsConfig,
        object_store: Arc<dyn ObjectStore>,
        metrics: Arc<NodeMetrics>,
        blocking_work: BlockingWorkPlacement,
    ) -> Self {
        Self {
            object_store,
            object_read_slots: Semaphore::new(
                config
                    .object_read_concurrency
                    .clamp(1, Semaphore::MAX_PERMITS),
            ),
            headers: Mutex::new(SegmentHeaderCache::new(HEADER_CACHE_MAX_BYTES)),
            read_cache: HistoryReadCache::new(
                config.read_cache_directory.clone(),
                config.read_cache_max_bytes,
                blocking_work,
            ),
            metrics,
            max_page_rounds: config.max_page_rounds.max(1),
        }
    }

    /// Returns the most committed view rounds of one history page.
    pub(crate) fn max_page_rounds(&self) -> usize {
        self.max_page_rounds
    }

    /// Returns the records of the segment, in ascending seq order, for a history page.
    ///
    /// Returns `None` when the object does not exist, as after the run was purged. Returns an
    /// error when object storage fails or the object differs from the catalog row.
    pub(crate) async fn read_records(
        &self,
        location: SegmentLocation<'_>,
    ) -> Result<Option<Arc<[HistoryRecord]>>, HistorySegmentReadError> {
        let segment = location.segment;
        let key = history_segment_object_key(
            location.tenant_id,
            location.flow_hash,
            location.run_id,
            segment.first_seq,
            segment.last_seq,
        );
        let mismatch = |detail: String| HistorySegmentReadError::Mismatch {
            key: key.clone(),
            detail,
        };
        let checksum: [u8; 16] = segment
            .content_checksum
            .as_slice()
            .try_into()
            .map_err(|_| mismatch("the catalog checksum is not 16 bytes".to_owned()))?;
        let Some(layout) = self
            .header(&key, &checksum, segment.object_size_bytes)
            .await?
        else {
            return Ok(None);
        };
        let rows = match layout.records_range() {
            None => Vec::new(),
            Some(range) => match self.records_block(&key, &checksum, &layout, range).await? {
                Some(rows) => rows,
                None => return Ok(None),
            },
        };
        let records = rows
            .into_iter()
            .map(|row| {
                let record = HistoryRecord::decode(row.row.as_slice()).map_err(|error| {
                    mismatch(format!("record {} does not decode: {error}", row.seq))
                })?;
                if record.seq == row.seq {
                    Ok(record)
                } else {
                    Err(mismatch(format!(
                        "record entry {} holds seq {}",
                        row.seq, record.seq
                    )))
                }
            })
            .collect::<Result<Vec<_>, _>>()?;
        Ok(Some(records.into()))
    }

    /// Returns the decoded header of the object, from memory, the disk cache, or object
    /// storage, or `None` when the object does not exist.
    async fn header(
        &self,
        key: &str,
        checksum: &[u8; 16],
        object_size: u64,
    ) -> Result<Option<Arc<HistorySegmentLayout>>, HistorySegmentReadError> {
        let cache_key = HistoryReadCacheKey::segment_part(key, checksum, SegmentPart::Header);
        if let Some(layout) = self.cached_header(&cache_key) {
            return Ok(Some(layout));
        }
        if let Some(stored) = self.read_cache.get(&cache_key).await {
            match checked_header(&stored, object_size, checksum) {
                Ok(layout) => {
                    self.record_read(HistoryReadSource::Cache);
                    return Ok(Some(self.remember_header(&cache_key, layout)));
                }
                Err(error) => {
                    tracing::warn!(%error, key, "a cached history segment header is damaged");
                    self.read_cache.remove(&cache_key).await;
                }
            }
        }
        let mut read_bytes = HEADER_READ_BYTES.min(object_size);
        loop {
            let Some(content) = self.read_range(key, 0..read_bytes).await? else {
                return Ok(None);
            };
            if content.object_size != object_size {
                return Err(HistorySegmentReadError::Mismatch {
                    key: key.to_owned(),
                    detail: format!(
                        "the object has {} bytes and the catalog row {object_size}",
                        content.object_size
                    ),
                });
            }
            match checked_header(&content.bytes, object_size, checksum) {
                Ok(layout) => {
                    self.record_read(HistoryReadSource::ObjectStore);
                    self.read_cache.put(&cache_key, content.bytes).await;
                    return Ok(Some(self.remember_header(&cache_key, layout)));
                }
                Err(HeaderCheckError::Codec(HistorySegmentCodecError::TruncatedHeader {
                    required,
                    ..
                })) if u64::try_from(required).is_ok_and(|required| required > read_bytes) => {
                    read_bytes = u64::try_from(required).expect("checked above");
                }
                Err(error) => {
                    return Err(HistorySegmentReadError::Mismatch {
                        key: key.to_owned(),
                        detail: error.to_string(),
                    });
                }
            }
        }
    }

    /// Returns the rows of the records block at `range`, or `None` when the object is gone.
    async fn records_block(
        &self,
        key: &str,
        checksum: &[u8; 16],
        layout: &HistorySegmentLayout,
        range: Range<u64>,
    ) -> Result<Option<Vec<HistorySegmentRecordRow>>, HistorySegmentReadError> {
        let cache_key = HistoryReadCacheKey::segment_part(key, checksum, SegmentPart::Records);
        if let Some(stored) = self.read_cache.get(&cache_key).await {
            match decode_records_block(layout, &stored) {
                Ok(rows) => {
                    self.record_read(HistoryReadSource::Cache);
                    return Ok(Some(rows));
                }
                Err(error) => {
                    tracing::warn!(%error, key, "a cached history records block is damaged");
                    self.read_cache.remove(&cache_key).await;
                }
            }
        }
        let expected_bytes = range.end - range.start;
        let Some(content) = self.read_range(key, range).await? else {
            return Ok(None);
        };
        let mismatch = |detail: String| HistorySegmentReadError::Mismatch {
            key: key.to_owned(),
            detail,
        };
        if u64::try_from(content.bytes.len()).ok() != Some(expected_bytes) {
            return Err(mismatch(format!(
                "the records block read returned {} of {expected_bytes} bytes",
                content.bytes.len()
            )));
        }
        let rows = decode_records_block(layout, &content.bytes)
            .map_err(|error| mismatch(error.to_string()))?;
        self.record_read(HistoryReadSource::ObjectStore);
        self.read_cache.put(&cache_key, content.bytes).await;
        Ok(Some(rows))
    }

    /// Reads `range` of the object while holding one object read slot.
    async fn read_range(
        &self,
        key: &str,
        range: Range<u64>,
    ) -> Result<Option<ObjectContent>, HistorySegmentReadError> {
        if range.is_empty() {
            return Ok(Some(ObjectContent {
                bytes: Bytes::new(),
                etag: String::new(),
                object_size: 0,
            }));
        }
        let _slot = self
            .object_read_slots
            .acquire()
            .await
            .expect("the object read slots are never closed");
        self.object_store
            .get_range(
                key,
                ByteRange {
                    start: range.start,
                    end_inclusive: range.end - 1,
                },
            )
            .await
            .map_err(|source| HistorySegmentReadError::ObjectStore {
                key: key.to_owned(),
                source,
            })
    }

    fn cached_header(&self, cache_key: &HistoryReadCacheKey) -> Option<Arc<HistorySegmentLayout>> {
        self.headers
            .lock()
            .expect("the header cache lock is poisoned only if a holder panicked")
            .get(cache_key.text())
    }

    fn remember_header(
        &self,
        cache_key: &HistoryReadCacheKey,
        layout: HistorySegmentLayout,
    ) -> Arc<HistorySegmentLayout> {
        let layout = Arc::new(layout);
        self.headers
            .lock()
            .expect("the header cache lock is poisoned only if a holder panicked")
            .insert(cache_key.text().to_owned(), Arc::clone(&layout));
        layout
    }

    /// Counts a history page that only local rows served.
    pub(crate) fn record_local_page(&self) {
        self.record_read(HistoryReadSource::Local);
    }

    fn record_read(&self, source: HistoryReadSource) {
        self.metrics
            .history_reads
            .record(HistoryReadKind::Page, source);
    }
}

/// Why header bytes do not describe the catalog's segment.
#[derive(Debug, thiserror::Error)]
enum HeaderCheckError {
    #[error(transparent)]
    Codec(HistorySegmentCodecError),
    #[error("the object's content checksum differs from the catalog row")]
    ChecksumMismatch,
}

fn checked_header(
    object_start: &[u8],
    object_size: u64,
    checksum: &[u8; 16],
) -> Result<HistorySegmentLayout, HeaderCheckError> {
    let layout = decode_history_segment_header(object_start, object_size)
        .map_err(HeaderCheckError::Codec)?;
    if layout.content_checksum() == *checksum {
        Ok(layout)
    } else {
        Err(HeaderCheckError::ChecksumMismatch)
    }
}
