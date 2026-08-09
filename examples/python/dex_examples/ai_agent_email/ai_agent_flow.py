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

"""An AI agent that drafts an email, then waits durably until its send time."""

from __future__ import annotations

import os
import smtplib
import time
from dataclasses import dataclass
from datetime import timedelta

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.ai_agent_email.agent import request_email_fields

GOOGLE_EMAIL_VARIABLE = "GOOGLE_EMAIL_ADDRESS"
GOOGLE_EMAIL_PASSWORD_VARIABLE = "GOOGLE_EMAIL_APP_PASSWORD"

STATUS_INITIALIZED = "initialized"
STATUS_WAITING = "waiting"
STATUS_PROCESSING = "processing"
STATUS_SENT = "sent"
STATUS_SKIPPED = "skipped"
STATUS_CANCELED = "canceled"

AGENT_OPTIONS = StepOptions(execute_method_timeout=timedelta(seconds=90))
# Sending retries forever by default; stop after a minute so a bad app password
# surfaces instead of looping.
SENDING_OPTIONS = StepOptions(
    execute_retry=RetryPolicy(total_duration=timedelta(seconds=60))
)


@dataclass
class FlowDetails:
    """Field names match the keys the ai-agent-email web UI reads."""

    status: str | None = None
    current_request: str | None = None
    current_request_draft: str | None = None
    response_id: str | None = None
    email_recipient: str | None = None
    email_subject: str | None = None
    email_body: str | None = None
    send_time_seconds: int = 0


def google_credentials() -> tuple[str, str] | None:
    """Returns the Gmail address and app password, or None when either is unset."""
    address = os.environ.get(GOOGLE_EMAIL_VARIABLE)
    app_password = os.environ.get(GOOGLE_EMAIL_PASSWORD_VARIABLE)
    if not address or not app_password:
        return None
    return address, app_password


class Sending(Step[None]):
    def __init__(self, flow: EmailAgentFlow) -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return SENDING_OPTIONS

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        recipient = self.flow.email_recipient.get(context) or ""
        subject = self.flow.email_subject.get(context) or ""
        body = self.flow.email_body.get(context) or ""

        credentials = google_credentials()
        if credentials is None:
            print(f"no Google credentials, not sending {subject!r} to {recipient}")
            self.flow.status.set(context, STATUS_SKIPPED)
            return graceful_complete(STATUS_SKIPPED)

        sender, app_password = credentials
        smtp_server = smtplib.SMTP_SSL("smtp.gmail.com", 465)
        try:
            smtp_server.ehlo()
            smtp_server.login(sender, app_password)
            smtp_server.sendmail(sender, recipient, f"Subject: {subject}\n\n{body}")
        finally:
            smtp_server.quit()

        self.flow.status.set(context, STATUS_SENT)
        return graceful_complete(STATUS_SENT)


class Schedule(Step[None]):
    def __init__(self, flow: EmailAgentFlow, sending: Sending) -> None:
        self.flow = flow
        self.sending = sending

    def wait_for(self, context: Context, input: None) -> Wait:
        del input
        self.flow.status.set(context, STATUS_WAITING)
        send_time = self.flow.scheduled_time_seconds.get(context)
        return Wait.any_of(
            # Dex timers are durable, so a worker restart does not lose the send.
            Timer.by_duration(timedelta(seconds=max(0, send_time - int(time.time())))),
            self.flow.user_input.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        requests = self.flow.user_input.results(context)
        if not requests:
            return go_to(self.sending, None)
        # Put the message back so the agent Step handles it the usual way.
        self.flow.user_input.publish(context, requests[0])
        return go_to(self.flow.agent, None)


class Agent(Step[None]):
    def __init__(self, flow: EmailAgentFlow, schedule: Schedule) -> None:
        self.flow = flow
        self.schedule = schedule

    def get_step_options(self) -> StepOptions:
        return AGENT_OPTIONS

    def wait_for(self, context: Context, input: None) -> Wait:
        del input
        self.flow.status.set(context, STATUS_WAITING)
        return Wait.until(self.flow.user_input.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        requests = self.flow.user_input.results(context)
        if not requests:
            raise RuntimeError("no user request found")
        user_request = requests[0]
        self.flow.current_request.set(context, user_request)

        reply = request_email_fields(
            user_request,
            self.flow.previous_response_id.get(context),
        )
        if reply.response_id is not None:
            self.flow.previous_response_id.set(context, reply.response_id)

        response = reply.response
        if response.cancel_operation:
            self.flow.status.set(context, STATUS_CANCELED)
            return graceful_complete("cancel emailing")

        if response.email_send_time_unix_seconds:
            self.flow.scheduled_time_seconds.set(
                context,
                response.email_send_time_unix_seconds,
            )
        if response.email_body:
            self.flow.email_body.set(context, response.email_body)
        if response.email_subject:
            self.flow.email_subject.set(context, response.email_subject)
        if response.email_recipient:
            self.flow.email_recipient.set(context, response.email_recipient)

        if (
            self.flow.scheduled_time_seconds.get(context) > 0
            and self.flow.email_recipient.get(context)
            and self.flow.email_body.get(context)
        ):
            return go_to(self.schedule, None)
        return go_to(self, None)


class Init(Step[None]):
    def __init__(self, flow: EmailAgentFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.flow.status.set(context, STATUS_INITIALIZED)
        print(f"flow started, id: {context.flow_id}")

        if google_credentials() is None:
            print("no Google credentials, the email will be drafted but not sent")
        return go_to(self.flow.agent, None)


class EmailAgentFlow(Flow[None]):
    DA_STATUS = "Status"
    DA_CURRENT_REQUEST = "CurrentRequest"
    DA_CURRENT_REQUEST_DRAFT = "RequestDraft"
    DA_PREVIOUS_RESPONSE_ID = "PreviousResponseId"
    DA_EMAIL_RECIPIENT = "EmailRecipient"
    DA_EMAIL_SUBJECT = "EmailSubject"
    DA_EMAIL_BODY = "EmailBody"
    DA_SCHEDULED_TIME_SECONDS = "ScheduledTime"
    CH_USER_INPUT = "UserInput"

    status = Attribute(DA_STATUS, str)
    current_request = Attribute(DA_CURRENT_REQUEST, str)
    current_request_draft = Attribute(DA_CURRENT_REQUEST_DRAFT, str)
    previous_response_id = Attribute(DA_PREVIOUS_RESPONSE_ID, str)
    email_recipient = Attribute(DA_EMAIL_RECIPIENT, str)
    email_subject = Attribute(DA_EMAIL_SUBJECT, str)
    email_body = Attribute(DA_EMAIL_BODY, str)
    scheduled_time_seconds = Attribute(DA_SCHEDULED_TIME_SECONDS, int)
    user_input = Channel(CH_USER_INPUT, str)

    def __init__(self) -> None:
        self.sending = Sending(self)
        self.schedule = Schedule(self, self.sending)
        self.agent = Agent(self, self.schedule)
        self.init = Init(self)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.init).other_steps(
            self.agent,
            self.schedule,
            self.sending,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.status,
            self.current_request,
            self.current_request_draft,
            self.previous_response_id,
            self.email_recipient,
            self.email_subject,
            self.email_body,
            self.scheduled_time_seconds,
            self.user_input,
        )

    @rpc
    def send_request(self, context: Context, input: str) -> RPCResult[bool]:
        if self.status.get(context) != STATUS_WAITING:
            return RPCResult(False)
        self.current_request_draft.set(context, "")
        self.user_input.publish(context, input)
        self.status.set(context, STATUS_PROCESSING)
        return RPCResult(True)

    @rpc
    def save_draft(self, context: Context, input: str) -> None:
        self.current_request_draft.set(context, input)

    @rpc
    def describe(self, context: Context) -> RPCResult[FlowDetails]:
        return RPCResult(
            FlowDetails(
                status=self.status.get(context),
                current_request=self.current_request.get(context),
                current_request_draft=self.current_request_draft.get(context),
                response_id=self.previous_response_id.get(context),
                email_recipient=self.email_recipient.get(context),
                email_subject=self.email_subject.get(context),
                email_body=self.email_body.get(context),
                send_time_seconds=self.scheduled_time_seconds.get(context),
            )
        )
