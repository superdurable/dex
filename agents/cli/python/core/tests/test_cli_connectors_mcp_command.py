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
import re
import signal
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from dex_agents.testing import PrivateDexEngine, find_free_ports
from tests.conftest import CliHome, FakeConnectorsMcp, config_yaml, wait_for_exit

# dex.server names a server, so no dexcli dev runs; listing connectors makes no Dex call.
REMOTE_SERVER: dict[str, Any] = {
    "dex": {"server": "http://127.0.0.1:1"},
    "connectors": {"mcp": "auto"},
}
SLACK_CONNECTION = {"connectorId": "slack", "connectionName": "support-slack"}

# Serves connectors mcp from the test's variables and passes every other command to the real dexcli.
# Each mode other than unsupported and error waits for stdin to close, then records whether
# the Dex Web of dexcli dev still answered.
_WRAPPER_SCRIPT = """#!/bin/sh
if [ "$1" = "connectors" ] && [ "$2" = "mcp" ]; then
  markers="$DEX_TEST_MARKER_DIRECTORY"
  echo "$@" > "$markers/connectors-mcp.args"
  echo $$ > "$markers/connectors-mcp.pid"
  case "$DEX_TEST_CONNECTORS_MCP_MODE" in
    unsupported) echo 'unknown command "connectors"' >&2; exit 2 ;;
    error) echo '{"type":"error","message":"fake startup failure"}'; exit 1 ;;
    hang) ;;
    empty-line) echo "" ;;
    close-stdout) exec 1>&- ;;
    null) echo null ;;
    bad-url) echo '{"type":"ready","url":"http://["}' ;;
    huge) head -c 1100000 /dev/zero | tr '\\0' a ;;
    orphan)
      sh -c 'trap "" TERM; echo $$ > "$0/descendant.pid"; while :; do sleep 1; done' "$markers" &
      while [ ! -s "$markers/descendant.pid" ]; do sleep 0.05; done
      printf '{"type":"ready","url":"%s","pid":%s}\\n' "$DEX_TEST_CONNECTORS_MCP_URL" "$$" ;;
    *) printf '{"type":"ready","url":"%s","pid":%s}\\n' "$DEX_TEST_CONNECTORS_MCP_URL" "$$" ;;
  esac
  cat > /dev/null
  curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$DEX_AGENTS_WEB_PORT/readyz" \\
    > "$markers/dev-at-stop" 2>/dev/null
  touch "$markers/connectors-mcp.stopped"
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


async def test_a_remote_server_with_a_dexcli_that_may_lack_the_command_names_the_version(
    wrapped_home: CliHome,
) -> None:
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_MODE"] = "unsupported"
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))
    _write_connections(wrapped_home, [SLACK_CONNECTION])

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 1, completed.stderr
    assert re.fullmatch(
        r"Connectors are off: \S+dexcli-wrapper\.sh may not support dexcli connectors mcp, "
        r"which needs dexcli 1\.47\.7 or later; see \S+dexcli-connectors-mcp\.log\. "
        r"Update it, or set connectors\.mcp to a URL\.\n",
        completed.stdout,
    ), completed.stdout


@pytest.mark.parametrize(
    ("mode", "reason"),
    [
        ("empty-line", "dexcli connectors mcp printed an empty line"),
        ("close-stdout", "dexcli connectors mcp closed its output without a line"),
        ("null", "unexpected output: null"),
        ("bad-url", 'unexpected output: {"type":"ready","url":"http://["}'),
        ("huge", "dexcli connectors mcp printed more than 1048576 bytes without a newline"),
    ],
)
async def test_a_bad_ready_line_stops_the_child_and_keeps_connector_tools_off(
    wrapped_home: CliHome, mode: str, reason: str
) -> None:
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_MODE"] = mode
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))
    _write_connections(wrapped_home, [SLACK_CONNECTION])

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 1, completed.stderr
    off_line = completed.stdout.splitlines()[0]
    assert off_line.startswith("Connectors are off: dexcli connectors mcp failed: "), off_line
    assert reason in off_line
    assert len(off_line) < 1_000, "the line carries a bounded reason"
    assert (_markers(wrapped_home) / "connectors-mcp.stopped").exists(), "the child was stopped"


async def test_stopping_kills_a_descendant_that_ignores_sigterm_after_the_leader_exits(
    wrapped_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_MODE"] = "orphan"
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_URL"] = fake_mcp.url
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))
    _write_connections(wrapped_home, [SLACK_CONNECTION])

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 0, completed.stderr
    descendant = int((_markers(wrapped_home) / "descendant.pid").read_text().strip())
    # The killed orphan may stay a zombie until init reaps it.
    for _ in range(100):
        if not _is_process_alive(descendant):
            return
        await asyncio.sleep(0.05)
    raise AssertionError(f"descendant {descendant} that ignores SIGTERM still runs")


async def test_a_sigterm_while_it_waits_stops_it_before_the_dexcli_dev_it_started(
    wrapped_home: CliHome,
) -> None:
    wrapped_home.extra_environment["DEX_TEST_CONNECTORS_MCP_MODE"] = "hang"
    wrapped_home.write_config(config_yaml({"connectors": {"mcp": "auto"}}))
    child = wrapped_home.spawn("connectors")
    pid_path = _markers(wrapped_home) / "connectors-mcp.pid"
    for _ in range(2400):
        if pid_path.exists():
            break
        await asyncio.sleep(0.05)
    assert pid_path.exists(), "dexcli connectors mcp started after dexcli dev"

    child.kill(signal.SIGTERM)

    assert await wait_for_exit(child, timeout=120) == 143
    assert (_markers(wrapped_home) / "connectors-mcp.stopped").exists()
    assert (
        _markers(wrapped_home) / "dev-at-stop"
    ).read_text().strip() == "200", "dexcli dev still ran when dexcli connectors mcp stopped"


def _is_process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


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


async def test_an_unwritable_log_file_keeps_connector_tools_off_instead_of_failing(
    wrapped_home: CliHome,
) -> None:
    wrapped_home.write_config(config_yaml(REMOTE_SERVER))
    _write_connections(wrapped_home, [SLACK_CONNECTION])
    log_path = wrapped_home.home / ".dex" / "agents" / "dexcli-connectors-mcp.log"
    log_path.write_text("", encoding="utf-8")
    log_path.chmod(0o400)

    completed = await asyncio.to_thread(wrapped_home.run, "connectors")

    assert completed.returncode == 1, completed.stderr
    assert completed.stdout.startswith(
        "Connectors are off: dexcli connectors mcp failed: cannot run "
    )
    assert not (_markers(wrapped_home) / "connectors-mcp.args").exists()


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
