// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Child runs that a time travel's owner reads for SubFlow reset policies; apply never reads them.

use dexzd_keyspace::{PartitionHasher, RunId, TenantId};
use dexzd_protocol::dex::{FlowResult, FlowStatus, Value};
use dexzd_protocol::dexzd::v1::{
    ChildFlowStartMessage, ParentFlowReference, RunRecord, flow_command::Command,
    inbox_message_command,
};

use crate::history::{HistorySource, LocalHistorySource, genesis_seq};
use crate::state_access::{FlowTransaction, RunRows, read_flow_directory};
use crate::values::{ValueLimits, complete_worker_value};
use crate::{ApplyContext, ApplyError, FlowStateReader};

/// Names the child run of one restored SubFlow condition.
#[derive(Debug, Clone, Copy)]
pub struct ChildFlowRunQuery<'query> {
    /// Tenant of the parent and child Flows.
    pub tenant_id: TenantId,
    /// Child Flow ID of the condition.
    pub child_flow_id: &'query str,
    /// Start request ID that the parent used for the condition's child start.
    pub start_request_id: &'query str,
    /// The condition's parent reference, which the child run reports to.
    pub parent: &'query ParentFlowReference,
    /// Whether to read the start the child run received.
    pub should_read_start: bool,
}

/// What an owner read from a child run.
#[derive(Debug, Clone, PartialEq)]
pub struct ChildFlowRun {
    /// The child run.
    pub run_id: RunId,
    /// The child run's current attempt.
    pub run_attempt: u32,
    /// The final result the run reports to its parents once it ended; `None` while it runs.
    pub ended_result: Option<FlowResult>,
    /// The start the run received, when the query asked for it.
    pub start: Option<ChildFlowStartRead>,
}

/// The start a child run received, or why a restart cannot repeat it.
#[derive(Debug, Clone, PartialEq)]
pub enum ChildFlowStartRead {
    /// The start, with complete values.
    Available(Box<ChildFlowStartMessage>),
    /// Why a restart cannot repeat the start.
    Unavailable(String),
}

/// Reads child runs of restored SubFlow conditions from committed state, possibly on other
/// partitions.
pub trait ChildFlowRuns {
    /// Returns the child run that `query` names: the latest run that its start request ID
    /// started, or else the latest run that reports to its parent reference.
    ///
    /// Returns `None` when the child Flow has no such run, for example after the run was purged.
    /// Returns an error when stored state is corrupt.
    fn child_flow_run(
        &self,
        query: &ChildFlowRunQuery<'_>,
    ) -> Result<Option<ChildFlowRun>, ApplyError>;
}

/// The [`ChildFlowRuns`] that finds no child run, for computations without child Flow reads.
#[derive(Debug, Default, Clone, Copy)]
pub struct NoChildFlowRuns;

impl ChildFlowRuns for NoChildFlowRuns {
    fn child_flow_run(
        &self,
        _query: &ChildFlowRunQuery<'_>,
    ) -> Result<Option<ChildFlowRun>, ApplyError> {
        Ok(None)
    }
}

/// The [`ChildFlowRuns`] over committed state that holds the child Flows' partitions.
pub struct StateChildFlowRuns<'state> {
    state: &'state dyn FlowStateReader,
    hasher: &'state PartitionHasher,
}

impl<'state> StateChildFlowRuns<'state> {
    /// Reads child runs from `state`, placing each child Flow with `hasher`.
    pub fn new(state: &'state dyn FlowStateReader, hasher: &'state PartitionHasher) -> Self {
        Self { state, hasher }
    }
}

impl ChildFlowRuns for StateChildFlowRuns<'_> {
    fn child_flow_run(
        &self,
        query: &ChildFlowRunQuery<'_>,
    ) -> Result<Option<ChildFlowRun>, ApplyError> {
        let flow_hash = self.hasher.flow_hash(query.tenant_id, query.child_flow_id);
        let context = ApplyContext {
            partition: self.hasher.partition_of(flow_hash),
            flow_hash,
        };
        let transaction = FlowTransaction::new(self.state, context);
        let Some((rows, record)) = find_child_run(&transaction, query)? else {
            return Ok(None);
        };
        let ended_result = if record.status == FlowStatus::Running as i32 {
            None
        } else {
            Some(crate::cross_flow::closed_run_flow_result(
                &transaction,
                rows,
                &record,
            )?)
        };
        let start = if query.should_read_start {
            Some(read_genesis_start(&transaction, rows, &record)?)
        } else {
            None
        };
        Ok(Some(ChildFlowRun {
            run_id: rows.run_id,
            run_attempt: record.current_attempt,
            ended_result,
            start,
        }))
    }
}

/// Returns the latest run that the start request ID started, or else the latest run that reports
/// to the parent reference. A purging run counts as gone.
fn find_child_run(
    transaction: &FlowTransaction<'_>,
    query: &ChildFlowRunQuery<'_>,
) -> Result<Option<(RunRows, RunRecord)>, ApplyError> {
    let directory = read_flow_directory(transaction, query.tenant_id, query.child_flow_id)?;
    let started_run = directory
        .runs
        .iter()
        .rev()
        .find(|run| run.start_request_id == query.start_request_id);
    let candidates: Vec<_> = match started_run {
        Some(run) => vec![run],
        None => directory.runs.iter().rev().collect(),
    };
    for entry in candidates {
        let run_id =
            RunId::from_slice(&entry.run_id).map_err(|error| ApplyError::InvariantViolation {
                detail: format!(
                    "the directory of child Flow {} has a malformed run ID: {error}",
                    query.child_flow_id
                ),
            })?;
        let rows = RunRows::new(transaction.context().flow_hash, run_id);
        let Some(record) = rows.run_record(transaction)? else {
            continue;
        };
        if record.is_purging {
            continue;
        }
        let is_attached = started_run.is_some()
            || record.parent.as_ref() == Some(query.parent)
            || record.attached_parents.contains(query.parent);
        if is_attached {
            return Ok(Some((rows, record)));
        }
    }
    Ok(None)
}

/// Reads the child start that the run's genesis history record holds, which is never pruned.
///
/// Returns why a restart cannot repeat it when the run was not started by a parent, or when the
/// start carried hard-delete Attributes, whose objects the child run owns.
fn read_genesis_start(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
) -> Result<ChildFlowStartRead, ApplyError> {
    let genesis = genesis_seq(transaction, rows)?;
    let history = LocalHistorySource::new(transaction, rows);
    let Some(genesis_record) = history.history_records(genesis..=genesis, 1).next() else {
        return Ok(ChildFlowStartRead::Unavailable(
            "the child run no longer holds its genesis history record".to_owned(),
        ));
    };
    let body = genesis_record?.command.and_then(|command| command.command);
    let Some(Command::InboxMessage(inbox)) = body else {
        return Ok(ChildFlowStartRead::Unavailable(
            "the child run was not started by its parent".to_owned(),
        ));
    };
    let Some(inbox_message_command::Payload::ChildStart(mut start)) = inbox.payload else {
        return Ok(ChildFlowStartRead::Unavailable(
            "the child run was not started by its parent".to_owned(),
        ));
    };
    if !start.hard_delete_objects.is_empty() {
        return Ok(ChildFlowStartRead::Unavailable(
            "its start wrote hard-delete Attributes, which a restart cannot repeat".to_owned(),
        ));
    }
    let limits = ValueLimits {
        wire_inline_threshold_bytes: record
            .settings
            .unwrap_or_default()
            .wire_inline_threshold_bytes,
        max_value_bytes: u64::MAX,
    };
    let Some(condition) = start.condition.as_mut() else {
        return Ok(ChildFlowStartRead::Available(Box::new(start)));
    };
    let attribute_values = condition
        .options
        .iter_mut()
        .flat_map(|options| options.attributes.iter_mut())
        .filter_map(|write| write.value.as_mut());
    for value in condition.step_input.iter_mut().chain(attribute_values) {
        if let Err(reason) = complete_start_value(transaction, rows, limits, value)? {
            return Ok(ChildFlowStartRead::Unavailable(reason));
        }
    }
    Ok(ChildFlowStartRead::Available(Box::new(start)))
}

/// Replaces a value reference of the child run, which history records may hold, with its value.
///
/// Returns why the value cannot be completed.
fn complete_start_value(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    limits: ValueLimits,
    value: &mut Value,
) -> Result<Result<(), String>, ApplyError> {
    match complete_worker_value(transaction, rows, limits, value)? {
        Ok(complete) => {
            *value = complete;
            Ok(Ok(()))
        }
        Err(rejection) => Ok(Err(format!(
            "its start holds a value it no longer stores: {}",
            rejection.message()
        ))),
    }
}
