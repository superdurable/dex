// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SO1: SearchOrder rows match search documents; an orphan row would fail unordered queries.
//! SA1: active Step type counts and search documents match a count of every Step execution.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::read_model::search_order_index_keys_of_document;
use dexzd_protocol::dex::ActiveStepSearchMode;
use dexzd_protocol::dexzd::v1::StepExecutionPhase;

use crate::invariants::{InvariantViolation, RunContext};
use crate::row_decoding::hex;
use crate::state_view::{FlowView, RunView};

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

const ACTIVE_STEP_TYPES: &str = "SA1-active-step-types";

/// Active Step executions of one Step type: (with WaitFor, without WaitFor).
type StepTypeCounts = BTreeMap<String, (u64, u64)>;

/// Requires counts only while search is not DISABLED, and a search document listing what a full
/// count selects.
pub(crate) fn check_active_step_types(
    context: &RunContext<'_>,
    run: &RunView,
) -> Result<(), InvariantViolation> {
    let Some(record) = run.record.as_ref() else {
        return Ok(());
    };
    let mode = record
        .flow_config
        .as_ref()
        .and_then(|config| config.active_step_search_mode)
        .and_then(|mode| ActiveStepSearchMode::try_from(mode).ok())
        .unwrap_or(ActiveStepSearchMode::Unspecified);
    let recount = count_active_step_executions(run);
    let stored: Option<StepTypeCounts> =
        record
            .search_active_step_type_counts
            .as_ref()
            .map(|counts| {
                counts
                    .step_types
                    .iter()
                    .map(|(step_type, count)| {
                        (
                            step_type.clone(),
                            (count.with_wait_for, count.without_wait_for),
                        )
                    })
                    .collect()
            });
    let expected_stored = (mode != ActiveStepSearchMode::Disabled).then(|| recount.clone());
    if stored != expected_stored {
        return Err(InvariantViolation::new(
            ACTIVE_STEP_TYPES,
            format!(
                "{context}: search mode {mode:?} stores active Step type counts {stored:?}, \
                 but its Step executions count {expected_stored:?}"
            ),
        ));
    }
    let Some(document) = run.search_document.as_ref() else {
        return Ok(());
    };
    let expected_types: Vec<String> = recount
        .into_iter()
        .filter(|(_, (with_wait_for, without_wait_for))| match mode {
            ActiveStepSearchMode::Disabled => false,
            ActiveStepSearchMode::EnabledForAll => with_wait_for + without_wait_for > 0,
            ActiveStepSearchMode::Unspecified
            | ActiveStepSearchMode::EnabledForStepsWithWaitFor => *with_wait_for > 0,
        })
        .map(|(step_type, _)| step_type)
        .collect();
    if document.active_step_types != expected_types {
        return Err(InvariantViolation::new(
            ACTIVE_STEP_TYPES,
            format!(
                "{context}: the search document lists active Step types {:?}, but its Step \
                 executions select {expected_types:?}",
                document.active_step_types
            ),
        ));
    }
    Ok(())
}

/// Counts every active Step execution other than the Flow timeout handler.
fn count_active_step_executions(run: &RunView) -> StepTypeCounts {
    let mut counts = StepTypeCounts::new();
    for step in run.step_executions.values() {
        let is_active = matches!(
            StepExecutionPhase::try_from(step.phase),
            Ok(StepExecutionPhase::WaitFor
                | StepExecutionPhase::Waiting
                | StepExecutionPhase::Execute)
        );
        if step.is_timeout_handler || !is_active {
            continue;
        }
        let count = counts.entry(step.step_type.clone()).or_default();
        if step
            .options
            .as_ref()
            .is_some_and(|options| options.skip_wait_for)
        {
            count.1 += 1;
        } else {
            count.0 += 1;
        }
    }
    counts
}
