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
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

from dex import (
    AsyncContext,
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
POLL_INTERVAL = timedelta(seconds=2)
JOB_CALL_TIMEOUT = timedelta(seconds=10)
JOB_WAIT_BUDGET = timedelta(minutes=5)

JOB_PROGRESS = Stream("JobProgress", JobStatus, 1 << 20)


# The checkpoint carries the deadline so it stays stable across retries.
@dataclass(frozen=True)
class AwaitJobCheckpoint:
    reported_state: JobState
    deadline: datetime


class AwaitJob(Step[str]):
    def __init__(self, jobs: FakeJobService) -> None:
        self.jobs = jobs

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=timedelta(minutes=10),
            heartbeat_timeout=timedelta(minutes=1),
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                backoff_coefficient=2.0,
                maximum_attempts=3,
                total_duration=timedelta(minutes=10),
            ),
        )

    async def execute(self, context: AsyncContext, job_id: str) -> StepDecision:
        checkpoint = context.get_last_heartbeat_value(AwaitJobCheckpoint)
        deadline = checkpoint.deadline if checkpoint else datetime.now(UTC) + JOB_WAIT_BUDGET
        reported_state = checkpoint.reported_state if checkpoint else None
        while True:
            status = await self.get_job_status(job_id)
            if status.state is JobState.SUCCEEDED:
                return graceful_complete(status.result)
            if status.state is JobState.FAILED:
                return force_fail(f"job {job_id} failed")
            if datetime.now(UTC) > deadline:
                return force_fail(f"job {job_id} did not finish within {JOB_WAIT_BUDGET}")
            if status.state != reported_state:
                JOB_PROGRESS.write(context, status)
                reported_state = status.state
            else:
                await context.heartbeat(AwaitJobCheckpoint(status.state, deadline))
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

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_job).other_steps(self.await_job)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(JOB_PROGRESS)
