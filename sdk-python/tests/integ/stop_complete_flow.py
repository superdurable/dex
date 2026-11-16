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

import asyncio
import threading

from dex import AsyncContext, Context, Flow, Step, StepDecision, StepList, dead_end


class StopCompleteBlockingStep(Step[None]):
    def __init__(self, flow: StopCompleteFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        self.flow.execute_started.set()
        self.flow.release_execute.wait(timeout=20)
        return dead_end()


class StopCompleteFlow(Flow[None]):
    def __init__(self) -> None:
        self.execute_started = threading.Event()
        self.release_execute = threading.Event()
        self.blocking = StopCompleteBlockingStep(self)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.blocking)


class StopCompleteAsyncBlockingStep(Step[None]):
    def __init__(self, flow: StopCompleteAsyncFlow) -> None:
        self.flow = flow

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        del context, input
        self.flow.execute_started.set()
        await asyncio.wait_for(self.flow.release_execute.wait(), timeout=20)
        return dead_end()


class StopCompleteAsyncFlow(Flow[None]):
    def __init__(self) -> None:
        self.execute_started = asyncio.Event()
        self.release_execute = asyncio.Event()
        self.blocking = StopCompleteAsyncBlockingStep(self)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.blocking)
