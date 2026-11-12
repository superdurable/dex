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

"""A complete web app for a durable agent: start, message, read, stream, approve.

It hosts the Worker and the Client in the web process. Add your own
authentication before you expose these routes: anyone who knows a session ID
can read and change that session here.
"""

from __future__ import annotations

import uuid
from pathlib import Path
from typing import Any

from dex_agents import AgentSessionFlow
from dex_agents.ui_stream import (
    AGENT_EVENT_STREAM_HEADERS,
    agent_snapshot_to_json,
    stream_agent_event_frames,
)
from quart import Quart, Response, jsonify, make_response, request

from agent_harness_examples.quick_start.safe_send import send_user_message_once
from agent_harness_examples.quick_start.session import QuickStartApp


def create_web_app(
    session_flow: AgentSessionFlow, server_address: str, blob_cache_dir: Path
) -> Quart:
    """Returns a Quart app whose routes run one durable agent's sessions."""
    web = Quart(__name__)
    apps: list[QuickStartApp] = []

    def dex() -> QuickStartApp:
        return apps[0]

    # The Worker and the Client bind to the event loop that serves the app.
    @web.before_serving
    async def start_worker() -> None:
        apps.append(QuickStartApp(session_flow, server_address, blob_cache_dir))
        await dex().start_worker()

    @web.after_serving
    async def stop_worker() -> None:
        await apps.pop().close()

    @web.post("/sessions")
    async def start_session() -> Response:
        session_id = f"web-{uuid.uuid4().hex}"
        await dex().sessions.start_session(session_id)
        return jsonify(session_id=session_id)

    @web.post("/sessions/<session_id>/messages")
    async def send_message(session_id: str) -> Response:
        body: dict[str, Any] = await request.get_json()
        await send_user_message_once(dex().sessions, session_id, str(body["text"]))
        return jsonify(accepted=True)

    @web.get("/sessions/<session_id>")
    async def read_session(session_id: str) -> Response:
        return jsonify(agent_snapshot_to_json(await dex().sessions.get_agent_snapshot(session_id)))

    @web.get("/sessions/<session_id>/events")
    async def stream_events(session_id: str) -> Response:
        resume_token = request.headers.get("Last-Event-ID", "")
        frames = await stream_agent_event_frames(
            dex().client, session_flow, session_id, resume_token=resume_token
        )
        response = await make_response(frames, dict(AGENT_EVENT_STREAM_HEADERS))
        assert isinstance(response, Response)
        response.timeout = None
        return response

    @web.post("/sessions/<session_id>/interrupts/<interrupt_id>")
    async def resolve(session_id: str, interrupt_id: str) -> Response:
        body: dict[str, Any] = await request.get_json()
        accepted = await dex().sessions.resolve_interrupt(
            session_id, interrupt_id, approved=body.get("approved") is True
        )
        return jsonify(accepted=accepted)

    return web
