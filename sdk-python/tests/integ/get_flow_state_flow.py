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

from datetime import timedelta

from dex import (
    Context,
    Flow,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Timer,
    Wait,
    graceful_complete,
)

GET_FLOW_STATE_FAILURE_DETAIL = "python get_flow_state charge declined"
GET_FLOW_STATE_RETRY_INTERVAL = timedelta(seconds=60)


class ChargeDeclinedError(Exception):
    pass


class GetFlowStateFailingStep(Step[None]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=GET_FLOW_STATE_RETRY_INTERVAL,
                maximum_attempts=100,
            )
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        raise ChargeDeclinedError(GET_FLOW_STATE_FAILURE_DETAIL)


class GetFlowStateFailingFlow(Flow[None]):
    def __init__(self) -> None:
        self.start = GetFlowStateFailingStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)


class GetFlowStateWaitingStep(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(
            Timer.by_duration(timedelta(hours=1), condition_id="get-flow-state-timer")
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("waited")


class GetFlowStateWaitingFlow(Flow[None]):
    def __init__(self) -> None:
        self.start = GetFlowStateWaitingStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)
