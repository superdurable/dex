// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The owner's computation of a CloneForkFlow: the source point's state, every value it holds,
//! and the child Flow findings, ready to stage on the new Flow's partition.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::row_keys::value_row_key;
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, ValueReference};
use dexzd_protocol::dex::{
    CloneForkFlowRequest, ErrorSubStatus, FlowCloneForkPointType, FlowCloneForkSource, FlowConfig,
    FlowResetType, FlowStatus, FlowTimeoutHandlerOptions, ResetFlowRequest,
};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, CheckpointRow, CommandReply, FlowCloneForkStageCommand,
    FlowCloneForkStagedTarget, HardDeleteObjectReference, RunRecord, StoredValueId,
    TimeTravelStagedChildFlow, TimeTravelStagedValue, ValueRecord, stored_value, timer_id,
};
use prost::Message;

use super::copied_values::referenced_table_values;
use super::staging::{CloneForkStagedEntryTooLarge, StagedCloneForkParts, stage_commands};
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::command_handlers::run_cursor::flow_not_found_reply;
use crate::command_handlers::step_commands::flow_timeout_duration_micros;
use crate::history::attempt_scope::{
    AttemptRow, decode_timer_id, holder_rows, read_attempt_rows, run_status_of,
};
use crate::history::lineage::{HistorySource, LocalHistorySource};
use crate::history::reset_target::TravelTarget;
use crate::history::state_rebuild::{
    HistoryPoint, ReplayMemoryBudget, ReplayResources, encoded_bytes, replay_memory_exceeded_reply,
};
use crate::history::time_travel::{
    ChildFlowSources, GoneChildFlowCondition, SubFlowFindingRequest, SubFlowResetOperation,
    TimeTravelComputationSources, TimeTravelResult, compute_point_state, reset_policy,
    sub_flow_findings,
};
use crate::history_offload::segmented_history::{
    LoadedHistorySegments, MissingHistorySegments, SegmentedHistorySource, UnloadedSegmentAction,
};
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::OffloadedValues;
use crate::{ApplyContext, ApplyError, FlowStateReader};

/// A CloneForkFlow that an owner computes before it stages the copy.
pub struct CloneForkComputationRequest<'request> {
    /// The source run.
    pub source_run_id: RunId,
    /// The client's request.
    pub clone_fork: &'request CloneForkFlowRequest,
    /// The owner's current time, which HISTORY_EVENT_TIME points are checked against.
    pub now_micros: i64,
    /// Largest number of bytes the computation may hold in memory, for replay, rows, and values.
    pub memory_budget_bytes: u64,
}

/// The outcome of [`compute_clone_fork`].
#[derive(Debug)]
pub enum CloneForkComputation {
    /// The copy, ready to stage.
    Computed(Box<ComputedCloneFork>),
    /// The reply that rejects the request.
    Rejected(CommandReply),
}

/// The outcome of [`compute_clone_fork_from_segments`].
#[derive(Debug)]
pub enum SegmentedCloneForkComputation {
    /// The computation read every history record and checkpoint it needed.
    Computed(CloneForkComputation),
    /// Like `Computed`, but a condition's child looked gone; views of different times may need
    /// a new round.
    ComputedWithGoneChildFlow {
        /// The computation of this round.
        computation: CloneForkComputation,
        /// The conditions whose child looked gone; never empty.
        gone_child_flows: BTreeSet<GoneChildFlowCondition>,
    },
    /// The computation reached uploaded segments that are not loaded. Fetch them, then compute
    /// again on a new committed view.
    NeedsSegments(MissingHistorySegments),
}

/// A source run's current hard-delete Attribute, which a clone fork uploads again for the new
/// run, since its object belongs to the source run.
#[derive(Debug, Clone, PartialEq)]
pub struct CloneForkHardDeleteAttribute {
    /// The Attribute key.
    pub key: String,
    /// The source run's object that holds the value.
    pub object: HardDeleteObjectReference,
}

/// The computed copy of a source point and what the new run takes from the source run.
#[derive(Debug)]
pub struct ComputedCloneFork {
    target: FlowCloneForkStagedTarget,
    rows: Vec<CheckpointRow>,
    values: Vec<TimeTravelStagedValue>,
    child_flows: Vec<TimeTravelStagedChildFlow>,
    flow_config: FlowConfig,
    flow_timeout_duration_micros: i64,
    timeout_policy: i32,
    timeout_handler_options: Option<FlowTimeoutHandlerOptions>,
    hard_delete_attributes: Vec<CloneForkHardDeleteAttribute>,
    state_bytes: u64,
}

impl ComputedCloneFork {
    /// Returns the source point: the source run, its run attempt, and the last event the point
    /// includes.
    pub fn source(&self) -> &FlowCloneForkSource {
        self.target
            .source
            .as_ref()
            .expect("a computed clone fork names its source")
    }

    /// Returns the source run's Flow configuration, which the request's override changes.
    pub fn source_flow_config(&self) -> &FlowConfig {
        &self.flow_config
    }

    /// Returns the Flow timeout of the new run in microseconds, counted from the clone fork;
    /// zero means none. The computation rejected a request that sets a timeout after the source
    /// point's timeout fired.
    pub fn flow_timeout_duration_micros(&self) -> i64 {
        self.flow_timeout_duration_micros
    }

    /// Returns the source run's Flow timeout policy.
    pub fn timeout_policy(&self) -> i32 {
        self.timeout_policy
    }

    /// Returns the source run's Flow timeout handler options.
    pub fn timeout_handler_options(&self) -> Option<&FlowTimeoutHandlerOptions> {
        self.timeout_handler_options.as_ref()
    }

    /// Returns the source run's current hard-delete Attributes, which the new run receives.
    pub fn hard_delete_attributes(&self) -> &[CloneForkHardDeleteAttribute] {
        &self.hard_delete_attributes
    }

    /// Returns the encoded bytes of the copied rows and values, about the active state they give
    /// the new run.
    pub fn state_bytes(&self) -> u64 {
        self.state_bytes
    }

    /// Returns the FlowCloneForkStage commands that stage the copy under `staging_id`.
    ///
    /// Chunks hold at most 8 MiB of entries or one larger entry; chunk 0 carries the target.
    ///
    /// Returns [`CloneForkStagedEntryTooLarge`] when an entry cannot fit in one log record.
    pub fn stage_commands(
        &self,
        staging_id: &[u8],
    ) -> Result<Vec<FlowCloneForkStageCommand>, CloneForkStagedEntryTooLarge> {
        stage_commands(
            staging_id,
            &StagedCloneForkParts {
                target: &self.target,
                rows: &self.rows,
                values: &self.values,
                child_flows: &self.child_flows,
            },
        )
    }
}

/// Computes what a CloneForkFlow of `request` copies from its source run.
///
/// Returns [`CloneForkComputation::Rejected`] with NOT_FOUND for a missing or purging source
/// run, with the reply ResetFlow gives for a point it cannot reach, with FAILED_PRECONDITION
/// when the point's state is not RUNNING, holds a value no source has, or the SubFlow reset
/// policy rejects a copied wait, and with RESOURCE_EXHAUSTED past the memory budget. Returns an
/// error when stored state is corrupt.
pub fn compute_clone_fork(
    context: &ApplyContext,
    sources: &TimeTravelComputationSources<'_>,
    request: &CloneForkComputationRequest<'_>,
) -> Result<CloneForkComputation, ApplyError> {
    compute_noting_gone_child_flows(context, sources, request)
        .map(|(computation, _gone_child_flows)| computation)
}

/// Computes as [`compute_clone_fork`] does.
///
/// Returns the computation and the copied conditions whose child looked gone, or an error when
/// stored state is corrupt.
fn compute_noting_gone_child_flows(
    context: &ApplyContext,
    sources: &TimeTravelComputationSources<'_>,
    request: &CloneForkComputationRequest<'_>,
) -> Result<(CloneForkComputation, BTreeSet<GoneChildFlowCondition>), ApplyError> {
    let rejected = |reply| Ok((CloneForkComputation::Rejected(reply), BTreeSet::new()));
    let transaction = FlowTransaction::new(sources.state, *context);
    let rows = RunRows::new(context.flow_hash, request.source_run_id);
    let clone_fork = request.clone_fork;
    let record = match rows.run_record(&transaction)? {
        Some(record) if !record.is_purging => record,
        _ => return rejected(flow_not_found_reply(&clone_fork.source_flow_id)),
    };
    let memory_budget = ReplayMemoryBudget::new(request.memory_budget_bytes);
    let resources = ReplayResources {
        offloaded_values: sources.offloaded_values,
        memory_budget: &memory_budget,
    };
    let point = match point_state(
        &transaction,
        sources.history,
        rows,
        &record,
        request,
        resources,
    )? {
        Ok(point) => point,
        Err(reply) => return rejected(reply),
    };
    if let Err(reply) = check_running_point(&record, &point) {
        return rejected(reply);
    }
    let hard_delete_attributes = current_hard_delete_attributes(&transaction, rows)?;
    let hard_delete_keys: BTreeSet<&[u8]> = hard_delete_attributes
        .iter()
        .map(|attribute| attribute.key.as_bytes())
        .collect();
    let mut copied_rows = Vec::with_capacity(point.rows.len());
    for row in &point.rows {
        if !row.is_hard_delete_attribute()? && !hard_delete_keys.contains(row.key_suffix.as_slice())
        {
            copied_rows.push(row.clone());
        }
    }
    let flow_timeout_duration_micros =
        match new_flow_timeout(&record, &copied_rows, clone_fork.flow_timeout_seconds)? {
            Ok(duration) => duration,
            Err(reply) => return rejected(reply),
        };
    let policy = match reset_policy(clone_fork.sub_flow_reset_policy) {
        Ok(policy) => policy,
        Err(reply) => return rejected(reply),
    };
    let findings = sub_flow_findings(&SubFlowFindingRequest {
        transaction: &transaction,
        rows,
        record: &record,
        policy,
        operation: SubFlowResetOperation::CloneFork,
        restored_rows: &copied_rows,
        point: point.target.point,
        sources: ChildFlowSources {
            child_flow_runs: sources.child_flow_runs,
            memory_budget: &memory_budget,
        },
    })?;
    let gone_child_flows = findings.gone_child_flows;
    let child_flows = match findings.outcome {
        Ok(findings) => findings,
        Err(reply) => return Ok((CloneForkComputation::Rejected(reply), gone_child_flows)),
    };
    let checkpoint_rows: Vec<CheckpointRow> = copied_rows
        .iter()
        .map(AttemptRow::to_checkpoint_row)
        .collect();
    let values = match copied_values(&transaction, rows, &point, &copied_rows, resources)? {
        Ok(values) => values,
        Err(reply) => return Ok((CloneForkComputation::Rejected(reply), gone_child_flows)),
    };
    let target = staged_target(&record, &point, clone_fork, request.source_run_id);
    let row_bytes = checkpoint_rows
        .iter()
        .map(|row| encoded_bytes(row.encoded_len()))
        .fold(0, u64::saturating_add);
    let value_bytes = values
        .iter()
        .map(|value| encoded_bytes(value.encoded_len()))
        .fold(0, u64::saturating_add);
    // Staging encodes every entry once more.
    if !memory_budget.reserve(row_bytes.saturating_add(value_bytes)) {
        return Ok((
            CloneForkComputation::Rejected(replay_memory_exceeded_reply(
                point.target.point,
                memory_budget.limit_bytes(),
                "the staged clone fork",
            )),
            gone_child_flows,
        ));
    }
    Ok((
        CloneForkComputation::Computed(Box::new(ComputedCloneFork {
            target,
            rows: checkpoint_rows,
            values,
            child_flows,
            flow_config: record.flow_config.clone().unwrap_or_default(),
            flow_timeout_duration_micros,
            timeout_policy: record.timeout_policy,
            timeout_handler_options: record.timeout_handler_options.clone(),
            hard_delete_attributes,
            state_bytes: row_bytes.saturating_add(value_bytes),
        })),
        gone_child_flows,
    ))
}

/// Computes `request` as [`compute_clone_fork`] does, reading the source run's history from
/// local rows and from the uploaded segments that `segments` holds.
///
/// Returns [`SegmentedCloneForkComputation::NeedsSegments`] when the computation reached
/// uploaded segments that `segments` lacks; its result is discarded then. Returns an error when
/// stored state is corrupt.
pub fn compute_clone_fork_from_segments(
    context: &ApplyContext,
    state: &dyn FlowStateReader,
    segments: &LoadedHistorySegments,
    offloaded_values: &dyn OffloadedValues,
    child_flow_runs: &dyn crate::history::ChildFlowRuns,
    request: &CloneForkComputationRequest<'_>,
) -> Result<SegmentedCloneForkComputation, ApplyError> {
    let transaction = FlowTransaction::new(state, *context);
    let rows = RunRows::new(context.flow_hash, request.source_run_id);
    let segmented = match rows.run_record(&transaction)? {
        // An unlimited catalog budget never ends a stream early.
        Some(record) if !record.is_purging => Some(SegmentedHistorySource::open(
            &transaction,
            rows,
            &record,
            segments,
            UnloadedSegmentAction::Report,
            usize::MAX,
        )?),
        _ => None,
    };
    let local = LocalHistorySource::new(&transaction, rows);
    let history: &dyn HistorySource = match &segmented {
        Some(segmented) => segmented,
        None => &local,
    };
    let computation = compute_noting_gone_child_flows(
        context,
        &TimeTravelComputationSources {
            state,
            history,
            offloaded_values,
            child_flow_runs,
        },
        request,
    );
    if let Some(segmented) = &segmented {
        let missing = segmented.take_replay_misses()?;
        if !missing.is_empty() {
            return Ok(SegmentedCloneForkComputation::NeedsSegments(missing));
        }
        if let Some(page_end) = segmented.page_end() {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "a clone fork read of run {} ended history early at run seq {}",
                    request.source_run_id, page_end.seq
                ),
            });
        }
    }
    computation.map(|(computation, gone_child_flows)| {
        if gone_child_flows.is_empty() {
            SegmentedCloneForkComputation::Computed(computation)
        } else {
            SegmentedCloneForkComputation::ComputedWithGoneChildFlow {
                computation,
                gone_child_flows,
            }
        }
    })
}

/// Returns the state at the point the request selects, with the values the store lacks.
///
/// Returns the reply that rejects the request, or an error when stored state is corrupt.
fn point_state(
    transaction: &FlowTransaction<'_>,
    history: &dyn HistorySource,
    rows: RunRows,
    record: &RunRecord,
    request: &CloneForkComputationRequest<'_>,
    resources: ReplayResources<'_>,
) -> Result<Result<TimeTravelResult, CommandReply>, ApplyError> {
    let clone_fork = request.clone_fork;
    let point_type = FlowCloneForkPointType::try_from(clone_fork.point_type)
        .unwrap_or(FlowCloneForkPointType::Unspecified);
    let reset_type = match point_type {
        FlowCloneForkPointType::Unspecified | FlowCloneForkPointType::Current => {
            return current_point_state(transaction, rows, record, clone_fork, resources);
        }
        FlowCloneForkPointType::Beginning => FlowResetType::Beginning,
        FlowCloneForkPointType::HistoryEventTime => FlowResetType::HistoryEventTime,
        FlowCloneForkPointType::StepType => FlowResetType::StepType,
        FlowCloneForkPointType::StepExecutionId => FlowResetType::StepExecutionId,
        FlowCloneForkPointType::EventId => FlowResetType::EventId,
    };
    let reset = ResetFlowRequest {
        flow_id: clone_fork.source_flow_id.clone(),
        run_id: request.source_run_id.to_string(),
        reset_type: reset_type as i32,
        reason: clone_fork.reason.clone(),
        history_event_time: clone_fork.history_event_time.clone(),
        step_type: clone_fork.step_type.clone(),
        step_execution_id: clone_fork.step_execution_id.clone(),
        skip_writes_reapply: true,
        step_method: clone_fork.step_method,
        run_attempt: clone_fork.run_attempt,
        sub_flow_reset_policy: clone_fork.sub_flow_reset_policy,
        event_id: clone_fork.event_id,
    };
    compute_point_state(
        transaction,
        history,
        rows,
        record,
        &reset,
        request.now_micros,
        resources,
    )
}

/// Returns the committed state of the source run's current attempt.
fn current_point_state(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
    clone_fork: &CloneForkFlowRequest,
    resources: ReplayResources<'_>,
) -> Result<Result<TimeTravelResult, CommandReply>, ApplyError> {
    if clone_fork.run_attempt != 0 {
        return Ok(Err(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            "the CURRENT point selects the current run attempt, so run_attempt must be zero",
        )));
    }
    let point = HistoryPoint {
        attempt: record.current_attempt,
        seq: record.next_run_seq.saturating_sub(1),
    };
    let attempt_rows = read_attempt_rows(transaction, rows)?;
    let row_bytes = attempt_rows
        .iter()
        .map(AttemptRow::encoded_bytes)
        .fold(0, u64::saturating_add);
    if !resources.memory_budget.reserve(row_bytes) {
        return Ok(Err(replay_memory_exceeded_reply(
            point,
            resources.memory_budget.limit_bytes(),
            "the current state",
        )));
    }
    Ok(Ok(TimeTravelResult {
        selected_attempt: record.current_attempt,
        target: TravelTarget {
            point,
            last_event_id: record.next_event_id.saturating_sub(1),
            close_history_message: None,
        },
        run_status: run_status_of(record),
        rows: attempt_rows,
        unstored_values: Vec::new(),
        reapplied_inputs: Vec::new(),
        child_flows: None,
    }))
}

/// Checks that the point's state is RUNNING and not closing.
///
/// Returns the FAILED_PRECONDITION reply that asks for an earlier point.
fn check_running_point(record: &RunRecord, point: &TimeTravelResult) -> Result<(), CommandReply> {
    let status = &point.run_status;
    let state = if status.status != FlowStatus::Running as i32 {
        FlowStatus::try_from(status.status)
            .unwrap_or(FlowStatus::Unspecified)
            .as_str_name()
            .to_owned()
    } else if status.pending_close.is_some() || status.is_graceful_completion_requested {
        "closing".to_owned()
    } else {
        return Ok(());
    };
    Err(error_reply(
        grpc_code::FAILED_PRECONDITION,
        ErrorSubStatus::Uncategorized,
        format!(
            "the state of run {} of Flow {} at the selected point (run attempt {}, event {}) is \
             {state}; a clone fork copies only a RUNNING state, so select an earlier point",
            RunId::from_slice(&record.run_id)
                .map(|run_id| run_id.to_string())
                .unwrap_or_default(),
            record.flow_id,
            point.target.point.attempt,
            point.target.last_event_id
        ),
    ))
}

/// Returns the Flow timeout of the new run: the requested one, else the source run's duration.
///
/// Returns FAILED_PRECONDITION when the request sets a timeout but the source's had already
/// fired at the point, whose state then keeps that outcome.
fn new_flow_timeout(
    record: &RunRecord,
    copied_rows: &[AttemptRow],
    requested_seconds: i32,
) -> Result<Result<i64, CommandReply>, ApplyError> {
    let source_duration_micros = flow_timeout_duration_micros(record);
    let mut has_armed_timeout = false;
    for row in copied_rows
        .iter()
        .filter(|row| row.table == RunTable::Timer)
    {
        let timer = decode_timer_id(&row.key_suffix)?;
        has_armed_timeout |= matches!(timer.kind, Some(timer_id::Kind::FlowTimeout(_)));
    }
    let has_fired = source_duration_micros > 0 && !has_armed_timeout;
    if has_fired {
        if requested_seconds > 0 {
            return Ok(Err(error_reply(
                grpc_code::FAILED_PRECONDITION,
                ErrorSubStatus::Uncategorized,
                "the Flow timeout of the source run had already fired at the selected point, \
                 so the new run keeps that outcome and flow_timeout_seconds must be zero",
            )));
        }
        return Ok(Ok(0));
    }
    if requested_seconds > 0 {
        return Ok(Ok(i64::from(requested_seconds) * 1_000_000));
    }
    Ok(Ok(source_duration_micros))
}

/// Returns the source run's current hard-delete Attributes that hold a value.
fn current_hard_delete_attributes(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
) -> Result<Vec<CloneForkHardDeleteAttribute>, ApplyError> {
    let mut attributes = Vec::new();
    for row in read_attempt_rows(transaction, rows)? {
        if row.table != RunTable::Attribute {
            continue;
        }
        let record = AttributeRecord::decode(row.value.as_slice()).map_err(|error| {
            ApplyError::InvariantViolation {
                detail: format!("an Attribute row is corrupt: {error}"),
            }
        })?;
        if !record.is_hard_delete_on_null {
            continue;
        }
        let Some(stored_value::Kind::HardDeleteObject(object)) =
            record.value.and_then(|value| value.kind)
        else {
            continue;
        };
        let key =
            String::from_utf8(row.key_suffix).map_err(|error| ApplyError::InvariantViolation {
                detail: format!("an Attribute key is invalid UTF-8: {error}"),
            })?;
        attributes.push(CloneForkHardDeleteAttribute { key, object });
    }
    Ok(attributes)
}

/// Returns every value the copied rows reference, complete, in value ID order.
///
/// Each value reserves its size first. Returns FAILED_PRECONDITION naming a value that neither
/// the store, the computation, nor the offloaded values have, and RESOURCE_EXHAUSTED past the
/// memory budget.
fn copied_values(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    point: &TimeTravelResult,
    copied_rows: &[AttemptRow],
    resources: ReplayResources<'_>,
) -> Result<Result<Vec<TimeTravelStagedValue>, CommandReply>, ApplyError> {
    let mut unstored: BTreeMap<(u64, u32), &TimeTravelStagedValue> = point
        .unstored_values
        .iter()
        .filter_map(|staged| {
            staged
                .value_id
                .map(|id| ((id.run_seq, id.index_in_command), staged))
        })
        .collect();
    let referenced = referenced_table_values(&holder_rows(copied_rows, &point.run_status))?;
    let mut values = Vec::with_capacity(referenced.len());
    let mut first_unavailable = None;
    for (value_id, size_bytes) in referenced {
        if !resources.memory_budget.reserve(size_bytes) {
            return Ok(Err(replay_memory_exceeded_reply(
                point.target.point,
                resources.memory_budget.limit_bytes(),
                "the values the copied state holds",
            )));
        }
        let key = (value_id.run_seq, value_id.index_in_command);
        let value = match unstored.remove(&key) {
            Some(staged) => staged.value.clone(),
            None => {
                let stored: Option<ValueRecord> = transaction.get_message(
                    ColumnFamily::Value,
                    &value_row_key(rows.flow_hash, rows.run_id, RunTable::Value, value_id),
                    "value",
                )?;
                match stored.and_then(|record| record.value) {
                    Some(value) => Some(value),
                    None => resources
                        .offloaded_values
                        .offloaded_value(rows.run_id, value_id)?,
                }
            }
        };
        // Every value is asked for, so an owner learns all it must fetch at once.
        let Some(value) = value else {
            first_unavailable.get_or_insert(value_id);
            continue;
        };
        values.push(TimeTravelStagedValue {
            value_id: Some(StoredValueId {
                run_seq: value_id.run_seq,
                index_in_command: value_id.index_in_command,
            }),
            value: Some(value),
        });
    }
    if let Some(value_id) = first_unavailable {
        let reference = ValueReference {
            run_id: rows.run_id,
            value_id,
        };
        return Ok(Err(error_reply(
            grpc_code::FAILED_PRECONDITION,
            ErrorSubStatus::Uncategorized,
            format!(
                "the state at the selected point holds value {reference}, which the source run \
                 no longer stores; select a later point"
            ),
        )));
    }
    Ok(Ok(values))
}

fn staged_target(
    record: &RunRecord,
    point: &TimeTravelResult,
    clone_fork: &CloneForkFlowRequest,
    source_run_id: RunId,
) -> FlowCloneForkStagedTarget {
    FlowCloneForkStagedTarget {
        request: Some(clone_fork.clone()),
        source: Some(FlowCloneForkSource {
            flow_id: record.flow_id.clone(),
            run_id: source_run_id.to_string(),
            run_attempt: i32::try_from(point.target.point.attempt).unwrap_or(i32::MAX),
            event_id: point.target.last_event_id,
        }),
        source_run_seq: point.target.point.seq,
        first_run_seq: record.next_run_seq.max(point.target.point.seq + 1),
        flow_type: record.flow_type.clone(),
        run_status: Some(point.run_status.clone()),
    }
}
