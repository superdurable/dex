# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deprecated alias of ``dex_agents.builtin_loop.transcript``.

.. deprecated:: 0.5.0
   Import from ``dex_agents.builtin_loop`` instead.
"""

from typing import TYPE_CHECKING, Any

import dex_agents.builtin_loop.transcript as _replacement_module
from dex_agents.stepwise._deprecated_alias import forward_deprecated_attribute

if TYPE_CHECKING:
    from dex_agents.builtin_loop.model import (
        ModelInputItem,
        ModelOutputItem,
        ModelToolCall,
        ToolResultItem,
        UserTextItem,
    )
    from dex_agents.builtin_loop.transcript import (
        MODEL_OUTPUT_ITEM_TYPE,
        TOOL_RESULT_ITEM_TYPE,
        USER_TEXT_ITEM_TYPE,
        pair_tool_calls_with_results,
    )
    from dex_agents.builtin_loop.transcript import (
        decode_builtin_loop_message as decode_stepwise_message,
    )
    from dex_agents.builtin_loop.transcript import (
        encode_builtin_loop_message as encode_stepwise_message,
    )
    from dex_agents.runner import OUTCOME_UNKNOWN_TOOL_RESULT, FrameworkMessage
    from dex_agents.values import AgentMessage, MessageRole

_RENAMED_ATTRIBUTES = {
    "encode_stepwise_message": "encode_builtin_loop_message",
    "decode_stepwise_message": "decode_builtin_loop_message",
}

__all__ = [
    "MODEL_OUTPUT_ITEM_TYPE",
    "OUTCOME_UNKNOWN_TOOL_RESULT",
    "TOOL_RESULT_ITEM_TYPE",
    "USER_TEXT_ITEM_TYPE",
    "AgentMessage",
    "FrameworkMessage",
    "MessageRole",
    "ModelInputItem",
    "ModelOutputItem",
    "ModelToolCall",
    "ToolResultItem",
    "UserTextItem",
    "decode_stepwise_message",
    "encode_stepwise_message",
    "pair_tool_calls_with_results",
]


def __getattr__(name: str) -> Any:
    return forward_deprecated_attribute(__name__, _replacement_module, _RENAMED_ATTRIBUTES, name)


def __dir__() -> list[str]:
    return sorted(__all__)
