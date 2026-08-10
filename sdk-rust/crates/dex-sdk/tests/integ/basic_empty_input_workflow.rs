// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList};

pub(crate) struct BasicEmptyInputWorkflow {
    first: BasicEmptyFirstStep,
    second: BasicEmptySecondStep,
}

impl BasicEmptyInputWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            first: BasicEmptyFirstStep,
            second: BasicEmptySecondStep,
        }
    }
}

impl Flow for BasicEmptyInputWorkflow {
    type StartInput = ();

    fn flow_type(&self) -> &'static str {
        "test-customized-flow-type"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct BasicEmptyFirstStep;

impl Step for BasicEmptyFirstStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&BasicEmptySecondStep, ()))
    }
}

struct BasicEmptySecondStep;

impl Step for BasicEmptySecondStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(()))
    }
}
