# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A connector task an earlier attempt started is resumed, whatever the server lists now.

The first attempt of a tool Step starts the task and saves its handle as the
heartbeat checkpoint; its Worker then exits before the Step result commits.
The retried attempt gets the checkpoint, so these tests call the source's
``execute_deferred_tool_call`` twice the way the session does.
"""

from __future__ import annotations

from dataclasses import replace

import pytest

from dex_agents import DeferredToolExecution, ToolEffect, ToolExecutionError, ToolTaskStarted
from dex_agents.mcp import McpTool
from dex_agents_cli_core import ConnectorsMcpToolSource, TurnConfigResolver, resolve_config
from dex_agents_cli_core.connectors import ConnectorToolDefinition, read_connector_catalog
from tests.conftest import FakeConnectorsMcp
from tests.fake_connectors_mcp import REDDIT_CONNECTION_ENVIRONMENT_VARIABLE
from tests.test_tool_calls import execution, turn_context

REPORT_TOOL = "reddit_export_report"


async def start_report_task(
    fake_mcp: FakeConnectorsMcp,
) -> tuple[ConnectorsMcpToolSource, DeferredToolExecution, str]:
    """Run the first attempt; return the source, the retried attempt, and the task ID."""
    config = resolve_config(
        {"connectors": {"mcp": fake_mcp.url, "enabled": ["reddit"]}}, "builtin", "scripted-model"
    )
    resolver = TurnConfigResolver(config, None, lambda warnings: None)
    source = ConnectorsMcpToolSource(fake_mcp.url, resolver)
    (identity,) = [
        tool.identity
        for tool in await read_connector_catalog(fake_mcp.url)
        if tool.name == REPORT_TOOL
    ]
    listed = ConnectorToolDefinition(
        name=REPORT_TOOL,
        description="Export a report.",
        input_schema={"type": "object"},
        mcp_tool=McpTool(
            name=f"connectors__{REPORT_TOOL}",
            server_name="connectors",
            mcp_tool_name=REPORT_TOOL,
            description="Export a report.",
            input_schema={"type": "object"},
            annotations=None,
            framework_effect=ToolEffect.READ,
        ),
        identity=identity,
    )
    call = source.deferred_tool_call(
        turn_context({}),
        tool_call_id="call-1",
        tool_name=REPORT_TOOL,
        arguments="{}",
        listed_tool=listed,
    )
    saved: list[str] = []

    async def save_heartbeat_checkpoint(checkpoint: str) -> None:
        saved.append(checkpoint)

    first = replace(
        execution(call),
        save_heartbeat_checkpoint=save_heartbeat_checkpoint,
        supports_tool_tasks=True,
    )
    started = await source.execute_deferred_tool_call(first)
    assert isinstance(started, ToolTaskStarted)
    (checkpoint,) = saved
    retried = replace(first, attempt=2, heartbeat_checkpoint=checkpoint)
    return source, retried, started.task_id


async def test_a_started_task_is_resumed_while_the_server_cannot_be_listed(
    fake_mcp: FakeConnectorsMcp,
) -> None:
    source, retried, task_id = await start_report_task(fake_mcp)
    fake_mcp.stop()
    resumed = await source.execute_deferred_tool_call(retried)
    assert resumed == ToolTaskStarted(task_id=task_id, poll_interval_seconds=1)
    assert fake_mcp.runs(REPORT_TOOL) == 1


async def test_a_started_task_is_resumed_after_its_tool_name_moved_to_another_connection(
    fake_mcp: FakeConnectorsMcp,
) -> None:
    source, retried, task_id = await start_report_task(fake_mcp)
    fake_mcp.stop()
    await fake_mcp.start({REDDIT_CONNECTION_ENVIRONMENT_VARIABLE: "other-reddit"})
    resumed = await source.execute_deferred_tool_call(retried)
    assert resumed == ToolTaskStarted(task_id=task_id, poll_interval_seconds=1)
    # The retry sent no new tools/call, so the server ran the operation once.
    assert fake_mcp.runs(REPORT_TOOL) == 1


async def test_a_new_call_of_a_moved_tool_name_is_refused_before_it_is_sent(
    fake_mcp: FakeConnectorsMcp,
) -> None:
    source, retried, _ = await start_report_task(fake_mcp)
    fresh = replace(retried, attempt=1, heartbeat_checkpoint=None)
    fake_mcp.stop()
    await fake_mcp.start({REDDIT_CONNECTION_ENVIRONMENT_VARIABLE: "other-reddit"})
    with pytest.raises(ToolExecutionError, match="now runs reddit/other-reddit exportReport"):
        await source.execute_deferred_tool_call(fresh)
    assert fake_mcp.runs(REPORT_TOOL) == 1


async def test_a_task_polled_in_the_call_is_checked_because_it_may_be_sent_again(
    fake_mcp: FakeConnectorsMcp,
) -> None:
    # The saved checkpoint has the same shape for a task polled inside the call.
    source, retried, _ = await start_report_task(fake_mcp)
    polled_in_call = replace(retried, supports_tool_tasks=False)
    fake_mcp.stop()
    # The restarted server lost the task, and the name now runs another connection.
    await fake_mcp.start({REDDIT_CONNECTION_ENVIRONMENT_VARIABLE: "other-reddit"})
    with pytest.raises(ToolExecutionError, match="now runs reddit/other-reddit exportReport"):
        await source.execute_deferred_tool_call(polled_in_call)
    assert fake_mcp.runs(REPORT_TOOL) == 1


async def test_a_worker_that_does_not_enable_the_connector_refuses_even_a_resume(
    fake_mcp: FakeConnectorsMcp,
) -> None:
    _, retried, _ = await start_report_task(fake_mcp)
    config = resolve_config(
        {"connectors": {"mcp": fake_mcp.url, "enabled": ["hackernews"]}},
        "builtin",
        "scripted-model",
    )
    other_worker = ConnectorsMcpToolSource(
        fake_mcp.url, TurnConfigResolver(config, None, lambda warnings: None)
    )
    with pytest.raises(ToolExecutionError, match="is switched off for this session"):
        await other_worker.execute_deferred_tool_call(retried)
    assert fake_mcp.runs(REPORT_TOOL) == 1
