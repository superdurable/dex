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
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, Timer, Wait,
};

pub const CHANNEL_APPROVE: Rpc<(), ()> = Rpc::new("ChannelApprove");

fn approval() -> Channel<String> {
    Channel::new("Approval")
}

#[derive(Default)]
pub struct ChannelFlow {
    wait: ChannelWait,
}

impl ChannelFlow {
    fn approve(&self, context: &mut Context) -> HandlerResult<()> {
        approval().publish(context, "approved".to_string())
    }
}

impl Flow for ChannelFlow {
    type StartInput = i32;

    fn flow_type(&self) -> &'static str {
        "ChannelFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.wait)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&approval())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(CHANNEL_APPROVE, Self::approve)
    }
}

#[derive(Default)]
struct ChannelWait;

impl Step for ChannelWait {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            approval().for_one(),
            Timer::by_duration(Duration::from_secs(input.max(0) as u64)),
        ]))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let approvals = approval().condition_results(context)?;
        if let Some(value) = approvals.first() {
            return Ok(StepDecision::graceful_complete(value.clone()));
        }
        Ok(StepDecision::go_to(&ChannelWait, input))
    }
}
