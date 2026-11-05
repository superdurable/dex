// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Read access to stored state for apply, and the transaction that buffers apply's writes.

mod flow_transaction;
mod run_rows;

pub(crate) use flow_transaction::{FlowTransaction, StateRowChange};
pub(crate) use run_rows::{RunRows, read_flow_directory, write_flow_directory};

use dexzd_keyspace::ColumnFamily;

/// A stored row: (key, encoded value).
pub type StateRow = (Vec<u8>, Vec<u8>);

/// Error from the store behind a [`FlowStateReader`].
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("state read of {column_family:?} failed: {detail}")]
pub struct StateReadError {
    /// Column family being read.
    pub column_family: ColumnFamily,
    /// Store message.
    pub detail: String,
}

/// Read-only view of committed or speculative state that apply runs against.
///
/// Implementations: the local store's consistent view, the runtime's speculative overlay, and
/// the temporary state that time travel and replay checks build. Apply reads only the keys of
/// the command's Flow and the partition indexes of its partition.
pub trait FlowStateReader {
    /// Returns the row at `key`, or `None` when it does not exist.
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError>;

    /// Returns up to `limit` rows with keys in `[start, end)`, in ascending key order.
    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError>;

    /// Returns the first row in `[start, end)`, or [`BoundedRowSeek::Incomplete`] after skipping
    /// more than `max_skipped_deletions` deleted entries. Readers without deletion markers never
    /// give up.
    fn seek_first_bounded(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        _max_skipped_deletions: u64,
    ) -> Result<BoundedRowSeek, StateReadError> {
        Ok(
            match self.scan(column_family, start, end, 1)?.into_iter().next() {
                Some(row) => BoundedRowSeek::Found(row),
                None => BoundedRowSeek::Exhausted,
            },
        )
    }
}

/// The first row of a bounded seek.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum BoundedRowSeek {
    /// The first row in the range.
    Found(StateRow),
    /// The range has no row.
    Exhausted,
    /// The seek skipped too many deleted entries before finding a row.
    Incomplete,
}
