// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dexzd_clock::WallClock;
use dexzd_protocol::dex::{
    self, DispatchTaskErrorReason, PollWorkerSlotKind, PollWorkerTask, PollWorkerTaskFrame,
    PollWorkerTaskRegister, poll_worker_task, poll_worker_task_frame,
};
use dexzd_protocol::task_queue_names::registered_task_queue_name_error;
use dexzd_request_validation::quote_go_string;
use tonic::Status;

use super::{DispatchOutcome, DispatchRequest, DispatchTaskPayload};
use crate::MatchingConfig;

pub(super) fn build_task(
    task_id: &str,
    payload: &DispatchTaskPayload,
    deadline: SystemTime,
) -> PollWorkerTask {
    let mut task = PollWorkerTask {
        task_id: task_id.to_owned(),
        deadline: Some(Default::default()),
        request: None,
    };
    let deadline = deadline
        .duration_since(UNIX_EPOCH)
        .expect("validated deadline is after Unix epoch");
    let timestamp = task
        .deadline
        .as_mut()
        .expect("task deadline was initialized");
    timestamp.seconds = i64::try_from(deadline.as_secs()).unwrap_or(i64::MAX);
    timestamp.nanos = i32::try_from(deadline.subsec_nanos()).expect("nanoseconds fit i32");
    task.request = Some(match payload {
        DispatchTaskPayload::WaitFor(value) => poll_worker_task::Request::WaitFor(value.clone()),
        DispatchTaskPayload::Execute(value) => poll_worker_task::Request::Execute(value.clone()),
        DispatchTaskPayload::Rpc(value) => poll_worker_task::Request::Rpc(value.clone()),
    });
    task
}

pub(super) fn validate_dispatch(
    request: &DispatchRequest,
    config: &MatchingConfig,
    clock: &dyn WallClock,
) -> Result<(), Status> {
    if request.flow_type.is_empty() || request.flow_type.len() > config.max_queue_length {
        return Err(Status::invalid_argument(
            "Flow type queue must have a valid length",
        ));
    }
    validate_dispatched_task_queue(&request.task_queue)?;
    if request
        .sticky_app_worker_id
        .as_ref()
        .is_some_and(|id| id.is_empty() || id.len() > config.max_queue_length)
    {
        return Err(Status::invalid_argument(
            "sticky queue must have a valid length",
        ));
    }
    if system_time_micros(request.deadline) <= clock.now_unix_micros() {
        return Err(Status::invalid_argument("future task deadline is required"));
    }
    if request.dispatch_wait == Some(Duration::ZERO) {
        return Err(Status::invalid_argument(
            "positive dispatch_wait is required",
        ));
    }
    Ok(())
}

pub(super) fn validate_registration(
    registration: &PollWorkerTaskRegister,
    config: &MatchingConfig,
) -> Result<(), Status> {
    let worker_id = registration.app_worker_id.as_bytes();
    if worker_id.is_empty() || worker_id.len() > 128 {
        return Err(Status::invalid_argument(
            "app_worker_id must have 1 to 128 characters",
        ));
    }
    if !worker_id
        .iter()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
    {
        return Err(Status::invalid_argument(
            "app_worker_id contains an invalid character",
        ));
    }
    if registration.supports_reserved_slot_dispatch {
        if !registration.queue.is_empty() {
            return Err(Status::invalid_argument(
                "a reserved-slot registration must leave queue empty",
            ));
        }
        if PollWorkerSlotKind::try_from(registration.slot_kind).is_err() {
            return Err(Status::invalid_argument("invalid slot_kind"));
        }
    } else {
        if registration.slot_kind != PollWorkerSlotKind::Unspecified as i32 {
            return Err(Status::invalid_argument(
                "slot_kind requires supports_reserved_slot_dispatch",
            ));
        }
        if registration.queue.is_empty() || registration.queue.len() > config.max_queue_length {
            return Err(Status::invalid_argument("queue must have a valid length"));
        }
    }
    if let Some(reason) = registered_task_queue_name_error(&registration.task_queue) {
        return Err(Status::invalid_argument(format!(
            "invalid task_queue {}: {reason}",
            quote_go_string(&registration.task_queue)
        )));
    }
    if registration.flow_types.len() > config.max_flow_types {
        return Err(Status::invalid_argument("too many flow_types"));
    }
    if registration
        .flow_types
        .iter()
        .any(|flow_type| flow_type.len() > config.max_flow_type_length)
    {
        return Err(Status::invalid_argument(
            "flow_type exceeds the length limit",
        ));
    }
    if !registration.supports_reserved_slot_dispatch
        && registration.queue != registration.app_worker_id
        && !registration.flow_types.contains(&registration.queue)
    {
        return Err(Status::invalid_argument(
            "queue must be app_worker_id or a declared flow_type",
        ));
    }
    Ok(())
}

/// Checks the task queue of a server-built dispatch, which holds a stored, validated name.
pub(super) fn validate_dispatched_task_queue(task_queue: &str) -> Result<(), Status> {
    match registered_task_queue_name_error(task_queue) {
        Some(reason) => Err(Status::invalid_argument(format!(
            "invalid task queue {}: {reason}",
            quote_go_string(task_queue)
        ))),
        None => Ok(()),
    }
}

pub(super) fn is_final(frame: &PollWorkerTaskFrame) -> bool {
    match &frame.output {
        Some(
            poll_worker_task_frame::Output::WorkerError(_) | poll_worker_task_frame::Output::Rpc(_),
        ) => true,
        Some(poll_worker_task_frame::Output::WaitFor(output)) => matches!(
            output.output,
            None | Some(dex::invoke_wait_for_method_output::Output::Result(_))
        ),
        Some(poll_worker_task_frame::Output::Execute(output)) => matches!(
            output.output,
            None | Some(dex::invoke_execute_method_output::Output::Result(_))
        ),
        None => false,
    }
}

pub(super) fn disconnected_outcome(detail: &str) -> DispatchOutcome {
    DispatchOutcome::DispatchError {
        reason: DispatchTaskErrorReason::WorkerDisconnected,
        detail: detail.to_owned(),
    }
}

pub(super) fn system_time_micros(time: SystemTime) -> i64 {
    time.duration_since(UNIX_EPOCH)
        .map(|duration| i64::try_from(duration.as_micros()).unwrap_or(i64::MAX))
        .unwrap_or(i64::MIN)
}
