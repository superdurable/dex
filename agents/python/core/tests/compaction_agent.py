# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Stepwise agent with compaction and a scripted model, driven by the user text.

The model reads the turn's command: the last user text, or, once compaction
summarized it, the last command the summary lists.

* ``pad <characters> <tag>`` answers ``<tag>:`` and that many characters of padding.
* ``chain <count> [tag]`` calls ``lookup_order`` once per model call until it has
  ``<count>`` results, then answers ``chain done: <count> lookups``.
* ``plan <task>`` calls ``write_todos`` with one task in progress, then answers ``planned``.

Like a provider, the model refuses a request whose input exceeds the context window.

A summary call answers ``commands: <c1> || <c2> ...`` with the newest ten
commands and ``tool results: <n>``, folding in the previous summary, then
``COMPACTION_AGENT_SUMMARY_PAD_CHARACTERS`` characters of padding, so its size
stays bounded like a real summary's. A ``pad`` answer whose tag contains
``gate-model`` waits at the ``model`` FileGate first. It waits at the ``summary`` FileGate when a
summarized command contains ``gate-summary``, and raises while the file
``fail-summary`` exists in the state directory.

Every model call is appended to ``model-requests.jsonl`` before it answers.
``COMPACTION_AGENT_WINDOW_TOKENS`` sets the context window; the scripted model
counts four characters per token. ``COMPACTION_AGENT_MESSAGE_WINDOW_SIZE``,
``COMPACTION_AGENT_MESSAGE_CHUNK_SIZE``,
``COMPACTION_AGENT_MESSAGE_CHUNK_MAX_BYTES``, and
``COMPACTION_AGENT_RETAINED_MESSAGE_LIMIT`` (``none`` keeps every message) set
the message storage options.
"""

from __future__ import annotations

import json
import os
from collections.abc import Sequence
from dataclasses import replace
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentSessionFlow,
    AgentTracingOptions,
    BuiltInTool,
    CompactionPolicy,
    ContextSummaryRequest,
    ContextSummaryResult,
    ModelCallMetadata,
    ToolEffect,
)
from dex_agents.stepwise import (
    CONTEXT_SUMMARY_INSTRUCTIONS,
    CONTEXT_SUMMARY_NOTE_HEADER,
    AgentDefinition,
    ModelInputItem,
    ModelRequest,
    StepwiseAgentRunner,
    ToolResultItem,
    UserTextItem,
    function_tool,
    stepwise_agent_session_options,
)
from dex_agents.testing import (
    FileGate,
    InvocationLog,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
)
from tests.file_spend_limit import FileSpendLimit
from tests.scripted_agent import SPEND_LIMIT_ENVIRONMENT_VARIABLE

COMPACTION_AGENT_FLOW_TYPE = "CompactionScriptedAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "COMPACTION_AGENT_STATE_DIR"
WINDOW_TOKENS_ENVIRONMENT_VARIABLE = "COMPACTION_AGENT_WINDOW_TOKENS"
SUMMARY_PAD_ENVIRONMENT_VARIABLE = "COMPACTION_AGENT_SUMMARY_PAD_CHARACTERS"
MESSAGE_WINDOW_SIZE_ENVIRONMENT_VARIABLE = "COMPACTION_AGENT_MESSAGE_WINDOW_SIZE"
MESSAGE_CHUNK_SIZE_ENVIRONMENT_VARIABLE = "COMPACTION_AGENT_MESSAGE_CHUNK_SIZE"
MESSAGE_CHUNK_MAX_BYTES_ENVIRONMENT_VARIABLE = "COMPACTION_AGENT_MESSAGE_CHUNK_MAX_BYTES"
RETAINED_MESSAGE_LIMIT_ENVIRONMENT_VARIABLE = "COMPACTION_AGENT_RETAINED_MESSAGE_LIMIT"
MODEL_GATE_NAME = "model"
MODEL_GATE_TAG = "gate-model"
DEFAULT_WINDOW_TOKENS = 600
MODEL_REQUEST_LOG_NAME = "model-requests.jsonl"
INVOCATION_LOG_NAME = "invocations.log"
FAIL_SUMMARY_FILE_NAME = "fail-summary"
SUMMARY_GATE_NAME = "summary"
SUMMARY_GATE_COMMAND = "gate-summary"
INSTRUCTIONS = "You are a scripted compaction test agent."
LOOKUP_DETAIL = " detail" * 30
_COMMANDS_PREFIX = "commands: "
_TOOL_RESULTS_PREFIX = "tool results: "
_SUMMARY_COMMAND_LIMIT = 10


class _SummaryMetadataRunner(StepwiseAgentRunner):
    def __init__(self, definition: AgentDefinition, metadata: ModelCallMetadata) -> None:
        super().__init__(definition)
        self._metadata = metadata

    async def summarize_context(self, request: ContextSummaryRequest) -> ContextSummaryResult:
        result = await super().summarize_context(request)
        return replace(result, model_call_metadata=self._metadata)


def build_compaction_agent_flow(
    state_dir: Path,
    window_tokens: int = DEFAULT_WINDOW_TOKENS,
    summary_pad_characters: int = 0,
    tracing: AgentTracingOptions | None = None,
    summary_model_call_metadata: ModelCallMetadata | None = None,
    message_storage: dict[str, Any] | None = None,
) -> AgentSessionFlow:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @function_tool(effect=ToolEffect.READ)
    def lookup_order(step: int) -> str:
        """Look up one order."""
        invocations.record(f"lookup_order:{step}")
        return f"order {step}:{LOOKUP_DETAIL}"

    async def respond(request: ModelRequest) -> ScriptedModelReply:
        is_summary = request.instructions == CONTEXT_SUMMARY_INSTRUCTIONS
        _log_request(state_dir, request, "summary" if is_summary else "model")
        if model.estimate_input_tokens(request) > window_tokens:
            raise RuntimeError("the request exceeds the context window")
        if is_summary:
            return await _summarize(state_dir, request, summary_pad_characters)
        commands, results, is_summary_last = _read_conversation(request.input_items)
        verb, _, rest = commands[-1].partition(" ")
        if verb == "pad":
            characters, tag = rest.split(" ", 1)
            if MODEL_GATE_TAG in tag:
                gate = FileGate(state_dir, MODEL_GATE_NAME)
                gate.mark_reached()
                await gate.wait_for_release()
            return ScriptedModelReply(text=f"{tag}:" + "x" * int(characters))
        if verb == "chain":
            # The summary is the first item, so it and the shown results count every lookup.
            done = results if is_summary_last else len(ScriptedModelClient.tool_results(request))
            if done >= int(rest.split(" ", 1)[0]):
                return ScriptedModelReply(text=f"chain done: {done} lookups")
            return ScriptedModelReply(
                tool_calls=[ScriptedToolCall(tool_name="lookup_order", arguments={"step": done})]
            )
        if verb == "plan":
            if ScriptedModelClient.tool_results(request):
                return ScriptedModelReply(text="planned")
            todos = [{"content": rest, "status": "in_progress"}]
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(tool_name=BuiltInTool.WRITE_TODOS, arguments={"todos": todos})
                ]
            )
        raise ValueError(f"unknown scripted command {commands[-1]!r}")

    model = ScriptedModelClient(respond)
    definition = AgentDefinition(
        name="compaction-test",
        instructions=INSTRUCTIONS,
        model=model,
        tools=[lookup_order],
        built_in_tools=frozenset({BuiltInTool.WRITE_TODOS}),
        compaction=CompactionPolicy(context_window_tokens=window_tokens),
    )
    options = stepwise_agent_session_options(definition)
    if tracing is not None:
        options = replace(options, tracing=tracing)
    if message_storage:
        options = replace(options, **message_storage)
    if os.environ.get(SPEND_LIMIT_ENVIRONMENT_VARIABLE):
        options = replace(options, spend_limit=FileSpendLimit(state_dir))
    runner = (
        StepwiseAgentRunner(definition)
        if summary_model_call_metadata is None
        else _SummaryMetadataRunner(definition, summary_model_call_metadata)
    )
    return AgentSessionFlow(runner, flow_type=COMPACTION_AGENT_FLOW_TYPE, options=options)


def build_compaction_agent_flow_from_environment() -> AgentSessionFlow:
    return build_compaction_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        int(os.environ.get(WINDOW_TOKENS_ENVIRONMENT_VARIABLE, DEFAULT_WINDOW_TOKENS)),
        int(os.environ.get(SUMMARY_PAD_ENVIRONMENT_VARIABLE, "0")),
        message_storage=_message_storage_from_environment(),
    )


def _message_storage_from_environment() -> dict[str, Any]:
    storage: dict[str, Any] = {}
    for option, variable in (
        ("message_window_size", MESSAGE_WINDOW_SIZE_ENVIRONMENT_VARIABLE),
        ("message_chunk_size", MESSAGE_CHUNK_SIZE_ENVIRONMENT_VARIABLE),
        ("message_chunk_max_bytes", MESSAGE_CHUNK_MAX_BYTES_ENVIRONMENT_VARIABLE),
        ("retained_message_limit", RETAINED_MESSAGE_LIMIT_ENVIRONMENT_VARIABLE),
    ):
        value = os.environ.get(variable)
        if value is not None:
            storage[option] = None if value == "none" else int(value)
    return storage


def read_model_requests(state_dir: Path) -> list[dict[str, Any]]:
    """Return every logged model call, oldest first."""
    path = state_dir / MODEL_REQUEST_LOG_NAME
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line]


async def _summarize(
    state_dir: Path, request: ModelRequest, pad_characters: int
) -> ScriptedModelReply:
    # The last item is the request to summarize, not part of the conversation.
    commands, results, _ = _read_conversation(request.input_items[:-1])
    if any(SUMMARY_GATE_COMMAND in command for command in commands):
        gate = FileGate(state_dir, SUMMARY_GATE_NAME)
        gate.mark_reached()
        await gate.wait_for_release()
    if (state_dir / FAIL_SUMMARY_FILE_NAME).exists():
        raise RuntimeError("scripted summary failure")
    newest_commands = " || ".join(commands[-_SUMMARY_COMMAND_LIMIT:])
    text = f"{_COMMANDS_PREFIX}{newest_commands}\n{_TOOL_RESULTS_PREFIX}{results}"
    return ScriptedModelReply(
        text=text + ("\npadding: " + "y" * pad_characters if pad_characters else "")
    )


def _read_conversation(items: Sequence[ModelInputItem]) -> tuple[list[str], int, bool]:
    """Return the user commands in order, the tool results so far, and whether a summary is last."""
    commands: list[str] = []
    results = 0
    is_summary_last = False
    for item in items:
        if isinstance(item, ToolResultItem):
            results += 1
        elif isinstance(item, UserTextItem) and item.text.startswith(CONTEXT_SUMMARY_NOTE_HEADER):
            _, command_line, result_line, *_ = item.text.split("\n")
            commands.extend(command_line.removeprefix(_COMMANDS_PREFIX).split(" || "))
            results += int(result_line.removeprefix(_TOOL_RESULTS_PREFIX))
            is_summary_last = True
        elif isinstance(item, UserTextItem) and not item.text.startswith("[Dex]"):
            commands.append(item.text)
            is_summary_last = False
    return commands, results, is_summary_last


def _log_request(state_dir: Path, request: ModelRequest, kind: str) -> None:
    entry = {
        "kind": kind,
        "turn_id": request.metadata.turn_id,
        "attempt": request.metadata.attempt,
        "items": [_describe_item(item) for item in request.input_items],
    }
    with (state_dir / MODEL_REQUEST_LOG_NAME).open("a") as log:
        log.write(json.dumps(entry) + "\n")


def _describe_item(item: ModelInputItem) -> list[Any]:
    if isinstance(item, UserTextItem):
        return ["user", item.text]
    if isinstance(item, ToolResultItem):
        return ["result", item.tool_call_id, item.output]
    return ["output", item.text, [call.tool_call_id for call in item.tool_calls]]
