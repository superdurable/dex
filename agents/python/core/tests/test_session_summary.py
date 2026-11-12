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

from dex import AsyncClient, FlowStatus

from dex_agents import (
    AGENT_SESSION_LAST_ACTIVITY_AT,
    AGENT_SESSION_TITLE,
    AGENT_SESSION_TURN_COUNT,
    AgentSessionSummary,
    list_agent_sessions,
    sort_agent_sessions_by_last_activity,
)
from dex_agents.testing import wait_until
from tests.conftest import ScriptedAgentHarness


async def test_the_summary_follows_each_turn_and_lists_without_a_worker(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say Fix the\n\tflaky   retry test")
    await harness.wait_for_turns(session_id, 1)
    first = await _wait_for_summary(harness, session_id, turn_count=1)

    assert first.title == "say Fix the flaky retry test"
    assert first.status is FlowStatus.RUNNING
    assert first.last_activity_at is not None
    assert first.last_activity_at >= first.started_at.replace(microsecond=0)
    values = await harness.stack.client.get_attributes(
        session_id, AGENT_SESSION_TITLE, AGENT_SESSION_LAST_ACTIVITY_AT, AGENT_SESSION_TURN_COUNT
    )
    assert values.get(AGENT_SESSION_TITLE) == first.title
    assert values.get(AGENT_SESSION_TURN_COUNT) == 1
    assert values.get(AGENT_SESSION_LAST_ACTIVITY_AT) == first.last_activity_at.isoformat(
        timespec="milliseconds"
    ).replace("+00:00", "Z")

    await _wait_past(first.last_activity_at)
    assert await harness.sessions.send_user_message(session_id, "say a different second message")
    await harness.wait_for_turns(session_id, 2)
    worker.kill()

    second = await _wait_for_summary(harness, session_id, turn_count=2)
    assert second.title == first.title
    assert second.last_activity_at is not None
    assert second.last_activity_at > first.last_activity_at


async def test_sessions_page_sort_by_last_activity_and_search_by_title(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
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
