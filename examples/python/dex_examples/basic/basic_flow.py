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

"""The smallest end-to-end Flow: two Steps, one Attribute, one Channel, two RPCs."""

from __future__ import annotations

from datetime import timedelta

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)


class WaitForApproval(Step[int]):
    def __init__(self, approval: Channel[str]) -> None:
        self.approval = approval

    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.any_of(
            self.approval.for_one(),
            Timer.by_duration(timedelta(seconds=input)),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        approvals = self.approval.results(context)
        if approvals:
            return graceful_complete(approvals[0])
        return go_to(self, input)


class Increment(Step[int]):
    def __init__(self, wait_for_approval: WaitForApproval) -> None:
        self.wait_for_approval = wait_for_approval

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return go_to(self.wait_for_approval, input + 1)


class BasicFlow(Flow[int]):
    DA_TEST_STRING = "TestString"
    CHANNEL_APPROVAL = "Approval"

    test_string = Attribute(DA_TEST_STRING, str)
    approval = Channel(CHANNEL_APPROVAL, str)

    def __init__(self) -> None:
        self.wait_for_approval = WaitForApproval(self.approval)
        self.increment = Increment(self.wait_for_approval)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.increment).other_steps(self.wait_for_approval)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.test_string, self.approval)

    @rpc
    def append_string(self, context: Context, input: str) -> RPCResult[str]:
        current = self.test_string.get(context) or ""
        appended = f"{current}, {input}"
        self.test_string.set(context, appended)
        return RPCResult(appended)

    @rpc
    def approve(self, context: Context) -> None:
        self.approval.publish(context, "approved")
