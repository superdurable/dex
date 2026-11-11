// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList};

pub(crate) struct FlowDefinitionUploadWorkflow {
    flow_type: &'static str,
    increment: FlowDefinitionUploadIncrementStep,
}

impl FlowDefinitionUploadWorkflow {
    /// Creates a Flow whose Flow type is unique to one test run.
    pub(crate) fn new(flow_type: &str) -> Self {
        Self {
            flow_type: Box::leak(flow_type.to_string().into_boxed_str()),
            increment: FlowDefinitionUploadIncrementStep,
        }
    }
}

impl Flow for FlowDefinitionUploadWorkflow {
    type StartInput = i32;

    fn flow_type(&self) -> &'static str {
        self.flow_type
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.increment)
    }
}

struct FlowDefinitionUploadIncrementStep;

impl Step for FlowDefinitionUploadIncrementStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}
