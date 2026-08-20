// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::time::Duration;

use dex_sdk::{
    Context, Flow, FlowTimeoutPolicy, HandlerError, HandlerResult, Step, StepDecision, StepList,
    SubFlow, SubFlowOptions, Wait,
};

#[derive(Default)]
pub struct SubFlowChildFlow {
    start: SubFlowChildStep,
}

impl Flow for SubFlowChildFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

#[derive(Default)]
struct SubFlowChildStep;

impl Step for SubFlowChildStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

#[derive(Default)]
pub struct SubFlowParentFlow {
    start: SubFlowParentStep,
}

impl SubFlowParentFlow {
    pub fn new() -> Self {
        Self {
            start: SubFlowParentStep,
        }
    }
}

impl Flow for SubFlowParentFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

#[derive(Default)]
struct SubFlowParentStep;

impl Step for SubFlowParentStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        let child = SubFlowChildFlow::default();
        let condition = SubFlow::run_with_options(
            &child,
            input,
            SubFlowOptions::new()
                .timeout(Duration::from_secs(3600))
                .timeout_policy(FlowTimeoutPolicy::Cancel),
        )
        .map_err(HandlerError::from_error)?;
        Ok(Wait::until(condition))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let result = SubFlow::condition_result(context)?;
        let output: i32 = result
            .single_output()
            .map_err(HandlerError::from_error)?;
        let flow_id = SubFlow::flow_id_at(context, 0)?;
        Ok(StepDecision::graceful_complete(format!(
            "{flow_id}|{output}"
        )))
    }
}
