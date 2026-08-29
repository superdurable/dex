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
)

from dex_examples.patterns.shared.service_dependency import ServiceDependency

REMINDER_INTERVAL = timedelta(seconds=5)


class ReminderStep(Step[None]):
    def __init__(
        self,
        service: ServiceDependency,
        opt_out: Channel[None],
    ) -> None:
        self.service = service
        self.opt_out = opt_out

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.any_of(
            Timer.by_duration(REMINDER_INTERVAL),
            self.opt_out.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        if not context.has_timer_fired():
            return graceful_complete()
        self.service.send_email("Reminder: please respond", "Hello, ...")
        return go_to(ReminderStep, None)


class ReminderFlow(Flow[None]):
    OPT_OUT_CHANNEL = "OptOut"

    opt_out = Channel(OPT_OUT_CHANNEL, type(None))

    def __init__(self, service: ServiceDependency) -> None:
        self.reminder_step = ReminderStep(service, self.opt_out)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.reminder_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.opt_out)
