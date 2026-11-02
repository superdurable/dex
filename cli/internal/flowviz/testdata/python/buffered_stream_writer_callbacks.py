# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from dex import AsyncContext, Flow, PersistenceSchema, Step, StepDecision, StepList, Stream
from dex import force_complete


async def stream_model_reply(write_text, write_reasoning):
    write_reasoning("Planning the reply.")
    write_text("Here is the reply.")


class CallModel(Step[None]):
    def __init__(self, flow):
        self.flow = flow

    async def execute(self, context: AsyncContext, input: None) -> StepDecision:
        assistant_text = self.flow.assistant_text.buffered_text(context)
        reasoning_summary = self.flow.reasoning_summary.buffered_text(context)
        await stream_model_reply(assistant_text.write, reasoning_summary.write)
        return force_complete()


class ModelReplyFlow(Flow[None]):
    reasoning_summary = Stream("ReasoningSummary", str, 1024 * 1024)
    assistant_text = Stream("AssistantText", str, 1024 * 1024)

    def __init__(self):
        self.call_model = CallModel(self)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.call_model)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.reasoning_summary, self.assistant_text)
