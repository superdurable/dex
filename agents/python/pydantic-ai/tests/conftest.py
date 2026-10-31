# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Each test gets a private Dex engine and Worker child processes it can kill."""

from __future__ import annotations

import json
from collections.abc import AsyncIterator
from dataclasses import dataclass
from pathlib import Path
from uuid import uuid4

import pytest_asyncio

from dex_agents import (
    AgentEvent,
    AgentInterrupt,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSnapshot,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    FileGate,
    InvocationLog,
    wait_for_agent_snapshot,
)
from tests.scripted_agent import (
    INVOCATION_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_scripted_agent_flow,
)

PACKAGE_DIR = Path(__file__).resolve().parents[1]
WORKER_MODULE = "tests.scripted_worker"


@dataclass
class ScriptedAgentHarness:
    stack: AgentTestStack
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path

    @property
    def invocations(self) -> InvocationLog:
        return InvocationLog(self.state_dir / INVOCATION_LOG_NAME)

    def gate(self, name: str) -> FileGate:
        return FileGate(self.state_dir, name)

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

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

    async def wait_for_pending_interrupt(
        self,
        session_id: str,
        tool_name: str | None = None,
    ) -> AgentInterrupt:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: next(
                (
                    interrupt
                    for interrupt in snapshot.pending_interrupts
                    if tool_name is None or interrupt.tool_name == tool_name
                ),
                None,
            ),
            description=f"a pending interrupt for {tool_name or 'any tool'}",
        )

    async def list_agent_events(self, session_id: str) -> list[AgentEvent]:
        page = await self.stack.client.list_stream_messages(session_id, self.flow.agent_events, 200)
        return [message.value for message in reversed(page.messages)]

    async def list_event_payloads(
        self,
        session_id: str,
        event_type: str,
    ) -> list[dict[str, object]]:
        return [
            json.loads(event.payload)
            for event in await self.list_agent_events(session_id)
            if event.type == event_type
        ]


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
        worker_cwd=PACKAGE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield ScriptedAgentHarness(stack, flow, stack.sessions(flow), state_dir)
