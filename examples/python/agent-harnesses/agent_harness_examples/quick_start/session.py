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

"""Hosts one framework's quick start agent and runs one turn of its session.

The Worker and the Client live in this process. The Worker runs the session's
Steps. The Client starts the session, sends the message, reads the session,
and answers each approval.
"""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from dex_agents import (
    AgentInterrupt,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSessionStatus,
    MessageRole,
)

from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.demo_model_script import is_demo_model
from agent_harness_examples.google_adk_demo_model import GoogleADKDemoModel
from agent_harness_examples.langchain_demo_model import LangChainDemoChatModel
from agent_harness_examples.openai_agents_demo_model import OpenAIAgentsDemoModel
from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.pydantic_ai_demo_model import build_pydantic_ai_demo_model
from agent_harness_examples.quick_start import (
    claude_agent_sdk_quick_start,
    google_adk_quick_start,
    langchain_quick_start,
    openai_agents_quick_start,
    pydantic_ai_quick_start,
)

PULL_REGISTRATION_TIMEOUT_SECONDS = 30.0
# Tool calls run as parallel Steps, so the Worker needs a slot for each one.
MAX_CONCURRENT_TASKS = 16
POLL_INTERVAL_SECONDS = 0.5

ApprovalDecider = Callable[[AgentInterrupt], Awaitable[bool]]


def build_quick_start_agent(
    framework: str, order_desk: OrderDesk, config: AgentHarnessExamplesConfig
) -> AgentSessionFlow:
    """Returns the durable agent of one framework, on the local demo model unless one is set."""
    if framework == "claude-agent-sdk":
        return claude_agent_sdk_quick_start.build_durable_agent(
            order_desk, config.claude_agent_sdk_model
        )
    if framework == "google-adk":
        model = config.google_adk_model
        return google_adk_quick_start.build_durable_agent(
            order_desk, GoogleADKDemoModel() if is_demo_model(model) else model
        )
    if framework == "langchain":
        model = config.langchain_model
        return langchain_quick_start.build_durable_agent(
            order_desk, LangChainDemoChatModel() if is_demo_model(model) else model
        )
    if framework == "openai-agents":
        model = config.openai_agents_model
        return openai_agents_quick_start.build_durable_agent(
            order_desk, OpenAIAgentsDemoModel() if is_demo_model(model) else model
        )
    if framework == "pydantic-ai":
        model = config.pydantic_ai_model
        return pydantic_ai_quick_start.build_durable_agent(
            order_desk, build_pydantic_ai_demo_model() if is_demo_model(model) else model
        )
    raise ValueError(f"unknown framework {framework!r}; use one of {', '.join(FRAMEWORKS)}")


FRAMEWORKS = ("claude-agent-sdk", "google-adk", "langchain", "openai-agents", "pydantic-ai")


class QuickStartApp:
    """One Registry, Worker, and Client for one durable agent."""

    def __init__(self, session_flow: AgentSessionFlow, config: AgentHarnessExamplesConfig) -> None:
        # Agent session Steps are async, so the Registry allows async handlers.
        self.registry = Registry((session_flow,), allow_async_handlers=True)
        config.blob_cache_dir.mkdir(parents=True, exist_ok=True)
        self.blob_cache = open_blob_cache(BlobCacheConfig(str(config.blob_cache_dir), 1 << 30))
        self.worker = AsyncWorker(
            self.registry,
            self.blob_cache,
            WorkerOptions(
                server_address=config.server_address,
                pull=PullWorkerOptions(max_concurrent_tasks=MAX_CONCURRENT_TASKS),
            ),
        )
        self.client = AsyncClient(
            self.registry, self.blob_cache, ClientOptions(server_address=config.server_address)
        )
        self.sessions = AgentSessionClient(self.client, session_flow)
        self._worker_task: asyncio.Task[None] | None = None

    async def start_worker(self) -> None:
        self._worker_task = asyncio.create_task(self.worker.start())
        await self.worker.wait_pull_registered(timeout=PULL_REGISTRATION_TIMEOUT_SECONDS)

    async def close(self) -> None:
        """Closes the Client, the Worker, and the BlobCache, even when a step fails."""
        try:
            await self.client.close()
        finally:
            try:
                await self.worker.close()
                if self._worker_task is not None:
                    await asyncio.wait_for(self._worker_task, timeout=10)
            finally:
                self.blob_cache.close()


async def run_turn(
    sessions: AgentSessionClient,
    session_id: str,
    message: str | None,
    decide_approval: ApprovalDecider,
) -> list[str]:
    """Sends message, or resumes the active turn when it is None, and returns the replies.

    Each tool call that waits for approval goes to decide_approval once.
    """
    snapshot = await sessions.get_agent_snapshot(session_id)
    finished_turns = snapshot.state.turn_count if snapshot.state else 0
    if message is not None:
        await sessions.send_user_message(session_id, message)
        target_turn = finished_turns + 1
    elif snapshot.state and snapshot.state.active_turn_id:
        target_turn = finished_turns + 1
    else:
        target_turn = finished_turns
    answered: set[str] = set()
    while True:
        snapshot = await sessions.get_agent_snapshot(session_id)
        state = snapshot.state
        for interrupt in snapshot.pending_interrupts:
            if interrupt.interrupt_id in answered:
                continue
            answered.add(interrupt.interrupt_id)
            approved = await decide_approval(interrupt)
            await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=approved)
        if state is not None and state.status == AgentSessionStatus.FAILED:
            raise RuntimeError(f"the session failed: {state.last_turn_error}")
        if (
            state is not None
            and state.turn_count >= target_turn
            and state.status == AgentSessionStatus.WAITING_FOR_USER_MESSAGE
        ):
            turn_id = f"turn-{target_turn}"
            return [
                item.text
                for item in snapshot.messages
                if item.turn_id == turn_id and item.role == MessageRole.ASSISTANT and item.text
            ]
        await asyncio.sleep(POLL_INTERVAL_SECONDS)
