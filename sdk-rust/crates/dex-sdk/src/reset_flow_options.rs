// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::SystemTime;

use crate::{Step, StepExecutionId};

#[derive(Clone, Debug)]
pub enum ResetFlowOptions {
    Beginning,
    HistoryEventId(i64),
    HistoryEventTime(SystemTime),
    StepType(&'static str),
    StepExecution(StepExecutionId),
}

impl ResetFlowOptions {
    pub fn from_beginning() -> Self {
        Self::Beginning
    }

    pub fn from_history_event_id(event_id: i64) -> Self {
        Self::HistoryEventId(event_id)
    }

    pub fn from_history_event_time(event_time: SystemTime) -> Self {
        Self::HistoryEventTime(event_time)
    }

    pub fn from_step<SomeStep: Step>(step: &SomeStep) -> Self {
        Self::StepType(step.step_type())
    }

    pub fn from_step_execution(step_execution: StepExecutionId) -> Self {
        Self::StepExecution(step_execution)
    }
}
