// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Identifiers, partition hashing, RocksDB key encodings, and value references shared by every server-zd crate.
//!
//! Everything in this crate is a permanent format. Byte values of enums and the layout of keys
//! never change; new variants and key kinds are only appended.

use std::ops::RangeInclusive;

mod clone_fork_staging_keys;
pub mod dispatch_queue_keys;
mod identifiers;
mod key_layout;
mod node_identifier;
mod partition_hash;
pub mod row_keys;
mod value_reference;

pub use identifiers::{FlowHash, IdentifierError, PartitionId, RunId, TenantId, ValueId};
pub use key_layout::{
    ColumnFamily, FLOW_CLONE_FORK_STAGING_KEY_TAG, FLOW_DIRECTORY_KEY_TAG, FlowKeyRange, IndexKind,
    KeyLayoutError, ParsedRunKey, RUN_KEY_TAG, RunTable, decode_partition_meta_key,
    flow_directory_key, flow_directory_prefix, parse_run_key, partition_index_key,
    partition_index_prefix, partition_key_range, partition_meta_key, run_key, run_prefix,
    run_table_prefix,
};
pub use node_identifier::{NodeIdentifierError, validate_node_identifier};
pub use partition_hash::{PartitionHashError, PartitionHasher};
pub use value_reference::{VALUE_REFERENCE_PREFIX, ValueReference, ValueReferenceError};

/// Current RocksDB key layout version, recorded in the cluster object.
pub const KEY_LAYOUT_VERSION: u32 = 1;

/// Key layout versions reserved for applications that embed server-zd.
///
/// server-zd's own key layout versions always stay below this range, so a server-zd binary,
/// including an already released one, never opens a cluster that such an application created.
pub const EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS: RangeInclusive<u32> = 0x8000_0000..=u32::MAX;

/// Tag bytes after the Flow hash that are reserved for applications that embed server-zd.
///
/// server-zd's own tags, such as [`FLOW_DIRECTORY_KEY_TAG`] and [`RUN_KEY_TAG`], stay below it.
pub const EMBEDDING_APPLICATION_KEY_TAGS: RangeInclusive<u8> = 0x80..=0xff;

/// [`IndexKind`] bytes reserved for applications that embed server-zd.
///
/// server-zd's own index kinds stay below this range.
pub const EMBEDDING_APPLICATION_INDEX_KINDS: RangeInclusive<u8> = 0x80..=0xff;

const _: () = assert!(KEY_LAYOUT_VERSION < *EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS.start());

#[cfg(test)]
mod embedding_application_reservation_tests {
    use super::{
        EMBEDDING_APPLICATION_INDEX_KINDS, EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS,
        EMBEDDING_APPLICATION_KEY_TAGS, FLOW_CLONE_FORK_STAGING_KEY_TAG, FLOW_DIRECTORY_KEY_TAG,
        IndexKind, KEY_LAYOUT_VERSION, RUN_KEY_TAG,
    };

    #[test]
    fn server_zd_values_stay_below_the_embedding_application_ranges() {
        assert!(!EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS.contains(&KEY_LAYOUT_VERSION));
        for tag in [
            FLOW_DIRECTORY_KEY_TAG,
            RUN_KEY_TAG,
            FLOW_CLONE_FORK_STAGING_KEY_TAG,
        ] {
            assert!(
                !EMBEDDING_APPLICATION_KEY_TAGS.contains(&tag),
                "tag {tag:#04x}"
            );
        }
        for kind in IndexKind::ALL {
            assert!(
                !EMBEDDING_APPLICATION_INDEX_KINDS.contains(&(kind as u8)),
                "{kind:?}"
            );
        }
    }
}
