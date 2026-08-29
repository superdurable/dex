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

from dex import Context, Flow, PersistenceSchema, Step, StepDecision, StepList, go_to, graceful_complete


class IterationStep(Step[str]):
    def execute(self, context: Context, page_token: str) -> StepDecision:
        del context
        next_page_token = "page-2" if not page_token else "page-3" if page_token == "page-2" else ""
        print(f"Migrating page {page_token}")
        return graceful_complete() if not next_page_token else go_to(IterationStep, next_page_token)


class IterationFlow(Flow[str]):
    def __init__(self) -> None:
        self.iteration_step = IterationStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.iteration_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
