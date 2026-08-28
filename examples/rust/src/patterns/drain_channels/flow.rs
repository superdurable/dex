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

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, Step,
    StepDecision, StepList, StepMovement, Wait,
};
use serde::{Deserialize, Serialize};

#[derive(Default)]
pub struct DrainInternalChannelFlow {
    init: Init,
    main: MainStep,
    side: SideStep,
    finalize: Finalize,
}

impl Flow for DrainInternalChannelFlow {
    type StartInput = Vec<String>;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.init)
            .and(&self.main)
            .and(&self.side)
            .and(&self.finalize)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&SIDE_STEP_DATA)
    }
}

#[derive(Default)]
struct Init;

impl Step for Init {
    type Input = Vec<String>;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&MainStep, input),
            StepMovement::to(&SideStep, ()),
        ]))
    }
}

#[derive(Default)]
struct MainStep;

impl Step for MainStep {
    type Input = Vec<String>;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        for value in input {
            SIDE_STEP_DATA.publish(context, SideStepData::Message(value))?;
        }
        Ok(StepDecision::go_to(&Finalize, ()))
    }
}

#[derive(Default)]
struct SideStep;

impl Step for SideStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(SIDE_STEP_DATA.for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let command = SIDE_STEP_DATA
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        match command {
            SideStepData::Message(value) => {
                context.record_event("drained-internal", value)?;
                Ok(StepDecision::go_to(&SideStep, ()))
            }
            SideStepData::Final => Ok(StepDecision::graceful_complete(())),
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
enum SideStepData {
    #[default]
    Final,
    Message(String),
}

#[derive(Default)]
struct Finalize;

impl Step for Finalize {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        SIDE_STEP_DATA.publish(context, SideStepData::Final)?;
        Ok(StepDecision::graceful_complete(()))
    }
}

pub const EXAMPLE_RPC: Rpc<String, String> = Rpc::new("exampleRPC");

#[derive(Default)]
pub struct DrainingExternalChannelFlow {
    drain: DrainChannel,
}

impl DrainingExternalChannelFlow {
    fn example_rpc(
        &self,
        context: &mut Context,
        input: String,
    ) -> HandlerResult<RpcResult<String>> {
        EXTERNAL_QUEUE.publish(context, input.clone())?;
        Ok(RpcResult::new(input))
    }
}

impl Flow for DrainingExternalChannelFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.drain)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&EXTERNAL_QUEUE)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(EXAMPLE_RPC, Self::example_rpc)
    }
}

#[derive(Default)]
struct DrainChannel;

impl Step for DrainChannel {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, input: String) -> HandlerResult<Wait> {
        if input.is_empty() {
            return Ok(Wait::until(EXTERNAL_QUEUE.for_one()));
        }
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        let item = if input.is_empty() {
            EXTERNAL_QUEUE
                .condition_results(context)?
                .into_iter()
                .next()
                .unwrap_or_default()
        } else {
            input
        };
        context.record_event("drained-channel", item)?;
        Ok(StepDecision::force_complete_if_channels_empty(
            String::new(),
            StepMovement::to(&DrainChannel, String::new()),
            [EXTERNAL_QUEUE.when_empty()],
        ))
    }
}

static SIDE_STEP_DATA: LazyLock<Channel<SideStepData>> =
    LazyLock::new(|| Channel::new("SideStepData"));

pub(crate) static EXTERNAL_QUEUE: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("drain-channel-queue"));
