# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""ToolCallGate decisions for tool use IDs with records, driven directly.

The CLI resumes a deferred call under its recorded name and asks the hook
before ``can_use_tool``, so no CLI run reaches a mismatched name or a
permission ask that comes first.
"""

from __future__ import annotations

import json
from dataclasses import replace
from pathlib import Path
from typing import Any, cast

from claude_agent_sdk import (
    HookContext,
    HookInput,
    PermissionResult,
    PermissionResultAllow,
    PermissionResultDeny,
    ToolPermissionContext,
)

from dex_agents import (
    AgentToolCall,
    AgentTurnContext,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
)
from dex_agents_claude_agent_sdk.tool_gate import (
    DECIDED_TOOL_USE_ID_MESSAGE,
    MISMATCHED_REPLAY_MESSAGE,
    REPEATED_REPLAY_MESSAGE,
    UNPERMITTED_TOOL_CALL_MESSAGE,
    ToolCallGate,
    ToolCatalog,
)
from tests.claude_test_agent import build_custom_tools

HOOK_CONTEXT = cast(HookContext, {"signal": None})
REPLAY_ONLY_SOURCE_TOOL_NAMES = ("post_note", "hiding_note")


class RecordedCallsTurn:
    """The parts of ``AgentTurnContext`` the gate uses, with fixed records."""

    def __init__(self, records: list[AgentToolCall]) -> None:
        self._records = {record.tool_call_id: record for record in records}
        self.events = self

    def recorded_tool_call(self, tool_call_id: str) -> AgentToolCall | None:
        return self._records.get(tool_call_id)

    def resolve_tool_policy(
        self, tool_name: str, framework_effect: ToolEffect | None = None
    ) -> ToolPolicy:
        return ToolPolicy(framework_effect or ToolEffect.UNCLASSIFIED, requires_approval=False)

    def write_tool_call_started(self, tool_call_id: str, tool_name: str) -> None:
        return None

    def write_tool_call_finished(
        self, tool_call_id: str, tool_name: str, status: ToolCallStatus
    ) -> None:
        return None


async def test_custom_or_source_record_is_never_replayed_as_bash(tmp_path: Path) -> None:
    records = [
        _succeeded_record("call-1", "publish", {"message": "rm -rf build"}),
        _succeeded_record("call-2", "post_note", {"accountId": "a-1", "text": "hello"}),
    ]
    for call_id, recorded_tool_name in (("call-1", "publish"), ("call-2", "post_note")):
        message = MISMATCHED_REPLAY_MESSAGE.format(
            tool_name="Bash", tool_use_id=call_id, recorded_tool_name=recorded_tool_name
        )
        hook_output = await _gate(tmp_path, records).decide_pre_tool_use(
            _hook_input("Bash", call_id, {"command": "ls"}), call_id, HOOK_CONTEXT
        )
        assert _decision(hook_output) == ("deny", message, None)

        permission = await _ask_permission(_gate(tmp_path, records), "Bash", call_id)
        assert permission == PermissionResultDeny(message=message)


async def test_source_record_replays_only_as_its_own_tool(tmp_path: Path) -> None:
    gate = _gate(tmp_path, [_succeeded_record("call-1", "post_note", {"text": "x"})])

    hook_output = await gate.decide_pre_tool_use(
        _hook_input("mcp__tools__hiding_note", "call-1"), "call-1", HOOK_CONTEXT
    )

    assert _decision(hook_output) == (
        "deny",
        MISMATCHED_REPLAY_MESSAGE.format(
            tool_name="hiding_note", tool_use_id="call-1", recorded_tool_name="post_note"
        ),
        None,
    )
    # The denied replay left no plan, so neither tool answers under its ID.
    for tool_name in REPLAY_ONLY_SOURCE_TOOL_NAMES:
        result = await gate.call_custom_tool(tool_name, "call-1", {}, None)
        assert result["is_error"] is True


async def test_source_record_replays_with_its_input_and_never_as_a_bare_cli_name(
    tmp_path: Path,
) -> None:
    record = _succeeded_record("call-1", "post_note", {"text": "x"})
    hook_first = _gate(tmp_path, [record])
    replay = await hook_first.decide_pre_tool_use(
        _hook_input("mcp__tools__post_note", "call-1", {"text": "other"}), "call-1", HOOK_CONTEXT
    )
    assert _decision(replay) == ("allow", None, {"text": "x"})
    # The follow-up ask names the bare tool: same name, another kind.
    assert await _ask_permission(hook_first, "post_note", "call-1") == PermissionResultDeny(
        message=REPEATED_REPLAY_MESSAGE.format(tool_name="post_note", tool_use_id="call-1")
    )

    permission_first = _gate(tmp_path, [record])
    assert await _ask_permission(permission_first, "post_note", "call-1") == PermissionResultDeny(
        message=MISMATCHED_REPLAY_MESSAGE.format(
            tool_name="post_note", tool_use_id="call-1", recorded_tool_name="post_note"
        )
    )


async def test_permission_ask_after_a_hook_allow_is_denied(tmp_path: Path) -> None:
    gate = _gate(tmp_path, [])
    inline = await gate.decide_pre_tool_use(
        _hook_input("mcp__tools__lookup", "call-2", {"query": "q"}), "call-2", HOOK_CONTEXT
    )
    assert _decision(inline) == ("allow", None, None)

    async def run_lookup(arguments: dict[str, Any]) -> dict[str, Any]:
        return {"content": [{"type": "text", "text": f"found {arguments['query']}"}]}

    result = await gate.call_custom_tool("lookup", "call-2", {"query": "q"}, run_lookup)
    assert result["content"][0]["text"] == "found q"
    gate.record_tool_result("call-2", is_error=False)

    # The hook's allow was the call's one decision, so every later ask is denied.
    for cli_tool_name, tool_name in (
        ("mcp__tools__lookup", "lookup"),
        ("lookup", "lookup"),
        ("Bash", "Bash"),
    ):
        assert await _ask_permission(
            gate, cli_tool_name, "call-2", {"query": "other"}
        ) == PermissionResultDeny(message=UNPERMITTED_TOOL_CALL_MESSAGE.format(tool_name=tool_name))


async def test_tool_use_id_gets_one_hook_decision_per_segment(tmp_path: Path) -> None:
    gate = _gate(tmp_path, [])
    first_input = _hook_input("mcp__tools__lookup", "call-3", {"query": "q"})
    assert _decision(await gate.decide_pre_tool_use(first_input, "call-3", HOOK_CONTEXT)) == (
        "allow",
        None,
        None,
    )
    unpermitted = PermissionResultDeny(
        message=UNPERMITTED_TOOL_CALL_MESSAGE.format(tool_name="lookup")
    )
    assert await _ask_permission(gate, "mcp__tools__lookup", "call-3") == unpermitted

    second_input = _hook_input("mcp__tools__lookup", "call-3", {"query": "other"})
    assert _decision(await gate.decide_pre_tool_use(second_input, "call-3", HOOK_CONTEXT)) == (
        "deny",
        DECIDED_TOOL_USE_ID_MESSAGE.format(tool_name="lookup", tool_use_id="call-3"),
        None,
    )
    assert await _ask_permission(gate, "mcp__tools__lookup", "call-3") == unpermitted
    # The repeated hook decision planned nothing, so the custom tool does not run again.
    result = await gate.call_custom_tool("lookup", "call-3", {"query": "other"}, None)
    assert result["is_error"] is True


async def test_recorded_call_is_replayed_at_most_once_per_segment(tmp_path: Path) -> None:
    record = _succeeded_record("call-1", "Bash", {"command": "echo reviewed"})
    repeated = REPEATED_REPLAY_MESSAGE.format(tool_name="Bash", tool_use_id="call-1")

    hook_first = _gate(tmp_path, [record])
    first = await hook_first.decide_pre_tool_use(
        _hook_input("Bash", "call-1", {"command": "rm -rf build"}), "call-1", HOOK_CONTEXT
    )
    assert _decision(first) == ("allow", None, {"command": "echo reviewed"})
    # The hook's replay was the one decision; a later ask is denied.
    assert await _ask_permission(
        hook_first, "Bash", "call-1", {"command": "rm -rf build"}
    ) == PermissionResultDeny(message=repeated)
    second = await hook_first.decide_pre_tool_use(
        _hook_input("Bash", "call-1"), "call-1", HOOK_CONTEXT
    )
    assert _decision(second) == ("deny", repeated, None)

    # With the hook skipped, the first ask decides the replay with the recorded input.
    permission_first = _gate(tmp_path, [record])
    assert await _ask_permission(
        permission_first, "Bash", "call-1", {"command": "rm -rf build"}
    ) == PermissionResultAllow(updated_input={"command": "echo reviewed"})
    assert await _ask_permission(permission_first, "Bash", "call-1") == PermissionResultDeny(
        message=repeated
    )
    after_permission = await permission_first.decide_pre_tool_use(
        _hook_input("Bash", "call-1"), "call-1", HOOK_CONTEXT
    )
    assert _decision(after_permission) == ("deny", repeated, None)

    rejected_record = replace(
        record, status=ToolCallStatus.REJECTED, output=None, rejection_message="no"
    )
    rejected = _gate(tmp_path, [rejected_record])
    rejected_output = await rejected.decide_pre_tool_use(
        _hook_input("Bash", "call-1"), "call-1", HOOK_CONTEXT
    )
    # A hook denial reaches the model as a hook error, so the ask that follows denies it.
    rejection = "The user rejected this Bash call: no"
    assert _decision(rejected_output) == ("ask", rejection, None)
    assert await _ask_permission(rejected, "Bash", "call-1") == PermissionResultDeny(
        message=rejection
    )
    assert await _ask_permission(rejected, "Bash", "call-1") == PermissionResultDeny(
        message=repeated
    )


def _gate(tmp_path: Path, records: list[AgentToolCall]) -> ToolCallGate:
    catalog = ToolCatalog(
        build_custom_tools(tmp_path),
        "tools",
        replay_only_tool_names=REPLAY_ONLY_SOURCE_TOOL_NAMES,
    )
    return ToolCallGate(cast(AgentTurnContext, RecordedCallsTurn(records)), catalog, ())


def _succeeded_record(
    tool_call_id: str, tool_name: str, arguments: dict[str, Any]
) -> AgentToolCall:
    return AgentToolCall(
        tool_call_id=tool_call_id,
        turn_id="turn-1",
        tool_name=tool_name,
        arguments=json.dumps(arguments),
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
        status=ToolCallStatus.SUCCEEDED,
        output="recorded",
    )


def _hook_input(
    cli_tool_name: str, tool_use_id: str, tool_input: dict[str, Any] | None = None
) -> HookInput:
    return cast(
        HookInput,
        {
            "hook_event_name": "PreToolUse",
            "tool_name": cli_tool_name,
            "tool_input": tool_input or {},
            "tool_use_id": tool_use_id,
        },
    )


async def _ask_permission(
    gate: ToolCallGate,
    cli_tool_name: str,
    tool_use_id: str,
    tool_input: dict[str, Any] | None = None,
) -> PermissionResult:
    return await gate.decide_tool_permission(
        cli_tool_name, tool_input or {}, ToolPermissionContext(tool_use_id=tool_use_id)
    )


def _decision(hook_output: object) -> tuple[str, str | None, dict[str, Any] | None]:
    specific = cast(dict[str, Any], hook_output)["hookSpecificOutput"]
    return (
        specific["permissionDecision"],
        specific.get("permissionDecisionReason"),
        specific.get("updatedInput"),
    )
