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

import asyncio
from typing import Callable

from dex import (
    AsyncClient,
    Context,
    FlowClosingError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    FlowStatus,
    RPCResult,
    RpcLockConflictError,
)
from quart import Blueprint

from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.patterns.deferred_choice.offer_response_flow import (
    OFFER_RESPONSE_WINDOW_CLOSED,
)
from dex_examples.shared.query import required_query

RESPONSE_LOCK_CONFLICT_ATTEMPTS = 5
RESPONSE_LOCK_CONFLICT_BACKOFF_SECONDS = 0.2

OfferResponseRPC = Callable[[Context, str], RPCResult[str]]


def create_deferred_choice_blueprint(app_state: ExampleApp) -> Blueprint:
    blueprint = Blueprint(
        "pattern_deferred_choice", __name__, url_prefix="/patterns/deferred-choice"
    )

    @blueprint.get("/start")
    async def start_offer_response() -> str:
        return await app_state.client.start_flow(
            app_state.offer_response,
            required_query("workflowId"),
            required_query("offerId"),
            start_options(),
        )

    @blueprint.get("/accept")
    async def accept_customer_offer() -> tuple[str, int]:
        return await send_customer_response(
            app_state.client, app_state.offer_response.accept_customer_offer
        )

    @blueprint.get("/reject")
    async def reject_customer_offer() -> tuple[str, int]:
        return await send_customer_response(
            app_state.client, app_state.offer_response.reject_customer_offer
        )

    return blueprint


async def send_customer_response(
    client: AsyncClient, rpc: OfferResponseRPC
) -> tuple[str, int]:
    """Answers 409 when the response window is already closed."""
    flow_id = required_query("workflowId")
    try:
        outcome = await invoke_response_rpc(client, rpc, flow_id, required_query("note"))
    except FlowClosingError:
        return "offer response window closed", 409
    except (FlowNotActiveOrNotFoundError, FlowNotFoundError):
        if await is_offer_closed(client, flow_id):
            return "offer response window closed", 409
        raise
    if outcome == OFFER_RESPONSE_WINDOW_CLOSED:
        return "offer response window closed", 409
    return "offer response recorded", 200


async def is_offer_closed(client: AsyncClient, flow_id: str) -> bool:
    """Returns whether the offer Flow exists and has closed, which also closes its window.

    Raises FlowNotFoundError when the Flow never existed.
    """
    info = await client.describe_flow(flow_id)
    return info.status is not FlowStatus.RUNNING


async def invoke_response_rpc(
    client: AsyncClient, rpc: OfferResponseRPC, flow_id: str, note: str
) -> str:
    """Retries a lock conflict with bounded backoff.

    Retrying is safe: a conflicting call committed nothing.
    Returns the RPC outcome, or raises the last lock conflict.
    """
    for attempt in range(1, RESPONSE_LOCK_CONFLICT_ATTEMPTS):
        try:
            return await client.invoke_rpc(rpc, flow_id, note)
        except RpcLockConflictError:
            await asyncio.sleep(attempt * RESPONSE_LOCK_CONFLICT_BACKOFF_SECONDS)
    return await client.invoke_rpc(rpc, flow_id, note)
