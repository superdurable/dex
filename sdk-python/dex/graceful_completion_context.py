# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dex._invocation_context import InvocationContext
from dex.context import Context


def is_graceful_completion_condition_completed(context: Context) -> bool:
    """Report whether the graceful completion condition of the current Wait completed.

    Call it in ``execute`` after a Wait that includes
    :func:`dex.graceful_completion_requested`. It reads the condition results
    of the current ``execute`` call and never sends a request. It returns
    ``True`` when the Flow had a graceful completion request as the Wait
    completed. That holds even when another :meth:`Wait.any_of` Condition
    won, such as a Timer that fired or a Channel message. Read consumed
    Channel messages first, so a message that won is not dropped.

    It returns ``False`` when the Wait has no such Condition, outside
    ``execute``, and after ``Wait.skip_immediately``. It also returns
    ``False`` for a Context that Dex did not create, such as an application's
    own ``Context`` implementation or a test fake. It is a function rather
    than a ``Context`` member, so such implementations keep type-checking.

    Example:
        >>> from dex import (
        ...     Channel, Context, Step, StepDecision, Wait, dead_end, go_to,
        ...     is_graceful_completion_condition_completed,
        ... )
        >>> USER_MESSAGES = Channel("user-messages", str)
        >>> class WaitForMessage(Step[None]):
        ...     def execute(self, context: Context, input: None) -> StepDecision:
        ...         messages = USER_MESSAGES.results(context)
        ...         if messages:
        ...             return go_to(HandleMessage, messages[0])
        ...         if is_graceful_completion_condition_completed(context):
        ...             return dead_end()
        ...         return go_to(WaitForMessage, None)

    Args:
        context: The Context that Dex passed to the current Step method.

    Returns:
        ``True`` when the current ``execute`` call's Wait reports its graceful
        completion condition as completed; otherwise ``False``.

    .. versionadded:: 1.45.4
    """
    return (
        isinstance(context, InvocationContext)
        and context.is_graceful_completion_condition_completed()
    )


def supports_graceful_completion_condition(context: Context) -> bool:
    """Report whether the connected Dex Server supports the graceful completion condition.

    Use it in ``wait_for`` to add :func:`dex.graceful_completion_requested`
    only when the Server supports it. With an older Server, a Wait that
    contains the condition fails with
    ``GracefulCompletionConditionUnsupportedError`` after ``wait_for``
    returns, and Step code cannot catch that error. Check this function first
    and keep the Wait without the condition instead.

    It reads the Server protocol version that the Worker negotiated with
    ``GetServerInfo`` when it started. It never sends a request, so it is
    cheap to call on every invocation. It returns ``True`` when that version
    is 7 or later. It works in ``wait_for``, ``execute``, and RPC handlers
    alike. The Worker negotiates before it receives any task, so a Context
    that Dex created always sees the negotiated version. It returns ``False``
    for a Context that Dex did not create, such as an application's own
    ``Context`` implementation or a test fake.

    Example:
        >>> from dex import (
        ...     Channel, Context, Step, Wait, graceful_completion_requested,
        ...     supports_graceful_completion_condition,
        ... )
        >>> USER_MESSAGES = Channel("user-messages", str)
        >>> class WaitForMessage(Step[None]):
        ...     def wait_for(self, context: Context, input: None) -> Wait:
        ...         if supports_graceful_completion_condition(context):
        ...             return Wait.any_of(
        ...                 USER_MESSAGES.for_one(),
        ...                 graceful_completion_requested(),
        ...             )
        ...         return Wait.until(USER_MESSAGES.for_one())

    Args:
        context: The Context that Dex passed to the current Step or RPC method.

    Returns:
        ``True`` when the Worker's negotiated Server protocol supports the
        graceful completion condition; otherwise ``False``.

    .. versionadded:: 1.45.4
    """
    return (
        isinstance(context, InvocationContext)
        and context.supports_graceful_completion_condition()
    )
