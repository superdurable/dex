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
# See the License for the applicable language governing permissions and
# limitations under the License.

from __future__ import annotations

from datetime import timedelta

from dex_examples.app import ExampleApp
from dex_examples.shared.query import required_int_query, required_query, started_flow
from quart import Blueprint, Response

from dex import (
    AsyncClient,
    FlowConfig,
    FlowTimeoutPolicy,
    IdReusePolicy,
    StartFlowOptions,
    StepDurability,
)

from .example_flow import ExampleFlow, status


def start_flow_options() -> StartFlowOptions:
    return StartFlowOptions(
        timeout=timedelta(hours=1),
        config_override=FlowConfig(step_durability=StepDurability.SYNC),
    )


def example_start_flow_options() -> StartFlowOptions:
    return StartFlowOptions(
        timeout=timedelta(minutes=30),
        timeout_policy=FlowTimeoutPolicy.HANDLER,
        id_reuse_policy=IdReusePolicy.DISALLOW,
        config_override=FlowConfig(step_durability=StepDurability.SYNC),
        ignore_already_started=True,
        request_id="start-order-123",
    ).with_attribute(status, "queued")


async def start_example_flow_request(
    client: AsyncClient, example_flow: ExampleFlow, flow_id: str, input_num: int
) -> str:
    options = StartFlowOptions(
        id_reuse_policy=IdReusePolicy.DISALLOW,
        request_id=f"start-example-flow:{input_num}:{flow_id}",
        ignore_already_started=True,
    ).with_attribute(status, "queued")
    await client.start_flow(example_flow, flow_id, input_num, options)
    return flow_id


def create_flow_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("primitive_flow", __name__, url_prefix="/primitives/flow")

    @blueprint.get("/start")
    async def start() -> Response:
        flow_id = required_query("workflowId")
        run_id = await app_state.client.start_flow(
            app_state.example_flow,
            flow_id,
            required_int_query("inputNum"),
            start_flow_options(),
        )
        return started_flow(flow_id, run_id)

    return blueprint
