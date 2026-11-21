# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Backoff for model calls that fail with a transient model API error."""

from __future__ import annotations

import math
import random as _random
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from email.utils import parsedate_to_datetime
from enum import Enum
from typing import Any, TypeVar

__all__ = ["ModelRetryPolicy"]

_Result = TypeVar("_Result")
# Dex takes Step durations as a signed 32-bit number of seconds.
_MAX_SECONDS = 2_147_483_647
_TRANSIENT_STATUSES = frozenset({408, 409, 429})
_CONNECTION_ERROR_NAMES = frozenset(
    {
        "APIConnectionError",
        "APITimeoutError",
        "TransportError",
        "ClientConnectionError",
        "ServerDisconnectedError",
        "ServerTimeoutError",
    }
)


@dataclass(frozen=True, kw_only=True)
class ModelRetryPolicy:
    """Set how ``RunModel`` and ``CompactContext`` retry a transient model API error.

    A transient error is an HTTP 408, 409, 429, or 5xx answer from the model
    API, or a connection error or timeout on the way to it. Dex retries such a
    call with exponential backoff and jitter. The first wait is
    ``initial_interval``. Each next wait doubles, up to ``maximum_interval``.
    Each wait is drawn between half of that value and all of it, so sessions
    that failed together do not all retry together. When the error carries
    ``Retry-After`` or ``retry-after-ms``, the wait is at least that long.

    Retries stop after the nominal waits add up to more than
    ``total_duration``. Then the turn fails, as before. The defaults wait about
    two minutes in all. That rides out a typical provider outage or rate-limit
    window, which usually clears within a minute, and still ends a turn whose
    provider is down instead of keeping it waiting.

    Any other HTTP 4xx answer, such as 400 or 401, fails the turn at once: a
    retry would get the same answer. Other errors, method timeouts, and Worker
    crashes retry up to ``RunModelLimit.maximum_attempts``, as before.
    ``maximum_attempts=1`` disables every retry, transient ones included.

    Dex reads the HTTP status from ``status_code``, ``status``, ``code``, or
    ``response.status_code``, and the wait from ``headers`` or
    ``response.headers``, as the OpenAI, Anthropic, Google GenAI, LiteLLM,
    and httpx errors set them. It follows ``__cause__`` and ``__context__``.

    Example:
        >>> options = AgentSessionOptions(
        ...     run_model_limit=RunModelLimit(
        ...         retry=ModelRetryPolicy(
        ...             maximum_interval=timedelta(minutes=1),
        ...             total_duration=timedelta(minutes=5),
        ...         ),
        ...     ),
        ... )

    .. versionadded:: 0.6.16

    Attributes:
        initial_interval: Wait before the first retry, in whole seconds.
            Defaults to 1 second.
        maximum_interval: Longest wait between two attempts, in whole seconds.
            Defaults to 30 seconds.
        total_duration: The nominal waits of transient retries add up to at
            most this, in whole seconds. Defaults to 2 minutes. Zero gives
            transient errors only the retries of ``maximum_attempts``.

    Raises:
        ValueError: If a value is not a whole number of seconds, the initial
            interval is shorter than one second, or the maximum interval is
            shorter than the initial interval.
    """

    initial_interval: timedelta = timedelta(seconds=1)
    maximum_interval: timedelta = timedelta(seconds=30)
    total_duration: timedelta = timedelta(minutes=2)

    def __post_init__(self) -> None:
        for name, value, minimum in (
            ("initial_interval", self.initial_interval, timedelta(seconds=1)),
            ("maximum_interval", self.maximum_interval, timedelta(seconds=1)),
            ("total_duration", self.total_duration, timedelta()),
        ):
            if value < minimum or value.microseconds or value.total_seconds() > _MAX_SECONDS:
                raise ValueError(
                    f"retry.{name} must be a whole number of seconds,"
                    f" at least {int(minimum.total_seconds())}"
                )
        if self.maximum_interval < self.initial_interval:
            raise ValueError("retry.maximum_interval must not be shorter than initial_interval")

    @property
    def transient_retries(self) -> int:
        """Return the retries the nominal waits fit in ``total_duration``.

        Returns:
            How many retries a transient error gets on top of
            ``maximum_attempts``. The default policy gives 7: waits of 1, 2,
            4, 8, 16, 30, and 30 seconds.
        """
        retries = 0
        waited = 0.0
        while True:
            waited += self._nominal_interval_seconds(retries + 1)
            if waited > self.total_duration.total_seconds():
                return retries
            retries += 1

    def _nominal_interval_seconds(self, retry: int) -> float:
        initial = self.initial_interval.total_seconds()
        doubled: float = initial * 2.0 ** min(retry - 1, 52)
        return min(doubled, self.maximum_interval.total_seconds())


class ModelErrorKind(Enum):
    """What a model call's error says about calling again."""

    TRANSIENT = "transient"
    PERMANENT = "permanent"
    UNKNOWN = "unknown"


@dataclass(frozen=True)
class ModelErrorClassification:
    """A classified model call error and the provider's requested wait in seconds."""

    kind: ModelErrorKind
    retry_after_seconds: float | None = None


def classify_model_error(error: BaseException) -> ModelErrorClassification:
    """Classify a model call's error from its HTTP status, name, and cause chain.

    Returns:
        ``TRANSIENT`` for a retryable model API error, ``PERMANENT`` for any
        other HTTP 4xx, else ``UNKNOWN``.
    """
    seen: set[int] = set()
    current: BaseException | None = error
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        status = _read_status(current)
        if status is not None:
            if status in _TRANSIENT_STATUSES or status >= 500:
                return ModelErrorClassification(
                    ModelErrorKind.TRANSIENT, _read_retry_after_seconds(current)
                )
            if status >= 400:
                return ModelErrorClassification(ModelErrorKind.PERMANENT)
        if _is_connection_error(current):
            return ModelErrorClassification(ModelErrorKind.TRANSIENT)
        current = current.__cause__ or current.__context__
    return ModelErrorClassification(ModelErrorKind.UNKNOWN)


def _read_status(error: BaseException) -> int | None:
    response = getattr(error, "response", None)
    for value in (
        getattr(error, "status_code", None),
        getattr(error, "status", None),
        getattr(error, "code", None),
        getattr(response, "status_code", None),
    ):
        if isinstance(value, int) and not isinstance(value, bool) and 100 <= value <= 599:
            return value
    return None


def _is_connection_error(error: BaseException) -> bool:
    if isinstance(error, ConnectionError | TimeoutError):
        return True
    return any(cls.__name__ in _CONNECTION_ERROR_NAMES for cls in type(error).__mro__)


def _read_retry_after_seconds(error: BaseException) -> float | None:
    response = getattr(error, "response", None)
    for headers in (getattr(error, "headers", None), getattr(response, "headers", None)):
        milliseconds = _to_number(_read_header(headers, "retry-after-ms"))
        if milliseconds is not None and milliseconds > 0:
            return milliseconds / 1000
        value = _read_header(headers, "retry-after")
        if value is None:
            continue
        seconds = _to_number(value)
        if seconds is not None and seconds > 0:
            return seconds
        try:
            date = parsedate_to_datetime(value)
        except (TypeError, ValueError):
            continue
        remaining = (date - datetime.now(UTC)).total_seconds()
        if remaining > 0:
            return remaining
    return None


def _read_header(headers: Any, name: str) -> str | None:
    if not isinstance(headers, Mapping) and not hasattr(headers, "get"):
        return None
    value = headers.get(name)
    if value is None and isinstance(headers, Mapping):
        value = next((item for key, item in headers.items() if str(key).lower() == name), None)
    return value if isinstance(value, str) else None


def _to_number(value: str | None) -> float | None:
    if value is None:
        return None
    try:
        number = float(value)
    except ValueError:
        return None
    return number if math.isfinite(number) else None


class ModelCallDecisionKind(Enum):
    """What a model Step does after its model call raised."""

    RETRY_AFTER = "retry_after"
    RERAISE = "reraise"
    FAIL_TURN = "fail_turn"


@dataclass(frozen=True)
class ModelCallRetryDecision:
    """A decision with the jittered wait in whole seconds for ``RETRY_AFTER``."""

    kind: ModelCallDecisionKind
    after_seconds: int = 0


def decide_model_call_retry(
    error: BaseException,
    attempt: int,
    maximum_attempts: int,
    retry: ModelRetryPolicy,
    random: float | None = None,
) -> ModelCallRetryDecision:
    """Decide what a model Step attempt does with its model call's error.

    Returns:
        ``RETRY_AFTER`` with the jittered wait for a transient error,
        ``FAIL_TURN`` for a permanent error, else ``RERAISE``, which leaves the
        error to the model Step's attempt limit.
    """
    if maximum_attempts <= 1:
        return ModelCallRetryDecision(ModelCallDecisionKind.RERAISE)
    classification = classify_model_error(error)
    if classification.kind is ModelErrorKind.PERMANENT:
        return ModelCallRetryDecision(ModelCallDecisionKind.FAIL_TURN)
    if classification.kind is ModelErrorKind.TRANSIENT:
        nominal = retry._nominal_interval_seconds(attempt)
        jitter = _random.random() if random is None else random
        wait = max(nominal / 2 + nominal / 2 * jitter, classification.retry_after_seconds or 0)
        return ModelCallRetryDecision(
            ModelCallDecisionKind.RETRY_AFTER, min(max(1, math.ceil(wait)), _MAX_SECONDS)
        )
    # The model Step's wrapper ends the turn on the last attempt, for every error.
    return ModelCallRetryDecision(ModelCallDecisionKind.RERAISE)


def model_step_maximum_attempts(maximum_attempts: int, retry: ModelRetryPolicy) -> int:
    """Return the attempts of a model Step's Dex retry policy."""
    return 1 if maximum_attempts <= 1 else maximum_attempts + retry.transient_retries


class TransientModelCallError(Exception):
    """Raised inside a model Step for a transient error; the Step turns it into ``retry_after``.

    ``RetryAfterError`` is frozen, so it cannot pass through ``contextlib``
    context managers, which set ``__traceback__``.
    """

    def __init__(self, after_seconds: int, cause: Exception) -> None:
        super().__init__(str(cause))
        self.after_seconds = after_seconds
        self.cause = cause


class ModelCallTurnFailure(Exception):
    """Raised inside a model Step when its model call failed so that the turn ends now."""

    def __init__(self, error_text: str) -> None:
        super().__init__(error_text)
        self.error_text = error_text


async def call_model_under_retry_policy(
    call: Callable[[], Awaitable[_Result]],
    attempt: int,
    maximum_attempts: int,
    retry: ModelRetryPolicy,
) -> _Result:
    """Run one model call of a model Step under the retry policy.

    Raises:
        TransientModelCallError: For a transient error, with the jittered wait.
        ModelCallTurnFailure: For an error that ends the turn now.
        Exception: The call's own error, for Dex to retry under the Step's
            retry policy.
    """
    try:
        return await call()
    except Exception as error:
        decision = decide_model_call_retry(error, attempt, maximum_attempts, retry)
        if decision.kind is ModelCallDecisionKind.RETRY_AFTER:
            raise TransientModelCallError(decision.after_seconds, error) from error
        if decision.kind is ModelCallDecisionKind.FAIL_TURN:
            raise ModelCallTurnFailure(str(error) or type(error).__name__) from error
        raise
