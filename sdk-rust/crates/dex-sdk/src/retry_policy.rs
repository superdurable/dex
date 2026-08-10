// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct RetryPolicy {
    pub(crate) initial_interval: Option<Duration>,
    pub(crate) backoff_coefficient: Option<f64>,
    pub(crate) maximum_interval: Option<Duration>,
    pub(crate) maximum_attempts: Option<u32>,
    pub(crate) total_duration: Option<Duration>,
}

impl RetryPolicy {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn initial_interval(mut self, value: Duration) -> Self {
        self.initial_interval = Some(value);
        self
    }

    pub fn backoff_coefficient(mut self, value: f64) -> Self {
        self.backoff_coefficient = Some(value);
        self
    }

    pub fn maximum_interval(mut self, value: Duration) -> Self {
        self.maximum_interval = Some(value);
        self
    }

    pub fn maximum_attempts(mut self, value: u32) -> Self {
        self.maximum_attempts = Some(value);
        self
    }

    pub fn total_duration(mut self, value: Duration) -> Self {
        self.total_duration = Some(value);
        self
    }
}
