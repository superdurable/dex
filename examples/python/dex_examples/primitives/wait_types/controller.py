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

from quart import Blueprint, Response

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.primitives.wait_types.wait_types_flow import WaitTypesInput
from dex_examples.shared.query import optional_query, required_query, started_flow


def create_wait_types_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("primitive_wait_types", __name__, url_prefix="/primitives/step/wait-types")

    @blueprint.get("/start")
    async def start() -> Response:
        flow_id = required_query("workflowId")
        timeout_seconds = int(optional_query("timeoutSeconds", "60"))
        run_id = await app_state.client.start_flow(
            app_state.wait_types,
            flow_id,
            WaitTypesInput(
                mode=required_query("mode"),
                timeout_seconds=timeout_seconds,
            ),
            start_options(),
        )
        return started_flow(flow_id, run_id)

    @blueprint.get("/signal-a")
    async def signal_a() -> str:
        await app_state.client.invoke_rpc(
            app_state.wait_types.signal_a,
            required_query("workflowId"),
        )
        return "done"

    @blueprint.get("/signal-b")
    async def signal_b() -> str:
        await app_state.client.invoke_rpc(
            app_state.wait_types.signal_b,
            required_query("workflowId"),
        )
        return "done"

    return blueprint
