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

from datetime import timedelta

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    go_to,
    go_to_multi,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.workflow.interruptible.work_job_parameters_input import (
    WorkJobParametersInput,
)

INTERRUPT_VALUE = "cancel"


class WorkAExecution(Step[WorkJobParametersInput]):
    def __init__(self, interrupt_signal: Attribute[str]) -> None:
        self.interrupt_signal = interrupt_signal

    def wait_for(self, context: Context, input: WorkJobParametersInput) -> Wait:
        del context, input
        return Wait.any_of(Timer.by_duration(timedelta(seconds=2)))

    def execute(
        self,
        context: Context,
        input: WorkJobParametersInput,
    ) -> StepDecision:
        if (self.interrupt_signal.get(context) or "") == INTERRUPT_VALUE:
            print("A: Interrupted!")
            return graceful_complete()

        if input.progress > input.job_upper_bound:
            print("Executing WorkAExecution completed")
            return graceful_complete()

        print(
            f"[{context.flow_id}][{context.step_execution_id}]: "
            f"Doing job {input.progress}"
        )
        return go_to(
            self,
            WorkJobParametersInput(input.job_upper_bound, input.progress + 1),
        )


class WorkNExecution(Step[WorkJobParametersInput]):
    def __init__(self, interrupt_signal: Attribute[str]) -> None:
        self.interrupt_signal = interrupt_signal

    def wait_for(self, context: Context, input: WorkJobParametersInput) -> Wait:
        del context, input
        return Wait.any_of(Timer.by_duration(timedelta(seconds=3)))

    def execute(
        self,
        context: Context,
        input: WorkJobParametersInput,
    ) -> StepDecision:
        if (self.interrupt_signal.get(context) or "") == INTERRUPT_VALUE:
            print("N: Interrupted!")
            return graceful_complete()

        if input.progress > input.job_upper_bound:
            print("Executing WorkNExecution completed")
            return graceful_complete()

        print(
            f"[{context.flow_id}][{context.step_execution_id}]: "
            f"Processing job {input.progress}"
        )
        return go_to(
            self,
            WorkJobParametersInput(input.job_upper_bound, input.progress + 1),
        )


class Init(Step[None]):
    def __init__(
        self,
        work_a_execution: WorkAExecution,
        work_n_execution: WorkNExecution,
        interrupt_signal: Attribute[str],
    ) -> None:
        self.work_a_execution = work_a_execution
        self.work_n_execution = work_n_execution
        self.interrupt_signal = interrupt_signal

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        parameters = WorkJobParametersInput(15, 1)
        return go_to_multi(
            StepMovement.of(self.work_a_execution, parameters),
            StepMovement.of(self.work_n_execution, parameters),
        )


class InterruptibleExecutionFlow(Flow[None]):
    DA_INTERRUPT_SIGNAL = "interruptSignal"

    interrupt_signal = Attribute(DA_INTERRUPT_SIGNAL, str)

    def __init__(self) -> None:
        self.work_a_execution = WorkAExecution(self.interrupt_signal)
        self.work_n_execution = WorkNExecution(self.interrupt_signal)
        self.init = Init(
            self.work_a_execution,
            self.work_n_execution,
            self.interrupt_signal,
        )

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.init).other_steps(
            self.work_a_execution,
            self.work_n_execution,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.interrupt_signal)

    @rpc
    def interrupt(self, context: Context) -> None:
        self.interrupt_signal.set(context, INTERRUPT_VALUE)
