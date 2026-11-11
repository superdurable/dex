# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deprecated alias of ``dex_agents.builtin_loop.function_tools``.

.. deprecated:: 0.5.0
   Import from ``dex_agents.builtin_loop`` instead.
"""

from typing import TYPE_CHECKING, Any

import dex_agents.builtin_loop.function_tools as _replacement_module
from dex_agents.stepwise._deprecated_alias import forward_deprecated_attribute

if TYPE_CHECKING:
    from dex_agents.builtin_loop.function_tools import FunctionTool, ToolCallContext, function_tool
    from dex_agents.builtin_loop.model import ModelToolDefinition
    from dex_agents.events import AgentEventWriter
    from dex_agents.runner import ToolExecutionError, ToolInputRequested, ToolPolicy
    from dex_agents.values import QuestionAnswer, ToolEffect

_RENAMED_ATTRIBUTES: dict[str, str] = {}

__all__ = [
    "AgentEventWriter",
    "FunctionTool",
    "ModelToolDefinition",
    "QuestionAnswer",
    "ToolCallContext",
    "ToolEffect",
    "ToolExecutionError",
    "ToolInputRequested",
    "ToolPolicy",
    "function_tool",
]


def __getattr__(name: str) -> Any:
    return forward_deprecated_attribute(__name__, _replacement_module, _RENAMED_ATTRIBUTES, name)


def __dir__() -> list[str]:
    return sorted(__all__)
