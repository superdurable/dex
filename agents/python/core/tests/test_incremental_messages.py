# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A runner that stores items incrementally must be able to finish an unfinished turn."""

from __future__ import annotations

import json

from dex_agents import AgentEventType
from tests.conftest import ScriptedAgentHarness


async def test_new_messages_without_a_builder_fail_the_turn_after_one_model_call(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call-incremental lookup {"query": "q"}')
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert failed.state.last_turn_error == (
        "a runner that sets ToolCallsRequested.new_messages must implement "
        "build_unfinished_turn_messages"
    )
    assert [message.text for message in failed.messages] == [
        'call-incremental lookup {"query": "q"}'
    ]
    assert failed.tool_calls == []
    assert failed.usage.model_calls == 1
    # RunModel failed the turn in its own commit: no retry and no failure route.
    assert harness.invocations.count("model:turn-1:1") == 1
    assert harness.invocations.count("lookup") == 0
    assert await harness.executed_step_types(session_id) == ["StartAgentSession", "RunModel"]
    failures = [
        json.loads(event.payload)
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.TURN_FAILED
    ]
    assert failures == [{"error": failed.state.last_turn_error}]
