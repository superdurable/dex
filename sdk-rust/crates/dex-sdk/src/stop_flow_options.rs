// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum StopType {
    Cancel,
    Terminate,
    Fail,
}

#[derive(Clone, Debug, Eq, PartialEq)]
/// Selects how [`crate::Client::stop_flow`] ends an active Flow.
///
/// Construct a value with [`Self::cancel`], [`Self::terminate`], or [`Self::fail`], then optionally
/// attach an operator-facing reason.
pub struct StopFlowOptions {
    pub(crate) stop_type: StopType,
    pub(crate) reason: Option<String>,
}

impl StopFlowOptions {
    fn new(stop_type: StopType) -> Self {
        Self {
            stop_type,
            reason: None,
        }
    }

    /// Requests cooperative cancellation so application cleanup can run.
    pub fn cancel() -> Self {
        Self::new(StopType::Cancel)
    }

    /// Terminates the Flow immediately without cooperative cleanup.
    pub fn terminate() -> Self {
        Self::new(StopType::Terminate)
    }

    /// Marks the Flow failed with the reason set by [`Self::reason`].
    pub fn fail() -> Self {
        Self::new(StopType::Fail)
    }

    /// Attaches a reason recorded with the stop operation.
    pub fn reason(mut self, value: impl Into<String>) -> Self {
        self.reason = Some(value.into());
        self
    }
}
