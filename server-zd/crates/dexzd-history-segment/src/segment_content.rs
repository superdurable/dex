// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::{RunId, ValueId};

/// The run and ranges one history segment covers.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HistorySegmentIdentity {
    /// Run whose history the segment holds.
    pub run_id: RunId,
    /// First seq of the segment; at least 1.
    pub first_seq: u64,
    /// Last seq of the segment; at least `first_seq`.
    pub last_seq: u64,
    /// First event ID assigned inside the seq range.
    pub first_event_id: i64,
    /// Last event ID assigned inside the seq range; smaller than `first_event_id` when the
    /// segment assigned no event.
    pub last_event_id: i64,
}

/// One history record row, as stored in the `history` column family.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HistorySegmentRecordRow {
    /// Seq of the record; the last 8 bytes of its key.
    pub seq: u64,
    /// The stored `HistoryRecord` bytes.
    pub row: Vec<u8>,
}

/// One checkpoint row, as stored in the `history` column family.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HistorySegmentCheckpointRow {
    /// Seq of the checkpoint.
    pub seq: u64,
    /// Run attempt of the checkpoint.
    pub attempt: u32,
    /// The stored `CheckpointRecord` bytes.
    pub row: Vec<u8>,
}

/// One superseded value row, as stored in the `value` column family.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HistorySegmentValueRow {
    /// The value.
    pub value_id: ValueId,
    /// The stored `ValueRecord` bytes.
    pub row: Vec<u8>,
}

/// Everything one history segment object holds.
///
/// Records are in ascending seq order, checkpoints in ascending (seq, attempt) order, and values in
/// ascending value ID order, each without duplicates and with seqs inside the identity's range.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HistorySegmentContent {
    /// The run and ranges.
    pub identity: HistorySegmentIdentity,
    /// History records whose seq is inside the range.
    pub records: Vec<HistorySegmentRecordRow>,
    /// Checkpoints of any attempt whose seq is inside the range.
    pub checkpoints: Vec<HistorySegmentCheckpointRow>,
    /// Values superseded inside the range when apply sealed the segment.
    pub values: Vec<HistorySegmentValueRow>,
}
