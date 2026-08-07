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
    Client,
    Context,
    DexException,
    ErrorSubStatus,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
)

from dex_examples.config import start_options
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
        client_provider: Callable[[], Client],
        parent_flow: ParentFlow,
    ) -> None:
        self.client_provider = client_provider
        self.parent_flow = parent_flow

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        batch = self._generate_tasks(input)
        parent_workflow_id = f"parent_workflow_{random.randint(1, NUM_PARENT_WORKFLOWS)}"

        client = self.client_provider()
        try:
            if not client.invoke_rpc(self.parent_flow.enqueue, parent_workflow_id, batch):
                raise EnqueueFailedError("Enqueue failed, retry in next attempt")
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_NOT_EXISTS:
                raise
            client.start_flow(
                self.parent_flow,
                parent_workflow_id,
                batch,
                start_options(),
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
        client_provider: Callable[[], Client],
        parent_flow: ParentFlow,
    ) -> None:
        self.request = Request(client_provider, parent_flow)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.request)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
