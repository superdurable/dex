# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Each test gets a private Dex engine and Worker child processes it can kill."""

from __future__ import annotations

from collections.abc import AsyncIterator
from dataclasses import dataclass
from pathlib import Path
from uuid import uuid4

import pytest_asyncio

from dex_agents import AgentEvent, AgentSessionClient, AgentSessionFlow, AgentSnapshot
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    FileGate,
    InvocationLog,
    wait_for_agent_snapshot,
)
from tests.scripted_agent import (
    IDEMPOTENCY_KEY_LOG_NAME,
    INVOCATION_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_scripted_agent_flow,
)
from tests.stepwise_agent import (
    STATE_DIR_ENVIRONMENT_VARIABLE as STEPWISE_STATE_DIR_ENVIRONMENT_VARIABLE,
)
from tests.stepwise_agent import build_stepwise_agent_flow

CORE_DIR = Path(__file__).resolve().parents[1]
WORKER_MODULE = "tests.scripted_worker"
STEPWISE_WORKER_MODULE = "tests.stepwise_worker"


@dataclass
class ScriptedAgentHarness:
    stack: AgentTestStack
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path

    @property
    def invocations(self) -> InvocationLog:
        return InvocationLog(self.state_dir / INVOCATION_LOG_NAME)

    @property
    def idempotency_keys(self) -> InvocationLog:
        return InvocationLog(self.state_dir / IDEMPOTENCY_KEY_LOG_NAME)

    def gate(self, name: str) -> FileGate:
        return FileGate(self.state_dir, name)

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

    async def agent_events(self, session_id: str) -> list[AgentEvent]:
        page = await self.stack.client.list_stream_messages(session_id, self.flow.agent_events, 500)
        return [message.value for message in reversed(page.messages)]

    async def executed_step_types(self, session_id: str) -> list[str]:
        history = await self.stack.read_step_history(session_id)
        return [entry.step_type for entry in history if entry.event_type == EXECUTE_COMPLETED_EVENT]

    async def wait_for_turns(self, session_id: str, turn_count: int) -> AgentSnapshot:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            else None,
            description=f"{turn_count} finished turn(s)",
        )


@pytest_asyncio.fixture
async def harness(tmp_path: Path) -> AsyncIterator[ScriptedAgentHarness]:
    """Starts a private engine; each test starts its own Worker children."""
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_scripted_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield ScriptedAgentHarness(stack, flow, stack.sessions(flow), state_dir)


@pytest_asyncio.fixture
async def stepwise_harness(tmp_path: Path) -> AsyncIterator[ScriptedAgentHarness]:
    """Starts a private engine for the Stepwise scripted agent."""
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_stepwise_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        STEPWISE_WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STEPWISE_STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield ScriptedAgentHarness(stack, flow, stack.sessions(flow), state_dir)
