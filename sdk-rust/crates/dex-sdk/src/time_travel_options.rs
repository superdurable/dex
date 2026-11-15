// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::SystemTime;

use crate::{Step, StepExecutionId};

#[derive(Clone, Debug)]
/// Selects a historical point from which [`crate::Client::time_travel`] re-runs a Flow.
///
/// Time travel keeps the run ID and starts the next run attempt of that run.
///
/// Constructors identify exactly one time travel point. Builder methods add an audit reason and control
/// whether writes are reapplied. Write reapplication defaults to enabled.
pub struct TimeTravelOptions {
    pub(crate) point: TimeTravelPoint,
    pub(crate) reason: Option<String>,
    pub(crate) skip_writes_reapply: bool,
    pub(crate) sub_flow_reset_policy: Option<SubFlowResetPolicy>,
}

#[derive(Clone, Debug)]
pub(crate) enum TimeTravelPoint {
    Beginning,
    HistoryEventTime(SystemTime),
    StepType(&'static str),
    StepExecution(StepExecutionId, TimeTravelStepMethod),
    EventId(i64),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
#[non_exhaustive]
/// Decides what happens to a SubFlow condition that a restored Step still waits on.
///
/// [`crate::Client::time_travel`] and [`crate::Client::clone_fork_flow`] restore Steps from an
/// earlier state. A restored Step whose WaitFor waits on SubFlow conditions names, in each
/// condition, the child Flow that the Step started. The policy decides, per condition, whether
/// the restored wait attaches to that child, restarts the child, fails the condition, or rejects
/// the whole call. Without a policy, Dex uses [`SubFlowResetPolicy::AttachElseRestart`].
///
/// Attaching keeps the condition on its original child. When the child run has ended, its result
/// completes the condition. When the child still runs, the condition keeps waiting for it. A
/// child whose run no longer exists cannot be attached; the `AttachElse` policies then take their
/// second action. Under clone fork, the source run's children report only to the source run, so a
/// child that still runs cannot be attached either.
///
/// Restarting starts the child again with the start it originally received. Under time travel,
/// the child keeps its Flow ID and a still-running run of it is terminated first. Under clone
/// fork, the restart starts a new child Flow whose Flow ID derives from the new Flow ID, and the
/// source run's child never changes. A child whose run no longer exists cannot be restarted, and
/// the call fails.
///
/// Failing completes the condition with a failed child result whose error type is
/// [`crate::FlowErrorType::SubFlowAbandoned`]; the Step's Execute receives it like any failed
/// child. Under time travel, a child that still runs is terminated.
///
/// Rejecting fails the call with [`crate::SdkError::Service`] whose gRPC code is
/// `FailedPrecondition`, and nothing changes. Its detail names each Step execution and child Flow
/// ID that caused it.
///
/// ```
/// use dex_sdk::{SubFlowResetPolicy, TimeTravelOptions};
///
/// let options = TimeTravelOptions::from_beginning()
///     .sub_flow_reset_policy(SubFlowResetPolicy::AttachElseFail);
/// ```
///
/// Since 1.45.2.
pub enum SubFlowResetPolicy {
    /// Restarts every child, whether it ended or still runs.
    AlwaysRestart,
    /// Fails every condition, whether its child ended or still runs.
    AlwaysFail,
    /// Rejects the call when any restored Step waits on a SubFlow condition.
    AlwaysReject,
    /// Attaches each condition to its child and restarts a child that cannot be attached. Dex
    /// uses this policy when none is set.
    AttachElseRestart,
    /// Attaches each condition to its child and fails a condition whose child cannot be attached.
    AttachElseFail,
    /// Attaches each condition to its child and rejects the call when a child cannot be attached.
    AttachElseReject,
}

impl SubFlowResetPolicy {
    /// Returns the wire value of an optional policy; `None` lets the Server choose its default.
    pub(crate) fn wire_value(policy: Option<Self>) -> i32 {
        use dex_protocol::dex::SubFlowResetPolicy as ProtoSubFlowResetPolicy;
        (match policy {
            None => ProtoSubFlowResetPolicy::Unspecified,
            Some(Self::AlwaysRestart) => ProtoSubFlowResetPolicy::AlwaysRestart,
            Some(Self::AlwaysFail) => ProtoSubFlowResetPolicy::AlwaysFail,
            Some(Self::AlwaysReject) => ProtoSubFlowResetPolicy::AlwaysReject,
            Some(Self::AttachElseRestart) => ProtoSubFlowResetPolicy::AttachElseRestart,
            Some(Self::AttachElseFail) => ProtoSubFlowResetPolicy::AttachElseFail,
            Some(Self::AttachElseReject) => ProtoSubFlowResetPolicy::AttachElseReject,
        }) as i32
    }
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

    /// Resumes right after the change that recorded the history event `event_id`.
    ///
    /// Use the event ID that the Server's history events report. Events recorded together, such
    /// as a Step completion and the Steps it starts, share one point, so the restored state
    /// includes them all. An event inside a Step attempt resolves to the next point where the
    /// Flow can stop. The event must still be in the run's history window. A non-positive
    /// `event_id` makes [`crate::Client::time_travel`] return
    /// [`crate::SdkError::InvalidArgument`] before it sends the request.
    ///
    /// ```
    /// use dex_sdk::TimeTravelOptions;
    ///
    /// let options = TimeTravelOptions::from_event_id(42).reason("answer again");
    /// ```
    ///
    /// Since 1.45.2.
    pub fn from_event_id(event_id: i64) -> Self {
        Self::new(TimeTravelPoint::EventId(event_id))
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

    /// Decides what happens to the SubFlow conditions that restored Steps wait on.
    ///
    /// Without this option, Dex uses [`SubFlowResetPolicy::AttachElseRestart`]. Returns these
    /// options with the policy applied. See [`SubFlowResetPolicy`] for an example.
    ///
    /// Since 1.45.2.
    pub fn sub_flow_reset_policy(mut self, policy: SubFlowResetPolicy) -> Self {
        self.sub_flow_reset_policy = Some(policy);
        self
    }

    fn new(point: TimeTravelPoint) -> Self {
        Self {
            point,
            reason: None,
            skip_writes_reapply: false,
            sub_flow_reset_policy: None,
        }
    }
}

/// The request fields that select one history point, shared by ResetFlow and CloneForkFlow.
#[derive(Debug, Default, Eq, PartialEq)]
pub(crate) struct HistoryPointFields {
    pub(crate) history_event_time: String,
    pub(crate) step_type: String,
    pub(crate) step_execution_id: String,
    pub(crate) step_method: i32,
    pub(crate) event_id: i64,
}

impl TimeTravelPoint {
    /// Returns the request fields of this point; `format_time` renders an RFC 3339 time.
    pub(crate) fn fields(
        &self,
        format_time: impl FnOnce(SystemTime) -> crate::SdkResult<String>,
    ) -> crate::SdkResult<HistoryPointFields> {
        use dex_protocol::dex::FlowResetStepMethod;
        let mut fields = HistoryPointFields::default();
        match self {
            Self::Beginning => {}
            Self::HistoryEventTime(time) => fields.history_event_time = format_time(*time)?,
            Self::StepType(step_type) => fields.step_type = (*step_type).to_string(),
            Self::StepExecution(execution, method) => {
                fields.step_execution_id =
                    format!("{}-{}", execution.step_type, execution.execution_number);
                fields.step_method = (match method {
                    TimeTravelStepMethod::WaitFor => FlowResetStepMethod::WaitFor,
                    TimeTravelStepMethod::Execute => FlowResetStepMethod::Execute,
                }) as i32;
            }
            Self::EventId(event_id) => {
                if *event_id <= 0 {
                    return Err(crate::client::invalid(format!(
                        "history event ID must be positive, got {event_id}"
                    )));
                }
                fields.event_id = *event_id;
            }
        }
        Ok(fields)
    }
}
