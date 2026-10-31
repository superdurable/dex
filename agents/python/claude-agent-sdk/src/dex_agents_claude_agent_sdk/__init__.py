# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Durable Claude Agent SDK sessions on Dex.

``claude_agent_session_flow`` registers a Claude Agent SDK agent as a Dex
agent session Flow. Each turn runs in a Claude Code CLI process inside a
Dex Step; the session stores the CLI transcript and resumes from it. Custom
tool calls that write run in their own Dex Steps. See the package README.
"""

from dex_agents_claude_agent_sdk.runner import (
    APPROVED_CLI_TOOL_OUTPUT,
    DEFAULT_CUSTOM_TOOL_SERVER_NAME,
    ClaudeAgentOptionsContext,
    ClaudeAgentOptionsFactory,
    ClaudeAgentRunner,
    current_deferred_tool_execution,
)
from dex_agents_claude_agent_sdk.session_flow import claude_agent_session_flow
from dex_agents_claude_agent_sdk.tool_gate import CLAUDE_CODE_BUILT_IN_TOOL_NAMES
from dex_agents_claude_agent_sdk.transcript import (
    CLAUDE_TRANSCRIPT_ITEM_KIND,
    USER_PROMPT_ITEM_KIND,
)

__all__ = [
    "APPROVED_CLI_TOOL_OUTPUT",
    "CLAUDE_CODE_BUILT_IN_TOOL_NAMES",
    "CLAUDE_TRANSCRIPT_ITEM_KIND",
    "DEFAULT_CUSTOM_TOOL_SERVER_NAME",
    "USER_PROMPT_ITEM_KIND",
    "ClaudeAgentOptionsContext",
    "ClaudeAgentOptionsFactory",
    "ClaudeAgentRunner",
    "claude_agent_session_flow",
    "current_deferred_tool_execution",
]
