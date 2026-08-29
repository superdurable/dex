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
from dex_examples.shared.query import required_query


def create_inactiveness_tracker_timer_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_inactiveness_tracker_timer",
        __name__,
        url_prefix="/patterns/inactiveness-tracker-timer",
    )

    @blueprint.get("/start")
    async def start_inactiveness_tracker_timer() -> str:
        return await app_state.client.start_flow(
            app_state.inactiveness_tracker,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/activity")
    async def record_activity() -> str:
        await app_state.client.invoke_rpc(
            app_state.inactiveness_tracker.record_activity,
            required_query("workflowId"),
        )
        return "activity recorded"

    return blueprint
