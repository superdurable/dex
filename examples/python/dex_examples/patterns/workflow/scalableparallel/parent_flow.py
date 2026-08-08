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

"""See also: Scalable Parallelism Control (docs/design)."""

from __future__ import annotations

from datetime import timedelta
from typing import Callable

from dex import (
    AsyncClient,
    Attribute,
    Channel,
    ChannelMap,
    Context,
    DexException,
    ErrorSubStatus,
    Flow,
    IdReusePolicy,
    PersistenceSchema,
    RPCResult,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    force_complete_when_channels_empty,
    go_to,
    rpc,
)

from dex_examples.patterns.workflow.scalableparallel.child_flow import ChildFlow
from dex_examples.patterns.workflow.scalableparallel.models.batch_enqueue_request import (
    BatchEnqueueRequest,
)

NUM_PARENT_WORKFLOWS = 2
CONCURRENCY_PER_PARENT_WORKFLOW = 3
MAX_BUFFERED_TASKS = 10


class LoopForNextMessage(Step[None]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        child_flow: ChildFlow,
        task_queue: Channel[str],
        child_complete: ChannelMap[None],
        current_wait_child_wfs: Attribute[list[str]],
    ) -> None:
        self.client_provider = client_provider
        self.child_flow = child_flow
        self.task_queue = task_queue
        self.child_complete = child_complete
        self.current_wait_child_wfs = current_wait_child_wfs

    def wait_for(self, context: Context, input: None) -> Wait:
        del input
        waiting = self.current_wait_child_wfs.get(context) or []

        conditions = [
            self.child_complete.for_one(child_wf_id) for child_wf_id in waiting
        ]
        if len(waiting) < CONCURRENCY_PER_PARENT_WORKFLOW:
            conditions.insert(0, self.task_queue.for_one())
        return Wait.any_of(*conditions)

    async def execute(  # type: ignore[override]
        self, context: Context, input: None
    ) -> StepDecision:
        del input
        new_wait_list = list(self.current_wait_child_wfs.get(context) or [])

        task_results = self.task_queue.results(context)
        if task_results:
            request = task_results[0]
            child_workflow_id = f"processing-{request}"
            try:
                await self.client_provider().start_flow(
                    self.child_flow,
                    child_workflow_id,
                    request,
                    StartFlowOptions(
                        timeout=timedelta(hours=1),
                        ignore_already_started=True,
                        request_id=context.step_execution_id,
                        id_reuse_policy=IdReusePolicy.DISALLOW,
                    ).with_attribute(ChildFlow.parent_workflow_id, context.flow_id),
                )
                new_wait_list.append(child_workflow_id)
            except DexException as error:
                if error.sub_status is not ErrorSubStatus.FLOW_ALREADY_STARTED:
                    raise
                print(
                    "already started by other state/workflow, ignore it "
                    "-- not waiting for it"
                )

        for child_wf_id in list(new_wait_list):
            if self.child_complete.results(context, child_wf_id):
                new_wait_list.remove(child_wf_id)

        self.current_wait_child_wfs.set(context, new_wait_list)

        if not new_wait_list:
            return force_complete_when_channels_empty(
                None,
                StepMovement.of(self, None),
                self.task_queue,
            )
        return go_to(self, None)


class Init(Step[BatchEnqueueRequest]):
    def __init__(
        self,
        loop_for_next_message: LoopForNextMessage,
        task_queue: Channel[str],
    ) -> None:
        self.loop_for_next_message = loop_for_next_message
        self.task_queue = task_queue

    def execute(self, context: Context, input: BatchEnqueueRequest) -> StepDecision:
        for uuid in input.items:
            self.task_queue.publish(context, uuid)
        return go_to(self.loop_for_next_message, None)


class ParentFlow(Flow[BatchEnqueueRequest]):
    TASK_QUEUE = "TaskQueue"
    CHILD_COMPLETE_CHANNEL_PREFIX = "ChildComplete_"
    DA_CURRENT_WAIT_CHILD_WFS = "CurrentWaitChildWfs"

    task_queue = Channel(TASK_QUEUE, str)
    child_complete = ChannelMap[None](CHILD_COMPLETE_CHANNEL_PREFIX, type(None))
    current_wait_child_wfs = Attribute(DA_CURRENT_WAIT_CHILD_WFS, list[str])

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        child_flow: ChildFlow,
    ) -> None:
        self.loop_for_next_message = LoopForNextMessage(
            client_provider,
            child_flow,
            self.task_queue,
            self.child_complete,
            self.current_wait_child_wfs,
        )
        self.init = Init(self.loop_for_next_message, self.task_queue)

    def get_steps(self) -> StepList[BatchEnqueueRequest]:
        return StepList.start_step(self.init).other_steps(self.loop_for_next_message)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.task_queue,
            self.child_complete,
            self.current_wait_child_wfs,
        )

    @rpc
    def enqueue(
        self,
        context: Context,
        input: BatchEnqueueRequest,
    ) -> RPCResult[bool]:
        if self.task_queue.size(context) + len(input.items) > MAX_BUFFERED_TASKS:
            return RPCResult(False)
        for uuid in input.items:
            self.task_queue.publish(context, uuid)
        return RPCResult(True)

    @rpc
    def complete_child_workflow(self, context: Context, input: str) -> None:
        self.child_complete.publish(context, input, None)
