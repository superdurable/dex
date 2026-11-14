# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The session terminal lease: acquire, renew, refuse, expire, and release."""

from __future__ import annotations

import asyncio
from dataclasses import replace
from datetime import datetime

import pytest

from dex_agents import (
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    SESSION_TERMINAL_LEASE,
    AcquireSessionTerminalLeaseRequest,
)
from tests.conftest import ScriptedAgentHarness


def _request(holder_id: str, ttl_seconds: int = 15) -> AcquireSessionTerminalLeaseRequest:
    return AcquireSessionTerminalLeaseRequest(
        holder_id=holder_id, host_name=f"host-{holder_id}", process_id=4242, ttl_seconds=ttl_seconds
    )


async def _started_session(harness: ScriptedAgentHarness) -> str:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    return session_id


async def test_one_holder_gets_the_lease_and_another_is_told_who_has_it(
    harness: ScriptedAgentHarness,
) -> None:
    session_id = await _started_session(harness)

    first = await harness.sessions.acquire_session_terminal_lease(session_id, _request("a"))
    refused = await harness.sessions.acquire_session_terminal_lease(session_id, _request("b"))

    assert first.is_acquired
    assert first.lease is not None
    assert (first.lease.holder_id, first.lease.host_name, first.lease.process_id) == (
        "a",
        "host-a",
        4242,
    )
    opened_at = datetime.fromisoformat(first.lease.opened_at)
    expires_at = datetime.fromisoformat(first.lease.expires_at)
    assert (expires_at - opened_at).total_seconds() == 15
    assert not refused.is_acquired
    assert refused.lease == first.lease
    stored = await harness.stack.client.get_attributes(session_id, SESSION_TERMINAL_LEASE)
    assert stored.get(SESSION_TERMINAL_LEASE) == first.lease


async def test_a_renewal_keeps_the_open_time_and_moves_the_expiry(
    harness: ScriptedAgentHarness,
) -> None:
    session_id = await _started_session(harness)
    first = await harness.sessions.acquire_session_terminal_lease(session_id, _request("a"))
    await asyncio.sleep(0.05)

    renewed = await harness.sessions.acquire_session_terminal_lease(session_id, _request("a"))

    assert first.lease is not None and renewed.lease is not None
    assert renewed.is_acquired
    assert renewed.lease.opened_at == first.lease.opened_at
    assert renewed.lease.expires_at > first.lease.expires_at


async def test_an_expired_lease_goes_to_the_next_holder(harness: ScriptedAgentHarness) -> None:
    session_id = await _started_session(harness)
    await harness.sessions.acquire_session_terminal_lease(session_id, _request("a", ttl_seconds=1))
    await asyncio.sleep(1.2)

    taken = await harness.sessions.acquire_session_terminal_lease(session_id, _request("b"))
    renewal_of_the_first = await harness.sessions.acquire_session_terminal_lease(
        session_id, _request("a")
    )

    assert taken.is_acquired
    assert taken.lease is not None and taken.lease.holder_id == "b"
    assert not renewal_of_the_first.is_acquired
    assert renewal_of_the_first.lease == taken.lease


async def test_release_frees_the_lease_only_for_its_holder(harness: ScriptedAgentHarness) -> None:
    session_id = await _started_session(harness)
    await harness.sessions.acquire_session_terminal_lease(session_id, _request("a"))

    assert not await harness.sessions.release_session_terminal_lease(session_id, "b")
    assert await harness.sessions.release_session_terminal_lease(session_id, "a")
    assert not await harness.sessions.release_session_terminal_lease(session_id, "a")
    stored = await harness.stack.client.get_attributes(session_id, SESSION_TERMINAL_LEASE)
    assert stored.get(SESSION_TERMINAL_LEASE) is None
    assert (
        await harness.sessions.acquire_session_terminal_lease(session_id, _request("b"))
    ).is_acquired


async def test_a_new_holders_environment_stays_as_the_last_terminal_environment(
    harness: ScriptedAgentHarness,
) -> None:
    session_id = await _started_session(harness)
    sessions = harness.sessions
    environment_a = {"host_name": "host-a", "workspace": "/work/a"}
    environment_b = {"host_name": "host-b", "workspace": "/work/b"}

    async def last_environment() -> dict[str, str] | None:
        stored = await harness.stack.client.get_attributes(
            session_id, SESSION_LAST_TERMINAL_ENVIRONMENT
        )
        return stored.get(SESSION_LAST_TERMINAL_ENVIRONMENT)

    await sessions.acquire_session_terminal_lease(session_id, _request("a"))
    assert await last_environment() is None
    await sessions.release_session_terminal_lease(session_id, "a")

    await sessions.acquire_session_terminal_lease(
        session_id, replace(_request("a"), environment=environment_a)
    )
    assert await last_environment() == environment_a
    await sessions.acquire_session_terminal_lease(
        session_id, replace(_request("a"), environment=environment_b)
    )
    assert await last_environment() == environment_a, "a renewal keeps the stored environment"
    refused = await sessions.acquire_session_terminal_lease(
        session_id, replace(_request("b"), environment=environment_b)
    )
    assert not refused.is_acquired
    assert await last_environment() == environment_a, "a refused call keeps it"

    await sessions.release_session_terminal_lease(session_id, "a")
    assert await last_environment() == environment_a, "a release keeps it"
    await sessions.acquire_session_terminal_lease(
        session_id, replace(_request("b"), environment=environment_b)
    )
    assert await last_environment() == environment_b


@pytest.mark.parametrize(
    "request_",
    [
        AcquireSessionTerminalLeaseRequest(
            holder_id=" ", host_name="h", process_id=1, ttl_seconds=15
        ),
        AcquireSessionTerminalLeaseRequest(
            holder_id="a", host_name="h" * 256, process_id=1, ttl_seconds=15
        ),
        AcquireSessionTerminalLeaseRequest(
            holder_id="a", host_name="h", process_id=-1, ttl_seconds=15
        ),
        AcquireSessionTerminalLeaseRequest(
            holder_id="a", host_name="h", process_id=1, ttl_seconds=0
        ),
        AcquireSessionTerminalLeaseRequest(
            holder_id="a", host_name="h", process_id=1, ttl_seconds=3601
        ),
    ],
)
async def test_an_invalid_request_changes_nothing(
    harness: ScriptedAgentHarness, request_: AcquireSessionTerminalLeaseRequest
) -> None:
    session_id = await _started_session(harness)

    result = await harness.sessions.acquire_session_terminal_lease(session_id, request_)

    assert not result.is_acquired
    assert result.lease is None
    stored = await harness.stack.client.get_attributes(session_id, SESSION_TERMINAL_LEASE)
    assert stored.get(SESSION_TERMINAL_LEASE) is None
