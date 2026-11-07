# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The RunModel limit bounds one turn segment, and a retry reruns the segment whole."""

from __future__ import annotations

from tests.claude_test_agent import RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE
from tests.conftest import ClaudeAgentHarness


async def test_a_segment_past_the_limit_reruns_whole_and_then_fails_the_turn(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker({RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "4,2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall never-released")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error
    assert harness.gate("never-released").reached_count() == 2
    assert harness.invocations.count("cli:new") == 2
    assert harness.invocations.count("lookup") == 2


async def test_a_segment_within_a_raised_limit_finishes(harness: ClaudeAgentHarness) -> None:
    await harness.stack.start_worker({RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "3600,1"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall turn-gate")
    gate = harness.gate("turn-gate")
    await gate.wait_until_reached()

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "stalled at turn-gate"
    assert gate.reached_count() == 1
