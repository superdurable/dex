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
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, Wait,
};

#[derive(Default)]
pub struct ChildFlow {
    process: ProcessChild,
}

impl Flow for ChildFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.process)
    }
}

#[derive(Default)]
struct ProcessChild;

impl Step for ProcessChild {
    type Input = String;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("scalable-child-task", input.clone())?;
        Ok(StepDecision::graceful_complete(input))
    }
}

pub const SCALABLE_PARENT_ENQUEUE: Rpc<Vec<String>, usize> = Rpc::new("ScalableParentEnqueue");

#[derive(Default)]
pub struct ParentFlow {
    drain: DrainParentQueue,
}

impl ParentFlow {
    fn enqueue(
        &self,
        context: &mut Context,
        tasks: Vec<String>,
    ) -> HandlerResult<RpcResult<usize>> {
        let capacity = capacity().get(context)?.unwrap_or(100);
        let available = capacity.saturating_sub(parent_queue().size(context)?);
        if tasks.len() > available {
            return Err(dex_sdk::HandlerError::new("parent task queue is full"));
        }
        for task in tasks {
            parent_queue().publish(context, task)?;
        }
        Ok(RpcResult::new(available))
    }
}

impl Flow for ParentFlow {
    type StartInput = usize;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.drain)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&capacity())
            .channel(&parent_queue())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(SCALABLE_PARENT_ENQUEUE, Self::enqueue)
    }
}

#[derive(Default)]
struct DrainParentQueue;

impl Step for DrainParentQueue {
    type Input = usize;

    fn wait_for(&self, context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        capacity().set(context, input.max(1))?;
        Ok(Wait::until(parent_queue().for_one()))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let task = parent_queue()
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        context.record_event("dispatch-child", task)?;
        Ok(StepDecision::go_to(&DrainParentQueue, input))
    }
}

pub const REQUEST_RECEIVER_SUBMIT: Rpc<Vec<String>, usize> = Rpc::new("RequestReceiverSubmit");

#[derive(Default)]
pub struct RequestReceiverFlow {
    forward: ForwardRequests,
}

impl RequestReceiverFlow {
    fn submit(&self, context: &mut Context, tasks: Vec<String>) -> HandlerResult<RpcResult<usize>> {
        for task in tasks {
            request_buffer().publish(context, task)?;
        }
        Ok(RpcResult::new(request_buffer().size(context)?))
    }
}

impl Flow for RequestReceiverFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.forward)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&request_buffer())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(REQUEST_RECEIVER_SUBMIT, Self::submit)
    }
}

#[derive(Default)]
struct ForwardRequests;

impl Step for ForwardRequests {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(request_buffer().for_one()))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        let request = request_buffer()
            .condition_results(context)?
            .into_iter()
            .next()
            .unwrap_or_default();
        context.record_event("forward-parent-request", request)?;
        Ok(StepDecision::go_to(&ForwardRequests, ()))
    }
}

fn capacity() -> Attribute<usize> {
    Attribute::new("scalable-parent-capacity")
}

fn parent_queue() -> Channel<String> {
    Channel::new("scalable-parent-queue")
}

fn request_buffer() -> Channel<String> {
    Channel::new("scalable-request-buffer")
}
