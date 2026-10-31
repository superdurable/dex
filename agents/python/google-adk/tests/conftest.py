# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Each test gets a private Dex engine and ADK Worker children it can kill."""

from __future__ import annotations

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
    StepHistoryEntry,
    wait_for_agent_snapshot,
)
from tests.scripted_adk_agent import (
    INVOCATION_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_scripted_adk_flow,
    build_scripted_adk_pipeline_flow,
)
from tests.scripted_adk_topologies import build_topology_flows

PACKAGE_DIR = Path(__file__).resolve().parents[1]
WORKER_MODULE = "tests.adk_worker"


@dataclass
class AdkAgentHarness:
    stack: AgentTestStack
    flows: dict[str, AgentSessionFlow]
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path

    def sessions_for(self, flow_type: str) -> AgentSessionClient:
        return self.stack.sessions(self.flows[flow_type])

    async def step_history(self, session_id: str) -> list[StepHistoryEntry]:
        return await self.stack.read_step_history(session_id)

    async def executed_step_types(self, session_id: str) -> list[str]:
        return [
            entry.step_type
            for entry in await self.step_history(session_id)
            if entry.event_type == EXECUTE_COMPLETED_EVENT
        ]

    async def agent_events(self, session_id: str) -> list[AgentEvent]:
        """Return the session's retained events, oldest first."""
        page = await self.stack.client.list_stream_messages(session_id, self.flow.agent_events, 500)
        return [message.value for message in reversed(page.messages)]

    @property
    def invocations(self) -> InvocationLog:
        return InvocationLog(self.state_dir / INVOCATION_LOG_NAME)

    def gate(self, name: str) -> FileGate:
        return FileGate(self.state_dir, name)

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

    async def wait_for_turns(
        self,
        session_id: str,
        turn_count: int,
        sessions: AgentSessionClient | None = None,
    ) -> AgentSnapshot:
        return await wait_for_agent_snapshot(
            sessions or self.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            else None,
            description=f"{turn_count} finished turn(s)",
        )

    async def wait_for_interrupt(
        self,
        session_id: str,
        tool_name: str,
        sessions: AgentSessionClient | None = None,
    ) -> AgentInterrupt:
        return await wait_for_agent_snapshot(
            sessions or self.sessions,
            session_id,
            lambda snapshot: next(
                (
                    interrupt
                    for interrupt in snapshot.pending_interrupts
                    if interrupt.tool_name == tool_name
                ),
                None,
            ),
            description=f"a pending {tool_name} approval",
        )


@pytest_asyncio.fixture
async def harness(tmp_path: Path) -> AsyncIterator[AdkAgentHarness]:
    """Starts a private engine; each test starts its own Worker children."""
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_scripted_adk_flow()
    flows = {
        candidate.get_flow_type(): candidate
        for candidate in [flow, build_scripted_adk_pipeline_flow(), *build_topology_flows()]
    }
    async with AgentTestStack(
        tmp_path,
        list(flows.values()),
        WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield AdkAgentHarness(stack, flows, flow, stack.sessions(flow), state_dir)
