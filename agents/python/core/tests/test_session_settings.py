# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Per-session settings: start, update at the next turn, the resolver, the limit, and crashes."""

from __future__ import annotations

from datetime import timedelta

import pytest
from dex import FlowStatus

from dex_agents import (
    INVALID_START_SESSION_SETTINGS_REASON,
    MAX_SESSION_SETTINGS_BYTES,
    SESSION_SETTINGS,
    SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
    TURN_SESSION_SETTINGS,
    AgentSessionStartInput,
    TurnSessionSettings,
    UpdateSessionSettingsRequest,
    session_settings_size,
    validate_session_settings,
)
from tests.conftest import ScriptedAgentHarness
from tests.stepwise_agent import INSTRUCTIONS


async def test_settings_from_the_start_input_choose_the_model_and_the_instructions(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "which-model", session_settings={"model": "alpha", "persona": "pirate"}
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"model alpha: {INSTRUCTIONS} Persona: pirate."
    assert snapshot.session_settings == {"model": "alpha", "persona": "pirate"}
    assert harness.invocations.count("model-name:turn-1:1:alpha") == 1
    values = await harness.stack.client.get_attributes(
        session_id, SESSION_SETTINGS, TURN_SESSION_SETTINGS
    )
    assert values.get(SESSION_SETTINGS) == {"model": "alpha", "persona": "pirate"}
    assert values.get(TURN_SESSION_SETTINGS) == TurnSessionSettings(
        turn_id="turn-1", session_settings={"model": "alpha", "persona": "pirate"}
    )


async def test_a_session_without_settings_uses_the_default_model(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "which-model")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"model default: {INSTRUCTIONS}"
    assert snapshot.session_settings is None


async def test_an_update_between_turns_applies_to_the_next_turn(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "which-model", session_settings={"model": "alpha"}
    )
    await harness.wait_for_turns(session_id, 1)

    assert await harness.sessions.update_session_settings(session_id, {"model": "beta"})
    assert (await harness.sessions.get_agent_snapshot(session_id)).session_settings == {
        "model": "beta"
    }
    assert await harness.sessions.send_user_message(session_id, "which-model")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == f"model beta: {INSTRUCTIONS}"

    # An empty object clears the settings.
    assert await harness.sessions.update_session_settings(session_id, {})
    assert await harness.sessions.send_user_message(session_id, "which-model")
    third = await harness.wait_for_turns(session_id, 3)
    assert third.messages[-1].text == f"model default: {INSTRUCTIONS}"
    assert third.session_settings is None


async def test_a_running_turn_keeps_the_settings_it_started_with(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "lookup-gated lookup-gate order-7", session_settings={"model": "alpha"}
    )
    gate = harness.gate("lookup-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.update_session_settings(session_id, {"model": "beta"})
    gate.release()
    first = await harness.wait_for_turns(session_id, 1)
    assert first.messages[-1].text == "lookup_order: found order-7"
    assert harness.invocations.count("model-name:turn-1:1:alpha") == 1
    assert harness.invocations.count("model-name:turn-1:2:alpha") == 1
    assert not [name for name in harness.invocations.names() if name.endswith(":beta")]

    assert await harness.sessions.send_user_message(session_id, "which-model")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == f"model beta: {INSTRUCTIONS}"


async def test_a_worker_killed_after_an_update_retries_the_model_call_with_the_turn_settings(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "lookup-then-stall model-gate order-7", session_settings={"model": "alpha"}
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.update_session_settings(session_id, {"model": "beta"})

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    first = await harness.wait_for_turns(session_id, 1)

    assert first.messages[-1].text == "lookup_order: found order-7"
    assert harness.invocations.count("model-name:turn-1:2:alpha") == 2
    assert first.session_settings == {"model": "beta"}
    assert await harness.sessions.send_user_message(session_id, "which-model")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == f"model beta: {INSTRUCTIONS}"


async def test_settings_over_the_limit_are_refused_before_and_inside_the_session(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    # {"note":"<padding>"} is 11 bytes plus the padding.
    largest = {"note": "x" * (MAX_SESSION_SETTINGS_BYTES - 11)}
    too_large = {"note": "x" * (MAX_SESSION_SETTINGS_BYTES - 10)}
    assert session_settings_size(largest) == MAX_SESSION_SETTINGS_BYTES
    validate_session_settings(largest)
    with pytest.raises(ValueError, match="more than the 16384 allowed"):
        validate_session_settings(too_large)
    # Non-ASCII counts as UTF-8 bytes, not characters.
    assert session_settings_size({"note": "é"}) == 13

    session_id = harness.new_session_id()
    with pytest.raises(ValueError):
        await harness.sessions.start_session(session_id, session_settings=too_large)
    with pytest.raises(ValueError):
        await harness.sessions.start_session(session_id, session_settings=[1])  # type: ignore[arg-type]
    await harness.sessions.start_session(session_id, session_settings=largest)
    await harness.wait_for_turns(session_id, 0)

    with pytest.raises(ValueError):
        await harness.sessions.update_session_settings(session_id, too_large)
    assert (await harness.sessions.get_agent_snapshot(session_id)).session_settings == largest
    # The session accepts a tolerance over the Client's limit, never more.
    within_tolerance = {"note": "x" * (MAX_SESSION_SETTINGS_BYTES + 100)}
    beyond_tolerance = {
        "note": "x" * (MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES)
    }
    assert await harness.stack.client.invoke_rpc(
        harness.flow.update_session_settings,
        session_id,
        UpdateSessionSettingsRequest(session_settings=within_tolerance),
    )
    assert not await harness.stack.client.invoke_rpc(
        harness.flow.update_session_settings,
        session_id,
        UpdateSessionSettingsRequest(session_settings=beyond_tolerance),
    )


async def test_an_update_without_settings_returns_false_and_changes_nothing(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hi", session_settings={"model": "alpha"})
    await harness.wait_for_turns(session_id, 1)

    # A missing or null field decodes as None.
    assert not await harness.stack.client.invoke_rpc(
        harness.flow.update_session_settings, session_id, UpdateSessionSettingsRequest()
    )
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    assert snapshot.session_settings == {"model": "alpha"}


async def test_a_start_whose_settings_the_session_refuses_fails_the_session_at_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    oversized = {"note": "x" * (MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES)}
    # The raw Client skips AgentSessionClient's check.
    await harness.stack.client.start_flow(
        harness.flow, session_id, AgentSessionStartInput(session_settings=oversized)
    )

    result = await harness.stack.client.wait_for_flow(session_id, timedelta(seconds=60))
    assert result.status is FlowStatus.FAILED
    assert INVALID_START_SESSION_SETTINGS_REASON in (result.error_message or "")
    history = await harness.stack.read_step_history(session_id)
    assert {entry.final_attempt for entry in history if entry.step_type == "StartAgentSession"} == {
        1
    }
