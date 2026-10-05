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

from quart import Blueprint, abort

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import required_query


def create_polling_pattern_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("pattern_polling", __name__, url_prefix="/patterns/polling")

    @blueprint.get("/start")
    async def start_polling() -> str:
        return await app_state.client.start_flow(
            app_state.polling,
            required_query("workflowId"),
            None,
            start_options(),
        )

    @blueprint.get("/complete-job")
    async def complete_job() -> str:
        try:
            app_state.polling_jobs.complete_job(required_query("workflowId"))
        except LookupError as error:
            abort(404, description=str(error))
        return "done"

    return blueprint
