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

"""The Slack connection that the AI Agent and the agent harness examples open."""

from __future__ import annotations

import json
from pathlib import Path

from dex_connectors.sdk import ConnectionRef

from dex_examples.products.ai_agent.slack_connector_tools import (
    SLACK_CONNECTION_NAME,
    open_slack_project_connection,
)

EXAMPLES_DIR = Path(__file__).resolve().parents[2]
SLACK_CONNECTOR_TOOLS_COPIES = (
    EXAMPLES_DIR / "dex_examples" / "products" / "ai-agent" / "slack_connector_tools.py",
    EXAMPLES_DIR / "agent-harnesses" / "agent_harness_examples" / "slack_connector_tools.py",
)
LOCAL_CONNECTIONS_SCHEMA_VERSION = "connectors.dex.dev/local-connections/v1alpha1"


def test_the_two_projects_share_one_slack_connector_tools_module() -> None:
    ai_agent_copy, agent_harnesses_copy = SLACK_CONNECTOR_TOOLS_COPIES
    assert ai_agent_copy.read_bytes() == agent_harnesses_copy.read_bytes()


async def test_slack_is_off_without_a_project_configuration() -> None:
    assert await open_slack_project_connection({}) is None


async def test_slack_is_off_when_the_connections_file_is_missing(tmp_path: Path) -> None:
    environment = {"DEX_CONNECTOR_CONFIG_FILE": str(tmp_path / "connections.json")}
    assert await open_slack_project_connection(environment) is None


async def test_slack_is_off_when_the_project_has_no_slack_connection(tmp_path: Path) -> None:
    connections_file = _write_connections_file(tmp_path, [])
    environment = {"DEX_CONNECTOR_CONFIG_FILE": str(connections_file)}
    assert await open_slack_project_connection(environment) is None


async def test_slack_opens_the_named_project_connection(tmp_path: Path) -> None:
    connections_file = _write_connections_file(
        tmp_path,
        [
            {
                "connectorId": "slack",
                "connectionName": SLACK_CONNECTION_NAME,
                "modulePath": "github.com/superdurable/dex-connectors-library/connectors/slack/go",
                "provider": "slack",
                "configuration": {},
                "status": "READY",
                "credentials": {"bot_token": "xoxb-test", "user_token": "xoxp-test"},
            }
        ],
    )
    environment = {"DEX_CONNECTOR_CONFIG_FILE": str(connections_file)}
    opened = await open_slack_project_connection(environment)
    assert opened is not None
    try:
        assert opened.connection.reference == ConnectionRef("slack", SLACK_CONNECTION_NAME)
        assert "xoxb-test" not in repr(opened)
    finally:
        await opened.aclose()


def _write_connections_file(directory: Path, connections: list[dict[str, object]]) -> Path:
    """Writes a Dex Web connections.json, which the connector SDK reads only with mode 0600."""
    path = directory / "connections.json"
    path.write_text(
        json.dumps({"schemaVersion": LOCAL_CONNECTIONS_SCHEMA_VERSION, "connections": connections})
    )
    path.chmod(0o600)
    return path
