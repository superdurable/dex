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
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision, StepList,
    StepMovement, Wait,
};
use std::{
    sync::LazyLock,
    thread,
    time::{Duration, SystemTime},
};

#[derive(Default)]
pub struct StaticParallelStepsFlow {
    init: StaticInit,
    work_a: WorkA,
    work_b: WorkB,
}
impl Flow for StaticParallelStepsFlow {
    type StartInput = String;
    fn steps(&self) -> StepList<'_, String> {
        StepList::start(&self.init)
            .and(&self.work_a)
            .and(&self.work_b)
    }
}
#[derive(Default)]
struct StaticInit;
impl Step for StaticInit {
    type Input = String;
    fn step_type(&self) -> &'static str {
        "InitStep"
    }
    fn execute(&self, _: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&WorkA, input.clone()),
            StepMovement::to(&WorkB, input),
        ]))
    }
}
#[derive(Default)]
struct WorkA;
impl Step for WorkA {
    type Input = String;
    fn step_type(&self) -> &'static str {
        "WorkAStep"
    }
    fn execute(&self, _: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!("A:{input}")))
    }
}
#[derive(Default)]
struct WorkB;
impl Step for WorkB {
    type Input = String;
    fn step_type(&self) -> &'static str {
        "WorkBStep"
    }
    fn execute(&self, _: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!("B:{input}")))
    }
}

#[derive(Default)]
pub struct DynamicParallelStepsFlow {
    init: DynamicInit,
    work: DynamicWork,
}
impl Flow for DynamicParallelStepsFlow {
    type StartInput = usize;
    fn steps(&self) -> StepList<'_, usize> {
        StepList::start(&self.init).and(&self.work)
    }
}
#[derive(Default)]
struct DynamicInit;
impl Step for DynamicInit {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "InitStep"
    }
    fn execute(&self, _: &mut Context, count: usize) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many(
            (0..count).map(|index| StepMovement::to(&DynamicWork, index)),
        ))
    }
}
#[derive(Default)]
struct DynamicWork;
impl Step for DynamicWork {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "DoWorkStep"
    }
    fn execute(&self, _: &mut Context, input: usize) -> HandlerResult<StepDecision> {
        random_delay();
        Ok(StepDecision::graceful_complete(input))
    }
}

#[derive(Default)]
pub struct AwaitParallelStepsFlow {
    init: AwaitInit,
    work: AwaitWork,
    await_step: AwaitStep,
}
impl Flow for AwaitParallelStepsFlow {
    type StartInput = usize;
    fn steps(&self) -> StepList<'_, usize> {
        StepList::start(&self.init)
            .and(&self.work)
            .and(&self.await_step)
    }
    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&COMPLETE)
    }
}
#[derive(Default)]
struct AwaitInit;
impl Step for AwaitInit {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "InitStep"
    }
    fn execute(&self, _: &mut Context, count: usize) -> HandlerResult<StepDecision> {
        let mut movements = vec![StepMovement::to(&AwaitStep, count)];
        movements.extend((0..count).map(|index| StepMovement::to(&AwaitWork, index)));
        Ok(StepDecision::go_to_many(movements))
    }
}
#[derive(Default)]
struct AwaitWork;
impl Step for AwaitWork {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "DoWorkStep"
    }
    fn execute(&self, context: &mut Context, _: usize) -> HandlerResult<StepDecision> {
        random_delay();
        COMPLETE.publish(context, ())?;
        Ok(StepDecision::dead_end())
    }
}
#[derive(Default)]
struct AwaitStep;
impl Step for AwaitStep {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "AwaitStep"
    }
    fn wait_for(&self, _: &mut Context, count: usize) -> HandlerResult<Wait> {
        Ok(Wait::until(COMPLETE.for_n(count)))
    }
    fn execute(&self, _: &mut Context, count: usize) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(count))
    }
}
static COMPLETE: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("parallel-complete"));

#[derive(Default)]
pub struct FirstWinParallelStepsFlow {
    init: FirstWinInit,
    work: FirstWinWork,
}
impl Flow for FirstWinParallelStepsFlow {
    type StartInput = usize;
    fn steps(&self) -> StepList<'_, usize> {
        StepList::start(&self.init).and(&self.work)
    }
}
#[derive(Default)]
struct FirstWinInit;
impl Step for FirstWinInit {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "InitStep"
    }
    fn execute(&self, _: &mut Context, count: usize) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many(
            (0..count).map(|index| StepMovement::to(&FirstWinWork, index)),
        ))
    }
}
#[derive(Default)]
struct FirstWinWork;
impl Step for FirstWinWork {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "DoWorkStep"
    }
    fn execute(&self, _: &mut Context, input: usize) -> HandlerResult<StepDecision> {
        random_delay();
        Ok(StepDecision::graceful_complete(input).cancel_sibling_step(&FirstWinWork))
    }
}

fn random_delay() {
    let nanos = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .expect("system clock must follow Unix epoch")
        .subsec_nanos();
    thread::sleep(Duration::from_millis(50 + u64::from(nanos % 450)));
}
