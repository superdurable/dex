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

from datetime import timedelta

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    dead_end,
    go_to,
    go_to_multi,
    graceful_complete,
)

from dex_examples.shared.my_dependency_service import MyDependencyService

TASK_A_COMPLETED = "task-a-completed"
TASK_B_COMPLETED = "task-b-completed"
TASK_C_COMPLETED = "task-c-completed"


class WaitForTasks(Step[None]):
    def __init__(
        self,
        task_a_completed: Channel[None],
        task_b_completed: Channel[None],
        task_c_completed: Channel[None],
    ) -> None:
        self.task_a_completed = task_a_completed
        self.task_b_completed = task_b_completed
        self.task_c_completed = task_c_completed

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.all_of(
            self.task_a_completed.for_one(),
            self.task_b_completed.for_one(),
            self.task_c_completed.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete("all tasks completed")


class Poll(Step[int]):
    def __init__(
        self,
        service: MyDependencyService,
        current_polls: Attribute[int],
        task_c_completed: Channel[None],
    ) -> None:
        self.service = service
        self.current_polls = current_polls
        self.task_c_completed = task_c_completed

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.until(Timer.by_duration(timedelta(seconds=1)))

    def execute(self, context: Context, input: int) -> StepDecision:
        self.service.call_api1("calling API1 for polling service C")
        polls = self.current_polls.get(context)
        if polls >= input:
            self.task_c_completed.publish(context, None)
            return dead_end()
        self.current_polls.set(context, polls + 1)
        return go_to(self, input)


class Initialize(Step[int]):
    def __init__(
        self,
        current_polls: Attribute[int],
        poll: Poll,
        wait_for_tasks: WaitForTasks,
    ) -> None:
        self.current_polls = current_polls
        self.poll = poll
        self.wait_for_tasks = wait_for_tasks

    def execute(self, context: Context, input: int) -> StepDecision:
        self.current_polls.set(context, 0)
        return go_to_multi(
            StepMovement.of(self.poll, input),
            StepMovement.of(self.wait_for_tasks, None),
        )


class PollingFlow(Flow[int]):
    current_polls = Attribute("current-polls", int)
    task_a_completed = Channel[None](TASK_A_COMPLETED, type(None))
    task_b_completed = Channel[None](TASK_B_COMPLETED, type(None))
    task_c_completed = Channel[None](TASK_C_COMPLETED, type(None))

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.poll = Poll(service, self.current_polls, self.task_c_completed)
        self.wait_for_tasks = WaitForTasks(
            self.task_a_completed,
            self.task_b_completed,
            self.task_c_completed,
        )
        self.initialize = Initialize(
            self.current_polls,
            self.poll,
            self.wait_for_tasks,
        )

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.initialize).other_steps(
            self.poll,
            self.wait_for_tasks,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.current_polls,
            self.task_a_completed,
            self.task_b_completed,
            self.task_c_completed,
        )
