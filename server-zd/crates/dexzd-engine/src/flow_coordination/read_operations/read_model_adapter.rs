// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Adapters to apply's read model for GetFlowState and GetHistoryEvents.

use dexzd_apply::read_model::{
    ReadModelError, SegmentedHistoryPage, get_flow_state, get_history_events_from_segments,
};
use dexzd_apply::{ApplyContext, FlowStateReader, LoadedHistorySegments};
use dexzd_protocol::dex::{
    ErrorSubStatus, GetFlowStateRequest, GetFlowStateResponse, GetHistoryEventsRequest, Kv,
    StepCompletionOutput, StepMethodPendingReason, Value,
};

use dexzd_keyspace::row_keys::run_named_key;
use dexzd_keyspace::{ColumnFamily, RunId, RunTable};
use dexzd_protocol::dexzd::v1::StepExecutionRecord;

use crate::flow_coordination::read_operations::state_rows::{
    decode_row, hydrate_value_reference, read_run,
};
use crate::flow_coordination::{FlowAddress, FlowCoordinationError, StepQueueStatus, grpc_code};

/// Returns the Flow state projected by apply's read model, with value references replaced by
/// their values when `is_hydrating`. `is_rpc_lock_held` reports the live locks of admitted
/// transactional RPCs, which only the Flow's coordinator knows.
///
/// Returns `NOT_FOUND` + `FLOW_NOT_EXISTS` when the run does not exist, and `Internal` when a
/// value reference of the projection has no value row.
pub(crate) fn read_flow_state(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    flow: &FlowAddress,
    request: &GetFlowStateRequest,
    is_hydrating: bool,
    step_queues: &dyn StepQueueStatus,
    is_rpc_lock_held: &dyn Fn(RunId, &str) -> bool,
) -> Result<GetFlowStateResponse, FlowCoordinationError> {
    let run = read_run(state, context, flow, &request.run_id)?;
    let mut response = get_flow_state(state, context.flow_hash, run.run_id)
        .map_err(|error| read_model_error(flow, error))?;
    for step in &mut response.active_step_executions {
        let invocations = [
            step.wait_for_invocation.as_mut(),
            step.execute_invocation.as_mut(),
        ];
        for invocation in invocations.into_iter().flatten() {
            let is_waiting_for_worker =
                invocation.pending_reason == StepMethodPendingReason::WaitingForWorker as i32;
            if !is_waiting_for_worker {
                continue;
            }
            let lock_keys =
                invocation_lock_keys(state, context, run.run_id, &step.step_execution_id)?;
            if lock_keys
                .iter()
                .any(|lock_key| is_rpc_lock_held(run.run_id, lock_key))
            {
                invocation.pending_reason = StepMethodPendingReason::WaitingForLocks as i32;
            } else if step_queues.is_waiting_for_supporting_worker(
                flow.tenant_id.0,
                dexzd_apply::run_task_queue(&run.record),
                &run.record.flow_type,
                &step.step_type,
            ) {
                invocation.pending_reason =
                    StepMethodPendingReason::WaitingForSupportingWorker as i32;
            }
        }
    }
    if is_hydrating {
        for value in flow_state_values(&mut response) {
            if !hydrate_value_reference(state, context.flow_hash, run.run_id, value)? {
                return Err(FlowCoordinationError::Internal {
                    detail: format!(
                        "a GetFlowState value of run {} has no value row",
                        run.run_id
                    ),
                });
            }
        }
    }
    Ok(response)
}

/// Returns the lock keys of the Step execution's current invocation.
fn invocation_lock_keys(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    run_id: RunId,
    step_execution_id: &str,
) -> Result<Vec<String>, FlowCoordinationError> {
    let key = run_named_key(
        context.flow_hash,
        run_id,
        RunTable::StepExecution,
        step_execution_id,
    );
    let step: Option<StepExecutionRecord> =
        decode_row(state, ColumnFamily::Flow, &key, "Step execution")?;
    Ok(step
        .and_then(|step| step.invocation)
        .map(|invocation| invocation.lock_keys)
        .unwrap_or_default())
}

/// Returns the history page that apply's read model projects from `state` and the loaded
/// segments, or the uploaded segments it still needs, with the run it read. With
/// `should_end_before_unloaded_segment`, a shorter page that stops at the first unloaded
/// segment replaces the list when it makes progress.
///
/// `run_id` names the run as a request does; empty selects the Flow's current run. Returns
/// `NOT_FOUND` + `FLOW_NOT_EXISTS` when the run does not exist, `INVALID_ARGUMENT` for a
/// negative position or a malformed page token, and `RESOURCE_EXHAUSTED` when the first event
/// alone exceeds `max_response_bytes`.
#[expect(
    clippy::too_many_arguments,
    reason = "mirrors get_history_events_from_segments plus the Flow address"
)]
pub(crate) fn read_history_page(
    state: &dyn FlowStateReader,
    context: ApplyContext,
    flow: &FlowAddress,
    run_id: &str,
    request: &GetHistoryEventsRequest,
    max_response_bytes: usize,
    segments: &LoadedHistorySegments,
    should_end_before_unloaded_segment: bool,
) -> Result<(RunId, SegmentedHistoryPage), FlowCoordinationError> {
    let run = read_run(state, context, flow, run_id)?;
    get_history_events_from_segments(
        state,
        context.flow_hash,
        run.run_id,
        request.start_internal_event_id,
        request.estimate_page_size,
        &request.next_page_token,
        request.run_attempt,
        max_response_bytes,
        segments,
        should_end_before_unloaded_segment,
    )
    .map(|page| (run.run_id, page))
    .map_err(|error| read_model_error(flow, error))
}

/// Returns every value of a GetFlowState response.
fn flow_state_values(response: &mut GetFlowStateResponse) -> Vec<&mut Value> {
    let mut values: Vec<&mut Value> = Vec::new();
    values.extend(kv_values(&mut response.attributes));
    for step in &mut response.active_step_executions {
        values.extend(
            step.movement
                .as_mut()
                .and_then(|movement| movement.step_input.as_mut()),
        );
        if let Some(conditions) = step.completed_conditions.as_mut() {
            for result in conditions.completed_sub_flow_results.values_mut() {
                values.extend(step_output_values(&mut result.results));
            }
        }
        values.extend(kv_values(&mut step.step_execution_locals));
    }
    for movement in &mut response.queued_steps {
        values.extend(movement.step_input.as_mut());
    }
    for channel in response.pending_channel_messages.values_mut() {
        values.extend(
            channel
                .messages
                .iter_mut()
                .filter_map(|message| message.value.as_mut()),
        );
    }
    values.extend(step_output_values(&mut response.completed_steps));
    values
}

fn kv_values(kvs: &mut [Kv]) -> impl Iterator<Item = &mut Value> {
    kvs.iter_mut().filter_map(|kv| kv.value.as_mut())
}

fn step_output_values(outputs: &mut [StepCompletionOutput]) -> impl Iterator<Item = &mut Value> {
    outputs
        .iter_mut()
        .filter_map(|output| output.completed_step_output.as_mut())
}

fn read_model_error(flow: &FlowAddress, error: ReadModelError) -> FlowCoordinationError {
    match error {
        ReadModelError::RunNotFound { .. } => FlowCoordinationError::flow_not_found(&flow.flow_id),
        ReadModelError::InvalidRequest { detail } => {
            FlowCoordinationError::invalid_argument(detail)
        }
        ReadModelError::State(error) => error.into(),
        error @ ReadModelError::UnsupportedAttributeValue { .. } => {
            FlowCoordinationError::Internal {
                detail: error.to_string(),
            }
        }
        error @ ReadModelError::HistoryEventTooLarge { .. } => FlowCoordinationError::rejected(
            grpc_code::RESOURCE_EXHAUSTED,
            ErrorSubStatus::Uncategorized,
            error.to_string(),
        ),
    }
}
