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

from dex import (
    AsyncClient,
    FlowConfig,
    FlowTimeoutPolicy,
    IdReusePolicy,
    RetryPolicy,
    StartFlowOptions,
    StepDurability,
    WorkerTarget,
)
from quart import Blueprint, Response

from dex_examples.app import ExampleApp
from dex_examples.shared.query import required_int_query, required_query, started_flow

from .example_flow import status


def start_flow_options() -> StartFlowOptions:
    return StartFlowOptions(
        timeout=timedelta(hours=1),
        config_override=FlowConfig(step_durability=StepDurability.SYNC),
    )


def example_start_flow_options() -> StartFlowOptions:
    return StartFlowOptions(
        timeout=timedelta(minutes=30),
        timeout_policy=FlowTimeoutPolicy.HANDLER,
        start_delay=timedelta(minutes=5),
        id_reuse_policy=IdReusePolicy.DISALLOW,
        retry_policy=RetryPolicy(
            initial_interval=timedelta(minutes=1),
            backoff_coefficient=2,
            maximum_interval=timedelta(minutes=10),
            maximum_attempts=3,
        ),
        config_override=FlowConfig(step_durability=StepDurability.SYNC),
        ignore_already_started=True,
        request_id="start-order-123",
    ).with_attribute(status, "queued")


async def reroute_active_flow(client: AsyncClient, flow_id: str) -> None:
    await client.update_flow_config(
        flow_id,
        FlowConfig(worker_target=WorkerTarget("worker-canary:8803")),
    )


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
