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

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    SubFlow,
    Wait,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.parallel_subflows.example_subflow import ExampleSubFlow
from dex_examples.patterns.parallel_subflows.models import (
    DEFAULT_CONCURRENCY,
    MAX_BUFFERED_REQUESTS,
    ParentInput,
)


class LongLiveInitStep(Step[ParentInput]):
    def __init__(
        self,
        request_channel: Channel[str],
        stopped: Attribute[bool],
    ) -> None:
        self.request_channel = request_channel
        self.stopped = stopped

    def get_step_type(self) -> str:
        return "InitStep"

    def execute(self, context: Context, input: ParentInput) -> StepDecision:
        for request in input.requests:
            self.request_channel.publish(context, request)
        self.stopped.set(context, False)
        concurrency = input.concurrency if input.concurrency > 0 else DEFAULT_CONCURRENCY
        return go_to_many(
            *(StepMovement.of(LongLiveHandleRequestStep, None) for _ in range(concurrency))
        )


class LongLiveHandleRequestStep(Step[None]):
    def __init__(self, request_channel: Channel[str]) -> None:
        self.request_channel = request_channel

    def get_step_type(self) -> str:
        return "HandleRequestStep"

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(self.request_channel.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(LongLiveHandleSubFlowStep, self.request_channel.results(context)[0])


class LongLiveHandleSubFlowStep(Step[str]):
    def __init__(
        self,
        example_subflow: ExampleSubFlow,
        stopped: Attribute[bool],
    ) -> None:
        self.example_subflow = example_subflow
        self.stopped = stopped

    def get_step_type(self) -> str:
        return "HandleSubFlowStep"

    def wait_for(self, context: Context, request: str) -> Wait:
        return Wait.until(SubFlow.run(self.example_subflow, request))

    def execute(self, context: Context, request: str) -> StepDecision:
        if self.stopped.get(context):
            return graceful_complete()
        return go_to(LongLiveHandleRequestStep, None)


class AdvancedLongLiveParentFlow(Flow[ParentInput]):
    request_channel = Channel("RequestChannel", str)
    stopped = Attribute("Stopped", bool)

    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.init = LongLiveInitStep(self.request_channel, self.stopped)
        self.handle_request = LongLiveHandleRequestStep(self.request_channel)
        self.handle_subflow = LongLiveHandleSubFlowStep(example_subflow, self.stopped)

    def get_steps(self) -> StepList[ParentInput]:
        return StepList.start_step(self.init).other_steps(
            self.handle_request, self.handle_subflow
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.request_channel, self.stopped)

    @rpc
    def send_request(self, context: Context, request: str) -> RPCResult[bool]:
        if self.request_channel.size(context) >= MAX_BUFFERED_REQUESTS:
            return RPCResult(False)
        self.request_channel.publish(context, request)
        return RPCResult(True)

    @rpc
    def stop(self, context: Context) -> None:
        self.stopped.set(context, True)
