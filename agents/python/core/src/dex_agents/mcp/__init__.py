# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""MCP servers' tools as tools of a Dex agent session.

``McpToolSource`` connects to MCP servers configured on the Worker, over stdio
or Streamable HTTP, and offers their tools as agent tool definitions. Each
call runs in its own tool Step, with an effect class from the tool's
annotations, so a crash never repeats a write silently. Install the MCP SDK
with the ``mcp`` extra::

    pip install "dex-agents[mcp]"

See the package README for the full contract.
"""

import importlib.util

if importlib.util.find_spec("mcp") is None:
    raise ModuleNotFoundError(
        'dex_agents.mcp needs the MCP SDK; install it with pip install "dex-agents[mcp]"',
        name="mcp",
    )

from dex_agents.mcp._servers import (
    McpServerConfig,
    McpStdioServerConfig,
    McpStreamableHttpServerConfig,
)
from dex_agents.mcp._tasks import TASKS_EXTENSION_IDENTIFIER
from dex_agents.mcp._tool_source import (
    DEFAULT_MCP_CONNECTION_ATTEMPTS,
    DEFAULT_MCP_CONNECTION_TIMEOUT,
    DEFAULT_MCP_REQUEST_TIMEOUT,
    DEFAULT_MCP_TASK_WAIT_TIMEOUT,
    IDEMPOTENCY_KEY_META_KEY,
    MCP_TASK_STATUS_EVENT_TYPE,
    MCP_TOOL_PROGRESS_EVENT_TYPE,
    DeferredToolHandler,
    McpToolCallInterruptedError,
    McpToolListingError,
    McpToolSource,
)
from dex_agents.mcp._tools import McpTool, McpToolAnnotations, tool_effect_from_mcp_annotations

__all__ = [
    "DEFAULT_MCP_CONNECTION_ATTEMPTS",
    "DEFAULT_MCP_CONNECTION_TIMEOUT",
    "DEFAULT_MCP_REQUEST_TIMEOUT",
    "DEFAULT_MCP_TASK_WAIT_TIMEOUT",
    "IDEMPOTENCY_KEY_META_KEY",
    "MCP_TASK_STATUS_EVENT_TYPE",
    "MCP_TOOL_PROGRESS_EVENT_TYPE",
    "TASKS_EXTENSION_IDENTIFIER",
    "DeferredToolHandler",
    "McpServerConfig",
    "McpStdioServerConfig",
    "McpStreamableHttpServerConfig",
    "McpTool",
    "McpToolAnnotations",
    "McpToolCallInterruptedError",
    "McpToolListingError",
    "McpToolSource",
    "tool_effect_from_mcp_annotations",
]
