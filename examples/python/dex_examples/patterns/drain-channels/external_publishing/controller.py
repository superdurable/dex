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

import uuid
from dataclasses import replace

from dex import (
    FlowAlreadyStartedError,
    FlowNotActiveOrNotFoundError,
    IdReusePolicy,
    StartFlowOptions,
)
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import optional_query, required_query

# Bounds how often a message that lost a start race is published again.
START_OR_PUBLISH_ATTEMPTS = 3


def pattern_start_options(message_id: str) -> StartFlowOptions:
    """Restarts the Flow ID once its run closed, with one start per message."""
    return replace(
        start_options(),
        id_reuse_policy=IdReusePolicy.ALLOW_IF_NOT_RUNNING,
        request_id=f"start:{message_id}",
    )


def create_draining_channel_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_drain_external",
        __name__,
        url_prefix="/patterns/drain-channels/external-publishing",
    )

    @blueprint.get("/start-or-publish")
    async def start_or_publish_draining_channel() -> str:
        flow_id = required_query("workflowId")
        message_id = optional_query("messageId", "") or str(uuid.uuid4())
        message = f"message {message_id}"
        for _ in range(START_OR_PUBLISH_ATTEMPTS):
            try:
                await app_state.client.invoke_rpc(
                    app_state.drain_external.publish_external_channel_message,
                    flow_id,
                    message,
                )
                return "Published to the Flow"
            except FlowNotActiveOrNotFoundError:
                pass
            try:
                run_id = await app_state.client.start_flow(
                    app_state.drain_external,
                    flow_id,
                    message,
                    pattern_start_options(message_id),
                )
                return f"Started the workflow with runId {run_id}"
            except FlowAlreadyStartedError:
                # Another caller started the run first: publish this message to that run.
                continue
        raise RuntimeError(f"message {message_id} is unconfirmed: no run accepted it")

    return blueprint
