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

import json
from dataclasses import asdict

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.shared.service_dependency import ServiceDependency
from dex_examples.patterns.wait_for_step_completion.job_seeker_data import (
    JobSeekerData,
)


class UpdateExternalSystem(Step[JobSeekerData]):
    def __init__(self, external_service: ServiceDependency) -> None:
        self.external_service = external_service

    def execute(self, context: Context, input: JobSeekerData) -> StepDecision:
        del context
        self.external_service.update_external_system(
            json.dumps(asdict(input), sort_keys=True)
        )
        return graceful_complete()


class PersistData(Step[JobSeekerData]):
    def __init__(
        self,
        update_external_system: UpdateExternalSystem,
        mongo_collection: ServiceDependency,
        job_seeker_data: Attribute[JobSeekerData],
    ) -> None:
        self.update_external_system = update_external_system
        self.mongo_collection = mongo_collection
        self.job_seeker_data = job_seeker_data

    def execute(self, context: Context, input: JobSeekerData) -> StepDecision:
        self.mongo_collection.upsert(input)
        self.job_seeker_data.set(context, input)
        return go_to(UpdateExternalSystem, input)


class WaitForStepCompletionFlow(Flow[JobSeekerData]):
    JOB_SEEKER_DATA = "job_seeker_data"

    job_seeker_data = Attribute(JOB_SEEKER_DATA, JobSeekerData)

    def __init__(self, service: ServiceDependency) -> None:
        self.update_external_system = UpdateExternalSystem(service)
        self.persist_data = PersistData(
            self.update_external_system,
            service,
            self.job_seeker_data,
        )

    def get_steps(self) -> StepList[JobSeekerData]:
        return StepList.start_step(self.persist_data).other_steps(
            self.update_external_system
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.job_seeker_data)

    @rpc
    def get_job_seeker_data(self, context: Context) -> RPCResult[JobSeekerData]:
        data = self.job_seeker_data.get(context)
        if data is None:
            raise RuntimeError("Job seeker data was not persisted to the data store")
        return RPCResult(data)
