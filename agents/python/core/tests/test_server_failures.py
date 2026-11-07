# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Crash the Dex Server under each agent Step while the Worker keeps running."""

from __future__ import annotations

import json
from collections.abc import AsyncIterator, Callable
from contextlib import asynccontextmanager
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentEvent,
    AgentEventType,
    AgentInterrupt,
    AgentSessionFlow,
    MessageRole,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    WAIT_FOR_COMPLETED_EVENT,
    FileGate,
    wait_for_agent_snapshot,
    wait_until,
)
from tests.compaction_agent import STATE_DIR_ENVIRONMENT_VARIABLE as COMPACTION_STATE_DIR
from tests.compaction_agent import SUMMARY_GATE_NAME, build_compaction_agent_flow
from tests.conftest import (
    CORE_DIR,
    STEPWISE_WORKER_MODULE,
    TASK_WORKER_MODULE,
    WORKER_MODULE,
    ScriptedAgentHarness,
)
from tests.scripted_agent import STATE_DIR_ENVIRONMENT_VARIABLE, build_scripted_agent_flow
from tests.server_failures import (
    CrashableServer,
    CrashableServerStack,
    RestartedServer,
    TakenOverServer,
)
from tests.stepwise_agent import STATE_DIR_ENVIRONMENT_VARIABLE as STEPWISE_STATE_DIR
from tests.stepwise_agent import build_stepwise_agent_flow
from tests.task_agent import STATE_DIR_ENVIRONMENT_VARIABLE as TASK_STATE_DIR
from tests.task_agent import build_task_agent_flow
from tests.test_compaction import PAD_TURN_COMMANDS, CompactionHarness
from tests.test_compaction import WORKER_MODULE as COMPACTION_WORKER_MODULE

TASK_CALL = "turn-1-call-0"


@pytest.fixture(params=[RestartedServer, TakenOverServer], ids=["restart", "takeover"])
def server(request: pytest.FixtureRequest, tmp_path: Path) -> CrashableServer:
    server_type: type[CrashableServer] = request.param
    return server_type(tmp_path / "server")


@asynccontextmanager
async def _stack(
    tmp_path: Path,
    server: CrashableServer,
    build_flow: Callable[[Path], AgentSessionFlow],
    worker_module: str,
    state_dir_variable: str,
) -> AsyncIterator[tuple[CrashableServerStack, AgentSessionFlow, Path]]:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_flow(state_dir)
    stack = CrashableServerStack(
        tmp_path,
        server,
        [flow],
        worker_module,
        worker_cwd=CORE_DIR,
        worker_env={state_dir_variable: str(state_dir)},
    )
    async with stack:
        yield stack, flow, state_dir


@asynccontextmanager
async def _harness(
    tmp_path: Path,
    server: CrashableServer,
    build_flow: Callable[[Path], AgentSessionFlow],
    worker_module: str,
    state_dir_variable: str,
) -> AsyncIterator[ScriptedAgentHarness]:
    async with _stack(tmp_path, server, build_flow, worker_module, state_dir_variable) as (
        stack,
        flow,
        state_dir,
    ):
        await stack.start_worker()
        yield ScriptedAgentHarness(stack, flow, stack.sessions(flow), state_dir)


def _scripted(tmp_path: Path, server: CrashableServer) -> Any:
    return _harness(
        tmp_path, server, build_scripted_agent_flow, WORKER_MODULE, STATE_DIR_ENVIRONMENT_VARIABLE
    )


def _stepwise(tmp_path: Path, server: CrashableServer) -> Any:
    return _harness(
        tmp_path, server, build_stepwise_agent_flow, STEPWISE_WORKER_MODULE, STEPWISE_STATE_DIR
    )


def _task(tmp_path: Path, server: CrashableServer) -> Any:
    return _harness(tmp_path, server, build_task_agent_flow, TASK_WORKER_MODULE, TASK_STATE_DIR)


async def _crash_and_recover(server: CrashableServer, gate: FileGate | None = None) -> None:
    await server.crash()
    if gate is not None:
        # Releasing first means the interrupted run could finish while the server is down.
        gate.release()
    await server.recover()


async def test_server_failure_during_run_model_reruns_only_that_call(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id, 'call-then-stall model-gate lookup {"query": "q"}'
        )
        gate = harness.gate("model-gate")
        await gate.wait_until_reached()

        await _crash_and_recover(server, gate)
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == "lookup: found q"
        assert harness.invocations.count("lookup") == 1
        assert harness.invocations.count("model:turn-1:1") == 1
        assert harness.invocations.count("model:turn-1:2") == 2


async def test_server_failure_before_any_model_output_reruns_the_model_call(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, "block window-gate 2")
        gate = harness.gate("window-gate")
        await gate.wait_until_reached()

        await _crash_and_recover(server)
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == "blocked at window-gate"
        assert gate.reached_count() == 2


async def test_server_failure_before_the_first_heartbeat_retries_an_idempotent_write(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id,
            'call charge {"amount": 9, "blocking_gate": "window-gate", "blocking_seconds": 2}',
        )
        await harness.gate("window-gate").wait_until_reached()

        await _crash_and_recover(server)
        snapshot = await harness.wait_for_turns(session_id, 1)

        key = f"{session_id}/turn-1/turn-1-call-0"
        assert snapshot.messages[-1].text == f"charge: charged 9 with key {key}"
        assert harness.idempotency_keys.names() == [
            f"charge {key} attempt 1",
            f"charge {key} attempt 2",
        ]


async def test_server_failure_during_call_tool_retries_with_the_same_key(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id, 'call charge {"amount": 9, "gate": "charge-gate"}'
        )
        gate = harness.gate("charge-gate")
        await gate.wait_until_reached()

        await _crash_and_recover(server, gate)
        snapshot = await harness.wait_for_turns(session_id, 1)

        key = f"{session_id}/turn-1/turn-1-call-0"
        assert snapshot.messages[-1].text == f"charge: charged 9 with key {key}"
        assert harness.idempotency_keys.names() == [
            f"charge {key} attempt 1",
            f"charge {key} attempt 2",
        ]


async def test_server_failure_during_a_single_attempt_write_records_outcome_unknown(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id, 'call slow_write {"message": "ledger", "gate": "write-gate"}'
        )
        gate = harness.gate("write-gate")
        await gate.wait_until_reached()

        await _crash_and_recover(server, gate)
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
        tool_results = [m for m in snapshot.messages if m.role is MessageRole.TOOL]
        assert [m.tool_output for m in tool_results] == [OUTCOME_UNKNOWN_TOOL_RESULT]
        assert harness.invocations.count("slow_write") == 1
        assert gate.reached_count() == 1
        assert "RecordToolOutcomeUnknown" in await harness.executed_step_types(session_id)


async def test_server_failure_before_the_first_heartbeat_runs_a_single_attempt_tool_once(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id,
            'call slow_write {"message": "m", "blocking_gate": "window-gate", '
            '"blocking_seconds": 2}',
        )
        gate = harness.gate("window-gate")
        await gate.wait_until_reached()

        await _crash_and_recover(server)
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
        assert harness.invocations.count("slow_write") == 1
        assert gate.reached_count() == 1


async def test_an_approval_wait_survives_a_server_failure_and_the_write_runs_once(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, 'call publish {"message": "launch"}')
        interrupt = await _wait_for_pending_interrupt(harness, session_id)
        await _wait_for_step_methods(
            harness, session_id, "AwaitToolApproval", WAIT_FOR_COMPLETED_EVENT, 1
        )

        await _crash_and_recover(server)
        after = await _wait_for_snapshot(harness, session_id)
        assert after.pending_interrupts == [interrupt]
        assert await harness.sessions.resolve_interrupt(
            session_id, interrupt.interrupt_id, approved=True
        )
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == "publish: published launch"
        assert harness.invocations.count("publish") == 1


async def test_a_question_wait_survives_a_server_failure_and_the_answer_resumes(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _stepwise(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, "ask Ship it today?")
        question = await _wait_for_pending_interrupt(harness, session_id)
        await _wait_for_step_methods(
            harness, session_id, "AwaitQuestionAnswer", WAIT_FOR_COMPLETED_EVENT, 1
        )

        await _crash_and_recover(server)
        after = await _wait_for_snapshot(harness, session_id)
        assert after.pending_interrupts == [question]
        assert await harness.sessions.answer_question(session_id, question.interrupt_id, "yes")
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == 'ask_user: {"status":"answered","answer":"yes"}'
        assert harness.invocations.count("model:turn-1:1") == 1


async def test_a_timer_wait_survives_a_server_failure_and_fires_once(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _stepwise(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, "wait 3 the failure")
        await _wait_for_step_methods(harness, session_id, "AwaitTimer", WAIT_FOR_COMPLETED_EVENT, 1)

        await _crash_and_recover(server)
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == (
            'durable_wait: {"status":"completed","waited_seconds":3,"reason":"the failure"}'
        )
        assert (await harness.executed_step_types(session_id)).count("AwaitTimer") == 1
        # The memory Stream backend lost the earlier events; the Timer result is durable state.
        assert _finished_statuses(await harness.agent_events(session_id)) == ["succeeded"]


async def test_a_poll_wait_survives_a_server_failure_and_polling_resumes(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _task(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id, 'call report {"topic": "q2", "interval": 3, "poll_gate": "report-gate"}'
        )
        gate = harness.gate("report-gate")
        await gate.wait_until_reached()
        # The second PollToolTask waits on its Timer when the server dies.
        await _wait_for_step_methods(
            harness, session_id, "PollToolTask", WAIT_FOR_COMPLETED_EVENT, 2
        )

        await _crash_and_recover(server, gate)
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == "report: report on q2"
        assert harness.task_invocations.count(f"create {TASK_CALL}") == 1
        assert harness.task_invocations.count(f"start {TASK_CALL} attempt 1") == 1
        assert _finished_statuses(await harness.agent_events(session_id)) == ["succeeded"]


async def test_server_failure_during_a_poll_polls_again_and_keeps_one_task(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _task(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(
            session_id, 'call report {"topic": "q3", "polls": 1, "block_gate": "poll-gate"}'
        )
        gate = harness.gate("poll-gate")
        await gate.wait_until_reached()

        await _crash_and_recover(server, gate)
        snapshot = await harness.wait_for_turns(session_id, 1)

        assert snapshot.messages[-1].text == "report: report on q3"
        assert gate.reached_count() == 2
        assert harness.task_invocations.count(f"create {TASK_CALL}") == 1
        assert _finished_statuses(await harness.agent_events(session_id)) == ["succeeded"]


async def test_an_idle_session_survives_a_server_failure_and_the_next_message_starts_a_turn(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _scripted(tmp_path, server) as harness:
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, "say first")
        await harness.wait_for_turns(session_id, 1)

        await _crash_and_recover(server)

        async def message_was_taken() -> bool:
            return bool(await harness.sessions.send_user_message(session_id, "say second"))

        await wait_until("the session to take a message", message_was_taken)
        snapshot = await harness.wait_for_turns(session_id, 2)
        assert snapshot.messages[-1].text == "said: second"


async def test_server_failure_during_compact_context_reruns_only_the_summary_call(
    tmp_path: Path, server: CrashableServer
) -> None:
    async with _stack(
        tmp_path,
        server,
        build_compaction_agent_flow,
        COMPACTION_WORKER_MODULE,
        COMPACTION_STATE_DIR,
    ) as (stack, flow, state_dir):
        harness = CompactionHarness(stack, flow, stack.sessions(flow), state_dir)
        await stack.start_worker()
        session_id = harness.new_session_id()
        commands = ["pad 400 gate-summary", *PAD_TURN_COMMANDS[1:]]
        await harness.sessions.start_session(session_id, commands[0])
        for command in commands[1:]:
            assert await harness.sessions.send_user_message(session_id, command)
        gate = FileGate(state_dir, SUMMARY_GATE_NAME)
        await gate.wait_until_reached()

        await _crash_and_recover(server, gate)
        snapshot = await harness.wait_for_turns(session_id, len(commands))

        assert gate.reached_count() == 2
        assert len(harness.model_requests("summary")) == 2
        assert snapshot.context_summary is not None
        assert snapshot.context_summary.generation == 1
        assert (
            len(await harness.event_payloads(session_id, AgentEventType.COMPACTION_FINISHED)) == 1
        )
        history = await harness.stack.read_step_history(session_id)
        assert [
            entry.step_type
            for entry in history
            if entry.event_type == EXECUTE_COMPLETED_EVENT and entry.step_type == "CompactContext"
        ] == ["CompactContext"]


async def _wait_for_pending_interrupt(
    harness: ScriptedAgentHarness, session_id: str
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="a pending interrupt",
    )


async def _wait_for_snapshot(harness: ScriptedAgentHarness, session_id: str) -> Any:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot,
        description="a snapshot from the recovered server",
    )


async def _wait_for_step_methods(
    harness: ScriptedAgentHarness,
    session_id: str,
    step_type: str,
    event_type: str,
    count: int,
) -> None:
    async def has_completed() -> bool:
        history = await harness.stack.read_step_history(session_id)
        completed = [
            entry
            for entry in history
            if entry.step_type == step_type and entry.event_type == event_type
        ]
        return len(completed) >= count

    await wait_until(f"{count} {step_type} {event_type}", has_completed)


def _finished_statuses(events: list[AgentEvent]) -> list[str]:
    return [
        json.loads(event.payload)["status"]
        for event in events
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
