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

"""Recovers exhausted work retries through a manual retry or skip decision."""

from __future__ import annotations

from datetime import timedelta

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Wait,
    force_fail,
    go_to,
    graceful_complete,
)


class DoWorkStep(Step[bool]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                backoff_coefficient=2.0,
                maximum_interval=timedelta(seconds=4),
                maximum_attempts=4,
            )
        ).on_execute_failure_proceed_to(ManualStep)

    def execute(self, context: Context, should_fail: bool) -> StepDecision:
        del context
        if should_fail:
            raise RuntimeError("work failed")
        return graceful_complete("work completed")


class ManualStep(Step[bool]):
    def __init__(
        self,
        retry_channel: Channel[None],
        skip_channel: Channel[None],
    ) -> None:
        self.retry_channel = retry_channel
        self.skip_channel = skip_channel

    def wait_for(self, context: Context, input: bool) -> Wait:
        del context, input
        return Wait.any_of(
            self.retry_channel.for_one(),
            self.skip_channel.for_one(),
        )

    def execute(self, context: Context, input: bool) -> StepDecision:
        del input
        if self.retry_channel.results(context):
            return go_to(DoWorkStep, False)
        return force_fail("manual recovery skipped")


class ManualRecoveryFlow(Flow[bool]):
    RETRY_CHANNEL = "manual-recovery-retry"
    SKIP_CHANNEL = "manual-recovery-skip"

    retry_channel = Channel[None](RETRY_CHANNEL, type(None))
    skip_channel = Channel[None](SKIP_CHANNEL, type(None))

    def __init__(self) -> None:
        self.do_work_step = DoWorkStep()
        self.manual_step = ManualStep(self.retry_channel, self.skip_channel)

    def get_steps(self) -> StepList[bool]:
        return StepList.start_step(self.do_work_step).other_steps(self.manual_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.retry_channel, self.skip_channel)
