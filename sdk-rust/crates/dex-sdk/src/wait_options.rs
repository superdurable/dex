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
/// An abandoned infinite wait remains in flight until completion or Flow closure.
pub struct WaitForStepCompletionOptions {
    pub(crate) request_id: String,
    pub(crate) maximum_wait_time: Duration,
}

impl WaitForStepCompletionOptions {
    /// Creates empty options. The Client requires a Request ID when the wait begins.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the caller-owned idempotency key for this logical wait.
    pub fn request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = request_id.into();
        self
    }

    /// Sets the total handler wait budget. Zero waits indefinitely.
    pub fn maximum_wait_time(mut self, maximum_wait_time: Duration) -> Self {
        self.maximum_wait_time = maximum_wait_time;
        self
    }
}

#[derive(Clone, Debug, Default)]
/// Configures one durable Attribute match wait.
///
/// An abandoned infinite wait remains in flight until a match or Flow closure.
pub struct WaitForAttributeOptions {
    pub(crate) request_id: String,
    pub(crate) maximum_wait_time: Duration,
}

impl WaitForAttributeOptions {
    /// Creates empty options. The Client requires a Request ID when the wait begins.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the caller-owned idempotency key for this logical predicate.
    pub fn request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = request_id.into();
        self
    }

    /// Sets the total handler wait budget. Zero waits indefinitely.
    pub fn maximum_wait_time(mut self, maximum_wait_time: Duration) -> Self {
        self.maximum_wait_time = maximum_wait_time;
        self
    }
}

pub(crate) struct ClientWaitBudget {
    deadline: Option<Instant>,
}

impl ClientWaitBudget {
    pub(crate) fn new(request_id: &str, maximum_wait_time: Duration) -> SdkResult<Self> {
        if request_id.is_empty() {
            return Err(crate::client::invalid("wait request ID is required"));
        }
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
