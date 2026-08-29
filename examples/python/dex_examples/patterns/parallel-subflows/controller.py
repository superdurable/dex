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

from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.parallel_subflows.flows import (
    ParentInput,
    SubmitRequestInput,
)
from dex_examples.shared.query import required_query


def create_parallel_subflows_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_parallel_subflows",
        __name__,
        url_prefix="/patterns/parallel-subflows",
    )

    @blueprint.get("/start/basic")
    async def start_basic() -> str:
        return await app_state.client.start_flow(
            app_state.basic_subflows,
            required_query("workflowId"),
            ["one", "two", "three", "four"],
            start_options(),
        )

    async def start_parent(flow: object) -> str:
        return await app_state.client.start_flow(
            flow,  # type: ignore[arg-type]
            required_query("workflowId"),
            ParentInput(["one", "two", "three"], 3),
            start_options(),
        )

    @blueprint.get("/start/long-lived-parent")
    async def start_long_live() -> str:
        return await start_parent(app_state.long_live_subflows)

    @blueprint.get("/start/short-lived-parent")
    async def start_short_live() -> str:
        return await start_parent(app_state.short_live_subflows)

    @blueprint.get("/start/submit")
    async def start_submit() -> str:
        return await app_state.client.start_flow(
            app_state.submit_subflow_request,
            required_query("workflowId"),
            SubmitRequestInput("one", ["parallel-parent-0", "parallel-parent-1"]),
            start_options(),
        )

    return blueprint
