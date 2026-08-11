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
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    force_complete_if_channels_empty,
)

DRAIN_WINDOW_SECONDS = 20


class ProcessSignal(Step[str]):
    def __init__(self, queue_signal_channel: Channel[str]) -> None:
        self.queue_signal_channel = queue_signal_channel

    def wait_for(self, context: Context, input: str) -> Wait:
        del context
        if input is None:
            return Wait.until(self.queue_signal_channel.for_one())
        return Wait.skip_immediately()

    async def execute(  # type: ignore[override]
        self, context: Context, input: str
    ) -> StepDecision:
        if input is not None:
            print(f"DrainSignalChannelsFlow process signal value: {input}")
        else:
            values = self.queue_signal_channel.results(context)
            if not values:
                raise RuntimeError("No signal request found")
            value = values[0]
            if value is None:
                raise RuntimeError("No signal value found")
            print(f"DrainSignalChannelsFlow process signal value: {value}")

        # Yield so AsyncWorker can serve other Flows during the drain window.
        await asyncio.sleep(DRAIN_WINDOW_SECONDS)

        return force_complete_if_channels_empty(
            None,
            StepMovement.of(self, None),
            self.queue_signal_channel,
        )


class DrainSignalChannelsFlow(Flow[str]):
    QUEUE_SIGNAL_CHANNEL = "queueSignalChannel"

    queue_signal_channel = Channel(QUEUE_SIGNAL_CHANNEL, str)

    def __init__(self) -> None:
        self.process_signal = ProcessSignal(self.queue_signal_channel)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.process_signal)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.queue_signal_channel)
