// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt;

use dexzd_keyspace::ValueId;

/// A block of the history segment body.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HistorySegmentBlockKind {
    /// The history records block.
    Records,
    /// The checkpoints block.
    Checkpoints,
    /// The values block as a whole.
    Values,
    /// One value inside the values block.
    Value(ValueId),
}

impl fmt::Display for HistorySegmentBlockKind {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Records => formatter.write_str("records block"),
            Self::Checkpoints => formatter.write_str("checkpoints block"),
            Self::Values => formatter.write_str("values block"),
            Self::Value(value_id) => write!(
                formatter,
                "value {}/{}",
                value_id.run_seq, value_id.index_in_command
            ),
        }
    }
}

/// Why a history segment could not be encoded or decoded.
#[derive(Debug, thiserror::Error)]
pub enum HistorySegmentCodecError {
    /// The content to encode breaks the order or range rules of `HistorySegmentContent`.
    #[error("history segment content is invalid: {detail}")]
    InvalidContent {
        /// The broken rule.
        detail: String,
    },
    /// Compressing a block failed.
    #[error("compressing the history segment {block} failed: {detail}")]
    Compression {
        /// The block.
        block: HistorySegmentBlockKind,
        /// The compressor's message.
        detail: String,
    },
    /// The bytes given hold less than the prefix and header need; read at least `required` bytes
    /// from the start of the object.
    #[error(
        "history segment header needs {required} bytes from the object start but has {available}"
    )]
    TruncatedHeader {
        /// Bytes needed from the start of the object.
        required: usize,
        /// Bytes given.
        available: usize,
    },
    /// The object does not start with the history segment magic.
    #[error("object is not a history segment: its magic differs")]
    BadMagic,
    /// The object has a format version this binary cannot read.
    #[error("history segment format version {version} is not supported")]
    UnsupportedFormatVersion {
        /// The stored version.
        version: u16,
    },
    /// The header bytes differ from their checksum.
    #[error("history segment header checksum differs")]
    HeaderChecksumMismatch,
    /// The header does not decode or contradicts the layout rules.
    #[error("history segment header is invalid: {detail}")]
    InvalidHeader {
        /// What is wrong.
        detail: String,
    },
    /// The object size differs from the size its header describes, as after a truncation.
    #[error("history segment object has {actual} bytes but its header describes {expected}")]
    ObjectSizeMismatch {
        /// Size the header describes.
        expected: u64,
        /// Size of the object.
        actual: u64,
    },
    /// The stored bytes of a block or value have a different length than the header records.
    #[error("history segment {block} has {actual} stored bytes but its header records {expected}")]
    BlockLengthMismatch {
        /// The block.
        block: HistorySegmentBlockKind,
        /// Length the header records.
        expected: u64,
        /// Length given.
        actual: u64,
    },
    /// The stored bytes of a block or value differ from their checksum.
    #[error("history segment {block} checksum differs")]
    BlockChecksumMismatch {
        /// The block.
        block: HistorySegmentBlockKind,
    },
    /// A block or value does not decompress to its recorded length.
    #[error("decompressing the history segment {block} failed: {detail}")]
    Decompression {
        /// The block.
        block: HistorySegmentBlockKind,
        /// What failed.
        detail: String,
    },
    /// The entries inside a decompressed block are malformed or out of order.
    #[error("history segment {block} is malformed: {detail}")]
    MalformedBlock {
        /// The block.
        block: HistorySegmentBlockKind,
        /// What is wrong.
        detail: String,
    },
    /// The whole-object trailer differs from the object bytes.
    #[error("history segment trailer checksum differs")]
    TrailerChecksumMismatch,
    /// The decoded content differs from the header's content checksum.
    #[error("history segment content checksum differs")]
    ContentChecksumMismatch,
}
