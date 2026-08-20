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

import time
from datetime import timedelta

from dex import (
    Attribute,
    AttributeIndex,
    Channel,
    Context,
    Flow,
    IndexType,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    dead_end,
    go_to,
    go_to_multi,
    graceful_complete,
    rpc,
)

from dex_examples.shared.my_dependency_service import MyDependencyService
from dex_examples.products.engagement.engagement_description import (
    EngagementDescription,
)
from dex_examples.products.engagement.engagement_input import EngagementInput
from dex_examples.products.engagement.status import Status

STATUS_SEARCH_KEY = "CustomKeywordField"

opt_out_reminder = Channel[None]("OptOutReminder", type(None))
complete_process = Channel[None]("CompleteProcess", type(None))


def current_time_millis() -> int:
    return int(time.time() * 1000)


class NotifyExternalSystem(Step[Status]):
    def __init__(self, flow: "EngagementFlow") -> None:
        self.flow = flow

    def execute(self, context: Context, input: Status) -> StepDecision:
        self.flow.service.update_external_system(
            f"notify engagement from employer {self.flow.employer_id.get(context)} "
            f"to job seeker {self.flow.job_seeker_id.get(context)} for status {input}"
        )
        return dead_end()


class Reminder(Step[None]):
    def __init__(self, flow: "EngagementFlow") -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(timedelta(seconds=5)),
            opt_out_reminder.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        status = self.flow.engagement_status.get(context)
        if status is not Status.INITIATED:
            return dead_end()
        if opt_out_reminder.results(context):
            self.flow.update_status(context, status, "user opted out of reminders")
            return dead_end()
        self.flow.service.send_email(
            self.flow.job_seeker_id.get(context),
            "Reminder: please respond",
            "Please respond to the engagement.",
        )
        return go_to(self, None)


class ProcessTimeout(Step[None]):
    def __init__(self, flow: "EngagementFlow") -> None:
        self.flow = flow

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(timedelta(days=60)),
            complete_process.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        description = self.flow.describe_engagement(context)
        result = "timeout"
        if description.current_status == Status.ACCEPTED:
            result = "done"
        self.flow.service.update_external_system(
            f"engagement from employer {description.employer_id} "
            f"to job seeker {description.job_seeker_id} "
            f"finished with status {description.current_status}"
        )
        return graceful_complete(result)


class Initialize(Step[EngagementInput]):
    def __init__(self, flow: "EngagementFlow") -> None:
        self.flow = flow

    def execute(self, context: Context, input: EngagementInput) -> StepDecision:
        self.flow.employer_id.set(context, input.employer_id)
        self.flow.job_seeker_id.set(context, input.job_seeker_id)
        self.flow.engagement_status.set(context, Status.INITIATED)
        self.flow.last_update_timestamp.set(context, current_time_millis())
        self.flow.notes.set(context, input.notes or "")
        return go_to_multi(
            StepMovement.of(self.flow.process_timeout, None),
            StepMovement.of(self.flow.reminder, None),
            StepMovement.of(self.flow.notify_external_system, Status.INITIATED),
        )


class EngagementFlow(Flow[EngagementInput]):
    employer_id = Attribute("EmployerId", str)
    job_seeker_id = Attribute("JobSeekerId", str)
    engagement_status = Attribute(
        "EngagementStatus",
        Status,
        AttributeIndex(IndexType.KEYWORD, STATUS_SEARCH_KEY),
    )
    last_update_timestamp = Attribute("LastUpdateTimeMillis", int)
    notes = Attribute("notes", str)

    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.initialize = Initialize(self)
        self.process_timeout = ProcessTimeout(self)
        self.reminder = Reminder(self)
        self.notify_external_system = NotifyExternalSystem(self)

    def get_steps(self) -> StepList[EngagementInput]:
        return StepList.start_step(self.initialize).other_steps(
            self.process_timeout,
            self.reminder,
            self.notify_external_system,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.employer_id,
            self.job_seeker_id,
            self.engagement_status,
            self.last_update_timestamp,
            self.notes,
            opt_out_reminder,
            complete_process,
        )

    @rpc
    def describe(self, context: Context) -> RPCResult[EngagementDescription]:
        return RPCResult(self.describe_engagement(context))

    @rpc
    def decline(self, context: Context, note: str) -> RPCResult[Status]:
        status = self.engagement_status.get(context)
        if status is not Status.INITIATED:
            raise RuntimeError(
                "can only decline an initiated engagement; "
                f'current status is "{status}"'
            )
        self.update_status(context, Status.DECLINED, note)
        return RPCResult(
            Status.DECLINED,
            next_steps=(
                StepMovement.of(self.notify_external_system, Status.DECLINED),
            ),
        )

    @rpc
    def accept(self, context: Context, note: str) -> RPCResult[Status]:
        status = self.engagement_status.get(context)
        if status is not Status.INITIATED and status is not Status.DECLINED:
            raise RuntimeError(
                "can only accept an initiated or declined engagement; "
                f'current status is "{status}"'
            )
        self.update_status(context, Status.ACCEPTED, note)
        complete_process.publish(context, None)
        return RPCResult(
            Status.ACCEPTED,
            next_steps=(
                StepMovement.of(self.notify_external_system, Status.ACCEPTED),
            ),
        )

    def describe_engagement(self, context: Context) -> EngagementDescription:
        return EngagementDescription(
            self.employer_id.get(context),
            self.job_seeker_id.get(context),
            self.notes.get(context),
            self.engagement_status.get(context),
        )

    def update_status(self, context: Context, status: Status, note: str) -> None:
        self.engagement_status.set(context, status)
        self.last_update_timestamp.set(context, current_time_millis())
        current_notes = self.notes.get(context) or ""
        if note:
            current_notes += ";" + note
        self.notes.set(context, current_notes)
