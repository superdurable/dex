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
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, Step,
    StepDecision, StepList, StepMovement, Wait,
};

#[derive(Default)]
pub struct DrainInternalChannelsFlow {
    seed: Seed,
    drain: DrainInternal,
}

impl Flow for DrainInternalChannelsFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.seed).and(&self.drain)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&internal_queue())
    }
}

#[derive(Default)]
struct Seed;

impl Step for Seed {
    type Input = Vec<String>;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        for item in input {
            internal_queue().publish(context, item)?;
        }
        Ok(StepDecision::go_to(&DrainInternal, ()))
    }
}

#[derive(Default)]
struct DrainInternal;

impl Step for DrainInternal {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(internal_queue().for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let item = internal_queue()
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        context.record_event("drained-internal", item)?;
        Ok(StepDecision::force_complete_if_channels_empty(
            (),
            StepMovement::to(&DrainInternal, ()),
            [internal_queue().when_empty()],
        ))
    }
}

pub const EXAMPLE_RPC: Rpc<String, String> = Rpc::new("exampleRPC");

#[derive(Default)]
pub struct DrainingChannelFlow {
    drain: DrainChannel,
}

impl DrainingChannelFlow {
    fn example_rpc(
        &self,
        context: &mut Context,
        input: String,
    ) -> HandlerResult<RpcResult<String>> {
        channel_queue().publish(context, input.clone())?;
        Ok(RpcResult::new(input))
    }
}

impl Flow for DrainingChannelFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.drain)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&channel_queue())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(EXAMPLE_RPC, Self::example_rpc)
    }
}

#[derive(Default)]
struct DrainChannel;

impl Step for DrainChannel {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(channel_queue().for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let item = channel_queue()
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        context.record_event("drained-channel", item)?;
        Ok(StepDecision::force_complete_if_channels_empty(
            (),
            StepMovement::to(&DrainChannel, ()),
            [channel_queue().when_empty()],
        ))
    }
}

fn internal_queue() -> Channel<String> {
    Channel::new("drain-internal-queue")
}

fn channel_queue() -> Channel<String> {
    Channel::new("drain-channel-queue")
}
