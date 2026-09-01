# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Integration coverage for durable context and MCP tool routing."""

from __future__ import annotations

import asyncio
import socket
import sys
from collections.abc import Callable
from pathlib import Path

from dex import AsyncClient, StartFlowOptions

from dex_examples.app import ExampleApp
from dex_examples.products.ai_agent.mcp_registry import MCPRegistry
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    HistoryRequest,
    ToolApprovalRequest,
    UserMessage,
)
from tests.integ.conftest import WAIT_TIMEOUT, wait_until


async def test_mcp_registry_supports_streamable_http(tmp_path: Path) -> None:
    port = _available_port()
    server_path = Path(__file__).with_name("ai_agent_mcp_server.py")
    process = await asyncio.create_subprocess_exec(
        sys.executable,
        str(server_path),
        "--transport",
        "streamable-http",
        "--port",
        str(port),
        stdout=asyncio.subprocess.DEVNULL,
        stderr=asyncio.subprocess.PIPE,
    )
    registry: MCPRegistry | None = None
    try:
        await _wait_for_port(port, process)
        config_path = tmp_path / "mcp-http.yaml"
        config_path.write_text(
            f"""
servers:
  - name: http_test
    transport: streamable_http
    url: http://127.0.0.1:{port}/mcp
    trust_read_only_annotations: true
""".strip()
        )
        registry = MCPRegistry.from_file(config_path)
        await registry.start()

        async def write_progress(message: str) -> None:
            pass

        result = await registry.execute(
            "http_test__lookup",
            {"query": "streamable"},
            [],
            write_progress,
        )
        assert "found:streamable" in result.content
    finally:
        if registry is not None:
            await registry.close()
        process.terminate()
        await process.wait()


async def test_ai_agent_executes_mcp_primitives_and_approval(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-mcp")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(enabled_mcp_servers=["test"]),
        StartFlowOptions(),
    )
    await _send(client, app, flow_id, '/tool test__lookup {"query":"dex"}')
    await _wait_for_content(client, app, flow_id, "found:dex")

    await _send(client, app, flow_id, '/tool test__publish {"message":"hello"}')

    async def approval_id() -> str | None:
        description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
        return description.pending_approval_call_id

    call_id = await wait_until("MCP write approval", approval_id, WAIT_TIMEOUT)
    assert await client.invoke_rpc(
        app.ai_agent.approve_tool,
        flow_id,
        ToolApprovalRequest(call_id, True),
    )
    await _wait_for_content(client, app, flow_id, "published:hello")

    await _send(
        client,
        app,
        flow_id,
        '/tool mcp_read_resource {"server":"test","uri":"test://guide"}',
    )
    await _wait_for_content(client, app, flow_id, "Durable MCP resource")

    await _send(
        client,
        app,
        flow_id,
        '/tool mcp_get_prompt {"server":"test","name":"greeting","arguments":{"name":"Dex"}}',
    )
    await _wait_for_content(client, app, flow_id, "Greet Dex")


async def test_ai_agent_compacts_before_enforcing_message_retention(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-compaction")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(
            max_context_tokens=80,
            compaction_trigger_fraction=0.5,
            compaction_keep_fraction=0.1,
            message_retention_limit=3,
        ),
        StartFlowOptions(),
    )

    for index in range(5):
        await _send(client, app, flow_id, f"turn {index}: " + "context " * 20)
        expected_sequence = (index + 1) * 2

        async def turn_finished() -> bool:
            description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
            return description.last_sequence >= expected_sequence

        await wait_until(f"AI Agent turn {index}", turn_finished, WAIT_TIMEOUT)

    description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
    page = await client.invoke_rpc(
        app.ai_agent.history,
        flow_id,
        HistoryRequest(limit=20),
    )
    assert description.summarized_through_sequence > 0
    assert len(page.messages) <= 3
    assert page.messages[-1].message.role == "assistant"


async def test_ai_agent_rejects_tools_disabled_for_the_session(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("ai-agent-tool-allowlist")
    await client.start_flow(
        app.ai_agent,
        flow_id,
        AgentConfig(
            enabled_mcp_servers=["test"],
            enabled_tools=["test__lookup"],
        ),
        StartFlowOptions(),
    )

    await _send(client, app, flow_id, '/tool test__publish {"message":"blocked"}')
    await _wait_for_content(client, app, flow_id, "unknown_or_disabled_tool")
    description = await client.invoke_rpc(app.ai_agent.describe, flow_id)
    assert description.pending_approval_call_id is None


async def _send(
    client: AsyncClient,
    app: ExampleApp,
    flow_id: str,
    content: str,
) -> None:
    assert await client.invoke_rpc(
        app.ai_agent.send_message,
        flow_id,
        UserMessage(content),
    )


async def _wait_for_content(
    client: AsyncClient,
    app: ExampleApp,
    flow_id: str,
    expected: str,
) -> None:
    async def contains_content() -> bool:
        page = await client.invoke_rpc(
            app.ai_agent.history,
            flow_id,
            HistoryRequest(limit=100),
        )
        return any(expected in item.message.content for item in page.messages)

    await wait_until(f"message containing {expected!r}", contains_content, WAIT_TIMEOUT)


def _available_port() -> int:
    with socket.socket() as server_socket:
        server_socket.bind(("127.0.0.1", 0))
        return int(server_socket.getsockname()[1])


async def _wait_for_port(
    port: int,
    process: asyncio.subprocess.Process,
) -> None:
    deadline = asyncio.get_running_loop().time() + 10
    while asyncio.get_running_loop().time() < deadline:
        if process.returncode is not None:
            stderr = await process.stderr.read() if process.stderr is not None else b""
            raise RuntimeError(f"MCP HTTP server exited: {stderr.decode()}")
        try:
            _, writer = await asyncio.open_connection("127.0.0.1", port)
            writer.close()
            await writer.wait_closed()
            return
        except OSError:
            await asyncio.sleep(0.05)
    raise RuntimeError("MCP HTTP server did not become ready")
