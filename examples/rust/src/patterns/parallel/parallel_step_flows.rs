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
    Context, Flow, HandlerResult, Join, JoinRound, PersistenceSchema, Step, StepDecision, StepList,
    StepMovement, Wait,
};
use serde::{Deserialize, Serialize};
use std::{sync::LazyLock, thread, time::Duration};

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
        "DoWork"
    }
    fn execute(&self, _: &mut Context, input: usize) -> HandlerResult<StepDecision> {
        thread::sleep(Duration::from_millis(fastrand::u64(50..500)));
        Ok(StepDecision::graceful_complete(input))
    }
}

#[derive(Default)]
pub struct AwaitParallelStepsFlow {
    init: AwaitInit,
    work: AwaitWork,
    await_step: Await,
}
impl Flow for AwaitParallelStepsFlow {
    type StartInput = usize;
    fn steps(&self) -> StepList<'_, usize> {
        StepList::start(&self.init)
            .and(&self.work)
            .and(&self.await_step)
    }
    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().join(&COMPLETE)
    }
}
#[derive(Default)]
struct AwaitInit;
impl Step for AwaitInit {
    type Input = usize;
    fn step_type(&self) -> &'static str {
        "InitStep"
    }
    fn execute(&self, context: &mut Context, count: usize) -> HandlerResult<StepDecision> {
        let branches = (0..count)
            .map(|index| {
                Box::new(move |round: &JoinRound| {
                    StepMovement::to(
                        &AwaitWork,
                        WorkItem {
                            index,
                            round: round.clone(),
                        },
                    )
                }) as Box<dyn FnOnce(&JoinRound) -> StepMovement>
            })
            .collect();
        let movements =
            COMPLETE.fork(context, branches, |round| StepMovement::to(&Await, round))?;
        Ok(StepDecision::go_to_many(movements))
    }
}
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct WorkItem {
    pub index: usize,
    pub round: JoinRound,
}
#[derive(Default)]
struct AwaitWork;
impl Step for AwaitWork {
    type Input = WorkItem;
    fn step_type(&self) -> &'static str {
        "DoWork"
    }
    fn execute(&self, context: &mut Context, item: WorkItem) -> HandlerResult<StepDecision> {
        thread::sleep(Duration::from_millis(fastrand::u64(50..500)));
        COMPLETE.end_branch(context, &item.round, item.index)
    }
}
#[derive(Default)]
struct Await;
impl Step for Await {
    type Input = JoinRound;
    fn step_type(&self) -> &'static str {
        "Await"
    }
    fn wait_for(&self, _: &mut Context, round: JoinRound) -> HandlerResult<Wait> {
        Ok(Wait::until(COMPLETE.for_all_branches(&round)))
    }
    fn execute(&self, context: &mut Context, round: JoinRound) -> HandlerResult<StepDecision> {
        let results = COMPLETE.branch_results(context, &round)?;
        Ok(StepDecision::graceful_complete(results.len()))
    }
}
static COMPLETE: LazyLock<Join<usize>> = LazyLock::new(|| Join::new("parallel-complete"));

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
        "DoWork"
    }
    fn execute(&self, _: &mut Context, input: usize) -> HandlerResult<StepDecision> {
        thread::sleep(Duration::from_millis(fastrand::u64(50..500)));
        Ok(StepDecision::graceful_complete(input).cancel_sibling_step(&FirstWinWork))
    }
}
