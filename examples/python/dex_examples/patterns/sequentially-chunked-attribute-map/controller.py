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

from dex import RPCInvokeOptions, StartFlowOptions
from quart import Blueprint, Response, abort, jsonify, request

from dex_examples.app import ExampleApp
from dex_examples.patterns.sequentially_chunked_attribute_map.chunked_subscriber_flow import (
    CURRENT_CHUNK_INSTANCE,
    FLOW_ID,
    GetSubscriberPageInput,
    RegisterSubscriberInput,
    SubscriberArchiveState,
    SubscriberChunk,
    validate_subscriber_page_token,
)


def create_sequentially_chunked_attribute_map_blueprint(
    app_state: ExampleApp,
) -> Blueprint:
    blueprint = Blueprint(
        "pattern_sequentially_chunked_attribute_map",
        __name__,
        url_prefix="/patterns/sequentially-chunked-attribute-map",
    )

    @blueprint.post("/start")
    async def start() -> Response:
        flow = app_state.chunked_subscriber
        options = (
            StartFlowOptions(ignore_already_started=True)
            .with_attribute(
                flow.archive_state,
                SubscriberArchiveState(next_sequence=1),
            )
            .with_attribute(
                flow.subscriber_chunks,
                CURRENT_CHUNK_INSTANCE,
                SubscriberChunk(subscribers=[]),
            )
        )
        run_id = await app_state.client.start_flow(flow, FLOW_ID, None, options)
        return jsonify({"flowID": FLOW_ID, "runID": run_id})

    @blueprint.post("/register")
    async def register_subscriber() -> Response:
        body = await request.get_json(silent=True)
        if not isinstance(body, dict):
            abort(400, description="JSON request body is required")
        subscriber_id = body.get("subscriberId")
        delivery_address = body.get("deliveryAddress")
        if not isinstance(subscriber_id, str) or not subscriber_id:
            abort(400, description="subscriberId is required")
        if not isinstance(delivery_address, str) or not delivery_address:
            abort(400, description="deliveryAddress is required")
        subscriber = await app_state.client.invoke_rpc(
            app_state.chunked_subscriber.register_subscriber,
            FLOW_ID,
            RegisterSubscriberInput(subscriber_id, delivery_address),
        )
        return jsonify(_subscriber_json(asdict(subscriber)))

    @blueprint.get("/subscribers")
    async def get_subscriber_page() -> Response:
        try:
            page_token = validate_subscriber_page_token(
                request.args.get("pageToken", "")
            )
        except ValueError as error:
            abort(400, description=str(error))
        flow = app_state.chunked_subscriber
        page = await app_state.client.invoke_rpc(
            flow.get_subscriber_page,
            FLOW_ID,
            GetSubscriberPageInput(page_token),
            options=RPCInvokeOptions(
                load_attribute_map_instances=(flow.subscriber_chunks.load(page_token),)
            ),
        )
        return jsonify(
            {
                "pageToken": page.page_token,
                "subscribers": [
                    _subscriber_json(asdict(subscriber))
                    for subscriber in page.subscribers
                ],
                "nextPageToken": page.next_page_token,
            }
        )

    return blueprint


def _subscriber_json(subscriber: dict[str, object]) -> dict[str, object]:
    return {
        "sequence": subscriber["sequence"],
        "subscriberId": subscriber["subscriber_id"],
        "deliveryAddress": subscriber["delivery_address"],
    }
