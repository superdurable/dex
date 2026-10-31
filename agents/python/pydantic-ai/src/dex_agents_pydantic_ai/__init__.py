# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run Pydantic AI agents as durable Dex agent sessions in Step mode.

``pydantic_ai_session_flow`` turns an unchanged Pydantic AI agent into an
``AgentSessionFlow`` from ``dex_agents``. Each model call is one ``RunModel``
Step that stops with Pydantic AI's deferred tools, and each tool call, READ
included, runs in its own Dex Step, in parallel with the other calls of the
same model response. See the package README for the effect table and limits.
"""

from dex_agents_pydantic_ai.runner import (
    IDEMPOTENCY_KEY_METADATA_KEY,
    PydanticAIRunner,
    deferred_tool_idempotency_key,
    pydantic_ai_session_flow,
)

__all__ = [
    "IDEMPOTENCY_KEY_METADATA_KEY",
    "PydanticAIRunner",
    "deferred_tool_idempotency_key",
    "pydantic_ai_session_flow",
]
