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
    StepList, StepMovement, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const INTERRUPTIBLE_INTERRUPT: Rpc<(), ()> = Rpc::new("InterruptibleInterrupt");

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct WorkJobParametersInput {
    pub job_upper_bound: i32,
    pub progress: i32,
}

#[derive(Default)]
pub struct InterruptibleExecutionFlow {
    init: Init,
    work_a_execution: WorkAExecution,
    work_n_execution: WorkNExecution,
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
        StepList::start(&self.init)
            .and(&self.work_a_execution)
            .and(&self.work_n_execution)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&interrupt_signal())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(INTERRUPTIBLE_INTERRUPT, Self::interrupt)
    }
}

#[derive(Default)]
struct Init;

impl Step for Init {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let input = WorkJobParametersInput {
            job_upper_bound: 15,
            progress: 1,
        };
        Ok(StepDecision::go_to_many([
            StepMovement::to(&WorkAExecution, input.clone()),
            StepMovement::to(&WorkNExecution, input),
        ]))
    }
}

#[derive(Default)]
struct WorkAExecution;

impl Step for WorkAExecution {
    type Input = WorkJobParametersInput;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(1))))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if interrupt_signal().get(context)?.as_deref() == Some("cancel") {
            println!("A: Interrupted!");
            return Ok(StepDecision::graceful_complete(()));
        }
        if input.progress > input.job_upper_bound {
            println!("Executing WorkAExecution completed");
            return Ok(StepDecision::graceful_complete(()));
        }
        println!(
            "[{}][{}]: Doing job {}",
            context.flow_id(),
            context.step_execution_id(),
            input.progress
        );
        Ok(StepDecision::go_to(
            &WorkAExecution,
            WorkJobParametersInput {
                job_upper_bound: input.job_upper_bound,
                progress: input.progress + 1,
            },
        ))
    }
}

#[derive(Default)]
struct WorkNExecution;

impl Step for WorkNExecution {
    type Input = WorkJobParametersInput;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(3))))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if interrupt_signal().get(context)?.as_deref() == Some("cancel") {
            println!("N: Interrupted!");
            return Ok(StepDecision::graceful_complete(()));
        }
        if input.progress > input.job_upper_bound {
            println!("Executing WorkNExecution completed");
            return Ok(StepDecision::graceful_complete(()));
        }
        println!(
            "[{}][{}]: Processing job {}",
            context.flow_id(),
            context.step_execution_id(),
            input.progress
        );
        Ok(StepDecision::go_to(
            &WorkNExecution,
            WorkJobParametersInput {
                job_upper_bound: input.job_upper_bound,
                progress: input.progress + 1,
            },
        ))
    }
}

fn interrupt_signal() -> Attribute<String> {
    Attribute::new("interruptSignal")
}
