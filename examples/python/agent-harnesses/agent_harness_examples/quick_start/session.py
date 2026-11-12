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
from pathlib import Path

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


PULL_REGISTRATION_TIMEOUT_SECONDS = 30.0
# Tool calls run as parallel Steps, so the Worker needs a slot for each one.
MAX_CONCURRENT_TASKS = 16
POLL_INTERVAL_SECONDS = 0.5

ApprovalDecider = Callable[[AgentInterrupt], Awaitable[bool]]


class QuickStartApp:
    """One Registry, Worker, and Client for one durable agent."""

    def __init__(
        self, session_flow: AgentSessionFlow, server_address: str, blob_cache_dir: Path
    ) -> None:
        # Agent session Steps are async, so the Registry allows async handlers.
        self.registry = Registry((session_flow,), allow_async_handlers=True)
        blob_cache_dir.mkdir(parents=True, exist_ok=True)
        self.blob_cache = open_blob_cache(BlobCacheConfig(str(blob_cache_dir), 1 << 30))
        self.worker = AsyncWorker(
            self.registry,
            self.blob_cache,
            WorkerOptions(
                server_address=server_address,
                pull=PullWorkerOptions(max_concurrent_tasks=MAX_CONCURRENT_TASKS),
            ),
        )
        self.client = AsyncClient(
            self.registry, self.blob_cache, ClientOptions(server_address=server_address)
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
