# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A session at ``limits.maxFlowStateBytes`` ends failed, with the reason, on a real engine."""

from __future__ import annotations

import asyncio
import json
import re
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path
from uuid import uuid4

import pytest
from dex import DexServiceError, FlowStatus

from dex_agents import (
    AgentEventType,
    AgentSessionClient,
    AgentSessionFailedError,
    AgentSessionFlow,
    AgentSessionStatus,
    AgentSnapshot,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    InvocationLog,
    wait_for_agent_snapshot,
)
from tests.conftest import CORE_DIR
from tests.scripted_agent import INVOCATION_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.state_limit_agent import build_state_limit_agent_flow

WORKER_MODULE = "tests.state_limit_worker"
STATE_LIMIT_BYTES = 131_072
# A stored answer costs about twice its characters: two of these leave room for a large write.
LARGE_ANSWER = "pad 20000"
NO_ROOM_SESSIONS = 24
NO_ROOM_SMALL_TURNS = 40
STATE_LIMIT_REASON = re.compile(
    rf"^run state of \d+ bytes exceeds the maximum of {STATE_LIMIT_BYTES} bytes$"
)


@dataclass
class StateLimitHarness:
    stack: AgentTestStack
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

    def model_calls(self) -> list[str]:
        names = InvocationLog(self.state_dir / INVOCATION_LOG_NAME).names()
        return [name for name in names if name.startswith("model:")]

    def tool_runs(self, tool_name: str) -> int:
        return InvocationLog(self.state_dir / INVOCATION_LOG_NAME).count(tool_name)

    async def stored_snapshot(self, session_id: str) -> AgentSnapshot:
        snapshot: AgentSnapshot = await self.stack.client.invoke_rpc(
            self.flow.get_agent_snapshot, session_id
        )
        return snapshot

    async def fill(self, session_id: str, turns: int) -> None:
        for turn in range(1, turns + 1):
            assert await self.sessions.send_user_message(session_id, LARGE_ANSWER)
            await wait_for_agent_snapshot(
                self.sessions,
                session_id,
                lambda snapshot, turn=turn: snapshot  # type: ignore[misc]
                if snapshot.state is not None
                and snapshot.state.turn_count >= turn
                and snapshot.state.active_turn_id is None
                else None,
                description=f"turn {turn} to finish",
            )

    async def close_reason(self, session_id: str) -> str:
        result = await self.stack.client.wait_for_flow(session_id)
        assert result.status is FlowStatus.FAILED
        assert result.error_message is not None
        assert STATE_LIMIT_REASON.match(result.error_message)
        return result.error_message

    async def executed_step_types(self, session_id: str) -> list[str]:
        history = await self.stack.read_step_history(session_id)
        return [entry.step_type for entry in history if entry.event_type == EXECUTE_COMPLETED_EVENT]

    async def assert_new_messages_are_rejected(self, session_id: str, reason: str) -> None:
        with pytest.raises(AgentSessionFailedError) as sent:
            await self.sessions.send_user_message(session_id, "say hello")
        assert sent.value.reason == reason
        assert reason in str(sent.value)
        with pytest.raises(AgentSessionFailedError):
            await self.sessions.steer_user_message(session_id, text="say hello")


@asynccontextmanager
async def _state_limit_harness(
    tmp_path: Path, *, state_limit: int = STATE_LIMIT_BYTES, value_limit: int = STATE_LIMIT_BYTES
) -> AsyncIterator[StateLimitHarness]:
    config_path = tmp_path / "server-zd.yaml"
    config_path.write_text(
        f"values:\n  maxValueBytes: {value_limit}\n"
        f"limits:\n  maxFlowStateBytes: {state_limit}\n"
        "dev:\n  durabilityMode: processCrashSafe\n"
    )
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_state_limit_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
        server_zd_config=config_path,
    ) as stack:
        await stack.start_worker()
        yield StateLimitHarness(stack, flow, stack.sessions(flow), state_dir)


async def test_a_model_answer_over_the_limit_fails_the_session_and_keeps_queued_messages(
    tmp_path: Path,
) -> None:
    async with _state_limit_harness(tmp_path) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id)
        # Two answers fit; the third crosses the limit by far more than the failure's own commit.
        queued = [f"pad {30_000 + offset}" for offset in range(8)]
        for text in queued:
            assert await harness.sessions.send_user_message(session_id, text)

        reason = await harness.close_reason(session_id)

        # The commit that stores the failure closes the Flow, so the stored state says it all.
        stored = await harness.stored_snapshot(session_id)
        assert stored.state is not None
        assert stored.state.status is AgentSessionStatus.FAILED
        assert stored.state.last_turn_error == reason
        assert stored.state.active_turn_id is None
        assert await harness.sessions.get_agent_snapshot(session_id) == stored

        # Only one paid model call was lost; the session did not go on to the queued messages.
        assert len(harness.model_calls()) == stored.state.turn_count
        waiting = [pending.message.text for pending in stored.queued_messages]
        assert waiting == queued[stored.state.turn_count :]
        assert waiting and stored.queued_message_count == len(waiting)

        await harness.assert_new_messages_are_rejected(session_id, reason)
        page = await harness.stack.client.list_stream_messages(
            session_id, harness.flow.agent_events, 10
        )
        failures = [
            json.loads(message.value.payload)
            for message in page.messages
            if message.value.type == AgentEventType.TURN_FAILED
        ]
        assert failures == [{"error": reason}]


@pytest.mark.parametrize(
    ("tool_name", "recovery_step"),
    [("bulk", "RecordToolFailure"), ("bulk_write", "RecordToolOutcomeUnknown")],
)
async def test_a_tool_result_over_the_limit_fails_the_session(
    tmp_path: Path, tool_name: str, recovery_step: str
) -> None:
    async with _state_limit_harness(tmp_path) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id)
        await harness.fill(session_id, turns=2)
        model_calls_before = len(harness.model_calls())
        arguments = json.dumps({"characters": 70_000})
        assert await harness.sessions.send_user_message(session_id, f"call {tool_name} {arguments}")
        assert await harness.sessions.send_user_message(session_id, "say after the tool")

        reason = await harness.close_reason(session_id)

        assert recovery_step in await harness.executed_step_types(session_id)
        assert harness.tool_runs(tool_name) == 1
        # The model never saw a tool failure, so it cannot decide to call the tool again.
        assert len(harness.model_calls()) == model_calls_before + 1
        stored = await harness.stored_snapshot(session_id)
        assert stored.state is not None
        assert stored.state.status is AgentSessionStatus.FAILED
        assert stored.state.last_turn_error == reason
        assert stored.state.active_turn_id is None
        assert [pending.message.text for pending in stored.queued_messages] == [
            "say after the tool"
        ]
        await harness.assert_new_messages_are_rejected(session_id, reason)


async def test_a_session_with_no_room_to_store_its_failure_still_reports_it(
    tmp_path: Path,
) -> None:
    """A small commit can leave less room than the failure's own commit needs.

    Sessions differ in how much room the last commit left, so some cannot store the failure.
    Each still reports it, from the Flow's close.
    """
    async with _state_limit_harness(tmp_path) as harness:

        async def run_to_failure(first_answer_characters: int) -> bool:
            session_id = harness.new_session_id()
            await harness.sessions.start_session(session_id)
            # All sent while there is room: a later send could be refused instead of failing a Step.
            for text in [
                "pad 25000",
                f"pad {first_answer_characters}",
                *["pad 40"] * NO_ROOM_SMALL_TURNS,
            ]:
                assert await harness.sessions.send_user_message(session_id, text)

            reason = await harness.close_reason(session_id)

            snapshot = await harness.sessions.get_agent_snapshot(session_id)
            assert snapshot.state is not None
            assert snapshot.state.status is AgentSessionStatus.FAILED
            assert snapshot.state.last_turn_error == reason
            assert snapshot.state.active_turn_id is None
            assert snapshot.pending_interrupts == []
            await harness.assert_new_messages_are_rejected(session_id, reason)
            stored = await harness.stored_snapshot(session_id)
            assert stored.state is not None
            return stored.state.status is AgentSessionStatus.FAILED

        stored_the_failure = await asyncio.gather(
            *[run_to_failure(25_000 + 37 * offset) for offset in range(NO_ROOM_SESSIONS)]
        )

        assert not all(stored_the_failure)


async def test_a_message_that_does_not_fit_is_refused_and_the_session_stays_usable(
    tmp_path: Path,
) -> None:
    """No Step fails here, so the session does not fail: only the caller learns of it."""
    async with _state_limit_harness(tmp_path) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id)
        await harness.fill(session_id, turns=2)

        with pytest.raises(DexServiceError) as refused:
            await harness.sessions.send_user_message(session_id, "say " + "u" * 60_000)

        assert refused.value.code.name == "RESOURCE_EXHAUSTED"
        assert STATE_LIMIT_REASON.match(refused.value.detail)
        snapshot = await harness.sessions.get_agent_snapshot(session_id)
        assert snapshot.state is not None
        assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
        assert snapshot.queued_message_count == 0
        assert await harness.sessions.send_user_message(session_id, "say still here")
        await wait_for_agent_snapshot(
            harness.sessions,
            session_id,
            lambda current: current
            if current.state is not None and current.state.turn_count == 3
            else None,
            description="the next turn to finish",
        )


async def test_one_value_over_its_own_limit_fails_only_the_turn(tmp_path: Path) -> None:
    async with _state_limit_harness(tmp_path, state_limit=1_048_576, value_limit=65_536) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, "pad 40000")
        failed = await wait_for_agent_snapshot(
            harness.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None and snapshot.state.last_turn_error is not None
            else None,
            description="the turn to fail",
        )
        assert failed.state is not None
        assert failed.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE

        assert await harness.sessions.send_user_message(session_id, "say next")
        finished = await wait_for_agent_snapshot(
            harness.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count == 2
            and snapshot.state.active_turn_id is None
            else None,
            description="the next turn to finish",
        )
        assert finished.state is not None
        assert finished.state.last_turn_error is None
