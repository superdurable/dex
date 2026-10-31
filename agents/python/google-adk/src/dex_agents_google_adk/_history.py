# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""ADK session events stored as agent messages, and ADK sessions rebuilt from them."""

from __future__ import annotations

import json
from collections.abc import Iterable, Mapping, Sequence
from typing import Any

from google.adk.events import Event
from google.adk.flows.llm_flows.functions import REQUEST_CONFIRMATION_FUNCTION_CALL_NAME

# ADK's Runner applies this when it appends a user message; replays must match it.
from google.adk.runners import _find_active_task_scope
from google.adk.sessions import Session
from google.genai import types

from dex_agents import AgentMessage, FrameworkMessage, MessageRole


def serialize_adk_event(event: Event) -> str:
    return event.model_dump_json(exclude_none=True)


def deserialize_adk_event(framework_item: str) -> Event:
    return Event.model_validate_json(framework_item)


def adk_event_to_json_object(event: Event) -> dict[str, Any]:
    decoded: dict[str, Any] = json.loads(serialize_adk_event(event))
    return decoded


def adk_event_from_json_object(value: Mapping[str, Any]) -> Event:
    return Event.model_validate(value)


def is_adk_event_kept_after_turn(event: Event) -> bool:
    """Report whether a completed turn stores the event.

    Returns:
        ``False`` only for an event without content, output, or actions other
        than the resumability markers that matter only inside its invocation.
    """
    if event.content is not None and event.content.parts:
        return True
    if event.output is not None:
        return True
    actions = event.actions.model_dump(exclude_none=True, exclude={"end_of_agent", "agent_state"})
    return any(actions.values())


def without_confirmation_plumbing(events: Sequence[Event]) -> list[Event]:
    """Return a completed turn's events as if ADK had run each tool call directly.

    Every tool call of a turn pauses on an ``adk_request_confirmation`` call.
    Once the turn completes, the requests, their answers, and the placeholder
    responses only repeat what the recorded tool responses say, so the stored
    history leaves them out. ADK hides them from the model in any case.
    """
    kept_events: list[Event] = []
    for event in events:
        if event.content is None or not event.content.parts:
            kept_events.append(event)
            continue
        placeholder_ids = set(event.actions.requested_tool_confirmations)
        remaining_parts = [
            part
            for part in event.content.parts
            if not _is_confirmation_part(part)
            and not (part.function_response and part.function_response.id in placeholder_ids)
        ]
        if len(remaining_parts) == len(event.content.parts) and not placeholder_ids:
            kept_events.append(event)
            continue
        stripped = event.model_copy(
            update={
                "content": event.content.model_copy(update={"parts": remaining_parts})
                if remaining_parts
                else None,
                "actions": event.actions.model_copy(
                    update={"requested_tool_confirmations": {}, "skip_summarization": None}
                ),
                "long_running_tool_ids": None,
            }
        )
        if is_adk_event_kept_after_turn(stripped):
            kept_events.append(stripped)
    return kept_events


def _is_confirmation_part(part: types.Part) -> bool:
    call_name = part.function_call.name if part.function_call else None
    response_name = part.function_response.name if part.function_response else None
    return REQUEST_CONFIRMATION_FUNCTION_CALL_NAME in (call_name, response_name)


def framework_message_for_adk_event(event: Event) -> FrameworkMessage:
    """Project one ADK event onto the stored message fields a UI reads."""
    function_calls = event.get_function_calls()
    function_responses = event.get_function_responses()
    if function_responses:
        role = MessageRole.TOOL
    elif event.author == "user":
        role = MessageRole.USER
    else:
        role = MessageRole.ASSISTANT
    tool_call_id: str | None = None
    tool_name: str | None = None
    tool_arguments: str | None = None
    tool_output: str | None = None
    if len(function_calls) == 1 and not function_responses:
        call = function_calls[0]
        tool_call_id, tool_name = call.id, call.name
        tool_arguments = json.dumps(call.args or {}, ensure_ascii=False)
    elif len(function_responses) == 1 and not function_calls:
        response = function_responses[0]
        tool_call_id, tool_name = response.id, response.name
        tool_output = display_text_of_tool_response(response.response or {})
    return FrameworkMessage(
        role=role,
        framework_item=serialize_adk_event(event),
        text=visible_text_of_adk_event(event) or None,
        tool_call_id=tool_call_id,
        tool_name=tool_name,
        tool_arguments=tool_arguments,
        tool_output=tool_output,
    )


def visible_text_of_adk_event(event: Event) -> str:
    """Return the event's non-thought text parts joined, or an empty string."""
    if event.content is None:
        return ""
    return "".join(
        part.text for part in event.content.parts or [] if part.text and not part.thought
    )


def display_text_of_tool_response(response: Mapping[str, Any]) -> str:
    """Return a bare ``result`` string as is, and any other response as JSON text."""
    result = response.get("result")
    if len(response) == 1 and isinstance(result, str):
        return result
    return json.dumps(dict(response), ensure_ascii=False, default=str)


def stored_adk_events(messages: Sequence[AgentMessage]) -> list[Event]:
    """Decode stored messages into ADK events, as ADK itself appended them.

    A user message that ``build_user_message`` stored takes the ADK invocation
    ID of the next event of its turn, which ADK assigned when it appended its
    own copy of the message.
    """
    events = [deserialize_adk_event(message.framework_item) for message in messages]
    for index, message in enumerate(messages):
        if message.role is not MessageRole.USER:
            continue
        later_event = next(
            (
                events[later_index]
                for later_index in range(index + 1, len(messages))
                if messages[later_index].turn_id == message.turn_id
            ),
            None,
        )
        if later_event is not None and later_event.invocation_id:
            events[index] = events[index].model_copy(
                update={"invocation_id": later_event.invocation_id}
            )
    return events


def build_adk_session(
    *,
    app_name: str,
    user_id: str,
    session_id: str,
    events: Iterable[Event],
) -> Session:
    """Rebuild an ADK session by replaying stored events and their state deltas.

    A user text event without an isolation scope gets the scope of the task
    agent that was waiting for the user, as ADK's Runner stamps it on append.
    """
    session = Session(id=session_id, app_name=app_name, user_id=user_id)
    for event in events:
        if _is_user_text_event(event) and event.isolation_scope is None:
            active_task_scope = _find_active_task_scope(session)
            if active_task_scope is not None:
                event = event.model_copy(update={"isolation_scope": active_task_scope[0]})
        session.state.update(event.actions.state_delta)
        session.events.append(event)
    if session.events:
        session.last_update_time = session.events[-1].timestamp
    return session


def _is_user_text_event(event: Event) -> bool:
    return event.author == "user" and not event.get_function_responses()
