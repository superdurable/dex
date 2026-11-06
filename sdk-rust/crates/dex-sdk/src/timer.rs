// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use crate::Condition;

/// Creates durable timer conditions for Step waits.
///
/// Returning a timer Condition from WaitFor lets Dex wait without keeping a Worker handler
/// running. Creating the Condition itself does not block or start a local timer.
///
/// ```
/// use dex_sdk::{Timer, Wait};
/// use std::time::Duration;
/// let wait = Wait::until(Timer::by_duration(Duration::from_secs(30)));
/// ```
pub struct Timer;

impl Timer {
    /// Returns a condition that becomes true after `duration` elapses.
    ///
    /// Timer duration is measured by Dex workflow time and survives Worker restarts.
    /// Durations must fit signed 32-bit whole seconds when the Worker submits the wait.
    /// Invalid durations fail wait-result mapping. Use [`crate::Context::has_timer_fired`] during
    /// Execute to distinguish a timer from another winning Condition.
    pub fn by_duration(duration: Duration) -> Condition {
        Condition::timer(duration)
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Selects one timer from a Step execution for [`crate::Client::skip_timer`].
pub enum TimerId {
    /// Targets the condition carrying this explicit condition ID.
    ConditionId(String),
    /// Targets the zero-based timer-condition index within the wait result.
    ConditionIndex(usize),
}

impl TimerId {
    /// Selects a timer by the ID assigned with [`Condition::with_id`].
    pub fn by_condition_id(id: impl Into<String>) -> Self {
        Self::ConditionId(id.into())
    }

    /// Selects a timer by its zero-based condition index.
    pub fn by_condition_index(index: usize) -> Self {
        Self::ConditionIndex(index)
    }
}
