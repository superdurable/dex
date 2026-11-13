# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The launcher side of the terminal UI's local requests: keys, the in-flight limit, and cancel.

The launcher keeps the one key table for every method, including the ones it
relays to the Worker host, so the table outlives a Worker host that stops.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import re
import time
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from typing import Any

from dex_agents_cli_core.dex_connection import describe_dex_refusal_without_details
from dex_agents_cli_core.terminal_ui.contract import (
    GET_REQUEST_RESULT,
    MAX_IN_FLIGHT_REQUESTS,
    REMEMBERED_KEY_LIMIT,
    REMEMBERED_KEY_SECONDS,
    IdempotencyKeyError,
    idempotency_key_time_ms,
    invalid_params_message,
)

_MAX_REQUEST_ID = 2**53 - 1
#: Error codes that end a wait or reject a request; a key never remembers them.
NEVER_REMEMBERED_CODES = frozenset(
    {"invalid_params", "unknown_method", "busy", "cancelled", "timeout", "host_stopped"}
)
_INVALID_KEY_MESSAGE = "The idempotency key is not a lowercase UUIDv7."
_METHOD_NAME = re.compile(r"[a-z][a-z0-9_]*")
_LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class RequestOutcome:
    """What one keyed operation ended with: a result object or an error.

    Attributes:
        result: The method's result, or ``None`` for an error.
        error: ``{"code", "message"}``, or ``None`` for a result.
        is_remembered: ``False`` for an outcome that is not the operation's,
            such as ``host_stopped``, so ``get_request_result`` answers
            ``unknown`` for its key.
    """

    result: Mapping[str, Any] | None = None
    error: Mapping[str, str] | None = None
    is_remembered: bool = True

    @classmethod
    def succeeded(cls, result: Mapping[str, Any]) -> RequestOutcome:
        """Return a result outcome."""
        return cls(result=result)

    @classmethod
    def failed(cls, code: str, message: str, *, is_remembered: bool = True) -> RequestOutcome:
        """Return an error outcome with a code and one English sentence."""
        return cls(error={"code": code, "message": message}, is_remembered=is_remembered)

    def response_fields(self) -> dict[str, Any]:
        """Return the ``result`` or ``error`` field of a response."""
        return {"error": dict(self.error)} if self.error is not None else {"result": self.result}


#: Runs one keyed operation: its params and its idempotency key.
RequestHandler = Callable[[Mapping[str, Any], str], Awaitable[RequestOutcome]]


@dataclass
class _KeyedOperation:
    method: str
    params: Mapping[str, Any]
    task: asyncio.Task[RequestOutcome]
    outcome: RequestOutcome | None = None


class IdempotencyTable:
    """The keys the launcher side remembers, as contract 1 bounds them.

    A running key is never forgotten. A finished key is forgotten 10 minutes
    after it finished, or earlier, the first finished first, while more than
    256 keys are kept. Times are in seconds of one monotonic clock.
    """

    def __init__(
        self,
        capacity: int = REMEMBERED_KEY_LIMIT,
        retention_seconds: float = REMEMBERED_KEY_SECONDS,
    ) -> None:
        """Make an empty table with the contract's limits, or a test's."""
        self._capacity = capacity
        self._retention_seconds = retention_seconds
        self._finished_at: dict[str, float | None] = {}

    def arrive(self, key: str, now: float) -> None:
        """Record a running key that the table does not hold."""
        self._forget_expired(now)
        self._finished_at.setdefault(key, None)
        self._forget_over_capacity()

    def finish(self, key: str, now: float) -> None:
        """Record that a running key's operation ended."""
        if key in self._finished_at:
            self._finished_at[key] = now
            self._forget_over_capacity()

    def forget(self, key: str) -> None:
        """Drop a key, so a retry is handled afresh."""
        self._finished_at.pop(key, None)

    def holds(self, key: str, now: float) -> bool:
        """Return whether the table still remembers a key."""
        return self.state(key, now) != "unknown"

    def state(self, key: str, now: float) -> str:
        """Return ``running``, ``done``, or ``unknown`` for a key."""
        self._forget_expired(now)
        if key not in self._finished_at:
            return "unknown"
        return "running" if self._finished_at[key] is None else "done"

    def _forget_expired(self, now: float) -> None:
        for key, finished_at in list(self._finished_at.items()):
            if finished_at is not None and now - finished_at >= self._retention_seconds:
                del self._finished_at[key]

    def _forget_over_capacity(self) -> None:
        excess = len(self._finished_at) - self._capacity
        if excess <= 0:
            return
        finished = sorted(
            (finished_at, key)
            for key, finished_at in self._finished_at.items()
            if finished_at is not None
        )
        for _, key in finished[:excess]:
            del self._finished_at[key]


@dataclass
class _WaitingResponse:
    method: str
    is_answered: bool = False
    waiter: asyncio.Task[None] | None = field(default=None, repr=False)


class LocalRequestServer:
    """Answer the terminal UI's requests: each gets exactly one response.

    A method without a handler gets ``unknown_method``. A fifth waiting
    request gets ``busy``. A known key returns the first outcome. ``cancel``
    answers ``cancelled`` at once and lets the operation finish under its key.
    """

    def __init__(
        self,
        handlers: Mapping[str, RequestHandler],
        send: Callable[[Mapping[str, Any]], Awaitable[None]],
        *,
        table: IdempotencyTable | None = None,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        """Serve the methods in ``handlers`` and send each response with ``send``."""
        self._handlers = dict(handlers)
        self._send = send
        self._table = table or IdempotencyTable()
        self._clock = clock
        self._operations: dict[str, _KeyedOperation] = {}
        self._waiting: dict[int, _WaitingResponse] = {}

    async def handle_message(self, message: Mapping[str, Any]) -> None:
        """Handle a ``request`` or ``cancel``; other types belong to the caller.

        A request or cancel without a usable ``id`` is malformed: it is dropped
        without an answer, and the channel stays open.
        """
        request_id = _request_id(message)
        if request_id is None:
            _LOGGER.warning("dropped a %s message without a usable id", message["type"])
        elif message["type"] == "cancel":
            await self._cancel(request_id)
        elif message["type"] == "request":
            await self._handle_request(request_id, message)

    async def close(self) -> None:
        """Stop waiting for every outcome; operations already running finish on their own."""
        waiters = [waiting.waiter for waiting in self._waiting.values() if waiting.waiter]
        for waiter in waiters:
            waiter.cancel()
        for waiter in waiters:
            with contextlib.suppress(asyncio.CancelledError):
                await waiter

    async def _handle_request(self, request_id: int, message: Mapping[str, Any]) -> None:
        method = message.get("method")
        params = message.get("params")
        if not isinstance(method, str) or not isinstance(params, dict):
            await self._reject(request_id, "invalid_params", "The request has no method or params.")
            return
        if method == GET_REQUEST_RESULT:
            if "idempotency_key" in message:
                await self._reject(
                    request_id, "invalid_params", "get_request_result takes its key in params."
                )
                return
            await self._answer_request_result(request_id, params)
            return
        # A malformed request is invalid_params even for a method this command lacks.
        if _METHOD_NAME.fullmatch(method) is None:
            await self._reject(request_id, "invalid_params", "The method name is malformed.")
            return
        if "idempotency_key" not in message:
            await self._reject(request_id, "invalid_params", "The request has no idempotency_key.")
            return
        key = _valid_key(message.get("idempotency_key"))
        if key is None:
            await self._reject(request_id, "invalid_params", _INVALID_KEY_MESSAGE)
            return
        handler = self._handlers.get(method)
        if handler is None:
            await self._reject(
                request_id, "unknown_method", f"This command does not serve {method}."
            )
            return
        problem = invalid_params_message(method, params)
        if problem is not None:
            await self._reject(request_id, "invalid_params", problem)
            return
        if self._count_waiting() >= MAX_IN_FLIGHT_REQUESTS:
            await self._reject(
                request_id,
                "busy",
                f"{MAX_IN_FLIGHT_REQUESTS} local requests are already running.",
            )
            return
        operation = self._known_operation(key)
        if operation is not None and (operation.method != method or operation.params != params):
            await self._reject(
                request_id, "invalid_params", "This idempotency key belongs to another request."
            )
            return
        if operation is None:
            operation = self._start_operation(key, method, params, handler)
        waiting = _WaitingResponse(method)
        self._waiting[request_id] = waiting
        waiting.waiter = asyncio.create_task(self._answer_when_done(request_id, waiting, operation))

    def _known_operation(self, key: str) -> _KeyedOperation | None:
        if self._table.state(key, self._clock()) == "unknown":
            self._operations.pop(key, None)
            return None
        return self._operations.get(key)

    def _start_operation(
        self, key: str, method: str, params: Mapping[str, Any], handler: RequestHandler
    ) -> _KeyedOperation:
        now = self._clock()
        self._table.arrive(key, now)
        for forgotten in [known for known in self._operations if not self._table.holds(known, now)]:
            del self._operations[forgotten]
        task = asyncio.create_task(_run_handler(handler, method, params, key))
        operation = _KeyedOperation(method, params, task)
        self._operations[key] = operation
        task.add_done_callback(lambda _: self._record_outcome(key, operation))
        return operation

    def _record_outcome(self, key: str, operation: _KeyedOperation) -> None:
        if operation.task.cancelled():
            outcome = RequestOutcome.failed(
                "cancelled", f"Stopped {operation.method}.", is_remembered=False
            )
        else:
            outcome = operation.task.result()
        operation.outcome = outcome
        if self._operations.get(key) is not operation:
            return
        is_wait_error = (
            outcome.error is not None and outcome.error["code"] in NEVER_REMEMBERED_CODES
        )
        if outcome.is_remembered and not is_wait_error:
            self._table.finish(key, self._clock())
        else:
            self._table.forget(key)
            del self._operations[key]

    async def _answer_when_done(
        self, request_id: int, waiting: _WaitingResponse, operation: _KeyedOperation
    ) -> None:
        with contextlib.suppress(asyncio.CancelledError):
            await asyncio.wait({operation.task})
        if waiting.is_answered:
            self._waiting.pop(request_id, None)
            return
        waiting.is_answered = True
        self._waiting.pop(request_id, None)
        outcome = operation.outcome
        if outcome is None:
            return
        await self._send({"type": "response", "id": request_id, **outcome.response_fields()})

    async def _cancel(self, request_id: int) -> None:
        waiting = self._waiting.get(request_id)
        if waiting is None or waiting.is_answered:
            return
        waiting.is_answered = True
        await self._send(
            {
                "type": "response",
                "id": request_id,
                "error": {"code": "cancelled", "message": f"Stopped waiting for {waiting.method}."},
            }
        )

    async def _answer_request_result(self, request_id: int, params: Mapping[str, Any]) -> None:
        key = _valid_key(params.get("idempotency_key"))
        if key is None:
            await self._reject(request_id, "invalid_params", _INVALID_KEY_MESSAGE)
            return
        operation = self._known_operation(key)
        result: dict[str, Any]
        if operation is None:
            result = {"state": "unknown"}
        elif operation.outcome is None:
            result = {"state": "running"}
        else:
            result = {"state": "done", **operation.outcome.response_fields()}
        await self._send({"type": "response", "id": request_id, "result": result})

    async def _reject(self, request_id: int, code: str, message: str) -> None:
        await self._send(
            {"type": "response", "id": request_id, "error": {"code": code, "message": message}}
        )

    def _count_waiting(self) -> int:
        return sum(1 for waiting in self._waiting.values() if not waiting.is_answered)


async def _run_handler(
    handler: RequestHandler, method: str, params: Mapping[str, Any], key: str
) -> RequestOutcome:
    try:
        return await handler(params, key)
    except Exception as error:  # A handler bug must still answer the request once.
        refused = describe_dex_refusal_without_details(error)
        if refused is None:
            _LOGGER.exception("local request failed: method=%s", method)
        else:
            _LOGGER.error("local request failed: method=%s: %s", method, refused)
        return RequestOutcome.failed(
            "request_failed", f"{method} failed; see the log.", is_remembered=False
        )


def _valid_key(value: object) -> str | None:
    try:
        idempotency_key_time_ms(value)
    except IdempotencyKeyError:
        return None
    return value if isinstance(value, str) else None


def _request_id(message: Mapping[str, Any]) -> int | None:
    request_id = message.get("id")
    if (
        not isinstance(request_id, int)
        or isinstance(request_id, bool)
        or not 1 <= request_id <= _MAX_REQUEST_ID
    ):
        return None
    return request_id
