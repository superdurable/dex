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
/// Selects a historical point from which [`crate::Client::reset_flow`] creates a new run.
///
/// Constructors identify exactly one reset point. Builder methods add an audit reason and control
/// whether Channel messages and locking RPCs are reapplied. Both reapply switches default to
/// `false`, so historical operations are reapplied.
pub struct ResetFlowOptions {
    pub(crate) point: ResetPoint,
    pub(crate) reason: Option<String>,
    pub(crate) skip_channel_messages_reapply: bool,
    pub(crate) skip_locking_rpc_reapply: bool,
}

#[derive(Clone, Debug)]
pub(crate) enum ResetPoint {
    Beginning,
    HistoryEventId(i64),
    HistoryEventTime(SystemTime),
    StepType(&'static str),
    StepExecution(StepExecutionId),
}

impl ResetFlowOptions {
    /// Resets before the first workflow-history event.
    pub fn from_beginning() -> Self {
        Self::new(ResetPoint::Beginning)
    }

    /// Resets at the specified server workflow-history event ID.
    pub fn from_history_event_id(event_id: i64) -> Self {
        Self::new(ResetPoint::HistoryEventId(event_id))
    }

    /// Resets at the last eligible history event at or before `event_time`.
    pub fn from_history_event_time(event_time: SystemTime) -> Self {
        Self::new(ResetPoint::HistoryEventTime(event_time))
    }

    /// Resets before the first execution of the selected Step type.
    pub fn from_step<SomeStep: Step>(step: &SomeStep) -> Self {
        Self::new(ResetPoint::StepType(step.step_type()))
    }

    /// Resets before the exact Step execution identified by `step_execution`.
    pub fn from_step_execution(step_execution: StepExecutionId) -> Self {
        Self::new(ResetPoint::StepExecution(step_execution))
    }

    /// Adds an operator-facing reason recorded with the reset.
    pub fn reason(mut self, reason: impl Into<String>) -> Self {
        self.reason = Some(reason.into());
        self
    }

    /// Controls whether post-reset Channel messages are skipped instead of reapplied.
    pub fn skip_channel_messages_reapply(mut self, skip: bool) -> Self {
        self.skip_channel_messages_reapply = skip;
        self
    }

    /// Controls whether post-reset locking RPCs are skipped instead of reapplied.
    pub fn skip_locking_rpc_reapply(mut self, skip: bool) -> Self {
        self.skip_locking_rpc_reapply = skip;
        self
    }

    fn new(point: ResetPoint) -> Self {
        Self {
            point,
            reason: None,
            skip_channel_messages_reapply: false,
            skip_locking_rpc_reapply: false,
        }
    }
}
