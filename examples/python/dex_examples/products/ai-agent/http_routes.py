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

"""HTTP routes of the AI Agent UI, each one call on AgentSessionClient or dex_agents.ui_stream."""

from __future__ import annotations

import hashlib
import hmac
import uuid
from dataclasses import asdict
from pathlib import Path
from typing import Any

from dex import ChannelMessageNotFoundError, FlowNotActiveOrNotFoundError, RpcLockConflictError
from dex_agents import BUILT_IN_TOOL_DEFINITIONS, AgentSessionClient
from dex_agents.mcp import McpToolListingError, McpToolSource
from dex_agents.stepwise import StepwiseAgentRunner
from dex_agents.ui_stream import AGENT_EVENT_STREAM_HEADERS, InvalidAgentEventResumeTokenError
from dex_agents.ui_stream import read_agent_event_stream_snapshot, stream_agent_event_frames
from quart import Blueprint, Response, jsonify, make_response, render_template, request
from werkzeug.exceptions import BadRequest, Conflict, Forbidden, NotFound

from dex_examples.app import ExampleApp
from dex_examples.shared.query import optional_query, required_query

WEB_ROOT = Path(__file__).resolve().parents[3] / "ai-agent"
TEMPLATE_DIR = WEB_ROOT / "templates"
STATIC_DIR = WEB_ROOT / "static"
SESSION_TOKEN_HEADER = "X-Agent-Session-Token"


def sign_session_id(session_key: bytes, session_id: str) -> str:
    """Returns the session token: an HMAC of the session ID, so no token is stored anywhere."""
    return hmac.new(session_key, session_id.encode(), hashlib.sha256).hexdigest()


def create_ai_agent_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("ai_agent", __name__, url_prefix="/products/ai-agent")

    def sessions() -> AgentSessionClient:
        return AgentSessionClient(app_state.client, app_state.ai_agent)

    def authorize_session(session_id: str) -> str:
        # Every route that names a session checks its token before any Dex call.
        expected = sign_session_id(app_state.config.agent_session_key, session_id)
        if not hmac.compare_digest(request.headers.get(SESSION_TOKEN_HEADER, ""), expected):
            raise Forbidden("this browser has no token for the AI Agent session")
        return session_id

    async def session_request() -> tuple[dict[str, Any], str]:
        body = await request.get_json(silent=True)
        if not isinstance(body, dict):
            raise BadRequest("the request body must be a JSON object")
        return body, authorize_session(_required_string(body, "session_id"))

    @blueprint.errorhandler(FlowNotActiveOrNotFoundError)
    async def report_closed_session(error: FlowNotActiveOrNotFoundError) -> tuple[str, int]:
        return "the AI Agent session is closed or no longer retained; start a new one", 409

    @blueprint.errorhandler(RpcLockConflictError)
    async def report_busy_session(error: RpcLockConflictError) -> tuple[str, int]:
        return "the AI Agent session is busy; try again", 409

    @blueprint.get("/")
    async def index() -> str:
        bundle_version = (STATIC_DIR / "js" / "bundle.js").stat().st_mtime_ns
        return await render_template("index.html", bundle_version=bundle_version)

    @blueprint.get("/settings")
    async def get_worker_settings() -> Response:
        config, runner = app_state.config, app_state.ai_agent.runner
        assert isinstance(runner, StepwiseAgentRunner)
        try:
            sources = runner.definition.tool_sources
            mcp_sources = [source for source in sources if isinstance(source, McpToolSource)]
            mcp_tools = [tool for source in mcp_sources for tool in await source.list_tools()]
        except McpToolListingError:
            mcp_tools = []
        return jsonify(
            model=config.agent_models[0],
            models=list(config.agent_models),
            system_prompt=config.agent_system_prompt,
            context_window_tokens=config.agent_context_tokens,
            mcp_servers=[server.name for server in app_state.ai_agent_mcp_servers],
            mcp_tools=[
                {"name": tool.name, "effect": tool.framework_effect or "unclassified"}
                for tool in mcp_tools
            ],
            built_in_tools=[definition.name for definition in BUILT_IN_TOOL_DEFINITIONS],
        )

    @blueprint.post("/sessions")
    async def start_session() -> Response:
        body = await request.get_json(silent=True)
        if not isinstance(body, dict):
            raise BadRequest("the request body must be a JSON object")
        model = body.get("model", app_state.config.agent_models[0])
        if model not in app_state.config.agent_models:
            raise BadRequest("model must be one of the models in /settings")
        # The server picks the session ID, so a caller cannot claim an existing session.
        session_id = f"ai-agent-{uuid.uuid4()}"
        # Settings carry only the model name; the Worker resolves it, and keys stay on the Worker.
        await sessions().start_session(session_id, session_settings={"model": model})
        session_token = sign_session_id(app_state.config.agent_session_key, session_id)
        return jsonify(session_id=session_id, session_token=session_token)

    @blueprint.get("/snapshot")
    async def get_agent_snapshot() -> Response:
        session_id = authorize_session(required_query("session_id"))
        try:
            snapshot = await read_agent_event_stream_snapshot(
                app_state.client, app_state.ai_agent, session_id
            )
        except FlowNotActiveOrNotFoundError as error:
            raise NotFound("the AI Agent session is missing or no longer retained") from error
        return jsonify(snapshot.to_json())

    @blueprint.get("/history")
    async def list_agent_messages() -> Response:
        session_id = authorize_session(required_query("session_id"))
        before_sequence = optional_query("before_sequence", "")
        if before_sequence and not before_sequence.isdigit():
            raise BadRequest("before_sequence must be a message sequence")
        anchor = int(before_sequence) if before_sequence else None
        page = await sessions().list_agent_messages(session_id, before_sequence=anchor, limit=100)
        messages = [asdict(message) for message in page.messages]
        for message in messages:
            # Only the session's Steps read the framework item, which can hold the instructions.
            message.pop("framework_item")
        return jsonify(messages=messages, next_before_sequence=page.next_before_sequence)

    @blueprint.get("/events")
    async def stream_agent_events() -> Response:
        session_id = authorize_session(required_query("session_id"))
        try:
            await sessions().get_agent_snapshot(session_id)
        except FlowNotActiveOrNotFoundError as error:
            raise NotFound("the AI Agent session is missing or no longer retained") from error
        resume_token = request.headers.get("Last-Event-ID") or request.args.get("resume_token", "")
        try:
            frames = await stream_agent_event_frames(
                app_state.client, app_state.ai_agent, session_id, resume_token=resume_token
            )
        except InvalidAgentEventResumeTokenError as error:
            raise BadRequest("the resume token is not valid; reload the snapshot") from error
        response = await make_response(frames, dict(AGENT_EVENT_STREAM_HEADERS))
        response.timeout = None
        return response

    @blueprint.post("/messages")
    async def send_user_message() -> Response:
        body, session_id = await session_request()
        accepted = await sessions().send_user_message(
            session_id, _required_string(body, "text"), plan_mode=body.get("plan_mode") is True
        )
        return jsonify(accepted=accepted)

    @blueprint.post("/messages/steer")
    async def steer_user_message() -> Response:
        body, session_id = await session_request()
        try:
            accepted = await sessions().steer_user_message(
                session_id, message_id=body.get("message_id"), text=body.get("text")
            )
        except ChannelMessageNotFoundError as error:
            raise Conflict("the queued message was consumed; reload the snapshot") from error
        return jsonify(accepted=accepted)

    @blueprint.post("/messages/delete")
    async def delete_queued_message() -> Response:
        body, session_id = await session_request()
        message_id = _required_string(body, "message_id")
        try:
            accepted = await sessions().delete_queued_message(session_id, message_id)
        except ChannelMessageNotFoundError as error:
            raise Conflict("the queued message was consumed; reload the snapshot") from error
        return jsonify(accepted=accepted)

    @blueprint.post("/turns/cancel")
    async def cancel_turn() -> Response:
        body, session_id = await session_request()
        return jsonify(accepted=await sessions().cancel_turn(session_id, body.get("turn_id")))

    @blueprint.post("/interrupts/resolve")
    async def resolve_interrupt() -> Response:
        body, session_id = await session_request()
        interrupt_id = _required_string(body, "interrupt_id")
        if not isinstance(approved := body.get("approved"), bool):
            raise BadRequest("approved must be a boolean")
        accepted = await sessions().resolve_interrupt(session_id, interrupt_id, approved=approved)
        return jsonify(accepted=accepted)

    @blueprint.post("/questions/answer")
    async def answer_question() -> Response:
        body, session_id = await session_request()
        interrupt_id = _required_string(body, "interrupt_id")
        answer = await sessions().answer_question(
            session_id, interrupt_id, _required_string(body, "answer")
        )
        return jsonify(accepted=answer)

    @blueprint.post("/plans/execute")
    async def execute_plan() -> Response:
        body, session_id = await session_request()
        if not isinstance(revision := body.get("revision"), int):
            raise BadRequest("revision must be an integer")
        return jsonify(accepted=await sessions().execute_plan(session_id, revision))

    return blueprint


def _required_string(body: dict[str, Any], name: str) -> str:
    value = body.get(name)
    if not isinstance(value, str) or not value.strip():
        raise BadRequest(f"{name} must be a non-empty string")
    return value
