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
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, Step,
    StepDecision, StepList, StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const PUBLISH_APPROVAL_MESSAGE: Rpc<(), ()> = Rpc::new("PublishApprovalMessage");
pub const ENQUEUE_CHANNEL_MESSAGE: Rpc<String, ()> = Rpc::new("EnqueueChannelMessage");
pub const GET_QUEUED_MESSAGES: Rpc<(), Vec<PendingMessage>> = Rpc::new("GetQueuedMessages");
pub const DELETE_QUEUED_MESSAGE: Rpc<QueuedMessageReference, ()> = Rpc::new("DeleteQueuedMessage");
pub const GET_PRIORITIZED_MESSAGES: Rpc<(), Vec<PendingMessage>> =
    Rpc::new("GetPrioritizedMessages");
pub const MOVE_QUEUED_MESSAGE_TO_PRIORITIZED_MESSAGES: Rpc<QueuedMessageReference, ()> =
    Rpc::new("MoveQueuedMessageToPrioritizedMessages");

static APPROVAL_MESSAGES: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("ApprovalMessages"));
pub static QUEUED_MESSAGES: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("QueuedMessages"));
pub static PRIORITIZED_MESSAGES: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("PrioritizedMessages"));

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct QueuedMessageReference {
    pub message_id: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct PendingMessage {
    #[serde(rename = "messageID")]
    pub message_id: String,
    pub value: String,
}

#[derive(Default)]
pub struct ChannelFlow {
    wait: ChannelWait,
}

impl ChannelFlow {
    fn publish_approval_message(&self, context: &mut Context) -> HandlerResult<()> {
        APPROVAL_MESSAGES.publish(context, "approved".to_string())
    }

    fn enqueue_channel_message(&self, context: &mut Context, value: String) -> HandlerResult<()> {
        QUEUED_MESSAGES.publish(context, value)
    }

    fn get_queued_messages(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<Vec<PendingMessage>>> {
        let messages = QUEUED_MESSAGES
            .pending_messages(context)?
            .into_iter()
            .map(|message| PendingMessage {
                message_id: message.message_id,
                value: message.value,
            })
            .collect();
        Ok(RpcResult::new(messages))
    }

    fn delete_queued_message(
        &self,
        context: &mut Context,
        queued_message: QueuedMessageReference,
    ) -> HandlerResult<()> {
        QUEUED_MESSAGES.delete(context, &queued_message.message_id)
    }

    fn get_prioritized_messages(
        &self,
        context: &mut Context,
    ) -> HandlerResult<RpcResult<Vec<PendingMessage>>> {
        let messages = PRIORITIZED_MESSAGES
            .pending_messages(context)?
            .into_iter()
            .map(|message| PendingMessage {
                message_id: message.message_id,
                value: message.value,
            })
            .collect();
        Ok(RpcResult::new(messages))
    }

    fn move_queued_message_to_prioritized_messages(
        &self,
        context: &mut Context,
        queued_message: QueuedMessageReference,
    ) -> HandlerResult<()> {
        let message_to_prioritize =
            QUEUED_MESSAGES.find_pending_message(context, &queued_message.message_id)?;
        QUEUED_MESSAGES.delete(context, &queued_message.message_id)?;
        if let Some(message_to_prioritize) = message_to_prioritize {
            PRIORITIZED_MESSAGES.publish(context, message_to_prioritize.value)?;
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
            .channel(&APPROVAL_MESSAGES)
            .channel(&QUEUED_MESSAGES)
            .channel(&PRIORITIZED_MESSAGES)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(PUBLISH_APPROVAL_MESSAGE, Self::publish_approval_message)
            .procedure(ENQUEUE_CHANNEL_MESSAGE, Self::enqueue_channel_message)
            .function_without_input(
                GET_QUEUED_MESSAGES.load_channel(&QUEUED_MESSAGES),
                Self::get_queued_messages,
            )
            .procedure(
                DELETE_QUEUED_MESSAGE
                    .is_transactional()
                    .load_channel(&QUEUED_MESSAGES),
                Self::delete_queued_message,
            )
            .function_without_input(
                GET_PRIORITIZED_MESSAGES.load_channel(&PRIORITIZED_MESSAGES),
                Self::get_prioritized_messages,
            )
            .procedure(
                MOVE_QUEUED_MESSAGE_TO_PRIORITIZED_MESSAGES
                    .is_transactional()
                    .load_channel(&QUEUED_MESSAGES),
                Self::move_queued_message_to_prioritized_messages,
            )
    }
}

#[derive(Default)]
struct ChannelWait;

impl Step for ChannelWait {
    type Input = i32;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_load_channel(&QUEUED_MESSAGES)
    }

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            APPROVAL_MESSAGES.for_one(),
            Timer::by_duration(Duration::from_secs(input.max(0) as u64)),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let pending_queued_messages = QUEUED_MESSAGES.pending_messages(context)?;
        if let Some(queued_message) = pending_queued_messages.first() {
            QUEUED_MESSAGES.delete(context, &queued_message.message_id)?;
            return Ok(StepDecision::graceful_complete(
                queued_message.value.clone(),
            ));
        }
        if context.has_any_timer_fired() {
            return Ok(StepDecision::graceful_complete(
                "approval timed out".to_owned(),
            ));
        }
        let approval_message_values = APPROVAL_MESSAGES.condition_results(context)?;
        Ok(StepDecision::graceful_complete(
            approval_message_values[0].clone(),
        ))
    }
}
