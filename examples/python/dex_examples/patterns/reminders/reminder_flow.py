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
from enum import Enum

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    force_complete,
    go_to,
    go_to_many,
    rpc,
)

from dex_examples.patterns.shared.service_dependency import ServiceDependency

PROCESS_TIMEOUT = timedelta(days=60)
REMINDER_INTERVAL = timedelta(seconds=5)


class Status(Enum):
    INITIATED = "INITIATED"
    ACCEPTED = "ACCEPTED"


class ProcessTimeout(Step[None]):
    def __init__(
        self,
        service: ServiceDependency,
        status: Attribute[str],
        complete_process: Channel[None],
    ) -> None:
        self.service = service
        self.status = status
        self.complete_process = complete_process

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(PROCESS_TIMEOUT),
            self.complete_process.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        accepted = self.status.get(context) == Status.ACCEPTED.value
        self.service.update_external_system(
            "notify for status: " + ("ACCEPTED" if accepted else "TIMEOUT")
        )
        return force_complete("done")


class Reminder(Step[None]):
    def __init__(
        self,
        service: ServiceDependency,
        status: Attribute[str],
        opt_out_reminder: Channel[None],
    ) -> None:
        self.service = service
        self.status = status
        self.opt_out_reminder = opt_out_reminder

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(REMINDER_INTERVAL),
            self.opt_out_reminder.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        if self.status.get(context) == Status.ACCEPTED.value:
            print("Reminder state timer expired, but status already ACCEPTED")
            return force_complete("done")

        if self.opt_out_reminder.results(context):
            self.service.update_external_system("user opted out - no more reminders")
            return force_complete("done - opt out")

        self.service.send_email("Reminder:xxx please respond", "Hello xxx, ...")
        return go_to(Reminder, None)


class Init(Step[None]):
    def __init__(
        self,
        process_timeout: ProcessTimeout,
        reminder: Reminder,
        status: Attribute[str],
    ) -> None:
        self.process_timeout = process_timeout
        self.reminder = reminder
        self.status = status

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.status.set(context, Status.INITIATED.value)
        return go_to_many(
            StepMovement.of(ProcessTimeout, None),
            StepMovement.of(Reminder, None),
        )


class ReminderFlow(Flow[None]):
    DA_STATUS = "Status"
    SIGNAL_NAME_OPT_OUT_REMINDER = "OptOutReminder"
    INTERNAL_CHANNEL_COMPLETE_PROCESS = "CompleteProcess"

    status = Attribute(DA_STATUS, str)
    opt_out_reminder = Channel[None](SIGNAL_NAME_OPT_OUT_REMINDER, type(None))
    complete_process = Channel[None](INTERNAL_CHANNEL_COMPLETE_PROCESS, type(None))

    def __init__(self, service: ServiceDependency) -> None:
        self.process_timeout = ProcessTimeout(
            service,
            self.status,
            self.complete_process,
        )
        self.reminder = Reminder(service, self.status, self.opt_out_reminder)
        self.init = Init(self.process_timeout, self.reminder, self.status)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.init).other_steps(
            self.process_timeout,
            self.reminder,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.status,
            self.opt_out_reminder,
            self.complete_process,
        )

    @rpc
    def accept(self, context: Context) -> None:
        if self.status.get(context) != Status.INITIATED.value:
            raise ValueError("can only accept in INITIATED status")
        self.status.set(context, Status.ACCEPTED.value)
        self.complete_process.publish(context, None)
