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

"""ParentFlowV2 demonstrates starting and waiting for child flows."""

from __future__ import annotations

from datetime import timedelta
from typing import Any, Callable, cast

from dex import (
    AsyncClient,
    Channel,
    Context,
    DexException,
    ErrorSubStatus,
    Flow,
    LongPollTimeoutError,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    go_to,
    go_to_multi,
)

from dex_examples.config import start_options
from dex_examples.patterns.workflow.parentchild.wait_for_child_input import (
    WaitForChildInput,
)
from dex_examples.patterns.workflow.scalableparallel.child_flow import ChildFlow

CONCURRENCY_PER_PARENT_WORKFLOW = 3
MAX_WAIT_SECONDS = 10


class AwaitChildWorkflowCompletion(Step[WaitForChildInput]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        loop_for_next_task_provider: Callable[[], LoopForNextTask],
    ) -> None:
        self.client_provider = client_provider
        self.loop_for_next_task_provider = loop_for_next_task_provider

    def wait_for(self, context: Context, input: WaitForChildInput) -> Wait:
        del context
        return Wait.until(Timer.by_duration(timedelta(seconds=input.timer_seconds)))

    async def execute(  # type: ignore[override]
        self, context: Context, input: WaitForChildInput
    ) -> StepDecision:
        del context
        try:
            await self.client_provider().wait_for_flow(
                input.child_wf_id,
                cast(type[Any], type(None)),
                timedelta(seconds=max(input.timer_seconds, 1)),
            )
        except LongPollTimeoutError:
            return go_to(
                self,
                WaitForChildInput(
                    input.child_wf_id,
                    min(input.timer_seconds * 2, MAX_WAIT_SECONDS),
                ),
            )
        return go_to(self.loop_for_next_task_provider(), None)


class StartChildWorkflow(Step[int]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        child_flow: ChildFlow,
        await_child_workflow_completion: AwaitChildWorkflowCompletion,
    ) -> None:
        self.client_provider = client_provider
        self.child_flow = child_flow
        self.await_child_workflow_completion = await_child_workflow_completion

    async def execute(  # type: ignore[override]
        self, context: Context, input: int
    ) -> StepDecision:
        del context
        child_workflow_id = f"child-wf-{input}"
        try:
            await self.client_provider().start_flow(
                self.child_flow,
                child_workflow_id,
                str(input),
                start_options(),
            )
        except DexException as error:
            if error.sub_status is not ErrorSubStatus.FLOW_ALREADY_STARTED:
                raise
            print("ignore this error because it is already started")
        return go_to(
            self.await_child_workflow_completion,
            WaitForChildInput(child_workflow_id, 1),
        )


class LoopForNextTask(Step[None]):
    def __init__(
        self,
        start_child_workflow: StartChildWorkflow,
        task_queue: Channel[int],
    ) -> None:
        self.start_child_workflow = start_child_workflow
        self.task_queue = task_queue

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.until(self.task_queue.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        request = self.task_queue.results(context)[0]
        return go_to(self.start_child_workflow, request)


class Init(Step[int]):
    def __init__(
        self,
        loop_for_next_task: LoopForNextTask,
        task_queue: Channel[int],
    ) -> None:
        self.loop_for_next_task = loop_for_next_task
        self.task_queue = task_queue

    def execute(self, context: Context, input: int) -> StepDecision:
        for index in range(input):
            self.task_queue.publish(context, index)

        return go_to_multi(
            *(
                StepMovement.of(self.loop_for_next_task, None)
                for _ in range(CONCURRENCY_PER_PARENT_WORKFLOW)
            )
        )


class ParentFlowV2(Flow[int]):
    TASK_QUEUE = "task_queue"

    task_queue = Channel(TASK_QUEUE, int)

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        child_flow: ChildFlow,
    ) -> None:
        self.await_child_workflow_completion = AwaitChildWorkflowCompletion(
            client_provider,
            lambda: self.loop_for_next_task,
        )
        self.start_child_workflow = StartChildWorkflow(
            client_provider,
            child_flow,
            self.await_child_workflow_completion,
        )
        self.loop_for_next_task = LoopForNextTask(
            self.start_child_workflow,
            self.task_queue,
        )
        self.init = Init(self.loop_for_next_task, self.task_queue)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.init).other_steps(
            self.loop_for_next_task,
            self.start_child_workflow,
            self.await_child_workflow_completion,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.task_queue)
