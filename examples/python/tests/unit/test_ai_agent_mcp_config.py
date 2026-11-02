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

"""The AI Agent Worker's MCP configuration file."""

from __future__ import annotations

from pathlib import Path

import pytest
from dex_agents.mcp import McpStdioServerConfig, McpStreamableHttpServerConfig

from dex_examples.products.ai_agent.ai_agent_session import load_mcp_servers


def test_mcp_servers_resolve_secrets_and_distrust_annotations_by_default(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("DOCS_TOKEN", "Bearer docs-token")
    monkeypatch.setenv("SEARCH_KEY", "search-key")
    config = tmp_path / "mcp.yaml"
    config.write_text(
        """
servers:
  - name: search
    transport: stdio
    command: search-server
    args: [--stdio]
    env:
      API_KEY: SEARCH_KEY
  - name: docs
    transport: streamable_http
    url: https://docs.example.test/mcp
    headers:
      Authorization: DOCS_TOKEN
    trust_read_only_annotations: true
""".strip()
    )

    search, docs = load_mcp_servers(config)

    assert isinstance(search, McpStdioServerConfig)
    assert search.args == ("--stdio",)
    assert search.env == {"API_KEY": "search-key"}
    assert not search.trusts_tool_annotations
    assert isinstance(docs, McpStreamableHttpServerConfig)
    assert docs.headers == {"Authorization": "Bearer docs-token"}
    assert docs.trusts_tool_annotations
    assert load_mcp_servers(None) == []


@pytest.mark.parametrize(
    ("server", "message"),
    [
        (
            "{name: docs, transport: stdio, command: x, tools: {search: {read_only: true}}}",
            "MCP server 'docs' has unsupported keys \\['tools'\\]",
        ),
        ("{name: docs, transport: websocket}", "needs transport stdio or streamable_http"),
        (
            "{name: docs, transport: stdio, command: x, env: {KEY: MISSING_MCP_SECRET}}",
            "needs the environment variables \\['MISSING_MCP_SECRET'\\]",
        ),
    ],
)
def test_mcp_servers_reject_invalid_configuration(
    tmp_path: Path, server: str, message: str
) -> None:
    config = tmp_path / "mcp.yaml"
    config.write_text(f"servers:\n  - {server}\n")
    with pytest.raises(ValueError, match=message):
        load_mcp_servers(config)
