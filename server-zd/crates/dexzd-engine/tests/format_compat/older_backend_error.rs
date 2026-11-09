// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Maps the Flow error category in `backend_error` back to the raw failure text that frozen
//! logs recorded.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex};

use dexzd_apply::{ApplyContext, ApplyError, ApplyOutcome, FlowStateReader, StateWrite};
use dexzd_engine::CommandApplier;
use dexzd_keyspace::row_keys::{history_row_metadata_key, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key};
use dexzd_protocol::dex::{
    DispatchTaskErrorReason, FlowErrorType, StepMethodFailure, flow_history_event,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistoryRecord, HistoryRowMetadata, RunRecord, StepAttemptFailedCommand,
    StepAttemptFailureKind, StepExecutionRecord, StepMethod, flow_command::Command,
};
use prost::Message;

use crate::golden_replay::SnapshotUse;
use crate::old_log_replay::{StoredRows, replay_with_applier};
use crate::state_dump::{StateDump, dumped_value, hex};

/// Every `backend_error` that apply records for an attempt failure.
const FLOW_ERROR_CATEGORIES: [FlowErrorType; 2] =
    [FlowErrorType::WorkerApiFail, FlowErrorType::Internal];

/// Run ID, Step execution ID, method, and attempt of one attempt failure.
type FailureKey = ([u8; 16], String, i32, u32);

/// Apply's category for one applied attempt failure, and the recording binary's raw text.
#[derive(Debug, PartialEq)]
pub(crate) struct AppliedFailure {
    category: FlowErrorType,
    raw_text: String,
}

/// Applied attempt failures by run ID, Step execution ID, method, and attempt.
pub(crate) type AppliedFailures = BTreeMap<FailureKey, AppliedFailure>;

/// Replays the log with the debug replay checks and returns its state with the raw failure
/// text, and the byte counters, that the recording binary wrote.
pub(crate) async fn replay_with_older_backend_errors(
    directory: &std::path::Path,
    snapshot_use: SnapshotUse,
) -> (StateDump, StoredRows) {
    let applier = Arc::new(StepRowRecordingApplier::default());
    let (mut replayed, mut stored) =
        replay_with_applier(directory, snapshot_use, true, applier.clone()).await;
    let failures = applier.applied_failures();
    let step_row_write_deltas = applier.step_row_write_deltas(&failures);
    with_older_backend_errors(
        &mut replayed,
        &mut stored,
        &failures,
        &step_row_write_deltas,
    );
    (replayed, stored)
}

/// Applies commands with `dexzd-apply` and keeps each applied command's writes once.
#[derive(Default)]
pub(crate) struct StepRowRecordingApplier {
    applied: Mutex<AppliedCommands>,
}

#[derive(Default)]
struct AppliedCommands {
    /// Partition, proposal time, and command ID of each kept command; the debug check repeats
    /// apply.
    seen: BTreeSet<(u16, i64, Vec<u8>)>,
    commands: Vec<(FlowCommand, Vec<StateWrite>)>,
}

impl CommandApplier for StepRowRecordingApplier {
    fn apply(
        &self,
        context: &ApplyContext,
        state: &dyn FlowStateReader,
        command: &FlowCommand,
    ) -> Result<ApplyOutcome, ApplyError> {
        let outcome = dexzd_apply::apply_flow_command(context, state, command)?;
        let mut applied = self.applied.lock().expect("the applied commands lock");
        let identity = (
            context.partition.0,
            command.proposed_at_micros,
            command.command_id.clone(),
        );
        if applied.seen.insert(identity) {
            applied
                .commands
                .push((command.clone(), outcome.writes.clone()));
        }
        Ok(outcome)
    }
}

impl StepRowRecordingApplier {
    /// Returns the category and raw text of every applied attempt failure command.
    ///
    /// Panics when two applied commands fail the same attempt differently.
    pub(crate) fn applied_failures(&self) -> AppliedFailures {
        let applied = self.applied.lock().expect("the applied commands lock");
        let mut failures = AppliedFailures::new();
        for (command, _) in &applied.commands {
            let Some(Command::StepAttemptFailed(failed)) = &command.command else {
                continue;
            };
            let invocation = failed
                .invocation
                .as_ref()
                .expect("failure commands name their invocation");
            let run_id: [u8; 16] = command
                .run_id
                .as_slice()
                .try_into()
                .expect("failure commands carry a 16-byte run ID");
            // The rule of the recording binary: the server detail, else the Worker's detail.
            let raw_text = if failed.server_detail.is_empty() {
                failed
                    .worker_error
                    .as_ref()
                    .map(|worker_error| worker_error.detail.clone())
                    .unwrap_or_default()
            } else {
                failed.server_detail.clone()
            };
            let key = (
                run_id,
                invocation.step_execution_id.clone(),
                invocation.method,
                failed.attempt,
            );
            let failure = AppliedFailure {
                category: flow_error_type_for_attempt_failure(failed),
                raw_text,
            };
            if let Some(earlier) = failures.get(&key) {
                assert_eq!(
                    *earlier, failure,
                    "applied commands fail attempt {key:?} differently"
                );
            }
            failures.insert(key, failure);
        }
        failures
    }

    /// Returns, by run record key, how many more Step row bytes the replay wrote since the run's
    /// last checkpoint than the recording binary did.
    ///
    /// Ended Steps' rows are gone, so each logged command's Step row writes give it.
    fn step_row_write_deltas(&self, failures: &AppliedFailures) -> BTreeMap<Vec<u8>, i128> {
        let applied = self.applied.lock().expect("the applied commands lock");
        let mut deltas: BTreeMap<Vec<u8>, i128> = BTreeMap::new();
        for (_, writes) in &applied.commands {
            let mut checkpointed_runs = BTreeSet::new();
            for write in writes {
                let StateWrite::Put {
                    column_family,
                    key,
                    value,
                } = write
                else {
                    continue;
                };
                let Ok(parsed) = parse_run_key(key) else {
                    continue;
                };
                let record_key =
                    run_singleton_key(parsed.flow_hash, parsed.run_id, RunTable::RunRecord);
                match (column_family, parsed.table) {
                    (ColumnFamily::History, RunTable::Checkpoint) => {
                        checkpointed_runs.insert(record_key);
                    }
                    (ColumnFamily::Flow, RunTable::StepExecution) => {
                        let older_value = older_step_row(value, parsed.run_id.0, failures);
                        *deltas.entry(record_key).or_default() +=
                            value.len() as i128 - older_value.len() as i128;
                    }
                    _ => {}
                }
            }
            for record_key in checkpointed_runs {
                deltas.insert(record_key, 0);
            }
        }
        deltas
    }
}

/// Returns the Step row with its last failure's category text replaced by the raw text of
/// the failure command.
///
/// Panics when an applied failure command's failure lacks its category, when a category has no
/// applied command, or when the command's raw text disagrees with `older_backend_error`.
fn older_step_row(value: &[u8], run_id: [u8; 16], failures: &AppliedFailures) -> Vec<u8> {
    let mut step = StepExecutionRecord::decode(value).expect("Step rows decode");
    assert_eq!(
        step.encode_to_vec(),
        value,
        "a replayed Step row holds bytes that decoding drops or rewrites"
    );
    let step_execution_id = step.step_execution_id.clone();
    let Some(invocation) = step.invocation.as_mut() else {
        return value.to_vec();
    };
    let method = invocation.method;
    let Some(failure) = invocation.last_failure.as_mut() else {
        return value.to_vec();
    };
    let Some(raw_text) =
        applied_failure_raw_text(failures, run_id, &step_execution_id, method, failure)
    else {
        return value.to_vec();
    };
    let is_dispatch_failure = failure.details.as_ref().is_some_and(|details| {
        details.dispatch_task_error_reason != DispatchTaskErrorReason::Unspecified as i32
    });
    if !is_dispatch_failure {
        assert_eq!(
            older_backend_error(failure).as_deref(),
            Some(raw_text.as_str()),
            "the failure details give the failure command's raw text"
        );
    }
    failure.backend_error = raw_text;
    step.encode_to_vec()
}

/// Rewrites category failure text in history rows, their metadata, and their run records'
/// counters.
fn with_older_backend_errors(
    replayed: &mut StateDump,
    stored: &mut StoredRows,
    failures: &AppliedFailures,
    step_row_write_deltas: &BTreeMap<Vec<u8>, i128>,
) {
    let flow_name = ColumnFamily::Flow.name().to_owned();
    let history_name = ColumnFamily::History.name().to_owned();
    let history_rows: Vec<(Vec<u8>, Vec<u8>)> = stored
        .iter()
        .filter(|((column_family, key), _)| {
            *column_family == history_name
                && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::HistoryRecord)
        })
        .map(|((_, key), value)| (key.clone(), value.clone()))
        .collect();
    let mut history_deltas: BTreeMap<Vec<u8>, i128> = BTreeMap::new();
    for (key, value) in history_rows {
        let older_value = history_with_older_backend_errors(&key, &value, failures);
        if older_value == value {
            continue;
        }
        let parsed = parse_run_key(&key).expect("history keys parse");
        let seq = u64::from_be_bytes(
            parsed
                .suffix
                .try_into()
                .expect("history record keys end with an eight-byte seq"),
        );
        let metadata_key = history_row_metadata_key(
            parsed.flow_hash,
            parsed.run_id,
            seq,
            RunTable::HistoryRecord,
            0,
        );
        let metadata_value = stored
            .get(&(flow_name.clone(), metadata_key.clone()))
            .expect("every history row has metadata");
        let mut metadata =
            HistoryRowMetadata::decode(metadata_value.as_slice()).expect("metadata decodes");
        assert_eq!(
            metadata.encode_to_vec(),
            *metadata_value,
            "replayed history row metadata holds bytes that decoding drops or rewrites"
        );
        assert_eq!(
            metadata.row_size_bytes,
            (key.len() + value.len()) as u64,
            "history row metadata counts the replayed row"
        );
        metadata.row_size_bytes = (key.len() + older_value.len()) as u64;
        let older_metadata = metadata.encode_to_vec();
        // History bytes count the metadata row and the history row size it records.
        let history_delta = (older_metadata.len() as i128 - metadata_value.len() as i128)
            + (older_value.len() as i128 - value.len() as i128);
        *history_deltas
            .entry(run_singleton_key(
                parsed.flow_hash,
                parsed.run_id,
                RunTable::RunRecord,
            ))
            .or_default() += history_delta;
        replace_row(replayed, stored, &flow_name, metadata_key, older_metadata);
        replace_row(replayed, stored, &history_name, key, older_value);
    }
    let run_record_keys: BTreeSet<&Vec<u8>> = history_deltas
        .keys()
        .chain(step_row_write_deltas.keys())
        .collect();
    for record_key in run_record_keys {
        let history_delta = history_deltas.get(record_key).copied().unwrap_or_default();
        let write_delta = step_row_write_deltas
            .get(record_key)
            .copied()
            .unwrap_or_default();
        if history_delta == 0 && write_delta == 0 {
            continue;
        }
        let value = stored
            .get(&(flow_name.clone(), record_key.clone()))
            .expect("a run with category failure text keeps its run record");
        let older_record = run_record_with_older_counters(value, history_delta, write_delta);
        replace_row(
            replayed,
            stored,
            &flow_name,
            record_key.clone(),
            older_record,
        );
    }
}

/// Returns the run record with the history bytes and row write bytes that the older failure
/// text gives, and with its own size settled in its state row bytes.
///
/// Panics when the run record does not encode back to the same bytes.
fn run_record_with_older_counters(value: &[u8], history_delta: i128, write_delta: i128) -> Vec<u8> {
    let mut record = RunRecord::decode(value).expect("run records decode");
    assert_eq!(
        record.encode_to_vec(),
        value,
        "a replayed run record holds bytes that decoding drops or rewrites"
    );
    let replayed_size = record.encoded_len() as u64;
    let mut usage = record.storage.expect("run records count storage");
    usage.history_bytes = u64::try_from(i128::from(usage.history_bytes) + history_delta)
        .expect("older history bytes are not negative");
    record.row_write_bytes_since_checkpoint =
        u64::try_from(i128::from(record.row_write_bytes_since_checkpoint) - write_delta)
            .expect("older row write bytes are not negative");
    let other_state_bytes = usage.state_row_bytes - replayed_size;
    // The record's own row counts in its state row bytes; repeat until its size settles.
    for _ in 0..4 {
        record.storage = Some(usage);
        let own_size = record.encoded_len() as u64;
        if usage.state_row_bytes == other_state_bytes + own_size {
            return record.encode_to_vec();
        }
        usage.state_row_bytes = other_state_bytes + own_size;
    }
    panic!("the run record size does not settle")
}

fn replace_row(
    replayed: &mut StateDump,
    stored: &mut StoredRows,
    column_family: &str,
    key: Vec<u8>,
    value: Vec<u8>,
) {
    replayed.insert((column_family.to_owned(), hex(&key)), dumped_value(&value));
    stored.insert((column_family.to_owned(), key), value);
}

/// Returns the history row with every category `backend_error` mapped back to its raw text.
///
/// Panics when the row does not encode back to the same bytes, or when a failure disagrees with
/// `applied_failure_raw_text`.
pub(crate) fn history_with_older_backend_errors(
    key: &[u8],
    value: &[u8],
    failures: &AppliedFailures,
) -> Vec<u8> {
    let run_id = parse_run_key(key).expect("history keys parse").run_id.0;
    let mut record = HistoryRecord::decode(value).expect("history rows decode");
    assert_eq!(
        record.encode_to_vec(),
        value,
        "a replayed history row holds bytes that decoding drops or rewrites"
    );
    for event in &mut record.events {
        let (method, context, output) = match event.payload.as_mut() {
            Some(flow_history_event::Payload::StepWaitForPending(pending)) => {
                (StepMethod::WaitFor, pending.context.as_mut(), None)
            }
            Some(flow_history_event::Payload::StepExecutePending(pending)) => {
                (StepMethod::Execute, pending.context.as_mut(), None)
            }
            Some(flow_history_event::Payload::StepWaitForCompleted(completed)) => {
                (StepMethod::WaitFor, completed.context.as_mut(), None)
            }
            Some(flow_history_event::Payload::StepExecuteCompleted(completed)) => {
                (StepMethod::Execute, completed.context.as_mut(), None)
            }
            Some(flow_history_event::Payload::StepWaitForFailed(failed)) => (
                StepMethod::WaitFor,
                failed.context.as_mut(),
                failed.output.as_mut(),
            ),
            Some(flow_history_event::Payload::StepExecuteFailed(failed)) => (
                StepMethod::Execute,
                failed.context.as_mut(),
                failed.output.as_mut(),
            ),
            _ => continue,
        };
        let Some(context) = context else {
            assert!(
                output.is_none_or(|output| output.failure.is_none()),
                "a Step method failure event names its Step execution"
            );
            continue;
        };
        let step_execution_id = context.step_execution_id.clone();
        let event_failures = context
            .last_failure_info
            .as_mut()
            .into_iter()
            .chain(output.and_then(|output| output.failure.as_mut()));
        for failure in event_failures {
            let Some(raw_text) = applied_failure_raw_text(
                failures,
                run_id,
                &step_execution_id,
                method as i32,
                failure,
            ) else {
                continue;
            };
            assert_eq!(
                older_backend_error(failure).as_deref(),
                Some(raw_text.as_str()),
                "the failure details give the failure command's raw text"
            );
            failure.backend_error = raw_text;
        }
    }
    record.encode_to_vec()
}

/// Returns the raw text of the applied command that failed this attempt, or `None` for a
/// restored failure that no replayed command wrote.
///
/// Panics when an applied command matches but `failure` lacks its category, or when a category
/// has no applied command.
fn applied_failure_raw_text(
    failures: &AppliedFailures,
    run_id: [u8; 16],
    step_execution_id: &str,
    method: i32,
    failure: &StepMethodFailure,
) -> Option<String> {
    let attempt = u32::try_from(failure.attempt).expect("failure attempts are positive");
    let Some(applied) = failures.get(&(run_id, step_execution_id.to_owned(), method, attempt))
    else {
        assert!(
            !is_flow_error_category(&failure.backend_error),
            "no applied command failed attempt {attempt} of method {method} of \
             {step_execution_id}"
        );
        return None;
    };
    assert_eq!(
        failure.backend_error,
        applied.category.as_str_name(),
        "attempt {attempt} of {step_execution_id} records its failure command's category"
    );
    Some(applied.raw_text.clone())
}

/// Returns the raw text that older binaries recorded as this failure's `backend_error`, or
/// `None` when it already holds raw text.
///
/// Panics for a dispatch failure, whose `details.detail` may hold rewritten text.
fn older_backend_error(failure: &StepMethodFailure) -> Option<String> {
    if !is_flow_error_category(&failure.backend_error) {
        return None;
    }
    let details = failure
        .details
        .as_ref()
        .expect("an attempt failure records its details");
    assert_eq!(
        details.dispatch_task_error_reason,
        DispatchTaskErrorReason::Unspecified as i32,
        "the raw text of dispatch failure {failure:?} is not recoverable from its details"
    );
    // Without a dispatch reason, `details.detail` is the unchanged server detail.
    if !details.detail.is_empty() {
        return Some(details.detail.clone());
    }
    assert!(
        !details.original_worker_error_detail.is_empty(),
        "failure {failure:?} holds neither a server detail nor a Worker detail"
    );
    Some(details.original_worker_error_detail.clone())
}

fn is_flow_error_category(backend_error: &str) -> bool {
    FLOW_ERROR_CATEGORIES
        .iter()
        .any(|category| category.as_str_name() == backend_error)
}

/// Restates `flow_error_type_for_attempt_failure` in dexzd-apply `steps/step_results.rs`.
fn flow_error_type_for_attempt_failure(command: &StepAttemptFailedCommand) -> FlowErrorType {
    let kind = StepAttemptFailureKind::try_from(command.kind)
        .unwrap_or(StepAttemptFailureKind::Unspecified);
    match kind {
        StepAttemptFailureKind::DispatchError => {
            let is_worker_dispatch_failure = [
                DispatchTaskErrorReason::NoWorkerAvailable,
                DispatchTaskErrorReason::WorkerDisconnected,
                DispatchTaskErrorReason::UnknownName,
            ]
            .iter()
            .any(|reason| {
                command
                    .server_detail
                    .starts_with(&format!("{}: ", reason.as_str_name()))
            });
            if is_worker_dispatch_failure {
                FlowErrorType::WorkerApiFail
            } else {
                FlowErrorType::Internal
            }
        }
        StepAttemptFailureKind::InvalidResponse | StepAttemptFailureKind::RequestTooLarge => {
            FlowErrorType::Internal
        }
        StepAttemptFailureKind::WorkerError
        | StepAttemptFailureKind::MethodTimeout
        | StepAttemptFailureKind::HeartbeatTimeout
        | StepAttemptFailureKind::RetryDeadline
        | StepAttemptFailureKind::ValueTooLarge
        | StepAttemptFailureKind::UnsupportedFeature
        | StepAttemptFailureKind::StartedAttemptInterrupted
        | StepAttemptFailureKind::Unspecified => FlowErrorType::WorkerApiFail,
    }
}
