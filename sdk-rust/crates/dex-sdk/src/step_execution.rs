// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use crate::Step;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StepExecutionId {
    step_type: &'static str,
    execution_number: u32,
}

impl StepExecutionId {
    pub fn of<SomeStep: Step>(step: &SomeStep) -> Self {
        Self {
            step_type: step.step_type(),
            execution_number: 1,
        }
    }

    pub fn execution_number(mut self, execution_number: u32) -> Self {
        self.execution_number = execution_number;
        self
    }
}
