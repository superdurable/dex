// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

#[derive(Clone, Debug, Default, PartialEq)]
/// Overrides Dex retry timing for a Flow, `wait_for`, or `execute` operation.
///
/// Unset fields use server defaults. Intervals and total duration use [`Duration`]; maximum
/// attempts counts the initial attempt. A zero maximum attempts value uses the server behavior.
///
/// Step retries default to a one-second initial interval, a multiplier of 2, a 100-second
/// maximum interval, unlimited attempts, and four hours of total duration. Zero values select
/// those defaults. A maximum of one disables retries. Non-retryable handler failures do not retry.
///
/// Changed in 1.35.0: a Step's total duration starts at its first hand-out to a Worker.
/// It only gates scheduling another retry after failure; it never interrupts a running attempt.
/// Method and heartbeat timeouts still apply to that attempt. Initial queue and lock waits do
/// not consume the budget. Later backoff and queue waits do. A scheduled retry can start after
/// the budget expires. Returning attempt one without running resets the budget's start.
///
/// Whole-Flow retry uses only interval, multiplier, maximum interval, and maximum attempts.
/// [`Self::total_duration`] applies to Step methods, not whole-Flow retry.
/// Duration values must fit signed 32-bit whole seconds when submitted. Builders take ownership
/// and return the updated policy; validation occurs when the options are mapped for a request.
///
/// # Examples
///
/// ```
/// use dex_sdk::RetryPolicy;
/// use std::time::Duration;
///
/// let retry = RetryPolicy::new()
///     .initial_interval(Duration::from_secs(1))
///     .backoff_coefficient(2.0)
///     .maximum_interval(Duration::from_secs(30))
///     .maximum_attempts(5);
/// ```
pub struct RetryPolicy {
    pub(crate) initial_interval: Option<Duration>,
    pub(crate) backoff_coefficient: Option<f64>,
    pub(crate) maximum_interval: Option<Duration>,
    pub(crate) maximum_attempts: Option<u32>,
    pub(crate) total_duration: Option<Duration>,
}

impl RetryPolicy {
    /// Creates an empty policy that preserves every server default.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the delay before the first retry.
    ///
    /// Zero uses one second. The value must fit signed 32-bit whole seconds.
    pub fn initial_interval(mut self, value: Duration) -> Self {
        self.initial_interval = Some(value);
        self
    }

    /// Sets the multiplier applied to each successive retry interval.
    ///
    /// Zero uses 2. The retry delay grows by this multiplier until the maximum interval.
    pub fn backoff_coefficient(mut self, value: f64) -> Self {
        self.backoff_coefficient = Some(value);
        self
    }

    /// Caps the delay between retries.
    ///
    /// Zero uses 100 seconds. The value must fit signed 32-bit whole seconds.
    pub fn maximum_interval(mut self, value: Duration) -> Self {
        self.maximum_interval = Some(value);
        self
    }

    /// Sets the total attempt limit, including the initial attempt.
    ///
    /// Zero allows unlimited attempts within the total-duration budget for Steps. One disables retries.
    /// Values above the signed 32-bit maximum fail when the policy is submitted.
    pub fn maximum_attempts(mut self, value: u32) -> Self {
        self.maximum_attempts = Some(value);
        self
    }

    /// Sets the elapsed-time budget used to decide whether to retry a failed Step attempt.
    ///
    /// For Steps, time starts when Dex first hands the Step to a Worker. Dex checks this budget
    /// only after an attempt fails; expiration never ends a running attempt. Unset uses the Server
    /// default of four hours. Zero also selects that default. `value` uses [`Duration`] and is
    /// serialized in seconds; invalid durations fail when the policy is submitted. Returns this
    /// policy with the budget applied.
    ///
    /// Changed in 1.35.0: queueing before the first hand-out is excluded, and this budget no
    /// longer shortens a running attempt's method timeout. See [`RetryPolicy`] for retry queueing.
    ///
    /// ```
    /// use dex_sdk::RetryPolicy;
    /// use std::time::Duration;
    /// let retry = RetryPolicy::new().total_duration(Duration::from_secs(60));
    /// ```
    pub fn total_duration(mut self, value: Duration) -> Self {
        self.total_duration = Some(value);
        self
    }
}
