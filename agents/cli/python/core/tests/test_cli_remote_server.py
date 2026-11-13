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
import logging
from collections.abc import Iterator
from pathlib import Path
from typing import Any, cast

import pexpect
import pytest
from dex import AsyncClient

from dex_agents.testing import find_free_ports
from dex_agents_cli_core.cli import _upload_session_flow_definition
from dex_agents_cli_core.dex_connection import (
    DexConnectionSettings,
    DexRefusalDetailsFilter,
    describe_rejected_dex_credential,
)
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
    for arguments in (("sessions",), ("connectors",), ()):
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
        assert completed.stderr.strip().endswith(
            f"{COMMAND}: could not exchange DEX_API_KEY for an access token at "
            f"{remote_server.token_endpoint.url} (HTTP status 401). Check DEX_API_KEY, "
            "dex.auth.token_url, and dex.auth.server_id."
        )
        assert "invalid_api_key" not in completed.stderr
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
        assert completed.stderr.strip().endswith(
            f"{COMMAND}: the Dex server at {remote_server.address} rejected the bearer token in "
            "DEX_CREDENTIAL (UNAUTHENTICATED). Check DEX_CREDENTIAL."
        )
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


SHELL_ENVIRONMENT_COMMAND = "env > shell-environment.txt"
SECRET_SENTINELS = {
    "DEX_API_KEY": "dex-api-key-sentinel-1",
    "DEX_CREDENTIAL": "dex-credential-sentinel-2",
    "DEX_CREDENTIAL_FILE": "/run/dex-credential-file-sentinel-3",
}


async def run_shell_environment_command(home: CliHome, config: dict[str, Any]) -> str:
    """Run the shell tool once in the plain chat and return the environment it saw."""
    home.write_config(
        config_yaml({**config, "local_tools": {"shell": {"allow": [SHELL_ENVIRONMENT_COMMAND]}}})
    )
    child = home.spawn("--plain")
    await read_session_id(child)
    await expect(child, "> ")
    child.sendline(f'call run_shell_command {{"command": "{SHELL_ENVIRONMENT_COMMAND}"}}')
    await expect(child, "done: run_shell_command=exit code 0")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    return (home.home / "dex-agent-workspace" / "shell-environment.txt").read_text()


def assert_no_dex_secret(shell_environment: str) -> None:
    for name, value in SECRET_SENTINELS.items():
        assert f"{name}=" not in shell_environment, name
        assert value not in shell_environment, name


async def test_a_shell_command_never_sees_exported_dex_credentials_without_dex_auth(
    cli_home: CliHome,
) -> None:
    cli_home.extra_environment.update(SECRET_SENTINELS)
    assert_no_dex_secret(await run_shell_environment_command(cli_home, NO_CONNECTORS))


async def test_a_shell_command_never_sees_the_credential_the_command_uses(
    remote_home: CliHome, remote_server: AuthenticatedDexServer
) -> None:
    remote_home.extra_environment.update(SECRET_SENTINELS)
    remote_home.extra_environment["DEX_CREDENTIAL"] = remote_server.access_token
    shell_environment = await run_shell_environment_command(
        remote_home, bearer_over_plaintext(remote_server)
    )
    assert_no_dex_secret(shell_environment)
    assert remote_server.access_token not in shell_environment


class _EchoingStatusError(Exception):
    """A gRPC error, by shape, whose details echo the credential back."""

    def __init__(self, name: str, details: str) -> None:
        super().__init__(details)
        self._name = name
        self._details = details

    def code(self) -> Any:
        return type("StatusCode", (), {"name": self._name})()

    def details(self) -> str:
        return self._details


@pytest.mark.parametrize("status", ["UNAUTHENTICATED", "PERMISSION_DENIED"])
def test_a_refusal_never_shows_the_server_details(status: str) -> None:
    secret = "bearer-token-echoed-by-a-proxy-9876"
    settings = DexConnectionSettings.from_json(
        {
            "tls": {"enabled": True, "ca_file": None, "server_name": None},
            "auth": {"mode": "bearer", "source": "env"},
        }
    )
    failure = RuntimeError("cannot start the Worker")
    failure.__cause__ = _EchoingStatusError(status, f"token {secret} is not valid")
    line = describe_rejected_dex_credential(failure, settings, "dex.example.com:443")
    assert line is not None
    assert secret not in line
    assert f"({status})" in line


class _RefusingUploadClient:
    """A Client, by shape, whose upload a proxy refuses with the credential in the details."""

    def __init__(self, status: str, secret: str) -> None:
        self._status = status
        self._secret = secret

    async def upload_flow_definitions(self, documents: Any) -> Any:
        failure = RuntimeError(f"upload failed: {self._secret}")
        raise failure from _EchoingStatusError(self._status, f"token {self._secret} is bad")


@pytest.mark.parametrize("status", ["UNAUTHENTICATED", "PERMISSION_DENIED"])
async def test_a_refused_session_graph_upload_logs_no_server_details(
    status: str, caplog: pytest.LogCaptureFixture
) -> None:
    secret = "bearer-token-echoed-in-upload-4321"
    with caplog.at_level(logging.WARNING):
        await _upload_session_flow_definition(
            cast(AsyncClient, _RefusingUploadClient(status, secret)), "DexAgentsTestSession-abc123"
        )
    (record,) = [record for record in caplog.records if "upload failed" in record.getMessage()]
    assert secret not in record.getMessage()
    assert f"({status})" in record.getMessage()


@pytest.mark.parametrize("status", ["UNAUTHENTICATED", "PERMISSION_DENIED"])
def test_the_log_filter_drops_refusal_details_that_the_sdk_logs(status: str) -> None:
    secret = "bearer-token-echoed-in-sdk-log-2468"
    stream = io.StringIO()
    handler = logging.StreamHandler(stream)
    handler.addFilter(DexRefusalDetailsFilter())
    logger = logging.getLogger(f"dex.test_refusal_filter.{status}")
    logger.addHandler(handler)
    logger.propagate = False
    try:
        failure = RuntimeError(f"upload failed: {secret}")
        failure.__cause__ = _EchoingStatusError(status, f"token {secret} is bad")
        logger.warning("Flow definition upload failed; the Worker continues starting: %s", failure)
        try:
            raise failure
        except RuntimeError:
            logger.exception("pull stream failed")
    finally:
        logger.removeHandler(handler)
    text = stream.getvalue()
    assert secret not in text
    assert text.count(f"({status})") == 2
