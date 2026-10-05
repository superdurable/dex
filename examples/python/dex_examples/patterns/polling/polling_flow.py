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

import asyncio
from datetime import timedelta

from dex import (
    AsyncContext,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Stream,
    force_fail,
    go_to,
    graceful_complete,
)

from dex_examples.patterns.polling.job_service import FakeJobService, JobState, JobStatus

# Keep POLL_INTERVAL + JOB_CALL_TIMEOUT <= heartbeat_timeout - 10s.
# MAX_JOB_WAIT bounds one attempt and all retries; Dex enforces it.
POLL_INTERVAL = timedelta(seconds=2)
JOB_CALL_TIMEOUT = timedelta(seconds=10)
MAX_JOB_WAIT = timedelta(minutes=10)

JOB_PROGRESS = Stream("JobProgress", JobStatus, 1 << 20)


class RecordJobWaitFailure(Step[str]):
    def execute(self, context: Context, job_id: str) -> StepDecision:
        failure = context.recovery_error
        assert failure is not None
        return force_fail(
            f"waiting for job {job_id} failed: {failure.error_type}: {failure.detail}"
        )


class AwaitJob(Step[str]):
    def __init__(self, jobs: FakeJobService) -> None:
        self.jobs = jobs

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=MAX_JOB_WAIT,
            heartbeat_timeout=timedelta(minutes=1),
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                backoff_coefficient=2.0,
                maximum_attempts=3,
                total_duration=MAX_JOB_WAIT,
            ),
        ).on_execute_failure_proceed_to(RecordJobWaitFailure)

    async def execute(self, context: AsyncContext, job_id: str) -> StepDecision:
        reported_state = context.get_last_heartbeat_value(JobState)
        while True:
            status = await self.get_job_status(job_id)
            if status.state is JobState.SUCCEEDED:
                return graceful_complete(status.result)
            if status.state is JobState.FAILED:
                return force_fail(f"job {job_id} failed")
            if status.state != reported_state:
                JOB_PROGRESS.write(context, status)
                reported_state = status.state
            else:
                await context.heartbeat(reported_state)
            await asyncio.sleep(POLL_INTERVAL.total_seconds())

    async def get_job_status(self, job_id: str) -> JobStatus:
        return await asyncio.wait_for(
            self.jobs.get_job_status(job_id), JOB_CALL_TIMEOUT.total_seconds()
        )


class StartJob(Step[None]):
    def __init__(self, jobs: FakeJobService) -> None:
        self.jobs = jobs

    async def execute(self, context: AsyncContext, input: None) -> StepDecision:
        job_id = context.flow_id
        await asyncio.wait_for(
            self.jobs.start_job(job_id), JOB_CALL_TIMEOUT.total_seconds()
        )
        return go_to(AwaitJob, job_id)


class PollingFlow(Flow[None]):
    def __init__(self, jobs: FakeJobService) -> None:
        self.start_job = StartJob(jobs)
        self.await_job = AwaitJob(jobs)
        self.record_job_wait_failure = RecordJobWaitFailure()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_job).other_steps(
            self.await_job, self.record_job_wait_failure
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(JOB_PROGRESS)
