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
from typing import Annotated, Any, NotRequired, cast

import dex_agents_cli  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from deepagents import create_deep_agent
from deepagents.graph import DeepAgentState
from langchain.agents.middleware import AgentMiddleware, AgentState, ModelRequest, ModelResponse
from langchain.agents.middleware.todo import WRITE_TODOS_TOOL_DESCRIPTION, Todo
from langchain.chat_models import init_chat_model
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import SystemMessage
from langchain_core.tools import BaseTool, StructuredTool

from dex_agents import AgentSessionFlow
from dex_agents_cli import ConfigError, HarnessContext, LocalToolGroup, current_turn_config
from dex_agents_langgraph import DurableToolCallMiddleware, langchain_agent_session_flow

#: The Flow type of the command's sessions, before the runtime namespaces it.
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


class DeepAgentsHarness:
    """The ``dex-agents-deepagents`` command: a Deep Agents agent in the terminal.

    The agent comes from ``create_deep_agent`` and runs on the Dex LangGraph
    adapter, so every model call is a ``RunModel`` Step and every tool call
    its own tool Step. Deep Agents' own file tools and its ``task`` subagent
    tool are removed: the runtime's local tools provide files and shell
    commands with the runtime's policies, and a subagent would run its model
    and tool calls inside one tool Step, out of Dex's sight. ``write_todos``
    stays as a read-only tool that records the plan in the conversation.
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

        def build_agent() -> Any:
            return create_deep_agent(
                deep_agents_chat_model(file_model),
                tools=[write_todos_tool()],
                middleware=[
                    _WithoutFilesystemTools(),
                    _WithoutSubagents(),
                    DurableToolCallMiddleware(),
                    TurnModelMiddleware(file_model),
                ],
                # Deep Agents' own schema stores messages in a DeltaChannel, which sessions refuse.
                state_schema=cast("type[DeepAgentState]", DurableDeepAgentState),
            )

        return langchain_agent_session_flow(
            build_agent,
            flow_type=context.flow_type,
            tool_sources=context.tool_sources,
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


def merge_files(current: dict[str, Any] | None, update: dict[str, Any | None]) -> dict[str, Any]:
    """Merge a ``files`` update into the agent state; ``None`` deletes a file.

    Args:
        current: The files so far.
        update: The changed files.

    Returns:
        The merged files.
    """
    merged = dict(current or {})
    for path, file_data in update.items():
        if file_data is None:
            merged.pop(path, None)
        else:
            merged[path] = file_data
    return merged


class DurableDeepAgentState(AgentState[Any]):
    """The Deep Agents state with plain channels that an agent session can store.

    ``messages`` keeps LangChain's ``add_messages`` reducer. ``files`` holds
    what Deep Agents' summarization offloads to its in-state backend.
    """

    files: Annotated[NotRequired[dict[str, Any]], merge_files]


class TurnModelMiddleware(AgentMiddleware[Any, Any, Any]):
    """Give each model call the running turn's model and instructions."""

    def __init__(self, file_model: str) -> None:
        """Keep the model the agent was built with.

        Args:
            file_model: The configuration file's model.
        """
        super().__init__()
        self._file_model = file_model
        self._models: dict[str, BaseChatModel] = {}

    async def awrap_model_call(
        self,
        request: ModelRequest[Any],
        handler: Callable[[ModelRequest[Any]], Awaitable[ModelResponse[Any]]],
    ) -> ModelResponse[Any]:
        """Use the turn's model, and put the turn's instructions first in the system message."""
        turn = current_turn_config()
        if turn is None:
            return await handler(request)
        overrides: dict[str, Any] = {}
        if turn.model != self._file_model:
            if turn.model not in self._models:
                self._models[turn.model] = deep_agents_chat_model(turn.model)
            overrides["model"] = self._models[turn.model]
        instructions = turn.instructions or DEFAULT_INSTRUCTIONS
        existing = request.system_message.text if request.system_message is not None else ""
        overrides["system_message"] = SystemMessage(
            f"{instructions}\n\n{existing}" if existing else instructions
        )
        return await handler(request.override(**overrides))


class _WithoutFilesystemTools(AgentMiddleware[Any, Any, Any]):
    """Replace Deep Agents' file tools, whose names the runtime's local tools use."""

    @property
    def name(self) -> str:
        return "FilesystemMiddleware"


class _WithoutSubagents(AgentMiddleware[Any, Any, Any]):
    """Replace the ``task`` tool, whose subagent would run inside one tool Step."""

    @property
    def name(self) -> str:
        return "SubAgentMiddleware"
