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
    RPCResult,
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
    rpc,
)

from dex_examples.my_dependency_service import MyDependencyService


class CallAPI4(Step[None]):
    def __init__(self, service: MyDependencyService, data: Attribute[str]) -> None:
        self.service = service
        self.data = data

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        value = self.data.get(context)
        self.service.call_api4(value)
        return graceful_complete(value)


class CallAPI3(Step[None]):
    def __init__(
        self,
        service: MyDependencyService,
        data: Attribute[str],
        ready: Channel[None],
        call_api4: CallAPI4,
    ) -> None:
        self.service = service
        self.data = data
        self.ready = ready
        self.call_api4 = call_api4

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(timedelta(hours=24)),
            self.ready.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        value = self.data.get(context)
        self.service.call_api3(value)
        if context.has_timer_fired():
            return go_to(self.call_api4, None)
        return graceful_complete(value)


class CallAPI2(Step[None]):
    def __init__(self, service: MyDependencyService, data: Attribute[str]) -> None:
        self.service = service
        self.data = data

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.service.call_api2(self.data.get(context))
        return dead_end()


class CallAPI1(Step[str]):
    def __init__(
        self,
        service: MyDependencyService,
        data: Attribute[str],
        call_api2: CallAPI2,
        call_api3: CallAPI3,
    ) -> None:
        self.service = service
        self.data = data
        self.call_api2 = call_api2
        self.call_api3 = call_api3

    def execute(self, context: Context, input: str) -> StepDecision:
        self.service.call_api1(input)
        self.data.set(context, input)
        return go_to_multi(
            StepMovement.of(self.call_api2, None),
            StepMovement.of(self.call_api3, None),
        )


class OrchestrationFlow(Flow[str]):
    data = Attribute("data", str)
    ready = Channel[None]("Ready", type(None))

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.call_api4 = CallAPI4(service, self.data)
        self.call_api3 = CallAPI3(service, self.data, self.ready, self.call_api4)
        self.call_api2 = CallAPI2(service, self.data)
        self.call_api1 = CallAPI1(service, self.data, self.call_api2, self.call_api3)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.call_api1).other_steps(
            self.call_api2,
            self.call_api3,
            self.call_api4,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.data, self.ready)

    @rpc
    def swap(self, context: Context, new_data: str) -> RPCResult[str]:
        old_data = self.data.get(context)
        self.data.set(context, new_data)
        return RPCResult(old_data)
