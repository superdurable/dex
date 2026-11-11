# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deprecated alias of ``dex_agents.builtin_loop.framework_tool_sources``.

.. deprecated:: 0.5.0
   Import from ``dex_agents.builtin_loop`` instead.
"""

from typing import TYPE_CHECKING, Any

import dex_agents.builtin_loop.framework_tool_sources as _replacement_module
from dex_agents.stepwise._deprecated_alias import forward_deprecated_attribute

if TYPE_CHECKING:
    from dex_agents.builtin_loop.framework_tool_sources import (
        FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY,
        SOURCE_TOOL_NOT_OFFERED_RESULT,
        FrameworkToolSourceRouter,
        SourceToolListing,
    )
    from dex_agents.builtin_loop.runner import DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT
    from dex_agents.builtin_loop.tool_sources import SourceToolDefinition, ToolSource
    from dex_agents.runner import (
        AgentTurnContext,
        DeferredToolCall,
        DeferredToolExecution,
        DeferredToolOutcome,
        ToolCallStepOptions,
    )
    from dex_agents.values import AgentToolCall

_RENAMED_ATTRIBUTES: dict[str, str] = {}

__all__ = [
    "DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT",
    "FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY",
    "SOURCE_TOOL_NOT_OFFERED_RESULT",
    "AgentToolCall",
    "AgentTurnContext",
    "DeferredToolCall",
    "DeferredToolExecution",
    "DeferredToolOutcome",
    "FrameworkToolSourceRouter",
    "SourceToolDefinition",
    "SourceToolListing",
    "ToolCallStepOptions",
    "ToolSource",
]


def __getattr__(name: str) -> Any:
    return forward_deprecated_attribute(__name__, _replacement_module, _RENAMED_ATTRIBUTES, name)


def __dir__() -> list[str]:
    return sorted(__all__)
