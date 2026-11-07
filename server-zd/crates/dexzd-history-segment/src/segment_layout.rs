// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The fixed prefix, the header rules, entry framing, and the content checksum, shared by the
//! encoder and the decoders.
//!
//! The format has no limit beyond its field widths: rows below 4 GiB, which every RocksDB value
//! is, a header below 4 GiB, and 64-bit offsets. So every segment apply can seal is encodable.

use std::ops::Range;

use dexzd_keyspace::{RunId, ValueId};
use dexzd_protocol::dexzd::v1::{
    HistorySegmentBlock, HistorySegmentHeader, HistorySegmentValueEntry,
};
use xxhash_rust::xxh3::Xxh3;

use crate::codec_error::HistorySegmentCodecError;
use crate::segment_content::HistorySegmentIdentity;

pub(crate) const MAGIC: &[u8; 8] = b"DEXZDHIS";
pub(crate) const FORMAT_VERSION: u16 = 1;
/// Bytes before the header: magic, format version, header length, and header xxh3-64.
pub const HISTORY_SEGMENT_PREFIX_BYTES: usize = 22;
/// Bytes of the trailer: the xxh3-128 of every preceding byte.
pub const HISTORY_SEGMENT_TRAILER_BYTES: usize = 16;
pub(crate) const RECORD_ENTRY_PREFIX_BYTES: usize = 12;
pub(crate) const CHECKPOINT_ENTRY_PREFIX_BYTES: usize = 16;
const RECORDS_SECTION_TAG: u8 = b'R';
const CHECKPOINTS_SECTION_TAG: u8 = b'C';
const VALUES_SECTION_TAG: u8 = b'V';

/// The validated prefix and header of one history segment object.
#[derive(Debug, Clone, PartialEq)]
pub struct HistorySegmentLayout {
    header: HistorySegmentHeader,
    identity: HistorySegmentIdentity,
    body_offset: u64,
    object_size: u64,
    content_checksum: [u8; 16],
}

impl HistorySegmentLayout {
    /// Validates `header`, whose encoded length is `header_bytes`, against the layout rules and
    /// requires it to describe exactly `object_size` bytes. All offset arithmetic is checked, so
    /// every range the layout returns lies inside the object.
    pub(crate) fn new(
        header: HistorySegmentHeader,
        header_bytes: usize,
        object_size: u64,
    ) -> Result<Self, HistorySegmentCodecError> {
        let run_id = RunId::from_slice(&header.run_id)
            .map_err(|_| invalid_header("the run ID is not 16 bytes"))?;
        if header.first_seq == 0 || header.first_seq > header.last_seq {
            return Err(invalid_header(format!(
                "seq range {}..={} is empty or starts at zero",
                header.first_seq, header.last_seq
            )));
        }
        let content_checksum: [u8; 16] = header
            .content_checksum
            .as_slice()
            .try_into()
            .map_err(|_| invalid_header("the content checksum is not 16 bytes"))?;
        let records = required_block(header.records.as_ref(), "records")?;
        let checkpoints = required_block(header.checkpoints.as_ref(), "checkpoints")?;
        let values = required_block(header.values.as_ref(), "values")?;
        let records_end = validate_block(records, 0, header.record_count > 0, "records")?;
        let checkpoints_end = validate_block(
            checkpoints,
            records_end,
            header.checkpoint_count > 0,
            "checkpoints",
        )?;
        // Empty value rows store no frame, so only non-empty values give the block content.
        let has_stored_values = header
            .value_entries
            .iter()
            .any(|entry| entry.uncompressed_length > 0);
        let body_length = validate_block(values, checkpoints_end, has_stored_values, "values")?;
        validate_value_entries(values, &header.value_entries)?;
        let described_object_size = u64::try_from(HISTORY_SEGMENT_PREFIX_BYTES)
            .ok()
            .zip(u64::try_from(header_bytes).ok())
            .and_then(|(prefix, header_length)| prefix.checked_add(header_length))
            .and_then(|body_offset| {
                body_offset
                    .checked_add(body_length)?
                    .checked_add(HISTORY_SEGMENT_TRAILER_BYTES as u64)
                    .map(|size| (body_offset, size))
            });
        let Some((body_offset, described_object_size)) = described_object_size else {
            return Err(invalid_header("the described object size overflows"));
        };
        if described_object_size != object_size {
            return Err(HistorySegmentCodecError::ObjectSizeMismatch {
                expected: described_object_size,
                actual: object_size,
            });
        }
        Ok(Self {
            identity: HistorySegmentIdentity {
                run_id,
                first_seq: header.first_seq,
                last_seq: header.last_seq,
                first_event_id: header.first_event_id,
                last_event_id: header.last_event_id,
            },
            header,
            body_offset,
            object_size,
            content_checksum,
        })
    }

    /// The decoded header.
    pub fn header(&self) -> &HistorySegmentHeader {
        &self.header
    }

    /// The run and ranges of the segment.
    pub fn identity(&self) -> HistorySegmentIdentity {
        self.identity
    }

    /// The xxh3-128 of the segment's canonical uncompressed content, big-endian.
    pub fn content_checksum(&self) -> [u8; 16] {
        self.content_checksum
    }

    /// Size of the whole object, which the header describes exactly.
    pub fn object_size(&self) -> u64 {
        self.object_size
    }

    /// Object byte range of the records block; `None` when the segment has no record.
    pub fn records_range(&self) -> Option<Range<u64>> {
        self.block_range(self.records_block())
    }

    /// Object byte range of the checkpoints block; `None` when the segment has no checkpoint.
    pub fn checkpoints_range(&self) -> Option<Range<u64>> {
        self.block_range(self.checkpoints_block())
    }

    /// Returns the entry of `value_id`, or `None` when the segment does not hold that value.
    pub fn find_value(&self, value_id: ValueId) -> Option<&HistorySegmentValueEntry> {
        self.header
            .value_entries
            .binary_search_by(|entry| entry_value_id(entry).cmp(&value_id))
            .ok()
            .map(|index| &self.header.value_entries[index])
    }

    /// Returns the object byte range of `value_id`'s stored frame, empty for an empty value row,
    /// or `None` when the segment does not hold that value.
    pub fn value_range(&self, value_id: ValueId) -> Option<Range<u64>> {
        let entry = self.find_value(value_id)?;
        // Validation keeps every entry of this layout inside the body, below the object size.
        let start = self.body_offset + entry.offset;
        Some(start..start + entry.length)
    }

    pub(crate) fn records_block(&self) -> &HistorySegmentBlock {
        self.header
            .records
            .as_ref()
            .expect("a validated header has a records block")
    }

    pub(crate) fn checkpoints_block(&self) -> &HistorySegmentBlock {
        self.header
            .checkpoints
            .as_ref()
            .expect("a validated header has a checkpoints block")
    }

    pub(crate) fn values_block(&self) -> &HistorySegmentBlock {
        self.header
            .values
            .as_ref()
            .expect("a validated header has a values block")
    }

    pub(crate) fn body_offset(&self) -> u64 {
        self.body_offset
    }

    fn block_range(&self, block: &HistorySegmentBlock) -> Option<Range<u64>> {
        (block.length > 0).then(|| {
            let start = self.body_offset + block.offset;
            start..start + block.length
        })
    }
}

/// Returns the value ID of a header value entry.
pub(crate) fn entry_value_id(entry: &HistorySegmentValueEntry) -> ValueId {
    ValueId {
        run_seq: entry.run_seq,
        index_in_command: entry.index_in_command,
    }
}

fn required_block<'header>(
    block: Option<&'header HistorySegmentBlock>,
    name: &str,
) -> Result<&'header HistorySegmentBlock, HistorySegmentCodecError> {
    block.ok_or_else(|| invalid_header(format!("the {name} block is missing")))
}

/// Checks that the block starts at `expected_offset` and agrees with its entries.
///
/// Returns the body offset where the block ends.
fn validate_block(
    block: &HistorySegmentBlock,
    expected_offset: u64,
    has_entries: bool,
    name: &str,
) -> Result<u64, HistorySegmentCodecError> {
    if block.offset != expected_offset {
        return Err(invalid_header(format!(
            "the {name} block starts at {} instead of {expected_offset}",
            block.offset
        )));
    }
    if !is_consistent_frame(
        block.length,
        block.uncompressed_length,
        block.xxh3_64,
        has_entries,
    ) {
        return Err(invalid_header(format!(
            "the {name} block's lengths disagree with its entries"
        )));
    }
    block
        .offset
        .checked_add(block.length)
        .ok_or_else(|| invalid_header(format!("the {name} block overflows")))
}

fn validate_value_entries(
    values: &HistorySegmentBlock,
    entries: &[HistorySegmentValueEntry],
) -> Result<(), HistorySegmentCodecError> {
    let mut next_offset = values.offset;
    let mut uncompressed_total = 0_u64;
    let mut previous: Option<ValueId> = None;
    for entry in entries {
        let value_id = entry_value_id(entry);
        if previous.is_some_and(|previous| previous >= value_id) {
            return Err(invalid_header(
                "value entries are not in ascending value ID order",
            ));
        }
        previous = Some(value_id);
        let is_frame_consistent = is_consistent_frame(
            entry.length,
            entry.uncompressed_length,
            entry.xxh3_64,
            entry.uncompressed_length > 0,
        );
        if entry.offset != next_offset || !is_frame_consistent {
            return Err(invalid_header(format!(
                "value {}/{} is not contiguous or its lengths disagree",
                entry.run_seq, entry.index_in_command
            )));
        }
        next_offset = next_offset
            .checked_add(entry.length)
            .ok_or_else(|| invalid_header("value entries overflow"))?;
        uncompressed_total = uncompressed_total
            .checked_add(entry.uncompressed_length)
            .ok_or_else(|| invalid_header("value lengths overflow"))?;
    }
    let values_end = values
        .offset
        .checked_add(values.length)
        .ok_or_else(|| invalid_header("the values block overflows"))?;
    if next_offset != values_end || uncompressed_total != values.uncompressed_length {
        return Err(invalid_header(
            "value entries do not cover exactly the values block",
        ));
    }
    Ok(())
}

/// Returns whether a stored frame's lengths and checksum agree: an empty frame stores nothing,
/// has a zero checksum, and decompresses to nothing; a non-empty one decompresses to something.
fn is_consistent_frame(
    stored_length: u64,
    uncompressed_length: u64,
    xxh3_64: u64,
    has_content: bool,
) -> bool {
    if stored_length == 0 {
        !has_content && uncompressed_length == 0 && xxh3_64 == 0
    } else {
        has_content && uncompressed_length > 0
    }
}

pub(crate) fn invalid_header(detail: impl Into<String>) -> HistorySegmentCodecError {
    HistorySegmentCodecError::InvalidHeader {
        detail: detail.into(),
    }
}

/// Appends one records block entry: `seq u64 | length u32 | row`.
pub(crate) fn append_record_entry(
    block: &mut Vec<u8>,
    seq: u64,
    row: &[u8],
) -> Result<(), HistorySegmentCodecError> {
    block.extend_from_slice(&seq.to_le_bytes());
    block.extend_from_slice(&row_length(row)?.to_le_bytes());
    block.extend_from_slice(row);
    Ok(())
}

/// Appends one checkpoints block entry: `seq u64 | attempt u32 | length u32 | row`.
pub(crate) fn append_checkpoint_entry(
    block: &mut Vec<u8>,
    seq: u64,
    attempt: u32,
    row: &[u8],
) -> Result<(), HistorySegmentCodecError> {
    block.extend_from_slice(&seq.to_le_bytes());
    block.extend_from_slice(&attempt.to_le_bytes());
    block.extend_from_slice(&row_length(row)?.to_le_bytes());
    block.extend_from_slice(row);
    Ok(())
}

fn row_length(row: &[u8]) -> Result<u32, HistorySegmentCodecError> {
    u32::try_from(row.len()).map_err(|_| HistorySegmentCodecError::InvalidContent {
        detail: format!("a row of {} bytes exceeds the 4 GiB entry limit", row.len()),
    })
}

/// Streaming xxh3-128 of the canonical uncompressed content described on the header proto.
///
/// Each section starts with its tag and entry count, and each block with its length, so content
/// moved between sections never yields the same checksum.
pub(crate) struct ContentChecksum {
    hasher: Xxh3,
}

impl ContentChecksum {
    pub(crate) fn new(identity: &HistorySegmentIdentity) -> Self {
        let mut hasher = Xxh3::new();
        hasher.update(identity.run_id.as_bytes());
        hasher.update(&identity.first_seq.to_le_bytes());
        hasher.update(&identity.last_seq.to_le_bytes());
        hasher.update(&identity.first_event_id.to_le_bytes());
        hasher.update(&identity.last_event_id.to_le_bytes());
        Self { hasher }
    }

    /// Adds the uncompressed records block holding `record_count` entries.
    pub(crate) fn add_records(&mut self, record_count: u64, uncompressed: &[u8]) {
        self.add_section(RECORDS_SECTION_TAG, record_count, uncompressed);
    }

    /// Adds the uncompressed checkpoints block holding `checkpoint_count` entries.
    pub(crate) fn add_checkpoints(&mut self, checkpoint_count: u64, uncompressed: &[u8]) {
        self.add_section(CHECKPOINTS_SECTION_TAG, checkpoint_count, uncompressed);
    }

    /// Starts the values section of `value_count` values, which follow with [`Self::add_value`].
    pub(crate) fn begin_values(&mut self, value_count: u64) {
        self.hasher.update(&[VALUES_SECTION_TAG]);
        self.hasher.update(&value_count.to_le_bytes());
    }

    /// Adds one value row; values must be added in ascending value ID order.
    pub(crate) fn add_value(
        &mut self,
        value_id: ValueId,
        row: &[u8],
    ) -> Result<(), HistorySegmentCodecError> {
        self.hasher.update(&value_id.run_seq.to_le_bytes());
        self.hasher.update(&value_id.index_in_command.to_le_bytes());
        self.hasher.update(&row_length(row)?.to_le_bytes());
        self.hasher.update(row);
        Ok(())
    }

    pub(crate) fn finish(self) -> [u8; 16] {
        self.hasher.digest128().to_be_bytes()
    }

    fn add_section(&mut self, tag: u8, entry_count: u64, uncompressed: &[u8]) {
        self.hasher.update(&[tag]);
        self.hasher.update(&entry_count.to_le_bytes());
        self.hasher
            .update(&(uncompressed.len() as u64).to_le_bytes());
        self.hasher.update(uncompressed);
    }
}
