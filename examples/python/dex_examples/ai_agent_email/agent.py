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

"""Turns a free-form request into email fields, with OpenAI when it is configured."""

from __future__ import annotations

import os
import time
from typing import Any

from pydantic import BaseModel

OPENAI_KEY_VARIABLE = "OPENAI_API_KEY"

LOCAL_DRAFT_RECIPIENT = "demo@example.com"
LOCAL_DRAFT_SUBJECT = "Dex email agent demo"
LOCAL_DRAFT_DELAY_SECONDS = 5


class AgentResponse(BaseModel):
    email_recipient: str | None
    email_subject: str | None
    email_body: str | None
    email_send_time_unix_seconds: int | None
    cancel_operation: bool | None


class AgentReply(BaseModel):
    response_id: str | None
    response: AgentResponse


def request_email_fields(request: str, previous_response_id: str | None) -> AgentReply:
    if not os.environ.get(OPENAI_KEY_VARIABLE):
        print(f"{OPENAI_KEY_VARIABLE} is unset; drafting the email locally")
        return AgentReply(response_id=None, response=_local_draft(request))
    try:
        response = _create_response(request, previous_response_id)
    except Exception as error:
        print(f"the OpenAI request failed ({error}); drafting the email locally")
        return AgentReply(response_id=None, response=_local_draft(request))
    response_id = response.id if isinstance(response.id, str) else None
    payload = response.output[0].content[0].text
    return AgentReply(
        response_id=response_id,
        response=AgentResponse.model_validate_json(payload),
    )


def _local_draft(request: str) -> AgentResponse:
    """Returns a request-shaped draft so the example runs without OpenAI."""
    if "cancel" in request.lower():
        return AgentResponse(
            email_recipient="",
            email_subject="",
            email_body="",
            email_send_time_unix_seconds=0,
            cancel_operation=True,
        )
    return AgentResponse(
        email_recipient=LOCAL_DRAFT_RECIPIENT,
        email_subject=LOCAL_DRAFT_SUBJECT,
        email_body=request,
        email_send_time_unix_seconds=int(time.time()) + LOCAL_DRAFT_DELAY_SECONDS,
        cancel_operation=False,
    )


def _create_response(request: str, previous_response_id: str | None) -> Any:
    # Imported here so the rest of the examples run without the agent extras.
    from agents import AgentOutputSchema  # type: ignore[import-untyped]
    from agents.models.openai_responses import (  # type: ignore[import-untyped]
        Converter,
    )
    from openai import OpenAI

    return OpenAI().responses.create(
        model="gpt-4o",
        instructions=_instructions(int(time.time())),
        input=request,
        text=Converter.get_response_format(AgentOutputSchema(AgentResponse)),
        previous_response_id=previous_response_id,
    )


def _instructions(current_timestamp: int) -> str:
    return f"""
    Help prepare an email to be sent. Based on user requests, return email's subject, body, recipient
    , sending time and/or cancel_operation, if any of them available.
    The email subject or body may need to be translated if user requests to.
    The email subject and body must be complete, do not leave any place holders like [Your Name].
    The email's recipient should be in a valid email format, other wise, return empty string for that field.
    The sending time must be in unix timestamp in seconds.
    The current timestamp is {current_timestamp}.
    User may use relative time description based on today/now, you should calculate the timestamp based on the current timestamp {current_timestamp}.
    For example, tomorrow means current timestamp plus 86400,
    X seconds later means {current_timestamp} + X,
    X minutes later means {current_timestamp} + X*60,
    X hours later means {current_timestamp} + X * 3600.
    MAKE SURE the sending time is ALWAYS greater than the above provided current timestamp, if NOT, then it's wrong, you should always use current timestamp as the base.
    User may also ask to cancel the emailing operation, then return true for cancel_operation field.
    All the fields are optional.
    If there is no recipient, return empty string for the field.
    If there is no body, return empty string for the field.
    If there is no subject, return empty string for the field.
    If there is no sending time, return 0 for the field.
    If not asking to cancel emailing, return false for the field.
    """
