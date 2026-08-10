// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList, Wait};

pub(crate) struct BasicWorkflow {
    pub(crate) first: BasicFirstStep,
    pub(crate) second: BasicSecondStep,
}

impl BasicWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            first: BasicFirstStep,
            second: BasicSecondStep,
        }
    }
}

impl Flow for BasicWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

pub(crate) struct BasicFirstStep;

impl Step for BasicFirstStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: i32) -> HandlerResult<Wait> {
        context.set_step_execution_local("input", input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&BasicSecondStep, input + 1))
    }
}

pub(crate) struct BasicSecondStep;

impl Step for BasicSecondStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}
