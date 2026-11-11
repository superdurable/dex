# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Deep Agents harness: a ``create_deep_agent`` agent on the Dex LangGraph adapter."""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Mapping
from typing import Any

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from deepagents import create_deep_agent
from langchain.agents.middleware import AgentMiddleware, ModelRequest, ModelResponse
from langchain.agents.middleware.todo import WRITE_TODOS_TOOL_DESCRIPTION, Todo
from langchain.chat_models import init_chat_model
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import SystemMessage
from langchain_core.tools import BaseTool, StructuredTool

from dex_agents import AgentSessionFlow, AgentTurnContext
from dex_agents_cli_core import ConfigError, HarnessContext, LocalToolGroup, current_turn_config
from dex_agents_langgraph import DurableToolCallMiddleware, langchain_agent_session_flow

#: The Flow type of the command's sessions, before the CLI core namespaces it.
DEEPAGENTS_CLI_FLOW_TYPE = "DexAgentsDeepAgentsCliSession"

#: The model the command uses when the configuration file sets none.
DEFAULT_DEEPAGENTS_MODEL = "openai:gpt-5.2"

#: The instructions the agent gets when the configuration sets none.
DEFAULT_INSTRUCTIONS = (
    "You are a helpful assistant. Use your tools to work with the files and commands "
    "in your workspace and with the connected services. Plan multi-step work with "
    "write_todos."
)

#: The todo tool's name, as Deep Agents and LangChain name it.
WRITE_TODOS_TOOL_NAME = "write_todos"

#: Deep Agents' subagent tool, which the session neither offers nor runs.
TASK_TOOL_NAME = "task"


class DeepAgentsHarness:
    """The ``dex-agents-deepagents`` command: a Deep Agents agent in the terminal.

    The agent comes from ``create_deep_agent`` and runs on the Dex LangGraph
    adapter, so every model call is a ``RunModel`` Step and every tool call
    its own tool Step. Deep Agents' own file tools are removed and its
    ``task`` subagent tool is excluded: the CLI core's local tools provide
    files and shell commands with the CLI core's policies, and a subagent would
    run its model and tool calls inside one tool Step, out of Dex's sight.
    ``write_todos`` stays as a read-only tool that records the plan in the
    conversation.
    """

    name = "deepagents"
    config_section = "deepagents"
    flow_type = DEEPAGENTS_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_DEEPAGENTS_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the Deep Agents session Flow from the effective configuration.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type.

        Raises:
            ConfigError: If the ``deepagents`` section has a key; it has no options yet.
        """
        validate_deepagents_section(context.harness_options)
        file_model = context.config.model
        return langchain_agent_session_flow(
            lambda: build_deep_agent(file_model),
            flow_type=context.flow_type,
            tool_sources=context.tool_sources,
            media_store=context.media_store,
            excluded_tool_names=[TASK_TOOL_NAME],
            select_turn_chat_model=TurnChatModelSelector(file_model),
            should_defer_tool_calls_before_middleware=True,
        )


def build_deep_agent(file_model: str) -> Any:
    """Build the command's Deep Agents graph.

    Deep Agents' file tools and summarization are replaced by middleware that
    adds nothing. The summarization would make an extra model call with the
    file's model inside a ``RunModel`` Step. The session Flow excludes the
    ``task`` tool.

    Args:
        file_model: The configuration file's model; each turn may override it.

    Returns:
        The compiled agent graph.
    """
    return create_deep_agent(
        deep_agents_chat_model(file_model),
        tools=[write_todos_tool()],
        middleware=[
            _WithoutFilesystemTools(),
            _WithoutSummarization(),
            DurableToolCallMiddleware(),
            TurnInstructionsMiddleware(),
        ],
    )


def validate_deepagents_section(options: Mapping[str, Any]) -> None:
    """Reject every key of the ``deepagents`` section, which has no options yet.

    Args:
        options: The section, ``{}`` when the file omits it.

    Raises:
        ConfigError: ``schema`` with the first key's JSON Pointer.
    """
    for key in sorted(options):
        escaped = key.replace("~", "~0").replace("/", "~1")
        raise ConfigError("schema", f"/deepagents/{escaped}", keyword="additionalProperties")


def deep_agents_chat_model(model: str) -> BaseChatModel:
    """Build a chat model from a ``provider:model`` ID as Deep Agents does.

    An ``openai:`` model uses the Responses API, Deep Agents' default.

    Args:
        model: The ID, such as ``openai:gpt-5.2`` or ``anthropic:claude-opus-5-5``.

    Returns:
        The chat model. Its provider reads its key from the standard environment variable.
    """
    if model.startswith("openai:"):
        return init_chat_model(model, use_responses_api=True)
    return init_chat_model(model)


def write_todos_tool() -> BaseTool:
    """Return ``write_todos``: a read-only tool whose result repeats the new plan.

    LangChain's own todo tool returns a graph ``Command`` that writes agent
    state, which a Dex tool Step cannot return. The plan lives in the
    conversation instead.
    """

    def write_todos(todos: list[Todo]) -> str:
        lines = [f"- [{todo['status']}] {todo['content']}" for todo in todos]
        return "Updated the todo list:\n" + "\n".join(lines)

    return StructuredTool.from_function(
        write_todos,
        name=WRITE_TODOS_TOOL_NAME,
        description=WRITE_TODOS_TOOL_DESCRIPTION,
        metadata={"readOnlyHint": True},
    )


class TurnChatModelSelector:
    """Pick the running turn's chat model when it differs from the configuration file's."""

    def __init__(self, file_model: str) -> None:
        """Keep the model the agent was built with.

        Args:
            file_model: The configuration file's model.
        """
        self._file_model = file_model
        self._models: dict[str, BaseChatModel] = {}

    def __call__(self, turn: AgentTurnContext) -> BaseChatModel | None:
        """Return the turn's chat model, built once per ID, or ``None`` for the file's model."""
        turn_config = current_turn_config()
        if turn_config is None or turn_config.model == self._file_model:
            return None
        if turn_config.model not in self._models:
            self._models[turn_config.model] = deep_agents_chat_model(turn_config.model)
        return self._models[turn_config.model]


class TurnInstructionsMiddleware(AgentMiddleware[Any, Any, Any]):
    """Put the running turn's instructions first in every model call's system message."""

    async def awrap_model_call(
        self,
        request: ModelRequest[Any],
        handler: Callable[[ModelRequest[Any]], Awaitable[ModelResponse[Any]]],
    ) -> ModelResponse[Any]:
        """Prepend the turn's instructions, or the default ones, to the system message."""
        turn = current_turn_config()
        if turn is None:
            return await handler(request)
        instructions = turn.instructions or DEFAULT_INSTRUCTIONS
        existing = request.system_message.text if request.system_message is not None else ""
        system_message = SystemMessage(
            f"{instructions}\n\n{existing}" if existing else instructions
        )
        return await handler(request.override(system_message=system_message))


class _WithoutFilesystemTools(AgentMiddleware[Any, Any, Any]):
    """Replace Deep Agents' file tools, whose names the CLI core's local tools use."""

    @property
    def name(self) -> str:
        return "FilesystemMiddleware"


class _WithoutSummarization(AgentMiddleware[Any, Any, Any]):
    """Replace the summarization, which calls the file's model inside a ``RunModel`` Step."""

    @property
    def name(self) -> str:
        return "SummarizationMiddleware"
