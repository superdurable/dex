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

"""Parent/child demo using sync Client inside Step.execute."""

from __future__ import annotations

import random
from dataclasses import dataclass
from datetime import timedelta
from typing import Any, Callable, cast

from sync_examples.config import start_options

from dex import (
    Channel,
    Client,
    Context,
    Flow,
    FlowAlreadyStartedError,
    LongPollTimeoutError,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    go_to,
    go_to_many,
    graceful_complete,
)

CONCURRENCY_PER_PARENT_WORKFLOW = 3
MAX_WAIT_SECONDS = 10


@dataclass
class WaitForChildInput:
    child_wf_id: str
    timer_seconds: int


class ChildProcessing(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        return Wait.until(Timer.by_duration(timedelta(seconds=random.randint(1, 3))))

    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete()


class ChildFlow(Flow[str]):
    def __init__(self) -> None:
        self.processing = ChildProcessing()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.processing)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()


class AwaitChildWorkflowCompletion(Step[WaitForChildInput]):
    def __init__(
        self,
        client_provider: Callable[[], Client],
    ) -> None:
        self.client_provider = client_provider

    def wait_for(self, context: Context, input: WaitForChildInput) -> Wait:
        return Wait.until(Timer.by_duration(timedelta(seconds=input.timer_seconds)))

    def execute(self, context: Context, input: WaitForChildInput) -> StepDecision:
        try:
            self.client_provider().wait_for_flow(
                input.child_wf_id,
                timedelta(seconds=max(input.timer_seconds, 1)),
            )
        except LongPollTimeoutError:
            return go_to(
                AwaitChildWorkflowCompletion,
                WaitForChildInput(
                    input.child_wf_id,
                    min(input.timer_seconds * 2, MAX_WAIT_SECONDS),
                ),
            )
        return go_to(LoopForNextTask, None)


class StartChildWorkflow(Step[int]):
    def __init__(
        self,
        client_provider: Callable[[], Client],
        child_flow: ChildFlow,
        await_child_workflow_completion: AwaitChildWorkflowCompletion,
    ) -> None:
        self.client_provider = client_provider
        self.child_flow = child_flow
        self.await_child_workflow_completion = await_child_workflow_completion

    def execute(self, context: Context, input: int) -> StepDecision:
        child_workflow_id = f"{context.flow_id}-child-{input}"
        try:
            self.client_provider().start_flow(
                self.child_flow,
                child_workflow_id,
                str(input),
                start_options(),
            )
        except FlowAlreadyStartedError:
            pass
        return go_to(
            AwaitChildWorkflowCompletion,
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
        return Wait.until(self.task_queue.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        request = self.task_queue.results(context)[0]
        return go_to(StartChildWorkflow, request)


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

        return go_to_many(
            *(
                StepMovement.of(LoopForNextTask, None)
                for _ in range(CONCURRENCY_PER_PARENT_WORKFLOW)
            )
        )


class ParentFlowV2(Flow[int]):
    TASK_QUEUE = "sync_task_queue"

    task_queue = Channel(TASK_QUEUE, int)

    def __init__(
        self,
        client_provider: Callable[[], Client],
        child_flow: ChildFlow,
    ) -> None:
        self.await_child_workflow_completion = AwaitChildWorkflowCompletion(
            client_provider,
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
