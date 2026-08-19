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

from datetime import timedelta

from dex import IdReusePolicy, StartFlowOptions
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.shared.query import required_int_query, required_query

RETRY_PREVIOUS_FAILED = StartFlowOptions(
    timeout=timedelta(hours=1),
    id_reuse_policy=IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED,
)


def create_scalable_parallel_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_scalable_parallel",
        __name__,
        url_prefix="/patterns/scalable-parallel",
    )

    @blueprint.get("/start")
    async def start_scalable_parallel() -> str:
        await app_state.client.start_flow(
            app_state.request_receiver,
            required_query("workflowId"),
            required_int_query("numOfChildWfs"),
            RETRY_PREVIOUS_FAILED,
        )
        return "success"

    return blueprint
