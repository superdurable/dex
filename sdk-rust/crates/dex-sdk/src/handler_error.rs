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
/// Prefer [`Self::from_error`] when wrapping another [`Error`] so Dex receives that
/// concrete type name. Use [`Self::new`] when you want an explicit application error
/// type string.
///
/// # Examples
///
/// ```
/// use std::error::Error;
/// use std::fmt::{Display, Formatter};
///
/// use dex_sdk::HandlerError;
///
/// #[derive(Debug)]
/// struct PaymentError;
///
/// impl Display for PaymentError {
///     fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
///         formatter.write_str("payment failed")
///     }
/// }
///
/// impl Error for PaymentError {}
///
/// fn charge() -> Result<(), HandlerError> {
///     Err(HandlerError::new("PaymentDeclined", "payment failed"))
/// }
///
/// fn charge_from_error() -> Result<(), HandlerError> {
///     Err(HandlerError::from_error(PaymentError))
/// }
///
/// fn charge_with_retry() -> Result<(), HandlerError> {
///     Err(HandlerError::retry_after(30, "PaymentDeclined", "payment failed"))
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
    /// Creates a handler error with an explicit error type and message.
    ///
    /// `error_type` is the stable label Dex stores as
    /// [`dex_protocol::dex::WorkerErrorResponse::error_type`]. Prefer a short
    /// application-specific name such as `"PaymentDeclined"` or a library type
    /// path. `message` is the developer-facing detail string.
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
    /// let error = HandlerError::new("PaymentDeclined", "payment failed");
    /// assert_eq!(error.to_string(), "payment failed");
    /// ```
    pub fn new(error_type: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            error_type: error_type.into(),
            retry_after_seconds: None,
            stack: Backtrace::force_capture(),
        }
    }

    /// Creates a handler error from another [`Error`], preserving its type name.
    ///
    /// Uses [`std::any::type_name`] of `E` as
    /// [`dex_protocol::dex::WorkerErrorResponse::error_type`] and `error.to_string()`
    /// as the detail. Captures a stack at this call site the same way
    /// [`Self::new`] does. Prefer this when mapping database, HTTP, or other
    /// library failures into a Dex handler failure.
    ///
    /// # Examples
    ///
    /// ```
    /// use std::error::Error;
    /// use std::fmt::{Display, Formatter};
    ///
    /// use dex_sdk::HandlerError;
    ///
    /// #[derive(Debug)]
    /// struct DbError;
    ///
    /// impl Display for DbError {
    ///     fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
    ///         formatter.write_str("connection refused")
    ///     }
    /// }
    ///
    /// impl Error for DbError {}
    ///
    /// let error = HandlerError::from_error(DbError);
    /// assert_eq!(error.to_string(), "connection refused");
    /// ```
    pub fn from_error<E: Error>(error: E) -> Self {
        Self {
            message: error.to_string(),
            error_type: std::any::type_name::<E>().to_string(),
            retry_after_seconds: None,
            stack: Backtrace::force_capture(),
        }
    }

    /// Requests the next retry interval while preserving this failure.
    ///
    /// Captures a stack at this call site the same way [`Self::new`] does.
    /// `after_seconds` is the delay Dex should wait before retrying the Step
    /// method. `error_type` is stored as
    /// [`dex_protocol::dex::WorkerErrorResponse::error_type`]. `message` is the
    /// failure detail reported to Dex.
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_sdk::HandlerError;
    ///
    /// let error = HandlerError::retry_after(30, "PaymentDeclined", "payment failed");
    /// assert_eq!(error.to_string(), "payment failed");
    /// ```
    pub fn retry_after(
        after_seconds: i32,
        error_type: impl Into<String>,
        message: impl Into<String>,
    ) -> Self {
        Self {
            message: message.into(),
            error_type: error_type.into(),
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

    pub(crate) fn attach_finalizer_error(&mut self, failure: &Self) {
        self.message
            .push_str("; buffered Stream finalization failed: ");
        self.message.push_str(&failure.message);
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

    #[derive(Debug)]
    struct SampleDbError;

    impl Display for SampleDbError {
        fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
            formatter.write_str("connection refused")
        }
    }

    impl Error for SampleDbError {}

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
    fn new_stores_explicit_error_type() {
        let error = HandlerError::new("PaymentDeclined", "payment failed");
        assert_eq!("PaymentDeclined", error.error_type());
        assert_eq!("payment failed", error.to_string());
    }

    #[test]
    fn from_error_stores_concrete_type_name() {
        let error = HandlerError::from_error(SampleDbError);
        assert_eq!(std::any::type_name::<SampleDbError>(), error.error_type());
        assert_eq!("connection refused", error.to_string());
    }

    #[test]
    fn new_captures_stack_at_construction_site() {
        fn marker_frame() -> HandlerError {
            HandlerError::new("TestFailure", "boom")
        }
        let rendered = format!("{}", marker_frame().stack_trace());
        assert!(
            rendered.contains("marker_frame"),
            "expected construction-site frame, got {rendered}"
        );
    }

    #[test]
    fn from_error_captures_stack_at_construction_site() {
        fn marker_frame() -> HandlerError {
            HandlerError::from_error(SampleDbError)
        }
        let rendered = format!("{}", marker_frame().stack_trace());
        assert!(
            rendered.contains("marker_frame"),
            "expected construction-site frame, got {rendered}"
        );
    }
}
