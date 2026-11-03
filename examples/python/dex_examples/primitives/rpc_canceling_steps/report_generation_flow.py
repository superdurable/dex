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

"""RPC that cancels a running Step: a user stops a report while it generates."""

from __future__ import annotations

import asyncio
from datetime import timedelta

from dex import (
    AsyncContext,
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    Stream,
    dead_end,
    go_to,
    graceful_complete,
    rpc,
)

REPORT_READY = "ready"
REPORT_STOPPED = "stopped"
SECTION_COUNT = 10
SECTION_WORK = timedelta(seconds=1)

REPORT_STATUS = Attribute("ReportStatus", str)
REPORT_PROGRESS = Stream("ReportProgress", int, 1 << 20)


class GenerateReport(Step[str]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=timedelta(minutes=5),
            heartbeat_timeout=timedelta(seconds=30),
        )

    async def execute(self, context: AsyncContext, report_name: str) -> StepDecision:
        for section in range(1, SECTION_COUNT + 1):
            # A Stop cancels this task, so the await raises CancelledError.
            await asyncio.sleep(SECTION_WORK.total_seconds())
            REPORT_PROGRESS.write(context, section)
        REPORT_STATUS.set(context, REPORT_READY)
        return go_to(DeliverReport, report_name)


class DeliverReport(Step[str]):
    def execute(self, context: Context, report_name: str) -> StepDecision:
        return graceful_complete(f"{report_name} delivered")


class FinishReportStop(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        # The report can commit after stop_report read its snapshot.
        if REPORT_STATUS.get(context) == REPORT_READY:
            return dead_end()
        REPORT_STATUS.set(context, REPORT_STOPPED)
        return graceful_complete(REPORT_STOPPED)


class ReportGenerationFlow(Flow[str]):
    def __init__(self) -> None:
        self.generate_report = GenerateReport()
        self.deliver_report = DeliverReport()
        self.finish_report_stop = FinishReportStop()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.generate_report).other_steps(
            self.deliver_report, self.finish_report_stop
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(REPORT_STATUS, REPORT_PROGRESS)

    @rpc(timeout=timedelta(seconds=30))
    def stop_report(self, context: Context, input: None) -> RPCResult[bool]:
        if REPORT_STATUS.get(context) in (REPORT_READY, REPORT_STOPPED):
            return RPCResult(False)
        return RPCResult(
            True,
            next_steps=(StepMovement.of(FinishReportStop, None),),
        ).with_canceling_steps(GenerateReport)
