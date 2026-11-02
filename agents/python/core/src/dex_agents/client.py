# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Typed Client calls for agent sessions."""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from datetime import timedelta

from dex import AsyncClient, RpcLockConflictError, StartFlowOptions, StreamMessage

from dex_agents.session_flow import AgentSessionFlow
from dex_agents.values import (
    AgentEvent,
    AgentMessagesPage,
    AgentSessionStartInput,
    AgentSnapshot,
    AgentUserMessage,
    AnswerQuestionRequest,
    ListAgentMessagesRequest,
    ResolveInterruptRequest,
)

_LOCK_CONFLICT_RETRY_INTERVAL_SECONDS = 0.05


class AgentSessionClient:
    """Start agent sessions and call their RPCs with typed arguments.

    Wrap the application's ``AsyncClient`` and the registered
    ``AgentSessionFlow``. Every method is a thin call through the Client; the
    session ID is the Flow ID.

    Example:
        >>> sessions = AgentSessionClient(client, flow)
        >>> await sessions.start_session("session-42")
        >>> await sessions.send_user_message("session-42", "Refund order 7")
        >>> snapshot = await sessions.get_agent_snapshot("session-42")
        >>> for interrupt in snapshot.pending_interrupts:
        ...     await sessions.resolve_interrupt("session-42", interrupt.interrupt_id,
        ...                                      approved=True)
    """

    def __init__(
        self,
        client: AsyncClient,
        flow: AgentSessionFlow,
        *,
        lock_conflict_retry_timeout: timedelta = timedelta(seconds=5),
    ) -> None:
        """Create a typed view over one agent's sessions.

        Args:
            client: An open ``AsyncClient`` whose Registry contains ``flow``.
                The caller keeps ownership and closes it.
            flow: The registered ``AgentSessionFlow`` instance.
            lock_conflict_retry_timeout: How long ``resolve_interrupt`` and
                ``answer_question`` retry an ``RpcLockConflictError``. Defaults
                to five seconds.
        """
        self._client = client
        self._flow = flow
        self._lock_conflict_retry_timeout = lock_conflict_retry_timeout

    @property
    def flow(self) -> AgentSessionFlow:
        """Return the agent session Flow these calls target.

        Returns:
            The Flow passed to the constructor.
        """
        return self._flow

    async def start_session(
        self,
        session_id: str,
        initial_message: str | None = None,
        options: StartFlowOptions | None = None,
    ) -> str:
        """Start one agent session.

        Args:
            session_id: Flow ID of the new session.
            initial_message: Optional first user message.
            options: Start options, such as a Request ID for an idempotent start.
                ``None`` uses ``StartFlowOptions()``.

        Returns:
            The server-assigned run ID.

        Raises:
            FlowAlreadyStartedError: If the Flow ID is already in use.
        """
        return await self._client.start_flow(
            self._flow,
            session_id,
            AgentSessionStartInput(initial_message=initial_message),
            options or StartFlowOptions(),
        )

    async def send_user_message(self, session_id: str, text: str) -> bool:
        """Queue one user message.

        Args:
            session_id: Running session.
            text: Message text.

        Returns:
            ``True`` when queued; ``False`` for whitespace-only text.

        Raises:
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        return await self._client.invoke_rpc(
            self._flow.send_user_message, session_id, AgentUserMessage(text=text)
        )

    async def resolve_interrupt(
        self,
        session_id: str,
        interrupt_id: str,
        *,
        approved: bool,
        rejection_message: str | None = None,
    ) -> bool:
        """Approve or reject one pending interrupt.

        The call retries ``RpcLockConflictError`` until
        ``lock_conflict_retry_timeout`` passes. A retry is safe because a
        conflicting call makes no change.

        Args:
            session_id: Running session.
            interrupt_id: ``AgentInterrupt.interrupt_id``.
            approved: ``True`` to run the tool call.
            rejection_message: Message the model sees after a rejection.

        Returns:
            ``True`` when resolved; ``False`` when it is unknown, already resolved,
            or past its deadline.

        Raises:
            RpcLockConflictError: If the lock stays busy past the retry timeout.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        request = ResolveInterruptRequest(
            interrupt_id=interrupt_id, approved=approved, rejection_message=rejection_message
        )
        return await self._retry_lock_conflicts(
            lambda: self._client.invoke_rpc(self._flow.resolve_interrupt, session_id, request)
        )

    async def answer_question(self, session_id: str, interrupt_id: str, answer: str) -> bool:
        """Answer one pending ``question`` interrupt.

        The call retries ``RpcLockConflictError`` until
        ``lock_conflict_retry_timeout`` passes. A retry is safe because a
        conflicting call makes no change. Decline a question with
        ``resolve_interrupt(..., approved=False)``.

        Example:
            >>> snapshot = await sessions.get_agent_snapshot("session-42")
            >>> question = next(i for i in snapshot.pending_interrupts
            ...                 if i.kind is InterruptKind.QUESTION)
            >>> await sessions.answer_question("session-42", question.interrupt_id,
            ...                                question.choices[0])

        Args:
            session_id: Running session.
            interrupt_id: ``AgentInterrupt.interrupt_id`` of the question.
            answer: Answer text. It must not be blank, and it must be one of
                ``choices`` when the question does not allow free text.

        Returns:
            ``True`` when answered; ``False`` when the question is unknown,
            already resolved, past its deadline, or not a question, or when the
            answer is blank or not an allowed choice.

        Raises:
            RpcLockConflictError: If the lock stays busy past the retry timeout.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        request = AnswerQuestionRequest(interrupt_id=interrupt_id, answer=answer)
        return await self._retry_lock_conflicts(
            lambda: self._client.invoke_rpc(self._flow.answer_question, session_id, request)
        )

    async def _retry_lock_conflicts(self, invoke: Callable[[], Awaitable[bool]]) -> bool:
        loop = asyncio.get_running_loop()
        deadline = loop.time() + self._lock_conflict_retry_timeout.total_seconds()
        while True:
            try:
                return await invoke()
            except RpcLockConflictError:
                if loop.time() >= deadline:
                    raise
                await asyncio.sleep(_LOCK_CONFLICT_RETRY_INTERVAL_SECONDS)

    async def get_agent_snapshot(self, session_id: str) -> AgentSnapshot:
        """Return the session snapshot a UI renders.

        Args:
            session_id: Running or retained closed session.

        Returns:
            The snapshot from ``get_agent_snapshot``.
        """
        return await self._client.invoke_rpc(self._flow.get_agent_snapshot, session_id)

    async def list_agent_messages(
        self,
        session_id: str,
        *,
        before_sequence: int | None = None,
        limit: int = 50,
    ) -> AgentMessagesPage:
        """Return one page of stored messages, oldest first.

        Args:
            session_id: Running or retained closed session.
            before_sequence: Anchor from the previous page, or ``None`` for the newest page.
            limit: Page size, clamped to 1 through 200.

        Returns:
            The page from ``list_agent_messages``.
        """
        return await self._client.invoke_rpc(
            self._flow.list_agent_messages,
            session_id,
            ListAgentMessagesRequest(before_sequence=before_sequence, limit=limit),
        )

    async def read_agent_event(
        self,
        session_id: str,
        resume_token: str = "",
        timeout: timedelta | None = None,
    ) -> StreamMessage[AgentEvent]:
        """Wait for the next ``AgentEvents`` message after a resume token.

        Pass each returned ``resume_token`` to the next call. Events are best
        effort: after a gap, rebuild from ``get_agent_snapshot``.

        Args:
            session_id: Session whose events to read.
            resume_token: Token from the previous message, or ``""`` for the
                oldest retained event.
            timeout: Server-side long-poll duration, or ``None`` for the default.

        Returns:
            The next event and its resume token.

        Raises:
            LongPollTimeoutError: If no event arrives before the long poll ends.
        """
        return await self._client.read_stream(
            session_id, self._flow.agent_events, resume_token, timeout
        )
