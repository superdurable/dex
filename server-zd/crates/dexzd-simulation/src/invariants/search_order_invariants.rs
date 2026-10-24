// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SO1: SearchOrder rows match search documents; an orphan row would fail unordered queries.

use std::collections::BTreeSet;

use dexzd_apply::read_model::search_order_index_keys_of_document;

use crate::invariants::InvariantViolation;
use crate::row_decoding::hex;
use crate::state_view::FlowView;

/// Checks every completely decoded run, and that no SearchOrder row outlives its run.
pub(crate) fn check_search_order(flow_id: &str, view: &FlowView) -> Result<(), InvariantViolation> {
    let Some((partition, flow_hash)) = view.location else {
        return Ok(());
    };
    for (run_id, run) in &view.runs {
        if !run.is_complete {
            continue;
        }
        let actual = view
            .search_order_keys
            .get(run_id)
            .cloned()
            .unwrap_or_default();
        let expected: BTreeSet<Vec<u8>> = run
            .search_document
            .iter()
            .flat_map(|document| {
                search_order_index_keys_of_document(partition, flow_hash, *run_id, document)
            })
            .collect();
        if actual != expected {
            return Err(InvariantViolation::new(
                "SO1-search-order-index",
                format!(
                    "Flow {flow_id} run {run_id}: SearchOrder keys {:?} differ from the search \
                     document's {:?}",
                    actual.iter().map(|key| hex(key)).collect::<Vec<_>>(),
                    expected.iter().map(|key| hex(key)).collect::<Vec<_>>()
                ),
            ));
        }
    }
    for run_id in view.search_order_keys.keys() {
        if !view.runs.contains_key(run_id) {
            return Err(InvariantViolation::new(
                "SO1-search-order-index",
                format!("Flow {flow_id}: SearchOrder rows name run {run_id}, which has no rows"),
            ));
        }
    }
    Ok(())
}
