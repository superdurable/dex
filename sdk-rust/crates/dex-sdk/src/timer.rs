// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

use crate::Condition;

pub struct Timer;

impl Timer {
    pub fn by_duration(duration: Duration) -> Condition {
        Condition::timer(duration)
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum TimerId {
    ConditionId(String),
    ConditionIndex(usize),
}

impl TimerId {
    pub fn by_condition_id(id: impl Into<String>) -> Self {
        Self::ConditionId(id.into())
    }

    pub fn by_condition_index(index: usize) -> Self {
        Self::ConditionIndex(index)
    }
}
