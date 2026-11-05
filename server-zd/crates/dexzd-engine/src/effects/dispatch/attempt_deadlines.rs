// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use dexzd_protocol::dex::{
    DispatchTaskErrorReason, PollWorkerTaskFrame, WorkerErrorResponse, poll_worker_task_frame,
};
use dexzd_protocol::dexzd::v1::StepAttemptFailureKind;
use dexzd_worker_dispatch::DispatchOutcome;
use tokio::time::Instant;

use super::attempt_failure::AttemptFailure;
use crate::effects::method_request::MethodAttemptState;

const DEFAULT_METHOD_TIMEOUT: Duration = Duration::from_secs(2 * 60 * 60);
const DEFAULT_HEARTBEAT_TIMEOUT: Duration = Duration::from_secs(60);

/// How the attempt's dispatch ended, before it becomes a command.
pub(super) enum AttemptEnd {
    Result(Box<PollWorkerTaskFrame>, String),
    Failure(AttemptFailure),
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

/// The method timeout, started before the attempt's first state read.
pub(super) struct MethodDeadline {
    method_timeout: Duration,
    /// When the method timeout expires, counted from the attempt's start.
    pub(super) expires_at: Instant,
    is_retry_deadline: bool,
}

impl MethodDeadline {
    /// Starts the configured method timeout now, or the Go Server default when it is not positive.
    pub(super) fn start(
        method_timeout_seconds: i32,
        retry_deadline_micros: i64,
        now_micros: i64,
    ) -> Self {
        let method_timeout =
            positive_seconds(method_timeout_seconds).unwrap_or(DEFAULT_METHOD_TIMEOUT);
        let retry_remaining_micros = retry_deadline_micros.saturating_sub(now_micros).max(0) as u64;
        let retry_remaining = Duration::from_micros(retry_remaining_micros);
        let is_retry_deadline = retry_remaining < method_timeout;
        Self {
            method_timeout,
            expires_at: Instant::now() + method_timeout.min(retry_remaining),
            is_retry_deadline,
        }
    }

    /// Returns true once the method timeout has expired, even before its timer fires.
    pub(super) fn has_expired(&self) -> bool {
        Instant::now() >= self.expires_at
    }

    pub(super) fn timeout_failure(&self) -> AttemptFailure {
        if self.is_retry_deadline {
            let mut failure = AttemptFailure::server(
                StepAttemptFailureKind::RetryDeadline,
                "retry policy total duration expired",
            );
            failure.is_non_retryable = true;
            return failure;
        }
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
            })
        }
        DispatchOutcome::DispatchError { reason, detail } => {
            let reason_name = DispatchTaskErrorReason::as_str_name(&reason);
            AttemptEnd::Failure(AttemptFailure::server(
                StepAttemptFailureKind::DispatchError,
                format!("{reason_name}: {detail}"),
            ))
        }
        DispatchOutcome::Cancelled => AttemptEnd::Canceled,
        DispatchOutcome::ServerClosing => AttemptEnd::Failure(AttemptFailure::server(
            StepAttemptFailureKind::DispatchError,
            "matching server is closing",
        )),
        DispatchOutcome::DeadlineExceeded => AttemptEnd::Failure(deadline.timeout_failure()),
    }
}
