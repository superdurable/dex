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

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, StepMovement, Timer, Wait,
};

pub const ORCHESTRATION_SWAP: Rpc<String, String> = Rpc::new("OrchestrationSwap");
pub const ORCHESTRATION_READY: Rpc<(), ()> = Rpc::new("OrchestrationReady");

#[derive(Default)]
pub struct OrchestrationFlow {
    call_api_one: CallApiOne,
    call_api_two: CallApiTwo,
    call_api_three: CallApiThree,
}

impl OrchestrationFlow {
    fn swap(&self, context: &mut Context, replacement: String) -> HandlerResult<RpcResult<String>> {
        let data = Attribute::<String>::new("orchestration-data");
        let previous = data.get(context)?.unwrap_or_default();
        data.set(context, replacement)?;
        Ok(RpcResult::new(previous))
    }

    fn ready(&self, context: &mut Context) -> HandlerResult<()> {
        Channel::<()>::new("orchestration-ready").publish(context, ())
    }
}

impl Flow for OrchestrationFlow {
    type StartInput = String;

    fn flow_type(&self) -> &'static str {
        "OrchestrationFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.call_api_one)
            .and(&self.call_api_two)
            .and(&self.call_api_three)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&Attribute::<String>::new("orchestration-data"))
            .channel(&Channel::<()>::new("orchestration-ready"))
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
        Attribute::<String>::new("orchestration-data").set(context, input.clone())?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&CallApiTwo, input.clone()),
            StepMovement::to(&CallApiThree, input),
        ]))
    }
}

#[derive(Default)]
struct CallApiTwo;

impl Step for CallApiTwo {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("api-two", input)?;
        Ok(StepDecision::graceful_complete(()))
    }
}

#[derive(Default)]
struct CallApiThree;

impl Step for CallApiThree {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            Channel::<()>::new("orchestration-ready").for_one(),
            Timer::by_duration(Duration::from_secs(30)),
        ]))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("api-three", input)?;
        Ok(StepDecision::graceful_complete(()))
    }
}
