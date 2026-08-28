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

use std::time::Duration;

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, StepMovement, Timer, Wait,
};

pub const POLLING_COMPLETE_TASK: Rpc<String, ()> = Rpc::new("PollingCompleteTask");

#[derive(Default)]
pub struct PollingFlow {
    start: Start,
    wait_for_task_a: WaitForTaskA,
    wait_for_task_b: WaitForTaskB,
    poll_task_c: PollTaskC,
}

impl PollingFlow {
    fn complete_task(&self, context: &mut Context, task: String) -> HandlerResult<()> {
        match task.as_str() {
            "a" => TASK_A.publish(context, ()),
            "b" => TASK_B.publish(context, ()),
            _ => Err(dex_sdk::HandlerError::new("Polling", "task must be a or b")),
        }
    }
}

impl Flow for PollingFlow {
    type StartInput = u32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.wait_for_task_a)
            .and(&self.wait_for_task_b)
            .and(&self.poll_task_c)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&TASK_A).channel(&TASK_B)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(POLLING_COMPLETE_TASK, Self::complete_task)
    }
}

#[derive(Default)]
struct Start;

impl Step for Start {
    type Input = u32;

    fn execute(
        &self,
        _context: &mut Context,
        threshold: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&WaitForTaskA, ()),
            StepMovement::to(&WaitForTaskB, ()),
            StepMovement::to(&PollTaskC, (threshold.max(1), 0)),
        ]))
    }
}

#[derive(Default)]
struct WaitForTaskA;

impl Step for WaitForTaskA {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(TASK_A.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("task-a".to_string()))
    }
}

#[derive(Default)]
struct WaitForTaskB;

impl Step for WaitForTaskB {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(TASK_B.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("task-b".to_string()))
    }
}

#[derive(Default)]
struct PollTaskC;

impl Step for PollTaskC {
    type Input = (u32, u32);

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(5))))
    }

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let (threshold, attempts) = input;
        if attempts + 1 >= threshold {
            Ok(StepDecision::graceful_complete("task-c".to_string()))
        } else {
            Ok(StepDecision::go_to(&PollTaskC, (threshold, attempts + 1)))
        }
    }
}

static TASK_A: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("polling-task-a-completed"));

static TASK_B: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("polling-task-b-completed"));
