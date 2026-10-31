# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Claude agent under test: custom tools, policies, and options for the fake CLI.

* ``lookup`` is READ through its ``readOnlyHint`` annotation and runs inline.
* ``publish`` is a non-idempotent write that needs approval.
* ``charge`` is an idempotent write through its ``idempotentHint`` annotation.
* ``slow_write`` is a non-idempotent write without approval that waits at a gate.
* ``Bash`` runs inside the CLI and needs approval, the default for unknown tools.

An application PreToolUse hook rewrites ``publish`` input ``"rewrite me"`` to
``"rewritten"``, and appends ``+`` to any ``publish`` message or ``lookup``
query that starts with ``stamp`` on every call, resumed calls included. With
``REAL_CLI_ENVIRONMENT_VARIABLE`` set, the agent runs the bundled Claude Code
CLI with that JSON object as extra CLI environment.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, cast

from claude_agent_sdk import (
    ClaudeAgentOptions,
    HookContext,
    HookInput,
    HookJSONOutput,
    HookMatcher,
    PermissionMode,
    SdkMcpTool,
    ToolAnnotations,
    tool,
)

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents.testing import FileGate, InvocationLog
from dex_agents_claude_agent_sdk import (
    ClaudeAgentOptionsContext,
    claude_agent_session_flow,
    current_deferred_tool_execution,
)
from tests.fake_claude_cli import INVOCATION_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE

CLAUDE_TEST_AGENT_FLOW_TYPE = "ScriptedClaudeAgent"
FAKE_CLI_PATH_ENVIRONMENT_VARIABLE = "FAKE_CLAUDE_CLI_PATH"
REAL_CLI_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_REAL_CLI_ENV"
PERMISSION_MODE_ENVIRONMENT_VARIABLE = "CLAUDE_TEST_AGENT_PERMISSION_MODE"
REWRITTEN_PUBLISH_INPUT = {"message": "rewritten"}
SLOW_WRITE_GATE = "slow_write"
TOOL_POLICIES = {
    "publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


def build_claude_test_agent_flow(state_dir: Path) -> AgentSessionFlow:
    def build_options(context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        real_cli_environment = os.environ.get(REAL_CLI_ENVIRONMENT_VARIABLE)
        permission_mode = os.environ.get(PERMISSION_MODE_ENVIRONMENT_VARIABLE)
        return ClaudeAgentOptions(
            cli_path=None
            if real_cli_environment is not None
            else os.environ[FAKE_CLI_PATH_ENVIRONMENT_VARIABLE],
            cwd=str(state_dir / "workspace"),
            env={
                **json.loads(real_cli_environment or "{}"),
                "CLAUDE_CONFIG_DIR": str(state_dir / "claude-config"),
            },
            system_prompt=f"You are scripted agent {context.flow_id}.",
            tools=["Bash"],
            setting_sources=[],
            permission_mode=cast(PermissionMode | None, permission_mode),
            hooks={
                "PreToolUse": [
                    HookMatcher(matcher="mcp__tools__publish", hooks=[rewrite_publish_input]),
                    HookMatcher(matcher="mcp__tools__(publish|lookup)", hooks=[stamp_tool_input]),
                ]
            },
        )

    return claude_agent_session_flow(
        build_options,
        build_custom_tools(state_dir),
        flow_type=CLAUDE_TEST_AGENT_FLOW_TYPE,
        tool_policies=TOOL_POLICIES,
    )


def build_claude_test_agent_flow_from_environment() -> AgentSessionFlow:
    return build_claude_test_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


async def rewrite_publish_input(
    hook_input: HookInput, tool_use_id: str | None, context: HookContext
) -> HookJSONOutput:
    tool_input = cast(dict[str, Any], hook_input).get("tool_input") or {}
    if tool_input.get("message") != "rewrite me":
        return {}
    return cast(
        HookJSONOutput,
        {
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "updatedInput": REWRITTEN_PUBLISH_INPUT,
            }
        },
    )


async def stamp_tool_input(
    hook_input: HookInput, tool_use_id: str | None, context: HookContext
) -> HookJSONOutput:
    tool_input = dict(cast(dict[str, Any], hook_input).get("tool_input") or {})
    field_name = "query" if "query" in tool_input else "message"
    if not str(tool_input.get(field_name, "")).startswith("stamp"):
        return {}
    tool_input[field_name] = f"{tool_input[field_name]}+"
    return cast(
        HookJSONOutput,
        {"hookSpecificOutput": {"hookEventName": "PreToolUse", "updatedInput": tool_input}},
    )


def build_custom_tools(state_dir: Path) -> list[SdkMcpTool[Any]]:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    @tool(
        "lookup",
        "Look up a record",
        {"query": str},
        annotations=ToolAnnotations(readOnlyHint=True),
    )
    async def lookup(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("lookup")
        return _text_result(f"found {arguments['query']}")

    @tool("publish", "Publish a message", {"message": str})
    async def publish(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("publish")
        return _text_result(f"published {arguments['message']}")

    @tool(
        "charge",
        "Charge an amount",
        {
            "type": "object",
            "properties": {"amount": {"type": "integer"}, "fail_attempts": {"type": "integer"}},
            "required": ["amount"],
        },
        annotations=ToolAnnotations(idempotentHint=True),
    )
    async def charge(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("charge")
        execution = current_deferred_tool_execution()
        if execution is None:
            raise RuntimeError("charge must run in a deferred tool Step")
        if execution.attempt <= int(arguments.get("fail_attempts", 0)):
            raise RuntimeError(f"charge attempt {execution.attempt} lost its connection")
        if int(arguments["amount"]) <= 0:
            return _text_result("amount must be positive", is_error=True)
        return _text_result(f"charged {arguments['amount']} with key {execution.idempotency_key}")

    @tool("slow_write", "Write slowly", {"message": str})
    async def slow_write(arguments: dict[str, Any]) -> dict[str, Any]:
        invocations.record("slow_write")
        gate = FileGate(state_dir, SLOW_WRITE_GATE)
        gate.mark_reached()
        await gate.wait_for_release()
        return _text_result(f"wrote {arguments['message']}")

    return [lookup, publish, charge, slow_write]


def _text_result(text: str, *, is_error: bool = False) -> dict[str, Any]:
    return {"content": [{"type": "text", "text": text}], "is_error": is_error}
