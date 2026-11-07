// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Object format of server-zd history segments, with the encoder for uploads and the decoders for history reads.
//!
//! The object layout is documented on `HistorySegmentHeader` in
//! `proto/dexzd/v1/history_segment.proto`. Rows travel as the exact bytes stored locally; the codec
//! never decodes and re-encodes them, so every binary computes the same content checksum.

mod codec_error;
mod object_key;
mod segment_content;
mod segment_decoding;
mod segment_encoding;
mod segment_layout;

pub use codec_error::{HistorySegmentBlockKind, HistorySegmentCodecError};
pub use object_key::{
    HISTORY_SEGMENT_OBJECT_KEY_PREFIX, history_run_object_key_prefix, history_segment_object_key,
};
pub use segment_content::{
    HistorySegmentCheckpointRow, HistorySegmentContent, HistorySegmentIdentity,
    HistorySegmentRecordRow, HistorySegmentValueRow,
};
pub use segment_decoding::{
    decode_checkpoints_block, decode_history_segment, decode_history_segment_header,
    decode_records_block, decode_value,
};
pub use segment_encoding::{EncodedHistorySegment, encode_history_segment};
pub use segment_layout::{
    HISTORY_SEGMENT_PREFIX_BYTES, HISTORY_SEGMENT_TRAILER_BYTES, HistorySegmentLayout,
};
