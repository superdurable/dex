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

"""Minimal Client APIs Flow: indexed Attribute for search."""

from __future__ import annotations

from dex import (
    Attribute,
    AttributeIndex,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
)

KEYWORD_KEY = "CustomKeywordField"


class ClientApisStep(Step[str]):
    def __init__(self, flow: "ClientApisFlow") -> None:
        self.flow = flow

    def execute(self, context: Context, input: str) -> StepDecision:
        self.flow.keyword.set(context, input)
        return graceful_complete(input)


class ClientApisFlow(Flow[str]):
    def __init__(self) -> None:
        self.keyword = Attribute(
            KEYWORD_KEY,
            str,
            AttributeIndex(IndexType.KEYWORD),
        )
        self.index = ClientApisStep(self)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.index)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.keyword)
