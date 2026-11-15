# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The framework run state a Pydantic AI turn stores while its tool call Steps run."""

from __future__ import annotations

import json
from collections.abc import Sequence
from dataclasses import dataclass

from pydantic_ai import ModelMessage, ModelMessagesTypeAdapter

# Written by 0.6.11 and earlier; it held every message of the turn so far.
LEGACY_PENDING_TOOL_CALLS_FORMAT = "dex_agents_pydantic_ai.pending_tool_calls.v1"
# The session stores the turn's messages through ToolCallsRequested.new_messages.
PENDING_TOOL_CALLS_FORMAT = "dex_agents_pydantic_ai.pending_tool_calls.v2"


@dataclass(frozen=True)
class PendingToolCallsState:
    """Hold what resumes a turn while its tool call Steps run.

    Attributes:
        tool_call_ids: The calls of the latest model response that run as Steps.
        structured_result_tool_call_ids: Those calls whose result is structured JSON.
        unstored_turn_messages: The turn's messages that the session has not
            stored. Only a legacy run state holds any: the whole turn from its
            opening request on, while the session stored only the turn's user
            message.
    """

    tool_call_ids: Sequence[str]
    structured_result_tool_call_ids: Sequence[str]
    unstored_turn_messages: Sequence[ModelMessage] = ()

    def to_json(self) -> str:
        if self.unstored_turn_messages:
            raise ValueError("a new run state never holds unstored turn messages")
        return json.dumps(
            {
                "format": PENDING_TOOL_CALLS_FORMAT,
                "tool_call_ids": list(self.tool_call_ids),
                "structured_result_tool_call_ids": list(self.structured_result_tool_call_ids),
            },
            ensure_ascii=False,
        )

    @classmethod
    def from_json(cls, framework_run_state: str) -> PendingToolCallsState:
        state = json.loads(framework_run_state)
        run_state_format = state.get("format")
        unstored_turn_messages: Sequence[ModelMessage]
        if run_state_format == LEGACY_PENDING_TOOL_CALLS_FORMAT:
            unstored_turn_messages = ModelMessagesTypeAdapter.validate_python(
                state["turn_messages"]
            )
        elif run_state_format == PENDING_TOOL_CALLS_FORMAT:
            unstored_turn_messages = ()
        else:
            raise ValueError(f"unsupported Pydantic AI run state format {run_state_format!r}")
        return cls(
            tool_call_ids=[str(tool_call_id) for tool_call_id in state["tool_call_ids"]],
            structured_result_tool_call_ids=[
                str(tool_call_id) for tool_call_id in state["structured_result_tool_call_ids"]
            ],
            unstored_turn_messages=unstored_turn_messages,
        )
