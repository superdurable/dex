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
    Attribute, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, Timer, Wait,
};

pub const INTERRUPTIBLE_INTERRUPT: Rpc<(), ()> = Rpc::new("InterruptibleInterrupt");

#[derive(Default)]
pub struct InterruptibleExecutionFlow {
    run_until_cancelled: RunUntilCancelled,
}

impl InterruptibleExecutionFlow {
    fn interrupt(&self, context: &mut Context) -> HandlerResult<()> {
        interrupt_signal().set(context, "cancel".to_string())
    }
}

impl Flow for InterruptibleExecutionFlow {
    type StartInput = ();

    fn flow_type(&self) -> &'static str {
        "InterruptibleExecutionFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.run_until_cancelled)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&interrupt_signal())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(INTERRUPTIBLE_INTERRUPT, Self::interrupt)
    }
}

#[derive(Default)]
struct RunUntilCancelled;

impl Step for RunUntilCancelled {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_millis(
            1_500,
        ))))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        if interrupt_signal().get(context)?.as_deref() == Some("cancel") {
            return Ok(StepDecision::graceful_complete("cancelled".to_string()));
        }
        Ok(StepDecision::go_to(&RunUntilCancelled, ()))
    }
}

fn interrupt_signal() -> Attribute<String> {
    Attribute::new("interruptSignal")
}
