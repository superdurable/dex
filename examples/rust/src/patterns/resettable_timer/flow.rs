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
    StepList, Timer, Wait,
};

pub const RESETTABLE_TIMER_RESET: Rpc<(), ()> = Rpc::new("ResettableTimerReset");

#[derive(Default)]
pub struct ResettableTimerFlow {
    wait: WaitForInactivity,
}

impl ResettableTimerFlow {
    fn reset(&self, context: &mut Context) -> HandlerResult<()> {
        RESETS.publish(context, ())
    }
}

impl Flow for ResettableTimerFlow {
    type StartInput = u64;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.wait)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&RESETS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(RESETTABLE_TIMER_RESET, Self::reset)
    }
}

#[derive(Default)]
struct WaitForInactivity;

impl Step for WaitForInactivity {
    type Input = u64;

    fn wait_for(&self, _context: &mut Context, seconds: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            RESETS.for_one(),
            Timer::by_duration(Duration::from_secs(seconds.max(1))).with_id("inactivity"),
        ]))
    }

    fn execute(&self, context: &mut Context, seconds: Self::Input) -> HandlerResult<StepDecision> {
        if RESETS.condition_results(context)?.is_empty() {
            Ok(StepDecision::graceful_complete("timer-fired".to_string()))
        } else {
            Ok(StepDecision::go_to(&WaitForInactivity, seconds))
        }
    }
}

static RESETS: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("resettable-timer-resets"));
