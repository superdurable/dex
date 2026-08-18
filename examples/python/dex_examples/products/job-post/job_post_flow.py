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

import time
from datetime import timedelta

from dex import (
    Attribute,
    AttributeIndex,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    RetryPolicy,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    dead_end,
    rpc,
)

from dex_examples.shared.my_dependency_service import MyDependencyService
from dex_examples.products.job_post.job_info import JobInfo

EXTERNAL_UPDATE_OPTIONS = StepOptions(
    execute_retry=RetryPolicy(
        initial_interval=timedelta(seconds=3),
        backoff_coefficient=2.0,
        maximum_interval=timedelta(seconds=60),
        maximum_attempts=100,
        total_duration=timedelta(hours=1),
    )
)


class ExternalUpdate(Step[None]):
    def __init__(self, service: MyDependencyService) -> None:
        self.service = service

    def get_step_options(self) -> StepOptions:
        return EXTERNAL_UPDATE_OPTIONS

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        self.service.update_external_system("this is an update to external service")
        return dead_end()


class JobPostFlow(Flow[None]):
    title = Attribute("Title", str, AttributeIndex(IndexType.FULL_TEXT))
    job_description = Attribute(
        "JobDescription",
        str,
        AttributeIndex(IndexType.FULL_TEXT),
    )
    last_update_time_millis = Attribute(
        "LastUpdateTimeMillis",
        int,
        AttributeIndex(IndexType.INT),
    )
    notes = Attribute("Notes", str)

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.external_update = ExternalUpdate(service)

    def get_steps(self) -> StepList[None]:
        return StepList.without_start_step(self.external_update)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.title,
            self.job_description,
            self.last_update_time_millis,
            self.notes,
        )

    @rpc
    def get(self, context: Context) -> RPCResult[JobInfo]:
        return RPCResult(self.read_job_info(context))

    @rpc
    def get_with_strong_consistency(self, context: Context) -> RPCResult[JobInfo]:
        return self.get(context)

    @rpc
    def update(self, context: Context, input: JobInfo) -> RPCResult[None]:
        self.title.set(context, input.title or "")
        self.job_description.set(context, input.description or "")
        self.last_update_time_millis.set(context, int(time.time() * 1000))
        if input.notes is not None:
            self.notes.set(context, input.notes)
        return RPCResult(
            None,
            next_steps=(StepMovement.of(self.external_update, None),),
        )

    def read_job_info(self, context: Context) -> JobInfo:
        return JobInfo(
            self.title.get(context),
            self.job_description.get(context),
            self.notes.get(context),
        )
