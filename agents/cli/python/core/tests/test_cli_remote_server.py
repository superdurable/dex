# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A command on a remote Dex server that needs a credential, over plaintext and TLS."""

from __future__ import annotations

import asyncio
import io
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pexpect
import pytest

from dex_agents.testing import find_free_ports
from tests.authenticated_dex_server import (
    TEST_SERVER_ID,
    AuthenticatedDexServer,
    authenticated_dex_server,
)
from tests.conftest import CliHome, config_yaml, expect, wait_for_exit
from tests.fake_terminal_ui import FakeTerminalUi, chat_transcript

COMMAND = "dex-agents-scripted"
NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}


@pytest.fixture(scope="module")
def remote_server(tmp_path_factory: pytest.TempPathFactory) -> Iterator[AuthenticatedDexServer]:
    with authenticated_dex_server(tmp_path_factory.mktemp("remote-dex")) as server:
        yield server


@pytest.fixture
def remote_home(tmp_path: Path) -> Iterator[CliHome]:
    """A private home whose commands name a remote server, so no dexcli dev runs."""
    dex_port, web_port = find_free_ports(2)
    home = CliHome(
        home=tmp_path / "home",
        dex_port=dex_port,
        web_port=web_port,
        opened_urls=tmp_path / "opened-urls.txt",
    )
    home.home.mkdir()
    yield home
    home.close()


def api_key_over_tls(server: AuthenticatedDexServer) -> dict[str, Any]:
    return {
        **NO_CONNECTORS,
        "dex": {
            "server": f"https://localhost:{server.proxy.port}",
            "auth": {
                "mode": "api_key",
                "token_url": server.token_endpoint.url,
                "server_id": TEST_SERVER_ID,
            },
            "tls": {"ca_file": str(server.certificates.ca_file)},
        },
    }


def bearer_over_plaintext(server: AuthenticatedDexServer) -> dict[str, Any]:
    return {
        **NO_CONNECTORS,
        "dex": {"server": f"http://{server.address}", "auth": {"mode": "bearer", "source": "env"}},
    }


async def read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


async def test_an_api_key_over_tls_with_a_private_ca_runs_a_turn_and_lists_the_session(
    remote_home: CliHome, remote_server: AuthenticatedDexServer
) -> None:
    remote_home.write_config(config_yaml(api_key_over_tls(remote_server)))
    remote_home.extra_environment["DEX_API_KEY"] = f" {remote_server.api_key}\n"
    exchanges_before = remote_server.token_endpoint.exchange_count
    child = remote_home.spawn("--plain")
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline("hello remote")
    await expect(child, "echo: hello remote")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    listed = await asyncio.to_thread(remote_home.run, "sessions")
    assert listed.returncode == 0, listed.stderr
    assert session_id in listed.stdout
    assert remote_server.token_endpoint.exchange_count > exchanges_before
    assert remote_server.proxy.handshake_count > 0


async def test_a_bearer_token_from_the_environment_lists_sessions(
    remote_home: CliHome, remote_server: AuthenticatedDexServer
) -> None:
    remote_home.write_config(config_yaml(bearer_over_plaintext(remote_server)))
    remote_home.extra_environment["DEX_CREDENTIAL"] = remote_server.access_token
    listed = await asyncio.to_thread(remote_home.run, "sessions")
    assert listed.returncode == 0, listed.stderr
    assert "No sessions yet." in listed.stdout


async def test_a_missing_api_key_stops_the_command_before_any_call(
    remote_home: CliHome, remote_server: AuthenticatedDexServer
) -> None:
    remote_home.write_config(config_yaml(api_key_over_tls(remote_server)))
    remote_home.extra_environment["DEX_API_KEY"] = "  "
    for arguments in (("sessions",), ()):
        completed = await asyncio.to_thread(remote_home.run, *arguments)
        assert completed.returncode == 2
        assert completed.stderr.strip().endswith(
            f"{COMMAND}: DEX_API_KEY is not set; dex.auth mode api_key needs it."
        )


async def test_a_wrong_api_key_names_the_token_exchange(
    remote_home: CliHome, remote_server: AuthenticatedDexServer
) -> None:
    remote_home.write_config(config_yaml(api_key_over_tls(remote_server)))
    wrong_key = "dex_test_key_wrong_1234"
    remote_home.extra_environment["DEX_API_KEY"] = wrong_key
    rejected_before = remote_server.token_endpoint.rejected_count
    for arguments in (("sessions",), ()):
        completed = await asyncio.to_thread(remote_home.run, *arguments)
        assert completed.returncode == 1, completed.stderr
        exchange_line = f"{COMMAND}: could not exchange DEX_API_KEY for an access token: "
        assert exchange_line in completed.stderr
        assert "invalid_api_key" in completed.stderr
        assert completed.stderr.strip().endswith(
            "Check DEX_API_KEY, dex.auth.token_url, and dex.auth.server_id."
        )
        assert wrong_key not in completed.stderr + completed.stdout
    assert remote_server.token_endpoint.rejected_count > rejected_before


async def test_a_wrong_bearer_token_names_the_rejected_credential(
    remote_home: CliHome, remote_server: AuthenticatedDexServer
) -> None:
    remote_home.write_config(config_yaml(bearer_over_plaintext(remote_server)))
    wrong_token = "wrong-bearer-token-5678"
    remote_home.extra_environment["DEX_CREDENTIAL"] = wrong_token
    for arguments in (("sessions",), ()):
        completed = await asyncio.to_thread(remote_home.run, *arguments)
        assert completed.returncode == 1, completed.stderr
        assert (
            f"{COMMAND}: the Dex server at {remote_server.address} rejected the bearer token in "
            "DEX_CREDENTIAL (UNAUTHENTICATED: "
        ) in completed.stderr
        assert completed.stderr.strip().endswith("Check DEX_CREDENTIAL.")
        assert wrong_token not in completed.stderr + completed.stdout


async def test_the_terminal_ui_gets_the_tls_and_auth_settings_and_only_the_api_key(
    remote_home: CliHome, remote_server: AuthenticatedDexServer, tmp_path: Path
) -> None:
    transcript = chat_transcript([])
    launch = transcript["steps"][0]["message"]
    launch["dex"]["tls"] = {
        "enabled": True,
        "ca_file": str(remote_server.certificates.ca_file),
        "server_name": None,
    }
    launch["dex"]["auth"] = {
        "mode": "api_key",
        "token_url": remote_server.token_endpoint.url,
        "server_id": TEST_SERVER_ID,
    }
    fake = FakeTerminalUi(tmp_path / "tui", transcript)
    remote_home.write_config(config_yaml(api_key_over_tls(remote_server)))
    remote_home.extra_environment.update(fake.environment())
    remote_home.extra_environment.update(
        DEX_API_KEY=remote_server.api_key,
        DEX_SERVER_ID=TEST_SERVER_ID,
        DEX_TLS_CA_FILE=str(remote_server.certificates.ca_file),
        OPENAI_API_KEY="sk-test-remote-terminal-ui-sentinel",
    )
    child = remote_home.spawn()
    child.logfile_read = screen = io.StringIO()
    await read_session_id(child)
    assert await wait_for_exit(child) == 0
    assert fake.mismatches() == [], screen.getvalue()
    (start,) = [event for event in fake.events() if event["event"] == "start"]
    names = set(start["environment_names"])
    assert "DEX_API_KEY" in names
    assert not names & {"DEX_SERVER_ID", "DEX_TLS_CA_FILE", "DEX_TOKEN_URL", "OPENAI_API_KEY"}
    assert remote_server.api_key not in fake.record_path.read_text()
