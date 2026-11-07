# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Compaction for ADK sessions: the summary events, carried state, and prompt size."""

from __future__ import annotations

import dataclasses
import json
from collections.abc import Sequence
from typing import Any

from google.adk.agents import BaseAgent, LlmAgent
from google.adk.events import Event, EventActions
from google.adk.models import BaseLlm
from google.adk.models.registry import LLMRegistry
from google.adk.runners import _find_active_task_scope
from google.adk.tools import BaseTool
from google.genai import types

from dex_agents import (
    AgentMessage,
    ContextSummary,
    ContextSummaryRequest,
    build_context_summary_note,
    estimate_text_tokens,
)
from dex_agents_google_adk._history import (
    build_adk_session,
    deserialize_adk_event,
    stored_adk_events,
)

#: Start of the summary's last line, which carries the ADK session state and the last agent.
SUMMARY_STATE_MARKER = "[Dex] ADK session state: "

_STATE_SEPARATOR = "\n\n"


def split_carried_state(text: str) -> tuple[str, dict[str, Any]]:
    """Split a summary into the model's text and the carried ADK values."""
    prose, separator, carried = text.rpartition(_STATE_SEPARATOR + SUMMARY_STATE_MARKER)
    if not separator:
        return text, {"state": {}, "agent": None, "branch": None}
    return prose, json.loads(carried)


def without_carried_state(request: ContextSummaryRequest) -> ContextSummaryRequest:
    """Return the request with the carried values left out of the previous summary."""
    previous = request.previous_summary
    if previous is None:
        return request
    prose, _ = split_carried_state(previous.text)
    return dataclasses.replace(request, previous_summary=dataclasses.replace(previous, text=prose))


def carry_summary_state(text: str, request: ContextSummaryRequest) -> str:
    """Append the state and last agent of the previous summary and the summarized events."""
    previous = request.previous_summary
    _, carried = split_carried_state(previous.text) if previous is not None else ("", {})
    state: dict[str, Any] = dict(carried.get("state", {}))
    agent, branch = carried.get("agent"), carried.get("branch")
    for message in request.messages:
        event = deserialize_adk_event(message.framework_item)
        state.update(event.actions.state_delta)
        if event.author != "user":
            agent, branch = event.author, event.branch
    values = {"state": state, "agent": agent, "branch": branch}
    encoded = json.dumps(values, ensure_ascii=False, default=str)
    return f"{text}{_STATE_SEPARATOR}{SUMMARY_STATE_MARKER}{encoded}"


def build_summary_events(summary: ContextSummary) -> list[Event]:
    """Return the events that stand for the summarized ones in a rebuilt ADK session.

    A user event carries the note and the summarized state. An empty event by
    the agent that replied last keeps ADK routing the next message to it.
    """
    prose, carried = split_carried_state(summary.text)
    invocation_id = f"dex-context-summary-{summary.generation}"
    note = build_context_summary_note(dataclasses.replace(summary, text=prose))
    events = [
        Event(
            id=f"{invocation_id}-note",
            invocation_id=invocation_id,
            author="user",
            timestamp=0.0,
            content=types.Content(role="user", parts=[types.Part(text=note)]),
            actions=EventActions(state_delta=carried.get("state", {})),
        )
    ]
    agent = carried.get("agent")
    if isinstance(agent, str) and agent:
        events.append(
            Event(
                id=f"{invocation_id}-agent",
                invocation_id=invocation_id,
                author=agent,
                branch=carried.get("branch"),
                timestamp=0.0,
            )
        )
    return events


def has_active_task_at(messages: Sequence[AgentMessage], cutoff: int, app_name: str) -> bool:
    """Report whether a task-mode agent waits for the user at the cut after ``cutoff``."""
    summarized = [message for message in messages if message.sequence <= cutoff]
    session = build_adk_session(
        app_name=app_name, user_id="", session_id="", events=stored_adk_events(summarized)
    )
    return _find_active_task_scope(session) is not None


def resolve_summary_llm(summary_model: BaseLlm | str | None, root_agent: BaseAgent) -> BaseLlm:
    """Return the summary model, or the model of the first ``LlmAgent`` of the tree."""
    if isinstance(summary_model, BaseLlm):
        return summary_model
    if isinstance(summary_model, str):
        return LLMRegistry.new_llm(summary_model)
    for agent in _walk_agents(root_agent):
        if isinstance(agent, LlmAgent):
            return agent.canonical_model
    raise ValueError("compaction needs an LlmAgent in the tree or a summary_model")


def estimate_agent_tree_prompt_tokens(root_agent: BaseAgent) -> int:
    """Return the largest static prompt of an ``LlmAgent`` in the tree, in estimated tokens."""
    return max(
        (
            _estimate_llm_agent_prompt_tokens(agent)
            for agent in _walk_agents(root_agent)
            if isinstance(agent, LlmAgent)
        ),
        default=0,
    )


def _estimate_llm_agent_prompt_tokens(agent: LlmAgent) -> int:
    parts: list[str] = []
    for instruction in (agent.instruction, agent.global_instruction, agent.static_instruction):
        if isinstance(instruction, str):
            parts.append(instruction)
        elif isinstance(instruction, types.Content):
            parts.append(instruction.model_dump_json(exclude_none=True))
    for tool in agent.tools:
        if isinstance(tool, BaseTool):
            parts.extend((tool.name, tool.description))
            try:
                declaration = tool._get_declaration()
            except Exception:
                # Estimating must never fail a session; the name and description still count.
                declaration = None
            if declaration is not None:
                parts.append(declaration.model_dump_json(exclude_none=True))
        elif callable(tool):
            parts.extend((getattr(tool, "__name__", ""), getattr(tool, "__doc__", None) or ""))
    return sum(map(estimate_text_tokens, parts))


def _walk_agents(root_agent: BaseAgent) -> list[BaseAgent]:
    agents = [root_agent]
    for agent in agents:
        agents.extend(agent.sub_agents)
    return agents
