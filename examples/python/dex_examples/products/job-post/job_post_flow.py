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
    Channel,
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
    Wait,
    go_to,
    go_to_many,
    rpc,
)

from dex_examples.shared.my_dependency_service import MyDependencyService
from dex_examples.products.job_post.job_info import JobInfo, PostingUpdate

JOB_BOARD_UPDATE_RETRY = RetryPolicy(
    initial_interval=timedelta(seconds=3),
    backoff_coefficient=2.0,
    maximum_interval=timedelta(seconds=60),
    maximum_attempts=100,
    total_duration=timedelta(hours=1),
)
UPDATE_VERSION = Attribute("UpdateVersion", int)
UPDATE_POSTING_LOCK = Attribute("UpdatePostingLock", type(None))
LINKEDIN_POSTING_UPDATES = Channel("LinkedInPostingUpdates", PostingUpdate)
INDEED_POSTING_UPDATES = Channel("IndeedPostingUpdates", PostingUpdate)


class InitStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to_many(
            StepMovement.of(UpdateLinkedInPosting, None),
            StepMovement.of(UpdateIndeedPosting, None),
        )


class UpdateLinkedInPosting(Step[None]):
    def __init__(self, service: MyDependencyService) -> None:
        self.service = service

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=JOB_BOARD_UPDATE_RETRY)

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(LINKEDIN_POSTING_UPDATES.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        update = LINKEDIN_POSTING_UPDATES.results(context)[0]
        self.service.update_external_system(
            f"update LinkedIn job posting v{update.version} "
            f"[{update.idempotency_key}]: {update.posting.title}"
        )
        return go_to(UpdateLinkedInPosting, None)


class UpdateIndeedPosting(Step[None]):
    def __init__(self, service: MyDependencyService) -> None:
        self.service = service

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=JOB_BOARD_UPDATE_RETRY)

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(INDEED_POSTING_UPDATES.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        update = INDEED_POSTING_UPDATES.results(context)[0]
        self.service.update_external_system(
            f"update Indeed job posting v{update.version} "
            f"[{update.idempotency_key}]: {update.posting.title}"
        )
        return go_to(UpdateIndeedPosting, None)


class JobPostingFlow(Flow[None]):
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
    update_version = UPDATE_VERSION
    update_posting_lock = UPDATE_POSTING_LOCK
    linkedin_posting_updates = LINKEDIN_POSTING_UPDATES
    indeed_posting_updates = INDEED_POSTING_UPDATES

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.init = InitStep()
        self.update_linkedin_posting = UpdateLinkedInPosting(service)
        self.update_indeed_posting = UpdateIndeedPosting(service)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.init).other_steps(
            self.update_linkedin_posting,
            self.update_indeed_posting,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.title,
            self.job_description,
            self.last_update_time_millis,
            self.notes,
            self.update_version,
            self.update_posting_lock,
            self.linkedin_posting_updates,
            self.indeed_posting_updates,
        )

    @rpc
    def get(self, context: Context) -> RPCResult[JobInfo]:
        return RPCResult(self.read_job_info(context))

    @rpc
    def get_with_strong_consistency(self, context: Context) -> RPCResult[JobInfo]:
        return self.get(context)

    @rpc(lock_attributes=(update_posting_lock.lock(),))
    def update(self, context: Context, input: JobInfo) -> RPCResult[int]:
        version = self.update_version.get(context) + 1
        self.title.set(context, input.title or "")
        self.job_description.set(context, input.description or "")
        self.last_update_time_millis.set(context, int(time.time() * 1000))
        if input.notes is not None:
            self.notes.set(context, input.notes)
        self.update_version.set(context, version)
        update = PostingUpdate(
            version,
            f"{context.flow_id}:{version}",
            input,
        )
        self.linkedin_posting_updates.publish(context, update)
        self.indeed_posting_updates.publish(context, update)
        return RPCResult(version)

    def read_job_info(self, context: Context) -> JobInfo:
        return JobInfo(
            self.title.get(context),
            self.job_description.get(context),
            self.notes.get(context),
        )
