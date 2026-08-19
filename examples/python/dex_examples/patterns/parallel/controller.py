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
from dex_examples.patterns.parallel.job_seeker import JobSeeker
from dex_examples.shared.query import required_int_query, required_query


def create_parallel_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("pattern_parallel", __name__, url_prefix="/patterns/parallel")

    @blueprint.get("/start/simple")
    async def start_simple_parallel() -> str:
        return await app_state.client.start_flow(
            app_state.simple_parallel,
            required_query("workflowId"),
            JobSeeker("123", "jobseeker@indeed.com", "0987654321"),
            start_options(),
        )

    @blueprint.get("/start/withAwait")
    async def start_parallel_with_await() -> str:
        return await app_state.client.start_flow(
            app_state.parallel_with_await,
            required_query("workflowId"),
            50,
            start_options(),
        )

    return blueprint
