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

import time
from dataclasses import asdict
from datetime import timedelta

from dex import FlowConfig, StartFlowOptions
from flask import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.controller.query import optional_query, required_query
from dex_examples.workflow.jobpost.job_info import JobInfo

SEARCH_MESSAGE = (
    "The Python SDK does not expose SearchFlows yet; Title and JobDescription "
    "are FULL_TEXT AttributeIndexes for when SearchFlows is available."
)


def create_job_post_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("job_post", __name__, url_prefix="/jobpost")

    @blueprint.get("/create")
    def create() -> str:
        flow = app_state.job_post
        flow_id = f"job_id_{int(time.time())}"
        options = (
            StartFlowOptions(
                timeout=timedelta(hours=24),
                config_override=FlowConfig(continue_as_new_threshold=10),
            )
            .with_attribute(flow.title, unquote(required_query("title")))
            .with_attribute(
                flow.job_description,
                unquote(required_query("description")),
            )
            .with_attribute(flow.last_update_time_millis, int(time.time() * 1000))
        )
        app_state.client.start_flow(flow, flow_id, None, options)
        return f"started workflowId: {flow_id}"

    @blueprint.get("/read")
    def read() -> Response:
        job_info = app_state.client.invoke_rpc(
            app_state.job_post.get,
            required_query("workflowId"),
        )
        return jsonify(asdict(job_info))

    @blueprint.get("/update")
    def update() -> str:
        job_info = JobInfo(
            unquote(required_query("title")),
            unquote(required_query("description")),
            unquote(optional_query("notes", "test-notes")),
        )
        app_state.client.invoke_rpc(
            app_state.job_post.update,
            required_query("workflowId"),
            job_info,
        )
        return "updated"

    @blueprint.get("/delete")
    def delete() -> str:
        app_state.client.stop_flow(required_query("workflowId"))
        return "marked as soft deleted, will be delete later after retention"

    @blueprint.get("/search")
    def search() -> Response:
        return jsonify(
            {
                "message": SEARCH_MESSAGE,
                "query": unquote(required_query("query")),
            }
        )

    return blueprint


def unquote(value: str) -> str:
    """Strips surrounding single and then double quotes left over from shell input."""
    stripped = value
    for quote in ("'", '"'):
        if stripped.startswith(quote):
            stripped = stripped[1:-1]
    return stripped
