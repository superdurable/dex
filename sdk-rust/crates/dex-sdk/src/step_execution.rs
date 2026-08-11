// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::Step;

#[derive(Clone, Debug, Eq, PartialEq)]
/// Identifies one execution of a Step type within a Flow run.
///
/// Execution numbers are one-based and default to `1`.
pub struct StepExecutionId {
    pub(crate) step_type: &'static str,
    pub(crate) execution_number: u32,
}

impl StepExecutionId {
    /// Targets the first execution of `step`.
    pub fn of<SomeStep: Step>(step: &SomeStep) -> Self {
        Self {
            step_type: step.step_type(),
            execution_number: 1,
        }
    }

    /// Selects a one-based execution number for repeated Step executions.
    pub fn execution_number(mut self, execution_number: u32) -> Self {
        self.execution_number = execution_number;
        self
    }
}
