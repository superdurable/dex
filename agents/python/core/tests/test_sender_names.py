# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Sender display names on user messages, on a real Dex Server."""

from __future__ import annotations

import json
from collections.abc import Sequence

from dex_agents import (
    AgentMessage,
    AgentUserMessage,
    MessageRole,
    SteerUserMessageRequest,
    is_valid_sender_name,
)
from tests.conftest import ScriptedAgentHarness

REFUSED_SENDER_NAMES = ["", " Alice", "Alice ", "A\nB", "A\u2028B", "\u0007", "x" * 65]


def test_sender_name_rule_counts_code_points_and_refuses_breaks() -> None:
    assert is_valid_sender_name("Alice")
    assert is_valid_sender_name("\U0001f600" * 64)
    assert is_valid_sender_name("\ufeffAlice")
    assert not is_valid_sender_name("\U0001f600" * 65)
    for name in REFUSED_SENDER_NAMES:
        assert not is_valid_sender_name(name), name


async def test_a_named_message_reaches_the_model_with_its_name_and_is_stored_with_it(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hi")
    await harness.wait_for_turns(session_id, 1)
    client, flow = harness.stack.client, harness.flow

    for name in REFUSED_SENDER_NAMES:
        refused = AgentUserMessage(text="hello", sender_name=name)
        assert not await client.invoke_rpc(flow.send_user_message, session_id, refused), name
    assert not await client.invoke_rpc(
        flow.steer_user_message,
        session_id,
        SteerUserMessageRequest(message_id="msg-1", sender_name="Alice"),
    )

    named = AgentUserMessage(text="hello there", sender_name="Alice")
    assert await client.invoke_rpc(flow.send_user_message, session_id, named)
    snapshot = await harness.wait_for_turns(session_id, 2)
    user = _user_message(snapshot.messages, "turn-2")
    assert (user.text, user.sender_name) == ("hello there", "Alice")
    assert json.loads(user.framework_item)["text"] == "Alice: hello there"
    assert snapshot.messages[-1].text == "heard Alice: hello there"
    assert (snapshot.messages[0].text, snapshot.messages[0].sender_name) == ("say hi", None)

    # Steering while idle queues the text with its name.
    steered = SteerUserMessageRequest(text="what now", sender_name="Bob")
    assert await client.invoke_rpc(flow.steer_user_message, session_id, steered)
    snapshot = await harness.wait_for_turns(session_id, 3)
    assert snapshot.messages[-1].text == "heard Bob: what now"
    assert _user_message(snapshot.messages, "turn-3").sender_name == "Bob"


def _user_message(messages: Sequence[AgentMessage], turn_id: str) -> AgentMessage:
    return next(m for m in messages if m.turn_id == turn_id and m.role is MessageRole.USER)
