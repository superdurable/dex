# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The model layer of Stepwise mode: one small interface for one model call."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any, Literal, Protocol, TypeAlias

from dex_agents.values import AgentUsage


@dataclass(frozen=True, kw_only=True)
class ModelSettings:
    """Tune every model call of one Stepwise agent.

    ``None`` leaves a setting to the provider's default. ``provider_options``
    pass through to the provider request unchanged, so a client can expose a
    provider feature that Dex does not model.

    Example:
        >>> ModelSettings(temperature=0.2, max_output_tokens=4_000, reasoning_effort="low",
        ...               provider_options={"parallel_tool_calls": False})

    Attributes:
        temperature: Sampling temperature, or ``None``.
        max_output_tokens: Upper bound of output tokens per call, or ``None``.
        reasoning_effort: Provider reasoning effort, such as ``"low"``, or ``None``.
        provider_options: Extra request fields, passed through unchanged.
    """

    temperature: float | None = None
    max_output_tokens: int | None = None
    reasoning_effort: str | None = None
    provider_options: Mapping[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, kw_only=True)
class ModelToolCall:
    """Describe one tool call a model response asked for.

    Attributes:
        tool_call_id: Provider tool call ID. It is non-empty and has no ``/``.
        tool_name: Tool name the model called.
        arguments: Tool arguments as JSON text, exactly as the model wrote them.
    """

    tool_call_id: str
    tool_name: str
    arguments: str


@dataclass(frozen=True, kw_only=True)
class UserTextItem:
    """Carry user text, or a note Dex adds as user text, into a model request.

    Attributes:
        text: The text.
    """

    text: str


@dataclass(frozen=True, kw_only=True)
class ModelOutputItem:
    """Store one output item exactly as a provider returned it.

    The session stores one item per provider output item, such as an OpenAI
    reasoning item, message, or function call, or one Anthropic assistant
    message with its thinking blocks. ``item`` is the provider's JSON text,
    opaque to Dex. Its opaque fields, such as thinking signatures and
    encrypted reasoning content, are kept as the exact strings the provider
    sent. A client replays ``item`` unchanged when ``provider`` and ``api``
    match its own. Otherwise it replays ``text`` and ``tool_calls`` in a
    neutral form and drops the rest, such as reasoning.

    Attributes:
        provider: Provider that produced the item, such as ``"openai"``.
        api: Provider API that produced it, such as ``"responses"``.
        item: Provider JSON text of the item.
        text: Visible text of the item, or ``None`` when it has none.
        tool_calls: Tool calls the item asks for, in order.
    """

    provider: str
    api: str
    item: str
    text: str | None = None
    tool_calls: Sequence[ModelToolCall] = ()


@dataclass(frozen=True, kw_only=True)
class ToolResultItem:
    """Carry one tool result into a model request.

    Attributes:
        tool_call_id: The tool call this result answers.
        tool_name: Tool name of that call.
        output: Result text the model sees.
    """

    tool_call_id: str
    tool_name: str
    output: str


#: One item of a model request: user text, a stored model output item, or a tool result.
ModelInputItem: TypeAlias = UserTextItem | ModelOutputItem | ToolResultItem


@dataclass(frozen=True, kw_only=True)
class ModelToolDefinition:
    """Describe one tool to the model.

    Attributes:
        name: Tool name the model calls.
        description: Model-visible description.
        parameters: JSON Schema object of the tool arguments.
    """

    name: str
    description: str
    parameters: Mapping[str, Any]


@dataclass(frozen=True, kw_only=True)
class NamedToolChoice:
    """Force the model to call one named tool.

    Attributes:
        tool_name: Name of a tool in ``ModelRequest.tools``.
    """

    tool_name: str


#: How the model may use tools: ``"auto"``, ``"none"``, ``"required"``, or one named tool.
ToolChoice: TypeAlias = Literal["auto", "none", "required"] | NamedToolChoice


@dataclass(frozen=True, kw_only=True)
class ModelRequestMetadata:
    """Identify the ``RunModel`` attempt that makes one model request.

    It describes the request's place in the session. Tracing's
    ``dex_agents.ModelCallMetadata`` is a different type: it describes the
    model and response of a call.

    Attributes:
        flow_id: Session ID, which is the Flow ID.
        turn_id: ``"turn-<n>"`` of the running turn.
        step_execution_id: Dex Step execution ID of the ``RunModel`` Step.
        attempt: One-based ``RunModel`` attempt.
    """

    flow_id: str
    turn_id: str
    step_execution_id: str
    attempt: int


@dataclass(frozen=True, kw_only=True)
class ModelRequest:
    """Describe one model call: the whole input, the tools, and the settings.

    The runner builds it from committed session state only, so a retried
    ``RunModel`` attempt sends the same request. The client sends the whole
    input every time and uses no provider-side conversation state.

    Attributes:
        instructions: System instructions.
        input_items: The conversation in order. Every model output item with
            tool calls is followed by a result for each call.
        tools: Tools the model may call. Empty means no tools.
        tool_choice: How the model may use ``tools``.
        settings: Model settings of the agent.
        metadata: The ``RunModel`` attempt that makes the call.
    """

    instructions: str
    input_items: Sequence[ModelInputItem]
    tools: Sequence[ModelToolDefinition]
    tool_choice: ToolChoice
    settings: ModelSettings
    metadata: ModelRequestMetadata


@dataclass(frozen=True, kw_only=True)
class ModelResponse:
    """Return the result of one model call.

    Attributes:
        output_items: Every output item of the response, provider-exact and in
            order. The session stores each one.
        text: Visible text of the response, for display.
        tool_calls: Every tool call of the response, in order.
        usage: Usage of this one request.
        response_model: Model name the provider reported, or ``""``.
        finish_reason: Provider finish reason, such as ``"stop"``, or ``""``.
    """

    output_items: Sequence[ModelOutputItem]
    text: str = ""
    tool_calls: Sequence[ModelToolCall] = ()
    usage: AgentUsage = field(default_factory=AgentUsage)
    response_model: str = ""
    finish_reason: str = ""


class ModelOutputWriter(Protocol):
    """Receive the live output of one model call.

    The runner passes the turn's ``AgentEventWriter``, so each chunk becomes a
    ``text_delta`` or ``reasoning_delta`` event on ``AgentEvents``.
    """

    def write_text_delta(self, text: str) -> None:
        """Write one chunk of visible model text.

        Args:
            text: Text chunk.
        """
        ...

    def write_reasoning_delta(self, text: str) -> None:
        """Write one chunk of a provider-authored reasoning summary.

        Args:
            text: Reasoning summary chunk.
        """
        ...


class ModelClient(Protocol):
    """Make exactly one model request for one ``RunModel`` attempt.

    Implement it to connect Stepwise mode to a model provider, or use
    ``dex_agents.litellm.LiteLLMModelClient``. Every client follows four rules:

    * One request per call, with no retries of its own. Retries are
      ``RunModel`` attempts, so each one is visible in the Flow history.
    * Dex owns the conversation. Send the whole input every time and use no
      provider-side conversation state, such as ``previous_response_id``.
    * Provider-exact transcript. Return each output item as the provider sent
      it, and replay a stored item unchanged when it comes from the same
      provider and API.
    * Read provider credentials from the Worker's environment or a credential
      provider, never from Flow input or Attributes.

    Example:
        >>> class EchoModelClient:
        ...     async def call_model(self, request, output):
        ...         text = "echo"
        ...         output.write_text_delta(text)
        ...         item = ModelOutputItem(provider="echo", api="echo", item='{"text":"echo"}',
        ...                                text=text)
        ...         return ModelResponse(output_items=[item], text=text,
        ...                              usage=AgentUsage(model_calls=1))
        ...
        ...     def estimate_input_tokens(self, request):
        ...         return 0
    """

    async def call_model(self, request: ModelRequest, output: ModelOutputWriter) -> ModelResponse:
        """Make one model request and stream its output.

        Args:
            request: The whole request.
            output: Writer for text and reasoning deltas.

        Returns:
            The response, with provider-exact output items and the usage of
            this request.

        Raises:
            Exception: Any exception fails the ``RunModel`` attempt, which Dex
                retries under the Step's retry policy.
        """
        ...

    def estimate_input_tokens(self, request: ModelRequest) -> int:
        """Estimate the input size of a request without making it.

        Args:
            request: The request to measure.

        Returns:
            An estimate of the input tokens, including opaque stored items.
        """
        ...
