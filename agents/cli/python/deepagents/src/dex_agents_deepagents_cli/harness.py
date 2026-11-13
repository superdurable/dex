# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Deep Agents harness: a ``create_deep_agent`` agent on the Dex LangGraph adapter."""

from __future__ import annotations

import dataclasses
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

from deepagents import create_deep_agent
from langchain.agents.middleware import (
    AgentMiddleware,
    ModelRequest,
    ModelResponse,
    TodoListMiddleware,
)
from langchain.chat_models import init_chat_model
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import SystemMessage
from langchain_core.tools import BaseTool

from dex_agents import (
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    ToolCallsRequested,
    ToolEffect,
    ToolExecutionError,
    ToolPolicy,
)
from dex_agents.builtin_loop import ToolSource
from dex_agents_cli_core import (
    CONNECTORS_TOOL_SOURCE_NAME,
    ConfigError,
    EffectiveConfig,
    HarnessContext,
    LocalToolGroup,
    current_turn_config,
    is_local_tool_group_enabled,
    resolve_turn_tool_policy,
)
from dex_agents_deepagents_cli.connector_tool_names import PrefixedConnectorToolSource
from dex_agents_deepagents_cli.workspace_backend import (
    bind_artifacts_session,
    build_workspace_backend,
    default_artifacts_directory,
    remove_stale_session_artifacts,
)
from dex_agents_langgraph import (
    CompiledGraphSource,
    DurableToolCallMiddleware,
    LangGraphAgentRunner,
)

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

#: Deep Agents' subagent tool. A subagent runs inside the tool Step of its call.
TASK_TOOL_NAME = "task"

#: Deep Agents' shell tool.
EXECUTE_TOOL_NAME = "execute"


@dataclass(frozen=True)
class DeepAgentsToolRule:
    """How the command runs one of Deep Agents' own tools.

    Attributes:
        effect: The effect class of each call, which decides its retries.
        groups: The local tool groups the turn must switch on, or the tool
            is neither offered nor run.
    """

    effect: ToolEffect
    groups: frozenset[LocalToolGroup] = frozenset()

    def is_enabled(self, turn: EffectiveConfig) -> bool:
        """Report whether the turn switches on every group the tool needs."""
        return all(is_local_tool_group_enabled(turn, group) for group in self.groups)


_FILES = frozenset({LocalToolGroup.FILES})

#: The rule of every tool the agent's graph has. No tool waits for approval
#: unless ``approval.always`` names it, as in Deep Agents without ``interrupt_on``.
DEEP_AGENTS_TOOL_RULES: Mapping[str, DeepAgentsToolRule] = {
    "ls": DeepAgentsToolRule(ToolEffect.READ, _FILES),
    "read_file": DeepAgentsToolRule(ToolEffect.READ, _FILES),
    "glob": DeepAgentsToolRule(ToolEffect.READ, _FILES),
    "grep": DeepAgentsToolRule(ToolEffect.READ, _FILES),
    "write_file": DeepAgentsToolRule(ToolEffect.IDEMPOTENT_WRITE, _FILES),
    # A rerun of a replace_all edit can apply it twice.
    "edit_file": DeepAgentsToolRule(ToolEffect.NON_IDEMPOTENT_WRITE, _FILES),
    "delete": DeepAgentsToolRule(ToolEffect.IDEMPOTENT_WRITE, _FILES),
    EXECUTE_TOOL_NAME: DeepAgentsToolRule(
        ToolEffect.NON_IDEMPOTENT_WRITE, frozenset({LocalToolGroup.SHELL})
    ),
    # A subagent may use every file tool and execute, without their own Steps or approvals.
    TASK_TOOL_NAME: DeepAgentsToolRule(
        ToolEffect.NON_IDEMPOTENT_WRITE, frozenset({LocalToolGroup.FILES, LocalToolGroup.SHELL})
    ),
    WRITE_TODOS_TOOL_NAME: DeepAgentsToolRule(ToolEffect.READ),
}

#: Tools whose ``approval.always`` entry makes ``task`` wait too: a subagent calls them unseen.
_TASK_APPROVAL_TOOL_NAMES = frozenset(DEEP_AGENTS_TOOL_RULES)


class DeepAgentsHarness:
    """The ``dex-agents-deepagents`` command: a Deep Agents agent in the terminal.

    The agent comes from ``create_deep_agent`` with Deep Agents' own tools:
    the file tools and ``execute`` on the workspace, ``write_todos``, and the
    ``task`` subagent tool. The CLI core's connector, web fetch, and browser
    tools come on top. The agent runs on the Dex LangGraph adapter, so every
    model call is a ``RunModel`` Step and every tool call its own tool Step,
    which runs Deep Agents' tool middleware once. As in Deep Agents without
    ``interrupt_on``, its own tools run without approval unless
    ``approval.always`` names them; connector writes always wait for one.

    The command declares no reasoning effort values, because LangChain chat
    models have no provider-neutral effort option. A ``reasoning_effort``
    session setting fails the turn with ``unsupported_reasoning_effort``.
    ``suggested_models`` lists OpenAI and Anthropic models for the terminal
    UI's ``/model``.

    .. versionchanged:: 0.6.2
       The suggested models.
    """

    name = "deepagents"
    config_section = "deepagents"
    flow_type = DEEPAGENTS_CLI_FLOW_TYPE
    default_model: str | None = DEFAULT_DEEPAGENTS_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset(
        {LocalToolGroup.FILES, LocalToolGroup.SHELL}
    )
    suggested_models = (
        "openai:gpt-5.2",
        "openai:gpt-5-mini",
        "anthropic:claude-opus-5-5",
        "anthropic:claude-sonnet-5-5",
    )

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        """Build the Deep Agents session Flow from the effective configuration.

        Args:
            context: The configuration, workspace, and tool sources.

        Returns:
            The Flow, with ``context.flow_type`` as its type.

        It first deletes the artifacts of sessions that changed none of
        them within ``SESSION_ARTIFACTS_RETENTION``.

        Raises:
            ConfigError: If the ``deepagents`` section has a key; it has no options yet.
        """
        validate_deepagents_section(context.harness_options)
        remove_stale_session_artifacts(default_artifacts_directory())
        file_model = context.config.model
        workspace = context.workspace
        runner = DeepAgentsAgentRunner(
            context,
            lambda: build_deep_agent(file_model, workspace),
            tool_sources=deep_agents_tool_sources(context.tool_sources),
            select_turn_chat_model=TurnChatModelSelector(file_model),
        )
        return AgentSessionFlow(
            runner,
            flow_type=context.flow_type,
            options=AgentSessionOptions(
                summary_attributes_indexed=context.is_session_summary_indexed
            ),
        )


def build_deep_agent(file_model: str, workspace: Path) -> Any:
    """Build the command's Deep Agents graph.

    Deep Agents' file tools and ``execute`` work on the workspace through
    ``build_workspace_backend``. LangChain's ``TodoListMiddleware`` adds
    ``write_todos``. The summarization is replaced by middleware that adds
    nothing: it would make an extra model call with the file's model inside a
    ``RunModel`` Step.

    Args:
        file_model: The configuration file's model; each turn may override it.
            A subagent of ``task`` always uses it.
        workspace: The absolute workspace directory.

    Returns:
        The compiled agent graph.
    """
    middleware: list[AgentMiddleware[Any, Any, Any]] = [
        TodoListMiddleware(),
        DurableToolCallMiddleware(),
        TurnInstructionsMiddleware(workspace),
        LocalToolSwitchesMiddleware(),
        _WithoutSummarization(),
    ]
    return create_deep_agent(
        deep_agents_chat_model(file_model),
        backend=build_workspace_backend(workspace),
        middleware=middleware,
    )


def deep_agents_tool_sources(tool_sources: Sequence[ToolSource]) -> list[ToolSource]:
    """Return the CLI core's tool sources, with connector tools that fit beside Deep Agents'.

    Args:
        tool_sources: ``HarnessContext.tool_sources``.

    Returns:
        The sources; the connectors source prefixes a tool whose name a Deep
        Agents tool uses and asks approval for every write.
    """
    return [
        PrefixedConnectorToolSource(source, DEEP_AGENTS_TOOL_RULES.keys())
        if source.source_name == CONNECTORS_TOOL_SOURCE_NAME
        else source
        for source in tool_sources
    ]


class DeepAgentsAgentRunner(LangGraphAgentRunner):
    """The LangGraph runner with the CLI core's per-turn rules for Deep Agents' own tools.

    Each call of a Deep Agents tool gets its effect from
    ``DEEP_AGENTS_TOOL_RULES`` and waits for approval only when the turn's
    ``approval.always`` names it. A call of a tool whose local tool group the
    turn switches off runs at once and its tool Step refuses it.
    """

    def __init__(
        self,
        context: HarnessContext,
        graph: CompiledGraphSource,
        *,
        tool_sources: Sequence[ToolSource],
        select_turn_chat_model: Callable[[AgentTurnContext], BaseChatModel | None],
    ) -> None:
        """Create the runner, deferring every tool call before Deep Agents' middleware.

        Args:
            context: The harness context, for each turn's configuration.
            graph: The graph, or the function that builds it on the Worker.
            tool_sources: The connector and local tool sources.
            select_turn_chat_model: Picks each turn's chat model.
        """
        super().__init__(
            graph,
            tool_sources=tool_sources,
            media_store=context.media_store,
            select_turn_chat_model=select_turn_chat_model,
            should_defer_tool_calls_before_middleware=True,
        )
        self._context = context

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Run one model call and give each Deep Agents tool call the turn's policy."""
        with bind_artifacts_session(turn.flow_id):
            outcome = await super().run_model_step(turn)
        if not isinstance(outcome, ToolCallsRequested):
            return outcome
        turn_config = self._context.resolve_turn(turn.session_settings)
        return dataclasses.replace(
            outcome,
            tool_calls=[
                _with_turn_policy(turn, turn_config, tool_call) for tool_call in outcome.tool_calls
            ],
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        """Run one call, after refusing a Deep Agents tool its turn switches off here.

        Raises:
            ToolExecutionError: The call's turn switches the tool's local tool
                group off on this Worker, so nothing runs.
        """
        rule = DEEP_AGENTS_TOOL_RULES.get(call.tool_call.tool_name)
        if rule is not None and not rule.is_enabled(
            self._context.resolve_tool_step_permissions(call)
        ):
            raise ToolExecutionError(
                f"tool {call.tool_call.tool_name} is switched off for this session"
            )
        with bind_artifacts_session(call.flow_id):
            return await super().execute_deferred_tool_call(call)


def _with_turn_policy(
    turn: AgentTurnContext, turn_config: EffectiveConfig, tool_call: DeferredToolCall
) -> DeferredToolCall:
    # Sources rename colliding tools, so a rule's name is always a graph tool.
    rule = DEEP_AGENTS_TOOL_RULES.get(tool_call.tool_name)
    if rule is None:
        return tool_call
    if not rule.is_enabled(turn_config):
        # Its tool Step only records the refusal, so nothing waits for approval.
        policy = ToolPolicy(ToolEffect.READ, requires_approval=False)
    else:
        is_task_approval_required = tool_call.tool_name == TASK_TOOL_NAME and bool(
            _TASK_APPROVAL_TOOL_NAMES & set(turn_config.approval.always)
        )
        policy = resolve_turn_tool_policy(
            turn, tool_call.tool_name, rule.effect, is_task_approval_required, turn_config
        )
    return dataclasses.replace(tool_call, policy=policy)


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
    """Put the running turn's instructions and the workspace first in every system message."""

    def __init__(self, workspace: Path | None = None) -> None:
        """Name the workspace the file tools work in.

        Args:
            workspace: The absolute workspace directory; ``None`` names none.
        """
        super().__init__()
        self._workspace = workspace

    async def awrap_model_call(
        self,
        request: ModelRequest[Any],
        handler: Callable[[ModelRequest[Any]], Awaitable[ModelResponse[Any]]],
    ) -> ModelResponse[Any]:
        """Prepend the turn's instructions, or the default ones, to the system message."""
        turn = current_turn_config()
        if turn is None:
            return await handler(request)
        sections = [turn.instructions or DEFAULT_INSTRUCTIONS]
        if self._workspace is not None:
            sections.append(
                f"Your workspace is {self._workspace}. File tools take absolute paths "
                "inside it, and execute runs commands in it."
            )
        if request.system_message is not None and request.system_message.text:
            sections.append(request.system_message.text)
        return await handler(request.override(system_message=SystemMessage("\n\n".join(sections))))


class LocalToolSwitchesMiddleware(AgentMiddleware[Any, Any, Any]):
    """Leave out the Deep Agents tools whose local tool group the running turn switches off."""

    async def awrap_model_call(
        self,
        request: ModelRequest[Any],
        handler: Callable[[ModelRequest[Any]], Awaitable[ModelResponse[Any]]],
    ) -> ModelResponse[Any]:
        """Remove the switched-off tools from the model request."""
        turn = current_turn_config()
        if turn is None:
            return await handler(request)
        tools = [tool for tool in request.tools if _is_offered(tool, turn)]
        if len(tools) == len(request.tools):
            return await handler(request)
        return await handler(request.override(tools=tools))


def _is_offered(tool: BaseTool | Mapping[str, Any], turn: EffectiveConfig) -> bool:
    name = tool.name if isinstance(tool, BaseTool) else tool.get("name")
    rule = DEEP_AGENTS_TOOL_RULES.get(name) if isinstance(name, str) else None
    return rule is None or rule.is_enabled(turn)


class _WithoutSummarization(AgentMiddleware[Any, Any, Any]):
    """Replace the summarization, which calls the file's model inside a ``RunModel`` Step."""

    @property
    def name(self) -> str:
        return "SummarizationMiddleware"
