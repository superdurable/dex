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

from flask import Blueprint, Response

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.controller.query import (
    accepted,
    required_query,
    started_flow,
)


def create_microservice_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("microservice", __name__, url_prefix="/microservice")

    @blueprint.get("/start")
    def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = app_state.client.start_flow(
            app_state.orchestration,
            flow_id,
            "test initial data",
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/swap")
    def swap() -> str:
        return app_state.client.invoke_rpc(
            app_state.orchestration.swap,
            required_query("workflowId"),
            required_query("data"),
        )

    @blueprint.get("/signal")
    def signal() -> Response:
        app_state.client.publish(
            required_query("workflowId"),
            app_state.orchestration.ready,
            None,
        )
        return accepted()

    return blueprint
