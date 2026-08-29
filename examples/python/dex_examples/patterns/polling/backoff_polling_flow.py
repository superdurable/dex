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

from dex_examples.patterns.shared.service_dependency import ServiceDependency


class PollingStep(Step[None]):
    def __init__(
        self,
        service: ServiceDependency,
    ) -> None:
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
        result = self.service.attempt_external_api_call("Poll for BackoffPollingFlow")
        return graceful_complete(result)


class BackoffPollingFlow(Flow[None]):
    def __init__(self, service: ServiceDependency) -> None:
        self.polling_step = PollingStep(service)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.polling_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
