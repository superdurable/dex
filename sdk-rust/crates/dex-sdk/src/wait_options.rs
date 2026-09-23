// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::{Duration, Instant};

use crate::{SdkError, SdkResult};

#[derive(Clone, Debug, Default)]
/// Configures one durable Step completion wait.
///
/// The server derives a stable Request ID from the Step execution when none is supplied.
/// Leave the maximum wait time at zero to wait indefinitely. A positive value bounds the
/// caller-visible wait. The accepted handler checks its deadline only on a later Workflow Task and
/// may retain its in-flight Update slot until then. Reattachments reuse that Update. A new
/// generation starts only after the handler completes with a deadline error.
pub struct WaitForStepCompletionOptions {
    pub(crate) request_id: String,
    pub(crate) maximum_wait_time: Duration,
}

impl WaitForStepCompletionOptions {
    /// Creates an infinite wait that uses the server-derived stable Request ID.
    pub fn new() -> Self {
        Self::default()
    }

    /// Overrides the stable Request ID derived from the Step execution.
    pub fn request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = request_id.into();
        self
    }

    /// Sets the caller-visible wait budget. Zero waits indefinitely.
    ///
    /// The accepted handler observes a positive deadline only on a later Workflow Task.
    pub fn maximum_wait_time(mut self, maximum_wait_time: Duration) -> Self {
        self.maximum_wait_time = maximum_wait_time;
        self
    }
}

#[derive(Clone, Debug, Default)]
/// Configures one durable Attribute match wait.
///
/// The server derives a stable Request ID from the Attribute condition when none is supplied.
/// Leave the maximum wait time at zero to wait indefinitely. A positive value bounds the
/// caller-visible wait. The accepted handler checks its deadline only on a later Workflow Task and
/// may retain its in-flight Update slot until then. Reattachments reuse that Update. A new
/// generation starts only after the handler completes with a deadline error.
pub struct WaitForAttributeOptions {
    pub(crate) request_id: String,
    pub(crate) maximum_wait_time: Duration,
}

impl WaitForAttributeOptions {
    /// Creates an infinite wait that uses the server-derived stable Request ID.
    pub fn new() -> Self {
        Self::default()
    }

    /// Overrides the stable Request ID derived from the Attribute condition.
    pub fn request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = request_id.into();
        self
    }

    /// Sets the caller-visible wait budget. Zero waits indefinitely.
    ///
    /// The accepted handler observes a positive deadline only on a later Workflow Task.
    pub fn maximum_wait_time(mut self, maximum_wait_time: Duration) -> Self {
        self.maximum_wait_time = maximum_wait_time;
        self
    }
}

pub(crate) struct ClientWaitBudget {
    deadline: Option<Instant>,
}

impl ClientWaitBudget {
    pub(crate) fn new(maximum_wait_time: Duration) -> SdkResult<Self> {
        crate::client::seconds32(maximum_wait_time)?;
        Ok(Self {
            deadline: (!maximum_wait_time.is_zero()).then(|| Instant::now() + maximum_wait_time),
        })
    }

    pub(crate) fn remaining_seconds(
        &self,
        operation: &'static str,
        flow_id: &str,
    ) -> SdkResult<i32> {
        let Some(deadline) = self.deadline else {
            return Ok(0);
        };
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(SdkError::wait_handler_timeout(operation, flow_id));
        }
        let seconds = remaining.as_secs() + u64::from(remaining.subsec_nanos() > 0);
        i32::try_from(seconds).map_err(|_| crate::client::invalid("Duration exceeds int32"))
    }
}
