// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::ValueId;
use dexzd_protocol::dexzd::v1::{
    HistorySegmentBlock, HistorySegmentHeader, HistorySegmentValueEntry,
};
use prost::Message;
use xxhash_rust::xxh3::{xxh3_64, xxh3_128};

use crate::codec_error::{HistorySegmentBlockKind, HistorySegmentCodecError};
use crate::segment_content::HistorySegmentContent;
use crate::segment_layout::{
    CHECKPOINT_ENTRY_PREFIX_BYTES, ContentChecksum, FORMAT_VERSION, HISTORY_SEGMENT_PREFIX_BYTES,
    HISTORY_SEGMENT_TRAILER_BYTES, MAGIC, RECORD_ENTRY_PREFIX_BYTES, append_checkpoint_entry,
    append_record_entry,
};

/// A fixed level keeps the bytes one binary writes stable; checksums never depend on it.
const ZSTD_LEVEL: i32 = 3;
/// Field number of `HistorySegmentHeader.value_entries`.
const VALUE_ENTRIES_FIELD_NUMBER: u32 = 11;

/// One encoded history segment object.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EncodedHistorySegment {
    /// The whole object.
    pub bytes: Vec<u8>,
    /// The xxh3-128 of the canonical uncompressed content, big-endian. Equal content always has
    /// an equal checksum, even when another zstd version compressed it into other bytes.
    pub content_checksum: [u8; 16],
}

/// Encodes `content` as one history segment object.
///
/// The format sets no aggregate size limit, so any content apply can seal encodes; only a row of
/// 4 GiB or more, which RocksDB cannot store, or a header of 4 GiB or more is rejected.
///
/// Returns `InvalidContent` when the content breaks the order or range rules of
/// [`HistorySegmentContent`] or a field width, and `Compression` when zstd fails.
pub fn encode_history_segment(
    content: &HistorySegmentContent,
) -> Result<EncodedHistorySegment, HistorySegmentCodecError> {
    validate_content(content)?;
    let identity = content.identity;
    let mut checksum = ContentChecksum::new(&identity);
    let block_sizes = PlainBlockSizes::of(content);
    let mut records_plain = Vec::with_capacity(block_sizes.records);
    for record in &content.records {
        append_record_entry(&mut records_plain, record.seq, &record.row)?;
    }
    let mut checkpoints_plain = Vec::with_capacity(block_sizes.checkpoints);
    for checkpoint in &content.checkpoints {
        append_checkpoint_entry(
            &mut checkpoints_plain,
            checkpoint.seq,
            checkpoint.attempt,
            &checkpoint.row,
        )?;
    }
    checksum.add_records(content.records.len() as u64, &records_plain);
    checksum.add_checkpoints(content.checkpoints.len() as u64, &checkpoints_plain);
    checksum.begin_values(content.values.len() as u64);
    let records_stored = compress(HistorySegmentBlockKind::Records, &records_plain)?;
    let checkpoints_stored = compress(HistorySegmentBlockKind::Checkpoints, &checkpoints_plain)?;
    let records = stored_block(0, &records_stored, records_plain.len() as u64);
    let checkpoints = stored_block(
        records.length,
        &checkpoints_stored,
        checkpoints_plain.len() as u64,
    );
    let values_offset = checkpoints.offset + checkpoints.length;
    let mut values_stored = Vec::with_capacity(block_sizes.value_frame_bounds);
    let mut value_entries = Vec::with_capacity(content.values.len());
    let mut values_uncompressed = 0_u64;
    for value in &content.values {
        checksum.add_value(value.value_id, &value.row)?;
        let stored = compress(HistorySegmentBlockKind::Value(value.value_id), &value.row)?;
        let frame = stored_block(
            values_offset + values_stored.len() as u64,
            &stored,
            value.row.len() as u64,
        );
        value_entries.push(HistorySegmentValueEntry {
            run_seq: value.value_id.run_seq,
            index_in_command: value.value_id.index_in_command,
            offset: frame.offset,
            length: frame.length,
            uncompressed_length: frame.uncompressed_length,
            xxh3_64: frame.xxh3_64,
        });
        values_uncompressed += frame.uncompressed_length;
        values_stored.extend_from_slice(&stored);
    }
    let values = stored_block(values_offset, &values_stored, values_uncompressed);
    let content_checksum = checksum.finish();
    let header = HistorySegmentHeader {
        run_id: identity.run_id.as_bytes().to_vec(),
        first_seq: identity.first_seq,
        last_seq: identity.last_seq,
        first_event_id: identity.first_event_id,
        last_event_id: identity.last_event_id,
        records: Some(records),
        record_count: entry_count(content.records.len(), "records")?,
        checkpoints: Some(checkpoints),
        checkpoint_count: entry_count(content.checkpoints.len(), "checkpoints")?,
        values: Some(values),
        value_entries,
        content_checksum: content_checksum.to_vec(),
    };
    let header_bytes = header.encode_to_vec();
    let header_length = u32::try_from(header_bytes.len()).map_err(|_| {
        invalid_content(format!(
            "the header of {} bytes exceeds the 4 GiB format limit",
            header_bytes.len()
        ))
    })?;
    let mut bytes = Vec::with_capacity(
        HISTORY_SEGMENT_PREFIX_BYTES
            + header_bytes.len()
            + records_stored.len()
            + checkpoints_stored.len()
            + values_stored.len()
            + HISTORY_SEGMENT_TRAILER_BYTES,
    );
    bytes.extend_from_slice(MAGIC);
    bytes.extend_from_slice(&FORMAT_VERSION.to_le_bytes());
    bytes.extend_from_slice(&header_length.to_le_bytes());
    bytes.extend_from_slice(&xxh3_64(&header_bytes).to_le_bytes());
    bytes.extend_from_slice(&header_bytes);
    bytes.extend_from_slice(&records_stored);
    bytes.extend_from_slice(&checkpoints_stored);
    bytes.extend_from_slice(&values_stored);
    let trailer = xxh3_128(&bytes);
    bytes.extend_from_slice(&trailer.to_le_bytes());
    Ok(EncodedHistorySegment {
        bytes,
        content_checksum,
    })
}

/// Returns the most bytes that [`encode_history_segment`] allocates at once for `content`: its
/// block buffers, compressed frames, header, and the encoded object.
///
/// The bound excludes `content` itself and the zstd compression context. It is computed from
/// row lengths alone, without compressing, so a caller can reserve memory before encoding. The
/// encoded object is never larger than this bound.
///
/// Since server-zd 0.1.23.
pub fn max_history_segment_encoding_bytes(content: &HistorySegmentContent) -> u64 {
    let sizes = PlainBlockSizes::of(content);
    let compressed_blocks = compress_bound(sizes.records)
        .saturating_add(compress_bound(sizes.checkpoints))
        .saturating_add(sizes.value_frame_bounds);
    let header = max_header_bytes(content);
    let object = HISTORY_SEGMENT_PREFIX_BYTES
        .saturating_add(header)
        .saturating_add(compressed_blocks)
        .saturating_add(HISTORY_SEGMENT_TRAILER_BYTES);
    let value_entries = content
        .values
        .len()
        .saturating_mul(size_of::<HistorySegmentValueEntry>());
    let total = sizes
        .records
        .saturating_add(sizes.checkpoints)
        .saturating_add(compressed_blocks)
        .saturating_add(sizes.largest_value_frame_bound)
        .saturating_add(value_entries)
        .saturating_add(header)
        .saturating_add(object);
    u64::try_from(total).unwrap_or(u64::MAX)
}

/// Exact plain block lengths and compressed value frame bounds of one content.
struct PlainBlockSizes {
    records: usize,
    checkpoints: usize,
    value_frame_bounds: usize,
    largest_value_frame_bound: usize,
}

impl PlainBlockSizes {
    fn of(content: &HistorySegmentContent) -> Self {
        let records = content
            .records
            .iter()
            .map(|record| RECORD_ENTRY_PREFIX_BYTES.saturating_add(record.row.len()))
            .fold(0, usize::saturating_add);
        let checkpoints = content
            .checkpoints
            .iter()
            .map(|checkpoint| CHECKPOINT_ENTRY_PREFIX_BYTES.saturating_add(checkpoint.row.len()))
            .fold(0, usize::saturating_add);
        let frame_bounds = content
            .values
            .iter()
            .map(|value| compress_bound(value.row.len()));
        Self {
            records,
            checkpoints,
            value_frame_bounds: frame_bounds.clone().fold(0, usize::saturating_add),
            largest_value_frame_bound: frame_bounds.max().unwrap_or(0),
        }
    }
}

/// Returns the most bytes zstd writes for `plain_length` bytes; an empty block stores nothing.
fn compress_bound(plain_length: usize) -> usize {
    if plain_length == 0 {
        0
    } else {
        zstd::zstd_safe::compress_bound(plain_length)
    }
}

/// Returns an upper bound of the encoded header: every varint field at its widest.
fn max_header_bytes(content: &HistorySegmentContent) -> usize {
    let widest_block = HistorySegmentBlock {
        offset: u64::MAX,
        length: u64::MAX,
        uncompressed_length: u64::MAX,
        xxh3_64: u64::MAX,
    };
    let identity = content.identity;
    let fixed = HistorySegmentHeader {
        run_id: identity.run_id.as_bytes().to_vec(),
        first_seq: identity.first_seq,
        last_seq: identity.last_seq,
        first_event_id: identity.first_event_id,
        last_event_id: identity.last_event_id,
        records: Some(widest_block),
        record_count: u32::MAX,
        checkpoints: Some(widest_block),
        checkpoint_count: u32::MAX,
        values: Some(widest_block),
        value_entries: Vec::new(),
        content_checksum: vec![0; 16],
    }
    .encoded_len();
    let widest_entry = HistorySegmentValueEntry {
        run_seq: u64::MAX,
        index_in_command: u32::MAX,
        offset: u64::MAX,
        length: u64::MAX,
        uncompressed_length: u64::MAX,
        xxh3_64: u64::MAX,
    }
    .encoded_len();
    let entry = prost::encoding::key_len(VALUE_ENTRIES_FIELD_NUMBER)
        + prost::length_delimiter_len(widest_entry)
        + widest_entry;
    fixed.saturating_add(content.values.len().saturating_mul(entry))
}

fn validate_content(content: &HistorySegmentContent) -> Result<(), HistorySegmentCodecError> {
    let identity = content.identity;
    if identity.first_seq == 0 || identity.first_seq > identity.last_seq {
        return Err(invalid_content(format!(
            "seq range {}..={} is empty or starts at zero",
            identity.first_seq, identity.last_seq
        )));
    }
    let seq_range = identity.first_seq..=identity.last_seq;
    let mut previous_seq = None;
    for record in &content.records {
        if !seq_range.contains(&record.seq) || previous_seq.is_some_and(|seq| seq >= record.seq) {
            return Err(invalid_content(format!(
                "record seq {} is outside the range or out of order",
                record.seq
            )));
        }
        previous_seq = Some(record.seq);
    }
    let mut previous_checkpoint = None;
    for checkpoint in &content.checkpoints {
        let position = (checkpoint.seq, checkpoint.attempt);
        if !seq_range.contains(&checkpoint.seq)
            || previous_checkpoint.is_some_and(|previous| previous >= position)
        {
            return Err(invalid_content(format!(
                "checkpoint ({}, {}) is outside the range or out of order",
                checkpoint.seq, checkpoint.attempt
            )));
        }
        previous_checkpoint = Some(position);
    }
    let mut previous_value: Option<ValueId> = None;
    for value in &content.values {
        if previous_value.is_some_and(|previous| previous >= value.value_id) {
            return Err(invalid_content(
                "values are not in ascending value ID order",
            ));
        }
        previous_value = Some(value.value_id);
    }
    Ok(())
}

fn compress(
    block: HistorySegmentBlockKind,
    plain: &[u8],
) -> Result<Vec<u8>, HistorySegmentCodecError> {
    if plain.is_empty() {
        return Ok(Vec::new());
    }
    zstd::bulk::compress(plain, ZSTD_LEVEL).map_err(|error| HistorySegmentCodecError::Compression {
        block,
        detail: error.to_string(),
    })
}

/// Describes a stored frame; an empty frame has a zero checksum.
fn stored_block(offset: u64, stored: &[u8], uncompressed_length: u64) -> HistorySegmentBlock {
    HistorySegmentBlock {
        offset,
        length: stored.len() as u64,
        uncompressed_length,
        xxh3_64: if stored.is_empty() {
            0
        } else {
            xxh3_64(stored)
        },
    }
}

fn entry_count(count: usize, name: &str) -> Result<u32, HistorySegmentCodecError> {
    u32::try_from(count).map_err(|_| invalid_content(format!("too many {name}: {count}")))
}

fn invalid_content(detail: impl Into<String>) -> HistorySegmentCodecError {
    HistorySegmentCodecError::InvalidContent {
        detail: detail.into(),
    }
}
