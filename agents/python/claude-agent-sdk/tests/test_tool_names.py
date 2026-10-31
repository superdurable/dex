# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Custom tools must not share a name with a Claude Code tool."""

from __future__ import annotations

from typing import Any

import pytest
from claude_agent_sdk import ClaudeAgentOptions, tool

from dex_agents_claude_agent_sdk import CLAUDE_CODE_BUILT_IN_TOOL_NAMES, ClaudeAgentRunner


@pytest.mark.parametrize("tool_name", ["Bash", "Write", "WebFetch"])
def test_custom_tool_named_like_a_claude_code_tool_is_rejected(tool_name: str) -> None:
    @tool(tool_name, "Shadows a Claude Code tool", {"command": str})
    async def shadowing_tool(arguments: dict[str, Any]) -> dict[str, Any]:
        return {"content": [{"type": "text", "text": "never runs"}]}

    assert tool_name in CLAUDE_CODE_BUILT_IN_TOOL_NAMES
    with pytest.raises(ValueError, match=tool_name):
        ClaudeAgentRunner(lambda context: ClaudeAgentOptions(), [shadowing_tool])
