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

"""HTTP routes of one agent's sessions, each one call on AgentSessionClient.

The routes are the same for every framework, because the session Flow and
its Client come from Dex Agents, not from the framework:

* POST /products/agent-harnesses/<harness>/sessions starts a session.
* POST .../messages sends a user message.
* POST .../interrupts/resolve approves or rejects a tool call.
* GET .../snapshot?session_id=... reads the session.
"""

from __future__ import annotations

import hashlib
import hmac
import uuid
from typing import Any

from dex import FlowNotActiveOrNotFoundError, RpcLockConflictError
from dex_agents import AgentSessionClient
from dex_agents.ui_stream import read_agent_event_stream_snapshot
from quart import Blueprint, Response, jsonify, request
from werkzeug.exceptions import BadRequest, Forbidden, NotFound

from agent_harness_examples.app import AgentHarnessExample, AgentHarnessExamplesApp

SESSION_TOKEN_HEADER = "X-Agent-Session-Token"


def sign_session_id(session_key: bytes, session_id: str) -> str:
    """Returns the session token: an HMAC of the session ID, so no token is stored anywhere."""
    return hmac.new(session_key, session_id.encode(), hashlib.sha256).hexdigest()


def create_agent_session_blueprint(
    app_state: AgentHarnessExamplesApp, example: AgentHarnessExample
) -> Blueprint:
    harness_name = example.harness_name
    blueprint = Blueprint(
        f"agent_harness_{harness_name.replace('-', '_')}",
        __name__,
        url_prefix=f"/products/agent-harnesses/{harness_name}",
    )
    sessions = AgentSessionClient(app_state.client, example.session_flow)

    def authorize_session(session_id: str) -> str:
        # Every route that names a session checks its token before any Dex call.
        expected = sign_session_id(app_state.config.session_key, session_id)
        if not hmac.compare_digest(request.headers.get(SESSION_TOKEN_HEADER, ""), expected):
            raise Forbidden("this caller has no token for the agent session")
        return session_id

    async def session_request() -> tuple[dict[str, Any], str]:
        body = await request.get_json(silent=True)
        if not isinstance(body, dict):
            raise BadRequest("the request body must be a JSON object")
        return body, authorize_session(_required_string(body, "session_id"))

    @blueprint.errorhandler(FlowNotActiveOrNotFoundError)
    async def report_closed_session(error: FlowNotActiveOrNotFoundError) -> tuple[str, int]:
        return "the agent session is closed or no longer retained; start a new one", 409

    @blueprint.errorhandler(RpcLockConflictError)
    async def report_busy_session(error: RpcLockConflictError) -> tuple[str, int]:
        return "the agent session is busy; try again", 409

    @blueprint.post("/sessions")
    async def start_session() -> Response:
        # The server picks the session ID, so a caller cannot claim an existing session.
        session_id = f"{harness_name}-order-support-{uuid.uuid4()}"
        await sessions.start_session(session_id)
        session_token = sign_session_id(app_state.config.session_key, session_id)
        return jsonify(session_id=session_id, session_token=session_token)

    @blueprint.post("/messages")
    async def send_user_message() -> Response:
        body, session_id = await session_request()
        accepted = await sessions.send_user_message(session_id, _required_string(body, "text"))
        return jsonify(accepted=accepted)

    @blueprint.post("/interrupts/resolve")
    async def resolve_interrupt() -> Response:
        body, session_id = await session_request()
        interrupt_id = _required_string(body, "interrupt_id")
        if not isinstance(approved := body.get("approved"), bool):
            raise BadRequest("approved must be a boolean")
        rejection_message = body.get("rejection_message")
        if rejection_message is not None and not isinstance(rejection_message, str):
            raise BadRequest("rejection_message must be a string")
        accepted = await sessions.resolve_interrupt(
            session_id, interrupt_id, approved=approved, rejection_message=rejection_message
        )
        return jsonify(accepted=accepted)

    @blueprint.get("/snapshot")
    async def get_agent_snapshot() -> Response:
        session_id = request.args.get("session_id", "")
        if not session_id:
            raise BadRequest("session_id is required")
        authorize_session(session_id)
        try:
            snapshot = await read_agent_event_stream_snapshot(
                app_state.client, example.session_flow, session_id
            )
        except FlowNotActiveOrNotFoundError as error:
            raise NotFound("the agent session is missing or no longer retained") from error
        return jsonify(snapshot.to_json())

    return blueprint


def _required_string(body: dict[str, Any], name: str) -> str:
    value = body.get(name)
    if not isinstance(value, str) or not value.strip():
        raise BadRequest(f"{name} must be a non-empty string")
    return value
