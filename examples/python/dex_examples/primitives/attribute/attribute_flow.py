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

"""Minimal Attribute Flow: declare, write, and read a persisted Attribute."""

from __future__ import annotations

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    graceful_complete,
)


class AttributeStep(Step[str]):
    def __init__(self, flow: "AttributeFlow") -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: str) -> Wait:
        self.flow.message.set(context, input)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: str) -> StepDecision:
        del input
        return graceful_complete(self.flow.message.get(context))


class AttributeFlow(Flow[str]):
    message = Attribute("primitive-attribute-message", str)

    def __init__(self) -> None:
        self.start = AttributeStep(self)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.message)
