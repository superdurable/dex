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
# See the License for the applicable language governing permissions and
# limitations under the License.

"""Minimal Channel Flow: publish on an RPC and wait in a Step."""

from __future__ import annotations

from datetime import timedelta

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

class ChannelWaitStep(Step[int]):
    def __init__(self, approval: Channel[str]) -> None:
        self.approval = approval

    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.any_of(
            self.approval.for_one(),
            Timer.by_duration(timedelta(seconds=input)),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        if context.has_timer_fired():
            return graceful_complete("approval timed out")
        approvals = self.approval.results(context)
        return graceful_complete(approvals[0])


class ChannelFlow(Flow[int]):
    approval = Channel("Approval", str)
    queued = Channel("Queued", str)
    moved = Channel("Moved", str)

    def __init__(self) -> None:
        self.wait_for_approval = ChannelWaitStep(self.approval)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.wait_for_approval)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.approval, self.queued, self.moved)

    @rpc
    def approve(self, context: Context) -> None:
        self.approval.publish(context, "approved")

    @rpc(is_transactional=True)
    def move(self, context: Context, input: str) -> None:
        self.queued.delete(context, input)
        self.moved.publish(context, "moved")
