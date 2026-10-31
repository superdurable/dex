# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run Google ADK agents inside the durable Dex agent session Flow, in Step mode."""

from __future__ import annotations

import json
import logging
from collections.abc import Iterator, Mapping, Sequence
from datetime import timedelta

from google.adk.agents import BaseAgent, LlmAgent, ParallelAgent
from google.adk.agents.invocation_context import InvocationContext, new_invocation_context_id
from google.adk.agents.run_config import RunConfig, StreamingMode
from google.adk.apps import App, ResumabilityConfig
from google.adk.artifacts import BaseArtifactService
from google.adk.auth.credential_service.base_credential_service import BaseCredentialService
from google.adk.events import Event
from google.adk.flows.llm_flows.functions import REQUEST_CONFIRMATION_FUNCTION_CALL_NAME
from google.adk.memory import BaseMemoryService
from google.adk.plugins.plugin_manager import PluginManager
from google.adk.runners import Runner
from google.adk.tools import ToolContext
from google.adk.tools.tool_confirmation import ToolConfirmation
from google.genai import types

from dex_agents import (
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    AgentEventWriter,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    AgentUsage,
    DeferredToolCall,
    DeferredToolExecution,
    FrameworkMessage,
    MessageRole,
    ToolCallsRequested,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
)
from dex_agents_google_adk._history import (
    build_adk_session,
    framework_message_for_adk_event,
    is_adk_event_kept_after_turn,
    serialize_adk_event,
    stored_adk_events,
    without_confirmation_plumbing,
)
from dex_agents_google_adk._run_state import SuspendedAdkTurn
from dex_agents_google_adk._session_service import TurnSessionService
from dex_agents_google_adk._tool_deferral import (
    ToolDeferralPlugin,
    encode_tool_output,
    find_agent_tool,
    require_distinct_tool_names,
    require_supported_agent_tree,
)
from dex_agents_google_adk.tools import IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY

#: ADK user ID of every session unless ``AdkAgentRunner`` gets another.
DEFAULT_ADK_USER_ID = "user"

_logger = logging.getLogger(__name__)


class AdkAgentRunner:
    """Run one Google ADK agent as the framework of a durable Dex agent session.

    Pass the runner to ``AgentSessionFlow``, or build both at once with
    ``adk_agent_session_flow``. The session runs in Step mode: each
    ``RunModel`` Step rebuilds the ADK session from the Dex session's stored
    events, runs the agent with a fresh ADK ``Runner`` until the model asks for
    tools or answers, and returns the events ADK appended. One Flow ID is one
    ADK session ID.

    ADK runs no tool inside ``RunModel``, READ tools included, except the ADK
    control tools that steer the invocation itself, such as
    ``transfer_to_agent``. The adapter's plugin pauses every other call through
    ADK's own tool confirmation, and the session Flow runs each call in its own
    Dex Step, in parallel. The next ``RunModel`` answers each
    ``adk_request_confirmation`` call, and the plugin gives ADK the recorded
    result instead of running the tool.

    The runner holds no per-session state, so one instance serves every
    session of its Flow type, also concurrently. The agent, its tools,
    toolsets, plugins, and the optional services stay owned by the
    application: the runner never closes them.

    Example:
        >>> from google.adk.agents import LlmAgent
        >>> agent = LlmAgent(name="support", model="gemini-2.5-flash",
        ...                  tools=[search_tickets, refund_order])
        >>> flow = AgentSessionFlow(AdkAgentRunner(agent), flow_type="SupportAgent",
        ...     options=AgentSessionOptions(tool_policies={
        ...         "search_tickets": ToolPolicy(ToolEffect.READ)}))
    """

    def __init__(
        self,
        agent: BaseAgent | App,
        *,
        run_config: RunConfig | None = None,
        user_id: str = DEFAULT_ADK_USER_ID,
        artifact_service: BaseArtifactService | None = None,
        memory_service: BaseMemoryService | None = None,
        credential_service: BaseCredentialService | None = None,
    ) -> None:
        """Wrap an ADK agent or App.

        Construct the runner once at Worker startup and keep it for the life of
        the process. The agent and App must not change after construction.

        Args:
            agent: The root agent, or an ``App`` that carries the root agent,
                plugins, and compaction or context cache configuration. A bare
                agent becomes ``App(name=agent.name, root_agent=agent)``. The
                root must be a ``BaseAgent``, such as an ``LlmAgent`` or
                ``SequentialAgent``. The adapter always runs the App as
                resumable, because a paused turn resumes its ADK invocation.
            run_config: ADK run configuration for every run. ``None`` uses
                ``RunConfig(streaming_mode=StreamingMode.SSE)`` so model text
                reaches ``AgentEvents`` as it streams.
            user_id: ADK user ID of every session, which tools read as
                ``tool_context.user_id``. Defaults to ``"user"``.
            artifact_service: ADK artifact service, or ``None`` for none.
            memory_service: ADK memory service, or ``None`` for none.
            credential_service: ADK credential service, or ``None`` for none.

        Raises:
            TypeError: If the App's root is not a ``BaseAgent``.
            ValueError: If two agents of the tree declare different tools with
                one name, because a tool Step finds its tool by name; if an
                agent has a code executor other than ``BuiltInCodeExecutor``,
                because it would run code inside ``RunModel``; or if a
                ``task`` or ``single_turn`` sub-agent sits under a root that is
                not an ``LlmAgent``, which ADK does not support.
        """
        app = agent if isinstance(agent, App) else App(name=agent.name, root_agent=agent)
        if not isinstance(app.root_agent, BaseAgent):
            raise TypeError("the ADK App root must be a BaseAgent")
        require_supported_agent_tree(app.root_agent)
        require_distinct_tool_names(app.root_agent)
        self._app = app
        self._root_agent: BaseAgent = app.root_agent
        self._run_config = run_config or RunConfig(streaming_mode=StreamingMode.SSE)
        self._user_id = user_id
        self._artifact_service = artifact_service
        self._memory_service = memory_service
        self._credential_service = credential_service

    @property
    def app(self) -> App:
        """Return the ADK App this runner runs.

        Returns:
            The App passed to the constructor, or the App built around the agent.
        """
        return self._app

    def build_user_message(self, text: str) -> FrameworkMessage:
        """Store user text as an ADK user event that starts a new ADK invocation.

        The session Flow calls it once per user message, and once for the
        note it stores after a failed turn that ran tools. ADK appends its own
        copy of a user message when the turn's first ``RunModel`` starts the
        invocation; later turns replay the stored event with that copy's
        invocation ID and isolation scope. A note replays as user text, so the
        next model call sees it.

        Args:
            text: The user message text.

        Returns:
            A ``MessageRole.USER`` message whose ``framework_item`` is the ADK
            event JSON.
        """
        event = Event(
            invocation_id=new_invocation_context_id(),
            author="user",
            content=types.Content(role="user", parts=[types.Part(text=text)]),
        )
        return FrameworkMessage(
            role=MessageRole.USER, text=text, framework_item=serialize_adk_event(event)
        )

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        """Run the ADK invocation of a turn until the model asks for tools or answers.

        The turn's first ``RunModel`` gives ADK the user message, and ADK
        appends it as it would for any new message, so plugins and task-mode
        sub-agents see it as usual. A later ``RunModel`` resumes the paused
        invocation: it answers every ``adk_request_confirmation`` call, one
        message per ADK branch, with ``confirmed`` set to whether the call was
        approved. ADK re-executes the confirmed calls, and the plugin returns
        each recorded result.

        An ``LlmAgent`` makes one model call per ``RunModel``. Agents that
        continue without tools, such as the next agent of a ``SequentialAgent``,
        the target of ``transfer_to_agent``, or the other branches of a
        ``ParallelAgent``, make their model calls in the same ``RunModel``.

        Args:
            turn: Committed session state for this model step.

        Returns:
            ``ToolCallsRequested`` with every tool call ADK paused on, in call
            order, or ``TurnCompleted`` with every event of the turn.

        Raises:
            RuntimeError: If ADK paused for something a Dex session cannot
                answer, such as a credential request; if one model response
                mixes an ADK control tool with other tool calls; if a tool's
                name resolves to another agent's tool; if a read
                ``AgentTool`` wraps an agent with write tools; if the tool
                calls span several ADK branches other than ``LlmAgent``
                branches of a root ``ParallelAgent``; or if a resumed call did
                not get its
                recorded result.
            Exception: Any exception from ADK, the model, or a control tool.
        """
        user_message_index = _turn_user_message_index(turn)
        earlier_events = stored_adk_events(turn.messages[:user_message_index])
        suspended: SuspendedAdkTurn | None = None
        if turn.framework_run_state is None:
            resumed_events: Sequence[Event] = []
            user_text = turn.messages[user_message_index].text or ""
            new_messages = [types.Content(role="user", parts=[types.Part(text=user_text)])]
        else:
            suspended = SuspendedAdkTurn.from_framework_run_state(turn.framework_run_state)
            resumed_events = suspended.events
            new_messages = _confirmation_response_messages(turn, suspended)
        session_service = TurnSessionService(
            build_adk_session(
                app_name=self._app.name,
                user_id=self._user_id,
                session_id=turn.flow_id,
                events=[*earlier_events, *resumed_events],
            )
        )
        deferral_plugin = ToolDeferralPlugin(turn, turn.flow_id, self._root_agent)
        live_events = _LiveAdkEventWriter(turn.events, deferral_plugin.control_tool_names)
        runner = Runner(
            app=self._app.model_copy(
                update={
                    "plugins": [*self._app.plugins, deferral_plugin],
                    "resumability_config": ResumabilityConfig(is_resumable=True),
                }
            ),
            session_service=session_service,
            artifact_service=self._artifact_service,
            memory_service=self._memory_service,
            credential_service=self._credential_service,
        )
        for new_message in new_messages:
            async for event in runner.run_async(
                user_id=self._user_id,
                session_id=turn.flow_id,
                invocation_id=None if suspended is None else suspended.invocation_id,
                new_message=new_message,
                run_config=self._run_config,
            ):
                live_events.write(event)
        segment_events = session_service.appended_events
        if suspended is not None:
            _raise_for_missing_recorded_responses(segment_events, suspended)
        if suspended is None:
            user_event = _appended_user_event(segment_events)
            invocation_id, user_event_id = user_event.invocation_id, user_event.id
        else:
            invocation_id, user_event_id = suspended.invocation_id, suspended.user_event_id
        turn_events = [*resumed_events, *segment_events]
        deferred = deferral_plugin.deferred_tool_calls
        _raise_for_control_tools_beside_deferred_calls(
            segment_events, deferral_plugin.control_tool_names, deferred
        )
        confirmation_request_ids = _confirmation_request_ids(segment_events, deferred)
        _raise_for_tool_calls_across_resumable_branches(segment_events, deferred, self._root_agent)
        _raise_for_unanswered_pauses(
            segment_events, turn_events, set(confirmation_request_ids.values())
        )
        usage = _segment_usage(segment_events, deferral_plugin.model_call_count)
        if not deferred:
            return TurnCompleted(
                new_messages=[
                    framework_message_for_adk_event(event)
                    for event in without_confirmation_plumbing(turn_events)
                    if event.id != user_event_id and is_adk_event_kept_after_turn(event)
                ],
                usage=usage,
            )
        return ToolCallsRequested(
            framework_run_state=SuspendedAdkTurn(
                invocation_id=invocation_id,
                user_event_id=user_event_id,
                events=turn_events,
                confirmation_request_ids=confirmation_request_ids,
            ).to_framework_run_state(),
            tool_calls=deferred,
            usage=usage,
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Run one tool call once, in its Dex tool Step, outside the ADK run.

        The tool is the first tool with the record's name in a depth-first walk
        of the agent tree; ``RunModel`` already failed the turn when that is not
        the calling agent's tool. It runs with a fresh ``ToolContext`` whose
        ``function_call_id`` is the tool call ID and whose session ID is the
        Flow ID. Its session has no events and empty state. Its
        ``tool_context.custom_metadata`` carries the call's idempotency key,
        which ``idempotency_key_for_tool_call`` reads. An approved call gets
        ``tool_context.tool_confirmation`` with ``confirmed=True``, so a tool
        built with ``require_confirmation`` runs instead of asking again. Tool
        calls of one model response run concurrently in parallel Steps.

        Args:
            call: The record to run and its idempotency key.

        Returns:
            The tool result: a bare string as is, any other result as the JSON
            text of its ADK function response.

        Raises:
            ToolExecutionError: If the tool raised it, or asked for a
                confirmation or credential inside its Step.
            LookupError: If no agent in the tree has the tool.
            Exception: Any other exception from the tool fails the Step attempt;
                after the last attempt the session records ``failed`` for a
                read and ``outcome_unknown`` for a write.
        """
        record = call.tool_call
        session = build_adk_session(
            app_name=self._app.name, user_id=self._user_id, session_id=call.flow_id, events=[]
        )
        invocation_context = InvocationContext(
            session_service=TurnSessionService(session),
            artifact_service=self._artifact_service,
            memory_service=self._memory_service,
            credential_service=self._credential_service,
            invocation_id=new_invocation_context_id(),
            agent=self._root_agent,
            session=session,
            run_config=self._run_config.model_copy(
                update={
                    "custom_metadata": {
                        **(self._run_config.custom_metadata or {}),
                        IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY: call.idempotency_key,
                    }
                }
            ),
            plugin_manager=PluginManager(plugins=list(self._app.plugins)),
        )
        found = await find_agent_tool(self._root_agent, record.tool_name, invocation_context)
        if found is None:
            raise LookupError(f"no agent under {self._root_agent.name} has tool {record.tool_name}")
        invocation_context.agent, tool = found
        dex_approval = ToolConfirmation(confirmed=True) if record.requires_approval else None
        tool_context = ToolContext(
            invocation_context,
            function_call_id=record.tool_call_id,
            tool_confirmation=dex_approval,
        )
        result = await tool.run_async(args=json.loads(record.arguments), tool_context=tool_context)
        actions = tool_context.actions
        if actions.requested_tool_confirmations or actions.requested_auth_configs:
            raise ToolExecutionError(
                f"tool {record.tool_name} asked for a confirmation or credential while it ran "
                "in its Dex Step, which cannot answer it"
            )
        if actions.state_delta or actions.artifact_delta:
            _logger.warning(
                "deferred ADK tool %s changed session state or artifacts; Dex discards the change",
                record.tool_name,
            )
        return ToolExecutionResult(output=encode_tool_output(result))


def adk_agent_session_flow(
    agent: BaseAgent | App,
    *,
    flow_type: str,
    tool_policies: Mapping[str, ToolPolicy] | None = None,
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED,
    interrupt_deadline: timedelta | None = None,
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    run_config: RunConfig | None = None,
    user_id: str = DEFAULT_ADK_USER_ID,
    artifact_service: BaseArtifactService | None = None,
    memory_service: BaseMemoryService | None = None,
    credential_service: BaseCredentialService | None = None,
) -> AgentSessionFlow:
    """Build the durable Dex agent session Flow for one Google ADK agent.

    Register the returned Flow in a Registry created with
    ``allow_async_handlers=True`` and serve it with an ``AsyncWorker``. Build it
    once per agent at startup, with a Flow type unique within the Registry.
    Build the same Flow, with the same Flow type, in every process that starts
    sessions or calls their RPCs.

    Example:
        >>> from google.adk.agents import LlmAgent
        >>> agent = LlmAgent(name="support", model="gemini-2.5-flash",
        ...                  tools=[search_tickets, refund_order])
        >>> flow = adk_agent_session_flow(
        ...     agent,
        ...     flow_type="SupportAgent",
        ...     tool_policies={
        ...         "search_tickets": ToolPolicy(ToolEffect.READ),
        ...         "refund_order": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE,
        ...                                    requires_approval=True),
        ...     },
        ... )
        >>> registry = Registry([flow], allow_async_handlers=True)

    Args:
        agent: The root ``BaseAgent``, or an ``App`` that carries it. See
            ``AdkAgentRunner``.
        flow_type: Protocol Flow type of the agent's sessions, such as
            ``"SupportAgent"``. Keep it stable: running sessions resume by it.
        tool_policies: Policies by tool name. They override ADK metadata and
            ``default_tool_effect``. ``None`` sets none.
        default_tool_effect: Effect of a tool without a policy or ADK metadata.
            Defaults to ``UNCLASSIFIED``: the call runs once, after approval.
        interrupt_deadline: How long an approval may stay pending before the
            session rejects it. ``None`` waits indefinitely.
        event_stream_capacity_bytes: Approximate byte budget of the
            ``AgentEvents`` Stream. Defaults to 10 MiB.
        run_config: ADK run configuration. See ``AdkAgentRunner``.
        user_id: ADK user ID of every session. Defaults to ``"user"``.
        artifact_service: ADK artifact service, or ``None`` for none.
        memory_service: ADK memory service, or ``None`` for none.
        credential_service: ADK credential service, or ``None`` for none.

    Returns:
        An ``AgentSessionFlow`` that runs the agent with an ``AdkAgentRunner``.

    Raises:
        ValueError: If ``flow_type`` is blank, or a capacity or deadline is not
            positive.
        TypeError: If the App's root is not a ``BaseAgent``.
        ValueError: If the agent tree has a feature that cannot run as Dex
            Steps. See ``AdkAgentRunner``.
    """
    return AgentSessionFlow(
        AdkAgentRunner(
            agent,
            run_config=run_config,
            user_id=user_id,
            artifact_service=artifact_service,
            memory_service=memory_service,
            credential_service=credential_service,
        ),
        flow_type=flow_type,
        options=AgentSessionOptions(
            tool_policies=tool_policies or {},
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            interrupt_deadline=interrupt_deadline,
        ),
    )


class _LiveAdkEventWriter:
    """Write streamed model text and control tool completions to ``AgentEvents``."""

    def __init__(self, events: AgentEventWriter, control_tool_names: Mapping[str, str]) -> None:
        self._events = events
        self._control_tool_names = control_tool_names
        self._has_partial_text = False

    def write(self, event: Event) -> None:
        for response in event.get_function_responses():
            tool_name = self._control_tool_names.get(response.id or "")
            if tool_name is not None and not event.partial:
                status = (
                    ToolCallStatus.FAILED
                    if "error" in (response.response or {})
                    else ToolCallStatus.SUCCEEDED
                )
                self._events.write_tool_call_finished(response.id or "", tool_name, status)
        if event.author == "user" or event.content is None or event.content.role != "model":
            return
        if event.partial:
            self._has_partial_text = True
            self._write_text_parts(event)
            return
        # A complete response repeats the text its partial responses already streamed.
        if not self._has_partial_text:
            self._write_text_parts(event)
        self._has_partial_text = False

    def _write_text_parts(self, event: Event) -> None:
        for part in _event_parts(event):
            if not part.text:
                continue
            if part.thought:
                self._events.write_reasoning_delta(part.text)
            else:
                self._events.write_text_delta(part.text)


def _event_parts(event: Event) -> Iterator[types.Part]:
    if event.content is not None:
        yield from event.content.parts or []


def _turn_user_message_index(turn: AgentTurnContext) -> int:
    for index, message in enumerate(turn.messages):
        if message.role is MessageRole.USER and message.turn_id == turn.turn_id:
            return index
    raise RuntimeError(f"turn {turn.turn_id} has no stored user message")


def _appended_user_event(segment_events: Sequence[Event]) -> Event:
    for event in segment_events:
        if event.author == "user" and not event.get_function_responses():
            return event
    raise RuntimeError("ADK did not append the turn's user message")


def _confirmation_response_messages(
    turn: AgentTurnContext, suspended: SuspendedAdkTurn
) -> list[types.Content]:
    """Answer every confirmation, one message per branch, because ADK stamps one branch."""
    branch_by_request_id = {
        function_call.id: event.branch
        for event in suspended.events
        for function_call in event.get_function_calls()
        if function_call.name == REQUEST_CONFIRMATION_FUNCTION_CALL_NAME
    }
    parts_by_branch: dict[str | None, list[types.Part]] = {}
    for tool_call_id, request_id in suspended.confirmation_request_ids.items():
        record = turn.recorded_tool_call(tool_call_id)
        if record is None or not record.status.is_final:
            raise RuntimeError(f"tool call {tool_call_id} has no final record")
        confirmation = ToolConfirmation(confirmed=record.status is not ToolCallStatus.REJECTED)
        parts_by_branch.setdefault(branch_by_request_id.get(request_id), []).append(
            types.Part(
                function_response=types.FunctionResponse(
                    id=request_id,
                    name=REQUEST_CONFIRMATION_FUNCTION_CALL_NAME,
                    response=confirmation.model_dump(by_alias=True, exclude_none=True),
                )
            )
        )
    if not parts_by_branch:
        raise RuntimeError(f"turn {turn.turn_id} resumed without a tool call")
    return [types.Content(role="user", parts=parts) for parts in parts_by_branch.values()]


def _raise_for_control_tools_beside_deferred_calls(
    segment_events: Sequence[Event],
    control_tool_names: Mapping[str, str],
    deferred_tool_calls: Sequence[DeferredToolCall],
) -> None:
    deferred_ids = {deferred.tool_call_id for deferred in deferred_tool_calls}
    for event in segment_events:
        call_ids = {function_call.id for function_call in event.get_function_calls()}
        control_names = sorted(
            name for call_id, name in control_tool_names.items() if call_id in call_ids
        )
        if control_names and call_ids & deferred_ids:
            raise RuntimeError(
                f"the model called {', '.join(control_names)} together with tools that need "
                "Dex Steps; ADK applies a control tool in the same response, before those "
                "results exist, so Dex fails the turn instead"
            )


def _raise_for_missing_recorded_responses(
    segment_events: Sequence[Event], suspended: SuspendedAdkTurn
) -> None:
    """Fail when ADK resumed without giving an agent the recorded result of its call."""
    answered_call_ids = {
        response.id
        for event in segment_events
        for response in event.get_function_responses()
        if response.id and response.id not in event.actions.requested_tool_confirmations
    }
    missing = [
        tool_call_id
        for tool_call_id in suspended.confirmation_request_ids
        if tool_call_id not in answered_call_ids
    ]
    if missing:
        raise RuntimeError(
            "ADK resumed the turn without giving the recorded results of tool calls "
            + ", ".join(missing)
            + " to their agents"
        )


def _raise_for_tool_calls_across_resumable_branches(
    segment_events: Sequence[Event],
    deferred_tool_calls: Sequence[DeferredToolCall],
    root_agent: BaseAgent,
) -> None:
    """Fail before any Step runs when ADK could not resume each branch with its answers.

    Each branch gets its own resume message, and ADK runs on after the first
    one finishes its branch. That is safe only for ``LlmAgent`` branches
    directly under a root ``ParallelAgent``.
    """
    deferred_ids = {deferred.tool_call_id for deferred in deferred_tool_calls}
    branches = {
        event.branch
        for event in segment_events
        for function_call in event.get_function_calls()
        if function_call.id in deferred_ids
    }
    if len(branches) <= 1:
        return
    # A branch that is itself a workflow agent runs on past its first answered agent.
    direct_branches = (
        {
            f"{root_agent.name}.{sub_agent.name}"
            for sub_agent in root_agent.sub_agents
            if isinstance(sub_agent, LlmAgent)
        }
        if isinstance(root_agent, ParallelAgent)
        else set()
    )
    if not branches <= direct_branches:
        raise RuntimeError(
            "one model step asked for tool calls in several ADK branches ("
            + ", ".join(sorted(str(branch) for branch in branches))
            + "); Dex resumes separate branches only when each is an LlmAgent directly "
            "under a root ParallelAgent, so it fails the turn before any tool runs"
        )


def _confirmation_request_ids(
    segment_events: Sequence[Event], deferred_tool_calls: Sequence[DeferredToolCall]
) -> dict[str, str]:
    request_ids: dict[str, str] = {}
    for event in segment_events:
        for function_call in event.get_function_calls():
            if function_call.name != REQUEST_CONFIRMATION_FUNCTION_CALL_NAME:
                continue
            original_call = (function_call.args or {}).get("originalFunctionCall")
            if isinstance(original_call, dict) and function_call.id:
                request_ids[str(original_call.get("id"))] = function_call.id
    ordered: dict[str, str] = {}
    for deferred in deferred_tool_calls:
        request_id = request_ids.get(deferred.tool_call_id)
        if request_id is None:
            raise RuntimeError(
                f"ADK did not pause for tool call {deferred.tool_call_id} ({deferred.tool_name})"
            )
        ordered[deferred.tool_call_id] = request_id
    return ordered


def _raise_for_unanswered_pauses(
    segment_events: Sequence[Event],
    turn_events: Sequence[Event],
    confirmation_request_ids: set[str],
) -> None:
    answered_call_ids = {
        response.id
        for event in turn_events
        for response in event.get_function_responses()
        if response.id
    }
    unanswered_tool_names = sorted(
        function_call.name or "<unnamed>"
        for event in segment_events
        for function_call in event.get_function_calls()
        if function_call.id in (event.long_running_tool_ids or set())
        and function_call.id not in answered_call_ids
        and function_call.id not in confirmation_request_ids
    )
    if unanswered_tool_names:
        raise RuntimeError(
            "ADK paused the turn for "
            + ", ".join(unanswered_tool_names)
            + ", which a Dex agent session cannot answer"
        )


def _segment_usage(segment_events: Sequence[Event], model_call_count: int) -> AgentUsage:
    usage = AgentUsage(model_calls=model_call_count)
    for event in segment_events:
        metadata = event.usage_metadata
        if metadata is None:
            continue
        reasoning_tokens = metadata.thoughts_token_count or 0
        usage = usage.combined_with(
            AgentUsage(
                input_tokens=(metadata.prompt_token_count or 0)
                + (metadata.tool_use_prompt_token_count or 0),
                output_tokens=(metadata.candidates_token_count or 0) + reasoning_tokens,
                cached_input_tokens=metadata.cached_content_token_count or 0,
                reasoning_tokens=reasoning_tokens,
            )
        )
    return usage
