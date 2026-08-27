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
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

import asyncio

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    force_complete_if_channels_empty,
    rpc,
)

DRAIN_WINDOW_SECONDS = 20


class ProcessMessage(Step[str]):
    def __init__(self, queue_channel: Channel[str]) -> None:
        self.queue_channel = queue_channel

    def wait_for(self, context: Context, input: str) -> Wait:
        del context
        if input is None:
            return Wait.until(self.queue_channel.for_one())
        return Wait.skip_immediately()

    async def execute(  # type: ignore[override]
        self, context: Context, input: str
    ) -> StepDecision:
        if input is not None:
            print(f"DrainingChannelFlow process message: {input}")
        else:
            values = self.queue_channel.results(context)
            if not values:
                raise RuntimeError("No channel message found")
            value = values[0]
            if value is None:
                raise RuntimeError("No channel message value found")
            print(f"DrainingChannelFlow process message: {value}")

        # Yield so AsyncWorker can serve other Flows during the drain window.
        await asyncio.sleep(DRAIN_WINDOW_SECONDS)

        return force_complete_if_channels_empty(
            None,
            StepMovement.of(ProcessMessage, None),
            self.queue_channel,
        )


class DrainingChannelFlow(Flow[str]):
    QUEUE_CHANNEL = "queueChannel"

    queue_channel = Channel(QUEUE_CHANNEL, str)

    def __init__(self) -> None:
        self.process_message = ProcessMessage(self.queue_channel)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.process_message)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.queue_channel)

    @rpc
    def example_rpc(self, context: Context, input: str) -> RPCResult[str]:
        self.queue_channel.publish(context, input)
        return RPCResult(input)
