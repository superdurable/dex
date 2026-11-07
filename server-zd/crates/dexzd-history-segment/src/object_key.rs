// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::{FlowHash, RunId, TenantId};

/// Key prefix of every history segment object in the primary object store.
pub const HISTORY_SEGMENT_OBJECT_KEY_PREFIX: &str = "history/";

/// Returns `history/<tenant:016x>/<flow_hash:016x>/<run_id>/`, the prefix of every history segment
/// object of the run.
pub fn history_run_object_key_prefix(
    tenant_id: TenantId,
    flow_hash: FlowHash,
    run_id: RunId,
) -> String {
    format!(
        "{HISTORY_SEGMENT_OBJECT_KEY_PREFIX}{:016x}/{:016x}/{run_id}/",
        tenant_id.0, flow_hash.0
    )
}

/// Returns the object key of the run's history segment `[first_seq, last_seq]`:
/// `history/<tenant:016x>/<flow_hash:016x>/<run_id>/<first_seq:020>-<last_seq:020>`.
///
/// Apply decides the range, so every owner of the partition writes the same key.
pub fn history_segment_object_key(
    tenant_id: TenantId,
    flow_hash: FlowHash,
    run_id: RunId,
    first_seq: u64,
    last_seq: u64,
) -> String {
    format!(
        "{}{first_seq:020}-{last_seq:020}",
        history_run_object_key_prefix(tenant_id, flow_hash, run_id)
    )
}

#[cfg(test)]
mod tests {
    use dexzd_keyspace::{FlowHash, RunId, TenantId};

    use super::{history_run_object_key_prefix, history_segment_object_key};

    #[test]
    fn segment_keys_sort_by_first_seq_under_the_run_prefix() {
        let run_id: RunId = "0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6"
            .parse()
            .expect("the run ID text is canonical");
        let key = history_segment_object_key(TenantId(1), FlowHash(0xab), run_id, 7, 1200);
        assert_eq!(
            key,
            "history/0000000000000001/00000000000000ab/0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6/\
             00000000000000000007-00000000000000001200"
        );
        let prefix = history_run_object_key_prefix(TenantId(1), FlowHash(0xab), run_id);
        assert!(key.starts_with(&prefix));
        assert!(key < history_segment_object_key(TenantId(1), FlowHash(0xab), run_id, 1201, 1300));
    }
}
