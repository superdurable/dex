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

from quart import Blueprint

from dex import (
    FlowTimeoutHandlerOptions,
    FlowTimeoutPolicy,
    RetryPolicy,
    StartFlowOptions,
)

from dex_examples.app import ExampleApp
from dex_examples.shared.query import optional_query, required_query


def create_timeout_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint("pattern_timeout", __name__, url_prefix="/patterns/timeout")

    @blueprint.get("/start")
    async def start_timeout() -> str:
        flow_id = required_query("workflowId")
        await app_state.client.start_flow(
            app_state.timeout,
            flow_id,
            optional_query("successfulWorkflow", "true").lower() == "true",
            StartFlowOptions(
                timeout=timedelta(minutes=1),
                timeout_policy=FlowTimeoutPolicy.HANDLER,
                timeout_handler_options=FlowTimeoutHandlerOptions(
                    method_timeout=timedelta(seconds=30),
                    retry=RetryPolicy(maximum_attempts=3),
                ),
            ),
        )
        return f"success for workflow {flow_id}"

    return blueprint
