// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use dexzd_protocol::dex::{
    DispatchTaskErrorReason, PollWorkerTaskFrame, PollWorkerTaskRejectReason, WorkerErrorResponse,
    poll_worker_task_frame,
};
use dexzd_protocol::dexzd::v1::{StepAttemptFailureKind, StepAttemptReturnReason};
use dexzd_worker_dispatch::{DispatchOutcome, StepTaskEvent};
use tokio::time::Instant;

use super::attempt_failure::AttemptFailure;
use crate::effects::method_request::MethodAttemptState;

const DEFAULT_METHOD_TIMEOUT: Duration = Duration::from_secs(2 * 60 * 60);
const DEFAULT_HEARTBEAT_TIMEOUT: Duration = Duration::from_secs(60);

/// How the attempt's dispatch ended, before it becomes a command.
pub(super) enum AttemptEnd {
    Result(Box<PollWorkerTaskFrame>, String),
    Failure(AttemptFailure),
    /// The Worker never ran the attempt; it returns to pending without using a retry.
    Returned(StepAttemptReturnReason),
    Canceled,
}

/// The attempt's owner-memory timeouts.
pub(super) struct AttemptTimeouts {
    pub(super) method: MethodDeadline,
    pub(super) heartbeat_timeout: Duration,
}

impl AttemptTimeouts {
    /// Returns the attempt's method timeout and heartbeat timeout, with the Go Server defaults.
    pub(super) fn new(state: &MethodAttemptState, method: MethodDeadline) -> Self {
        let heartbeat_timeout_seconds = state
            .step_execution
            .options
            .as_ref()
            .map_or(0, |options| options.heartbeat_timeout_seconds);
        let heartbeat_timeout =
            positive_seconds(heartbeat_timeout_seconds).unwrap_or(DEFAULT_HEARTBEAT_TIMEOUT);
        Self {
            method,
            heartbeat_timeout,
        }
    }

    /// Returns the first expired deadline's failure, checked against the clock rather than the timers.
    pub(super) fn expired_failure(
        &self,
        heartbeat_deadline: Option<Instant>,
    ) -> Option<AttemptFailure> {
        if self.method.has_expired()
            && heartbeat_deadline.is_none_or(|deadline| self.method.expires_at <= deadline)
        {
            return Some(self.method.timeout_failure());
        }
        if heartbeat_deadline.is_some_and(|deadline| Instant::now() >= deadline) {
            return Some(self.heartbeat_timeout_failure());
        }
        self.method
            .has_expired()
            .then(|| self.method.timeout_failure())
    }

    pub(super) fn heartbeat_timeout_failure(&self) -> AttemptFailure {
        AttemptFailure::server(
            StepAttemptFailureKind::HeartbeatTimeout,
            format!("no heartbeat within {}s", self.heartbeat_timeout.as_secs()),
        )
    }
}

/// The method timeout, started when the task frame goes to the Worker.
pub(super) struct MethodDeadline {
    method_timeout: Duration,
    /// When the method timeout expires, counted from the attempt's start.
    pub(super) expires_at: Instant,
}

impl MethodDeadline {
    /// Starts the configured method timeout now, or the Go Server default when it is not positive.
    ///
    /// The retry policy's total duration never shortens it (plan D74).
    pub(super) fn start(method_timeout_seconds: i32) -> Self {
        let method_timeout =
            positive_seconds(method_timeout_seconds).unwrap_or(DEFAULT_METHOD_TIMEOUT);
        Self {
            method_timeout,
            expires_at: Instant::now() + method_timeout,
        }
    }

    /// Returns true once the method timeout has expired, even before its timer fires.
    pub(super) fn has_expired(&self) -> bool {
        Instant::now() >= self.expires_at
    }

    /// Returns the configured method timeout.
    pub(super) fn method_timeout(&self) -> Duration {
        self.method_timeout
    }

    /// Returns the failure of an attempt whose request was not built within the method timeout;
    /// no Worker received it.
    pub(super) fn build_timeout_failure(&self) -> AttemptFailure {
        AttemptFailure::not_run(
            StepAttemptFailureKind::MethodTimeout,
            format!(
                "method timeout of {}s expired while building the Worker request",
                self.method_timeout.as_secs()
            ),
        )
    }

    pub(super) fn timeout_failure(&self) -> AttemptFailure {
        AttemptFailure::server(
            StepAttemptFailureKind::MethodTimeout,
            format!(
                "method timeout of {}s expired",
                self.method_timeout.as_secs()
            ),
        )
    }
}

fn positive_seconds(seconds: i32) -> Option<Duration> {
    u64::try_from(seconds)
        .ok()
        .filter(|seconds| *seconds > 0)
        .map(Duration::from_secs)
}

/// Converts the final event of a sent task into the attempt's end, or returns `None` for a
/// non-final output.
pub(super) fn task_event_end(
    event: StepTaskEvent,
    deadline: &MethodDeadline,
) -> Option<AttemptEnd> {
    Some(match event {
        StepTaskEvent::Output(_) => return None,
        StepTaskEvent::Completed(outcome) => attempt_end(outcome, deadline),
        StepTaskEvent::Rejected {
            reason: PollWorkerTaskRejectReason::UnknownStep,
            ..
        } => AttemptEnd::Returned(StepAttemptReturnReason::Unsupported),
        StepTaskEvent::Rejected {
            reason: PollWorkerTaskRejectReason::NoCapacity,
            is_reserved_slot: false,
        } => AttemptEnd::Returned(StepAttemptReturnReason::LegacyNoCapacity),
        StepTaskEvent::Rejected {
            reason: PollWorkerTaskRejectReason::NoCapacity,
            is_reserved_slot: true,
        } => AttemptEnd::Failure(AttemptFailure::not_run(
            StepAttemptFailureKind::DispatchError,
            "a reserved-slot Worker declined the task for capacity",
        )),
        StepTaskEvent::Rejected { reason, .. } => AttemptEnd::Failure(AttemptFailure::not_run(
            StepAttemptFailureKind::DispatchError,
            format!(
                "the Worker declined the Step task with {}",
                reason.as_str_name()
            ),
        )),
        StepTaskEvent::NotDelivered => AttemptEnd::Returned(StepAttemptReturnReason::NotDelivered),
        StepTaskEvent::Disconnected { detail } => AttemptEnd::Failure(AttemptFailure::server(
            StepAttemptFailureKind::DispatchError,
            format!(
                "{}: {detail}",
                DispatchTaskErrorReason::WorkerDisconnected.as_str_name()
            ),
        )),
    })
}

/// Converts a dispatch outcome into the attempt's end.
pub(super) fn attempt_end(outcome: DispatchOutcome, deadline: &MethodDeadline) -> AttemptEnd {
    match outcome {
        DispatchOutcome::WorkerResult {
            frame,
            app_worker_id,
        } => AttemptEnd::Result(Box::new(frame), app_worker_id),
        DispatchOutcome::WorkerError {
            frame,
            app_worker_id,
        } => {
            let worker_error = match frame.output {
                Some(poll_worker_task_frame::Output::WorkerError(error)) => error,
                _ => WorkerErrorResponse {
                    detail: "Worker returned an error frame without an error".to_owned(),
                    ..WorkerErrorResponse::default()
                },
            };
            AttemptEnd::Failure(AttemptFailure {
                kind: StepAttemptFailureKind::WorkerError,
                server_detail: String::new(),
                worker_error: Some(worker_error),
                is_non_retryable: false,
                served_by_app_worker_id: app_worker_id,
                omitted_value_slots: None,
                is_not_run: false,
            })
        }
        DispatchOutcome::DispatchError { reason, detail } => {
            let reason_name = DispatchTaskErrorReason::as_str_name(&reason);
            AttemptEnd::Failure(AttemptFailure::server(
                StepAttemptFailureKind::DispatchError,
                format!("{reason_name}: {detail}"),
            ))
        }
        // A planned stop writes nothing; the successor dispatches the attempt (plan section 3.6).
        DispatchOutcome::Cancelled | DispatchOutcome::ServerClosing => AttemptEnd::Canceled,
        DispatchOutcome::DeadlineExceeded => AttemptEnd::Failure(deadline.timeout_failure()),
    }
}
