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

pub const DRAIN_SIGNAL_PUBLISH: Rpc<String, ()> = Rpc::new("DrainSignalPublish");

#[derive(Default)]
pub struct DrainSignalChannelsFlow {
    drain: DrainSignal,
}

impl DrainSignalChannelsFlow {
    fn publish(&self, context: &mut Context, input: String) -> HandlerResult<()> {
        signal_queue().publish(context, input)
    }
}

impl Flow for DrainSignalChannelsFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.drain)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&signal_queue())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(DRAIN_SIGNAL_PUBLISH, Self::publish)
    }
}

#[derive(Default)]
struct DrainSignal;

impl Step for DrainSignal {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(signal_queue().for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let item = signal_queue()
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        context.record_event("drained-signal", item)?;
        Ok(StepDecision::force_complete_if_channels_empty(
            (),
            StepMovement::to(&DrainSignal, ()),
            [signal_queue().when_empty()],
        ))
    }
}

fn internal_queue() -> Channel<String> {
    Channel::new("drain-internal-queue")
}

fn signal_queue() -> Channel<String> {
    Channel::new("drain-signal-queue")
}
