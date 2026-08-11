// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

#[derive(Clone, Debug, Default, PartialEq)]
/// Overrides Dex retry timing for a Flow, `wait_for`, or `execute` operation.
///
/// Unset fields use server defaults. Intervals and total duration use [`Duration`]; maximum
/// attempts counts the initial attempt. A zero maximum attempts value uses the server behavior.
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
    pub fn initial_interval(mut self, value: Duration) -> Self {
        self.initial_interval = Some(value);
        self
    }

    /// Sets the multiplier applied to each successive retry interval.
    pub fn backoff_coefficient(mut self, value: f64) -> Self {
        self.backoff_coefficient = Some(value);
        self
    }

    /// Caps the delay between retries.
    pub fn maximum_interval(mut self, value: Duration) -> Self {
        self.maximum_interval = Some(value);
        self
    }

    /// Sets the total attempt limit, including the initial attempt.
    pub fn maximum_attempts(mut self, value: u32) -> Self {
        self.maximum_attempts = Some(value);
        self
    }

    /// Sets the total elapsed-time limit for all attempts.
    pub fn total_duration(mut self, value: Duration) -> Self {
        self.total_duration = Some(value);
        self
    }
}
