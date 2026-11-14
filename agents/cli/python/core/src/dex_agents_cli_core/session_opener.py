# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Open and leave sessions for the plain chat and the Worker host.

The order follows the session task queue plan: read the lease without a
Worker, pull the session's task queue only while the session is free, then
acquire the lease through the RPC that the pulling Worker serves.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from dex import AsyncClient, AsyncWorker, DexServiceError, FlowNotFoundError, FlowStatus

from dex_agents import (
    AGENT_STATE,
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    SESSION_TERMINAL_LEASE,
    AgentSessionClient,
    AgentSessionFlow,
    SessionTerminalLease,
    SessionTerminalLeaseResult,
    ToolExecutionError,
)
from dex_agents_cli_core.session_lease import (
    LEASE_CALL_TIMEOUT_SECONDS,
    LEASE_RELEASE_TIMEOUT_SECONDS,
    SessionLeaseHolder,
)
from dex_agents_cli_core.session_task_queues import (
    CANCELLED_TURN_LINE,
    build_takeover_line,
    describe_session_environment,
    is_legacy_session_flow_type,
    is_other_session_place,
    other_environment_legacy_session_error,
    other_harness_session_error,
    refused_model_call_error,
    refused_tool_call_error,
    session_task_queue,
    taken_over_turn_reason,
)

#: How long a session Worker may take to start pulling its task queue.
SESSION_WORKER_START_TIMEOUT_SECONDS = 30.0
# How long a takeover keeps trying cancel_turn while a short Step holds its lock.
_TURN_CANCELLATION_TIMEOUT_SECONDS = 5.0
# How long a takeover waits for the cancelled turn to end before the chat starts.
_TURN_END_TIMEOUT_SECONDS = 10.0
_TURN_END_POLL_SECONDS = 0.1
# The optional runner members the session Flow checks for; a wrapper keeps exactly these.
_OPTIONAL_RUNNER_METHODS = (
    "build_unfinished_turn_messages",
    "build_user_message_with_images",
    "tool_call_step_options",
    "poll_tool_task",
    "cancel_tool_task",
    "estimate_input_tokens",
    "select_compaction_cutoff",
    "summarize_context",
)
_LOGGER = logging.getLogger(__name__)


class UnopenableSessionError(Exception):
    """A session that does not exist, does not run, or belongs to another harness or environment."""


class SessionWorkerStartError(Exception):
    """The session Worker could not start pulling a session's task queue; waiting ends."""


@dataclass(frozen=True)
class OpenableSession:
    """A session this command can open.

    Attributes:
        session_id: The session.
        flow_type: The session's Flow type.
        has_session_task_queue: Whether the session runs on its own task queue.
            ``False`` for an environment-hashed session from a release before
            0.6.7, which runs on the default task queue of this execution
            environment's Workers.
        sessions: The session client of ``flow_type``'s Flow.
    """

    session_id: str
    flow_type: str
    has_session_task_queue: bool
    sessions: AgentSessionClient


@dataclass
class _PullingWorker:
    task_queue: str
    worker: AsyncWorker
    serving: asyncio.Task[None]
    started: asyncio.Task[None]


class SessionQueueWorker:
    """The Worker that pulls the task queue of the one session this command opens.

    One app Worker serves one task queue, so each queue gets a new Worker with
    a new app Worker ID.
    """

    def __init__(
        self,
        create_worker: Callable[[str, str], AsyncWorker],
        app_worker_id_for: Callable[[int], str],
        describe_start_failure: Callable[[BaseException], str],
    ) -> None:
        """Create the holder; ``pull`` starts a Worker.

        Args:
            create_worker: Builds a Worker that pulls a task queue with an app Worker ID.
            app_worker_id_for: Returns the app Worker ID of one incarnation.
            describe_start_failure: Returns the one line for a Worker that did not start.
        """
        self._create_worker = create_worker
        self._app_worker_id_for = app_worker_id_for
        self._describe_start_failure = describe_start_failure
        self._current: _PullingWorker | None = None
        self._closings: list[asyncio.Task[None]] = []
        self._incarnation = 0

    @property
    def task_queue(self) -> str | None:
        """Return the task queue the current Worker pulls, or ``None``."""
        return None if self._current is None else self._current.task_queue

    async def pull(self, task_queue: str) -> None:
        """Pull a task queue from now on, instead of any other one.

        Raises:
            SessionWorkerStartError: If the Worker did not start pulling.
        """
        if self._current is None or self._current.task_queue != task_queue:
            self.stop()
            self._incarnation += 1
            worker = self._create_worker(task_queue, self._app_worker_id_for(self._incarnation))
            serving = asyncio.create_task(worker.start())
            started = asyncio.create_task(_wait_until_pulling(worker, serving))
            self._current = _PullingWorker(task_queue, worker, serving, started)
        current = self._current
        try:
            await asyncio.shield(current.started)
        except asyncio.CancelledError:
            raise
        except Exception as error:
            if self._current is current:
                self.stop()
            raise SessionWorkerStartError(self._describe_start_failure(error)) from error

    def stop(self) -> None:
        """Stop pulling; the Worker drains in the background, and ``close`` waits for that."""
        current, self._current = self._current, None
        if current is not None:
            self._closings.append(asyncio.create_task(_close_pulling_worker(current)))

    async def close(self) -> None:
        """Stop pulling and wait until every Worker this holder started has closed."""
        self.stop()
        await asyncio.gather(*self._closings)


async def _wait_until_pulling(worker: AsyncWorker, serving: asyncio.Task[None]) -> None:
    """Return once the Worker pulls; raise the start failure when it fails first."""
    waiting = asyncio.ensure_future(
        worker.wait_pull_registered(timeout=SESSION_WORKER_START_TIMEOUT_SECONDS)
    )
    await asyncio.wait({waiting, serving}, return_when=asyncio.FIRST_COMPLETED)
    if serving.done() and not serving.cancelled() and serving.exception() is not None:
        waiting.cancel()
        with contextlib.suppress(BaseException):
            await waiting
        serving.result()
    if not waiting.done():
        waiting.cancel()
        raise RuntimeError("the session Worker stopped before it pulled its task queue")
    waiting.result()


async def _close_pulling_worker(pulling: _PullingWorker) -> None:
    pulling.started.cancel()
    try:
        await pulling.worker.close()
    except Exception as error:  # A failed close only loses this Worker's drain.
        _LOGGER.warning("the session Worker did not close cleanly: %s", error)
    for task in (pulling.started, pulling.serving):
        # The start failure was reported already; here it only must not stay unretrieved.
        with contextlib.suppress(BaseException):
            await task


class TakenOverTurnRefusal:
    """Refuse, in this process, the calls of a turn taken over from another place.

    The turn was in progress where the session was last open when this
    command took it over, so nothing approved there runs here.
    """

    def __init__(self) -> None:
        self._refused_turn_ids: dict[str, set[str]] = {}

    def refuse_turn(self, session_id: str, turn_id: str) -> None:
        """Refuse every model and tool call of one turn of a session from now on."""
        self._refused_turn_ids.setdefault(session_id, set()).add(turn_id)

    def is_refused(self, session_id: str, turn_id: str) -> bool:
        """Return whether this process refuses the calls of a session's turn."""
        return turn_id in self._refused_turn_ids.get(session_id, set())

    def wrap_flow(self, flow: AgentSessionFlow) -> AgentSessionFlow:
        """Return the same agent session Flow with this refusal in front of its runner.

        It covers model calls, tool calls, summaries, and tool task polls and
        cancellations. The wrapper keeps exactly the optional members the
        runner has, because the session Flow checks for them.

        Args:
            flow: The session Flow.

        Returns:
            A new Flow with the same type and options around the wrapped runner.
        """
        runner = _wrap_refusing_runner(flow.runner, self)
        return AgentSessionFlow(runner, flow_type=flow.get_flow_type(), options=flow.options)

    def require_model_call(self, session_id: str, turn_id: str) -> None:
        """Raise when this process refuses the model calls of a session's turn."""
        if self.is_refused(session_id, turn_id):
            raise RuntimeError(refused_model_call_error(taken_over_turn_reason(session_id)))

    def require_tool_call(self, session_id: str, turn_id: str, tool_name: str) -> None:
        """Raise ``ToolExecutionError`` when this process refuses the tool calls of a turn."""
        if self.is_refused(session_id, turn_id):
            # The tool has not run, so the call records failed rather than an unknown outcome.
            raise ToolExecutionError(
                refused_tool_call_error(tool_name, taken_over_turn_reason(session_id))
            )


class _RefusingRunner:
    def __init__(self, inner: Any, refusal: TakenOverTurnRefusal) -> None:
        self._inner = inner
        self._refusal = refusal

    def build_user_message(self, text: str) -> Any:
        return self._inner.build_user_message(text)

    async def run_model_step(self, turn: Any) -> Any:
        self._refusal.require_model_call(turn.flow_id, turn.turn_id)
        return await self._inner.run_model_step(turn)

    async def execute_deferred_tool_call(self, call: Any) -> Any:
        tool_call = call.tool_call
        self._refusal.require_tool_call(call.flow_id, tool_call.turn_id, tool_call.tool_name)
        return await self._inner.execute_deferred_tool_call(call)


async def _refused_summarize_context(self: _RefusingRunner, request: Any) -> Any:
    self._refusal.require_model_call(request.flow_id, request.turn_id)
    return await self._inner.summarize_context(request)


async def _refused_poll_tool_task(self: _RefusingRunner, poll: Any) -> Any:
    tool_call = poll.tool_call
    self._refusal.require_tool_call(poll.flow_id, tool_call.turn_id, tool_call.tool_name)
    return await self._inner.poll_tool_task(poll)


async def _refused_cancel_tool_task(self: _RefusingRunner, cancellation: Any) -> Any:
    tool_call = cancellation.tool_call
    self._refusal.require_tool_call(cancellation.flow_id, tool_call.turn_id, tool_call.tool_name)
    return await self._inner.cancel_tool_task(cancellation)


_REFUSING_METHODS: dict[str, Callable[..., Any]] = {
    "summarize_context": _refused_summarize_context,
    "poll_tool_task": _refused_poll_tool_task,
    "cancel_tool_task": _refused_cancel_tool_task,
}


def _wrap_refusing_runner(inner: Any, refusal: TakenOverTurnRefusal) -> Any:
    """Build a wrapper class with exactly the optional members the inner runner has."""
    namespace: dict[str, Any] = {
        name: _REFUSING_METHODS.get(name, _forwarding_method(name))
        for name in _OPTIONAL_RUNNER_METHODS
        if callable(getattr(inner, name, None))
    }
    if hasattr(inner, "supports_plan_mode"):
        namespace["supports_plan_mode"] = property(lambda self: self._inner.supports_plan_mode)
    wrapper_type = type("TakenOverTurnRefusingRunner", (_RefusingRunner,), namespace)
    return wrapper_type(inner, refusal)


def _forwarding_method(name: str) -> Callable[..., Any]:
    def forward(self: _RefusingRunner, *args: Any, **kwargs: Any) -> Any:
        return getattr(self._inner, name)(*args, **kwargs)

    forward.__name__ = name
    return forward


@dataclass(frozen=True)
class _PendingTakeover:
    previous_environment: Mapping[str, str]
    # The turn that was in progress there, which this command refuses and cancels.
    taken_over_turn_id: str | None


class SessionOpener:
    """Open and leave one session at a time for a command.

    A session of the harness's Flow type runs on its own task queue. Its lease
    is read first, without a Worker; only when no other terminal holds it does
    the session Worker pull the session's task queue and acquire the lease
    through the RPC that Worker serves. A session from before 0.6.7 keeps its
    environment-hashed Flow type and the default task queue, which the
    command's other Worker always pulls.
    """

    def __init__(
        self,
        *,
        client: AsyncClient,
        harness_flow_type: str,
        session_flow: AgentSessionFlow,
        legacy_session_flow: AgentSessionFlow,
        command_name: str,
        holder: SessionLeaseHolder,
        environment: Mapping[str, str],
        session_worker: SessionQueueWorker,
        turn_refusal: TakenOverTurnRefusal,
    ) -> None:
        """Open sessions through one Client and its two session Flows.

        Args:
            client: The command's Client, whose Registry has both Flows.
            harness_flow_type: The harness's Flow type, of every new session.
            session_flow: The session Flow of ``harness_flow_type``.
            legacy_session_flow: The session Flow of this execution
                environment's environment-hashed Flow type.
            command_name: ``dex-agents-<harness>``, for errors.
            holder: This process's lease holder.
            environment: This command's environment, which the session keeps
                as where it was last open.
            session_worker: The Worker of the open session's task queue.
            turn_refusal: The refusal in front of ``session_flow``'s runner.
        """
        self._client = client
        self._harness_flow_type = harness_flow_type
        self._legacy_flow_type = legacy_session_flow.get_flow_type()
        self._sessions = AgentSessionClient(client, session_flow)
        self._legacy_sessions = AgentSessionClient(client, legacy_session_flow)
        self._command_name = command_name
        self._holder = holder
        self._environment = dict(environment)
        self._session_worker = session_worker
        self._turn_refusal = turn_refusal
        self._opened_sessions: dict[str, OpenableSession] = {}
        self._pending_takeovers: dict[str, _PendingTakeover] = {}

    @property
    def holder(self) -> SessionLeaseHolder:
        """Return this process's lease holder."""
        return self._holder

    @property
    def sessions(self) -> AgentSessionClient:
        """Return the session client of the harness's Flow type."""
        return self._sessions

    async def describe_openable_session(self, session_id: str) -> OpenableSession:
        """Return the session to open, once ``DescribeFlow`` shows a Flow type this command serves.

        Raises:
            UnopenableSessionError: If the session does not exist, does not
                run, or belongs to another harness or to another execution
                environment's environment-hashed Flow type.
        """
        try:
            info = await self._client.describe_flow(session_id)
        except FlowNotFoundError as error:
            raise UnopenableSessionError(
                f"session {session_id} does not exist; list sessions with "
                f"{self._command_name} sessions"
            ) from error
        if info.flow_type == self._harness_flow_type:
            session = OpenableSession(session_id, info.flow_type, True, self._sessions)
        elif info.flow_type == self._legacy_flow_type:
            session = OpenableSession(session_id, info.flow_type, False, self._legacy_sessions)
        elif is_legacy_session_flow_type(self._harness_flow_type, info.flow_type):
            raise UnopenableSessionError(
                other_environment_legacy_session_error(session_id, info.flow_type)
            )
        else:
            raise UnopenableSessionError(
                other_harness_session_error(session_id, info.flow_type, self._command_name)
            )
        if info.status is not FlowStatus.RUNNING:
            raise UnopenableSessionError(
                f"session {session_id} is {info.status.value}, so it cannot continue"
            )
        return session

    def started_session(self, session_id: str) -> OpenableSession:
        """Return a session this command just started on its own task queue."""
        return OpenableSession(session_id, self._harness_flow_type, True, self._sessions)

    async def attempt(self, session: OpenableSession) -> SessionTerminalLeaseResult:
        """Try once to take a session's lease, as ``wait_for_session_lease`` asks.

        For a session on its own task queue, it reads the stored lease first.
        While another terminal's lease has not expired, it returns that lease
        and pulls nothing. Otherwise the session Worker pulls the session's
        task queue, and the acquire RPC runs on it; a refused or failed
        acquire stops the pulling again.

        Returns:
            The lease RPC's result, or a refusal that names the stored holder.

        Raises:
            SessionWorkerStartError: If the session Worker did not start.
            DexServiceError: If a Dex call failed.
            TimeoutError: If the lease call took longer than ``LEASE_CALL_TIMEOUT_SECONDS``.
        """
        session_id = session.session_id
        self._opened_sessions[session_id] = session
        if not session.has_session_task_queue:
            return await self._acquire(session.sessions, session_id)
        stored = await self._client.get_attributes(
            session_id, SESSION_TERMINAL_LEASE, SESSION_LAST_TERMINAL_ENVIRONMENT, AGENT_STATE
        )
        lease: SessionTerminalLease | None = stored.get(SESSION_TERMINAL_LEASE)
        last_environment: Mapping[str, str] | None = stored.get(SESSION_LAST_TERMINAL_ENVIRONMENT)
        state = stored.get(AGENT_STATE)
        is_held_here = lease is not None and lease.holder_id == self._holder.holder_id
        if lease is not None and not is_held_here and not is_lease_expired_here(lease):
            return SessionTerminalLeaseResult(is_acquired=False, lease=lease)
        is_takeover = (
            last_environment is not None
            and not is_held_here
            and is_other_session_place(last_environment, self._environment)
        )
        taken_over_turn_id = (
            state.active_turn_id if is_takeover and state is not None else None
        ) or None
        if taken_over_turn_id is not None:
            # Refused before the Worker pulls, so none of that turn's calls runs here.
            self._turn_refusal.refuse_turn(session_id, taken_over_turn_id)
        await self._session_worker.pull(session_task_queue(session_id))
        # A cancelled call may still have taken the lease; its release then needs this Worker.
        try:
            result = await self._acquire(self._sessions, session_id)
        except Exception:
            self._session_worker.stop()
            raise
        if not result.is_acquired:
            self._session_worker.stop()
            return result
        if is_takeover and last_environment is not None:
            self._pending_takeovers[session_id] = _PendingTakeover(
                dict(last_environment), taken_over_turn_id
            )
        return result

    async def renew(self, session_id: str) -> SessionTerminalLeaseResult:
        """Renew the lease of a session that ``attempt`` acquired.

        Raises:
            DexServiceError: If the call failed.
            TimeoutError: If it took longer than ``LEASE_CALL_TIMEOUT_SECONDS``.
        """
        return await self._acquire(self._sessions_of(session_id), session_id)

    async def complete_takeover(self, session_id: str) -> list[str]:
        """Finish a takeover from another place once the lease is held.

        It cancels the turn that was in progress there. A failed cancellation
        only leaves that turn refused in this process.

        Returns:
            The lines to print, none when the session was last open in this place.
        """
        takeover = self._pending_takeovers.pop(session_id, None)
        if takeover is None:
            return []
        lines = [
            build_takeover_line(
                session_id,
                describe_session_environment(takeover.previous_environment),
                describe_session_environment(self._environment),
            )
        ]
        turn_id = takeover.taken_over_turn_id
        try:
            if turn_id is not None and await self._read_active_turn_id(session_id) == turn_id:
                await asyncio.wait_for(
                    self._sessions.cancel_turn(session_id, turn_id),
                    _TURN_CANCELLATION_TIMEOUT_SECONDS,
                )
                # The chat must not show the cancelled turn's approvals, pending until it ends.
                await self._wait_for_turn_end(session_id, turn_id)
                lines.append(CANCELLED_TURN_LINE)
        except Exception as error:
            # The turn stays refused in this process, so none of its calls runs here.
            _LOGGER.warning("cannot cancel the taken over turn: session=%s: %s", session_id, error)
        return lines

    async def release(self, session_id: str) -> None:
        """Leave a session: release its lease while the session Worker still serves, then stop it.

        It takes at most ``LEASE_RELEASE_TIMEOUT_SECONDS``. A failed release is
        only logged: the lease expires anyway.
        """
        try:
            await asyncio.wait_for(
                self._sessions_of(session_id).release_session_terminal_lease(
                    session_id, self._holder.holder_id
                ),
                LEASE_RELEASE_TIMEOUT_SECONDS,
            )
        except (DexServiceError, TimeoutError) as error:
            _LOGGER.warning("cannot release the session lease: session=%s: %s", session_id, error)
        self.leave(session_id)

    def leave(self, session_id: str) -> None:
        """Stop pulling a session's task queue without releasing its lease."""
        session = self._opened_sessions.get(session_id)
        if (
            session is not None
            and session.has_session_task_queue
            and self._session_worker.task_queue == session_task_queue(session_id)
        ):
            self._session_worker.stop()

    async def _acquire(
        self, sessions: AgentSessionClient, session_id: str
    ) -> SessionTerminalLeaseResult:
        return await asyncio.wait_for(
            sessions.acquire_session_terminal_lease(
                session_id, self._holder.acquire_request(self._environment)
            ),
            LEASE_CALL_TIMEOUT_SECONDS,
        )

    def _sessions_of(self, session_id: str) -> AgentSessionClient:
        session = self._opened_sessions.get(session_id)
        return self._sessions if session is None else session.sessions

    async def _read_active_turn_id(self, session_id: str) -> str | None:
        stored = await self._client.get_attributes(session_id, AGENT_STATE)
        state = stored.get(AGENT_STATE)
        return None if state is None else state.active_turn_id or None

    async def _wait_for_turn_end(self, session_id: str, turn_id: str) -> None:
        """Wait a bounded time for a turn to end; a tool call that runs on keeps it going."""
        loop = asyncio.get_running_loop()
        deadline = loop.time() + _TURN_END_TIMEOUT_SECONDS
        while loop.time() < deadline and await self._read_active_turn_id(session_id) == turn_id:
            await asyncio.sleep(_TURN_END_POLL_SECONDS)


def is_lease_expired_here(lease: SessionTerminalLease, now: datetime | None = None) -> bool:
    """Return whether a stored lease expired by this host's clock.

    An expiry that does not parse, or has no time zone, counts as expired, so a
    damaged lease never blocks a session.

    Args:
        lease: The stored lease.
        now: This host's current time; ``None`` reads the clock.
    """
    try:
        expires_at = datetime.fromisoformat(lease.expires_at)
    except ValueError:
        return True
    if expires_at.tzinfo is None:
        return True
    return expires_at <= (now or datetime.now(expires_at.tzinfo))
