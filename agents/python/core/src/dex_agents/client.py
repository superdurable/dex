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
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import replace
from datetime import timedelta
from typing import Any, TypeVar

from dex import (
    ActiveStepSearchMode,
    AsyncClient,
    FlowConfig,
    FlowNotActiveOrNotFoundError,
    RpcLockConflictError,
    StartFlowOptions,
    StreamMessage,
)

from dex_agents.media import validate_image_references
from dex_agents.session_failure import (
    AgentSessionFailedError,
    read_agent_snapshot,
    read_session_failure,
)
from dex_agents.session_flow import AgentSessionFlow
from dex_agents.session_settings import validate_session_settings
from dex_agents.values import (
    AcquireSessionTerminalLeaseRequest,
    AgentEvent,
    AgentImageReference,
    AgentMessagesPage,
    AgentSessionStartInput,
    AgentSnapshot,
    AgentUserMessage,
    AnswerQuestionRequest,
    CancelTurnRequest,
    DeleteQueuedMessageRequest,
    ExecutePlanRequest,
    ListAgentMessagesRequest,
    ReleaseSessionTerminalLeaseRequest,
    ResolveInterruptRequest,
    SessionTerminalLeaseResult,
    SteerUserMessageRequest,
    UpdateSessionSettingsRequest,
)

_LOCK_CONFLICT_RETRY_INTERVAL_SECONDS = 0.05
_Output = TypeVar("_Output")


#: The search mode ``AgentSessionClient.start_session`` gives a new session.
#:
#: It indexes the Steps that wait, such as ``AwaitToolApproval``,
#: ``AwaitQuestionAnswer``, and ``AwaitUserMessage``, and leaves out Steps that
#: run straight away, such as ``RunModel``. So a session's search entry changes
#: when it enters or leaves a wait, not on every Step.
#:
#: Pass it in ``FlowConfig`` to ``AsyncClient.start_flow``, to a SubFlow's
#: ``config_override``, or to ``AsyncClient.update_flow_config`` for a running
#: session.
#:
#: .. versionadded:: 0.3.0
AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE = ActiveStepSearchMode.WITH_WAIT_FOR


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
            lock_conflict_retry_timeout: How long ``resolve_interrupt``,
                ``answer_question``, ``execute_plan``, and ``cancel_turn``
                retry an ``RpcLockConflictError``. Defaults to five seconds.
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
        *,
        session_settings: dict[str, Any] | None = None,
        start_environment: dict[str, str] | None = None,
    ) -> str:
        """Start one agent session.

        The session is searchable by the Steps it waits in: ``ActiveStepTypes =
        "AwaitToolApproval"`` finds it while a tool call waits for a person.
        This sets ``FlowConfig.active_step_search_mode`` to
        ``AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE`` unless ``options`` sets that
        field. Set it to ``ActiveStepSearchMode.DISABLED`` to opt out.

        ``AsyncClient.start_flow`` does not apply this default, so pass
        ``AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE`` there yourself. A session
        started as a SubFlow inherits its parent's mode unless its
        ``config_override`` sets one. To turn the search on for a session that
        is already running, call ``AsyncClient.update_flow_config``.

        Example:
            >>> await sessions.start_session(
            ...     "session-42", "Hello", session_settings={"model": "gpt-5-mini"})
            >>> page = await client.search_flows('ActiveStepTypes = "AwaitToolApproval"', 50)

        .. versionchanged:: 0.3.0
            New sessions are searchable by active Step type.

        .. versionchanged:: 0.5.0
            Added ``start_environment``.

        Args:
            session_id: Flow ID of the new session.
            initial_message: Optional first user message.
            options: Start options, such as a Request ID for an idempotent start.
                ``None`` uses ``StartFlowOptions()``. ``config_override`` keeps
                every field it sets.
            session_settings: The session's settings, which every turn's
                ``AgentTurnContext.session_settings`` carries until
                ``update_session_settings`` replaces them. ``None``, the
                default, starts without settings. Never put a secret in them.
            start_environment: Where the caller runs, such as its host name
                and workspace, recorded in the session's start input as
                ``AgentSessionStartInput.start_environment``. The session never
                reads it. ``None``, the default, records nothing. Never put a
                secret in it.

        Returns:
            The server-assigned run ID.

        Raises:
            ValueError: If ``session_settings`` is not a JSON object or is
                larger than ``MAX_SESSION_SETTINGS_BYTES``. Nothing is started.
            FlowAlreadyStartedError: If the Flow ID is already in use.
        """
        if session_settings is not None:
            validate_session_settings(session_settings)
        return await self._client.start_flow(
            self._flow,
            session_id,
            AgentSessionStartInput(
                initial_message=initial_message,
                session_settings=session_settings,
                start_environment=start_environment,
            ),
            _with_active_step_search(options or StartFlowOptions()),
        )

    async def update_session_settings(
        self, session_id: str, session_settings: dict[str, Any]
    ) -> bool:
        """Replace the session's settings from the next turn on.

        A running turn keeps the settings it started with. The settings
        replace the old ones; ``{}`` clears them.

        Example:
            >>> await sessions.update_session_settings("session-42", {"model": "gpt-5"})

        Args:
            session_id: Running session.
            session_settings: The complete new settings. Never put a secret in them.

        Returns:
            ``True`` when stored; ``False`` when the session has not
            initialized yet.

        Raises:
            ValueError: If ``session_settings`` is not a JSON object or is
                larger than ``MAX_SESSION_SETTINGS_BYTES``. Nothing is sent.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        validate_session_settings(session_settings)
        return await self._client.invoke_rpc(
            self._flow.update_session_settings,
            session_id,
            UpdateSessionSettingsRequest(session_settings=session_settings),
        )

    async def send_user_message(
        self,
        session_id: str,
        text: str,
        *,
        plan_mode: bool = False,
        images: Sequence[AgentImageReference] = (),
    ) -> bool:
        """Queue one user message.

        Example:
            >>> reference = await media_store.put_image(Path("chart.png").read_bytes())
            >>> await sessions.send_user_message("session-42", "What does this chart show?",
            ...                                  images=[reference])

        Args:
            session_id: Running session.
            text: Message text.
            plan_mode: ``True`` to start a planning turn that drafts a plan for
                ``execute_plan``. It needs ``AgentSessionOptions.plan_mode_enabled``.
            images: Images uploaded with ``MediaStore.put_image``, at most
                ``MAX_IMAGES_PER_MESSAGE``. Defaults to none.

                .. versionadded:: 0.5.0

        Returns:
            ``True`` when queued; ``False`` for whitespace-only text, for a
            ``plan_mode`` message when plan mode is not enabled, or for images
            when the session's runner does not implement
            ``ImageUserMessageBuilder``.

        Raises:
            ImageValidationError: If ``validate_image_references`` refuses the
                images. Nothing was sent.
            AgentSessionFailedError: If the session failed. ``reason`` says why.
            FlowNotActiveOrNotFoundError: If the session is not running.
            DexServiceError: With ``RESOURCE_EXHAUSTED`` when the message does
                not fit under ``limits.maxFlowStateBytes``. Nothing was queued,
                and the session stays as it was.
        """
        validate_image_references(images)
        return await self._raise_session_failure(
            session_id,
            self._client.invoke_rpc(
                self._flow.send_user_message,
                session_id,
                AgentUserMessage(text=text, plan_mode=plan_mode, images=list(images)),
            ),
        )

    async def execute_plan(self, session_id: str, revision: int) -> bool:
        """Approve one plan revision, so the session starts a turn that runs it.

        The call retries ``RpcLockConflictError`` until
        ``lock_conflict_retry_timeout`` passes. A retry is safe because a
        conflicting call makes no change.

        Example:
            >>> snapshot = await sessions.get_agent_snapshot("session-42")
            >>> if snapshot.plan is not None and snapshot.plan.status is PlanStatus.DRAFT:
            ...     await sessions.execute_plan("session-42", snapshot.plan.revision)

        Args:
            session_id: Running session.
            revision: ``AgentPlan.revision`` the user reviewed.

        Returns:
            ``True`` when accepted; ``False`` when the revision is not the
            current ``draft`` or ``active`` plan, an execution of it is already
            pending, or the session is not idle with nothing queued or pending.

        Raises:
            RpcLockConflictError: If the lock stays busy past the retry timeout.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        request = ExecutePlanRequest(revision=revision)
        return await self._retry_lock_conflicts(
            lambda: self._client.invoke_rpc(self._flow.execute_plan, session_id, request)
        )

    async def steer_user_message(
        self,
        session_id: str,
        *,
        text: str | None = None,
        message_id: str | None = None,
    ) -> bool:
        """Steer the running turn with new text or with a queued message.

        Pass exactly one of ``text`` and ``message_id``. The session applies
        the steered message at the turn's next safe Step boundary: it lets a
        running model call or tool call finish, abandons the tool calls that
        have not started, and starts a new turn with the steered messages.

        Example:
            >>> await sessions.steer_user_message("session-42", text="Use staging instead")
            >>> snapshot = await sessions.get_agent_snapshot("session-42")
            >>> await sessions.steer_user_message(
            ...     "session-42", message_id=snapshot.queued_messages[0].message_id)

        Args:
            session_id: Running session.
            text: Text of a new steered message.
            message_id: ``PendingUserMessage.message_id`` of a queued message.

        While no turn is active, new ``text`` is queued like
        ``send_user_message`` and starts the next turn at once.

        Returns:
            ``True`` when steered, or queued while no turn is active; ``False``
            when both or neither argument is set, the text is blank, the
            message is no longer queued, or the active turn began on a Worker
            without steering.

        Raises:
            ChannelMessageNotFoundError: If the session consumed the queued
                message while the call ran; nothing changed, so reload the
                snapshot.
            AgentSessionFailedError: If the session failed. ``reason`` says why.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        return await self._raise_session_failure(
            session_id,
            self._client.invoke_rpc(
                self._flow.steer_user_message,
                session_id,
                SteerUserMessageRequest(message_id=message_id, text=text),
            ),
        )

    async def delete_queued_message(self, session_id: str, message_id: str) -> bool:
        """Delete one queued user message before it starts a turn.

        Args:
            session_id: Running session.
            message_id: ``PendingUserMessage.message_id`` from ``get_agent_snapshot``.

        Returns:
            ``True`` when deleted; ``False`` when the message is no longer queued.

        Raises:
            ChannelMessageNotFoundError: If the session consumed the message
                while the call ran; nothing changed.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        return await self._client.invoke_rpc(
            self._flow.delete_queued_message,
            session_id,
            DeleteQueuedMessageRequest(message_id=message_id),
        )

    async def cancel_turn(self, session_id: str, turn_id: str | None = None) -> bool:
        """Stop the running turn now.

        A running model call stops, and waits for an approval, an answer, a
        Timer, or a remote task end. A tool call that has started runs to the
        end first. A model call that already committed its final answer
        completes the turn. The call retries ``RpcLockConflictError``, which
        another ``cancel_turn`` or a short session Step causes, until
        ``lock_conflict_retry_timeout`` passes.

        Args:
            session_id: Running session.
            turn_id: The turn to stop, or ``None`` for the active turn.

        Returns:
            ``True`` when the request is recorded; ``False`` when no turn is
            active, ``turn_id`` names another turn, or the active turn began
            on a Worker without steering.

        Raises:
            RpcLockConflictError: If the lock stays busy past the retry timeout.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        request = CancelTurnRequest(turn_id=turn_id)
        return await self._retry_lock_conflicts(
            lambda: self._client.invoke_rpc(self._flow.cancel_turn, session_id, request)
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

    async def acquire_session_terminal_lease(
        self, session_id: str, request: AcquireSessionTerminalLeaseRequest
    ) -> SessionTerminalLeaseResult:
        """Acquire or renew the lease that marks which terminal has the session open.

        A chat client calls it before it shows the session, and again before
        ``request.ttl_seconds`` pass, with the same ``holder_id``. When another
        holder has an unexpired lease, the result names it and nothing
        changes, so the caller can wait and try again. Dex never enforces the
        lease itself. The call retries ``RpcLockConflictError`` until
        ``lock_conflict_retry_timeout`` passes.

        .. versionadded:: 0.6.3

        Example:
            >>> request = AcquireSessionTerminalLeaseRequest(
            ...     holder_id=uuid.uuid4().hex, host_name=socket.gethostname(),
            ...     process_id=os.getpid(), ttl_seconds=15)
            >>> result = await sessions.acquire_session_terminal_lease("session-42", request)
            >>> if not result.is_acquired and result.lease is not None:
            ...     print(f"open on {result.lease.host_name}, pid {result.lease.process_id}")

        Args:
            session_id: Running session.
            request: The holder and how long the lease lasts.

        Returns:
            The outcome: whether the caller holds the lease, and the lease as
            it is now. See ``AgentSessionFlow.acquire_session_terminal_lease``.

        Raises:
            RpcLockConflictError: If the lock stays busy past the retry timeout.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        return await self._retry_lock_conflicts(
            lambda: self._client.invoke_rpc(
                self._flow.acquire_session_terminal_lease, session_id, request
            )
        )

    async def release_session_terminal_lease(self, session_id: str, holder_id: str) -> bool:
        """Release the terminal lease that ``holder_id`` holds.

        Call it when the chat client stops showing the session, so a waiting
        client can open it at once instead of after the lease expires. The
        call retries ``RpcLockConflictError`` until
        ``lock_conflict_retry_timeout`` passes.

        .. versionadded:: 0.6.3

        Args:
            session_id: Running session.
            holder_id: The ``holder_id`` that acquired the lease.

        Returns:
            ``True`` when the lease is released; ``False`` when the session has
            no lease or another holder has it.

        Raises:
            RpcLockConflictError: If the lock stays busy past the retry timeout.
            FlowNotActiveOrNotFoundError: If the session is not running.
        """
        request = ReleaseSessionTerminalLeaseRequest(holder_id=holder_id)
        return await self._retry_lock_conflicts(
            lambda: self._client.invoke_rpc(
                self._flow.release_session_terminal_lease, session_id, request
            )
        )

    async def _raise_session_failure(self, session_id: str, call: Awaitable[bool]) -> bool:
        try:
            return await call
        except FlowNotActiveOrNotFoundError as error:
            reason = await read_session_failure(self._client, session_id)
            if reason is None:
                raise
            raise AgentSessionFailedError(session_id, reason, error) from error

    async def _retry_lock_conflicts(self, invoke: Callable[[], Awaitable[_Output]]) -> _Output:
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
            The snapshot from ``get_agent_snapshot``. When the session's Flow
            failed, it has status ``FAILED`` and the Flow's reason, even if the
            session had no room left to store them.
        """
        return await read_agent_snapshot(self._client, self._flow, session_id)

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


def _with_active_step_search(options: StartFlowOptions) -> StartFlowOptions:
    config = options.config_override or FlowConfig()
    if config.active_step_search_mode is not None:
        return options
    return replace(
        options,
        config_override=replace(
            config, active_step_search_mode=AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE
        ),
    )
