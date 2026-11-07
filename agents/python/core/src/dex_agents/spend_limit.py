# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The application interface that decides whether a session may spend on a model call."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from datetime import timedelta
from typing import Any, Protocol, runtime_checkable

from dex_agents.values import AgentUsage

#: How long the session waits for ``check_model_call`` or ``report_model_usage``.
#:
#: Bound your own calls below it. A check that takes longer counts as a check
#: that raised. A report that takes longer is dropped.
#:
#: .. versionadded:: 0.3.0
SPEND_LIMIT_CALL_TIMEOUT = timedelta(seconds=30)

#: Starts ``AgentState.last_turn_error`` of a turn that a spend limit ended.
#:
#: The text goes on with ``": "`` and the reason from ``ModelCallDecision``.
#: Test for it to tell a refused call from a failed one.
#:
#: .. versionadded:: 0.3.0
MODEL_CALL_REFUSED_ERROR_PREFIX = "the model call was refused"


@dataclass(frozen=True, kw_only=True)
class ModelCallCheck:
    """Describe one model call that is about to run.

    The session builds one for every attempt of ``RunModel`` and
    ``CompactContext``, because every attempt can cost money.

    .. versionadded:: 0.3.0

    Attributes:
        flow_id: The session's Flow ID.
        turn_id: The turn that makes the call.
        step_type: ``"RunModel"`` or ``"CompactContext"``.
        attempt: One-based attempt of the Step.
        session_settings: The turn's session settings. Put the account or API
            key reference that the limit applies to here. Never a secret.
        session_usage: Committed usage of this session so far.
    """

    flow_id: str
    turn_id: str
    step_type: str
    attempt: int
    session_settings: Mapping[str, Any]
    session_usage: AgentUsage


@dataclass(frozen=True)
class ModelCallDecision:
    """Say whether a model call may run.

    Build one with ``allow`` or ``refuse``.

    .. versionadded:: 0.3.0

    Attributes:
        is_allowed: Whether the call may run.
        reason: Why it may not. It reaches ``AgentState.last_turn_error``, so
            people read it, and it is stored. Never put a secret in it.
    """

    is_allowed: bool
    reason: str = ""

    @classmethod
    def allow(cls) -> ModelCallDecision:
        """Return the decision that lets the call run.

        Returns:
            An allowing decision.
        """
        return cls(is_allowed=True)

    @classmethod
    def refuse(cls, reason: str) -> ModelCallDecision:
        """Return the decision that ends the turn instead of calling the model.

        Args:
            reason: Why the call may not run, such as ``"account over its
                monthly limit"``.

        Returns:
            A refusing decision.
        """
        return cls(is_allowed=False, reason=reason)


@dataclass(frozen=True, kw_only=True)
class ModelUsageReport:
    """Report what one finished model call used.

    .. versionadded:: 0.3.0

    Attributes:
        report_id: Stable ID of this attempt's call:
            ``<flow>/<turn>/<step execution>/<attempt>``. Count each ID once.
            A retry has a new ID because it is a new paid call.
        flow_id: The session's Flow ID.
        turn_id: The turn that made the call.
        step_type: ``"RunModel"`` or ``"CompactContext"``.
        attempt: One-based attempt of the Step.
        session_settings: The turn's session settings.
        usage: What the runner reported. A harness runner, such as the Claude
            Agent SDK adapter, reports a whole segment here: several model
            calls in ``usage.model_calls``. Zero tokens with ``model_calls``
            above zero mean the framework did not expose token counts, not that
            the call was free.
        session_usage: ``usage`` added to the committed session usage. It
            includes a call whose report raised or timed out, for an application
            that counts per session.
    """

    report_id: str
    flow_id: str
    turn_id: str
    step_type: str
    attempt: int
    session_settings: Mapping[str, Any]
    usage: AgentUsage
    session_usage: AgentUsage


@runtime_checkable
class ModelSpendLimit(Protocol):
    """Let an application decide whether a session may spend on a model call.

    A spend limit is usually per account or per API key, across many sessions,
    so the counters and the limit belong to the application. Pass an
    implementation as ``AgentSessionOptions.spend_limit``. Without one, the
    session makes no checks. The object lives in the Worker and is not stored.

    The session calls ``check_model_call`` before each attempt of ``RunModel``
    and ``CompactContext``, and ``report_model_usage`` after the attempt's model
    call returns. Both run inside the Step, on the Worker, so keep them fast and
    bound them below ``SPEND_LIMIT_CALL_TIMEOUT``.

    A refused call ends the turn: the session records
    ``MODEL_CALL_REFUSED_ERROR_PREFIX`` and the reason in
    ``AgentState.last_turn_error``, keeps the turn's finished tool results, and
    waits for the next user message, which is checked again. A check that
    raises or times out fails closed. It fails the attempt, Dex retries it under
    the Step's retry policy, and no model call runs without a passed check. When
    the retries run out the turn fails. To fail open, catch your own errors in
    ``check_model_call`` and return ``ModelCallDecision.allow()``.

    A report that raises or times out is logged and dropped, because failing the
    attempt would repeat a call that was already paid for.

    Limits to design for:

    * A check bounds the next call, not its cost. Sessions that pass together
      can overshoot a limit. A harness runner is checked once per segment, so
      the overshoot can be a whole segment. Set the harness's own budget option
      too, such as ``max_budget_usd`` for the Claude Agent SDK.
    * A call that fails, is cancelled, or is lost with its Worker reports
      nothing, though the provider may bill it. So the reports can be lower
      than the bill. They can also exceed ``AgentUsage``: a call whose Step did
      not commit reports here and again on its retry, under a new
      ``report_id``, while ``AgentUsage`` keeps only the committed attempt.
    * Reports are not durable. A report runs inside the Step, so a Worker that
      dies between the model's answer and the report loses it. That attempt
      never commits, so ``AgentUsage`` does not hold the call either, and only
      the provider's bill does. A report that raised or timed out is different:
      its attempt commits, so the next report's ``session_usage`` includes it.

    Example:
        >>> class AccountLimit:
        ...     async def check_model_call(self, check):
        ...         account = check.session_settings["account"]
        ...         if await spent_this_month(account) >= limit_of(account):
        ...             return ModelCallDecision.refuse("account over its monthly limit")
        ...         return ModelCallDecision.allow()
        ...
        ...     async def report_model_usage(self, report):
        ...         await record_once(report.report_id, report.usage)
        >>> options = AgentSessionOptions(spend_limit=AccountLimit())

    .. versionadded:: 0.3.0
    """

    async def check_model_call(self, check: ModelCallCheck) -> ModelCallDecision:
        """Decide whether one model call may run.

        Args:
            check: The call that is about to run.

        Returns:
            ``ModelCallDecision.allow()`` or ``ModelCallDecision.refuse(reason)``.

        Raises:
            Exception: Any error fails the attempt, so the call does not run.
        """
        ...

    async def report_model_usage(self, report: ModelUsageReport) -> None:
        """Record what one finished model call used.

        Args:
            report: The call's usage. Count each ``report_id`` once.

        Raises:
            Exception: Any error is logged and dropped.
        """
        ...
