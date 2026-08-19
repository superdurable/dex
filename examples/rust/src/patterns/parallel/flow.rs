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

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, StepMovement, Wait,
};

#[derive(Default)]
pub struct SimpleParallelStatesFlow {
    fork: Fork,
    branch_one: BranchOne,
    branch_two: BranchTwo,
}

impl Flow for SimpleParallelStatesFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.fork)
            .and(&self.branch_one)
            .and(&self.branch_two)
    }
}

#[derive(Default)]
struct Fork;

impl Step for Fork {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&BranchOne, input.clone()),
            StepMovement::to(&BranchTwo, input),
        ]))
    }
}

#[derive(Default)]
struct BranchOne;

impl Step for BranchOne {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!("one:{input}")))
    }
}

#[derive(Default)]
struct BranchTwo;

impl Step for BranchTwo {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!("two:{input}")))
    }
}

pub const PARALLEL_RELEASE_ONE: Rpc<(), ()> = Rpc::new("ParallelReleaseOne");
pub const PARALLEL_RELEASE_TWO: Rpc<(), ()> = Rpc::new("ParallelReleaseTwo");

#[derive(Default)]
pub struct ParallelStatesWithAwaitFlow {
    fork: AwaitFork,
    branch_one: AwaitBranchOne,
    branch_two: AwaitBranchTwo,
}

impl ParallelStatesWithAwaitFlow {
    fn release_one(&self, context: &mut Context) -> HandlerResult<()> {
        release_one().publish(context, ())
    }

    fn release_two(&self, context: &mut Context) -> HandlerResult<()> {
        release_two().publish(context, ())
    }
}

impl Flow for ParallelStatesWithAwaitFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.fork)
            .and(&self.branch_one)
            .and(&self.branch_two)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&release_one())
            .channel(&release_two())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(PARALLEL_RELEASE_ONE, Self::release_one)
            .procedure_without_input(PARALLEL_RELEASE_TWO, Self::release_two)
    }
}

#[derive(Default)]
struct AwaitFork;

impl Step for AwaitFork {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&AwaitBranchOne, ()),
            StepMovement::to(&AwaitBranchTwo, ()),
        ]))
    }
}

#[derive(Default)]
struct AwaitBranchOne;

impl Step for AwaitBranchOne {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(release_one().for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("one".to_string()))
    }
}

#[derive(Default)]
struct AwaitBranchTwo;

impl Step for AwaitBranchTwo {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(release_two().for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("two".to_string()))
    }
}

fn release_one() -> Channel<()> {
    Channel::new("parallel-release-one")
}

fn release_two() -> Channel<()> {
    Channel::new("parallel-release-two")
}
