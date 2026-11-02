# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Integration coverage for hard kills and code changes while the AI Agent runs."""

from __future__ import annotations

import asyncio
import json
import shutil
import subprocess
from collections.abc import AsyncIterator, Callable
from dataclasses import dataclass, field
from datetime import timedelta
from pathlib import Path

import pytest
import pytest_asyncio
from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentSessionClient,
    AgentSessionStatus,
    InterruptKind,
    MessageRole,
    PlanStatus,
)
from tests.integ.ai_agent_processes import (
    EXAMPLES_DIR,
    ExamplesWorkerProcess,
    PrivateDexEngine,
    available_port,
    server_zd_binary,
    write_agent_mcp_config,
)
from tests.integ.ai_agent_sessions import (
    draft_revision,
    is_idle,
    is_waiting_for_timer,
    list_all_messages,
    message_text,
    pending_interrupt,
    send_user_message,
    wait_for_message_text,
    wait_for_snapshot,
)

from dex_examples.app import ExampleApp
from dex_examples.config import ExamplesConfig

RESTART_TIMEOUT = timedelta(seconds=90)
TIMER_SECONDS = 30
# Long enough to kill the Worker mid call, short enough for the retried read to finish.
SLOW_CALL_SECONDS = 8
CODE_CHANGE_PATCH = EXAMPLES_DIR / "ai-agent" / "changes" / "report-tool-results.patch"


@dataclass
class PrivateAgentStack:
    engine: PrivateDexEngine
    app: ExampleApp
    agent_mcp_config: Path
    state_dir: Path
    workers: list[ExamplesWorkerProcess] = field(default_factory=list)

    @property
    def sessions(self) -> AgentSessionClient:
        return AgentSessionClient(self.app.client, self.app.ai_agent)

    def new_worker(
        self,
        project_dir: Path = EXAMPLES_DIR,
        extra_env: dict[str, str] | None = None,
    ) -> ExamplesWorkerProcess:
        worker = ExamplesWorkerProcess(
            self.engine.address,
            self.agent_mcp_config,
            self.state_dir,
            project_dir,
            extra_env,
        )
        self.workers.append(worker)
        return worker


@pytest_asyncio.fixture()
async def agent_stack(tmp_path: Path) -> AsyncIterator[PrivateAgentStack]:
    """Starts a private server-zd whose only Worker is a killable main.py process."""
    if server_zd_binary() is None:
        pytest.skip("set DEX_SERVER_ZD_BINARY to run the AI Agent crash tests on server-zd")
    engine = PrivateDexEngine(tmp_path)
    app = ExampleApp(
        ExamplesConfig(
            server_address=engine.address,
            http_address="127.0.0.1:0",
            blob_cache_dir=tmp_path / "client-blob-cache",
        )
    )
    stack = PrivateAgentStack(
        engine,
        app,
        write_agent_mcp_config(tmp_path / "mcp-servers.yaml"),
        tmp_path,
    )
    try:
        await engine.start()
        yield stack
    finally:
        for worker in stack.workers:
            worker.kill()
        engine.kill()
        await app.close()


async def test_ai_agent_resumes_every_wait_after_its_engine_and_worker_are_killed(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    sessions = agent_stack.sessions
    worker = agent_stack.new_worker()
    await worker.start()
    approval_session = await _start(agent_stack, new_flow_id("ai-agent-kill-approval"))
    timer_session = await _start(agent_stack, new_flow_id("ai-agent-kill-timer"))
    question_session = await _start(agent_stack, new_flow_id("ai-agent-kill-question"))

    await send_user_message(
        sessions, approval_session, '/tool test__publish {"message":"after restart"}'
    )
    await send_user_message(sessions, timer_session, f"/wait {TIMER_SECONDS} check after restart")
    await send_user_message(
        sessions, question_session, "/ask What date should I use?", plan_mode=True
    )
    revision = await wait_for_snapshot(
        sessions,
        question_session,
        "draft plan",
        draft_revision,
        RESTART_TIMEOUT,
    )
    assert await sessions.execute_plan(question_session, revision)
    approval = await wait_for_snapshot(
        sessions,
        approval_session,
        "tool approval",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.TOOL_APPROVAL),
        RESTART_TIMEOUT,
    )
    await wait_for_snapshot(
        sessions, timer_session, "durable wait", is_waiting_for_timer, RESTART_TIMEOUT
    )
    question = await wait_for_snapshot(
        sessions,
        question_session,
        "question",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.QUESTION),
        RESTART_TIMEOUT,
    )
    before = {
        session_id: await sessions.get_agent_snapshot(session_id)
        for session_id in (approval_session, timer_session, question_session)
    }

    worker.kill()
    agent_stack.engine.kill()
    await agent_stack.engine.start()
    await worker.start()

    # The same runs keep their conversation, plan, and pending approval, timer, or question.
    for session_id, snapshot in before.items():
        assert (
            await wait_for_snapshot(
                sessions, session_id, "snapshot", lambda item: item, RESTART_TIMEOUT
            )
            == snapshot
        )
    assert await sessions.resolve_interrupt(
        approval_session, approval.interrupt_id, approved=True
    )
    await wait_for_message_text(
        sessions, approval_session, "published:after restart", RESTART_TIMEOUT
    )
    await wait_for_message_text(sessions, timer_session, "check after restart", RESTART_TIMEOUT)
    timer_reply = await wait_for_message_text(
        sessions, timer_session, "Local demo response: {", RESTART_TIMEOUT
    )
    assert json.loads(timer_reply.text.split(": ", 1)[1])["status"] == "completed"
    assert await sessions.answer_question(question_session, question.interrupt_id, "September 12")
    await wait_for_snapshot(
        sessions,
        question_session,
        "completed plan",
        lambda snapshot: snapshot.plan is not None and snapshot.plan.status == PlanStatus.COMPLETED,
        RESTART_TIMEOUT,
    )


async def test_ai_agent_retries_a_cut_off_read_and_reports_a_cut_off_write(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    sessions = agent_stack.sessions
    worker = agent_stack.new_worker()
    await worker.start()
    read_session = await _start(agent_stack, new_flow_id("ai-agent-cut-read"))
    write_session = await _start(agent_stack, new_flow_id("ai-agent-cut-write"))
    await send_user_message(
        sessions,
        read_session,
        '/tool test__slow_lookup {"query":"cut off","seconds":%d}' % SLOW_CALL_SECONDS,
    )
    await send_user_message(
        sessions,
        write_session,
        '/tool test__slow_publish {"message":"cut off","seconds":%d}' % SLOW_CALL_SECONDS,
    )
    approval = await wait_for_snapshot(
        sessions,
        write_session,
        "write approval",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.TOOL_APPROVAL),
        RESTART_TIMEOUT,
    )
    assert await sessions.resolve_interrupt(write_session, approval.interrupt_id, approved=True)
    for session_id in (read_session, write_session):
        await wait_for_snapshot(
            sessions,
            session_id,
            "running tool",
            lambda snapshot: snapshot.state is not None
            and snapshot.state.status == AgentSessionStatus.RUNNING_TOOLS
            and not snapshot.pending_interrupts,
            RESTART_TIMEOUT,
        )
    # Give each call time to start its MCP server process.
    await asyncio.sleep(3)

    worker.kill()
    await worker.start()
    # A read is safe to repeat, so its tool Step runs it again.
    await wait_for_message_text(
        sessions, read_session, "Local demo response: found:cut off", RESTART_TIMEOUT
    )
    # A write that may have taken effect is never repeated; the model learns its outcome is unknown.
    write_result = await wait_for_message_text(
        sessions, write_session, OUTCOME_UNKNOWN_TOOL_RESULT, RESTART_TIMEOUT
    )
    assert write_result.role == MessageRole.TOOL
    messages = await list_all_messages(sessions, write_session)
    assert not any("published:cut off" in message_text(message) for message in messages)
    for session_id in (read_session, write_session):
        await send_user_message(sessions, session_id, "still running")
        await wait_for_message_text(
            sessions, session_id, "Local demo response: still running", RESTART_TIMEOUT
        )


async def test_ai_agent_repeats_only_the_model_call_a_worker_crash_cut_off(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    sessions = agent_stack.sessions
    worker = agent_stack.new_worker()
    await worker.start()
    session_id = await _start(agent_stack, new_flow_id("ai-agent-model-crash"))
    await send_user_message(sessions, session_id, "first reply")
    await wait_for_message_text(sessions, session_id, "Local demo response: first reply")
    await send_user_message(sessions, session_id, f"/slow {SLOW_CALL_SECONDS} second reply")
    await wait_for_snapshot(
        sessions,
        session_id,
        "model call",
        lambda snapshot: snapshot.state is not None
        and snapshot.state.status == AgentSessionStatus.RUNNING_MODEL,
        RESTART_TIMEOUT,
    )
    await asyncio.sleep(1)

    worker.kill()
    await asyncio.sleep(5)
    await worker.start()
    await wait_for_message_text(
        sessions,
        session_id,
        f"Local demo response: /slow {SLOW_CALL_SECONDS} second reply",
        RESTART_TIMEOUT,
    )
    messages = await list_all_messages(sessions, session_id)
    assert [message_text(message) for message in messages] == [
        "first reply",
        "Local demo response: first reply",
        f"/slow {SLOW_CALL_SECONDS} second reply",
        f"Local demo response: /slow {SLOW_CALL_SECONDS} second reply",
    ]


async def test_ai_agent_records_a_model_provider_failure(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    sessions = agent_stack.sessions
    worker = agent_stack.new_worker(
        extra_env={
            "DEX_AGENT_MODEL": "openai/gpt-5-mini",
            "OPENAI_API_KEY": "not-a-real-key",
            "OPENAI_BASE_URL": f"http://127.0.0.1:{available_port()}/v1",
        }
    )
    await worker.start()
    session_id = await _start(agent_stack, new_flow_id("ai-agent-model-api-failure"))
    await send_user_message(sessions, session_id, "hello")
    error = await wait_for_snapshot(
        sessions,
        session_id,
        "model failure",
        lambda snapshot: snapshot.state.last_turn_error
        if snapshot.state is not None and is_idle(snapshot)
        else None,
        RESTART_TIMEOUT,
    )
    assert error


async def test_ai_agent_compacts_context_on_the_workers_window(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    sessions = agent_stack.sessions
    worker = agent_stack.new_worker(extra_env={"DEX_AGENT_CONTEXT_TOKENS": "200"})
    await worker.start()
    session_id = await _start(agent_stack, new_flow_id("ai-agent-compaction"))
    for index in range(5):
        text = f"turn {index}: " + "context " * 20
        await send_user_message(sessions, session_id, text)
        await wait_for_message_text(sessions, session_id, f"Local demo response: {text}")
    snapshot = await wait_for_snapshot(
        sessions, session_id, "idle", lambda item: item if is_idle(item) else None
    )
    assert snapshot.context_summary is not None
    assert snapshot.context_summary.last_sequence > 0
    # Compaction shortens the model input; every message stays in the session.
    messages = await list_all_messages(sessions, session_id)
    assert len(messages) == 10
    assert messages[-1].role == MessageRole.ASSISTANT


async def test_ai_agent_answers_without_mcp_tools_when_every_server_is_down(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
    tmp_path: Path,
) -> None:
    sessions = agent_stack.sessions
    down_config = tmp_path / "mcp-servers-down.yaml"
    down_config.write_text(
        "servers:\n  - name: down\n    transport: stdio\n    command: /missing/mcp-server\n"
    )
    worker = agent_stack.new_worker(extra_env={"DEX_AGENT_MCP_CONFIG": str(down_config)})
    await worker.start()
    session_id = await _start(agent_stack, new_flow_id("ai-agent-mcp-down"))
    await send_user_message(sessions, session_id, '/tool down__lookup {"query":"x"}')
    await wait_for_message_text(
        sessions, session_id, "unknown tool down__lookup", RESTART_TIMEOUT
    )
    await send_user_message(sessions, session_id, "still answering")
    await wait_for_message_text(
        sessions, session_id, "Local demo response: still answering", RESTART_TIMEOUT
    )


async def test_ai_agent_waiting_session_follows_code_changed_while_it_waited(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
    tmp_path: Path,
) -> None:
    sessions = agent_stack.sessions
    project_dir = _copy_examples_project(tmp_path / "project")
    worker = agent_stack.new_worker(project_dir)
    await worker.start()
    session_id = await _start(agent_stack, new_flow_id("ai-agent-code-change"))
    await send_user_message(sessions, session_id, '/tool test__publish {"message":"new code"}')
    approval = await wait_for_snapshot(
        sessions,
        session_id,
        "tool approval",
        lambda snapshot: pending_interrupt(snapshot, InterruptKind.TOOL_APPROVAL),
        RESTART_TIMEOUT,
    )
    run_id = (await agent_stack.app.client.describe_flow(session_id)).run_id

    # The session waited while the next model call's code changed.
    worker.kill()
    subprocess.run(["patch", "-p1", "-i", str(CODE_CHANGE_PATCH)], cwd=project_dir, check=True)
    await worker.start()
    assert await sessions.resolve_interrupt(session_id, approval.interrupt_id, approved=True)
    await wait_for_message_text(sessions, session_id, "Done: published:new code", RESTART_TIMEOUT)
    assert (await agent_stack.app.client.describe_flow(session_id)).run_id == run_id


def _copy_examples_project(destination: Path) -> Path:
    destination.mkdir()
    shutil.copy2(EXAMPLES_DIR / "main.py", destination)
    ignore = shutil.ignore_patterns("__pycache__", "node_modules")
    for directory in ("dex_examples", "ai-agent"):
        shutil.copytree(EXAMPLES_DIR / directory, destination / directory, ignore=ignore)
    return destination


async def _start(stack: PrivateAgentStack, session_id: str) -> str:
    await stack.sessions.start_session(session_id)
    return session_id
