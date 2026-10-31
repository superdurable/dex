# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run OpenAI Agents SDK agents as durable Dex agent sessions.

``openai_agents_session_flow`` builds the shared Step-mode ``AgentSessionFlow``
from ``dex_agents`` for one agent. Every model call is a ``RunModel`` Step, and
every function tool call is its own Step, stopped through the SDK's own
approval mechanism. See the package README.
"""

from dex_agents_openai_agents.runner import (
    OpenAIAgentsRunner,
    get_current_deferred_tool_execution,
)
from dex_agents_openai_agents.session_flow import openai_agents_session_flow

__all__ = [
    "OpenAIAgentsRunner",
    "get_current_deferred_tool_execution",
    "openai_agents_session_flow",
]
