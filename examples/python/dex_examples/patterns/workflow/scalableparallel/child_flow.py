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

import random
from datetime import timedelta
from typing import TYPE_CHECKING, Callable

from dex import (
    AsyncClient,
    Attribute,
    Context,
    FlowNotActiveError,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    graceful_complete,
)

if TYPE_CHECKING:
    from dex_examples.patterns.workflow.scalableparallel.parent_flow import ParentFlow


class Processing(Step[str]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        parent_flow_provider: Callable[[], ParentFlow],
        parent_workflow_id: Attribute[str],
    ) -> None:
        self.client_provider = client_provider
        self.parent_flow_provider = parent_flow_provider
        self.parent_workflow_id = parent_workflow_id

    def wait_for(self, context: Context, input: str) -> Wait:
        del context, input
        return Wait.until(Timer.by_duration(timedelta(seconds=random.randint(1, 3))))

    async def execute(  # type: ignore[override]
        self, context: Context, input: str
    ) -> StepDecision:
        del input
        try:
            parent_id = self.parent_workflow_id.get(context)
        except Exception:
            # ParentFlowV2 starts children without seeding ParentWorkflowId.
            parent_id = ""
        if parent_id:
            try:
                await self.client_provider().invoke_rpc(
                    self.parent_flow_provider().complete_child_workflow,
                    parent_id,
                    context.flow_id,
                )
            except FlowNotActiveError:
                print(
                    "Parent workflow may have completed, might be duplicate "
                    "completion request, ignore it."
                )
        return graceful_complete()


class ChildFlow(Flow[str]):
    PARENT_WORKFLOW_ID = "ParentWorkflowId"

    parent_workflow_id = Attribute(PARENT_WORKFLOW_ID, str)

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        parent_flow_provider: Callable[[], ParentFlow],
    ) -> None:
        self.processing = Processing(
            client_provider,
            parent_flow_provider,
            self.parent_workflow_id,
        )

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.processing)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.parent_workflow_id)
