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

from __future__ import annotations

from dataclasses import asdict
from datetime import timedelta
from pathlib import Path
from typing import Any, TypedDict

from dex import StartFlowOptions
from quart import Blueprint, Response, jsonify, render_template, request
from werkzeug.exceptions import BadRequest, Conflict

from dex_examples.app import ExampleApp
from dex_examples.products.ai_agent.models import (
    AgentConfig,
    HistoryRequest,
    PlanExecutionRequest,
    ToolApprovalRequest,
    UserMessage,
)
from dex_examples.shared.query import optional_query, required_query

WEB_ROOT = Path(__file__).resolve().parents[3] / "ai-agent"
TEMPLATE_DIR = WEB_ROOT / "templates"
STATIC_DIR = WEB_ROOT / "static"

class ProviderConfig(TypedDict):
    label: str
    prefix: str
    defaultModel: str
    environmentVariable: str | None


PROVIDERS: dict[str, ProviderConfig] = {
    "mock": {
        "label": "Local mock",
        "prefix": "",
        "defaultModel": "mock/dex",
        "environmentVariable": None,
    },
    "openai": {
        "label": "OpenAI",
        "prefix": "openai",
        "defaultModel": "gpt-5-mini",
        "environmentVariable": "OPENAI_API_KEY",
    },
    "anthropic": {
        "label": "Anthropic",
        "prefix": "anthropic",
        "defaultModel": "claude-sonnet-4-5",
        "environmentVariable": "ANTHROPIC_API_KEY",
    },
    "gemini": {
        "label": "Google Gemini",
        "prefix": "gemini",
        "defaultModel": "gemini-2.5-flash",
        "environmentVariable": "GEMINI_API_KEY",
    },
    "groq": {
        "label": "Groq",
        "prefix": "groq",
        "defaultModel": "llama-3.3-70b-versatile",
        "environmentVariable": "GROQ_API_KEY",
    },
    "custom": {
        "label": "Other LiteLLM provider",
        "prefix": "",
        "defaultModel": "",
        "environmentVariable": None,
    },
}


def create_ai_agent_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("ai_agent", __name__, url_prefix="/products/ai-agent")

    @blueprint.get("/")
    async def index() -> str:
        bundle_version = (STATIC_DIR / "js" / "bundle.js").stat().st_mtime_ns
        return await render_template("index.html", bundle_version=bundle_version)

    @blueprint.get("/portal")
    async def portal() -> Response:
        registered_tools = {
            tool.definition.name: tool for tool in app_state.mcp_registry.registered_tools
        }
        tools = []
        for definition in app_state.mcp_registry.definitions([], []):
            registered = registered_tools.get(definition.name)
            tools.append(
                {
                    "name": definition.name,
                    "description": definition.description,
                    "requiresApproval": definition.requires_approval,
                    "server": registered.server_name if registered else None,
                }
            )
        return jsonify(
            providers=[
                {"id": provider_id, **provider}
                for provider_id, provider in PROVIDERS.items()
            ],
            mcpServers=app_state.mcp_registry.server_names,
            tools=tools,
            builtInTools=["write_todos", "request_user_input", "durable_wait"],
        )

    @blueprint.post("/start")
    async def start() -> Response:
        payload = await _json_object()
        flow_id = _required_string(payload, "workflowId")
        provider = _optional_string(payload, "provider", "mock")
        config = AgentConfig(
            model=_provider_model(
                provider,
                _optional_string(payload, "model", app_state.config.agent_model),
            ),
            system_prompt=_optional_string(
                payload,
                "systemPrompt",
                app_state.config.agent_system_prompt,
            ),
            compaction_model=_optional_nullable_string(payload, "compactionModel"),
            max_context_tokens=_optional_int(
                payload,
                "maxContextTokens",
                app_state.config.agent_context_tokens,
            ),
            compaction_trigger_fraction=_optional_float(
                payload,
                "compactionTriggerFraction",
                0.85,
            ),
            compaction_keep_fraction=_optional_float(
                payload,
                "compactionKeepFraction",
                0.10,
            ),
            message_retention_limit=_optional_int(
                payload,
                "messageRetentionLimit",
                app_state.config.agent_message_retention,
            ),
            mcp_enabled=_optional_bool(payload, "mcpEnabled", True),
            enabled_mcp_servers=_string_list(payload, "enabledMcpServers"),
            enabled_tools=_string_list(payload, "enabledTools"),
        )
        config.validate()
        api_key = _optional_nullable_string(payload, "apiKey")
        try:
            app_state.ai_agent_credentials.set_api_key(flow_id, api_key)
        except ValueError as error:
            raise BadRequest(str(error)) from error
        try:
            await app_state.client.start_flow(
                app_state.ai_agent,
                flow_id,
                config,
                StartFlowOptions(),
            )
        except Exception:
            app_state.ai_agent_credentials.set_api_key(flow_id, None)
            raise
        return jsonify(workflow_id=flow_id)

    @blueprint.post("/messages")
    async def send_message() -> Response:
        payload = await _json_object()
        accepted = await app_state.client.invoke_rpc(
            app_state.ai_agent.send_message,
            _required_string(payload, "workflowId"),
            UserMessage(
                _required_string(payload, "content"),
                _optional_bool(payload, "planMode", False),
            ),
        )
        return jsonify(accepted=accepted)

    @blueprint.post("/plans/execute")
    async def execute_plan() -> Response:
        payload = await _json_object()
        accepted = await app_state.client.invoke_rpc(
            app_state.ai_agent.execute_plan,
            _required_string(payload, "workflowId"),
            PlanExecutionRequest(_required_int(payload, "revision")),
        )
        if not accepted:
            raise Conflict("the plan revision is stale or cannot be executed")
        return jsonify(accepted=True)

    @blueprint.post("/tool-approvals")
    async def approve_tool() -> Response:
        payload = await _json_object()
        approved = payload.get("approved")
        if not isinstance(approved, bool):
            raise BadRequest("approved must be a boolean")
        accepted = await app_state.client.invoke_rpc(
            app_state.ai_agent.approve_tool,
            _required_string(payload, "workflowId"),
            ToolApprovalRequest(
                _required_string(payload, "callId"),
                approved,
            ),
        )
        return jsonify(accepted=accepted)

    @blueprint.get("/history")
    async def history() -> Response:
        before_text = optional_query("before", "")
        page = await app_state.client.invoke_rpc(
            app_state.ai_agent.history,
            required_query("workflowId"),
            HistoryRequest(
                before_sequence=int(before_text) if before_text else None,
                limit=int(optional_query("limit", "50")),
            ),
        )
        return jsonify(asdict(page))

    @blueprint.get("/events")
    async def events() -> Response:
        stream_name = optional_query("stream", "activity")
        if stream_name == "reasoning":
            message = await app_state.client.read_stream(
                required_query("workflowId"),
                app_state.ai_agent.reasoning_summary,
                optional_query("resumeToken", ""),
                timedelta(seconds=20),
            )
            return jsonify(
                kind="reasoning_summary",
                value=message.value,
                resume_token=message.resume_token,
                created_time=message.created_time.isoformat(),
                source=message.source,
            )
        if stream_name == "assistant":
            message = await app_state.client.read_stream(
                required_query("workflowId"),
                app_state.ai_agent.assistant_text,
                optional_query("resumeToken", ""),
                timedelta(seconds=20),
            )
            return jsonify(
                kind="assistant_text",
                value=message.value,
                resume_token=message.resume_token,
                created_time=message.created_time.isoformat(),
                source=message.source,
            )
        if stream_name != "activity":
            raise BadRequest("stream must be reasoning, assistant, or activity")
        message = await app_state.client.read_stream(
            required_query("workflowId"),
            app_state.ai_agent.agent_activity,
            optional_query("resumeToken", ""),
            timedelta(seconds=20),
        )
        return jsonify(
            kind="activity",
            value=asdict(message.value),
            resume_token=message.resume_token,
            created_time=message.created_time.isoformat(),
            source=message.source,
        )

    @blueprint.get("/describe")
    async def describe() -> Response:
        details = await app_state.client.invoke_rpc(
            app_state.ai_agent.describe,
            required_query("workflowId"),
        )
        return jsonify(asdict(details))

    return blueprint


async def _json_object() -> dict[str, Any]:
    payload = await request.get_json(silent=True)
    if not isinstance(payload, dict):
        raise BadRequest("request body must be a JSON object")
    return payload


def _required_string(payload: dict[str, Any], name: str) -> str:
    value = payload.get(name)
    if not isinstance(value, str) or not value.strip():
        raise BadRequest(f"{name} must be a non-empty string")
    return value


def _optional_string(payload: dict[str, Any], name: str, default: str) -> str:
    value = payload.get(name, default)
    if not isinstance(value, str):
        raise BadRequest(f"{name} must be a string")
    return value


def _optional_nullable_string(
    payload: dict[str, Any],
    name: str,
) -> str | None:
    value = payload.get(name)
    if value is None:
        return None
    if not isinstance(value, str):
        raise BadRequest(f"{name} must be a string")
    return value or None


def _optional_int(payload: dict[str, Any], name: str, default: int) -> int:
    value = payload.get(name, default)
    if isinstance(value, bool) or not isinstance(value, int):
        raise BadRequest(f"{name} must be an integer")
    return value


def _required_int(payload: dict[str, Any], name: str) -> int:
    value = payload.get(name)
    if isinstance(value, bool) or not isinstance(value, int):
        raise BadRequest(f"{name} must be an integer")
    return value


def _optional_bool(payload: dict[str, Any], name: str, default: bool) -> bool:
    value = payload.get(name, default)
    if not isinstance(value, bool):
        raise BadRequest(f"{name} must be a boolean")
    return value


def _optional_float(payload: dict[str, Any], name: str, default: float) -> float:
    value = payload.get(name, default)
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise BadRequest(f"{name} must be a number")
    return float(value)


def _string_list(payload: dict[str, Any], name: str) -> list[str]:
    value = payload.get(name, [])
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        raise BadRequest(f"{name} must be a list of strings")
    return value


def _provider_model(provider: str, model: str) -> str:
    normalized_provider = provider.strip().lower()
    try:
        provider_config = PROVIDERS[normalized_provider]
    except KeyError as error:
        raise BadRequest(f"unknown provider {provider!r}") from error
    if normalized_provider == "mock":
        return "mock/dex"
    normalized_model = model.strip()
    if not normalized_model:
        raise BadRequest("model must be a non-empty string")
    prefix = provider_config["prefix"]
    if not prefix:
        return normalized_model
    expected_prefix = f"{prefix}/"
    if "/" in normalized_model and not normalized_model.startswith(expected_prefix):
        raise BadRequest(
            f"model for {normalized_provider} must start with {expected_prefix}"
        )
    if normalized_model.startswith(expected_prefix):
        return normalized_model
    return f"{expected_prefix}{normalized_model}"
