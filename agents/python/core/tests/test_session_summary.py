# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Sessions list with their title, last activity time, and turn count, without a Worker."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from dex import AsyncClient, FlowStatus

from dex_agents import (
    AgentSessionSummary,
    SessionTitleSearchUnavailableError,
    list_agent_sessions,
    sort_agent_sessions_by_last_activity,
)
from dex_agents.testing import wait_until
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import SUMMARY_INDEXED_ENVIRONMENT_VARIABLE

SUMMARY_INDEXED = {SUMMARY_INDEXED_ENVIRONMENT_VARIABLE: "1"}


async def test_the_summary_follows_each_turn_and_lists_without_a_worker(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker(SUMMARY_INDEXED)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say Fix the\n\tflaky   retry test")
    await harness.wait_for_turns(session_id, 1)
    first = await _wait_for_summary(harness, session_id, turn_count=1)

    assert first.title == "say Fix the flaky retry test"
    assert first.status is FlowStatus.RUNNING
    assert first.last_activity_at is not None
    assert first.last_activity_at >= first.started_at.replace(microsecond=0)
    entry = (await harness.stack.client.search_flows(f'WorkflowId = "{session_id}"', 1)).flows[0]
    stored_time = entry.indexed_attributes["datetime1"].data
    assert isinstance(stored_time, str)
    # The index keeps the time; the Attribute stores it as RFC 3339 with milliseconds.
    assert datetime.fromisoformat(stored_time) == first.last_activity_at

    await _wait_past(first.last_activity_at)
    assert await harness.sessions.send_user_message(session_id, "say a different second message")
    await harness.wait_for_turns(session_id, 2)
    worker.kill()

    second = await _wait_for_summary(harness, session_id, turn_count=2)
    assert second.title == first.title
    assert second.last_activity_at is not None
    assert second.last_activity_at > first.last_activity_at

    # A session without a first message has a time and zero turns, but no title.
    await harness.stack.start_worker(SUMMARY_INDEXED)
    idle_id = harness.new_session_id()
    await harness.sessions.start_session(idle_id)
    idle = await _wait_for_summary(harness, idle_id, turn_count=0)
    assert idle.title is None
    assert idle.last_activity_at is not None


async def test_sessions_page_sort_by_last_activity_and_search_by_title(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker(SUMMARY_INDEXED)
    flow_type = harness.flow.get_flow_type()
    refund_order = harness.new_session_id()
    flaky_test = harness.new_session_id()
    refund_invoice = harness.new_session_id()
    for session_id, message in (
        (refund_order, "say Refund order 7"),
        (flaky_test, "say Fix the flaky test"),
        (refund_invoice, 'say refund the "duplicate" invoice'),
    ):
        await harness.sessions.start_session(session_id, message)
    for session_id in (refund_order, flaky_test, refund_invoice):
        await harness.wait_for_turns(session_id, 1)
        await _wait_for_summary(harness, session_id, turn_count=1)

    loaded = await _list_every_session(harness.stack.client, flow_type, page_size=2)
    assert [len(page) for page in loaded] == [2, 1]
    sessions = [session for page in loaded for session in page]
    assert sorted(session.session_id for session in sessions) == sorted(
        (refund_order, flaky_test, refund_invoice)
    )

    # The oldest session becomes the most recently active one.
    latest = max(session.last_activity_at for session in sessions if session.last_activity_at)
    await _wait_past(latest)
    assert await harness.sessions.send_user_message(refund_order, "say once more")
    await harness.wait_for_turns(refund_order, 2)
    await _wait_for_summary(harness, refund_order, turn_count=2)
    page = await list_agent_sessions(harness.stack.client, flow_type)
    ordered = sort_agent_sessions_by_last_activity(page.sessions)
    assert ordered[0].session_id == refund_order
    assert [session.last_activity_at for session in ordered] == sorted(
        (session.last_activity_at for session in ordered),
        key=lambda moment: moment or datetime.min.replace(tzinfo=UTC),
        reverse=True,
    )

    assert await _search(harness, flow_type, "refund") == sorted((refund_order, refund_invoice))
    assert await _search(harness, flow_type, "REFUND order") == [refund_order]
    assert await _search(harness, flow_type, 'duplicate" \\') == [refund_invoice]
    assert await _search(harness, flow_type, "flak") == []
    assert await _search(harness, flow_type, "  ") == sorted(
        (refund_order, flaky_test, refund_invoice)
    )
    titled = await _list_every_session(
        harness.stack.client, flow_type, page_size=1, title_search="refund"
    )
    assert [len(page) for page in titled] == [1, 1]


async def test_without_the_option_sessions_list_without_a_summary(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say refund order 7")
    await harness.wait_for_turns(session_id, 1)

    page = await list_agent_sessions(harness.stack.client, harness.flow.get_flow_type())
    [session] = page.sessions
    assert session.session_id == session_id
    assert (session.title, session.last_activity_at, session.turn_count) == (None, None, None)
    entry = (await harness.stack.client.search_flows(f'WorkflowId = "{session_id}"', 1)).flows[0]
    assert {"text1", "datetime1", "int1"}.isdisjoint(entry.indexed_attributes)
    # No Worker registered the title index on this server.
    with pytest.raises(SessionTitleSearchUnavailableError):
        await list_agent_sessions(
            harness.stack.client, harness.flow.get_flow_type(), title_search="refund"
        )


async def _wait_for_summary(
    harness: ScriptedAgentHarness, session_id: str, *, turn_count: int
) -> AgentSessionSummary:
    async def probe() -> AgentSessionSummary | None:
        page = await list_agent_sessions(harness.stack.client, harness.flow.get_flow_type())
        return next(
            (
                session
                for session in page.sessions
                if session.session_id == session_id and session.turn_count == turn_count
            ),
            None,
        )

    return await wait_until(f"{session_id} to list with {turn_count} turn(s)", probe)


async def _wait_past(moment: datetime) -> None:
    """Wait until the clock is in a later second, since activity times have one-second precision."""

    async def probe() -> bool:
        return datetime.now(UTC) >= moment + timedelta(seconds=1)

    await wait_until(f"the clock to pass {moment}", probe)


async def _list_every_session(
    client: AsyncClient, flow_type: str, *, page_size: int, title_search: str | None = None
) -> list[list[AgentSessionSummary]]:
    pages: list[list[AgentSessionSummary]] = []
    next_page_token = ""
    while True:
        page = await list_agent_sessions(
            client,
            flow_type,
            page_size=page_size,
            next_page_token=next_page_token,
            title_search=title_search,
        )
        pages.append(list(page.sessions))
        next_page_token = page.next_page_token
        if not next_page_token:
            return pages


async def _search(harness: ScriptedAgentHarness, flow_type: str, title_search: str) -> list[str]:
    page = await list_agent_sessions(
        harness.stack.client, flow_type, title_search=title_search, page_size=100
    )
    return sorted(session.session_id for session in page.sessions)
