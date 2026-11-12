# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The scripted agent with commands that grow the session's state.

Besides the commands of ``tests.scripted_agent``:

* ``pad <characters>`` answers with that many characters.
* ``call bulk {"characters": <n>}`` runs a read tool whose output has that many
  characters. ``call bulk_write`` does the same with a non-idempotent write.
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from dex_agents import (
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    AgentTurnOutcome,
    DeferredToolExecution,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
    TurnCompleted,
)
from tests.scripted_agent import (
    MODEL_CALL_USAGE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    SUMMARY_INDEXED_ENVIRONMENT_VARIABLE,
    TOOL_POLICIES,
    ScriptedRunner,
    _assistant_text,
    _current_user_text,
)

STATE_LIMIT_AGENT_FLOW_TYPE = "StateLimitAgent"
BULK_TOOL_POLICIES = {
    "bulk": ToolPolicy(ToolEffect.READ),
    "bulk_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


class StateLimitRunner(ScriptedRunner):
    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        command = _current_user_text(turn)
        if turn.framework_run_state is None and command.startswith("pad "):
            self._invocations.record(f"model:{turn.turn_id}:1")
            text = "p" * int(command.removeprefix("pad "))
            return TurnCompleted(
                new_messages=[_assistant_text(command, text)], usage=MODEL_CALL_USAGE
            )
        return await super().run_model_step(turn)

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        if call.tool_call.tool_name not in BULK_TOOL_POLICIES:
            return await super().execute_deferred_tool_call(call)
        self._invocations.record(call.tool_call.tool_name)
        characters = int(json.loads(call.tool_call.arguments)["characters"])
        return ToolExecutionResult(output="b" * characters)


def build_state_limit_agent_flow(
    state_dir: Path, *, summary_attributes_indexed: bool = False
) -> AgentSessionFlow:
    return AgentSessionFlow(
        StateLimitRunner(state_dir),
        flow_type=STATE_LIMIT_AGENT_FLOW_TYPE,
        options=AgentSessionOptions(
            tool_policies={**TOOL_POLICIES, **BULK_TOOL_POLICIES},
            summary_attributes_indexed=summary_attributes_indexed,
        ),
    )


def build_state_limit_agent_flow_from_environment() -> AgentSessionFlow:
    return build_state_limit_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        summary_attributes_indexed=bool(os.environ.get(SUMMARY_INDEXED_ENVIRONMENT_VARIABLE)),
    )
