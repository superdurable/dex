// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

use crate::Condition;

/// Creates durable timer conditions for Step waits.
pub struct Timer;

impl Timer {
    /// Returns a condition that becomes true after `duration` elapses.
    ///
    /// Timer duration is measured by Dex workflow time and survives Worker restarts.
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
