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

import asyncio

from dex import AsyncContext, Flow, Step, StepDecision, StepList, graceful_complete


class DoWorkStep(Step[str]):
    async def execute(self, context: AsyncContext, request: str) -> StepDecision:
        await asyncio.sleep((50 + len(request) % 10 * 50) / 1000)
        return graceful_complete(request)


class ExampleSubFlow(Flow[str]):
    def __init__(self) -> None:
        self.do_work = DoWorkStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.do_work)
