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

pub const RPC_TRIGGER: Rpc<String, String> = Rpc::new("RpcTrigger");

fn internal() -> Channel<()> {
    Channel::new("rpc-internal")
}

fn data() -> Attribute<String> {
    Attribute::new("rpc-data")
}

#[derive(Default)]
pub struct RpcFlow {
    wait: RpcWait,
    complete: RpcComplete,
}

impl RpcFlow {
    fn trigger(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<String>> {
        data().set(context, input.clone())?;
        internal().publish(context, ())?;
        Ok(RpcResult::new(input))
    }
}

impl Flow for RpcFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.wait).and(&self.complete)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&data())
            .channel(&internal())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(RPC_TRIGGER, Self::trigger)
    }
}

#[derive(Default)]
struct RpcWait;

impl Step for RpcWait {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(internal().for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&RpcComplete, 0))
    }
}

#[derive(Default)]
struct RpcComplete;

impl Step for RpcComplete {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}
