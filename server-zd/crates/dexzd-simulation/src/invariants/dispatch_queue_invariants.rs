// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! DispatchQueue index invariants (plan section 3.2): every row is the current pending attempt
//! of a Step of the current run attempt, at most one row per invocation.

use dexzd_protocol::dexzd::v1::StepInvocationPhase;

use crate::invariants::{InvariantViolation, RunContext};
use crate::state_view::{FlowView, RunView};

const QUEUE_ROW: &str = "Q1-dispatch-queue-row";

/// Checks the rows of one completely decoded run.
pub(crate) fn check_dispatch_queue_rows(
    context: &RunContext<'_>,
    run: &RunView,
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    let Some((partition, flow_hash)) = view.location else {
        return Ok(());
    };
    let Some(record) = run.record.as_ref() else {
        return Ok(());
    };
    for (entry, row) in view
        .dispatch_queue_index
        .iter()
        .filter(|(entry, _)| entry.position.run_id == *context.run_id)
    {
        let step_execution_id = &entry.position.step_execution_id;
        let Some(step) = run.step_executions.get(step_execution_id) else {
            return Err(InvariantViolation::new(
                QUEUE_ROW,
                format!("{context} queues missing Step execution {step_execution_id}"),
            ));
        };
        let expected =
            dexzd_apply::dispatch_queue_entry(partition, record, flow_hash, *context.run_id, step);
        let is_pending = step
            .invocation
            .as_ref()
            .is_some_and(|invocation| invocation.phase == StepInvocationPhase::Pending as i32);
        if !is_pending || expected.as_ref() != Some(entry) {
            return Err(InvariantViolation::new(
                QUEUE_ROW,
                format!(
                    "{context} queues {step_execution_id} as {:?}, but its invocation is {:?}",
                    entry.position, step.invocation
                ),
            ));
        }
        if row.run_attempt != record.current_attempt || row.attempt != entry.position.attempt {
            return Err(InvariantViolation::new(
                QUEUE_ROW,
                format!(
                    "{context} queues {step_execution_id} for run attempt {} attempt {}",
                    row.run_attempt, row.attempt
                ),
            ));
        }
    }
    Ok(())
}

/// Checks that no row names a run without a decoded record.
pub(crate) fn check_dispatch_queue_runs_exist(
    flow_id: &str,
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    for (entry, _) in &view.dispatch_queue_index {
        let has_run = view
            .runs
            .get(&entry.position.run_id)
            .is_some_and(|run| run.record.is_some());
        if !has_run {
            return Err(InvariantViolation::new(
                QUEUE_ROW,
                format!(
                    "Flow {flow_id} queues {} of missing run {}",
                    entry.position.step_execution_id, entry.position.run_id
                ),
            ));
        }
    }
    Ok(())
}
