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
from tests.support_agent import (
    IDEMPOTENCY_KEY_LOG_NAME,
    INVOCATION_LOG_NAME,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_support_agent_flow,
)

PACKAGE_DIR = Path(__file__).resolve().parents[1]
WORKER_MODULE = "tests.support_agent_worker"


@dataclass
class SupportAgentHarness:
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

    def model_calls(self) -> list[str]:
        return [name for name in self.invocations.names() if name.startswith("model:")]

    def tool_runs(self) -> list[str]:
        return [name for name in self.invocations.names() if not name.startswith("model:")]

    async def executed_step_types(self, session_id: str) -> list[str]:
        history = await self.stack.read_step_history(session_id)
        return [entry.step_type for entry in history if entry.event_type == EXECUTE_COMPLETED_EVENT]

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

    async def wait_for_pending_interrupt(self, session_id: str) -> AgentInterrupt:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: next(iter(snapshot.pending_interrupts), None),
            description="a pending interrupt",
        )

    async def list_agent_events(self, session_id: str) -> list[AgentEvent]:
        page = await self.stack.client.list_stream_messages(session_id, self.flow.agent_events, 500)
        return [message.value for message in reversed(page.messages)]


@pytest_asyncio.fixture
async def harness(tmp_path: Path) -> AsyncIterator[SupportAgentHarness]:
    """Starts a private engine; each test starts its own Worker children."""
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_support_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
    ) as stack:
        yield SupportAgentHarness(stack, flow, stack.sessions(flow), state_dir)
