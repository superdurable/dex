# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from datetime import timedelta

from dex import (
    Context,
    Flow,
    RetryPolicy,
    Step,
    StepDecision,
    StepDurability,
    StepList,
    StepOptions,
    Wait,
    graceful_complete,
)
from dex._grpc_errors import retry_after

WAIT_FOR_RETRY_AFTER_DETAIL = "python waitFor retry-after failure"
EXECUTE_RETRY_AFTER_DETAIL = "python execute retry-after failure"
RETRY_AFTER_SECONDS = 2
RETRY_POLICY_INTERVAL_SECONDS = 10


def _retry_after_step_options(*, wait_for: bool) -> StepOptions:
    retry = RetryPolicy(
        initial_interval=timedelta(seconds=RETRY_POLICY_INTERVAL_SECONDS),
        maximum_attempts=3,
    )
    if wait_for:
        return StepOptions(
            wait_for_retry=retry,
            wait_for_durability=StepDurability.SYNC,
            execute_durability=StepDurability.SYNC,
        )
    return StepOptions(
        execute_retry=retry,
        execute_durability=StepDurability.SYNC,
    )


class WorkerRetryAfterWaitForStep(Step[None]):
    def get_step_options(self) -> StepOptions:
        return _retry_after_step_options(wait_for=True)

    def wait_for(self, context: Context, _input: None) -> Wait:
        if context.attempt == 1:
            raise retry_after(
                RETRY_AFTER_SECONDS,
                RuntimeError(WAIT_FOR_RETRY_AFTER_DETAIL),
            )
        return Wait.skip_immediately()

    def execute(self, context: Context, _input: None) -> StepDecision:
        del context
        return graceful_complete("wait-retry-after")


class WorkerRetryAfterExecuteStep(Step[None]):
    def get_step_options(self) -> StepOptions:
        return _retry_after_step_options(wait_for=False)

    def execute(self, context: Context, _input: None) -> StepDecision:
        if context.attempt == 1:
            raise retry_after(
                RETRY_AFTER_SECONDS,
                RuntimeError(EXECUTE_RETRY_AFTER_DETAIL),
            )
        return graceful_complete("execute-retry-after")


class WorkerRetryAfterWaitForFlow(Flow[None]):
    def __init__(self) -> None:
        self.start = WorkerRetryAfterWaitForStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)


class WorkerRetryAfterExecuteFlow(Flow[None]):
    def __init__(self) -> None:
        self.start = WorkerRetryAfterExecuteStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)
