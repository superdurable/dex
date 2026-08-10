// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

pub struct Wait {
    pub(crate) kind: WaitKind,
}

pub(crate) enum WaitKind {
    SkipImmediately,
    AllOf(Vec<Condition>),
    AnyOf(Vec<Condition>),
    AnyCombinationOf(Vec<ConditionCombination>),
}

impl Wait {
    pub fn skip_immediately() -> Self {
        Self {
            kind: WaitKind::SkipImmediately,
        }
    }

    pub fn until(condition: Condition) -> Self {
        Self::all_of([condition])
    }

    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            kind: WaitKind::AllOf(conditions.into_iter().collect()),
        }
    }

    pub fn any_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            kind: WaitKind::AnyOf(conditions.into_iter().collect()),
        }
    }

    pub fn any_combination_of(
        combinations: impl IntoIterator<Item = ConditionCombination>,
    ) -> Self {
        Self {
            kind: WaitKind::AnyCombinationOf(combinations.into_iter().collect()),
        }
    }
}

pub struct Condition {
    pub(crate) id: Option<String>,
    pub(crate) kind: ConditionKind,
}

pub(crate) enum ConditionKind {
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
    pub(crate) conditions: Vec<Condition>,
}

impl ConditionCombination {
    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            conditions: conditions.into_iter().collect(),
        }
    }
}
