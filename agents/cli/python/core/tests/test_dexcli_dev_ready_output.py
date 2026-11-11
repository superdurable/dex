# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Reading the Dex and Connectors MCP addresses from dexcli dev output and its state file."""

from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

from dex_agents_cli_core.dex_server import _parse_ready_fields, _read_started_server
from dex_agents_cli_core.environment import RuntimeEnvironment

WEB_URL = "http://127.0.0.1:8802"
DEX_ADDRESS = "127.0.0.1:8801"


def ready_block(web_port: int = 8802, connectors_line: str | None = "default") -> str:
    """Return the ready output of dexcli dev; ``connectors_line=None`` omits the MCP line."""
    web_url = f"http://127.0.0.1:{web_port}"
    lines = ["", "Dex development environment is ready", "", f"Dex Web:       {web_url}"]
    if connectors_line == "default":
        lines.append(f"Connectors MCP: {web_url}/mcp")
    elif connectors_line is not None:
        lines.append(connectors_line)
    lines += [
        f"Dex Server:    127.0.0.1:{web_port - 1}",
        "State dir:     /home/user/.dex/zd/8801",
        "",
        "Press Ctrl+C to stop.",
    ]
    return "\n".join(lines) + "\n"


@pytest.mark.parametrize(
    "manager_line",
    [
        "Connectors MCP: started server 0.1.0 with 2 tools at http://127.0.0.1:55895/mcp, "
        "proxied at http://127.0.0.1:8802/mcp",
        "Connectors MCP: reusing running server 0.1.0 with 2 tools at "
        "http://127.0.0.1:55895/mcp, proxied at http://127.0.0.1:8802/mcp",
        "Connectors MCP: unavailable: no release for darwin-arm64. dexcli dev keeps running "
        "without /mcp.",
    ],
    ids=["started", "reusing", "unavailable"],
)
def test_later_connectors_log_lines_do_not_replace_the_ready_url(manager_line: str) -> None:
    fields = _parse_ready_fields("server log line\n" + ready_block() + manager_line + "\n")
    assert fields == {
        "web_url": WEB_URL,
        "connectors_mcp_url": f"{WEB_URL}/mcp",
        "dex_address": DEX_ADDRESS,
    }


def test_the_last_ready_block_of_a_restarted_dexcli_dev_wins() -> None:
    text = (
        ready_block(web_port=9902)
        + "Connectors MCP: started server 0.1.0 with 2 tools at http://127.0.0.1:1/mcp\n"
        + ready_block()
        + "Connectors MCP: started server 0.1.0 with 2 tools at http://127.0.0.1:2/mcp\n"
    )
    assert _parse_ready_fields(text) == {
        "web_url": WEB_URL,
        "connectors_mcp_url": f"{WEB_URL}/mcp",
        "dex_address": DEX_ADDRESS,
    }


def test_an_incomplete_last_ready_block_lacks_the_dex_address_so_the_wait_continues() -> None:
    complete = ready_block(web_port=9902)
    partial = "\nDex development environment is ready\n\nDex Web:       http://127.0.0.1:8802\n"
    fields = _parse_ready_fields(complete + partial)
    assert fields == {"web_url": WEB_URL}


def test_connectors_mcp_off_records_no_url() -> None:
    off = "Connectors MCP: off, because --bind-address is not a loopback address"
    fields = _parse_ready_fields(ready_block(connectors_line=off))
    assert fields == {"web_url": WEB_URL, "dex_address": DEX_ADDRESS}


def test_a_dexcli_without_the_connectors_mcp_line_records_no_url() -> None:
    fields = _parse_ready_fields(ready_block(connectors_line=None))
    assert fields == {"web_url": WEB_URL, "dex_address": DEX_ADDRESS}


@pytest.mark.parametrize(
    "recorded_url",
    [
        "started server 0.1.0 with 2 tools at http://127.0.0.1:55895/mcp",
        "off, because --bind-address is not a loopback address",
        "http://[bad",
        "",
        None,
    ],
    ids=["log-line", "off", "malformed-url", "empty", "null"],
)
def test_a_recorded_value_that_is_not_a_url_is_dropped(
    tmp_path: Path, recorded_url: str | None
) -> None:
    environment = RuntimeEnvironment(home=tmp_path)
    environment.agents_directory.mkdir(parents=True)
    environment.dev_server_state_path.write_text(
        json.dumps(
            {
                "pid": os.getpid(),
                "dex_address": DEX_ADDRESS,
                "web_url": WEB_URL,
                "connectors_mcp_url": recorded_url,
            }
        )
    )
    state = _read_started_server(environment, WEB_URL, DEX_ADDRESS)
    assert state is not None
    assert state.connectors_mcp_url is None


def test_a_recorded_url_is_kept(tmp_path: Path) -> None:
    environment = RuntimeEnvironment(home=tmp_path)
    environment.agents_directory.mkdir(parents=True)
    environment.dev_server_state_path.write_text(
        json.dumps(
            {
                "pid": os.getpid(),
                "dex_address": DEX_ADDRESS,
                "web_url": WEB_URL,
                "connectors_mcp_url": f"{WEB_URL}/mcp",
            }
        )
    )
    state = _read_started_server(environment, WEB_URL, DEX_ADDRESS)
    assert state is not None
    assert state.connectors_mcp_url == f"{WEB_URL}/mcp"
