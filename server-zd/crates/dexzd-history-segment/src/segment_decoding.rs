// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::io::Read;
use std::ops::RangeInclusive;

use dexzd_protocol::dexzd::v1::{
    HistorySegmentBlock, HistorySegmentHeader, HistorySegmentValueEntry,
};
use prost::Message;
use xxhash_rust::xxh3::{xxh3_64, xxh3_128};

use crate::codec_error::{HistorySegmentBlockKind, HistorySegmentCodecError};
use crate::segment_content::{
    HistorySegmentCheckpointRow, HistorySegmentContent, HistorySegmentRecordRow,
    HistorySegmentValueRow,
};
use crate::segment_layout::{
    CHECKPOINT_ENTRY_PREFIX_BYTES, ContentChecksum, FORMAT_VERSION, HISTORY_SEGMENT_PREFIX_BYTES,
    HISTORY_SEGMENT_TRAILER_BYTES, HistorySegmentLayout, MAGIC, RECORD_ENTRY_PREFIX_BYTES,
    entry_value_id, invalid_header,
};

/// Decodes the prefix and header from the first bytes of a history segment object.
///
/// `object_start` holds bytes from offset 0 of the object; `object_size` is the size of the
/// whole object, as a ranged read reports it. A ranged reader may read any amount first: when
/// that amount is short, `TruncatedHeader` names the bytes to read. The header must match its
/// checksum in the prefix and describe exactly `object_size` bytes, so a truncated or changed
/// object fails here.
///
/// Returns the validated layout, which locates every block and value of the object.
pub fn decode_history_segment_header(
    object_start: &[u8],
    object_size: u64,
) -> Result<HistorySegmentLayout, HistorySegmentCodecError> {
    let prefix = object_start.get(..HISTORY_SEGMENT_PREFIX_BYTES).ok_or(
        HistorySegmentCodecError::TruncatedHeader {
            required: HISTORY_SEGMENT_PREFIX_BYTES,
            available: object_start.len(),
        },
    )?;
    if &prefix[..8] != MAGIC {
        return Err(HistorySegmentCodecError::BadMagic);
    }
    let version = u16::from_le_bytes([prefix[8], prefix[9]]);
    if version != FORMAT_VERSION {
        return Err(HistorySegmentCodecError::UnsupportedFormatVersion { version });
    }
    let header_length = u32::from_le_bytes(
        prefix[10..14]
            .try_into()
            .expect("the prefix holds a four-byte header length"),
    );
    let header_checksum = u64::from_le_bytes(
        prefix[14..22]
            .try_into()
            .expect("the prefix holds an eight-byte header checksum"),
    );
    let required = usize::try_from(header_length)
        .ok()
        .and_then(|header_length| HISTORY_SEGMENT_PREFIX_BYTES.checked_add(header_length))
        .ok_or_else(|| invalid_header("the header length does not fit in memory"))?;
    if u64::try_from(required).map_or(true, |required| required > object_size) {
        return Err(HistorySegmentCodecError::ObjectSizeMismatch {
            expected: u64::try_from(required).unwrap_or(u64::MAX),
            actual: object_size,
        });
    }
    let header_bytes = object_start
        .get(HISTORY_SEGMENT_PREFIX_BYTES..required)
        .ok_or(HistorySegmentCodecError::TruncatedHeader {
            required,
            available: object_start.len(),
        })?;
    if xxh3_64(header_bytes) != header_checksum {
        return Err(HistorySegmentCodecError::HeaderChecksumMismatch);
    }
    let header = HistorySegmentHeader::decode(header_bytes)
        .map_err(|error| invalid_header(format!("the header does not decode: {error}")))?;
    HistorySegmentLayout::new(header, header_bytes.len(), object_size)
}

/// Decodes the records block from its stored bytes, which a ranged read of
/// [`HistorySegmentLayout::records_range`] returns; an empty block takes an empty slice.
///
/// Returns the records in ascending seq order, or an error when the bytes differ from their
/// checksum, do not decompress to the recorded length, or hold malformed entries.
pub fn decode_records_block(
    layout: &HistorySegmentLayout,
    stored: &[u8],
) -> Result<Vec<HistorySegmentRecordRow>, HistorySegmentCodecError> {
    let block = HistorySegmentBlockKind::Records;
    let plain = decompress_block(block, layout.records_block(), stored)?;
    parse_record_entries(layout, &plain)
}

/// Decodes the checkpoints block from its stored bytes, which a ranged read of
/// [`HistorySegmentLayout::checkpoints_range`] returns; an empty block takes an empty slice.
///
/// Returns the checkpoints in ascending (seq, attempt) order, or an error when the bytes differ
/// from their checksum, do not decompress to the recorded length, or hold malformed entries.
pub fn decode_checkpoints_block(
    layout: &HistorySegmentLayout,
    stored: &[u8],
) -> Result<Vec<HistorySegmentCheckpointRow>, HistorySegmentCodecError> {
    let block = HistorySegmentBlockKind::Checkpoints;
    let plain = decompress_block(block, layout.checkpoints_block(), stored)?;
    parse_checkpoint_entries(layout, &plain)
}

/// Decodes one value from its stored bytes, which a ranged read of
/// [`HistorySegmentLayout::value_range`] returns for the entry's value.
///
/// Returns the stored `ValueRecord` row, or an error when the bytes differ from the entry's
/// checksum or do not decompress to its recorded length.
pub fn decode_value(
    entry: &HistorySegmentValueEntry,
    stored: &[u8],
) -> Result<HistorySegmentValueRow, HistorySegmentCodecError> {
    let value_id = entry_value_id(entry);
    let row = decompress(
        HistorySegmentBlockKind::Value(value_id),
        stored,
        entry.length,
        entry.uncompressed_length,
        entry.xxh3_64,
    )?;
    Ok(HistorySegmentValueRow { value_id, row })
}

/// Decodes a whole history segment object and verifies the trailer, every block and value
/// checksum, and the content checksum.
///
/// Returns the content exactly as it was encoded.
pub fn decode_history_segment(
    object: &[u8],
) -> Result<HistorySegmentContent, HistorySegmentCodecError> {
    let layout = decode_history_segment_header(object, object.len() as u64)?;
    let trailer_start = object.len() - HISTORY_SEGMENT_TRAILER_BYTES;
    let stored_trailer = u128::from_le_bytes(
        object[trailer_start..]
            .try_into()
            .expect("the trailer has sixteen bytes"),
    );
    if xxh3_128(&object[..trailer_start]) != stored_trailer {
        return Err(HistorySegmentCodecError::TrailerChecksumMismatch);
    }
    let body = &object[usize_offset(layout.body_offset())..trailer_start];
    let records_plain = decompress_block(
        HistorySegmentBlockKind::Records,
        layout.records_block(),
        block_bytes(body, layout.records_block()),
    )?;
    let checkpoints_plain = decompress_block(
        HistorySegmentBlockKind::Checkpoints,
        layout.checkpoints_block(),
        block_bytes(body, layout.checkpoints_block()),
    )?;
    let values_block = layout.values_block();
    let values_stored = block_bytes(body, values_block);
    if !values_stored.is_empty() && xxh3_64(values_stored) != values_block.xxh3_64 {
        return Err(HistorySegmentCodecError::BlockChecksumMismatch {
            block: HistorySegmentBlockKind::Values,
        });
    }
    let identity = layout.identity();
    let records = parse_record_entries(&layout, &records_plain)?;
    let checkpoints = parse_checkpoint_entries(&layout, &checkpoints_plain)?;
    let value_entries = &layout.header().value_entries;
    let mut checksum = ContentChecksum::new(&identity);
    checksum.add_records(records.len() as u64, &records_plain);
    checksum.add_checkpoints(checkpoints.len() as u64, &checkpoints_plain);
    checksum.begin_values(value_entries.len() as u64);
    let mut values = Vec::with_capacity(value_entries.len());
    for entry in value_entries {
        let start = usize_offset(entry.offset);
        let value = decode_value(entry, &body[start..start + usize_offset(entry.length)])?;
        checksum.add_value(value.value_id, &value.row)?;
        values.push(value);
    }
    if checksum.finish() != layout.content_checksum() {
        return Err(HistorySegmentCodecError::ContentChecksumMismatch);
    }
    Ok(HistorySegmentContent {
        identity,
        records,
        checkpoints,
        values,
    })
}

fn decompress_block(
    block: HistorySegmentBlockKind,
    description: &HistorySegmentBlock,
    stored: &[u8],
) -> Result<Vec<u8>, HistorySegmentCodecError> {
    decompress(
        block,
        stored,
        description.length,
        description.uncompressed_length,
        description.xxh3_64,
    )
}

fn decompress(
    block: HistorySegmentBlockKind,
    stored: &[u8],
    expected_length: u64,
    uncompressed_length: u64,
    expected_xxh3_64: u64,
) -> Result<Vec<u8>, HistorySegmentCodecError> {
    if stored.len() as u64 != expected_length {
        return Err(HistorySegmentCodecError::BlockLengthMismatch {
            block,
            expected: expected_length,
            actual: stored.len() as u64,
        });
    }
    if stored.is_empty() {
        return Ok(Vec::new());
    }
    if xxh3_64(stored) != expected_xxh3_64 {
        return Err(HistorySegmentCodecError::BlockChecksumMismatch { block });
    }
    let decompression_error =
        |detail: String| HistorySegmentCodecError::Decompression { block, detail };
    let decoder = zstd::stream::read::Decoder::with_buffer(stored)
        .map_err(|error| decompression_error(error.to_string()))?;
    // Memory follows the real output: reading stops one byte past the recorded length.
    let mut plain = Vec::new();
    decoder
        .take(uncompressed_length.saturating_add(1))
        .read_to_end(&mut plain)
        .map_err(|error| decompression_error(error.to_string()))?;
    if plain.len() as u64 != uncompressed_length {
        return Err(HistorySegmentCodecError::Decompression {
            block,
            detail: format!(
                "decompressed to {} bytes instead of {uncompressed_length}",
                plain.len()
            ),
        });
    }
    Ok(plain)
}

fn parse_record_entries(
    layout: &HistorySegmentLayout,
    plain: &[u8],
) -> Result<Vec<HistorySegmentRecordRow>, HistorySegmentCodecError> {
    let block = HistorySegmentBlockKind::Records;
    let seq_range = seq_range(layout);
    let mut entries = EntryReader { block, rest: plain };
    let mut records = Vec::with_capacity(entry_capacity(
        layout.header().record_count,
        plain.len(),
        RECORD_ENTRY_PREFIX_BYTES,
    ));
    while !entries.rest.is_empty() {
        let prefix = entries.take(RECORD_ENTRY_PREFIX_BYTES)?;
        let seq = u64::from_le_bytes(prefix[..8].try_into().expect("eight seq bytes"));
        let length = u32::from_le_bytes(prefix[8..].try_into().expect("four length bytes"));
        if !seq_range.contains(&seq)
            || records
                .last()
                .is_some_and(|previous: &HistorySegmentRecordRow| previous.seq >= seq)
        {
            return Err(entries.malformed(format!(
                "record seq {seq} is outside the range or out of order"
            )));
        }
        let row = entries.take(length as usize)?.to_vec();
        records.push(HistorySegmentRecordRow { seq, row });
    }
    if records.len() != layout.header().record_count as usize {
        return Err(entries.malformed(format!(
            "holds {} records instead of {}",
            records.len(),
            layout.header().record_count
        )));
    }
    Ok(records)
}

fn parse_checkpoint_entries(
    layout: &HistorySegmentLayout,
    plain: &[u8],
) -> Result<Vec<HistorySegmentCheckpointRow>, HistorySegmentCodecError> {
    let block = HistorySegmentBlockKind::Checkpoints;
    let seq_range = seq_range(layout);
    let mut entries = EntryReader { block, rest: plain };
    let mut checkpoints: Vec<HistorySegmentCheckpointRow> = Vec::with_capacity(entry_capacity(
        layout.header().checkpoint_count,
        plain.len(),
        CHECKPOINT_ENTRY_PREFIX_BYTES,
    ));
    while !entries.rest.is_empty() {
        let prefix = entries.take(CHECKPOINT_ENTRY_PREFIX_BYTES)?;
        let seq = u64::from_le_bytes(prefix[..8].try_into().expect("eight seq bytes"));
        let attempt = u32::from_le_bytes(prefix[8..12].try_into().expect("four attempt bytes"));
        let length = u32::from_le_bytes(prefix[12..].try_into().expect("four length bytes"));
        let is_ordered = checkpoints
            .last()
            .is_none_or(|previous| (previous.seq, previous.attempt) < (seq, attempt));
        if !seq_range.contains(&seq) || !is_ordered {
            return Err(entries.malformed(format!(
                "checkpoint ({seq}, {attempt}) is outside the range or out of order"
            )));
        }
        let row = entries.take(length as usize)?.to_vec();
        checkpoints.push(HistorySegmentCheckpointRow { seq, attempt, row });
    }
    if checkpoints.len() != layout.header().checkpoint_count as usize {
        return Err(entries.malformed(format!(
            "holds {} checkpoints instead of {}",
            checkpoints.len(),
            layout.header().checkpoint_count
        )));
    }
    Ok(checkpoints)
}

/// Returns how many entries to reserve: the header's count, but never more than the block can
/// frame, since each entry needs at least its prefix bytes.
fn entry_capacity(header_count: u32, block_length: usize, entry_prefix_bytes: usize) -> usize {
    usize::try_from(header_count)
        .unwrap_or(usize::MAX)
        .min(block_length / entry_prefix_bytes)
}

fn seq_range(layout: &HistorySegmentLayout) -> RangeInclusive<u64> {
    let identity = layout.identity();
    identity.first_seq..=identity.last_seq
}

/// Reads length-prefixed entries from a decompressed block.
struct EntryReader<'block> {
    block: HistorySegmentBlockKind,
    rest: &'block [u8],
}

impl<'block> EntryReader<'block> {
    fn take(&mut self, length: usize) -> Result<&'block [u8], HistorySegmentCodecError> {
        if self.rest.len() < length {
            return Err(self.malformed(format!(
                "an entry needs {length} bytes but {} remain",
                self.rest.len()
            )));
        }
        let (taken, rest) = self.rest.split_at(length);
        self.rest = rest;
        Ok(taken)
    }

    fn malformed(&self, detail: String) -> HistorySegmentCodecError {
        HistorySegmentCodecError::MalformedBlock {
            block: self.block,
            detail,
        }
    }
}

fn block_bytes<'body>(body: &'body [u8], block: &HistorySegmentBlock) -> &'body [u8] {
    let start = usize_offset(block.offset);
    &body[start..start + usize_offset(block.length)]
}

/// Converts an offset or length that a validated header bounds by the object size in memory.
fn usize_offset(value: u64) -> usize {
    usize::try_from(value).expect("a validated offset fits the object in memory")
}
