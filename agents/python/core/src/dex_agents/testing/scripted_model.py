# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A model client that answers from a script, so Stepwise tests need no model key."""

from __future__ import annotations

import inspect
import json
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from dex_agents.stepwise.model import (
    ModelOutputItem,
    ModelOutputWriter,
    ModelRequest,
    ModelResponse,
    ModelToolCall,
    ToolResultItem,
    UserTextItem,
)
from dex_agents.values import AgentUsage

#: ``ModelOutputItem.provider`` of every item ``ScriptedModelClient`` returns.
SCRIPTED_MODEL_PROVIDER = "dex-scripted"
#: ``ModelOutputItem.api`` of every item ``ScriptedModelClient`` returns.
SCRIPTED_MODEL_API = "scripted"


@dataclass(frozen=True, kw_only=True)
class ScriptedToolCall:
    """Describe one tool call of a scripted model reply.

    Attributes:
        tool_name: Tool name.
        arguments: Arguments as JSON text, or an object that is encoded as JSON.
        tool_call_id: Tool call ID, or ``None`` for
            ``"<step_execution_id>-<index>"``, which is unique in the session.
    """

    tool_name: str
    arguments: str | Mapping[str, Any] = field(default_factory=dict)
    tool_call_id: str | None = None


@dataclass(frozen=True, kw_only=True)
class ScriptedModelReply:
    """Describe what a scripted model call returns.

    Attributes:
        text: Visible text, streamed as one ``text_delta``. ``""`` for none.
        tool_calls: Tool calls, in order.
        reasoning: Reasoning summary, streamed as one ``reasoning_delta`` and
            stored as a reasoning item, or ``None``.
        usage: Usage of the call.
    """

    text: str = ""
    tool_calls: Sequence[ScriptedToolCall] = ()
    reasoning: str | None = None
    usage: AgentUsage = field(
        default_factory=lambda: AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)
    )


#: The script: a sync or async function from the request to the reply.
ScriptedModelResponder = Callable[
    [ModelRequest], ScriptedModelReply | Awaitable[ScriptedModelReply]
]


class ScriptedModelClient:
    """Answer every model call from a script, for tests without a model key.

    The script is a function of the whole ``ModelRequest``, so it can read the
    user text and the tool results the runner replays. Keep it deterministic:
    a Worker that crashes mid-call reruns it in a new process. Its output
    items use ``SCRIPTED_MODEL_PROVIDER`` and ``SCRIPTED_MODEL_API``.

    Example:
        >>> def respond(request: ModelRequest) -> ScriptedModelReply:
        ...     if ScriptedModelClient.tool_results(request):
        ...         return ScriptedModelReply(text="done")
        ...     return ScriptedModelReply(
        ...         tool_calls=[ScriptedToolCall(tool_name="lookup_order",
        ...                                      arguments={"order_id": "7"})])
        >>> definition = AgentDefinition(name="test", instructions="Help.",
        ...                              model=ScriptedModelClient(respond),
        ...                              tools=[lookup_order])
    """

    def __init__(self, respond: ScriptedModelResponder) -> None:
        """Create a client around a script.

        Args:
            respond: Function that returns the reply for a request.
        """
        self._respond = respond
        self._requests: list[ModelRequest] = []

    @property
    def requests(self) -> Sequence[ModelRequest]:
        """Return every request this client received in this process.

        Returns:
            Requests in call order.
        """
        return tuple(self._requests)

    async def call_model(self, request: ModelRequest, output: ModelOutputWriter) -> ModelResponse:
        """Run the script for one request and stream its reply.

        Args:
            request: The request.
            output: Writer for text and reasoning deltas.

        Returns:
            One reasoning item, one message item, and one item per tool call,
            for the parts the reply has.
        """
        self._requests.append(request)
        reply = self._respond(request)
        if inspect.isawaitable(reply):
            reply = await reply
        items: list[ModelOutputItem] = []
        if reply.reasoning is not None:
            output.write_reasoning_delta(reply.reasoning)
            items.append(_scripted_item({"type": "reasoning", "summary": reply.reasoning}))
        if reply.text:
            output.write_text_delta(reply.text)
            items.append(_scripted_item({"type": "message", "text": reply.text}, text=reply.text))
        tool_calls: list[ModelToolCall] = []
        for index, scripted in enumerate(reply.tool_calls):
            call = ModelToolCall(
                tool_call_id=scripted.tool_call_id
                or f"{request.metadata.step_execution_id}-{index}",
                tool_name=scripted.tool_name,
                arguments=scripted.arguments
                if isinstance(scripted.arguments, str)
                else json.dumps(dict(scripted.arguments)),
            )
            tool_calls.append(call)
            items.append(
                _scripted_item(
                    {
                        "type": "function_call",
                        "call_id": call.tool_call_id,
                        "name": call.tool_name,
                        "arguments": call.arguments,
                    },
                    tool_calls=[call],
                )
            )
        return ModelResponse(
            output_items=items,
            text=reply.text,
            tool_calls=tool_calls,
            usage=reply.usage,
            response_model="scripted",
            finish_reason="tool_calls" if tool_calls else "stop",
        )

    def estimate_input_tokens(self, request: ModelRequest) -> int:
        """Estimate four characters per token over the request text.

        Args:
            request: The request.

        Returns:
            The estimate.
        """
        characters = len(request.instructions)
        for item in request.input_items:
            characters += len(item.item if isinstance(item, ModelOutputItem) else _item_text(item))
        return characters // 4

    @staticmethod
    def user_texts(request: ModelRequest) -> list[str]:
        """Return the text of every user item in a request, oldest first.

        Args:
            request: The request.

        Returns:
            User texts, including Dex notes stored as user text.
        """
        return [item.text for item in request.input_items if isinstance(item, UserTextItem)]

    @staticmethod
    def tool_results(request: ModelRequest) -> list[ToolResultItem]:
        """Return the tool results after the last user item of a request.

        Args:
            request: The request.

        Returns:
            The current turn's tool results, oldest first.
        """
        results: list[ToolResultItem] = []
        for item in request.input_items:
            if isinstance(item, UserTextItem):
                results = []
            elif isinstance(item, ToolResultItem):
                results.append(item)
        return results


def _scripted_item(
    item: Mapping[str, Any],
    *,
    text: str | None = None,
    tool_calls: Sequence[ModelToolCall] = (),
) -> ModelOutputItem:
    return ModelOutputItem(
        provider=SCRIPTED_MODEL_PROVIDER,
        api=SCRIPTED_MODEL_API,
        item=json.dumps(dict(item), separators=(",", ":")),
        text=text,
        tool_calls=tuple(tool_calls),
    )


def _item_text(item: UserTextItem | ToolResultItem) -> str:
    return item.text if isinstance(item, UserTextItem) else item.output
