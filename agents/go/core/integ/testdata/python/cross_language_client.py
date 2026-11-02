# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Drive one agent session on a Go Worker with the Python core's Client, and print what it saw.

Usage: cross_language_client.py <server address> <state dir> <session ID>
"""

from __future__ import annotations

import asyncio
import json
import sys
from pathlib import Path

from dex import AsyncClient, BlobCacheConfig, ClientOptions, Registry, open_blob_cache

from dex_agents import AgentSessionClient
from dex_agents.stepwise import AgentDefinition, stepwise_agent_session_flow
from dex_agents.testing import ScriptedModelClient, ScriptedModelReply
from dex_agents.ui_stream import agent_snapshot_to_json

_POLL_SECONDS = 0.1
_TIMEOUT_SECONDS = 90.0


async def wait_for(sessions: AgentSessionClient, session_id: str, description: str, is_done):
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _TIMEOUT_SECONDS
    while loop.time() < deadline:
        snapshot = await sessions.get_agent_snapshot(session_id)
        if is_done(snapshot):
            return snapshot
        await asyncio.sleep(_POLL_SECONDS)
    raise TimeoutError(f"session {session_id} never reached {description}")


async def main(address: str, state_dir: Path, session_id: str) -> None:
    # The Flow only types the Client calls; the Go Worker runs the Steps.
    flow = stepwise_agent_session_flow(
        AgentDefinition(
            name="scripted",
            instructions="unused",
            model=ScriptedModelClient(lambda request: ScriptedModelReply()),
        ),
        flow_type="ScriptedAgent",
    )
    cache = open_blob_cache(BlobCacheConfig(str(state_dir / "python-client-cache"), 64 * 1024 * 1024))
    client = AsyncClient(
        Registry([flow], allow_async_handlers=True), cache, ClientOptions(server_address=address)
    )
    try:
        sessions = AgentSessionClient(client, flow)
        await sessions.start_session(session_id, "lookup and refund")
        waiting = await wait_for(
            sessions, session_id, "a pending approval", lambda s: len(s.pending_interrupts) == 1
        )
        interrupt = waiting.pending_interrupts[0]
        queued = await sessions.send_user_message(session_id, "thanks")
        resolved = await sessions.resolve_interrupt(
            session_id, interrupt.interrupt_id, approved=True
        )
        resolved_again = await sessions.resolve_interrupt(
            session_id, interrupt.interrupt_id, approved=True
        )
        done = await wait_for(
            sessions,
            session_id,
            "two finished turns",
            lambda s: s.state is not None and s.state.turn_count == 2 and s.state.active_turn_id is None,
        )
        page = await sessions.list_agent_messages(session_id, limit=3)
        event_types: list[str] = []
        resume_token = ""
        while "turn_completed" not in event_types or event_types.count("turn_completed") < 2:
            message = await sessions.read_agent_event(session_id, resume_token)
            resume_token = message.resume_token
            event_types.append(message.value.type)
            json.loads(message.value.payload)
        print(
            json.dumps(
                {
                    "waiting_interrupt": {
                        "kind": str(interrupt.kind),
                        "tool_name": interrupt.tool_name,
                        "tool_call_id": interrupt.tool_call_id,
                    },
                    "waiting_tool_statuses": [str(call.status) for call in waiting.tool_calls],
                    "queued": queued,
                    "resolved": resolved,
                    "resolved_again": resolved_again,
                    "snapshot": agent_snapshot_to_json(done),
                    "page_sequences": [message.sequence for message in page.messages],
                    "page_next_before_sequence": page.next_before_sequence,
                    "event_types": event_types,
                }
            )
        )
    finally:
        await client.close()
        cache.close()


if __name__ == "__main__":
    asyncio.run(main(sys.argv[1], Path(sys.argv[2]), sys.argv[3]))
