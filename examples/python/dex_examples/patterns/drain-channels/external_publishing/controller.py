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

from dex import FlowNotActiveError
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import required_query


def create_draining_channel_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_drain_external",
        __name__,
        url_prefix="/patterns/drain-channels/external-publishing",
    )

    @blueprint.get("/start-or-publish")
    async def start_or_publish_draining_channel() -> str:
        flow_id = required_query("workflowId")
        try:
            await app_state.client.publish(
                flow_id,
                app_state.drain_external.queue_channel,
                "message from start-or-publish endpoint",
            )
        except FlowNotActiveError:
            run_id = await app_state.client.start_flow(
                app_state.drain_external,
                flow_id,
                "first message from start-or-publish",
                start_options(),
            )
            return f"Started the workflow with runId {run_id}"
        return "Published to the Flow"

    return blueprint
