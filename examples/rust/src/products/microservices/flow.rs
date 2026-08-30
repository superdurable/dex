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

use std::{sync::LazyLock, time::Duration};

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, StepMovement, Timer, Wait,
};

pub const ORCHESTRATION_SWAP: Rpc<String, String> = Rpc::new("OrchestrationSwap");
pub const ORCHESTRATION_READY: Rpc<(), ()> = Rpc::new("OrchestrationReady");
pub static DATA: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("orchestration-data"));
pub static READY: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("orchestration-ready"));

#[derive(Default)]
pub struct OrchestrationFlow {
    call_api_one: CallApiOne,
    call_api_two: CallApiTwo,
    call_api_three: CallApiThree,
    call_api_four: CallApiFour,
}

impl OrchestrationFlow {
    fn swap(&self, context: &mut Context, replacement: String) -> HandlerResult<RpcResult<String>> {
        let previous = DATA.get(context)?.unwrap_or_default();
        DATA.set(context, replacement)?;
        Ok(RpcResult::new(previous))
    }

    fn ready(&self, context: &mut Context) -> HandlerResult<()> {
        READY.publish(context, ())
    }
}

impl Flow for OrchestrationFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.call_api_one)
            .and(&self.call_api_two)
            .and(&self.call_api_three)
            .and(&self.call_api_four)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&DATA).channel(&READY)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(ORCHESTRATION_SWAP, Self::swap)
            .procedure_without_input(ORCHESTRATION_READY, Self::ready)
    }
}

#[derive(Default)]
struct CallApiOne;

impl Step for CallApiOne {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("api-one", input.clone())?;
        DATA.set(context, input)?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&CallApiTwo, ()),
            StepMovement::to(&CallApiThree, ()),
        ]))
    }
}

#[derive(Default)]
struct CallApiTwo;

impl Step for CallApiTwo {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("api-two", DATA.get(context)?.unwrap_or_default())?;
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CallApiThree;

impl Step for CallApiThree {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            READY.for_one(),
            Timer::by_duration(Duration::from_secs(24 * 60 * 60)),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let data = DATA.get(context)?.unwrap_or_default();
        context.record_event("api-three", data.clone())?;
        if context.has_timer_fired(0) {
            return Ok(StepDecision::go_to(&CallApiFour, ()));
        }
        Ok(StepDecision::graceful_complete(data))
    }
}

#[derive(Default)]
struct CallApiFour;

impl Step for CallApiFour {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let data = DATA.get(context)?.unwrap_or_default();
        context.record_event("api-four", data.clone())?;
        Ok(StepDecision::graceful_complete(data))
    }
}
