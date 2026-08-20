// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::backtrace::Backtrace;
use std::error::Error;
use std::fmt::{Display, Formatter};

/// Result returned by application Step and RPC handlers.
pub type HandlerResult<T> = Result<T, HandlerError>;

/// Reports an application-defined Step or RPC failure to Dex.
///
/// Return this error from [`crate::Step::wait_for`], [`crate::Step::execute`], or an RPC handler.
/// Dex records the message and applies the configured retry or failure policy.
///
/// Every constructor captures a stack at the construction site with
/// [`Backtrace::force_capture`]. The Worker prefers that construction-site
/// stack over a wrap-site capture when encoding [`dex_protocol::dex::WorkerErrorResponse`].
///
/// # Examples
///
/// ```
/// use dex_sdk::HandlerError;
///
/// fn charge() -> Result<(), HandlerError> {
///     Err(HandlerError::new("payment failed"))
/// }
///
/// fn charge_with_retry() -> Result<(), HandlerError> {
///     Err(HandlerError::retry_after(30, "payment failed"))
/// }
/// ```
#[derive(Debug)]
pub struct HandlerError {
    message: String,
    error_type: String,
    retry_after_seconds: Option<i32>,
    stack: Backtrace,
}

impl HandlerError {
    /// Creates a handler error with a developer-facing message.
    ///
    /// Captures a stack at this call site so Dex can report the origin of the
    /// failure. Prefer constructing the error where the failure is detected
    /// rather than mapping it later through a distant helper.
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_sdk::HandlerError;
    ///
    /// let error = HandlerError::new("payment failed");
    /// assert_eq!(error.to_string(), "payment failed");
    /// ```
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            error_type: std::any::type_name::<Self>().to_string(),
            retry_after_seconds: None,
            stack: Backtrace::force_capture(),
        }
    }

    /// Requests the next retry interval while preserving this failure.
    ///
    /// Captures a stack at this call site the same way [`Self::new`] does.
    /// `after_seconds` is the delay Dex should wait before retrying the Step
    /// method. `message` is the failure detail reported to Dex.
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_sdk::HandlerError;
    ///
    /// let error = HandlerError::retry_after(30, "payment failed");
    /// assert_eq!(error.to_string(), "payment failed");
    /// ```
    pub fn retry_after(after_seconds: i32, message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            error_type: std::any::type_name::<Self>().to_string(),
            retry_after_seconds: Some(after_seconds),
            stack: Backtrace::force_capture(),
        }
    }

    pub(crate) fn retry_after_seconds(&self) -> i32 {
        self.retry_after_seconds.unwrap_or(0)
    }

    pub(crate) fn invalid_step_result(
        flow_type: &str,
        step_type: Option<&str>,
        method: &str,
        detail: impl std::fmt::Display,
    ) -> Self {
        let target = step_type.map_or_else(
            || format!("RPC in Flow {flow_type}"),
            |step_type| format!("Flow {flow_type} Step {step_type}"),
        );
        Self {
            message: format!("{target} {method} returned an invalid result: {detail}"),
            error_type: "dex_sdk::SdkError::InvalidStepResult".to_string(),
            retry_after_seconds: None,
            stack: Backtrace::force_capture(),
        }
    }

    pub(crate) fn error_type(&self) -> &str {
        &self.error_type
    }

    pub(crate) fn stack_trace(&self) -> &Backtrace {
        &self.stack
    }
}

impl Display for HandlerError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl Error for HandlerError {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn invalid_step_result_has_stable_context_and_type() {
        let error = HandlerError::invalid_step_result(
            "OrderFlow",
            Some("ApproveOrder"),
            "execute",
            "missing movement",
        );
        assert_eq!("dex_sdk::SdkError::InvalidStepResult", error.error_type());
        assert!(
            error
                .to_string()
                .contains("Flow OrderFlow Step ApproveOrder")
        );
    }

    #[test]
    fn new_captures_stack_at_construction_site() {
        fn marker_frame() -> HandlerError {
            HandlerError::new("boom")
        }
        let rendered = format!("{}", marker_frame().stack_trace());
        assert!(
            rendered.contains("marker_frame"),
            "expected construction-site frame, got {rendered}"
        );
    }
}
