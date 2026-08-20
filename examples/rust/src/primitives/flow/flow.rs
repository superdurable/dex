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

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, Wait,
};

pub const DESCRIBE: Rpc<(), String> = Rpc::new("Describe");

pub(crate) fn status() -> Attribute<String> {
    Attribute::new("status")
}

fn notify() -> Channel<()> {
    Channel::new("notify")
}

#[derive(Default)]
pub struct ExampleFlow {
    example: ExampleStep,
    finish: FinishStep,
}

impl ExampleFlow {
    fn describe(&self, context: &mut Context, _input: ()) -> HandlerResult<RpcResult<String>> {
        let value = status().get(context)?.unwrap_or_default();
        Ok(RpcResult::new(value))
    }
}

impl Flow for ExampleFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.example).and(&self.finish)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&status())
            .channel(&notify())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(DESCRIBE, Self::describe)
    }
}

#[derive(Default)]
struct ExampleStep;

impl Step for ExampleStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        status().set(context, "running".to_string())?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&FinishStep, input + 1))
    }
}

#[derive(Default)]
struct FinishStep;

impl Step for FinishStep {
    type Input = i32;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        status().set(context, "done".to_string())?;
        Ok(StepDecision::graceful_complete(input + 1))
    }
}
