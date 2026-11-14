# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The session lease TTL: 600 seconds, sessions.lease_ttl_seconds, and the environment override."""

from __future__ import annotations

import io
import signal
from datetime import datetime
from pathlib import Path
from typing import Any

from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents import SESSION_TERMINAL_LEASE, SessionTerminalLease
from dex_agents.session_flow import SESSION_TERMINAL_LEASE_MAXIMUM_TTL_SECONDS
from dex_agents_cli_core.config import CONFIG_SCHEMA
from dex_agents_cli_core.environment import DEFAULT_SESSION_LEASE_TTL_SECONDS
from tests.conftest import CliHome, config_yaml, expect, wait_for_exit
from tests.fake_terminal_ui import FakeTerminalUi, chat_transcript
from tests.scripted_harness import SCRIPTED_FLOW_TYPE, build_session_flow_without_tools
from tests.test_session_lease import (
    NO_CONNECTORS,
    PROMPT,
    TTL_ENVIRONMENT_VARIABLE,
    open_first_chat,
    read_session_id,
)
from tests.test_terminal_ui_handoff import pause, start_chat

CONFIGURED_TTL_SECONDS = 45


def with_lease_ttl(seconds: object) -> dict[str, Any]:
    return {**NO_CONNECTORS, "sessions": {"lease_ttl_seconds": seconds}}


async def read_lease(cli_home: CliHome, tmp_path: Path, session_id: str) -> SessionTerminalLease:
    """Return the session's stored terminal lease; the read needs no Worker."""
    flow = build_session_flow_without_tools(SCRIPTED_FLOW_TYPE)
    cache_dir = tmp_path / "reader-cache"
    cache_dir.mkdir(exist_ok=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([flow], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=cli_home.address),
    )
    try:
        values = await client.get_attributes(session_id, SESSION_TERMINAL_LEASE)
    finally:
        await client.close()
        blob_cache.close()
    lease: SessionTerminalLease | None = values.get(SESSION_TERMINAL_LEASE)
    assert lease is not None
    return lease


def lease_seconds(lease: SessionTerminalLease) -> float:
    """Return how long the lease lasts past its first acquire, before any renewal moves it."""
    opened_at = datetime.fromisoformat(lease.opened_at)
    expires_at = datetime.fromisoformat(lease.expires_at)
    return (expires_at - opened_at).total_seconds()


def test_the_config_range_stays_within_what_the_session_accepts() -> None:
    lease_ttl = CONFIG_SCHEMA["properties"]["sessions"]["properties"]["lease_ttl_seconds"]
    assert lease_ttl["default"] == DEFAULT_SESSION_LEASE_TTL_SECONDS == 600
    assert 1 <= lease_ttl["minimum"] <= lease_ttl["maximum"]
    assert lease_ttl["maximum"] == SESSION_TERMINAL_LEASE_MAXIMUM_TTL_SECONDS


async def test_a_session_lease_lasts_600_seconds_by_default(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    first, session_id = await open_first_chat(cli_home)

    lease = await read_lease(cli_home, tmp_path, session_id)
    assert lease_seconds(lease) == DEFAULT_SESSION_LEASE_TTL_SECONDS
    first.sendeof()
    assert await wait_for_exit(first) == 0


async def test_sessions_lease_ttl_seconds_sets_the_lease(cli_home: CliHome, tmp_path: Path) -> None:
    cli_home.write_config(config_yaml(with_lease_ttl(CONFIGURED_TTL_SECONDS)))
    first, session_id = await open_first_chat(cli_home)

    lease = await read_lease(cli_home, tmp_path, session_id)
    # A renewal, every third of the TTL, keeps opened_at and moves expires_at later.
    assert CONFIGURED_TTL_SECONDS <= lease_seconds(lease) < 2 * CONFIGURED_TTL_SECONDS
    first.sendeof()
    assert await wait_for_exit(first) == 0


async def test_the_environment_variable_overrides_sessions_lease_ttl_seconds(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(with_lease_ttl(CONFIGURED_TTL_SECONDS)))
    cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE] = "3600"
    first, session_id = await open_first_chat(cli_home)

    lease = await read_lease(cli_home, tmp_path, session_id)
    assert lease_seconds(lease) == 3600
    first.sendeof()
    assert await wait_for_exit(first) == 0


async def test_an_invalid_environment_variable_leaves_sessions_lease_ttl_seconds(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(config_yaml(with_lease_ttl(CONFIGURED_TTL_SECONDS)))
    cli_home.extra_environment[TTL_ENVIRONMENT_VARIABLE] = "3601"
    first, session_id = await open_first_chat(cli_home)

    lease = await read_lease(cli_home, tmp_path, session_id)
    assert CONFIGURED_TTL_SECONDS <= lease_seconds(lease) < 2 * CONFIGURED_TTL_SECONDS
    first.sendeof()
    assert await wait_for_exit(first) == 0


async def test_the_terminal_ui_worker_host_uses_sessions_lease_ttl_seconds(
    cli_home: CliHome, tmp_path: Path
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(300_000)]))
    chat = start_chat(cli_home, fake, io.StringIO(), with_lease_ttl(CONFIGURED_TTL_SECONDS))
    session_id = await read_session_id(chat)
    await fake.wait_for("sent", type="ready")

    lease = await read_lease(cli_home, tmp_path, session_id)
    assert CONFIGURED_TTL_SECONDS <= lease_seconds(lease) < 2 * CONFIGURED_TTL_SECONDS
    chat.kill(signal.SIGTERM)
    assert await wait_for_exit(chat) == 143


async def test_a_lease_ttl_outside_the_range_stops_the_command_with_the_field(
    cli_home: CliHome,
) -> None:
    for seconds, keyword in ((9, "minimum"), (3601, "maximum"), ("600", "type")):
        cli_home.write_config(config_yaml(with_lease_ttl(seconds)))
        completed = cli_home.run()
        assert completed.returncode == 2, completed.stderr
        assert (
            f'schema at "/sessions/lease_ttl_seconds" (schema keyword {keyword})'
        ) in completed.stderr


async def test_a_clean_exit_still_hands_a_600_second_lease_over_at_once(
    cli_home: CliHome,
) -> None:
    cli_home.write_config(config_yaml(NO_CONNECTORS))
    first, session_id = await open_first_chat(cli_home)
    second = cli_home.spawn("--plain", "--resume", session_id)
    await expect(second, "is open in another terminal (")

    first.sendline("/exit")
    assert await wait_for_exit(first) == 0
    # The waiting command asks every second, however long the lease lasts.
    await expect(second, f"Took over session {session_id}.", timeout=10)
    await expect(second, PROMPT)
    second.sendeof()
    assert await wait_for_exit(second) == 0
