// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

/// Describes when Dex may invoke a Step's `execute` handler.
///
/// Build waits from durable timer and Channel [`Condition`] values. Combinators consume their
/// inputs and preserve order, which determines timer indexes used by [`crate::TimerId`].
///
/// # Examples
///
/// ```
/// use dex_sdk::{Channel, Timer, Wait};
/// use std::time::Duration;
///
/// let approvals = Channel::<String>::new("approvals");
/// let wait = Wait::any_of([
///     approvals.for_one().with_id("approved"),
///     Timer::by_duration(Duration::from_secs(300)).with_id("timeout"),
/// ]);
/// ```
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
    /// Skips `wait_for` and asks Dex to invoke `execute` immediately.
    pub fn skip_immediately() -> Self {
        Self {
            kind: WaitKind::SkipImmediately,
        }
    }

    /// Waits until one condition is satisfied.
    pub fn until(condition: Condition) -> Self {
        Self::all_of([condition])
    }

    /// Waits until every condition is satisfied.
    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            kind: WaitKind::AllOf(conditions.into_iter().collect()),
        }
    }

    /// Waits until at least one condition is satisfied.
    pub fn any_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            kind: WaitKind::AnyOf(conditions.into_iter().collect()),
        }
    }

    /// Waits until every condition in at least one combination is satisfied.
    pub fn any_combination_of(
        combinations: impl IntoIterator<Item = ConditionCombination>,
    ) -> Self {
        Self {
            kind: WaitKind::AnyCombinationOf(combinations.into_iter().collect()),
        }
    }
}

/// Represents one durable timer or Channel predicate.
///
/// Conditions are created by [`crate::Timer`], [`crate::Channel`], or [`crate::ChannelMap`]. Assign
/// an ID when application code must identify a timer independently of its position.
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

    /// Assigns a stable ID used by [`crate::TimerId::by_condition_id`].
    pub fn with_id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }
}

/// Groups conditions that must all succeed as one branch of [`Wait::any_combination_of`].
pub struct ConditionCombination {
    pub(crate) conditions: Vec<Condition>,
}

impl ConditionCombination {
    /// Creates a branch satisfied only when all `conditions` are satisfied.
    pub fn all_of(conditions: impl IntoIterator<Item = Condition>) -> Self {
        Self {
            conditions: conditions.into_iter().collect(),
        }
    }
}
