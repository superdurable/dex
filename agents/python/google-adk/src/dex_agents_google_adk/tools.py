# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Helpers for ADK tools that run in a Dex agent session."""

from __future__ import annotations

from google.adk.tools import ToolContext

#: ``tool_context.custom_metadata`` key that carries a deferred call's idempotency key.
IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY = "dex_idempotency_key"


def idempotency_key_for_tool_call(tool_context: ToolContext) -> str:
    """Return the Dex idempotency key of the tool call a ``ToolContext`` runs.

    Pass the key to an external system that deduplicates requests. An
    ``IDEMPOTENT_WRITE`` tool can run up to three times in ``ExecuteToolCall``,
    and every attempt gets the same key, ``"<flow ID>/<turn ID>/<tool call
    ID>"``. It is exactly ``DeferredToolExecution.idempotency_key``: the
    adapter puts it in ``tool_context.custom_metadata`` under
    ``IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY`` before it runs the tool.

    Every tool call runs in its own Dex tool Step and has a key. ADK control
    tools, such as ``transfer_to_agent``, run inside ``RunModel`` and have none.

    Example:
        >>> from dex_agents_google_adk import idempotency_key_for_tool_call
        >>> async def create_refund(order_id: str, tool_context: ToolContext) -> str:
        ...     key = idempotency_key_for_tool_call(tool_context)
        ...     refund = await payments.refund(order_id, idempotency_key=key)
        ...     return f"refund {refund.id} created"

    Args:
        tool_context: The ``ToolContext`` ADK passed to the tool.

    Returns:
        The idempotency key of the call, the same on every attempt.

    Raises:
        ValueError: If the tool does not run in a Dex tool Step.
    """
    idempotency_key = tool_context.custom_metadata.get(IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY)
    if not isinstance(idempotency_key, str):
        raise ValueError("only a tool call that runs in its Dex tool Step has an idempotency key")
    return idempotency_key
