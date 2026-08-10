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
import uuid
from typing import Callable

from dex import (
    AsyncClient,
    Context,
    FlowNotActiveError,
    Flow,
    IdReusePolicy,
    PersistenceSchema,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
)

from dex_examples.config import DEFAULT_TIMEOUT
from dex_examples.patterns.workflow.scalableparallel.exceptions.enqueue_failed_error import (
    EnqueueFailedError,
)
from dex_examples.patterns.workflow.scalableparallel.models.batch_enqueue_request import (
    BatchEnqueueRequest,
)
from dex_examples.patterns.workflow.scalableparallel.parent_flow import (
    NUM_PARENT_WORKFLOWS,
    ParentFlow,
)


class Request(Step[int]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        parent_flow: ParentFlow,
    ) -> None:
        self.client_provider = client_provider
        self.parent_flow = parent_flow

    async def execute(  # type: ignore[override]
        self, context: Context, input: int
    ) -> StepDecision:
        batch = self._generate_tasks(input)
        # Scope parent IDs to this receiver run so completed parents from earlier
        # runs do not block enqueue / restart.
        parent_workflow_id = (
            f"{context.flow_id}-parent-{random.randint(1, NUM_PARENT_WORKFLOWS)}"
        )

        client = self.client_provider()
        try:
            if not await client.invoke_rpc(
                self.parent_flow.enqueue, parent_workflow_id, batch
            ):
                raise EnqueueFailedError("Enqueue failed, retry in next attempt")
        except FlowNotActiveError:
            await client.start_flow(
                self.parent_flow,
                parent_workflow_id,
                batch,
                StartFlowOptions(
                    timeout=DEFAULT_TIMEOUT,
                    id_reuse_policy=IdReusePolicy.ALLOW_IF_NOT_RUNNING,
                ),
            )

        return graceful_complete()

    @staticmethod
    def _generate_tasks(number_of_child_wfs: int) -> BatchEnqueueRequest:
        return BatchEnqueueRequest(
            [str(uuid.uuid4()) for _ in range(number_of_child_wfs)]
        )


class RequestReceiverFlow(Flow[int]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        parent_flow: ParentFlow,
    ) -> None:
        self.request = Request(client_provider, parent_flow)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.request)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
