# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the applicable language governing permissions and
# limitations under the License.

"""Minimal Channel Flow: publish on an RPC and wait in a Step."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)


@dataclass(frozen=True)
class QueuedMessageReference:
    message_id: str


@dataclass(frozen=True)
class PendingMessage:
    message_id: str
    value: str


class ChannelWaitStep(Step[int]):
    def __init__(
        self,
        approval_messages: Channel[str],
        queued_messages: Channel[str],
    ) -> None:
        self.approval_messages = approval_messages
        self.queued_messages = queued_messages

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_load_channels=(self.queued_messages,))

    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.any_of(
            self.approval_messages.for_one(),
            Timer.by_duration(timedelta(seconds=input)),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        pending_queued_messages = self.queued_messages.pending_messages(context)
        if pending_queued_messages:
            self.queued_messages.delete(context, pending_queued_messages[0].message_id)
            return graceful_complete(pending_queued_messages[0].value)
        if context.has_timer_fired():
            return graceful_complete("approval timed out")
        approval_message_values = self.approval_messages.results(context)
        return graceful_complete(approval_message_values[0])


class ChannelFlow(Flow[int]):
    approval_messages = Channel("ApprovalMessages", str)
    queued_messages = Channel("QueuedMessages", str)
    prioritized_messages = Channel("PrioritizedMessages", str)

    def __init__(self) -> None:
        self.wait_for_approval = ChannelWaitStep(
            self.approval_messages, self.queued_messages
        )

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.wait_for_approval)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.approval_messages,
            self.queued_messages,
            self.prioritized_messages,
        )

    @rpc
    def publish_approval_message(self, context: Context) -> None:
        self.approval_messages.publish(context, "approved")

    @rpc
    def enqueue_channel_message(self, context: Context, input: str) -> None:
        self.queued_messages.publish(context, input)

    @rpc(load_channels=(queued_messages,))
    def get_queued_messages(
        self, context: Context
    ) -> RPCResult[list[PendingMessage]]:
        return RPCResult(
            [PendingMessage(message.message_id, message.value)
             for message in self.queued_messages.pending_messages(context)]
        )

    @rpc(is_transactional=True, load_channels=(queued_messages,))
    def delete_queued_message(
        self, context: Context, queued_message: QueuedMessageReference
    ) -> None:
        self.queued_messages.delete(context, queued_message.message_id)

    @rpc(load_channels=(prioritized_messages,))
    def get_prioritized_messages(
        self, context: Context
    ) -> RPCResult[list[PendingMessage]]:
        return RPCResult(
            [PendingMessage(message.message_id, message.value)
             for message in self.prioritized_messages.pending_messages(context)]
        )

    @rpc(is_transactional=True, load_channels=(queued_messages,))
    def move_queued_message_to_prioritized_messages(
        self, context: Context, queued_message: QueuedMessageReference
    ) -> None:
        message_to_prioritize = self.queued_messages.find_pending_message(
            context, queued_message.message_id
        )
        self.queued_messages.delete(context, queued_message.message_id)
        if message_to_prioritize is not None:
            self.prioritized_messages.publish(context, message_to_prioritize.value)
