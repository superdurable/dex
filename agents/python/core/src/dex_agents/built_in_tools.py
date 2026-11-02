# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Built-in tools that the session runs itself, under every runner.

A built-in tool is a session feature, not a runner feature. The application
enables it in ``AgentSessionOptions.built_in_tools``. The runner offers its
definition to the model like any other tool and returns the model's call in
``ToolCallsRequested`` like any other ``DeferredToolCall``. ``RunModel`` then
routes the call to the built-in's own Step instead of a tool Step.
"""

from __future__ import annotations

import json
import math
from dataclasses import dataclass
from datetime import timedelta
from enum import StrEnum
from typing import Any


class BuiltInTool(StrEnum):
    """Name one built-in tool the session can run itself.

    Enable built-in tools in ``AgentSessionOptions.built_in_tools``. An
    enabled built-in name is reserved: no application tool may use it. A
    disabled built-in name is an ordinary tool name. The values are the tool
    names the model sees.

    Attributes:
        ASK_USER: Ask the user a question. The turn waits in
            ``AwaitQuestionAnswer`` until ``answer_question`` answers it, and
            the answer becomes the tool's result.
        DURABLE_WAIT: Suspend the turn on a Timer in ``AwaitTimer``. No model
            call, coroutine, or Worker holds the wait.
    """

    ASK_USER = "ask_user"
    DURABLE_WAIT = "durable_wait"


@dataclass(frozen=True, kw_only=True)
class BuiltInToolDefinition:
    """Describe one built-in tool to the model.

    A runner offers the definitions of the enabled built-ins, from
    ``BUILT_IN_TOOL_DEFINITIONS``, to its model like any other tool. The text
    is identical in the Python and TypeScript cores.

    Example:
        >>> definitions = [
        ...     definition
        ...     for definition in BUILT_IN_TOOL_DEFINITIONS
        ...     if definition.name in turn.enabled_built_in_tools
        ... ]
        >>> tools = [framework_tool(d.name, d.description, d.parameters) for d in definitions]

    Attributes:
        name: Tool name the model calls.
        description: Model-visible description.
        parameters_json: JSON Schema of the tool arguments, as compact JSON text.
    """

    name: BuiltInTool
    description: str
    parameters_json: str

    @property
    def parameters(self) -> dict[str, Any]:
        """Return the JSON Schema of the tool arguments as a new dictionary.

        Returns:
            A fresh copy on every call, so changing it changes nothing else.
        """
        parameters: dict[str, Any] = json.loads(self.parameters_json)
        return parameters


def _schema_text(schema: dict[str, Any]) -> str:
    return json.dumps(schema, ensure_ascii=False, separators=(",", ":"))


#: Definitions of every built-in tool, in the order runners offer them.
BUILT_IN_TOOL_DEFINITIONS: tuple[BuiltInToolDefinition, ...] = (
    BuiltInToolDefinition(
        name=BuiltInTool.ASK_USER,
        description=(
            "Ask the user one question and wait for the answer, which becomes this tool's "
            "result. Use it only when you cannot continue without information or a decision "
            "from the user. The wait can last hours or days."
        ),
        parameters_json=_schema_text(
            {
                "type": "object",
                "properties": {
                    "question": {
                        "type": "string",
                        "description": "One concise question for the user.",
                    },
                    "choices": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Answers the user can pick. Omit for a free-text answer.",
                    },
                    "allows_free_text": {
                        "type": "boolean",
                        "description": (
                            "Whether the user may answer with text that is not one of the "
                            "choices. Defaults to true."
                        ),
                    },
                },
                "required": ["question"],
                "additionalProperties": False,
            }
        ),
    ),
    BuiltInToolDefinition(
        name=BuiltInTool.DURABLE_WAIT,
        description=(
            "Pause this turn for a number of whole seconds, then continue. Nothing runs "
            "during the wait. Use it to wait for something outside the conversation, such as "
            "a job that needs time to finish."
        ),
        parameters_json=_schema_text(
            {
                "type": "object",
                "properties": {
                    "duration_seconds": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "Whole seconds to wait.",
                    },
                    "reason": {
                        "type": "string",
                        "description": "Why the turn waits, shown to the user.",
                    },
                },
                "required": ["duration_seconds", "reason"],
                "additionalProperties": False,
            }
        ),
    ),
)

#: Model-visible rejection text for a question the user declined without a message.
QUESTION_DECLINED_TOOL_RESULT = "The user declined to answer."

#: Rejection message recorded when a question's interrupt deadline passes.
QUESTION_DEADLINE_PASSED = "question deadline passed"

#: Default ``AgentSessionOptions.max_durable_wait``: 365 days.
DEFAULT_MAX_DURABLE_WAIT = timedelta(days=365)


@dataclass(frozen=True, kw_only=True)
class AskUserArguments:
    """Hold the validated arguments of one ``ask_user`` call.

    Attributes:
        question: Question text; never blank.
        choices: Answers to offer; empty for free text only.
        allows_free_text: Whether an answer outside ``choices`` is accepted.
            ``True`` whenever ``choices`` is empty.
    """

    question: str
    choices: list[str]
    allows_free_text: bool


@dataclass(frozen=True, kw_only=True)
class DurableWaitArguments:
    """Hold the validated arguments of one ``durable_wait`` call.

    Attributes:
        duration_seconds: Whole seconds to wait, at least 1.
        reason: Why the turn waits.
    """

    duration_seconds: int
    reason: str


class BuiltInToolArgumentsError(ValueError):
    """Report built-in tool arguments that the session rejects.

    ``RunModel`` records the call as ``failed`` with ``str(error)``, which the
    model sees after ``ResumeTurn``.
    """


def parse_ask_user_arguments(arguments: str) -> AskUserArguments:
    """Validate the JSON arguments of one ``ask_user`` call.

    ``null`` for ``choices`` or ``allows_free_text`` means the argument is
    absent, because providers in strict-schema mode send ``null`` for an
    omitted optional argument. ``question`` is required and must not be blank.

    Args:
        arguments: Tool arguments as JSON text.

    Returns:
        The validated arguments.

    Raises:
        BuiltInToolArgumentsError: If the arguments break the ``ask_user`` schema.
    """
    values = _parse_arguments_object(BuiltInTool.ASK_USER, arguments)
    question = values.get("question")
    if not isinstance(question, str) or not question.strip():
        raise _invalid(BuiltInTool.ASK_USER, "question must be a non-empty string")
    # Strict-schema providers send null for an omitted optional argument.
    choices = values.get("choices")
    if choices is None:
        choices = []
    if not isinstance(choices, list) or not all(
        isinstance(choice, str) and choice.strip() for choice in choices
    ):
        raise _invalid(BuiltInTool.ASK_USER, "choices must be a list of non-empty strings")
    allows_free_text = values.get("allows_free_text")
    if allows_free_text is None:
        allows_free_text = True
    if not isinstance(allows_free_text, bool):
        raise _invalid(BuiltInTool.ASK_USER, "allows_free_text must be a boolean")
    return AskUserArguments(
        question=question,
        choices=list(choices),
        allows_free_text=allows_free_text or not choices,
    )


def parse_durable_wait_arguments(
    arguments: str,
    max_durable_wait: timedelta,
) -> DurableWaitArguments:
    """Validate the JSON arguments of one ``durable_wait`` call.

    A JSON number with no fractional part, such as ``2.0``, counts as an
    integer, as JSON Schema defines it.

    Args:
        arguments: Tool arguments as JSON text.
        max_durable_wait: Longest wait the session accepts.

    Returns:
        The validated arguments.

    Raises:
        BuiltInToolArgumentsError: If the arguments break the ``durable_wait``
            schema or the duration is outside 1 through ``max_durable_wait``.
    """
    values = _parse_arguments_object(BuiltInTool.DURABLE_WAIT, arguments)
    duration = values.get("duration_seconds")
    if (
        isinstance(duration, bool)
        or not isinstance(duration, int | float)
        or not math.isfinite(duration)
        or duration != int(duration)
    ):
        raise _invalid(BuiltInTool.DURABLE_WAIT, "duration_seconds must be an integer")
    maximum_seconds = int(max_durable_wait.total_seconds())
    if not 1 <= int(duration) <= maximum_seconds:
        raise _invalid(
            BuiltInTool.DURABLE_WAIT,
            f"duration_seconds must be between 1 and {maximum_seconds}",
        )
    reason = values.get("reason")
    if not isinstance(reason, str):
        raise _invalid(BuiltInTool.DURABLE_WAIT, "reason must be a string")
    return DurableWaitArguments(duration_seconds=int(duration), reason=reason)


def build_question_answered_output(answer: str) -> str:
    """Return the tool output the model sees after the user answered ``ask_user``.

    Args:
        answer: The user's answer.

    Returns:
        ``{"status":"answered","answer":"<answer>"}`` as compact JSON text.
    """
    return _compact_json({"status": "answered", "answer": answer})


def build_durable_wait_completed_output(waited_seconds: int, reason: str) -> str:
    """Return the tool output the model sees after a ``durable_wait`` Timer ended.

    Args:
        waited_seconds: The requested ``duration_seconds``.
        reason: The requested reason.

    Returns:
        ``{"status":"completed","waited_seconds":<n>,"reason":"<reason>"}`` as
        compact JSON text.
    """
    return _compact_json(
        {"status": "completed", "waited_seconds": waited_seconds, "reason": reason}
    )


def _parse_arguments_object(tool: BuiltInTool, arguments: str) -> dict[str, Any]:
    try:
        values = json.loads(arguments)
    except json.JSONDecodeError:
        raise _invalid(tool, "arguments must be a JSON object") from None
    if not isinstance(values, dict):
        raise _invalid(tool, "arguments must be a JSON object")
    allowed = set(json.loads(_definition(tool).parameters_json)["properties"])
    unknown = sorted(set(values) - allowed)
    if unknown:
        raise _invalid(tool, f"unknown argument {unknown[0]}")
    return values


def _definition(tool: BuiltInTool) -> BuiltInToolDefinition:
    return next(definition for definition in BUILT_IN_TOOL_DEFINITIONS if definition.name is tool)


def _invalid(tool: BuiltInTool, reason: str) -> BuiltInToolArgumentsError:
    return BuiltInToolArgumentsError(f"invalid {tool} arguments: {reason}")


def _compact_json(value: dict[str, object]) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))
