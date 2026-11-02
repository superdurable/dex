# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Stepwise ``ModelClient`` on LiteLLM, installed with ``dex-agents[litellm]``.

OpenAI models use the Responses API with ``store=False`` and encrypted
reasoning content, so reasoning items replay without provider-side state.
Every other provider uses streaming Chat Completions, which keeps Anthropic
thinking blocks and their signatures. Each call makes exactly one request.
"""

from __future__ import annotations

import inspect
import json
from collections.abc import AsyncIterator, Awaitable, Callable, Mapping, Sequence
from typing import Any, Literal

try:
    import litellm
except ImportError as error:  # pragma: no cover - exercised by the built-package smoke test
    raise ImportError(
        "dex_agents.litellm needs LiteLLM; install it with: pip install 'dex-agents[litellm]'"
    ) from error

from dex_agents.stepwise.model import (
    ModelInputItem,
    ModelOutputItem,
    ModelOutputWriter,
    ModelRequest,
    ModelResponse,
    ModelToolCall,
    ModelToolDefinition,
    NamedToolChoice,
    ToolChoice,
    ToolResultItem,
    UserTextItem,
)
from dex_agents.values import AgentUsage

#: ``ModelOutputItem.api`` of items from the OpenAI Responses API.
RESPONSES_API = "responses"
#: ``ModelOutputItem.api`` of items from Chat Completions.
CHAT_COMPLETIONS_API = "chat_completions"

#: Returns the provider API key for one call, or ``None`` for the Worker's environment.
ApiKeyProvider = Callable[[], str | None | Awaitable[str | None]]

# Input fields the Responses API accepts back for each output item type.
_RESPONSES_REPLAY_FIELDS: Mapping[str, tuple[str, ...]] = {
    "reasoning": ("type", "id", "summary", "content", "encrypted_content", "status"),
    "message": ("type", "id", "role", "content", "status"),
    "function_call": ("type", "id", "call_id", "name", "arguments", "status"),
}


class LiteLLMModelClient:
    """Make one LiteLLM request per ``RunModel``, with a provider-exact transcript.

    The client sends the whole input every time and keeps no conversation
    state at the provider. It stores every output item as the provider sent
    it: OpenAI reasoning items keep ``encrypted_content`` and Anthropic
    thinking blocks keep ``signature``, byte for byte. An item from another
    provider or API, after a model change, replays as its text and tool calls,
    and its reasoning is dropped. LiteLLM runs with ``num_retries=0``:
    retries are ``RunModel`` attempts.

    Provider credentials come from the Worker's environment, as LiteLLM reads
    them, such as ``OPENAI_API_KEY``, or from ``api_key_provider``. Never put
    them in Flow input or Attributes.

    Example:
        >>> model = LiteLLMModelClient("openai/gpt-5-mini")
        >>> claude = LiteLLMModelClient(
        ...     "anthropic/claude-sonnet-4-5",
        ...     request_options={"thinking": {"type": "enabled", "budget_tokens": 2048}},
        ... )
        >>> definition = AgentDefinition(name="support", instructions="Help.", model=model)
    """

    def __init__(
        self,
        model: str,
        *,
        api: Literal["responses", "chat_completions"] | None = None,
        api_base: str | None = None,
        api_key_provider: ApiKeyProvider | None = None,
        request_options: Mapping[str, Any] | None = None,
    ) -> None:
        """Create a client for one LiteLLM model.

        Args:
            model: LiteLLM model name, such as ``"openai/gpt-5-mini"`` or
                ``"anthropic/claude-sonnet-4-5"``.
            api: ``"responses"`` or ``"chat_completions"``. ``None`` picks the
                Responses API for OpenAI models and Chat Completions otherwise.
            api_base: Provider base URL, or ``None`` for LiteLLM's default.
            api_key_provider: Function that returns the API key for each call,
                or ``None`` to let LiteLLM read the Worker's environment.
            request_options: Extra LiteLLM request fields for every call, such
                as ``thinking``. ``ModelSettings.provider_options`` win over them.

        Raises:
            ValueError: If ``model`` is blank.
        """
        if not model.strip():
            raise ValueError("model must not be blank")
        self._model = model
        self._provider = _provider_name(model)
        self._api = api or (RESPONSES_API if self._provider == "openai" else CHAT_COMPLETIONS_API)
        self._api_base = api_base
        self._api_key_provider = api_key_provider
        self._request_options = dict(request_options or {})

    @property
    def model(self) -> str:
        """Return the LiteLLM model name.

        Returns:
            The model passed to the constructor.
        """
        return self._model

    @property
    def provider(self) -> str:
        """Return the provider recorded on every output item.

        Returns:
            The LiteLLM provider of the model, such as ``"openai"``.
        """
        return self._provider

    @property
    def api(self) -> str:
        """Return the provider API this client calls.

        Returns:
            ``"responses"`` or ``"chat_completions"``.
        """
        return self._api

    async def call_model(self, request: ModelRequest, output: ModelOutputWriter) -> ModelResponse:
        """Make one streaming request and return its provider-exact output items.

        Args:
            request: The whole request.
            output: Writer for text and reasoning deltas.

        Returns:
            The response with its usage.

        Raises:
            Exception: What LiteLLM raises, such as a rate limit error. The
                ``RunModel`` Step retries under its retry policy.
        """
        options = await self._common_options(request)
        if self._api == RESPONSES_API:
            return await self._call_responses(request, output, options)
        return await self._call_chat_completions(request, output, options)

    def estimate_input_tokens(self, request: ModelRequest) -> int:
        """Estimate the input tokens of a request with ``litellm.token_counter``.

        Args:
            request: The request to measure.

        Returns:
            LiteLLM's count of the neutral messages plus a quarter of the
            stored size of opaque items.
        """
        opaque_characters = sum(
            len(item.item) for item in request.input_items if isinstance(item, ModelOutputItem)
        )
        messages = _chat_messages(request, provider="", api="")
        try:
            counted = int(litellm.token_counter(model=self._model, messages=messages))
        except Exception:
            counted = sum(len(json.dumps(message)) for message in messages) // 4
        return counted + opaque_characters // 4

    async def _common_options(self, request: ModelRequest) -> dict[str, Any]:
        options: dict[str, Any] = {
            **self._request_options,
            "model": self._model,
            "stream": True,
            "num_retries": 0,
            "max_retries": 0,
        }
        if self._api_base is not None:
            options["api_base"] = self._api_base
        if self._api_key_provider is not None:
            api_key = self._api_key_provider()
            if inspect.isawaitable(api_key):
                api_key = await api_key
            if api_key is not None:
                options["api_key"] = api_key
        if request.settings.temperature is not None:
            options["temperature"] = request.settings.temperature
        return options

    async def _call_responses(
        self,
        request: ModelRequest,
        output: ModelOutputWriter,
        options: dict[str, Any],
    ) -> ModelResponse:
        reasoning: dict[str, Any] = {"summary": "auto"}
        if request.settings.reasoning_effort is not None:
            reasoning["effort"] = request.settings.reasoning_effort
        options.update(
            input=_responses_input(request.input_items, self._provider),
            instructions=request.instructions,
            include=["reasoning.encrypted_content"],
            store=False,
            reasoning=reasoning,
        )
        if request.tools:
            options["tools"] = [_responses_tool(tool) for tool in request.tools]
            options["tool_choice"] = _responses_tool_choice(request.tool_choice)
        if request.settings.max_output_tokens is not None:
            options["max_output_tokens"] = request.settings.max_output_tokens
        options.update(request.settings.provider_options)
        stream = await litellm.aresponses(**options)
        items_by_index: dict[int, dict[str, Any]] = {}
        completed: Any = None
        async for event in _aiter(stream):
            event_type = _field(event, "type")
            if event_type in ("response.output_text.delta", "response.refusal.delta"):
                output.write_text_delta(str(_field(event, "delta") or ""))
            elif event_type == "response.reasoning_summary_text.delta":
                output.write_reasoning_delta(str(_field(event, "delta") or ""))
            elif event_type == "response.output_item.done":
                items_by_index[int(_field(event, "output_index"))] = _plain(_field(event, "item"))
            elif event_type in ("response.completed", "response.incomplete"):
                completed = _field(event, "response")
            elif event_type in ("response.failed", "error"):
                raise RuntimeError(f"the Responses API stream failed: {_plain(event)}")
        if completed is None:
            raise RuntimeError("the Responses API stream ended without response.completed")
        items = [
            self._responses_output_item(items_by_index[index]) for index in sorted(items_by_index)
        ]
        tool_calls = [call for item in items for call in item.tool_calls]
        return ModelResponse(
            output_items=items,
            text="".join(item.text or "" for item in items),
            tool_calls=tool_calls,
            usage=_responses_usage(_plain(_field(completed, "usage") or {})),
            response_model=str(_field(completed, "model") or ""),
            finish_reason=str(_field(completed, "status") or ""),
        )

    def _responses_output_item(self, item: dict[str, Any]) -> ModelOutputItem:
        item_type = str(item.get("type"))
        fields = _RESPONSES_REPLAY_FIELDS.get(item_type)
        stored = item if fields is None else {key: item[key] for key in fields if key in item}
        text: str | None = None
        tool_calls: tuple[ModelToolCall, ...] = ()
        if item_type == "message":
            text = "".join(
                str(part.get("text") or part.get("refusal") or "")
                for part in item.get("content") or []
                if isinstance(part, dict)
            )
        elif item_type == "function_call":
            tool_calls = (
                ModelToolCall(
                    tool_call_id=str(item["call_id"]),
                    tool_name=str(item["name"]),
                    arguments=str(item.get("arguments") or "{}"),
                ),
            )
        return ModelOutputItem(
            provider=self._provider,
            api=RESPONSES_API,
            item=_compact_json(stored),
            text=text or None,
            tool_calls=tool_calls,
        )

    async def _call_chat_completions(
        self,
        request: ModelRequest,
        output: ModelOutputWriter,
        options: dict[str, Any],
    ) -> ModelResponse:
        options.update(
            messages=_chat_messages(request, provider=self._provider, api=CHAT_COMPLETIONS_API),
            stream_options={"include_usage": True},
        )
        if request.tools:
            options["tools"] = [_chat_tool(tool) for tool in request.tools]
            options["tool_choice"] = _chat_tool_choice(request.tool_choice)
        if request.settings.max_output_tokens is not None:
            options["max_tokens"] = request.settings.max_output_tokens
        if request.settings.reasoning_effort is not None:
            options["reasoning_effort"] = request.settings.reasoning_effort
        options.update(request.settings.provider_options)
        stream = await litellm.acompletion(**options)
        message = _ChatMessageAssembler()
        async for chunk in _aiter(stream):
            message.add_chunk(chunk, output)
        stored = message.stored_message()
        text = stored.get("content")
        tool_calls = tuple(
            ModelToolCall(
                tool_call_id=str(call["id"]),
                tool_name=str(call["function"]["name"]),
                arguments=str(call["function"]["arguments"] or "{}"),
            )
            for call in stored.get("tool_calls", [])
        )
        item = ModelOutputItem(
            provider=self._provider,
            api=CHAT_COMPLETIONS_API,
            item=_compact_json(stored),
            text=text or None,
            tool_calls=tool_calls,
        )
        return ModelResponse(
            output_items=[item],
            text=text or "",
            tool_calls=tool_calls,
            usage=_chat_usage(message.usage),
            response_model=message.model,
            finish_reason=message.finish_reason,
        )


class _ChatMessageAssembler:
    """Build one assistant message from streamed chunks, keeping thinking signatures exact."""

    def __init__(self) -> None:
        self._content: list[str] = []
        self._tool_calls: dict[int, dict[str, str]] = {}
        self._thinking_blocks: list[dict[str, Any]] = []
        self._thinking_text = ""
        self.usage: dict[str, Any] = {}
        self.model = ""
        self.finish_reason = ""

    def add_chunk(self, chunk: Any, output: ModelOutputWriter) -> None:
        usage = _field(chunk, "usage")
        if usage:
            self.usage = _plain(usage)
        self.model = str(_field(chunk, "model") or self.model)
        for choice in _field(chunk, "choices") or []:
            self.finish_reason = str(_field(choice, "finish_reason") or self.finish_reason)
            delta = _field(choice, "delta")
            content = _field(delta, "content")
            if isinstance(content, str) and content:
                self._content.append(content)
                output.write_text_delta(content)
            reasoning = _field(delta, "reasoning_content")
            if isinstance(reasoning, str):
                output.write_reasoning_delta(reasoning)
            for block in _field(delta, "thinking_blocks") or []:
                self._add_thinking_block(_plain(block))
            for call in _field(delta, "tool_calls") or []:
                self._add_tool_call_delta(call)

    def _add_thinking_block(self, block: dict[str, Any]) -> None:
        if block.get("type") == "redacted_thinking":
            self._thinking_blocks.append({"type": "redacted_thinking", "data": block["data"]})
            return
        text = str(block.get("thinking") or "")
        signature = block.get("signature")
        if not signature:
            self._thinking_text += text
            return
        # LiteLLM may repeat the whole block with its signature; never append it twice.
        if not text.startswith(self._thinking_text):
            text = self._thinking_text + text
        self._thinking_blocks.append({"type": "thinking", "thinking": text, "signature": signature})
        self._thinking_text = ""

    def _add_tool_call_delta(self, call: Any) -> None:
        current = self._tool_calls.setdefault(
            int(_field(call, "index") or 0), {"id": "", "name": "", "arguments": ""}
        )
        current["id"] += str(_field(call, "id") or "")
        function = _field(call, "function")
        current["name"] += str(_field(function, "name") or "")
        current["arguments"] += str(_field(function, "arguments") or "")

    def stored_message(self) -> dict[str, Any]:
        message: dict[str, Any] = {"role": "assistant", "content": "".join(self._content) or None}
        if self._tool_calls:
            message["tool_calls"] = [
                {
                    "id": call["id"],
                    "type": "function",
                    "function": {"name": call["name"], "arguments": call["arguments"]},
                }
                for _, call in sorted(self._tool_calls.items())
            ]
        if self._thinking_blocks:
            message["thinking_blocks"] = self._thinking_blocks
        return message


def _responses_input(items: Sequence[ModelInputItem], provider: str) -> list[dict[str, Any]]:
    converted: list[dict[str, Any]] = []
    for item in items:
        if isinstance(item, UserTextItem):
            converted.append({"type": "message", "role": "user", "content": item.text})
        elif isinstance(item, ToolResultItem):
            converted.append(
                {
                    "type": "function_call_output",
                    "call_id": item.tool_call_id,
                    "output": item.output,
                }
            )
        elif item.provider == provider and item.api == RESPONSES_API:
            converted.append(json.loads(item.item))
        else:
            if item.text:
                converted.append({"type": "message", "role": "assistant", "content": item.text})
            converted.extend(
                {
                    "type": "function_call",
                    "call_id": call.tool_call_id,
                    "name": call.tool_name,
                    "arguments": call.arguments,
                }
                for call in item.tool_calls
            )
    return converted


def _chat_messages(request: ModelRequest, *, provider: str, api: str) -> list[dict[str, Any]]:
    messages: list[dict[str, Any]] = []
    if request.instructions:
        messages.append({"role": "system", "content": request.instructions})
    pending_outputs: list[ModelOutputItem] = []
    for item in [*request.input_items, None]:
        if isinstance(item, ModelOutputItem):
            pending_outputs.append(item)
            continue
        if pending_outputs:
            messages.append(_chat_assistant_message(pending_outputs, provider, api))
            pending_outputs = []
        if isinstance(item, UserTextItem):
            messages.append({"role": "user", "content": item.text})
        elif isinstance(item, ToolResultItem):
            messages.append(
                {"role": "tool", "tool_call_id": item.tool_call_id, "content": item.output}
            )
    return messages


def _chat_assistant_message(
    outputs: Sequence[ModelOutputItem], provider: str, api: str
) -> dict[str, Any]:
    if len(outputs) == 1 and outputs[0].provider == provider and outputs[0].api == api:
        replayed: dict[str, Any] = json.loads(outputs[0].item)
        return replayed
    # Items from another provider or API replay as one neutral message; reasoning is dropped.
    message: dict[str, Any] = {
        "role": "assistant",
        "content": "".join(item.text or "" for item in outputs) or None,
    }
    tool_calls = [call for item in outputs for call in item.tool_calls]
    if tool_calls:
        message["tool_calls"] = [
            {
                "id": call.tool_call_id,
                "type": "function",
                "function": {"name": call.tool_name, "arguments": call.arguments},
            }
            for call in tool_calls
        ]
    return message


def _responses_tool(tool: ModelToolDefinition) -> dict[str, Any]:
    return {
        "type": "function",
        "name": tool.name,
        "description": tool.description,
        "parameters": dict(tool.parameters),
        "strict": False,
    }


def _chat_tool(tool: ModelToolDefinition) -> dict[str, Any]:
    return {
        "type": "function",
        "function": {
            "name": tool.name,
            "description": tool.description,
            "parameters": dict(tool.parameters),
        },
    }


def _responses_tool_choice(choice: ToolChoice) -> str | dict[str, str]:
    if isinstance(choice, NamedToolChoice):
        return {"type": "function", "name": choice.tool_name}
    return choice


def _chat_tool_choice(choice: ToolChoice) -> str | dict[str, Any]:
    if isinstance(choice, NamedToolChoice):
        return {"type": "function", "function": {"name": choice.tool_name}}
    return choice


def _responses_usage(usage: Mapping[str, Any]) -> AgentUsage:
    return AgentUsage(
        model_calls=1,
        input_tokens=int(usage.get("input_tokens") or 0),
        output_tokens=int(usage.get("output_tokens") or 0),
        cached_input_tokens=int(
            (usage.get("input_tokens_details") or {}).get("cached_tokens") or 0
        ),
        reasoning_tokens=int(
            (usage.get("output_tokens_details") or {}).get("reasoning_tokens") or 0
        ),
    )


def _chat_usage(usage: Mapping[str, Any]) -> AgentUsage:
    return AgentUsage(
        model_calls=1,
        input_tokens=int(usage.get("prompt_tokens") or 0),
        output_tokens=int(usage.get("completion_tokens") or 0),
        cached_input_tokens=int(
            (usage.get("prompt_tokens_details") or {}).get("cached_tokens") or 0
        ),
        reasoning_tokens=int(
            (usage.get("completion_tokens_details") or {}).get("reasoning_tokens") or 0
        ),
    )


def _provider_name(model: str) -> str:
    try:
        return str(litellm.get_llm_provider(model)[1])
    except Exception:
        return model.split("/", 1)[0] if "/" in model else "openai"


async def _aiter(stream: Any) -> AsyncIterator[Any]:
    async for event in stream:
        yield event


def _field(value: Any, name: str) -> Any:
    if isinstance(value, Mapping):
        return value.get(name)
    return getattr(value, name, None)


def _plain(value: Any) -> dict[str, Any]:
    if isinstance(value, Mapping):
        return dict(value)
    model_dump = getattr(value, "model_dump", None)
    if callable(model_dump):
        dumped: dict[str, Any] = model_dump(mode="json", exclude_none=True)
        return dumped
    return dict(vars(value))


def _compact_json(value: Mapping[str, Any]) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))
