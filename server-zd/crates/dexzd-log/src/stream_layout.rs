// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! How a log stream's batches are keyed, listed, and numbered in object storage.

use crate::batch_codec::{DecodedBatch, LogError, StreamIdentity};

pub(crate) const LIST_PAGE_KEYS: u32 = 1_000;

/// Returns the length of the stream prefix of `batch_key`, up to its last `/`.
pub(crate) fn batch_key_prefix_length(batch_key: &str) -> usize {
    batch_key.rfind('/').expect("a batch key ends with /<seq>") + 1
}

/// Returns the seq that a listed key of the stream names, or [`LogError::Corrupt`] for another name.
pub(crate) fn parse_listed_seq(key: &str, prefix_length: usize) -> Result<u64, LogError> {
    key[prefix_length..]
        .parse::<u64>()
        .map_err(|_| LogError::Corrupt(format!("log stream holds an object named {key}")))
}

pub(crate) fn validate_batch_identity(
    batch: &DecodedBatch,
    stream: &StreamIdentity,
    seq: u64,
) -> Result<(), LogError> {
    if batch.header.cluster_id != stream.cluster_id
        || batch.header.node_id != stream.node_id
        || batch.header.incarnation != stream.incarnation
        || batch.header.seq != seq
    {
        return Err(LogError::Corrupt(format!(
            "batch at seq {seq} has a different stream identity"
        )));
    }
    Ok(())
}

pub(crate) fn next_seq_after(seq: u64) -> Result<u64, LogError> {
    seq.checked_add(1)
        .ok_or_else(|| LogError::Invalid("stream sequence exhausted".into()))
}
