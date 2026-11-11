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
/// The Server does not read the request ID for this wait.
/// `request_timeout` bounds the SDK call across transport reattachments.
pub struct WaitForStepCompletionOptions {
    pub(crate) request_id: String,
    pub(crate) request_timeout: Duration,
}

impl WaitForStepCompletionOptions {
    /// Creates an infinite wait.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets an optional request ID; the Server does not read it.
    pub fn request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = request_id.into();
        self
    }

    /// Sets the total SDK call budget. Zero waits indefinitely.
    pub fn request_timeout(mut self, request_timeout: Duration) -> Self {
        self.request_timeout = request_timeout;
        self
    }
}

#[derive(Clone, Debug, Default)]
/// Configures one durable Attribute match wait.
///
/// The Server does not read the request ID for this wait.
/// `request_timeout` bounds the SDK call across transport reattachments.
pub struct WaitForAttributeOptions {
    pub(crate) request_id: String,
    pub(crate) request_timeout: Duration,
}

impl WaitForAttributeOptions {
    /// Creates an infinite wait.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets an optional request ID; the Server does not read it.
    pub fn request_id(mut self, request_id: impl Into<String>) -> Self {
        self.request_id = request_id.into();
        self
    }

    /// Sets the total SDK call budget. Zero waits indefinitely.
    pub fn request_timeout(mut self, request_timeout: Duration) -> Self {
        self.request_timeout = request_timeout;
        self
    }
}

pub(crate) struct ClientRequestAttempt {
    pub(crate) request_timeout_seconds: i32,
    pub(crate) transport_timeout: Option<Duration>,
}

pub(crate) struct ClientRequestBudget {
    deadline: Option<Instant>,
}

impl ClientRequestBudget {
    pub(crate) fn new(request_timeout: Duration) -> SdkResult<Self> {
        crate::client::seconds32(request_timeout)?;
        Ok(Self {
            deadline: (!request_timeout.is_zero()).then(|| Instant::now() + request_timeout),
        })
    }

    pub(crate) fn next_attempt(
        &self,
        operation: &'static str,
        flow_id: &str,
    ) -> SdkResult<ClientRequestAttempt> {
        let Some(deadline) = self.deadline else {
            return Ok(ClientRequestAttempt {
                request_timeout_seconds: 0,
                transport_timeout: None,
            });
        };
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(SdkError::request_timeout(operation, flow_id));
        }
        let seconds = remaining.as_secs() + u64::from(remaining.subsec_nanos() > 0);
        Ok(ClientRequestAttempt {
            request_timeout_seconds: i32::try_from(seconds)
                .map_err(|_| crate::client::invalid("Duration exceeds int32"))?,
            transport_timeout: Some(remaining),
        })
    }

    pub(crate) fn deadline(&self) -> Option<Instant> {
        self.deadline
    }

    pub(crate) fn has_deadline(&self) -> bool {
        self.deadline.is_some()
    }
}
