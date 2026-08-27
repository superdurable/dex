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
    go_to_multi,
    graceful_complete,
)

from dex_examples.patterns.shared.service_dependency import ServiceDependency
from dex_examples.patterns.drain_channels.internal.mongo_document import (
    MongoDocument,
)


class Finalize(Step[None]):
    def __init__(self, upsert_mongo_data: Channel[MongoDocument]) -> None:
        self.upsert_mongo_data = upsert_mongo_data

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.upsert_mongo_data.publish(
            context,
            MongoDocument("documentId-1", "FINALIZED", True),
        )
        return graceful_complete()


class ProcessData(Step[str]):
    def __init__(
        self,
        finalize: Finalize,
        external_service: ServiceDependency,
        upsert_mongo_data: Channel[MongoDocument],
        execution_counter: Attribute[int],
    ) -> None:
        self.finalize = finalize
        self.external_service = external_service
        self.upsert_mongo_data = upsert_mongo_data
        self.execution_counter = execution_counter

    def execute(self, context: Context, input: str) -> StepDecision:
        execution_count = self.execution_counter.get(context) + 1
        self.execution_counter.set(context, execution_count)

        statuses = {1: "RECEIVED", 2: "ACCEPTED", 3: "PASSED"}
        self.upsert_mongo_data.publish(
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
            return go_to(ProcessData, input)
        return go_to(Finalize, None)


class UpsertMongoRecord(Step[None]):
    def __init__(
        self,
        mongo_collection: ServiceDependency,
        upsert_mongo_data: Channel[MongoDocument],
    ) -> None:
        self.mongo_collection = mongo_collection
        self.upsert_mongo_data = upsert_mongo_data

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.until(self.upsert_mongo_data.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        documents = self.upsert_mongo_data.results(context)
        if not documents:
            raise RuntimeError("No document was sent")

        document = documents[0]
        if document is None:
            raise RuntimeError("No data was sent")

        self.mongo_collection.upsert(document)

        if document.final_command:
            return graceful_complete()
        return go_to(UpsertMongoRecord, None)


class Init(Step[str]):
    def __init__(
        self,
        upsert_mongo_record: UpsertMongoRecord,
        process_data: ProcessData,
        execution_counter: Attribute[int],
    ) -> None:
        self.upsert_mongo_record = upsert_mongo_record
        self.process_data = process_data
        self.execution_counter = execution_counter

    def execute(self, context: Context, input: str) -> StepDecision:
        self.execution_counter.set(context, 0)
        return go_to_multi(
            StepMovement.of(UpsertMongoRecord, None),
            StepMovement.of(ProcessData, input),
        )


class DrainInternalChannelsFlow(Flow[str]):
    UPSERT_MONGO_DATA_INTERNAL_CHANNEL = "upsert_mongo_data_internal_channel"
    PROCESS_DATA_STATE_EXECUTION_COUNTER = "process_data_state_execution_counter"

    process_data_state_execution_counter = Attribute(
        PROCESS_DATA_STATE_EXECUTION_COUNTER,
        int,
    )
    upsert_mongo_data = Channel(UPSERT_MONGO_DATA_INTERNAL_CHANNEL, MongoDocument)

    def __init__(self, service: ServiceDependency) -> None:
        self.finalize = Finalize(self.upsert_mongo_data)
        self.process_data = ProcessData(
            self.finalize,
            service,
            self.upsert_mongo_data,
            self.process_data_state_execution_counter,
        )
        self.upsert_mongo_record = UpsertMongoRecord(service, self.upsert_mongo_data)
        self.init = Init(
            self.upsert_mongo_record,
            self.process_data,
            self.process_data_state_execution_counter,
        )

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.init).other_steps(
            self.upsert_mongo_record,
            self.process_data,
            self.finalize,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.process_data_state_execution_counter,
            self.upsert_mongo_data,
        )
