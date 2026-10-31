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

import pytest
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

from tests.scripted_agents import (
    CREATE_AGENT_FLOW_TYPE,
    INVOCATION_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    STATE_GRAPH_FLOW_TYPE,
    build_scripted_agent_flows,
)
from tests.scripted_model import MODEL_CALL_INVOCATION_PREFIX

PACKAGE_DIR = Path(__file__).resolve().parents[1]
WORKER_MODULE = "tests.scripted_worker"
#: Both ways to build an agent; tests parametrized over it run once per way.
AGENT_FLOW_TYPES = [CREATE_AGENT_FLOW_TYPE, STATE_GRAPH_FLOW_TYPE]


@dataclass
class ScriptedAgentHarness:
    stack: AgentTestStack
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path

    @property
    def invocations(self) -> InvocationLog:
        return InvocationLog(self.state_dir / INVOCATION_LOG_NAME)

    def model_call_count(self, call_number: int | None = None) -> int:
        names = [
            name
            for name in self.invocations.names()
            if name.startswith(MODEL_CALL_INVOCATION_PREFIX)
        ]
        if call_number is None:
            return len(names)
        return names.count(f"{MODEL_CALL_INVOCATION_PREFIX}{call_number}")

    def tool_invocations(self) -> list[str]:
        return [
            name
            for name in self.invocations.names()
            if not name.startswith(MODEL_CALL_INVOCATION_PREFIX)
        ]

    async def read_step_history(self, session_id: str) -> list[StepHistoryEntry]:
        return await self.stack.read_step_history(session_id)

    async def executed_step_types(self, session_id: str) -> list[str]:
        return [
            entry.step_type
            for entry in await self.read_step_history(session_id)
            if entry.event_type == EXECUTE_COMPLETED_EVENT
        ]

    def gate(self, name: str) -> FileGate:
        return FileGate(self.state_dir, name)

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

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

    async def wait_for_pending_interrupts(
        self,
        session_id: str,
        count: int = 1,
    ) -> list[AgentInterrupt]:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: snapshot.pending_interrupts
            if len(snapshot.pending_interrupts) >= count
            else None,
            description=f"{count} pending interrupt(s)",
        )

    async def list_agent_events(self, session_id: str) -> list[AgentEvent]:
        page = await self.stack.client.list_stream_messages(session_id, self.flow.agent_events, 500)
        # Pages are newest first; tests read events in the order they were written.
        return [message.value for message in reversed(page.messages)]

    async def list_streamed_text(self, session_id: str, attempt: int = 1) -> str:
        return "".join(
            json.loads(event.payload)["text"]
            for event in await self.list_agent_events(session_id)
            if event.type == "text_delta" and event.attempt == attempt
        )


@pytest.fixture
def agent_flow_type() -> str:
    """Selects the agent under test; tests parametrize it with AGENT_FLOW_TYPES."""
    return CREATE_AGENT_FLOW_TYPE


@pytest_asyncio.fixture
async def harness(tmp_path: Path, agent_flow_type: str) -> AsyncIterator[ScriptedAgentHarness]:
    """Starts a private engine; each test starts its own Worker children."""
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flows = build_scripted_agent_flows(state_dir)
    flow = next(flow for flow in flows if flow.get_flow_type() == agent_flow_type)
    async with AgentTestStack(
        tmp_path,
        flows,
        WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield ScriptedAgentHarness(stack, flow, stack.sessions(flow), state_dir)
