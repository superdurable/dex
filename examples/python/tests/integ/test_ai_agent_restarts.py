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
from dex import AsyncClient, DexServiceError, StartFlowOptions
from tests.integ.ai_agent_processes import (
    EXAMPLES_DIR,
    ExamplesWorkerProcess,
    PrivateDexEngine,
    available_port,
    server_zd_binary,
    write_agent_mcp_config,
)
from tests.integ.conftest import wait_until
from tests.integ.flow_smoke_helper import dexcli_path, run_dexcli_json

from dex_examples.app import ExampleApp
from dex_examples.config import ExamplesConfig
from dex_examples.products.ai_agent.ai_agent_flow import STATUS_WAITING, AIAgentFlow
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    AgentDescription,
    AgentSnapshot,
    HistoryRequest,
    PlanExecutionRequest,
    ToolApprovalRequest,
    UserMessage,
)

RESTART_TIMEOUT = timedelta(seconds=90)
TIMER_SECONDS = 30
SLOW_CALL_SECONDS = 120
# A crash mid tool call must reach the model in seconds, Worker restart included.
TOOL_CRASH_REPORT_SECONDS = 20
# Outlasts all three CallModel attempts, each of which waits 10 seconds for a Worker.
MODEL_OUTAGE_SECONDS = 30
CODE_CHANGE_PATCH = EXAMPLES_DIR / "ai-agent" / "changes" / "save-task-summaries.patch"


@dataclass
class PrivateAgentStack:
    engine: PrivateDexEngine
    app: ExampleApp
    agent_mcp_config: Path
    state_dir: Path
    workers: list[ExamplesWorkerProcess] = field(default_factory=list)

    @property
    def client(self) -> AsyncClient:
        return self.app.client

    @property
    def agent(self) -> AIAgentFlow:
        return self.app.ai_agent

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
    client, agent = agent_stack.client, agent_stack.agent
    worker = agent_stack.new_worker()
    await worker.start()
    approval_flow = await _start_agent(agent_stack, new_flow_id("ai-agent-kill-approval"))
    timer_flow = await _start_agent(agent_stack, new_flow_id("ai-agent-kill-timer"))
    input_flow = await _start_agent(agent_stack, new_flow_id("ai-agent-kill-input"))

    await _send(agent_stack, approval_flow, '/tool test__publish {"message":"after restart"}')
    await _send(agent_stack, timer_flow, f"/wait {TIMER_SECONDS} check after restart")
    await _send(agent_stack, input_flow, "/ask What date should I use?", plan_mode=True)
    draft = await _wait_for_description(
        agent_stack,
        input_flow,
        "draft plan",
        # execute_plan accepts a plan only after the turn ends and the agent waits again.
        lambda description: description.plan
        if description.plan is not None
        and description.plan["status"] == "draft"
        and description.status == STATUS_WAITING
        else None,
    )
    assert await client.invoke_rpc(
        agent.execute_plan,
        input_flow,
        PlanExecutionRequest(int(draft["revision"])),
    )
    await _wait_for_description(
        agent_stack,
        approval_flow,
        "tool approval",
        lambda description: description.pending_approval_call_id,
    )
    await _wait_for_description(
        agent_stack,
        timer_flow,
        "durable timer",
        lambda description: description.status == "waiting_for_timer",
    )
    await _wait_for_description(
        agent_stack,
        input_flow,
        "user input",
        lambda description: description.pending_user_input_prompt is not None
        and description.plan is not None
        and description.plan["status"] == "active",
    )
    before = {
        flow_id: await client.invoke_rpc(agent.get_snapshot, flow_id)
        for flow_id in (approval_flow, timer_flow, input_flow)
    }

    worker.kill()
    agent_stack.engine.kill()
    await agent_stack.engine.start()
    await worker.start()

    # The same runs keep their conversation, plan, and pending approval, timer, or question.
    for flow_id, snapshot in before.items():
        assert await _wait_for_snapshot(agent_stack, flow_id) == snapshot

    approval_call_id = before[approval_flow].description.pending_approval_call_id
    assert approval_call_id is not None
    assert await client.invoke_rpc(
        agent.approve_tool,
        approval_flow,
        ToolApprovalRequest(approval_call_id, True),
    )
    await _wait_for_content(agent_stack, approval_flow, "published:after restart")
    await _wait_for_content(agent_stack, timer_flow, '"status": "completed"')
    await _send(agent_stack, input_flow, "September 12")
    await _wait_for_description(
        agent_stack,
        input_flow,
        "completed plan",
        lambda description: description.plan is not None
        and description.plan["status"] == "completed",
    )


async def test_ai_agent_reports_tool_calls_that_a_worker_crash_cut_off(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    worker = agent_stack.new_worker()
    await worker.start()
    read_flow = await _start_agent(agent_stack, new_flow_id("ai-agent-cut-read"))
    write_flow = await _start_agent(agent_stack, new_flow_id("ai-agent-cut-write"))
    await _send(
        agent_stack,
        read_flow,
        '/tool test__slow_lookup {"query":"cut off","seconds":%d}' % SLOW_CALL_SECONDS,
    )
    await _send(
        agent_stack,
        write_flow,
        '/tool test__slow_publish {"message":"cut off","seconds":%d}' % SLOW_CALL_SECONDS,
    )
    call_id = await _wait_for_description(
        agent_stack,
        write_flow,
        "write approval",
        lambda description: description.pending_approval_call_id,
    )
    assert await agent_stack.client.invoke_rpc(
        agent_stack.agent.approve_tool,
        write_flow,
        ToolApprovalRequest(call_id, True),
    )
    for flow_id in (read_flow, write_flow):
        await _wait_for_description(
            agent_stack,
            flow_id,
            "running tool",
            lambda description: description.status == "executing_tool",
        )
    # Give each call time to start its MCP server process.
    await asyncio.sleep(2)

    crashed_at = asyncio.get_running_loop().time()
    worker.kill()
    await worker.start()
    read_result = await _wait_for_tool_result(agent_stack, read_flow)
    write_result = await _wait_for_tool_result(agent_stack, write_flow)
    assert asyncio.get_running_loop().time() - crashed_at < TOOL_CRASH_REPORT_SECONDS

    assert json.loads(read_result) == {
        "status": "interrupted",
        "outcome": "known_failure",
        "retry_safe": True,
    }
    assert json.loads(write_result) == {
        "status": "interrupted",
        "outcome": "outcome_unknown",
        "retry_safe": False,
    }
    for flow_id in (read_flow, write_flow):
        await _send(agent_stack, flow_id, "still running")
        await _wait_for_content(agent_stack, flow_id, "Local demo response: still running")


async def test_ai_agent_returns_to_the_user_when_a_worker_outage_outlasts_a_model_call(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    worker = agent_stack.new_worker()
    await worker.start()
    flow_id = await _start_agent(agent_stack, new_flow_id("ai-agent-model-outage"))
    await _send(agent_stack, flow_id, f"/slow {SLOW_CALL_SECONDS} first reply")
    await _wait_for_description(
        agent_stack,
        flow_id,
        "model call",
        lambda description: description.status == "calling_model",
    )
    await asyncio.sleep(1)

    worker.kill()
    await asyncio.sleep(MODEL_OUTAGE_SECONDS)
    await worker.start()

    failure = await _wait_for_description(
        agent_stack,
        flow_id,
        "model failure",
        lambda description: description.model_failure
        if description.status == "waiting_for_message"
        else None,
    )
    assert isinstance(failure, dict)
    assert failure["operation"] == "The model call"
    assert failure["is_provider_api_error"] is False
    await _send(agent_stack, flow_id, "second reply")
    await _wait_for_content(agent_stack, flow_id, "Local demo response: second reply")
    description = await agent_stack.client.invoke_rpc(agent_stack.agent.describe, flow_id)
    assert description.model_failure is None
    history = await agent_stack.client.invoke_rpc(
        agent_stack.agent.get_history,
        flow_id,
        HistoryRequest(limit=100),
    )
    assert not any(
        "Local demo response: first reply" in item.message.content
        for item in history.messages
    )


async def test_ai_agent_flags_a_model_provider_api_failure(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
) -> None:
    worker = agent_stack.new_worker(
        extra_env={
            "OPENAI_API_KEY": "not-a-real-key",
            "OPENAI_BASE_URL": f"http://127.0.0.1:{available_port()}/v1",
        }
    )
    await worker.start()
    flow_id = await _start_agent(
        agent_stack,
        new_flow_id("ai-agent-model-api-failure"),
        AgentConfig(model="openai/gpt-5-mini"),
    )
    await _send(agent_stack, flow_id, "hello")

    failure = await _wait_for_description(
        agent_stack,
        flow_id,
        "model failure",
        lambda description: description.model_failure
        if description.status == "waiting_for_message"
        else None,
    )
    assert isinstance(failure, dict)
    assert failure["is_provider_api_error"] is True


async def test_ai_agent_waiting_run_follows_code_changed_while_it_waited(
    agent_stack: PrivateAgentStack,
    new_flow_id: Callable[[str], str],
    tmp_path: Path,
) -> None:
    project_dir = _copy_examples_project(tmp_path / "project")
    worker = agent_stack.new_worker(project_dir)
    await worker.start()
    flow_id = await _start_agent(agent_stack, new_flow_id("ai-agent-code-change"))
    await _send(agent_stack, flow_id, '/tool test__publish {"message":"new code"}')
    call_id = await _wait_for_description(
        agent_stack,
        flow_id,
        "tool approval",
        lambda description: description.pending_approval_call_id,
    )
    run_id = (await _wait_for_snapshot(agent_stack, flow_id)).run_id

    # The run started before SaveTaskSummary and TaskSummaries existed.
    worker.kill()
    subprocess.run(["patch", "-p1", "-i", str(CODE_CHANGE_PATCH)], cwd=project_dir, check=True)
    await worker.start()
    assert await agent_stack.client.invoke_rpc(
        agent_stack.agent.approve_tool,
        flow_id,
        ToolApprovalRequest(str(call_id), True),
    )

    async def task_summary() -> object:
        state = await asyncio.to_thread(
            run_dexcli_json,
            [
                dexcli_path(),
                "flow",
                "state",
                flow_id,
                "--server",
                agent_stack.engine.address,
                "--output",
                "json",
            ],
        )
        return next(
            (
                attribute["value"]
                for attribute in state["attributes"]
                if attribute["key"] == f"TaskSummaries/{call_id}"
            ),
            None,
        )

    summary = await wait_until("TaskSummaries entry", task_summary, RESTART_TIMEOUT)
    assert isinstance(summary, dict)
    assert summary["tool_name"] == "test__publish"
    assert summary["summary"] == "Done: published:new code"
    assert (await _wait_for_snapshot(agent_stack, flow_id)).run_id == run_id


def _copy_examples_project(destination: Path) -> Path:
    destination.mkdir()
    shutil.copy2(EXAMPLES_DIR / "main.py", destination)
    ignore = shutil.ignore_patterns("__pycache__", "node_modules")
    for directory in ("dex_examples", "ai-agent"):
        shutil.copytree(EXAMPLES_DIR / directory, destination / directory, ignore=ignore)
    return destination


async def _start_agent(
    stack: PrivateAgentStack,
    flow_id: str,
    config: AgentConfig | None = None,
) -> str:
    await stack.client.start_flow(
        stack.agent,
        flow_id,
        config or AgentConfig(enabled_mcp_servers=["test"]),
        StartFlowOptions(),
    )
    return flow_id


async def _send(
    stack: PrivateAgentStack,
    flow_id: str,
    content: str,
    plan_mode: bool = False,
) -> None:
    assert await stack.client.invoke_rpc(
        stack.agent.send_message,
        flow_id,
        UserMessage(content, plan_mode),
    )


async def _wait_for_description(
    stack: PrivateAgentStack,
    flow_id: str,
    description_name: str,
    select: Callable[[AgentDescription], object],
) -> object:
    async def selected() -> object:
        try:
            description = await stack.client.invoke_rpc(stack.agent.describe, flow_id)
        except DexServiceError:
            return None
        return select(description)

    return await wait_until(
        f"{flow_id} {description_name}",
        selected,
        RESTART_TIMEOUT,
    )


async def _wait_for_snapshot(stack: PrivateAgentStack, flow_id: str) -> AgentSnapshot:
    async def snapshot() -> AgentSnapshot | None:
        try:
            return await stack.client.invoke_rpc(stack.agent.get_snapshot, flow_id)
        except DexServiceError:
            return None

    return await wait_until(f"{flow_id} snapshot", snapshot, RESTART_TIMEOUT)


async def _wait_for_content(
    stack: PrivateAgentStack,
    flow_id: str,
    expected: str,
) -> None:
    async def contains_content() -> bool:
        snapshot = await _wait_for_snapshot(stack, flow_id)
        return any(expected in item.message.content for item in snapshot.messages)

    await wait_until(f"{flow_id} message containing {expected!r}", contains_content, RESTART_TIMEOUT)


async def _wait_for_tool_result(stack: PrivateAgentStack, flow_id: str) -> str:
    async def tool_result() -> str | None:
        snapshot = await _wait_for_snapshot(stack, flow_id)
        return next(
            (
                item.message.content
                for item in snapshot.messages
                if item.message.role == "tool"
            ),
            None,
        )

    return await wait_until(f"{flow_id} tool result", tool_result, RESTART_TIMEOUT)

