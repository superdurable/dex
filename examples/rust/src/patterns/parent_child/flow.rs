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
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step,
    StepDecision, StepList, Wait,
};

pub const PARENT_V2_CHILD_COMPLETED: Rpc<String, ()> = Rpc::new("ParentV2ChildCompleted");

#[derive(Default)]
pub struct ParentFlowV2 {
    record_child: RecordChild,
    await_child: AwaitChild,
}

impl ParentFlowV2 {
    fn child_completed(&self, context: &mut Context, output: String) -> HandlerResult<()> {
        child_output().publish(context, output)
    }
}

impl Flow for ParentFlowV2 {
    type StartInput = String;

    fn flow_type(&self) -> &'static str {
        "ParentFlowV2"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.record_child).and(&self.await_child)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&child_id())
            .channel(&child_output())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(PARENT_V2_CHILD_COMPLETED, Self::child_completed)
    }
}

#[derive(Default)]
struct RecordChild;

impl Step for RecordChild {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        child_id().set(context, input)?;
        Ok(StepDecision::go_to(&AwaitChild, ()))
    }
}

#[derive(Default)]
struct AwaitChild;

impl Step for AwaitChild {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(child_output().for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let output = child_output()
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        Ok(StepDecision::graceful_complete(output))
    }
}

fn child_id() -> Attribute<String> {
    Attribute::new("parent-v2-child-id")
}

fn child_output() -> Channel<String> {
    Channel::new("parent-v2-child-output")
}
