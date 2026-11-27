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

/// Configures one [`crate::Client::wait_for_flow_with_options`] call: which run it waits for and
/// how long it waits.
///
/// [`Self::new`] waits for the latest run of the Flow ID without a client-side timeout, like
/// [`crate::Client::wait_for_flow`].
///
/// # Examples
///
/// ```no_run
/// use std::time::Duration;
///
/// use dex_sdk::{Client, FlowResult, SdkResult, WaitForFlowOptions};
///
/// fn wait_for_tracked_shipment(client: &Client, tracked_run_id: &str) -> SdkResult<FlowResult> {
///     client.wait_for_flow_with_options(
///         "shipment-42",
///         WaitForFlowOptions::new()
///             .run_id(tracked_run_id)
///             .timeout(Duration::from_secs(60)),
///     )
/// }
/// ```
///
/// Since 1.45.9.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct WaitForFlowOptions {
    pub(crate) run_id: String,
    pub(crate) timeout: Option<Duration>,
}

impl WaitForFlowOptions {
    /// Creates options that wait for the latest run without a client-side timeout.
    ///
    /// Since 1.45.9.
    pub fn new() -> Self {
        Self::default()
    }

    /// Waits for this run of the Flow ID instead of its latest run.
    ///
    /// Use it when the caller tracks one run, such as a parent that started a child Flow under a
    /// business Flow ID and recorded the returned run ID. An empty string, the default, waits for
    /// the latest run.
    ///
    /// When the run has already closed, the wait returns its result at once, even while a newer
    /// run of the same Flow ID is running. A run that does not exist, belongs to another Flow ID,
    /// or is being deleted after its retention period returns [`crate::SdkError::FlowNotFound`].
    /// A run ID that is not in its canonical 36-character lowercase hyphenated UUID form returns
    /// [`crate::SdkError::Service`] whose [`crate::ServiceError::code`] is
    /// [`crate::GrpcCode::InvalidArgument`].
    ///
    /// Returns these options with the supplied run ID.
    ///
    /// Since 1.45.9.
    #[must_use]
    pub fn run_id(mut self, run_id: impl Into<String>) -> Self {
        self.run_id = run_id.into();
        self
    }

    /// Bounds the wait to `timeout`, which must use whole seconds.
    ///
    /// When the timeout elapses while the run is still active, the wait returns
    /// [`crate::SdkError::LongPollTimeout`], like [`crate::Client::wait_for_flow_with_timeout`].
    /// A wait timeout does not stop the Flow. A timeout with a fractional second returns
    /// [`crate::SdkError::InvalidArgument`] without contacting Dex. By default the wait has no
    /// client-side timeout.
    ///
    /// Returns these options with the supplied timeout.
    ///
    /// Since 1.45.9.
    #[must_use]
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
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
