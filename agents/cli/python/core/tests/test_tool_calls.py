# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""How the CLI core's sources build and refuse tool calls for a turn.

A built-in loop session never builds a call of a tool its listing left out, so a
stale listing kept by a runner, or a model naming a switched-off tool, is
driven here directly through the source's ``deferred_tool_call``.
"""

from __future__ import annotations

from collections.abc import Mapping
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    AgentEventWriter,
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
    ToolPolicy,
)
from dex_agents.mcp import McpTool
from dex_agents_cli_core import (
    ConnectorsMcpToolSource,
    LocalToolGroup,
    LocalToolSource,
    TurnConfigResolver,
    resolve_config,
    resolve_turn_tool_policy,
)
from dex_agents_cli_core.connectors import ConnectorToolDefinition, ConnectorToolIdentity
from dex_agents_cli_core.local_tools.source import LOCAL_TOOL_DEFINITIONS

FILE_CONFIG = {
    "connectors": {"mcp": "http://127.0.0.1:9/mcp", "enabled": ["hackernews", "slack"]},
    "local_tools": {"shell": {"allow": ["ls"]}},
}


async def _no_heartbeat() -> None:
    return None


def turn_context(
    session_settings: Mapping[str, Any], tool_policies: Mapping[str, ToolPolicy] | None = None
) -> AgentTurnContext:
    return AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=[],
        framework_run_state=None,
        tool_calls={},
        tool_policies=dict(tool_policies or {}),
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=_no_heartbeat,
        session_settings=session_settings,
    )


def execution(call: DeferredToolCall) -> DeferredToolExecution:
    return DeferredToolExecution(
        flow_id="session-1",
        tool_call=AgentToolCall(
            tool_call_id=call.tool_call_id,
            turn_id="turn-1",
            tool_name=call.tool_name,
            arguments=call.arguments,
            effect=call.policy.effect,
            requires_approval=bool(call.policy.requires_approval),
            status=ToolCallStatus.APPROVED,
            tool_source_reference=call.tool_source_reference,
        ),
        idempotency_key="session-1/turn-1/" + call.tool_call_id,
        attempt=1,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=_no_heartbeat,
    )


@pytest.fixture
def resolver() -> TurnConfigResolver:
    config = resolve_config(FILE_CONFIG, "builtin", "scripted-model")
    return TurnConfigResolver(config, None, lambda warnings: None)


def local_tool(name: str) -> Any:
    return next(definition for definition in LOCAL_TOOL_DEFINITIONS if definition.name == name)


async def test_a_switched_off_local_tool_from_a_stale_listing_is_refused_in_its_step(
    tmp_path: Path, resolver: TurnConfigResolver
) -> None:
    source = LocalToolSource(tmp_path, frozenset(LocalToolGroup), resolver)
    turn = turn_context({"local_tools": {"shell": {"enabled": False}}})
    call = source.deferred_tool_call(
        turn,
        tool_call_id="call-1",
        tool_name="run_shell_command",
        arguments='{"command": "touch ran.txt"}',
        listed_tool=local_tool("run_shell_command"),
    )
    assert call.policy == ToolPolicy(ToolEffect.READ, requires_approval=False)
    with pytest.raises(ToolExecutionError, match="is switched off for this session"):
        await source.execute_deferred_tool_call(execution(call))
    assert not (tmp_path / "ran.txt").exists()


async def test_a_local_tool_the_source_does_not_offer_is_refused(
    tmp_path: Path, resolver: TurnConfigResolver
) -> None:
    source = LocalToolSource(tmp_path, frozenset({LocalToolGroup.FILES}), resolver)
    call = source.deferred_tool_call(
        turn_context({}),
        tool_call_id="call-1",
        tool_name="fetch_web_page",
        arguments='{"url": "https://example.com"}',
        listed_tool=local_tool("fetch_web_page"),
    )
    with pytest.raises(ToolExecutionError, match="is switched off for this session"):
        await source.execute_deferred_tool_call(execution(call))


async def test_a_connector_tool_the_turn_narrowed_away_is_refused_in_its_step(
    resolver: TurnConfigResolver,
) -> None:
    source = ConnectorsMcpToolSource("http://127.0.0.1:9/mcp", resolver)
    listed = ConnectorToolDefinition(
        name="slack_post_thread_reply",
        description="Reply in a thread.",
        input_schema={"type": "object"},
        mcp_tool=McpTool(
            name="connectors__slack_post_thread_reply",
            server_name="connectors",
            mcp_tool_name="slack_post_thread_reply",
            description="Reply in a thread.",
            input_schema={"type": "object"},
            annotations=None,
            framework_effect=None,
        ),
        identity=ConnectorToolIdentity("slack", "work", "postThreadReply", "post_thread_reply"),
    )
    narrowed = turn_context({"connectors": {"enabled": ["hackernews"]}})
    call = source.deferred_tool_call(
        narrowed,
        tool_call_id="call-1",
        tool_name="slack_post_thread_reply",
        arguments="{}",
        listed_tool=listed,
    )
    with pytest.raises(ToolExecutionError, match="is switched off for this session"):
        await source.execute_deferred_tool_call(execution(call))
    allowed = source.deferred_tool_call(
        turn_context({}),
        tool_call_id="call-2",
        tool_name="slack_post_thread_reply",
        arguments="{}",
        listed_tool=listed,
    )
    assert allowed.tool_source_reference is not None
    assert "disabled_tool" not in allowed.tool_source_reference


def test_a_raised_effect_needs_approval_and_explicit_false_never_removes_it(
    resolver: TurnConfigResolver,
) -> None:
    turn_config = resolver.file_config
    raised = turn_context({}, {"fetch_web_page": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE)})
    assert resolve_turn_tool_policy(
        raised, "fetch_web_page", ToolEffect.READ, False, turn_config
    ) == ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=True)
    # An allow-listed shell command asks for no approval, but its effect still does by default.
    lowered = turn_context({}, {"run_shell_command": ToolPolicy(ToolEffect.READ)})
    assert resolve_turn_tool_policy(
        lowered, "run_shell_command", ToolEffect.NON_IDEMPOTENT_WRITE, False, turn_config
    ) == ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=True)
    unapproved = turn_context(
        {}, {"write_file": ToolPolicy(ToolEffect.READ, requires_approval=False)}
    )
    assert resolve_turn_tool_policy(
        unapproved, "write_file", ToolEffect.IDEMPOTENT_WRITE, True, turn_config
    ) == ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=True)
    always = resolver.resolve({"approval": {"always": ["read_file"]}})
    assert resolve_turn_tool_policy(
        turn_context({}), "read_file", ToolEffect.READ, False, always
    ) == ToolPolicy(ToolEffect.READ, requires_approval=True)
