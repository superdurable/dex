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
    StepOptions,
    SubFlow,
    Wait,
    force_complete_if_channels_empty,
    go_to,
    go_to_many,
    rpc,
)

from dex_examples.patterns.parallel_subflows.example_subflow import ExampleSubFlow
from dex_examples.patterns.parallel_subflows.models import (
    DEFAULT_CONCURRENCY,
    MAX_BUFFERED_REQUESTS,
    ParentInput,
)


class ShortLiveInitStep(Step[ParentInput]):
    def __init__(
        self,
        request_channel: Channel[str],
        curr_subflow_num: Attribute[int],
    ) -> None:
        self.request_channel = request_channel
        self.curr_subflow_num = curr_subflow_num

    def get_step_type(self) -> str:
        return "InitStep"

    def execute(self, context: Context, input: ParentInput) -> StepDecision:
        for request in input.requests:
            self.request_channel.publish(context, request)
        self.curr_subflow_num.set(context, 0)
        concurrency = input.concurrency if input.concurrency > 0 else DEFAULT_CONCURRENCY
        return go_to_many(
            *(StepMovement.of(ShortLiveHandleRequestStep, None) for _ in range(concurrency))
        )


class ShortLiveHandleRequestStep(Step[None]):
    def __init__(
        self,
        request_channel: Channel[str],
        curr_subflow_num: Attribute[int],
    ) -> None:
        self.request_channel = request_channel
        self.curr_subflow_num = curr_subflow_num

    def get_step_type(self) -> str:
        return "HandleRequestStep"

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_lock_attributes=(self.curr_subflow_num.lock(),))

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(self.request_channel.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        request = self.request_channel.results(context)[0]
        self.curr_subflow_num.set(context, (self.curr_subflow_num.get(context) or 0) + 1)
        return go_to(ShortLiveHandleSubFlowStep, request)


class ShortLiveHandleSubFlowStep(Step[str]):
    def __init__(
        self,
        example_subflow: ExampleSubFlow,
        request_channel: Channel[str],
        curr_subflow_num: Attribute[int],
    ) -> None:
        self.example_subflow = example_subflow
        self.request_channel = request_channel
        self.curr_subflow_num = curr_subflow_num

    def get_step_type(self) -> str:
        return "HandleSubFlowStep"

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_lock_attributes=(self.curr_subflow_num.lock(),))

    def wait_for(self, context: Context, request: str) -> Wait:
        return Wait.until(SubFlow.run(self.example_subflow, request))

    def execute(self, context: Context, request: str) -> StepDecision:
        current = (self.curr_subflow_num.get(context) or 0) - 1
        self.curr_subflow_num.set(context, current)
        if current == 0:
            return force_complete_if_channels_empty(
                None,
                StepMovement.of(ShortLiveHandleRequestStep, None),
                self.request_channel,
            )
        return go_to(ShortLiveHandleRequestStep, None)


class AdvancedShortLiveParentFlow(Flow[ParentInput]):
    request_channel = Channel("RequestChannel", str)
    curr_subflow_num = Attribute("CurrSubFlowNum", int)

    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.init = ShortLiveInitStep(self.request_channel, self.curr_subflow_num)
        self.handle_request = ShortLiveHandleRequestStep(
            self.request_channel, self.curr_subflow_num
        )
        self.handle_subflow = ShortLiveHandleSubFlowStep(
            example_subflow, self.request_channel, self.curr_subflow_num
        )

    def get_steps(self) -> StepList[ParentInput]:
        return StepList.start_step(self.init).other_steps(
            self.handle_request, self.handle_subflow
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.request_channel, self.curr_subflow_num)

    @rpc
    def send_request(self, context: Context, request: str) -> RPCResult[bool]:
        if self.request_channel.size(context) >= MAX_BUFFERED_REQUESTS:
            return RPCResult(False)
        self.request_channel.publish(context, request)
        return RPCResult(True)
