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

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList, Wait};

#[derive(Default)]
pub struct StepFlow {
    first: StepFirst,
    second: StepSecond,
}

impl Flow for StepFlow {
    type StartInput = i32;

    fn flow_type(&self) -> &'static str {
        "StepFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

#[derive(Default)]
struct StepFirst;

impl Step for StepFirst {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        context.set_step_execution_local("input", input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&StepSecond, input + 1))
    }
}

#[derive(Default)]
struct StepSecond;

impl Step for StepSecond {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}
