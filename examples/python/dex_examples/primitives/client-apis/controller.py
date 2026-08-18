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

from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import required_query, started_flow


def create_client_apis_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("primitive_client_apis", __name__, url_prefix="/primitives/client-apis")

    @blueprint.get("/start")
    async def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = await app_state.client.start_flow(
            app_state.client_apis,
            flow_id,
            required_query("keyword"),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/search")
    async def search() -> Response:
        page = await app_state.client.search_flows(
            required_query("query"),
            20,
            "",
        )
        return jsonify(
            {
                "flowIDs": [flow.flow_id for flow in page.flows],
                "nextPageToken": page.next_page_token,
            }
        )

    return blueprint
