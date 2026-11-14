# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""connectors.mcp: auto runs dexcli connectors mcp for this machine's connections.json."""

from __future__ import annotations

import asyncio
import json
import os
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from dex_agents.testing import PrivateDexEngine, find_free_ports
from tests.conftest import CliHome, FakeConnectorsMcp, config_yaml

# dex.server names a server, so no dexcli dev runs; listing connectors makes no Dex call.
REMOTE_SERVER: dict[str, Any] = {
    "dex": {"server": "http://127.0.0.1:1"},
    "connectors": {"mcp": "auto"},
}
SLACK_CONNECTION = {"connectorId": "slack", "connectionName": "support-slack"}

# Serves connectors mcp from the test's variables and passes every other command to the real dexcli.
_WRAPPER_SCRIPT = """#!/bin/sh
if [ "$1" = "connectors" ] && [ "$2" = "mcp" ]; then
  echo "$@" > "$DEX_TEST_MARKER_DIRECTORY/connectors-mcp.args"
  case "$DEX_TEST_CONNECTORS_MCP_MODE" in
    unsupported) echo 'unknown command "connectors"' >&2; exit 2 ;;
    error) echo '{"type":"error","message":"fake startup failure"}'; exit 1 ;;
  esac
  printf '{"type":"ready","url":"%s","pid":%s}\\n' "$DEX_TEST_CONNECTORS_MCP_URL" "$$"
  cat > /dev/null
  touch "$DEX_TEST_MARKER_DIRECTORY/connectors-mcp.stopped"
  exit 0
fi
exec "$DEX_TEST_REAL_DEXCLI" "$@"
"""


@pytest.fixture
def wrapped_home(tmp_path: Path) -> Iterator[CliHome]:
    """A private home whose DEXCLI is the wrapper script; the test sets its ports."""
    dex_port, web_port = find_free_ports(2)
    home = CliHome(
        home=tmp_path / "wrapped-home",
        dex_port=dex_port,
        web_port=web_port,
        opened_urls=tmp_path / "opened-urls.txt",
    )
    home.home.mkdir()
    markers = tmp_path / "markers"
    markers.mkdir()
    wrapper = tmp_path / "dexcli-wrapper.sh"
    wrapper.write_text(_WRAPPER_SCRIPT, encoding="utf-8")
    wrapper.chmod(0o755)
    home.extra_environment.update(
        DEXCLI=str(wrapper),
        DEX_TEST_REAL_DEXCLI=os.environ.get("DEXCLI", "dexcli"),
        DEX_TEST_MARKER_DIRECTORY=str(markers),
        DEX_TEST_CONNECTORS_MCP_MODE="ready",
    )
    yield home
    home.close()


def _markers(home: CliHome) -> Path:
    return Path(home.extra_environment["DEX_TEST_MARKER_DIRECTORY"])


def _write_connections(home: CliHome, connections: list[dict[str, str]]) -> None:
    directory = home.home / ".dex" / "connectors"
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "connections.json").write_text(
        json.dumps(
            {
                "schemaVersion": "connectors.dex.dev/local-connections/v1alpha1",
                "connections": connections,
            }
        ),
        encoding="utf-8",
    )


async def _wait_until_stopped(home: CliHome) -> None:
    for _ in range(200):
        if (_markers(home) / "connectors-mcp.stopped").exists():
            return
        await asyncio.sleep(0.05)
    raise AssertionError("dexcli connectors mcp did not stop when its stdin closed")


async def test_a_remote_server_lists_this_machines_connector_tools_and_stops_the_command(
    wrapped_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_URL"] = fake_mcp.url
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))
    _write_connections(wrapped_home, [SLACK_CONNECTION])

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 0, completed.stderr
    assert f"Connector tools at {fake_mcp.url}:" in completed.stdout
    assert "slack_post_thread_reply  (slack/support-slack postThreadReply)" in completed.stdout
    arguments = (_markers(wrapped_home) / "connectors-mcp.args").read_text().strip()
    assert arguments == "connectors mcp --exit-on-stdin-close"
    await _wait_until_stopped(wrapped_home)


async def test_a_remote_server_without_a_local_connection_keeps_connector_tools_off(
    wrapped_home: CliHome,
) -> None:
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 1, completed.stderr
    assert completed.stdout == (
        "Connectors are off: this machine has no authorized Connector connection. Authorize "
        "one on the Connectors page of Dex Web in a local dexcli dev, then run this command "
        "again.\n"
    )
    assert not (_markers(wrapped_home) / "connectors-mcp.args").exists()


async def test_a_remote_server_with_an_older_dexcli_names_the_version_it_needs(
    wrapped_home: CliHome,
) -> None:
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_MODE"] = "unsupported"
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))
    _write_connections(wrapped_home, [SLACK_CONNECTION])

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 1, completed.stderr
    assert "needs dexcli 1.47.7 or later to serve this machine's Connectors" in completed.stdout
    assert "Update it, or set connectors.mcp to a URL." in completed.stdout


async def test_a_remote_server_whose_dexcli_connectors_mcp_fails_says_why(
    wrapped_home: CliHome,
) -> None:
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_MODE"] = "error"
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))
    _write_connections(wrapped_home, [SLACK_CONNECTION])

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 1, completed.stderr
    assert completed.stdout.startswith(
        "Connectors are off: dexcli connectors mcp failed: fake startup failure; see "
    )


async def test_dex_server_auto_uses_dexcli_connectors_mcp_after_it_finds_dexcli_dev(
    shared_engine: PrivateDexEngine, wrapped_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    wrapped_home.dex_port = shared_engine.dex_port
    wrapped_home.web_port = shared_engine.web_port
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_URL"] = fake_mcp.url
    wrapped_home.write_config(config_yaml({"connectors": {"mcp": "auto"}}))

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 0, completed.stderr
    assert f"Connector tools at {fake_mcp.url}:" in completed.stdout
    web_url = f"http://127.0.0.1:{shared_engine.web_port}"
    assert f"Connections in Dex Web ({web_url}/v1/connectors)" in completed.stdout
    await _wait_until_stopped(wrapped_home)


async def test_dex_server_auto_falls_back_to_the_dexcli_dev_url_when_the_command_fails(
    shared_engine: PrivateDexEngine, wrapped_home: CliHome
) -> None:
    wrapped_home.dex_port = shared_engine.dex_port
    wrapped_home.web_port = shared_engine.web_port
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_MODE"] = "error"
    wrapped_home.write_config(config_yaml({"connectors": {"mcp": "auto"}}))

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert "The connectors MCP server is unavailable: " in completed.stdout
    log = (wrapped_home.home / ".dex" / "agents" / "scripted.log").read_text()
    web_url = f"http://127.0.0.1:{shared_engine.web_port}"
    assert f"Using {web_url}/mcp of dexcli dev instead." in log
