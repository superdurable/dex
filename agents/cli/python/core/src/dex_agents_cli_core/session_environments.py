# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Sessions that move between execution environments.

A command that resumes another environment's session polls that session's Flow
type. Dex routes Steps by Flow type alone, so every Worker that polls it may
receive the session's calls: the fence lets a model or tool call run only in
the environment that holds the session's lease. The lines and the decision are
fixed by ``agents/conformance/cli_session_environments.json``.
"""

from __future__ import annotations

import asyncio
import logging
import re
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime
from typing import Any

from dex import AsyncClient, DexServiceError

from dex_agents import (
    AGENT_STATE,
    SESSION_TERMINAL_LEASE,
    AgentSessionClient,
    AgentSessionFlow,
    AgentTurnContext,
    AgentTurnOutcome,
    ContextSummaryRequest,
    DeferredToolExecution,
    SessionTerminalLease,
    SessionTerminalLeaseResult,
    ToolExecutionError,
    ToolTaskCancellation,
    ToolTaskPoll,
)
from dex_agents_cli_core.session_lease import (
    LEASE_CALL_TIMEOUT_SECONDS,
    HeldSessionLease,
    is_lease_expired_here,
    is_session_call_allowed_here,
    session_owner_environment,
)
from dex_agents_cli_core.turns import OPTIONAL_RUNNER_METHODS

#: The line printed when a takeover cancelled the turn in progress.
CANCELLED_TURN_LINE = (
    "Cancelled the turn that was in progress there; none of its model or tool calls run here."
)
#: How long a takeover keeps trying ``cancel_turn`` while a short Step holds its lock.
_TURN_CANCELLATION_TIMEOUT_SECONDS = 5.0
#: How long a takeover waits for the cancelled turn to end before the chat starts.
_TURN_END_TIMEOUT_SECONDS = 10.0
#: How often a takeover reads whether the cancelled turn ended.
_TURN_END_POLL_SECONDS = 0.1
_SESSION_FLOW_TYPE_SUFFIX = re.compile(r"-[0-9a-z]{6}")
_FENCED_RUNNER_METHODS = frozenset(
    {
        "run_model_step",
        "execute_deferred_tool_call",
        "summarize_context",
        "poll_tool_task",
        "cancel_tool_task",
    }
)
_LOGGER = logging.getLogger(__name__)


class SessionWorkerStartError(Exception):
    """The deferred Worker of another environment's session could not start; waiting ends."""


def is_harness_session_flow_type(harness_flow_type: str, flow_type: str) -> bool:
    """Return whether a Flow type is the harness's base Flow type, ``-``, and 6 base36 digits."""
    return flow_type.startswith(harness_flow_type) and (
        _SESSION_FLOW_TYPE_SUFFIX.fullmatch(flow_type[len(harness_flow_type) :]) is not None
    )


def build_other_harness_error(session_id: str, flow_type: str, command_name: str) -> str:
    """Return the error for ``--resume`` of a session that another harness's command runs."""
    return (
        f"session {session_id} is a {flow_type} session, which {command_name} does not run; "
        "resume it with the command that started it"
    )


def build_takeover_line(
    session_id: str, previous_environment: str, this_environment: str, workspace: str
) -> str:
    """Return the line printed when a command takes a session over from another environment."""
    return (
        f"Session {session_id} was last open in execution environment {previous_environment}; "
        f"its model and tool calls now run in this one ({this_environment}), "
        f"in workspace {workspace}."
    )


def build_session_elsewhere_reason(session_id: str) -> str:
    """Return why a Worker outside the environment that holds a session refuses its call."""
    return f"session {session_id} is now open in another execution environment"


def build_turn_elsewhere_reason(session_id: str) -> str:
    """Return why the holder refuses a call of the turn in progress at its takeover."""
    return (
        "its turn started in another execution environment before this command took "
        f"session {session_id} over"
    )


def build_refused_tool_call_error(tool_name: str, reason: str) -> str:
    """Return the error of a refused tool call."""
    return f"tool {tool_name} did not run: {reason}"


def build_refused_model_call_error(reason: str) -> str:
    """Return the error of a refused model call."""
    return f"the model call did not run: {reason}"


async def read_stored_session_lease(
    client: AsyncClient, session_id: str
) -> SessionTerminalLease | None:
    """Read a session's stored lease from Dex, which needs no Worker of its Flow type.

    Raises:
        DexServiceError: If Dex refuses the read.
        TimeoutError: If Dex does not answer within ``LEASE_CALL_TIMEOUT_SECONDS``.
    """
    values = await asyncio.wait_for(
        client.get_attributes(session_id, SESSION_TERMINAL_LEASE), LEASE_CALL_TIMEOUT_SECONDS
    )
    stored: SessionTerminalLease | None = values.get(SESSION_TERMINAL_LEASE)
    return stored


class SessionEnvironmentFence:
    """Refuses a session's model and tool calls on a Worker outside the environment that holds it.

    Each call first reads the stored lease from Dex and runs only when
    ``is_session_call_allowed_here`` allows it. The holder also refuses the
    turn that was in progress when it took the session over from another
    environment.
    """

    def __init__(
        self,
        lease_reader: AsyncClient,
        *,
        session_flow_type: str,
        home_flow_type: str,
        holder_id: str,
    ) -> None:
        """Create the fence; ``with_session_environment_fence`` puts it in front of a runner.

        Args:
            lease_reader: A Client that reads the stored leases; it needs no Flows.
            session_flow_type: The Flow type the Worker polls.
            home_flow_type: The session Flow type of this command's own environment.
            holder_id: This process's lease holder ID.
        """
        self._lease_reader = lease_reader
        self._session_flow_type = session_flow_type
        self._home_flow_type = home_flow_type
        self._holder_id = holder_id
        self._refused_turn_ids: dict[str, set[str]] = {}

    def refuse_turn(self, session_id: str, turn_id: str) -> None:
        """Refuse, in this process, every model and tool call of one turn of a session."""
        self._refused_turn_ids.setdefault(session_id, set()).add(turn_id)

    async def refusal_reason(self, session_id: str, turn_id: str) -> str | None:
        """Return why this Worker must not run a call of a session's turn, or ``None`` when it may.

        Raises:
            DexServiceError: If Dex refuses the read of the stored lease.
            TimeoutError: If Dex does not answer it.
        """
        if turn_id in self._refused_turn_ids.get(session_id, set()):
            return build_turn_elsewhere_reason(session_id)
        stored = await read_stored_session_lease(self._lease_reader, session_id)
        is_allowed = is_session_call_allowed_here(
            session_flow_type=self._session_flow_type,
            home_flow_type=self._home_flow_type,
            holder_id=self._holder_id,
            stored=stored,
        )
        return None if is_allowed else build_session_elsewhere_reason(session_id)

    async def require_model_call_here(self, session_id: str, turn_id: str) -> None:
        """Raise when this Worker must not run a model call or a summary of the turn.

        Raises:
            RuntimeError: With ``build_refused_model_call_error`` when it must not.
        """
        reason = await self.refusal_reason(session_id, turn_id)
        if reason is not None:
            raise RuntimeError(build_refused_model_call_error(reason))

    async def require_tool_call_here(self, session_id: str, turn_id: str, tool_name: str) -> None:
        """Raise ``ToolExecutionError`` when this Worker must not run the tool call.

        The tool has not run then, so the call records ``failed``, also when
        Dex does not answer the read of the stored lease.
        """
        try:
            reason = await self.refusal_reason(session_id, turn_id)
        except (DexServiceError, TimeoutError) as error:
            raise ToolExecutionError(
                build_refused_tool_call_error(
                    tool_name, f"its session's lease could not be read: {error}"
                )
            ) from error
        if reason is not None:
            raise ToolExecutionError(build_refused_tool_call_error(tool_name, reason))


def with_session_environment_fence(
    flow: AgentSessionFlow, fence: SessionEnvironmentFence
) -> AgentSessionFlow:
    """Return the same agent session Flow, with the fence before each call that acts for it.

    The model calls, summaries, tool calls, and tool task polls and
    cancellations check the fence first. The other runner members are
    forwarded, and an optional member stays absent when the runner has none.

    Args:
        flow: The session Flow.
        fence: The fence.

    Returns:
        A new Flow with the same type and options around the wrapped runner.
    """
    inner = flow.runner
    namespace: dict[str, Any] = {
        name: _forwarding_method(name)
        for name in OPTIONAL_RUNNER_METHODS
        if name not in _FENCED_RUNNER_METHODS and callable(getattr(inner, name, None))
    }
    for name in ("summarize_context", "poll_tool_task", "cancel_tool_task"):
        if callable(getattr(inner, name, None)):
            namespace[name] = getattr(_FencedRunner, f"_fenced_{name}")
    if hasattr(inner, "supports_plan_mode"):
        namespace["supports_plan_mode"] = property(lambda self: self._inner.supports_plan_mode)
    wrapper_type = type("SessionEnvironmentFencedRunner", (_FencedRunner,), namespace)
    runner = wrapper_type(inner, fence)
    return AgentSessionFlow(runner, flow_type=flow.get_flow_type(), options=flow.options)


class _FencedRunner:
    def __init__(self, inner: Any, fence: SessionEnvironmentFence) -> None:
        self._inner = inner
        self._fence = fence

    def build_user_message(self, text: str) -> Any:
        return self._inner.build_user_message(text)

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        await self._fence.require_model_call_here(turn.flow_id, turn.turn_id)
        outcome: AgentTurnOutcome = await self._inner.run_model_step(turn)
        return outcome

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> Any:
        await self._fence.require_tool_call_here(
            call.flow_id, call.tool_call.turn_id, call.tool_call.tool_name
        )
        return await self._inner.execute_deferred_tool_call(call)

    async def _fenced_summarize_context(self, request: ContextSummaryRequest) -> Any:
        await self._fence.require_model_call_here(request.flow_id, request.turn_id)
        return await self._inner.summarize_context(request)

    async def _fenced_poll_tool_task(self, poll: ToolTaskPoll) -> Any:
        await self._fence.require_tool_call_here(
            poll.flow_id, poll.tool_call.turn_id, poll.tool_call.tool_name
        )
        return await self._inner.poll_tool_task(poll)

    async def _fenced_cancel_tool_task(self, cancellation: ToolTaskCancellation) -> Any:
        await self._fence.require_tool_call_here(
            cancellation.flow_id, cancellation.tool_call.turn_id, cancellation.tool_call.tool_name
        )
        return await self._inner.cancel_tool_task(cancellation)


def _forwarding_method(name: str) -> Callable[..., Any]:
    def forward(self: _FencedRunner, *args: Any, **kwargs: Any) -> Any:
        return getattr(self._inner, name)(*args, **kwargs)

    forward.__name__ = name
    return forward


class SessionTakeover:
    """Opens sessions for one command, including sessions of other execution environments.

    A Worker for another environment's session starts polling only once no
    other terminal's live lease is stored, so it takes none of the session's
    Steps while another terminal has the session open. A takeover from another
    environment refuses and cancels the turn in progress, so nothing approved
    there runs here.
    """

    def __init__(
        self,
        *,
        client: AsyncClient,
        sessions: AgentSessionClient,
        fence: SessionEnvironmentFence,
        lease: HeldSessionLease,
        holder_id: str,
        session_flow_type: str,
        home_flow_type: str,
        workspace: str,
        start_worker: Callable[[], Awaitable[None]],
        is_worker_started: Callable[[], bool],
    ) -> None:
        """Create the takeover.

        Args:
            client: The command's Client.
            sessions: The session client.
            fence: The fence in front of the session Flow's runner.
            lease: The lease this process holds.
            holder_id: This process's holder ID.
            session_flow_type: The Flow type the Worker polls.
            home_flow_type: The session Flow type of this command's own environment.
            workspace: This command's workspace, which the takeover line names.
            start_worker: Starts the Worker once; later calls wait for the first.
                It raises ``SessionWorkerStartError`` when the Worker cannot start.
            is_worker_started: Returns whether the Worker polls the Flow type already.
        """
        self._client = client
        self._sessions = sessions
        self._fence = fence
        self._lease = lease
        self._holder_id = holder_id
        self._session_flow_type = session_flow_type
        self._home_flow_type = home_flow_type
        self._workspace = workspace
        self._start_worker = start_worker
        self._is_worker_started = is_worker_started
        self._cross_environment_acquisitions: dict[str, str] = {}

    @property
    def is_other_environment_session(self) -> bool:
        """Return whether the Worker polls another execution environment's Flow type."""
        return self._session_flow_type != self._home_flow_type

    async def attempt(self, session_id: str) -> SessionTerminalLeaseResult:
        """Try once to take the session's lease, as ``wait_for_session_lease`` asks.

        Returns:
            The lease RPC's result, or a refusal naming the stored holder while
            the Worker still waits to start.

        Raises:
            SessionWorkerStartError: If the deferred Worker cannot start.
            DexServiceError: If a Dex call fails; a waiting caller tries again.
            TimeoutError: If a Dex call takes too long; a waiting caller tries again.
        """
        stored = await read_stored_session_lease(self._client, session_id)
        if not self._is_worker_started():
            if (
                stored is not None
                and stored.holder_id != self._holder_id
                and not is_lease_expired_here(stored, datetime.now(UTC))
            ):
                return SessionTerminalLeaseResult(is_acquired=False, lease=stored)
            await self._start_worker()
        previous_environment = session_owner_environment(self._session_flow_type, stored)
        is_cross_environment = (
            stored is None or stored.holder_id != self._holder_id
        ) and previous_environment != self._home_flow_type
        if is_cross_environment:
            # Refused before the lease is taken, so this Worker never runs a call approved there.
            active_turn_id = await self._read_active_turn_id(session_id)
            if active_turn_id is not None:
                self._fence.refuse_turn(session_id, active_turn_id)
        result = await self._lease.try_acquire(session_id)
        if result.is_acquired and is_cross_environment:
            self._cross_environment_acquisitions[session_id] = previous_environment
        return result

    async def complete_takeover(self, session_id: str) -> list[str]:
        """Finish a takeover from another environment once the lease is held.

        It refuses and cancels the turn in progress, and waits a bounded time
        for it to end, so the chat never shows the cancelled turn's approvals.

        Returns:
            The lines to print, none when the session did not come from
            another environment.
        """
        previous_environment = self._cross_environment_acquisitions.get(session_id)
        if previous_environment is None:
            return []
        lines = [
            build_takeover_line(
                session_id, previous_environment, self._home_flow_type, self._workspace
            )
        ]
        active_turn_id = await self._read_active_turn_id(session_id)
        if active_turn_id is not None:
            self._fence.refuse_turn(session_id, active_turn_id)
            await self._cancel_turn(session_id, active_turn_id)
            await self._wait_for_turn_end(session_id, active_turn_id)
            lines.append(CANCELLED_TURN_LINE)
        # Kept until here, so a failed read leaves the takeover to finish on the next try.
        del self._cross_environment_acquisitions[session_id]
        return lines

    async def _read_active_turn_id(self, session_id: str) -> str | None:
        values = await asyncio.wait_for(
            self._client.get_attributes(session_id, AGENT_STATE), LEASE_CALL_TIMEOUT_SECONDS
        )
        state = values.get(AGENT_STATE)
        return None if state is None else state.active_turn_id

    async def _cancel_turn(self, session_id: str, turn_id: str) -> None:
        """Cancel one turn; a failure is only logged, because the fence refuses the turn's calls."""
        try:
            await asyncio.wait_for(
                self._sessions.cancel_turn(session_id, turn_id), _TURN_CANCELLATION_TIMEOUT_SECONDS
            )
        except (DexServiceError, TimeoutError) as error:
            _LOGGER.warning("cannot cancel the turn in progress: session=%s: %s", session_id, error)

    async def _wait_for_turn_end(self, session_id: str, turn_id: str) -> None:
        """Wait a bounded time for a turn to end; a tool call that runs on keeps it going."""
        loop = asyncio.get_running_loop()
        deadline = loop.time() + _TURN_END_TIMEOUT_SECONDS
        while loop.time() < deadline and await self._read_active_turn_id(session_id) == turn_id:
            await asyncio.sleep(_TURN_END_POLL_SECONDS)
