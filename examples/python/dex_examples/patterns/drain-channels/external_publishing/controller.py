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
    RPCInvokeOptions,
    StartFlowOptions,
)
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.shared.query import optional_query, required_query

# Bounds how often a message that lost a start race is published again.
START_OR_PUBLISH_ATTEMPTS = 3


def pattern_start_options(message_id: str) -> StartFlowOptions:
    """Restarts a closed Flow ID.

    A retried start returns the original run while that run is still the latest
    run for the Flow ID and its start request ID matches.
    """
    return replace(
        start_options(),
        id_reuse_policy=IdReusePolicy.ALLOW_IF_NOT_RUNNING,
        request_id=f"start:{message_id}",
        ignore_already_started=True,
    )


def create_draining_channel_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_drain_external",
        __name__,
        url_prefix="/patterns/drain-channels/external-publishing",
    )

    @blueprint.get("/start-or-publish")
    async def start_or_publish_draining_channel() -> tuple[str, int]:
        flow_id = required_query("workflowId")
        message_id = optional_query("messageId", "") or str(uuid.uuid4())
        message = f"message {message_id}"
        for _ in range(START_OR_PUBLISH_ATTEMPTS):
            try:
                run_id = await app_state.client.start_flow(
                    app_state.drain_external,
                    flow_id,
                    message,
                    pattern_start_options(message_id),
                )
                return (
                    f"Accepted message {message_id} as the start input of runId {run_id}",
                    200,
                )
            except FlowAlreadyStartedError:
                # A different start request owns the active run: publish this message to that run.
                pass
            try:
                await app_state.client.invoke_rpc(
                    app_state.drain_external.publish_external_channel_message,
                    flow_id,
                    message,
                    options=RPCInvokeOptions(request_id=f"publish:{message_id}"),
                )
                return f"Accepted message {message_id}", 200
            except FlowNotActiveOrNotFoundError:
                # That run closed before the publish: start again.
                continue
        return (
            f"message {message_id} is unconfirmed; send it again with the same messageId",
            409,
        )

    return blueprint
