# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from collections import Counter
from datetime import timedelta

from dex import (
    Context,
    Flow,
    NonRetryableError,
    RecoveryErrorInfo,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Wait,
    WaitForFailurePolicy,
    graceful_complete,
)

NON_RETRYABLE_DETAIL = "python card rejected"

# Handler calls and recovery errors per Flow ID; the Worker runs in the test process.
attempts_by_flow_id: Counter[str] = Counter()
recovery_by_flow_id: dict[str, RecoveryErrorInfo] = {}


class CardRejectedError(Exception):
    pass


def _hourly_retry() -> RetryPolicy:
    """A real business retry policy: unlimited attempts an hour apart."""
    return RetryPolicy(
        initial_interval=timedelta(hours=1),
        maximum_interval=timedelta(hours=1),
    )


def _fail_permanently(context: Context) -> None:
    attempts_by_flow_id[context.flow_id] += 1
    try:
        raise CardRejectedError(NON_RETRYABLE_DETAIL)
    except CardRejectedError as error:
        raise NonRetryableError(error) from error


def _record_recovery(context: Context) -> StepDecision:
    recovery = context.recovery_error
    if recovery is None:
        raise NonRetryableError("recovery error is missing")
    recovery_by_flow_id[context.flow_id] = recovery
    return graceful_complete(recovery.detail)


class NonRetryableRecoveryStep(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        del context, input
        return Wait.skip_immediately()

    def execute(self, context: Context, input: str) -> StepDecision:
        del input
        return _record_recovery(context)


class NonRetryableChargeStep(Step[str]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=_hourly_retry()).on_execute_failure_proceed_to(
            NonRetryableRecoveryStep
        )

    def wait_for(self, context: Context, input: str) -> Wait:
        del context, input
        return Wait.skip_immediately()

    def execute(self, context: Context, input: str) -> StepDecision:
        del input
        _fail_permanently(context)
        raise AssertionError("unreachable")


class NonRetryableExecuteFlow(Flow[str]):
    def __init__(self) -> None:
        self.start = NonRetryableChargeStep()
        self.recover = NonRetryableRecoveryStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.start).other_steps(self.recover)


class NonRetryableWaitForStep(Step[None]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_retry=_hourly_retry(),
            wait_for_failure=WaitForFailurePolicy.PROCEED,
        )

    def wait_for(self, context: Context, input: None) -> Wait:
        del input
        _fail_permanently(context)
        raise AssertionError("unreachable")

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        return _record_recovery(context)


class NonRetryableWaitForFlow(Flow[None]):
    def __init__(self) -> None:
        self.start = NonRetryableWaitForStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)
