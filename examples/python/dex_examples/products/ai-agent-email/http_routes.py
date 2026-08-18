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

from dex import StartFlowOptions
from quart import Blueprint, Response, jsonify, render_template

from dex_examples.app import ExampleApp
from dex_examples.shared.query import optional_query, required_query

# The React bundle and Jinja template still live beside the pre-package sample.
WEB_ROOT = Path(__file__).resolve().parents[3] / "ai-agent-email"
TEMPLATE_DIR = WEB_ROOT / "templates"
STATIC_DIR = WEB_ROOT / "static"

START_OPTIONS = StartFlowOptions(timeout=timedelta(days=1))


def create_ai_agent_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("ai_agent", __name__, url_prefix="/products/ai-agent-email")

    @blueprint.get("/")
    async def index() -> str:
        return await render_template("index.html")

    @blueprint.get("/start")
    async def start() -> str:
        await app_state.client.start_flow(
            app_state.email_agent,
            required_query("workflowId"),
            None,
            START_OPTIONS,
        )
        return "workflow started"

    @blueprint.get("/request")
    async def send_request() -> str:
        accepted = await app_state.client.invoke_rpc(
            app_state.email_agent.send_request,
            required_query("workflowId"),
            required_query("request"),
        )
        return str(accepted)

    @blueprint.get("/describe")
    async def describe() -> Response:
        details = await app_state.client.invoke_rpc(
            app_state.email_agent.describe,
            required_query("workflowId"),
        )
        return jsonify(asdict(details))

    @blueprint.get("/save_draft")
    async def save_draft() -> str:
        await app_state.client.invoke_rpc(
            app_state.email_agent.save_draft,
            required_query("workflowId"),
            optional_query("draft", ""),
        )
        return "saved"

    return blueprint
