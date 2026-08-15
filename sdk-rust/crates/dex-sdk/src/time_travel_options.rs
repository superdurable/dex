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
/// Selects a historical point from which [`crate::Client::time_travel`] creates a new run.
///
/// Constructors identify exactly one time travel point. Builder methods add an audit reason and control
/// whether writes are reapplied. Write reapplication defaults to enabled.
pub struct TimeTravelOptions {
    pub(crate) point: TimeTravelPoint,
    pub(crate) reason: Option<String>,
    pub(crate) skip_writes_reapply: bool,
}

#[derive(Clone, Debug)]
pub(crate) enum TimeTravelPoint {
    Beginning,
    HistoryEventTime(SystemTime),
    StepType(&'static str),
    StepExecution(StepExecutionId, TimeTravelStepMethod),
}

#[derive(Clone, Copy, Debug)]
/// Selects the Step method used as a Step execution time travel boundary.
pub enum TimeTravelStepMethod {
    /// Reruns WaitFor and everything after it.
    WaitFor,
    /// Keeps the WaitFor result and reruns Execute and everything after it.
    Execute,
}

impl TimeTravelOptions {
    /// Resumes before the first workflow-history event.
    pub fn from_beginning() -> Self {
        Self::new(TimeTravelPoint::Beginning)
    }

    /// Resumes at the last eligible history event at or before `event_time`.
    pub fn from_history_event_time(event_time: SystemTime) -> Self {
        Self::new(TimeTravelPoint::HistoryEventTime(event_time))
    }

    /// Resumes before the first execution of the selected Step type.
    pub fn from_step<SomeStep: Step>(step: &SomeStep) -> Self {
        Self::new(TimeTravelPoint::StepType(step.step_type()))
    }

    /// Resumes before one method of the exact Step execution identified by `step_execution`.
    pub fn from_step_execution(
        step_execution: StepExecutionId,
        method: TimeTravelStepMethod,
    ) -> Self {
        Self::new(TimeTravelPoint::StepExecution(step_execution, method))
    }

    /// Adds an operator-facing reason recorded with time travel.
    pub fn reason(mut self, reason: impl Into<String>) -> Self {
        self.reason = Some(reason.into());
        self
    }

    /// Controls whether later RPCs, Channel publications, and Attribute writes are skipped.
    pub fn skip_writes_reapply(mut self, skip: bool) -> Self {
        self.skip_writes_reapply = skip;
        self
    }

    fn new(point: TimeTravelPoint) -> Self {
        Self {
            point,
            reason: None,
            skip_writes_reapply: false,
        }
    }
}
