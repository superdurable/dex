# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The session's ask_user built-in works under the Pydantic AI adapter with no adapter change."""

from __future__ import annotations

from dex_agents import InterruptKind
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE


async def test_ask_user_offered_as_an_external_tool_is_answered_and_the_turn_continues(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE: "ask_user"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call ask_user {"question": "Which size?", "choices": ["S", "M"], '
        '"allows_free_text": false}',
    )
    question = await harness.wait_for_pending_interrupt(session_id, "ask_user")
    assert question.kind is InterruptKind.QUESTION
    assert (question.question, question.choices, question.allows_free_text) == (
        "Which size?",
        ["S", "M"],
        False,
    )

    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "XL")
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "M")
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "S")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == 'ask_user: {"status":"answered","answer":"M"}'
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "AwaitQuestionAnswer",
        "ResumeTurn",
        "RunModel",
    ]
