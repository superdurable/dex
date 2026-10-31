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

PENDING_TOOL_CALLS_FORMAT = "dex_agents_pydantic_ai.pending_tool_calls.v1"


@dataclass(frozen=True)
class PendingToolCallsState:
    turn_messages: Sequence[ModelMessage]
    tool_call_ids: Sequence[str]
    structured_result_tool_call_ids: Sequence[str]

    def to_json(self) -> str:
        return json.dumps(
            {
                "format": PENDING_TOOL_CALLS_FORMAT,
                "turn_messages": ModelMessagesTypeAdapter.dump_python(
                    list(self.turn_messages), mode="json"
                ),
                "tool_call_ids": list(self.tool_call_ids),
                "structured_result_tool_call_ids": list(self.structured_result_tool_call_ids),
            },
            ensure_ascii=False,
        )

    @classmethod
    def from_json(cls, framework_run_state: str) -> PendingToolCallsState:
        state = json.loads(framework_run_state)
        if state.get("format") != PENDING_TOOL_CALLS_FORMAT:
            raise ValueError(f"unsupported Pydantic AI run state format {state.get('format')!r}")
        return cls(
            turn_messages=ModelMessagesTypeAdapter.validate_python(state["turn_messages"]),
            tool_call_ids=[str(tool_call_id) for tool_call_id in state["tool_call_ids"]],
            structured_result_tool_call_ids=[
                str(tool_call_id) for tool_call_id in state["structured_result_tool_call_ids"]
            ],
        )
