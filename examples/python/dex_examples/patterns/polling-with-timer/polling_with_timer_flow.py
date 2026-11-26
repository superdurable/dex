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
    Timer,
    Wait,
    force_fail,
    go_to,
    graceful_complete,
)

from dex_examples.patterns.polling.job_service import FakeJobService, JobState

# MAX_POLL_ROUNDS bounds the number of checks, not elapsed time; calls, retries, and queueing add time.
POLL_INTERVAL = timedelta(seconds=10)
POLL_INTERVAL_TIMER_ID = "poll-interval"
MAX_POLL_ROUNDS = 60
JOB_CALL_TIMEOUT = timedelta(seconds=10)


@dataclass(frozen=True)
class JobPollRound:
    job_id: str
    round_number: int


# dex:group group-id:failure group-label:"Failure"
# dex:explanation text:"Fail the Flow with the reason the status check failed."
class RecordJobWaitFailure(Step[JobPollRound]):
    def execute(self, context: Context, poll_round: JobPollRound) -> StepDecision:
        failure = context.recovery_error
        assert failure is not None
        return force_fail(
            f"checking job {poll_round.job_id} failed: "
            f"{failure.error_type}: {failure.detail}"
        )


# dex:group group-id:polling group-label:"Polling"
# dex:explanation text:"Wait for the interval Timer, then check the job once."
class CheckJobStatus(Step[JobPollRound]):
    def __init__(self, jobs: FakeJobService) -> None:
        self.jobs = jobs

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                backoff_coefficient=2.0,
                maximum_attempts=3,
            ),
        ).on_execute_failure_proceed_to(RecordJobWaitFailure)

    def wait_for(self, context: Context, poll_round: JobPollRound) -> Wait:
        return Wait.until(
            Timer.by_duration(POLL_INTERVAL, condition_id=POLL_INTERVAL_TIMER_ID)
        )

    async def execute(
        self, context: AsyncContext, poll_round: JobPollRound
    ) -> StepDecision:
        status = await asyncio.wait_for(
            self.jobs.get_job_status(poll_round.job_id),
            JOB_CALL_TIMEOUT.total_seconds(),
        )
        if status.state is JobState.SUCCEEDED:
            return graceful_complete(status.result)
        if status.state is JobState.FAILED:
            return force_fail(f"job {poll_round.job_id} failed")
        if poll_round.round_number >= MAX_POLL_ROUNDS:
            return force_fail(
                f"job {poll_round.job_id} did not finish in {MAX_POLL_ROUNDS} rounds"
            )
        next_round = JobPollRound(poll_round.job_id, poll_round.round_number + 1)
        return go_to(CheckJobStatus, next_round)


# dex:group group-id:start-job group-label:"Start job"
# dex:explanation text:"Start the external job."
class StartJob(Step[None]):
    def __init__(self, jobs: FakeJobService) -> None:
        self.jobs = jobs

    async def execute(self, context: AsyncContext, input: None) -> StepDecision:
        job_id = context.flow_id
        await asyncio.wait_for(
            self.jobs.start_job(job_id), JOB_CALL_TIMEOUT.total_seconds()
        )
        return go_to(CheckJobStatus, JobPollRound(job_id, 1))


class PollingWithTimerFlow(Flow[None]):
    def __init__(self, jobs: FakeJobService) -> None:
        self.start_job = StartJob(jobs)
        self.check_job_status = CheckJobStatus(jobs)
        self.record_job_wait_failure = RecordJobWaitFailure()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start_job).other_steps(
            self.check_job_status, self.record_job_wait_failure
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
