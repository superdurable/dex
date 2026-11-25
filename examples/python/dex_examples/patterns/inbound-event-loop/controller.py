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

from dataclasses import asdict

from quart import Blueprint, Response, jsonify

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.inbound_event_loop.support_ticket_flow import (
    CURRENT_TRANSCRIPT_CHUNK,
    TRANSCRIPT_CHUNKS,
)
from dex_examples.shared.query import required_query


def create_inbound_event_loop_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_inbound_event_loop",
        __name__,
        url_prefix="/patterns/inbound-event-loop",
    )

    @blueprint.get("/start")
    async def start_support_ticket() -> str:
        return await app_state.client.start_flow(
            app_state.support_ticket,
            required_query("workflowId"),
            "",
            start_options(),
        )

    @blueprint.get("/reply")
    async def send_ticket_reply() -> str:
        await app_state.client.invoke_rpc(
            app_state.support_ticket.send_ticket_reply,
            required_query("workflowId"),
            required_query("text"),
        )
        return "reply accepted"

    @blueprint.get("/resolve")
    async def resolve_ticket() -> str:
        await app_state.client.invoke_rpc(
            app_state.support_ticket.resolve_ticket,
            required_query("workflowId"),
            required_query("reason"),
        )
        return "resolution accepted"

    @blueprint.get("/transcript")
    async def get_ticket_transcript() -> Response:
        current_chunk = TRANSCRIPT_CHUNKS.instance(CURRENT_TRANSCRIPT_CHUNK)
        values = await app_state.client.get_attributes(
            required_query("workflowId"), current_chunk
        )
        chunk = values.get(current_chunk)
        replies = [asdict(reply) for reply in chunk.replies] if chunk else []
        return jsonify({"replies": replies})

    return blueprint
