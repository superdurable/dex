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

"""Fires one waiting refund Timer now: skip_timer FLOW_ID TIMER.

TIMER is manager-reminder, customer-idle, settlement-poll, or refund-deadline.
"""

from __future__ import annotations

import argparse
import asyncio
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path

from dex import (
    AsyncClient,
    DexServiceError,
    FlowNotActiveOrNotFoundError,
    StepExecutionId,
    TimerId,
)

from dex_examples.products.refund_durable_timers.refund_settings import (
    new_presenter_client,
    new_refund_timers_flow,
    open_cache,
)
from dex_examples.products.refund_durable_timers.refund_timers_flow import (
    CUSTOMER_IDLE_TIMER_ID,
    MANAGER_REMINDER_TIMER_ID,
    SETTLEMENT_POLL_TIMER_ID,
    AwaitManagerDecision,
    CollectSupportingDocuments,
    PollSettlementWithTimer,
)

# The Flow timeout is the timeout handler's only Timer: index 0 of execution 1.
FLOW_TIMEOUT_STEP_TYPE = "sys:timeout_handler"
SKIP_RETRY_INTERVAL_SECONDS = 0.5


@dataclass(frozen=True)
class RefundTimer:
    step_type: str
    timer_id: TimerId


REFUND_TIMERS = {
    "manager-reminder": RefundTimer(
        AwaitManagerDecision.__name__, TimerId.by_condition_id(MANAGER_REMINDER_TIMER_ID)
    ),
    "customer-idle": RefundTimer(
        CollectSupportingDocuments.__name__, TimerId.by_condition_id(CUSTOMER_IDLE_TIMER_ID)
    ),
    "settlement-poll": RefundTimer(
        PollSettlementWithTimer.__name__, TimerId.by_condition_id(SETTLEMENT_POLL_TIMER_ID)
    ),
    "refund-deadline": RefundTimer(FLOW_TIMEOUT_STEP_TYPE, TimerId.by_condition_index(0)),
}


class TimerNotWaitingError(RuntimeError):
    """Raised when the named Timer is not waiting before the deadline."""


async def find_waiting_execution(
    client: AsyncClient, flow_id: str, timer: RefundTimer
) -> StepExecutionId | None:
    """Returns the open execution of the Timer's Step, or None when that Step is not open."""
    if timer.step_type == FLOW_TIMEOUT_STEP_TYPE:
        return StepExecutionId(FLOW_TIMEOUT_STEP_TYPE, 1)
    state = await client.get_flow_state(flow_id)
    for step in state.active_step_executions:
        if step.step_type == timer.step_type:
            return StepExecutionId(step.step_type, step.execution_number)
    return None


async def skip_refund_timer(
    client: AsyncClient,
    flow_id: str,
    timer_name: str,
    timeout: timedelta = timedelta(seconds=30),
) -> StepExecutionId:
    """Skips the named Timer once it is waiting; returns the Step execution it fired in.

    Raises:
        TimerNotWaitingError: If the Timer does not wait within timeout.
    """
    timer = REFUND_TIMERS[timer_name]
    deadline = asyncio.get_running_loop().time() + timeout.total_seconds()
    last_error: Exception | None = None
    while asyncio.get_running_loop().time() < deadline:
        execution = await find_waiting_execution(client, flow_id, timer)
        if execution is not None:
            try:
                await client.skip_timer(flow_id, execution, timer.timer_id)
                return execution
            except FlowNotActiveOrNotFoundError:
                raise
            except DexServiceError as error:
                # The Step can be open before its Wait is registered.
                last_error = error
        await asyncio.sleep(SKIP_RETRY_INTERVAL_SECONDS)
    raise TimerNotWaitingError(f"{timer_name} Timer of {flow_id} is not waiting: {last_error}")


async def skip_timer_from_terminal(flow_id: str, timer_name: str) -> None:
    flow = new_refund_timers_flow()
    cache = open_cache(Path(".dex-blob-cache/refund-durable-timers-client"))
    client = new_presenter_client(flow, cache)
    try:
        execution = await skip_refund_timer(client, flow_id, timer_name)
        print(
            f"Fired {timer_name} in {execution.step_type} execution {execution.number}.",
            flush=True,
        )
    finally:
        await client.close()
        cache.close()


def main() -> None:
    parser = argparse.ArgumentParser(description="Fire one waiting refund Timer now.")
    parser.add_argument("flow_id")
    parser.add_argument("timer", choices=REFUND_TIMERS)
    arguments = parser.parse_args()
    asyncio.run(skip_timer_from_terminal(arguments.flow_id, arguments.timer))


if __name__ == "__main__":
    main()
