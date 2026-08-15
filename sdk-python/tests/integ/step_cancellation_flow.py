# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import asyncio
from datetime import timedelta
from enum import Enum
from typing import Any

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepDurability,
    StepList,
    StepMovement,
    StepOptions,
    Timer,
    Wait,
    WaitForFailurePolicy,
    dead_end,
    force_fail,
    go_to,
    go_to_multi,
    graceful_complete,
)


class CancellationScenario(Enum):
    HEARTBEAT_EXECUTE = "heartbeat-execute"
    HEARTBEAT_WAIT_FOR = "heartbeat-wait-for"
    LOCAL_EXECUTE = "local-execute"
    LOCAL_TIMEOUT_FALLBACK = "local-timeout-fallback"
    NO_HEARTBEAT = "no-heartbeat"
    GLOBAL_SELECTOR = "global-selector"
    SIBLING_SELECTOR = "sibling-selector"


class CancellationStart(Step[str]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: str) -> StepDecision:
        del context, input
        scenario = self.flow.scenario
        if scenario is CancellationScenario.HEARTBEAT_WAIT_FOR:
            return go_to_multi(
                StepMovement.of(self.flow.blocking_wait_for, None),
                StepMovement.of(self.flow.winner, None),
            )
        if scenario in {
            CancellationScenario.GLOBAL_SELECTOR,
            CancellationScenario.SIBLING_SELECTOR,
        }:
            return go_to_multi(
                StepMovement.of(self.flow.first_parent, None),
                StepMovement.of(self.flow.second_parent, None),
            )
        return go_to_multi(
            StepMovement.of(self.flow.blocking_execute, None),
            StepMovement.of(self.flow.winner, None),
        )


class CancellationBlockingExecute(Step[None]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    async def execute(  # type: ignore[override]
        self, context: Context, input: None
    ) -> StepDecision:
        del input
        self.flow.blocking_invocations += 1
        self.flow.blocking_started.set()
        duration = 7 if self.flow.scenario is CancellationScenario.NO_HEARTBEAT else 10
        try:
            await asyncio.sleep(duration)
        except asyncio.CancelledError:
            self.flow.handler_canceled = True
            self.flow.context_reported_cancellation = (
                context.is_cancellation_requested()
            )
            self.flow.cancellation_observed.set()
        self.flow.late_write.set(context, "late")
        self.flow.late_handler_returned.set()
        return go_to(self.flow.recovery, None)

    def get_step_options(self) -> StepOptions:
        options = StepOptions(
            execute_method_timeout=timedelta(seconds=15),
            heartbeat_timeout=(
                timedelta(seconds=2)
                if self.flow.scenario
                in {
                    CancellationScenario.HEARTBEAT_EXECUTE,
                    CancellationScenario.LOCAL_TIMEOUT_FALLBACK,
                }
                else None
            ),
            execute_durability=(
                StepDurability.ASYNC
                if self.flow.scenario
                in {
                    CancellationScenario.LOCAL_EXECUTE,
                    CancellationScenario.LOCAL_TIMEOUT_FALLBACK,
                }
                else StepDurability.SYNC
            ),
        )
        return options.on_execute_failure_proceed_to(self.flow.recovery)


class CancellationBlockingWaitFor(Step[None]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    async def wait_for(  # type: ignore[override]
        self, context: Context, input: None
    ) -> Wait:
        del input
        self.flow.blocking_invocations += 1
        self.flow.blocking_started.set()
        try:
            await asyncio.sleep(10)
        except asyncio.CancelledError:
            self.flow.handler_canceled = True
            self.flow.context_reported_cancellation = (
                context.is_cancellation_requested()
            )
            self.flow.cancellation_observed.set()
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        self.flow.recovery_ran = True
        return force_fail("canceled wait_for execution continued")

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_method_timeout=timedelta(seconds=15),
            heartbeat_timeout=timedelta(seconds=2),
            wait_for_failure=WaitForFailurePolicy.PROCEED,
            wait_for_durability=StepDurability.SYNC,
        )


class CancellationWinner(Step[None]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        if self.flow.scenario is CancellationScenario.LOCAL_EXECUTE:
            return Wait.skip_immediately()
        return Wait.until(Timer.by_duration(timedelta(seconds=3)))

    async def execute(  # type: ignore[override]
        self, context: Context, input: None
    ) -> StepDecision:
        del context, input
        if self.flow.scenario is CancellationScenario.LOCAL_EXECUTE:
            await asyncio.wait_for(self.flow.blocking_started.wait(), timeout=10)
            await asyncio.sleep(1)
        selected: Step[Any] = self.flow.blocking_execute
        if self.flow.scenario is CancellationScenario.HEARTBEAT_WAIT_FOR:
            selected = self.flow.blocking_wait_for
        return go_to(self.flow.final, self.flow.scenario.value).with_canceling_steps(
            selected
        )


class CancellationRecovery(Step[None]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        self.flow.recovery_ran = True
        return force_fail("canceled execution reached recovery")


class CancellationFinal(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        del context, input
        return Wait.until(Timer.by_duration(timedelta(seconds=1)))

    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        return graceful_complete(input)


class CancellationFirstParent(Step[None]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return go_to_multi(
            StepMovement.of(self.flow.selector_winner, None),
            StepMovement.of(self.flow.selector_waiting, "first"),
        )


class CancellationSecondParent(Step[None]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return go_to(self.flow.selector_waiting, "second")


class CancellationSelectorWinner(Step[None]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.until(Timer.by_duration(timedelta(seconds=1)))

    async def execute(  # type: ignore[override]
        self, context: Context, input: None
    ) -> StepDecision:
        del context, input
        await asyncio.wait_for(self.flow.selector_waits_registered.wait(), timeout=10)
        decision = go_to(self.flow.final, self.flow.scenario.value)
        if self.flow.scenario is CancellationScenario.GLOBAL_SELECTOR:
            return decision.with_canceling_steps(self.flow.selector_waiting)
        return decision.with_canceling_sibling_steps(self.flow.selector_waiting)


class CancellationSelectorWaiting(Step[str]):
    def __init__(self, flow: StepCancellationFlow) -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: str) -> Wait:
        del context
        self.flow.selector_wait_count += 1
        if self.flow.selector_wait_count == 2:
            self.flow.selector_waits_registered.set()
        duration = 30 if input == "first" else 2
        return Wait.until(Timer.by_duration(timedelta(seconds=duration)))

    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        if input == "first":
            self.flow.first_selector_executed = True
        else:
            self.flow.second_selector_executed = True
        return dead_end()


class StepCancellationFlow(Flow[str]):
    def __init__(self, scenario: CancellationScenario) -> None:
        self.scenario = scenario
        self.late_write = Attribute("python-cancellation-late-write", str)
        self.blocking_started = asyncio.Event()
        self.cancellation_observed = asyncio.Event()
        self.late_handler_returned = asyncio.Event()
        self.selector_waits_registered = asyncio.Event()
        self.handler_canceled = False
        self.context_reported_cancellation = False
        self.recovery_ran = False
        self.first_selector_executed = False
        self.second_selector_executed = False
        self.blocking_invocations = 0
        self.selector_wait_count = 0
        self.recovery = CancellationRecovery(self)
        self.blocking_execute = CancellationBlockingExecute(self)
        self.blocking_wait_for = CancellationBlockingWaitFor(self)
        self.winner = CancellationWinner(self)
        self.final = CancellationFinal()
        self.first_parent = CancellationFirstParent(self)
        self.second_parent = CancellationSecondParent(self)
        self.selector_winner = CancellationSelectorWinner(self)
        self.selector_waiting = CancellationSelectorWaiting(self)
        self.start = CancellationStart(self)

    def get_flow_type(self) -> str:
        return f"PythonStepCancellation{self.scenario.name}"

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.start).other_steps(
            self.blocking_execute,
            self.blocking_wait_for,
            self.winner,
            self.recovery,
            self.final,
            self.first_parent,
            self.second_parent,
            self.selector_winner,
            self.selector_waiting,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.late_write)
