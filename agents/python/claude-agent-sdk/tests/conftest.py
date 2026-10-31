# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Each test gets a private Dex engine, killable Workers, and the scripted fake CLI."""

from __future__ import annotations

import json
import os
import shlex
import sys
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass
from pathlib import Path
from uuid import uuid4

import pytest
import pytest_asyncio

from dex_agents import (
    AgentEvent,
    AgentEventType,
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
from tests.claude_test_agent import (
    FAKE_CLI_PATH_ENVIRONMENT_VARIABLE,
    REAL_CLI_ENVIRONMENT_VARIABLE,
    build_claude_test_agent_flow,
)
from tests.fake_claude_cli import INVOCATION_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.mock_messages_api import MockMessagesApi

PACKAGE_DIR = Path(__file__).resolve().parents[1]
FAKE_CLI_SCRIPT = Path(__file__).resolve().parent / "fake_claude_cli.py"
WORKER_MODULE = "tests.claude_test_worker"
MOCK_API_KEY = "sk-ant-mock-not-a-real-key"
# Refused port: any request that bypasses the mock fails instead of leaving the host.
UNREACHABLE_PROXY = "http://127.0.0.1:9"


@dataclass
class ClaudeAgentHarness:
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

    async def list_events(self, session_id: str) -> list[AgentEvent]:
        """Return the session's retained events, oldest first."""
        page = await self.stack.client.list_stream_messages(session_id, self.flow.agent_events, 500)
        return [message.value for message in reversed(page.messages)]

    async def executed_step_types(self, session_id: str) -> list[str]:
        history = await self.stack.read_step_history(session_id)
        return [entry.step_type for entry in history if entry.event_type == EXECUTE_COMPLETED_EVENT]

    async def text_deltas(self, session_id: str) -> list[tuple[int, str]]:
        return [
            (event.attempt, json.loads(event.payload)["text"])
            for event in await self.list_events(session_id)
            if event.type == AgentEventType.TEXT_DELTA
        ]


@pytest_asyncio.fixture
async def harness(tmp_path: Path) -> AsyncIterator[ClaudeAgentHarness]:
    """Starts a private engine; each test starts its own Worker children."""
    state_dir = tmp_path / "agent-state"
    (state_dir / "workspace").mkdir(parents=True)
    (state_dir / "claude-config").mkdir()
    fake_cli_path = tmp_path / "fake-claude"
    fake_cli_path.write_text(
        "#!/bin/sh\n"
        f'exec {shlex.quote(sys.executable)} {shlex.quote(str(FAKE_CLI_SCRIPT))} "$@"\n'
    )
    fake_cli_path.chmod(0o755)
    flow = build_claude_test_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={
            STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir),
            FAKE_CLI_PATH_ENVIRONMENT_VARIABLE: str(fake_cli_path),
        },
    ) as stack:
        yield ClaudeAgentHarness(stack, flow, stack.sessions(flow), state_dir)


@dataclass
class RealClaudeCliHarness(ClaudeAgentHarness):
    messages_api: MockMessagesApi
    claude_home: Path


@pytest.fixture
def messages_api(monkeypatch: pytest.MonkeyPatch) -> Iterator[MockMessagesApi]:
    """Starts the scripted Messages API and hides the host's Claude settings from Workers."""
    for name in list(os.environ):
        if name.startswith(("CLAUDE", "ANTHROPIC")) or name == "AI_AGENT":
            monkeypatch.delenv(name)
    api = MockMessagesApi()
    api.start()
    try:
        yield api
    finally:
        api.stop()


@pytest_asyncio.fixture
async def real_cli_harness(
    tmp_path: Path, messages_api: MockMessagesApi
) -> AsyncIterator[RealClaudeCliHarness]:
    """Runs the bundled Claude Code CLI against the mock with a private home and config."""
    state_dir = tmp_path / "agent-state"
    (state_dir / "workspace").mkdir(parents=True)
    (state_dir / "claude-config").mkdir()
    claude_home = tmp_path / "home"
    claude_home.mkdir()
    cli_environment = {
        "HOME": str(claude_home),
        "ANTHROPIC_API_KEY": MOCK_API_KEY,
        "ANTHROPIC_BASE_URL": messages_api.base_url,
        "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
        "DISABLE_AUTOUPDATER": "1",
        "DISABLE_TELEMETRY": "1",
        "DISABLE_ERROR_REPORTING": "1",
        "HTTPS_PROXY": UNREACHABLE_PROXY,
        "HTTP_PROXY": UNREACHABLE_PROXY,
        "NO_PROXY": "127.0.0.1,localhost",
    }
    flow = build_claude_test_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={
            STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir),
            REAL_CLI_ENVIRONMENT_VARIABLE: json.dumps(cli_environment),
            "CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK": "1",
        },
    ) as stack:
        yield RealClaudeCliHarness(
            stack, flow, stack.sessions(flow), state_dir, messages_api, claude_home
        )
