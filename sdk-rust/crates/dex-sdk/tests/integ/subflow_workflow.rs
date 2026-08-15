// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::Duration;

use dex_sdk::{
    Context, Flow, FlowConfig, FlowTimeoutPolicy, HandlerError, HandlerResult, Step, StepDecision,
    StepList, SubFlow, SubFlowOptions, SubFlowReusePolicy, Timer, Wait,
};

use crate::basic_abnormal_exit_workflow::BasicAbnormalExitWorkflow;
use crate::basic_workflow::BasicWorkflow;
use crate::timer_workflow::TimerWorkflow;

pub(crate) struct SingleSubFlowParent {
    pub(crate) start: SingleSubFlowStep,
}

impl SingleSubFlowParent {
    pub(crate) fn new(reuse_policy: Option<SubFlowReusePolicy>) -> Self {
        Self {
            start: SingleSubFlowStep { reuse_policy },
        }
    }
}

impl Flow for SingleSubFlowParent {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

pub(crate) struct SingleSubFlowStep {
    reuse_policy: Option<SubFlowReusePolicy>,
}

impl Step for SingleSubFlowStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: i32) -> HandlerResult<Wait> {
        let options = SubFlowOptions::new()
            .timeout(Duration::from_secs(3_600))
            .timeout_policy(FlowTimeoutPolicy::Cancel);
        let options = match self.reuse_policy {
            Some(policy) => options.reuse_policy(policy),
            None => options,
        };
        Ok(Wait::until(sub_flow_with_options(
            &BasicWorkflow::new(),
            input,
            options,
        )?))
    }

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let result = SubFlow::condition_result(context)?;
        let output = result.single_output::<i32>().map_err(handler_error)?;
        Ok(StepDecision::graceful_complete(format!(
            "{}|{:?}|{output}",
            SubFlow::flow_id(context)?,
            result.status()
        )))
    }
}

pub(crate) struct AllSubFlowParent {
    pub(crate) start: AllSubFlowStep,
}

impl AllSubFlowParent {
    pub(crate) fn new() -> Self {
        Self {
            start: AllSubFlowStep,
        }
    }
}

impl Flow for AllSubFlowParent {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

pub(crate) struct AllSubFlowStep;

impl Step for AllSubFlowStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: i32) -> HandlerResult<Wait> {
        Ok(Wait::all_of([
            sub_flow(&BasicWorkflow::new(), input)?,
            sub_flow(&BasicWorkflow::new(), input + 10)?,
        ]))
    }

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let mut values = Vec::new();
        for index in 0..2 {
            let result = SubFlow::condition_result_at(context, index)?;
            values.push(format!(
                "{}|{:?}|{}",
                SubFlow::flow_id_at(context, index)?,
                result.status(),
                result.single_output::<i32>().map_err(handler_error)?
            ));
        }
        Ok(StepDecision::graceful_complete(values.join(";")))
    }
}

pub(crate) struct AnySubFlowParent {
    pub(crate) start: AnySubFlowStep,
}

impl AnySubFlowParent {
    pub(crate) fn new() -> Self {
        Self {
            start: AnySubFlowStep,
        }
    }
}

impl Flow for AnySubFlowParent {
    type StartInput = u64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

pub(crate) struct AnySubFlowStep;

impl Step for AnySubFlowStep {
    type Input = u64;

    fn wait_for(&self, _context: &mut Context, input: u64) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            Timer::by_duration(Duration::ZERO),
            sub_flow(&TimerWorkflow::new(), input)?,
        ]))
    }

    fn execute(&self, context: &mut Context, _input: u64) -> HandlerResult<StepDecision> {
        let result = SubFlow::condition_result(context)?;
        let rejected_output = result.single_output::<i32>().is_err();
        Ok(StepDecision::graceful_complete(format!(
            "{}|{:?}|{}|{rejected_output}",
            SubFlow::flow_id(context)?,
            result.status(),
            result.is_terminal()
        )))
    }
}

pub(crate) struct TimerSubFlowParent {
    pub(crate) start: TimerSubFlowStep,
}

impl TimerSubFlowParent {
    pub(crate) fn new(reuse_policy: SubFlowReusePolicy) -> Self {
        Self {
            start: TimerSubFlowStep { reuse_policy },
        }
    }
}

impl Flow for TimerSubFlowParent {
    type StartInput = u64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

pub(crate) struct TimerSubFlowStep {
    reuse_policy: SubFlowReusePolicy,
}

impl Step for TimerSubFlowStep {
    type Input = u64;

    fn wait_for(&self, _context: &mut Context, input: u64) -> HandlerResult<Wait> {
        Ok(Wait::until(sub_flow_with_options(
            &TimerWorkflow::new(),
            input,
            SubFlowOptions::new().reuse_policy(self.reuse_policy),
        )?))
    }

    fn execute(&self, context: &mut Context, _input: u64) -> HandlerResult<StepDecision> {
        let result = SubFlow::condition_result(context)?;
        Ok(StepDecision::graceful_complete(format!(
            "{}|{:?}",
            SubFlow::flow_id(context)?,
            result.status()
        )))
    }
}

pub(crate) struct AbnormalSubFlowParent {
    pub(crate) start: AbnormalSubFlowStep,
}

impl AbnormalSubFlowParent {
    pub(crate) fn new() -> Self {
        Self {
            start: AbnormalSubFlowStep,
        }
    }
}

impl Flow for AbnormalSubFlowParent {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

pub(crate) struct AbnormalSubFlowStep;

impl Step for AbnormalSubFlowStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(sub_flow(
            &BasicAbnormalExitWorkflow::new(),
            input,
        )?))
    }

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let result = SubFlow::condition_result(context)?;
        Ok(StepDecision::graceful_complete(format!(
            "{}|{:?}",
            SubFlow::flow_id(context)?,
            result.status()
        )))
    }
}

pub(crate) struct ContinueAsNewSubFlowParent {
    pub(crate) start: ContinueAsNewSubFlowStep,
}

impl ContinueAsNewSubFlowParent {
    pub(crate) fn new() -> Self {
        Self {
            start: ContinueAsNewSubFlowStep,
        }
    }
}

impl Flow for ContinueAsNewSubFlowParent {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

pub(crate) struct ContinueAsNewSubFlowStep;

impl Step for ContinueAsNewSubFlowStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: i32) -> HandlerResult<Wait> {
        let options =
            SubFlowOptions::new().config_override(FlowConfig::new().continue_as_new_threshold(100));
        Ok(Wait::all_of([
            sub_flow_with_options(&BasicWorkflow::new(), input, options.clone())?,
            sub_flow_with_options(&TimerWorkflow::new(), 300, options)?,
        ]))
    }

    fn execute(&self, context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let completed = SubFlow::condition_result(context)?;
        let delayed = SubFlow::condition_result_at(context, 1)?;
        Ok(StepDecision::graceful_complete(format!(
            "{}|{}|{}|{:?}",
            SubFlow::flow_id(context)?,
            completed.single_output::<i32>().map_err(handler_error)?,
            SubFlow::flow_id_at(context, 1)?,
            delayed.status()
        )))
    }
}

fn sub_flow<SomeFlow: Flow>(
    flow: &SomeFlow,
    input: SomeFlow::StartInput,
) -> HandlerResult<dex_sdk::Condition> {
    SubFlow::run(flow, input).map_err(handler_error)
}

fn sub_flow_with_options<SomeFlow: Flow>(
    flow: &SomeFlow,
    input: SomeFlow::StartInput,
    options: SubFlowOptions,
) -> HandlerResult<dex_sdk::Condition> {
    SubFlow::run_with_options(flow, input, options).map_err(handler_error)
}

fn handler_error(error: impl std::fmt::Display) -> HandlerError {
    HandlerError::new(error.to_string())
}
