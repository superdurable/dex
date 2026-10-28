// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attempt lifecycle commands: started, heartbeat, failed, completed, and retry backoff.

use dexzd_keyspace::ColumnFamily;
use dexzd_keyspace::row_keys::{timer_index_key, timer_key};
use dexzd_protocol::dex::{
    DispatchTaskErrorReason, ErrorSubStatus, ExecuteMethodFailurePolicy, FlowErrorType,
    RecoveryErrorInfo, ServiceErrorResponse, StepMethodFailure, Value, WaitForMethodFailurePolicy,
    WorkerErrorResponse, value,
};
use dexzd_protocol::dexzd::v1::{
    RetryBackoffTimerId, StepAttemptFailedCommand, StepAttemptFailureKind,
    StepAttemptHeartbeatCommand, StepAttemptStartedCommand, StepExecutionPhase,
    StepExecutionRecord, StepInvocationKey, StepInvocationPhase, StepMethod,
    StepMethodCompletedCommand, TimerId, TimerRecord, step_attempt_heartbeat_command,
    step_method_completed_command, timer_id,
};
use prost::Message;

use crate::command_handlers::CommandScope;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::flow_close::{FlowClosing, close_flow};
use crate::command_handlers::run_cursor::RunCursor;
use crate::projections::history_events;
use crate::state_access::FlowTransaction;
use crate::steps::attempt_snapshot::compute_attempt_snapshot;
use crate::steps::invocations::{can_take_locks, release_heartbeat_checkpoint};
use crate::steps::{
    InvocationEnd, NewStepExecution, RetryDecision, apply_step_decision, create_step_execution,
    decide_retry, end_running_attempt, finish_step, make_invocation_pending, method_retry_policy,
    retry_started_at_micros, start_attempt, wait_conditions,
};
use crate::values::{
    ValueHolder, ValueRejection, hold_stored_value, release_stored_value, store_value,
};
use crate::{ApplyEffect, ApplyError};
use dexzd_protocol::dexzd::v1::CommandReply;

/// Outcome of a Step command handler.
pub(crate) enum StepCommandOutcome {
    /// Applied; record it with this reply.
    Applied(CommandReply),
    /// Nothing changes (stale attempt, missing Step); reply and discard writes.
    Ignored(CommandReply),
    /// The Worker's result is invalid in a way only apply can see; apply it as a non-retryable
    /// failure of the attempt instead.
    RejectedResult(ValueRejection),
    /// The result is missing or belongs to the other method; also a non-retryable failure.
    MismatchedResult(String),
}

fn stale_reply(detail: &str) -> CommandReply {
    error_reply(
        grpc_code::FAILED_PRECONDITION,
        ErrorSubStatus::Uncategorized,
        detail.to_owned(),
    )
}

/// Loads the Step and checks that `key`/`attempt` names its current invocation in `phase`.
fn load_invocation(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    key: Option<&StepInvocationKey>,
    attempt: u32,
    phase: StepInvocationPhase,
) -> Result<Option<StepExecutionRecord>, ApplyError> {
    let Some(key) = key else { return Ok(None) };
    let Some(step) = cursor
        .rows
        .step_execution(transaction, &key.step_execution_id)?
    else {
        return Ok(None);
    };
    let is_current = step.invocation.as_ref().is_some_and(|invocation| {
        invocation.method == key.method
            && invocation.attempt == attempt
            && invocation.phase == phase as i32
    });
    Ok(is_current.then_some(step))
}

pub(crate) fn apply_attempt_started(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    command: &StepAttemptStartedCommand,
) -> Result<StepCommandOutcome, ApplyError> {
    let Some(mut step) = load_invocation(
        transaction,
        cursor,
        command.invocation.as_ref(),
        command.attempt,
        StepInvocationPhase::Pending,
    )?
    else {
        return Ok(StepCommandOutcome::Ignored(stale_reply(
            "the attempt is no longer pending",
        )));
    };
    let key = command.invocation.clone().unwrap_or_default();
    let lock_keys = step
        .invocation
        .as_ref()
        .map(|invocation| invocation.lock_keys.clone())
        .unwrap_or_default();
    if !can_take_locks(transaction, cursor, &key, &lock_keys)? {
        return Ok(StepCommandOutcome::Ignored(error_reply(
            grpc_code::ABORTED,
            ErrorSubStatus::Uncategorized,
            "the attempt's locks are not available yet",
        )));
    }
    let method = StepMethod::try_from(key.method).unwrap_or(StepMethod::Execute);
    let snapshot = compute_attempt_snapshot(transaction, cursor, &step, method, command.attempt)?;
    start_attempt(
        transaction,
        cursor,
        &mut step,
        &command.task_id,
        scope.now_micros,
    )?;
    if let Some(invocation) = step.invocation.as_mut() {
        invocation.snapshot = Some(snapshot);
        invocation.pull_routing = command.pull_routing.clone();
    }
    cursor.rows.put_step_execution(transaction, &step);
    Ok(StepCommandOutcome::Applied(ok_reply()))
}

pub(crate) fn apply_attempt_heartbeat(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    command: &StepAttemptHeartbeatCommand,
) -> Result<StepCommandOutcome, ApplyError> {
    let Some(mut step) = load_invocation(
        transaction,
        cursor,
        command.invocation.as_ref(),
        command.attempt,
        StepInvocationPhase::Running,
    )?
    else {
        return Ok(StepCommandOutcome::Ignored(stale_reply(
            "the attempt is no longer running",
        )));
    };
    let Some(invocation) = step.invocation.as_mut() else {
        return Ok(StepCommandOutcome::Ignored(stale_reply(
            "the attempt is no longer running",
        )));
    };
    let holder = ValueHolder::HeartbeatCheckpoint {
        step_execution_id: step.step_execution_id.clone(),
        method: invocation.method,
    };
    if let Some(previous) = invocation.heartbeat_checkpoint.take() {
        release_stored_value(transaction, cursor.rows, &previous, &holder, cursor.seq)?;
    }
    if let Some(step_attempt_heartbeat_command::Checkpoint::Value(value)) = &command.checkpoint {
        let limits = cursor.value_limits();
        match store_value(
            transaction,
            cursor.rows,
            limits,
            &mut cursor.values,
            value,
            &holder,
        )? {
            Ok(stored) => invocation.heartbeat_checkpoint = Some(stored),
            Err(rejection) => return Ok(StepCommandOutcome::RejectedResult(rejection)),
        }
    }
    cursor.rows.put_step_execution(transaction, &step);
    Ok(StepCommandOutcome::Applied(ok_reply()))
}

pub(crate) fn apply_attempt_failed(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    command: &StepAttemptFailedCommand,
) -> Result<StepCommandOutcome, ApplyError> {
    let Some(mut step) = load_invocation(
        transaction,
        cursor,
        command.invocation.as_ref(),
        command.attempt,
        StepInvocationPhase::Running,
    )?
    else {
        return Ok(StepCommandOutcome::Ignored(ok_reply()));
    };
    let worker_error = command.worker_error.clone().unwrap_or_default();
    let dispatch_reason = dispatch_failure_reason(command);
    let failure = StepMethodFailure {
        backend_error: if command.server_detail.is_empty() {
            worker_error.detail.clone()
        } else {
            command.server_detail.clone()
        },
        details: Some(ServiceErrorResponse {
            detail: (dispatch_reason != DispatchTaskErrorReason::Unspecified)
                .then(|| go_dispatch_failure_text(&command.server_detail, &cursor.record.flow_type))
                .flatten()
                .unwrap_or_else(|| command.server_detail.clone()),
            dispatch_task_error_reason: dispatch_reason as i32,
            sub_status: ErrorSubStatus::WorkerApiError as i32,
            original_worker_error_detail: worker_error.detail.clone(),
            original_worker_error_type: worker_error.error_type.clone(),
            original_worker_error_status: worker_error.grpc_status,
            original_worker_error_stack_trace: worker_error.stack_trace.clone(),
        }),
        attempt: i32::try_from(command.attempt).unwrap_or(i32::MAX),
        pull_routing: command.pull_routing.clone(),
    };
    let key = command.invocation.clone().unwrap_or_default();
    let method = StepMethod::try_from(key.method).unwrap_or(StepMethod::Execute);
    let options = step.options.clone().unwrap_or_default();
    let policy = method_retry_policy(Some(&options), key.method);
    let retry_started_at = step
        .invocation
        .as_ref()
        .map_or(scope.now_micros, retry_started_at_micros);
    let decision = decide_retry(
        policy,
        command.attempt,
        retry_started_at,
        scope.now_micros,
        worker_error.retry_after_seconds,
        command.is_non_retryable,
    );
    if decision == RetryDecision::Exhausted {
        // Recorded before the attempt ends so the event keeps the final attempt snapshot.
        history_events::record_method_failed(
            transaction,
            cursor,
            &step,
            method,
            &failure,
            scope.now_micros,
        )?;
    }
    end_running_attempt(transaction, cursor, &mut step, InvocationEnd::Finished)?;
    match decision {
        RetryDecision::RetryAt { fires_at_micros } => {
            let next_attempt = command.attempt + 1;
            if let Some(invocation) = step.invocation.as_mut() {
                invocation.phase = StepInvocationPhase::Backoff as i32;
                invocation.attempt = next_attempt;
                invocation.backoff_fires_at_micros = fires_at_micros;
                invocation.last_failure = Some(failure);
                invocation.snapshot = None;
            }
            arm_retry_timer(transaction, cursor, &key, next_attempt, fires_at_micros);
            cursor.rows.put_step_execution(transaction, &step);
        }
        RetryDecision::Exhausted => {
            // The attempt already ended, so finish_step must not report it as canceled.
            if let Some(mut invocation) = step.invocation.take() {
                release_heartbeat_checkpoint(
                    transaction,
                    cursor,
                    &step.step_execution_id,
                    &mut invocation,
                )?;
            }
            let application_text = application_failure_text(
                command,
                &worker_error,
                &failure.backend_error,
                &cursor.record.flow_type,
            );
            if method == StepMethod::WaitFor
                && options.wait_for_failure_policy
                    == WaitForMethodFailurePolicy::ProceedOnFailure as i32
            {
                step.recovery_error =
                    Some(recovery_error(command, &worker_error, application_text));
                step.condition_results = Some(dexzd_protocol::dexzd::v1::ConditionResultsRecord {
                    wait_for_failed: true,
                    ..Default::default()
                });
                step.phase = StepExecutionPhase::Execute as i32;
                make_invocation_pending(
                    transaction,
                    cursor,
                    &mut step,
                    StepMethod::Execute,
                    1,
                    scope.now_micros,
                )?;
                cursor.rows.put_step_execution(transaction, &step);
            } else if method == StepMethod::Execute
                && options.execute_failure_policy
                    == ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32
                && !options.execute_failure_proceed_step_type.is_empty()
            {
                let new_step = NewStepExecution {
                    step_type: &options.execute_failure_proceed_step_type,
                    input: None,
                    options: options.execute_failure_proceed_step_options.as_deref(),
                    from_step_execution_id: &step.step_execution_id,
                    is_timeout_handler: false,
                };
                let step_execution_id =
                    create_step_execution(transaction, cursor, scope.now_micros, &new_step)?
                        .expect("recovery Step has no untrusted input");
                let mut recovery_step = cursor
                    .rows
                    .step_execution(transaction, &step_execution_id)?
                    .expect("created recovery Step exists");
                let input = if step.is_timeout_handler {
                    Some(dexzd_protocol::dexzd::v1::StoredValue {
                        kind: Some(dexzd_protocol::dexzd::v1::stored_value::Kind::InlineValue(
                            Value {
                                kind: Some(value::Kind::NullValue(0)),
                            },
                        )),
                    })
                } else {
                    step.input.clone()
                };
                if let Some(input) = input.as_ref() {
                    hold_stored_value(
                        transaction,
                        cursor.rows,
                        input,
                        &ValueHolder::StepInput {
                            step_execution_id: step_execution_id.clone(),
                        },
                    )?;
                }
                recovery_step.input = input;
                recovery_step.recovery_error =
                    Some(recovery_error(command, &worker_error, application_text));
                cursor.rows.put_step_execution(transaction, &recovery_step);
                finish_step(transaction, cursor, &mut step, StepExecutionPhase::Failed)?;
            } else {
                finish_step(transaction, cursor, &mut step, StepExecutionPhase::Failed)?;
                crate::command_handlers::flow_close::close_flow(
                    transaction,
                    cursor,
                    scope,
                    crate::command_handlers::flow_close::FlowClosing::failed(
                        flow_error_type_for_attempt_failure(command),
                        application_text,
                    ),
                )?;
            }
        }
    }
    Ok(StepCommandOutcome::Applied(ok_reply()))
}

/// Engine detail of a WaitFor final frame without a result; defensive, Workers cannot send one.
const ENGINE_WAIT_FOR_FRAME_WITHOUT_RESULT: &str = "WaitFor frame carries no result";
/// Engine detail of an Execute final frame without a result; defensive, Workers cannot send one.
const ENGINE_EXECUTE_FRAME_WITHOUT_RESULT: &str = "Execute frame carries no result";
/// Engine detail of a final frame for another method, such as an RPC frame.
const ENGINE_WRONG_METHOD_FRAME: &str = "final frame does not match the dispatched method";
/// Go Server text from `receiveWaitForMethodResponse` in `activityImpl.go`.
const GO_EMPTY_WAIT_FOR_OUTPUT: &str = "InvokeWaitForMethod emitted an empty output";
/// Go Server text from `receiveExecuteMethodResponse` in `activityImpl.go`.
const GO_EMPTY_EXECUTE_OUTPUT: &str = "InvokeExecuteMethod emitted an empty output";
/// Go Server text from `pullFrameError` wrapped by `matchingDispatchError` in `pullDispatch.go`.
const GO_UNEXPECTED_PULL_DISPATCH_FRAME: &str =
    "matching dispatch: rpc error: code = Internal desc = unexpected pull dispatch frame";
/// Go Server prefix from `pullFrameError` in `pullDispatch.go` for WORKER_DISCONNECTED.
const GO_WORKER_DISCONNECTED_PREFIX: &str = "Worker disconnected during pull dispatch";

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
        | StepAttemptFailureKind::Unspecified => FlowErrorType::WorkerApiFail,
    }
}

/// Returns the text applications see for an exhausted attempt failure, in both the Flow close
/// message and `RecoveryErrorInfo.detail`: the Go Server's text for the same failure, or
/// `raw_detail` when the Go Server has no equivalent. Step history keeps `raw_detail`.
fn application_failure_text(
    command: &StepAttemptFailedCommand,
    worker_error: &WorkerErrorResponse,
    raw_detail: &str,
    flow_type: &str,
) -> String {
    let kind = StepAttemptFailureKind::try_from(command.kind)
        .unwrap_or(StepAttemptFailureKind::Unspecified);
    let server_detail = command.server_detail.as_str();
    match kind {
        StepAttemptFailureKind::WorkerError if raw_detail.is_empty() => {
            go_worker_status_error(worker_error.grpc_status)
        }
        StepAttemptFailureKind::DispatchError => go_dispatch_failure_text(server_detail, flow_type)
            .unwrap_or_else(|| raw_detail.to_owned()),
        StepAttemptFailureKind::InvalidResponse => match server_detail {
            ENGINE_WAIT_FOR_FRAME_WITHOUT_RESULT => GO_EMPTY_WAIT_FOR_OUTPUT.to_owned(),
            ENGINE_EXECUTE_FRAME_WITHOUT_RESULT => GO_EMPTY_EXECUTE_OUTPUT.to_owned(),
            ENGINE_WRONG_METHOD_FRAME => GO_UNEXPECTED_PULL_DISPATCH_FRAME.to_owned(),
            _ => validation_message(server_detail)
                .unwrap_or(raw_detail)
                .to_owned(),
        },
        _ => raw_detail.to_owned(),
    }
}

/// Returns the reason named by a dispatch failure's `<reason name>: <detail>` server detail, or
/// `Unspecified` for another failure kind or an unrecognized prefix.
fn dispatch_failure_reason(command: &StepAttemptFailedCommand) -> DispatchTaskErrorReason {
    if command.kind != StepAttemptFailureKind::DispatchError as i32 {
        return DispatchTaskErrorReason::Unspecified;
    }
    command
        .server_detail
        .split_once(": ")
        .and_then(|(reason, _)| DispatchTaskErrorReason::from_str_name(reason))
        .unwrap_or(DispatchTaskErrorReason::Unspecified)
}

/// Returns the Go Server's text for an engine dispatch failure, `<reason name>: <detail>`, or
/// `None` for a reason the Go Server reports differently.
fn go_dispatch_failure_text(server_detail: &str, flow_type: &str) -> Option<String> {
    let reason_detail = |reason: DispatchTaskErrorReason| {
        server_detail
            .strip_prefix(reason.as_str_name())
            .and_then(|rest| rest.strip_prefix(": "))
    };
    if let Some(detail) = reason_detail(DispatchTaskErrorReason::NoWorkerAvailable) {
        // pullDispatch.go names the fallback queue, which is the Flow type.
        return Some(format!(
            "no Worker available for queue {flow_type}: {detail}"
        ));
    }
    if let Some(detail) = reason_detail(DispatchTaskErrorReason::WorkerDisconnected) {
        return Some(format!("{GO_WORKER_DISCONNECTED_PREFIX}: {detail}"));
    }
    // The Go Server reports UNKNOWN_NAME as a NotFound status carrying only the detail.
    reason_detail(DispatchTaskErrorReason::UnknownName).map(str::to_owned)
}

/// Returns the message of an engine validation detail, `<field path>: <message>`, or `None` when
/// `server_detail` does not start with a known validator field path.
///
/// The validation message already matches the Go Server's text; the field path is server-zd's.
fn validation_message(server_detail: &str) -> Option<&str> {
    let (field_path, message) = server_detail.split_once(": ")?;
    let has_validation_root = [
        "response",
        "output",
        "waiting_condition",
        "step_decision",
        "upsert_attributes",
        "record_events",
        "upsert_step_exe_locals",
        "publish_to_channel",
        "delete_from_channel",
        "action_permission_mappings",
    ]
    .iter()
    .any(|root| {
        field_path == *root
            || field_path.starts_with(&format!("{root}."))
            || field_path.starts_with(&format!("{root}["))
    });
    let is_field_path = has_validation_root
        && field_path
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.' | b'[' | b']'));
    is_field_path.then_some(message)
}

fn recovery_error(
    command: &StepAttemptFailedCommand,
    worker_error: &WorkerErrorResponse,
    detail: String,
) -> RecoveryErrorInfo {
    let kind = StepAttemptFailureKind::try_from(command.kind)
        .unwrap_or(StepAttemptFailureKind::Unspecified);
    let error_type = match kind {
        StepAttemptFailureKind::WorkerError => {
            if worker_error.error_type.is_empty() {
                "FLOW_ERROR_TYPE_WORKER_API_FAIL"
            } else {
                &worker_error.error_type
            }
        }
        StepAttemptFailureKind::MethodTimeout => "StartToClose",
        StepAttemptFailureKind::HeartbeatTimeout => "Heartbeat",
        StepAttemptFailureKind::RetryDeadline => "ScheduleToClose",
        StepAttemptFailureKind::DispatchError
        | StepAttemptFailureKind::InvalidResponse
        | StepAttemptFailureKind::RequestTooLarge => {
            flow_error_type_for_attempt_failure(command).as_str_name()
        }
        StepAttemptFailureKind::UnsupportedFeature => "UnsupportedFeature",
        StepAttemptFailureKind::ValueTooLarge => "ValueTooLarge",
        StepAttemptFailureKind::Unspecified => "UnknownStepFailure",
    };
    RecoveryErrorInfo {
        detail,
        error_type: error_type.to_owned(),
    }
}

/// Returns the Go Server's text for a Worker gRPC status without a message, which it uses
/// as the detail of a Worker error that has none.
fn go_worker_status_error(grpc_status: i32) -> String {
    let code_name = match grpc_status {
        1 => "Canceled",
        3 => "InvalidArgument",
        4 => "DeadlineExceeded",
        5 => "NotFound",
        6 => "AlreadyExists",
        7 => "PermissionDenied",
        8 => "ResourceExhausted",
        9 => "FailedPrecondition",
        10 => "Aborted",
        11 => "OutOfRange",
        12 => "Unimplemented",
        13 => "Internal",
        14 => "Unavailable",
        15 => "DataLoss",
        16 => "Unauthenticated",
        _ => "Unknown",
    };
    format!("rpc error: code = {code_name} desc = ")
}

fn arm_retry_timer(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    key: &StepInvocationKey,
    next_attempt: u32,
    fires_at_micros: i64,
) {
    let timer = TimerId {
        run_attempt: cursor.attempt(),
        kind: Some(timer_id::Kind::RetryBackoff(RetryBackoffTimerId {
            invocation: Some(key.clone()),
            next_attempt,
        })),
    };
    arm_timer(transaction, cursor, &timer, fires_at_micros);
}

/// Writes a timer row and its partition index entry.
pub(crate) fn arm_timer(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    timer: &TimerId,
    fires_at_micros: i64,
) {
    let encoded = timer.encode_to_vec();
    let flow_hash = cursor.rows.flow_hash;
    let run_id = cursor.run_id();
    let partition = transaction.context().partition;
    transaction.put_message(
        ColumnFamily::Flow,
        timer_key(flow_hash, run_id, &encoded),
        &TimerRecord {
            timer_id: Some(timer.clone()),
            fire_at_micros: fires_at_micros,
        },
    );
    transaction.put(
        ColumnFamily::PartitionIndex,
        timer_index_key(partition, fires_at_micros, flow_hash, run_id, &encoded),
        Vec::new(),
    );
    cursor.add_effect(ApplyEffect::TimerArmed {
        fire_at_micros: fires_at_micros,
    });
}

/// Removes a timer row and its index entry; returns the record when it existed.
pub(crate) fn disarm_timer(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    timer: &TimerId,
) -> Result<Option<TimerRecord>, ApplyError> {
    let encoded = timer.encode_to_vec();
    let flow_hash = cursor.rows.flow_hash;
    let run_id = cursor.run_id();
    let key = timer_key(flow_hash, run_id, &encoded);
    let record: Option<TimerRecord> = transaction.get_message(ColumnFamily::Flow, &key, "timer")?;
    if let Some(record) = &record {
        transaction.delete(ColumnFamily::Flow, key);
        let partition = transaction.context().partition;
        transaction.delete(
            ColumnFamily::PartitionIndex,
            timer_index_key(
                partition,
                record.fire_at_micros,
                flow_hash,
                run_id,
                &encoded,
            ),
        );
    }
    Ok(record)
}

/// A retry backoff timer fired: the next attempt becomes pending and queues for its locks.
pub(crate) fn apply_retry_backoff_fired(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    timer: &RetryBackoffTimerId,
) -> Result<(), ApplyError> {
    let Some(key) = &timer.invocation else {
        return Ok(());
    };
    let Some(mut step) = cursor
        .rows
        .step_execution(transaction, &key.step_execution_id)?
    else {
        return Ok(());
    };
    let is_waiting_for_this_timer = step.invocation.as_ref().is_some_and(|invocation| {
        invocation.method == key.method
            && invocation.attempt == timer.next_attempt
            && invocation.phase == StepInvocationPhase::Backoff as i32
    });
    if !is_waiting_for_this_timer {
        return Ok(());
    }
    let method = StepMethod::try_from(key.method).unwrap_or(StepMethod::Execute);
    let first_attempt_at = step
        .invocation
        .as_ref()
        .map_or(0, |invocation| invocation.first_attempt_at_micros);
    make_invocation_pending(
        transaction,
        cursor,
        &mut step,
        method,
        timer.next_attempt,
        first_attempt_at,
    )?;
    cursor.rows.put_step_execution(transaction, &step);
    Ok(())
}

pub(crate) fn apply_method_completed(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
    command: &StepMethodCompletedCommand,
) -> Result<StepCommandOutcome, ApplyError> {
    let Some(mut step) = load_invocation(
        transaction,
        cursor,
        command.invocation.as_ref(),
        command.attempt,
        StepInvocationPhase::Running,
    )?
    else {
        return Ok(StepCommandOutcome::Ignored(ok_reply()));
    };
    let Some(response) = response_for_invocation(&step, command) else {
        return Ok(StepCommandOutcome::MismatchedResult(format!(
            "the Worker result for Step execution {} does not match its running method",
            step.step_execution_id
        )));
    };
    if let step_method_completed_command::Response::WaitFor(wait_response) = response
        && let Some(condition) = wait_response.waiting_condition.as_ref()
        && !condition.sub_flow_conditions.is_empty()
        && let Err(detail) = crate::cross_flow::validate_child_start_conditions(
            cursor.record.flow_config.as_ref(),
            &condition.sub_flow_conditions,
        )
    {
        history_events::record_method_completed(
            transaction,
            cursor,
            &step,
            command,
            scope.now_micros,
        )?;
        close_flow(
            transaction,
            cursor,
            scope,
            FlowClosing::failed(FlowErrorType::InvalidUserFlowCode, detail),
        )?;
        return Ok(StepCommandOutcome::Applied(ok_reply()));
    }
    if !command.app_worker_id.is_empty() {
        cursor.record.sticky_app_worker_id = command.app_worker_id.clone();
    }
    history_events::record_method_completed(transaction, cursor, &step, command, scope.now_micros)?;
    end_running_attempt(transaction, cursor, &mut step, InvocationEnd::Finished)?;
    if let Some(invocation) = step.invocation.as_mut()
        && let Some(checkpoint) = invocation.heartbeat_checkpoint.take()
    {
        let holder = ValueHolder::HeartbeatCheckpoint {
            step_execution_id: step.step_execution_id.clone(),
            method: invocation.method,
        };
        release_stored_value(transaction, cursor.rows, &checkpoint, &holder, cursor.seq)?;
    }
    step.invocation = None;
    let outcome = match response {
        step_method_completed_command::Response::WaitFor(response) => {
            let effects = crate::command_handlers::flow_data::MethodEffects {
                writes: &response.upsert_attributes,
                deletions: &response.delete_from_channel,
                publications: &response.publish_to_channel,
                locals: &response.upsert_step_exe_locals,
                permission_mappings: response.action_permission_mappings.as_ref(),
                hard_delete_objects: &command.hard_delete_objects,
            };
            if let Err(rejection) = crate::command_handlers::flow_data::apply_method_effects(
                transaction,
                cursor,
                &mut step,
                &effects,
                scope,
            )? {
                return Ok(StepCommandOutcome::RejectedResult(rejection));
            }
            step.wait_for_pull_routing = command.pull_routing.clone();
            wait_conditions::register_wait(
                transaction,
                cursor,
                scope,
                &mut step,
                response.waiting_condition.as_ref(),
            )?
        }
        step_method_completed_command::Response::Execute(response) => {
            let effects = crate::command_handlers::flow_data::MethodEffects {
                writes: &response.upsert_attributes,
                deletions: &response.delete_from_channel,
                publications: &response.publish_to_channel,
                locals: &response.upsert_step_exe_locals,
                permission_mappings: response.action_permission_mappings.as_ref(),
                hard_delete_objects: &command.hard_delete_objects,
            };
            if let Err(rejection) = crate::command_handlers::flow_data::apply_method_effects(
                transaction,
                cursor,
                &mut step,
                &effects,
                scope,
            )? {
                return Ok(StepCommandOutcome::RejectedResult(rejection));
            }
            apply_step_decision(
                transaction,
                cursor,
                scope,
                &mut step,
                response.step_decision.as_ref(),
            )?
        }
    };
    if let Err(rejection) = outcome {
        return Ok(StepCommandOutcome::RejectedResult(rejection));
    }
    Ok(StepCommandOutcome::Applied(ok_reply()))
}

/// The completed response when it exists and belongs to the invocation's running method.
fn response_for_invocation<'command>(
    step: &StepExecutionRecord,
    command: &'command StepMethodCompletedCommand,
) -> Option<&'command step_method_completed_command::Response> {
    let method = step.invocation.as_ref()?.method;
    let response = command.response.as_ref()?;
    let is_matching_method = match response {
        step_method_completed_command::Response::WaitFor(_) => method == StepMethod::WaitFor as i32,
        step_method_completed_command::Response::Execute(_) => method == StepMethod::Execute as i32,
    };
    is_matching_method.then_some(response)
}
