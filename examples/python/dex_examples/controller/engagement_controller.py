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

from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.controller.query import (
    accepted,
    new_flow_id,
    optional_query,
    required_query,
    started_flow,
)
from dex_examples.workflow.engagement.engagement_input import EngagementInput


def create_engagement_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("engagement", __name__, url_prefix="/engagement")

    @blueprint.get("/start")
    async def start() -> Response:
        flow_id = new_flow_id("engagement")
        run_id = await app_state.client.start_flow(
            app_state.engagement,
            flow_id,
            EngagementInput("test-employer-id", "test-job-seeker-id", "test-notes"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/describe")
    async def describe() -> Response:
        description = await app_state.client.invoke_rpc(
            app_state.engagement.describe,
            required_query("workflowId"),
        )
        return jsonify(asdict(description))

    @blueprint.get("/optout")
    async def opt_out() -> Response:
        await app_state.client.publish(
            required_query("workflowId"),
            app_state.engagement.opt_out_reminder,
            None,
        )
        return accepted()

    @blueprint.get("/decline")
    async def decline() -> Response:
        status = await app_state.client.invoke_rpc(
            app_state.engagement.decline,
            required_query("workflowId"),
            optional_query("notes", ""),
        )
        return jsonify(status.value)

    @blueprint.get("/accept")
    async def accept() -> Response:
        status = await app_state.client.invoke_rpc(
            app_state.engagement.accept,
            required_query("workflowId"),
            optional_query("notes", ""),
        )
        return jsonify(status.value)

    return blueprint
