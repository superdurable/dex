// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

pub struct Wait {
    _private: (),
}

impl Wait {
    pub fn skip_immediately() -> Self {
        Self { _private: () }
    }

    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        for condition in conditions {
            let _ = condition;
        }
        Self { _private: () }
    }

    pub fn any_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        for condition in conditions {
            let _ = condition;
        }
        Self { _private: () }
    }

    pub fn any_combination_of(
        combinations: impl IntoIterator<Item = ConditionCombination>,
    ) -> Self {
        for combination in combinations {
            let _ = combination;
        }
        Self { _private: () }
    }
}

pub struct Condition {
    id: Option<String>,
    #[allow(dead_code)]
    kind: ConditionKind,
}

#[allow(dead_code)]
enum ConditionKind {
    Timer(Duration),
    Channel {
        name: String,
        instance: Option<String>,
        at_least: Option<usize>,
        at_most: Option<usize>,
    },
}

impl Condition {
    pub(crate) fn timer(duration: Duration) -> Self {
        Self {
            id: None,
            kind: ConditionKind::Timer(duration),
        }
    }

    pub(crate) fn channel(
        name: String,
        instance: Option<String>,
        at_least: Option<usize>,
        at_most: Option<usize>,
    ) -> Self {
        Self {
            id: None,
            kind: ConditionKind::Channel {
                name,
                instance,
                at_least,
                at_most,
            },
        }
    }

    pub fn with_id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }
}

pub struct ConditionCombination {
    _private: (),
}

impl ConditionCombination {
    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        for condition in conditions {
            let _ = condition;
        }
        Self { _private: () }
    }
}

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
