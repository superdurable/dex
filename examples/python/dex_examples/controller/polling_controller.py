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

from flask import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.controller.query import (
    accepted,
    required_int_query,
    required_query,
    started_flow,
)
from dex_examples.workflow.polling.polling_flow import (
    TASK_A_COMPLETED,
    TASK_B_COMPLETED,
)


def create_polling_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("polling", __name__, url_prefix="/polling")

    @blueprint.get("/start")
    def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = app_state.client.start_flow(
            app_state.polling,
            flow_id,
            required_int_query("pollingCompletionThreshold"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/complete")
    def complete() -> Response | tuple[Response, int]:
        flow_id = required_query("workflowId")
        channel_name = required_query("channel")
        if channel_name == TASK_A_COMPLETED:
            channel = app_state.polling.task_a_completed
        elif channel_name == TASK_B_COMPLETED:
            channel = app_state.polling.task_b_completed
        else:
            return jsonify({"error": "channel must identify task A or task B"}), 400
        app_state.client.publish(flow_id, channel, None)
        return accepted()

    return blueprint
