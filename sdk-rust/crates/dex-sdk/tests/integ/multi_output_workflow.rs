// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList, StepMovement};

pub(crate) struct MultiOutputWorkflow {
    pub(crate) string_step: MultiOutputStringStep,
    pub(crate) integer_step: MultiOutputIntegerStep,
    start: MultiOutputStartStep,
}

impl MultiOutputWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            string_step: MultiOutputStringStep,
            integer_step: MultiOutputIntegerStep,
            start: MultiOutputStartStep,
        }
    }
}

impl Flow for MultiOutputWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.string_step)
            .and(&self.integer_step)
    }
}

struct MultiOutputStartStep;

impl Step for MultiOutputStartStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&MultiOutputStringStep, ()),
            StepMovement::to(&MultiOutputIntegerStep, ()),
        ]))
    }
}

pub(crate) struct MultiOutputStringStep;

impl Step for MultiOutputStringStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("branch-one".to_string()))
    }
}

pub(crate) struct MultiOutputIntegerStep;

impl Step for MultiOutputIntegerStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(42_i32))
    }
}
