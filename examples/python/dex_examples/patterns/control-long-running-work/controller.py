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
from dex_examples.shared.query import required_int_query, required_query


def create_control_long_running_work_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_control_long_running_work",
        __name__,
        url_prefix="/patterns/control-long-running-work",
    )

    @blueprint.get("/start")
    async def start_record_backfill() -> str:
        return await app_state.client.start_flow(
            app_state.record_backfill,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/set-batch-size")
    async def set_batch_size() -> str:
        await app_state.client.invoke_rpc(
            app_state.record_backfill.set_batch_size,
            required_query("workflowId"),
            required_int_query("batchSize"),
        )
        return "done"

    @blueprint.get("/stop")
    async def request_stop() -> str:
        await app_state.client.invoke_rpc(
            app_state.record_backfill.request_stop,
            required_query("workflowId"),
        )
        return "done"

    return blueprint
