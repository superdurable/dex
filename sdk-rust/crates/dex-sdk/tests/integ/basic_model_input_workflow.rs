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

#[derive(serde::Deserialize, serde::Serialize)]
pub(crate) struct BasicModelInput {
    pub(crate) value: i32,
}

pub(crate) struct BasicModelInputWorkflow {
    start: BasicModelInputStep,
}

impl BasicModelInputWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: BasicModelInputStep,
        }
    }
}

impl Flow for BasicModelInputWorkflow {
    type StartInput = BasicModelInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct BasicModelInputStep;

impl Step for BasicModelInputStep {
    type Input = BasicModelInput;

    fn execute(
        &self,
        _context: &mut Context,
        input: BasicModelInput,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input.value))
    }
}
