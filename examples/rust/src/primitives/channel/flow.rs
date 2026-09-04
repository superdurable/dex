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

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const CHANNEL_APPROVE: Rpc<(), ()> = Rpc::new("ChannelApprove");
pub const CHANNEL_MOVE: Rpc<MoveMessage, ()> = Rpc::new("ChannelMove");

static APPROVAL: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("Approval"));
pub static QUEUED: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("Queued"));
pub static MOVED: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("Moved"));

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct MoveMessage {
    pub message_id: String,
}

#[derive(Default)]
pub struct ChannelFlow {
    wait: ChannelWait,
}

impl ChannelFlow {
    fn approve(&self, context: &mut Context) -> HandlerResult<()> {
        APPROVAL.publish(context, "approved".to_string())
    }

    fn move_message(&self, context: &mut Context, message: MoveMessage) -> HandlerResult<()> {
        let message_to_move = QUEUED.find_pending_message(context, &message.message_id)?;
        QUEUED.delete(context, &message.message_id)?;
        if let Some(message_to_move) = message_to_move {
            MOVED.publish(context, message_to_move.value)?;
        }
        Ok(())
    }
}

impl Flow for ChannelFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.wait)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&APPROVAL)
            .channel(&QUEUED)
            .channel(&MOVED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(CHANNEL_APPROVE, Self::approve)
            .procedure(
                CHANNEL_MOVE.is_transactional().load_channel(&QUEUED),
                Self::move_message,
            )
    }
}

#[derive(Default)]
struct ChannelWait;

impl Step for ChannelWait {
    type Input = i32;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_load_channel(&QUEUED)
    }

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            APPROVAL.for_one(),
            Timer::by_duration(Duration::from_secs(input.max(0) as u64)),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let pending = QUEUED.pending_messages(context)?;
        if let Some(message) = pending.first() {
            QUEUED.delete(context, &message.message_id)?;
            return Ok(StepDecision::graceful_complete(message.value.clone()));
        }
        if context.has_any_timer_fired() {
            return Ok(StepDecision::graceful_complete(
                "approval timed out".to_owned(),
            ));
        }
        let approvals = APPROVAL.condition_results(context)?;
        Ok(StepDecision::graceful_complete(approvals[0].clone()))
    }
}
