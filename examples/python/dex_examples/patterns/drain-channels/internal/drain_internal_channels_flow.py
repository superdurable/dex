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
    Wait,
    go_to,
    go_to_many,
    graceful_complete,
)

from dex_examples.patterns.shared.service_dependency import ServiceDependency
from dex_examples.patterns.drain_channels.internal.mongo_document import (
    MongoDocument,
)


class Finalize(Step[None]):
    def __init__(self, side_step_data: Channel[MongoDocument]) -> None:
        self.side_step_data = side_step_data

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.side_step_data.publish(
            context,
            MongoDocument("documentId-1", "FINALIZED", True),
        )
        return graceful_complete()


class MainStep(Step[str]):
    def __init__(
        self,
        finalize: Finalize,
        external_service: ServiceDependency,
        side_step_data: Channel[MongoDocument],
        execution_counter: Attribute[int],
    ) -> None:
        self.finalize = finalize
        self.external_service = external_service
        self.side_step_data = side_step_data
        self.execution_counter = execution_counter

    def execute(self, context: Context, input: str) -> StepDecision:
        execution_count = self.execution_counter.get(context) + 1
        self.execution_counter.set(context, execution_count)

        statuses = {1: "RECEIVED", 2: "ACCEPTED", 3: "PASSED"}
        self.side_step_data.publish(
            context,
            MongoDocument(input, statuses.get(execution_count, "ERROR"), False),
        )

        self.external_service.external_api_call(
            "external service call to process data (e.g. notify the job seeker)"
        )
        self.external_service.external_api_call(
            "a call to send metrics or add a log to logrepo"
        )

        if execution_count <= 3:
            return go_to(MainStep, input)
        return go_to(Finalize, None)


class SideStep(Step[None]):
    def __init__(
        self,
        mongo_collection: ServiceDependency,
        side_step_data: Channel[MongoDocument],
    ) -> None:
        self.mongo_collection = mongo_collection
        self.side_step_data = side_step_data

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.until(self.side_step_data.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        documents = self.side_step_data.results(context)
        if not documents:
            raise RuntimeError("No document was sent")

        document = documents[0]
        if document is None:
            raise RuntimeError("No data was sent")

        self.mongo_collection.upsert(document)

        if document.final_command:
            return graceful_complete()
        return go_to(SideStep, None)


class Init(Step[str]):
    def __init__(
        self,
        side_step: SideStep,
        main_step: MainStep,
        execution_counter: Attribute[int],
    ) -> None:
        self.side_step = side_step
        self.main_step = main_step
        self.execution_counter = execution_counter

    def execute(self, context: Context, input: str) -> StepDecision:
        self.execution_counter.set(context, 0)
        return go_to_many(
            StepMovement.of(SideStep, None),
            StepMovement.of(MainStep, input),
        )


class DrainInternalChannelFlow(Flow[str]):
    SIDE_STEP_DATA_CHANNEL = "SideStepData"
    MAIN_STEP_EXECUTION_COUNTER = "main_step_execution_counter"

    main_step_execution_counter = Attribute(
        MAIN_STEP_EXECUTION_COUNTER,
        int,
    )
    side_step_data = Channel(SIDE_STEP_DATA_CHANNEL, MongoDocument)

    def __init__(self, service: ServiceDependency) -> None:
        self.finalize = Finalize(self.side_step_data)
        self.main_step = MainStep(
            self.finalize,
            service,
            self.side_step_data,
            self.main_step_execution_counter,
        )
        self.side_step = SideStep(service, self.side_step_data)
        self.init = Init(
            self.side_step,
            self.main_step,
            self.main_step_execution_counter,
        )

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.init).other_steps(
            self.side_step,
            self.main_step,
            self.finalize,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.main_step_execution_counter,
            self.side_step_data,
        )
