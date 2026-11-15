// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Child runs that a time travel's owner reads for SubFlow reset policies; apply never reads them.

use dexzd_keyspace::row_keys::{history_record_key, history_row_metadata_key, run_singleton_key};
use dexzd_keyspace::{
    ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, flow_directory_key,
};
use dexzd_protocol::dex::{FlowResult, FlowStatus, Value};
use dexzd_protocol::dexzd::v1::{
    ChildFlowStartMessage, FlowDirectoryRecord, HistoryRecord, HistoryRowMetadata,
    ParentFlowReference, RunRecord, flow_command::Command, inbox_message_command,
};
use prost::Message;

use crate::history::genesis_seq;
use crate::history::state_rebuild::encoded_bytes;
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::{ValueLimits, complete_worker_value, referenced_value_size, stored_value_size};
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

/// The outcome of one [`ChildFlowRuns::child_flow_run`] read.
#[derive(Debug, Clone, PartialEq)]
pub enum ChildFlowRead {
    /// The child run the query names.
    Found(Box<ChildFlowRun>),
    /// The child Flow has no such run, for example after the run was purged.
    Missing,
    /// Holding what the read loads would exceed the computation's memory budget.
    MemoryExceeded,
}

/// The memory budget that child run reads reserve in.
pub trait ChildFlowReadMemory {
    /// Reserves `bytes` before the read loads or keeps them.
    ///
    /// Returns false, reserving nothing, when the reservation would exceed the budget.
    fn reserve_bytes(&self, bytes: u64) -> bool;

    /// Returns `bytes` of an earlier reservation whose data the read dropped.
    fn release_bytes(&self, bytes: u64);
}

/// Reads child runs of restored SubFlow conditions from committed state, possibly on other
/// partitions.
pub trait ChildFlowRuns {
    /// Reads the child run that `query` names: the latest run that its start request ID
    /// started, or else the latest run that reports to its parent reference.
    ///
    /// Before it loads each stored row, result, or value, the read reserves its size in
    /// `memory`. It releases what it drops, such as the child Flow's directory and the run
    /// records of other runs, so `memory` stays charged only for what the returned run holds.
    /// It stops when a reservation fails.
    ///
    /// Returns [`ChildFlowRead::Missing`] when the child Flow has no such run, and
    /// [`ChildFlowRead::MemoryExceeded`] when a reservation failed. Returns an error when stored
    /// state is corrupt.
    fn child_flow_run(
        &self,
        query: &ChildFlowRunQuery<'_>,
        memory: &dyn ChildFlowReadMemory,
    ) -> Result<ChildFlowRead, ApplyError>;
}

/// The [`ChildFlowRuns`] that finds no child run, for computations without child Flow reads.
#[derive(Debug, Default, Clone, Copy)]
pub struct NoChildFlowRuns;

impl ChildFlowRuns for NoChildFlowRuns {
    fn child_flow_run(
        &self,
        _query: &ChildFlowRunQuery<'_>,
        _memory: &dyn ChildFlowReadMemory,
    ) -> Result<ChildFlowRead, ApplyError> {
        Ok(ChildFlowRead::Missing)
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
        memory: &dyn ChildFlowReadMemory,
    ) -> Result<ChildFlowRead, ApplyError> {
        let flow_hash = self.hasher.flow_hash(query.tenant_id, query.child_flow_id);
        let context = ApplyContext {
            partition: self.hasher.partition_of(flow_hash),
            flow_hash,
        };
        let transaction = FlowTransaction::new(self.state, context);
        let (rows, record) = match find_child_run(&transaction, query, memory)? {
            ChildRunLookup::Found(rows, record) => (rows, *record),
            ChildRunLookup::Missing => return Ok(ChildFlowRead::Missing),
            ChildRunLookup::MemoryExceeded => return Ok(ChildFlowRead::MemoryExceeded),
        };
        let ended_result = if record.status == FlowStatus::Running as i32 {
            None
        } else {
            // Output sizes are known before their values load.
            let output_bytes = record
                .close
                .iter()
                .flat_map(|close| &close.results)
                .filter_map(|output| output.completed_step_output.as_ref())
                .map(stored_value_size)
                .fold(0, u64::saturating_add);
            if !memory.reserve_bytes(output_bytes) {
                return Ok(ChildFlowRead::MemoryExceeded);
            }
            Some(crate::cross_flow::closed_run_flow_result(
                &transaction,
                rows,
                &record,
            )?)
        };
        let start = if query.should_read_start {
            match read_genesis_start(&transaction, rows, &record, memory)? {
                Some(start) => Some(start),
                None => return Ok(ChildFlowRead::MemoryExceeded),
            }
        } else {
            None
        };
        Ok(ChildFlowRead::Found(Box::new(ChildFlowRun {
            run_id: rows.run_id,
            run_attempt: record.current_attempt,
            ended_result,
            start,
        })))
    }
}

enum ChildRunLookup {
    Found(RunRows, Box<RunRecord>),
    Missing,
    MemoryExceeded,
}

/// Returns the latest run that the start request ID started, or else the latest run that reports
/// to the parent reference. A purging run counts as gone. Each run record's raw row is reserved
/// before it decodes.
///
/// The directory's raw row is reserved before it decodes too, and released when the lookup
/// ends.
fn find_child_run(
    transaction: &FlowTransaction<'_>,
    query: &ChildFlowRunQuery<'_>,
    memory: &dyn ChildFlowReadMemory,
) -> Result<ChildRunLookup, ApplyError> {
    let directory_key = flow_directory_key(
        transaction.context().flow_hash,
        query.tenant_id,
        query.child_flow_id,
    );
    let Some(encoded_directory) = transaction.get(ColumnFamily::Flow, &directory_key)? else {
        return Ok(ChildRunLookup::Missing);
    };
    let directory_bytes = encoded_bytes(encoded_directory.len());
    if !memory.reserve_bytes(directory_bytes) {
        return Ok(ChildRunLookup::MemoryExceeded);
    }
    let directory = FlowDirectoryRecord::decode(encoded_directory.as_slice()).map_err(|error| {
        ApplyError::CorruptState {
            table: "flow directory",
            key: format!("{directory_key:02x?}"),
            detail: error.to_string(),
        }
    })?;
    let lookup = find_listed_child_run(transaction, query, &directory, memory);
    memory.release_bytes(directory_bytes);
    lookup
}

/// Looks up the child run among the runs `directory` lists, as [`find_child_run`] describes.
///
/// It releases the reservation of each run record it does not return.
fn find_listed_child_run(
    transaction: &FlowTransaction<'_>,
    query: &ChildFlowRunQuery<'_>,
    directory: &FlowDirectoryRecord,
    memory: &dyn ChildFlowReadMemory,
) -> Result<ChildRunLookup, ApplyError> {
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
        let record_key = run_singleton_key(rows.flow_hash, run_id, RunTable::RunRecord);
        let Some(encoded) = transaction.get(ColumnFamily::Flow, &record_key)? else {
            continue;
        };
        let record_bytes = encoded_bytes(encoded.len());
        if !memory.reserve_bytes(record_bytes) {
            return Ok(ChildRunLookup::MemoryExceeded);
        }
        let record =
            RunRecord::decode(encoded.as_slice()).map_err(|error| ApplyError::CorruptState {
                table: "run record",
                key: format!("{record_key:02x?}"),
                detail: error.to_string(),
            })?;
        let is_attached = !record.is_purging
            && (started_run.is_some()
                || record.parent.as_ref() == Some(query.parent)
                || record.attached_parents.contains(query.parent));
        if is_attached {
            return Ok(ChildRunLookup::Found(rows, Box::new(record)));
        }
        memory.release_bytes(record_bytes);
    }
    Ok(ChildRunLookup::Missing)
}

/// Reads the child start that the run's genesis history record holds, which is never pruned.
///
/// Returns why a restart cannot repeat it when the run was not started by a parent, or when the
/// start carried hard-delete Attributes, whose objects the child run owns. Returns `None` when
/// `memory` refused the record or a value it references.
fn read_genesis_start(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
    memory: &dyn ChildFlowReadMemory,
) -> Result<Option<ChildFlowStartRead>, ApplyError> {
    let genesis = genesis_seq(transaction, rows)?;
    let metadata: Option<HistoryRowMetadata> = transaction.get_message(
        ColumnFamily::Flow,
        &history_row_metadata_key(
            rows.flow_hash,
            rows.run_id,
            genesis,
            RunTable::HistoryRecord,
            0,
        ),
        "history metadata",
    )?;
    // The recorded row size is reserved before the read; rows without it, after the raw read.
    let recorded_size = metadata
        .map(|metadata| metadata.row_size_bytes)
        .filter(|size| *size > 0);
    if recorded_size.is_some_and(|size| !memory.reserve_bytes(size)) {
        return Ok(None);
    }
    let record_key = history_record_key(rows.flow_hash, rows.run_id, genesis);
    let Some(encoded) = transaction.get(ColumnFamily::History, &record_key)? else {
        return Ok(Some(ChildFlowStartRead::Unavailable(
            "the child run no longer holds its genesis history record".to_owned(),
        )));
    };
    if recorded_size.is_none() && !memory.reserve_bytes(encoded_bytes(encoded.len())) {
        return Ok(None);
    }
    let genesis_record =
        HistoryRecord::decode(encoded.as_slice()).map_err(|error| ApplyError::CorruptState {
            table: "history record",
            key: format!("{record_key:02x?}"),
            detail: error.to_string(),
        })?;
    let body = genesis_record.command.and_then(|command| command.command);
    let Some(Command::InboxMessage(inbox)) = body else {
        return Ok(Some(ChildFlowStartRead::Unavailable(
            "the child run was not started by its parent".to_owned(),
        )));
    };
    let Some(inbox_message_command::Payload::ChildStart(mut start)) = inbox.payload else {
        return Ok(Some(ChildFlowStartRead::Unavailable(
            "the child run was not started by its parent".to_owned(),
        )));
    };
    if !start.hard_delete_objects.is_empty() {
        return Ok(Some(ChildFlowStartRead::Unavailable(
            "its start wrote hard-delete Attributes, which a restart cannot repeat".to_owned(),
        )));
    }
    let limits = ValueLimits {
        wire_inline_threshold_bytes: record
            .settings
            .unwrap_or_default()
            .wire_inline_threshold_bytes,
        max_value_bytes: u64::MAX,
    };
    let Some(condition) = start.condition.as_mut() else {
        return Ok(Some(ChildFlowStartRead::Available(Box::new(start))));
    };
    let attribute_values = condition
        .options
        .iter_mut()
        .flat_map(|options| options.attributes.iter_mut())
        .filter_map(|write| write.value.as_mut());
    for value in condition.step_input.iter_mut().chain(attribute_values) {
        match complete_start_value(transaction, rows, limits, value, memory)? {
            StartValueCompletion::Completed => {}
            StartValueCompletion::Unavailable(reason) => {
                return Ok(Some(ChildFlowStartRead::Unavailable(reason)));
            }
            StartValueCompletion::MemoryExceeded => return Ok(None),
        }
    }
    Ok(Some(ChildFlowStartRead::Available(Box::new(start))))
}

enum StartValueCompletion {
    Completed,
    Unavailable(String),
    MemoryExceeded,
}

/// Replaces a value reference of the child run, which history records may hold, with its value.
/// It reserves the value's recorded size before loading it; the genesis record's reservation
/// already covers inline values.
fn complete_start_value(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    limits: ValueLimits,
    value: &mut Value,
    memory: &dyn ChildFlowReadMemory,
) -> Result<StartValueCompletion, ApplyError> {
    if let Some(size_bytes) = referenced_value_size(transaction, rows, value)?
        && !memory.reserve_bytes(size_bytes)
    {
        return Ok(StartValueCompletion::MemoryExceeded);
    }
    match complete_worker_value(transaction, rows, limits, value)? {
        Ok(complete) => {
            *value = complete;
            Ok(StartValueCompletion::Completed)
        }
        Err(rejection) => Ok(StartValueCompletion::Unavailable(format!(
            "its start holds a value it no longer stores: {}",
            rejection.message()
        ))),
    }
}
