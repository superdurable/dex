// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

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
    is_non_retryable: bool,
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
            is_non_retryable: false,
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
    /// Changed in 1.45.9: the result is non-retryable when `error` is a non-retryable
    /// `HandlerError` or has one in its [`Error::source`] chain. The type name and
    /// message still come from `error` itself.
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
            is_non_retryable: has_non_retryable_source(&error),
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
            is_non_retryable: false,
            stack: Backtrace::force_capture(),
        }
    }

    /// Creates a failure that ends the current Step method attempt without further retries.
    ///
    /// Return it from [`crate::Step::wait_for`] or [`crate::Step::execute`] when a later
    /// attempt cannot succeed, such as rejected input or a request that a provider reports as
    /// permanent. Dex skips the remaining attempts of the method's
    /// [`crate::RetryPolicy`] and applies the Step's failure policy at once. By default the
    /// Flow fails. A `wait_for` configured to proceed on failure runs `execute`, and an
    /// `execute` configured to proceed to a recovery Step starts that Step. The recovery Step
    /// sees `error_type` and `message` in its recovery error.
    ///
    /// Captures a stack at this call site the same way [`Self::new`] does. Use
    /// [`Self::into_non_retryable`] to mark an error built with [`Self::from_error`]. A
    /// non-retryable failure ignores a retry-after request. Returned from an RPC handler, it
    /// has no effect, because RPC handlers never retry.
    ///
    /// It needs server-zd 0.1.38 or later. An older server ignores the request and retries the
    /// attempt under the retry policy.
    ///
    /// Since 1.45.9.
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_sdk::HandlerError;
    ///
    /// fn charge(card_accepted: bool) -> Result<(), HandlerError> {
    ///     if !card_accepted {
    ///         return Err(HandlerError::non_retryable("CardRejected", "card rejected"));
    ///     }
    ///     Ok(())
    /// }
    ///
    /// assert!(charge(false).unwrap_err().is_non_retryable());
    /// ```
    pub fn non_retryable(error_type: impl Into<String>, message: impl Into<String>) -> Self {
        Self::new(error_type, message).into_non_retryable()
    }

    /// Marks this failure as non-retryable and returns it.
    ///
    /// The error keeps its type, message, and stack. Dex then handles it as
    /// [`Self::non_retryable`] describes.
    ///
    /// Since 1.45.9.
    ///
    /// # Examples
    ///
    /// ```
    /// use dex_sdk::HandlerError;
    ///
    /// let error = HandlerError::from_error(std::fmt::Error).into_non_retryable();
    /// assert!(error.is_non_retryable());
    /// ```
    #[must_use]
    pub fn into_non_retryable(mut self) -> Self {
        self.is_non_retryable = true;
        self
    }

    /// Returns whether Dex ends the Step method attempt without further retries.
    ///
    /// Since 1.45.9.
    pub fn is_non_retryable(&self) -> bool {
        self.is_non_retryable
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
            is_non_retryable: false,
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

impl Error for HandlerError {
    /// Returns a fixed non-retryable marker for a non-retryable failure, so that
    /// [`HandlerError::from_error`] keeps the flag through wrapping errors; otherwise `None`.
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        if self.is_non_retryable {
            Some(&NON_RETRYABLE_MARKER)
        } else {
            None
        }
    }
}

/// The source of a non-retryable [`HandlerError`].
#[derive(Debug)]
struct NonRetryableMarker;

static NON_RETRYABLE_MARKER: NonRetryableMarker = NonRetryableMarker;

impl Display for NonRetryableMarker {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("Dex non-retryable failure")
    }
}

impl Error for NonRetryableMarker {}

/// Bounds the source walk; a hand-written `source` can form a cycle.
const MAX_SOURCE_DEPTH: usize = 64;

/// Returns whether `error` is a non-retryable [`HandlerError`] or has one in its source chain.
fn has_non_retryable_source(error: &dyn Error) -> bool {
    let mut source = error.source();
    for _ in 0..MAX_SOURCE_DEPTH {
        let Some(current) = source else {
            return false;
        };
        if current.is::<NonRetryableMarker>()
            || current
                .downcast_ref::<HandlerError>()
                .is_some_and(HandlerError::is_non_retryable)
        {
            return true;
        }
        source = current.source();
    }
    false
}

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Debug)]
    struct ChargeError(HandlerError);

    impl Display for ChargeError {
        fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
            formatter.write_str("charge failed")
        }
    }

    impl Error for ChargeError {
        fn source(&self) -> Option<&(dyn Error + 'static)> {
            Some(&self.0)
        }
    }

    #[test]
    fn from_error_inherits_non_retryable_from_the_error_and_its_sources() {
        let marked = HandlerError::non_retryable("CardRejected", "rejected");
        assert!(HandlerError::from_error(marked).is_non_retryable());

        let wrapped = ChargeError(HandlerError::non_retryable("CardRejected", "rejected"));
        let error = HandlerError::from_error(wrapped);
        assert!(error.is_non_retryable());
        assert_eq!("charge failed", error.to_string());
        assert!(error.error_type().ends_with("ChargeError"));

        let retryable = ChargeError(HandlerError::new("Transient", "try again"));
        assert!(!HandlerError::from_error(retryable).is_non_retryable());
        assert!(
            HandlerError::new("Transient", "try again")
                .source()
                .is_none()
        );
    }

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
