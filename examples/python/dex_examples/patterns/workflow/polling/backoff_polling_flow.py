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

from __future__ import annotations

from datetime import timedelta

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    go_to,
    graceful_complete,
)

from dex_examples.patterns.services.service_dependency import ServiceDependency


class PollingComplete(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        print(f"Executing final state to complete the workflow: ({input})")
        return graceful_complete(input)


class ReadExternalDep(Step[None]):
    def __init__(
        self,
        polling_complete: PollingComplete,
        service: ServiceDependency,
    ) -> None:
        self.polling_complete = polling_complete
        self.service = service

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=3),
                backoff_coefficient=2.0,
                maximum_interval=timedelta(seconds=60),
                maximum_attempts=5,
                total_duration=timedelta(seconds=3600),
            )
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        result = self.service.attempt_external_api_call("Read for BackoffPollingFlow")
        return go_to(self.polling_complete, result)


class BackoffPollingFlow(Flow[None]):
    def __init__(self, service: ServiceDependency) -> None:
        self.polling_complete = PollingComplete()
        self.read_external_dep = ReadExternalDep(self.polling_complete, service)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.read_external_dep).other_steps(
            self.polling_complete
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
