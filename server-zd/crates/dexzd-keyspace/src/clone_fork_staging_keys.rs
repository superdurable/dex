// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Keys of CloneForkFlow stagings, which belong to a Flow but to none of its runs.

use crate::row_keys::{ordered_timestamp_bytes, timestamp_from_ordered_bytes};
use crate::{
    FLOW_CLONE_FORK_STAGING_KEY_TAG, FlowHash, IndexKind, PartitionId, partition_index_key,
};

/// Key of a CloneForkFlow staging record (`flow` column family):
/// `flow_hash(8) ‖ 0x03 ‖ staging_id`.
pub fn flow_clone_fork_staging_key(flow_hash: FlowHash, staging_id: &[u8]) -> Vec<u8> {
    let mut key = Vec::with_capacity(9 + staging_id.len());
    key.extend_from_slice(&flow_hash.to_be_bytes());
    key.push(FLOW_CLONE_FORK_STAGING_KEY_TAG);
    key.extend_from_slice(staging_id);
    key
}

/// Key of chunk `chunk_index` of a CloneForkFlow staging (`value` column family):
/// `flow_hash(8) ‖ 0x03 ‖ staging_id ‖ chunk_index(4)`.
pub fn flow_clone_fork_staging_chunk_key(
    flow_hash: FlowHash,
    staging_id: &[u8],
    chunk_index: u32,
) -> Vec<u8> {
    let mut key = flow_clone_fork_staging_key(flow_hash, staging_id);
    key.extend_from_slice(&chunk_index.to_be_bytes());
    key
}

/// Index key of a CloneForkFlow staging by the proposal time of its latest chunk.
pub fn flow_clone_fork_staging_index_key(
    partition: PartitionId,
    staged_at_micros: i64,
    flow_hash: FlowHash,
    staging_id: &[u8],
) -> Vec<u8> {
    let mut rest = ordered_timestamp_bytes(staged_at_micros).to_vec();
    rest.extend_from_slice(&flow_hash.to_be_bytes());
    rest.extend_from_slice(staging_id);
    partition_index_key(partition, IndexKind::FlowCloneForkStaging, &rest)
}

/// Decodes a [`flow_clone_fork_staging_index_key`].
///
/// Returns its staged time, Flow hash, and staging ID, or `None` for another layout.
pub fn decode_flow_clone_fork_staging_index_key(key: &[u8]) -> Option<(i64, FlowHash, &[u8])> {
    let prefix_length = 3;
    if key.get(2) != Some(&(IndexKind::FlowCloneForkStaging as u8)) {
        return None;
    }
    let rest = key.get(prefix_length..)?;
    let (staged_at, rest) = rest.split_first_chunk::<8>()?;
    let (flow_hash, staging_id) = rest.split_first_chunk::<8>()?;
    Some((
        timestamp_from_ordered_bytes(*staged_at),
        FlowHash(u64::from_be_bytes(*flow_hash)),
        staging_id,
    ))
}

#[cfg(test)]
mod tests {
    use super::{
        decode_flow_clone_fork_staging_index_key, flow_clone_fork_staging_chunk_key,
        flow_clone_fork_staging_index_key, flow_clone_fork_staging_key,
    };
    use crate::{FLOW_CLONE_FORK_STAGING_KEY_TAG, FlowHash, PartitionId, parse_run_key};

    #[test]
    fn clone_fork_staging_keys_stay_in_their_flow_and_decode_from_the_index() {
        let flow_hash = FlowHash(0x0102_0304_0506_0708);
        let staging_id = [9_u8; 16];
        let record_key = flow_clone_fork_staging_key(flow_hash, &staging_id);
        let chunk_key = flow_clone_fork_staging_chunk_key(flow_hash, &staging_id, 3);
        assert_eq!(record_key[..8], flow_hash.to_be_bytes());
        assert_eq!(record_key[8], FLOW_CLONE_FORK_STAGING_KEY_TAG);
        assert!(chunk_key.starts_with(&record_key));
        assert_eq!(chunk_key[record_key.len()..], 3_u32.to_be_bytes());
        assert!(parse_run_key(&record_key).is_err());
        let earlier = flow_clone_fork_staging_index_key(PartitionId(4), -5, flow_hash, &staging_id);
        let later = flow_clone_fork_staging_index_key(PartitionId(4), 7, flow_hash, &staging_id);
        assert!(earlier < later);
        assert_eq!(
            decode_flow_clone_fork_staging_index_key(&later),
            Some((7, flow_hash, &staging_id[..]))
        );
        assert_eq!(decode_flow_clone_fork_staging_index_key(&chunk_key), None);
    }
}
