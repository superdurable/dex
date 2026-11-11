# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A model scenario: scripted replies keyed by the turn's prompt and the step within the turn.

A scenario file is JSON or YAML::

    turns:
      - prompt: read the notes          # or prompt_contains: notes
        steps:
          - tool_calls: [{name: read_file, arguments: {path: notes.txt}}]
          - text: "read done: {tool_results}"

The latest user prompt picks the turn. The step is the number of model
replies after that prompt in the request, so a retried or replayed request
gets the same step again. A text may use ``{tool_results}`` (each tool result
after the prompt, as ``<tool>: <output>`` joined with ``; ``),
``{earlier_prompts}`` (the earlier prompts joined with `` | ``), and
``{model}``. A tool name matches an offered tool exactly or as its
``__<name>`` suffix, as Claude Code's ``mcp__<server>__<name>`` names. A
prompt that no turn matches falls back to ``scripted_reply``.
"""

from __future__ import annotations

import json
import threading
import uuid
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from ruamel.yaml import YAML

from dex_agents_cli_testing.scripted_model import (
    ScriptedConversation,
    ScriptedReply,
    ScriptedToolCall,
    resolve_tool_name,
    scripted_reply,
)


@dataclass(frozen=True)
class ScenarioToolCall:
    """One tool call a scenario step makes.

    Attributes:
        name: The tool name, or the suffix of an offered tool's name.
        arguments: The call's arguments.
    """

    name: str
    arguments: Mapping[str, Any]


@dataclass(frozen=True)
class ScenarioStep:
    """One model reply in a turn: text, tool calls, or both.

    Attributes:
        text: The reply text, with placeholders, or ``None``.
        tool_calls: The tool calls, in order.
    """

    text: str | None
    tool_calls: Sequence[ScenarioToolCall]


@dataclass(frozen=True)
class ScenarioTurn:
    """The replies to one prompt.

    Attributes:
        prompt: The exact prompt text, or ``None``.
        prompt_contains: A text the prompt contains, or ``None``.
        steps: The replies, in order; the last one repeats once they run out.
    """

    prompt: str | None
    prompt_contains: str | None
    steps: Sequence[ScenarioStep]

    def matches(self, prompt: str) -> bool:
        """Report whether this turn answers ``prompt``."""
        if self.prompt is not None:
            return prompt.strip() == self.prompt.strip()
        return self.prompt_contains is not None and self.prompt_contains in prompt


@dataclass(frozen=True)
class ScenarioReply:
    """A reply and where it came from.

    Attributes:
        reply: The reply.
        turn_label: The matched turn's prompt, or ``None`` for the fallback script.
        step_index: The step within the turn, or ``None`` for the fallback script.
    """

    reply: ScriptedReply
    turn_label: str | None
    step_index: int | None


@dataclass(frozen=True)
class ModelScenario:
    """A parsed scenario file.

    Attributes:
        turns: The turns, in match order.
    """

    turns: Sequence[ScenarioTurn]

    @staticmethod
    def parse(document: object) -> ModelScenario:
        """Parse a scenario document, raising ``ValueError`` for a malformed one."""
        if not isinstance(document, Mapping) or not isinstance(document.get("turns"), list):
            raise ValueError("a scenario is a mapping with a turns list")
        return ModelScenario(tuple(_parse_turn(turn) for turn in document["turns"]))

    @staticmethod
    def load(path: Path) -> ModelScenario:
        """Load a JSON or YAML scenario file."""
        text = path.read_text(encoding="utf-8")
        if path.suffix == ".json":
            return ModelScenario.parse(json.loads(text))
        return ModelScenario.parse(_plain(YAML(typ="safe").load(text)))

    def reply(self, conversation: ScriptedConversation) -> ScenarioReply:
        """Return the reply to one request."""
        prompt = conversation.prompts[-1] if conversation.prompts else ""
        for turn in self.turns:
            if turn.matches(prompt) and turn.steps:
                step_index = min(conversation.reply_count, len(turn.steps) - 1)
                reply = _step_reply(turn.steps[step_index], conversation)
                return ScenarioReply(reply, turn.prompt or turn.prompt_contains, step_index)
        return ScenarioReply(scripted_reply(conversation), None, None)


class ScenarioFile:
    """A scenario file that reloads when it changes; without a file, only the fallback script."""

    def __init__(self, path: Path | None) -> None:
        """Create the source; ``path`` is read on first use."""
        self._path = path
        self._lock = threading.Lock()
        self._loaded_mtime_ns: int | None = None
        self._scenario = ModelScenario(())

    def reply(self, conversation: ScriptedConversation) -> ScenarioReply:
        """Return the reply of the file's current scenario."""
        return self._current().reply(conversation)

    def _current(self) -> ModelScenario:
        if self._path is None:
            return self._scenario
        with self._lock:
            mtime_ns = self._path.stat().st_mtime_ns
            if mtime_ns != self._loaded_mtime_ns:
                self._scenario = ModelScenario.load(self._path)
                self._loaded_mtime_ns = mtime_ns
            return self._scenario


def _parse_turn(raw: object) -> ScenarioTurn:
    if not isinstance(raw, Mapping):
        raise ValueError("each turn is a mapping")
    prompt = raw.get("prompt")
    prompt_contains = raw.get("prompt_contains")
    if (prompt is None) == (prompt_contains is None):
        raise ValueError("each turn has exactly one of prompt and prompt_contains")
    steps = raw.get("steps")
    if not isinstance(steps, list) or not steps:
        raise ValueError(f"turn {prompt or prompt_contains!r} needs a non-empty steps list")
    return ScenarioTurn(
        prompt=None if prompt is None else str(prompt),
        prompt_contains=None if prompt_contains is None else str(prompt_contains),
        steps=tuple(_parse_step(step) for step in steps),
    )


def _parse_step(raw: object) -> ScenarioStep:
    if not isinstance(raw, Mapping):
        raise ValueError("each step is a mapping")
    text = raw.get("text")
    calls = raw.get("tool_calls") or []
    if text is None and not calls:
        raise ValueError("each step has a text or tool calls")
    return ScenarioStep(
        text=None if text is None else str(text),
        tool_calls=tuple(
            ScenarioToolCall(str(call["name"]), dict(call.get("arguments") or {})) for call in calls
        ),
    )


def _step_reply(step: ScenarioStep, conversation: ScriptedConversation) -> ScriptedReply:
    calls: list[ScriptedToolCall] = []
    for call in step.tool_calls:
        resolved = resolve_tool_name(call.name, conversation.tool_names)
        if resolved is None:
            offered = ", ".join(conversation.tool_names)
            return ScriptedReply(text=f"unbound {call.name}; offered tools: {offered}")
        calls.append(
            ScriptedToolCall(f"call_{uuid.uuid4().hex[:16]}", resolved, dict(call.arguments))
        )
    text = None if step.text is None else _fill_placeholders(step.text, conversation)
    return ScriptedReply(text=text, tool_calls=tuple(calls))


def _fill_placeholders(text: str, conversation: ScriptedConversation) -> str:
    results = "; ".join(
        f"{result.tool_name}: {result.output}" for result in conversation.tool_results
    )
    return (
        text.replace("{tool_results}", results)
        .replace("{earlier_prompts}", " | ".join(conversation.prompts[:-1]))
        .replace("{model}", conversation.model)
    )


def _plain(value: object) -> object:
    """Turn ruamel's mapping and sequence types into ``dict`` and ``list``."""
    if isinstance(value, Mapping):
        return {str(key): _plain(item) for key, item in value.items()}
    if isinstance(value, list):
        return [_plain(item) for item in value]
    return value
