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

"""The smallest host for a durable agent: one Worker, one Client, one turn."""

from __future__ import annotations

import asyncio

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from dex_agents import AgentSessionClient, AgentSessionFlow, AgentSessionStatus, AgentSnapshot


async def ask_agent(
    session_flow: AgentSessionFlow,
    session_id: str,
    message: str,
    server_address: str = "127.0.0.1:8801",
    blob_cache_dir: str = ".dex-blob-cache",
) -> AgentSnapshot:
    """Starts a session, sends one message, and returns the session after the turn."""
    registry = Registry((session_flow,), allow_async_handlers=True)
    cache = open_blob_cache(BlobCacheConfig(blob_cache_dir, 1 << 30))
    worker = AsyncWorker(registry, cache, WorkerOptions(server_address=server_address))
    client = AsyncClient(registry, cache, ClientOptions(server_address=server_address))
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        sessions = AgentSessionClient(client, session_flow)
        await sessions.start_session(session_id)
        await sessions.send_user_message(session_id, message)
        while True:
            snapshot = await sessions.get_agent_snapshot(session_id)
            state = snapshot.state
            if state and state.turn_count >= 1:
                if state.status == AgentSessionStatus.WAITING_FOR_USER_MESSAGE:
                    return snapshot
            if state and state.status == AgentSessionStatus.FAILED:
                raise RuntimeError(f"the session failed: {state.last_turn_error}")
            await asyncio.sleep(0.5)
    finally:
        await client.close()
        await worker.close()
        await worker_task
        cache.close()
