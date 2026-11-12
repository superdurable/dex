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

"""A durable OpenAI Agents SDK agent in one file.

Run Dex with `dexcli dev`, set OPENAI_API_KEY, then run:

    python agent.py "Cancel order 1002, it was a mistake"

The Worker and the Client run in this process. Each tool call that needs
approval asks on the terminal.
"""

from __future__ import annotations

import asyncio
import os
import sys
import uuid
from collections.abc import Awaitable, Callable

from agents import Agent, Model, function_tool
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from dex_agents import (
    AgentInterrupt,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSessionStatus,
    MessageRole,
    ToolEffect,
    ToolPolicy,
)
from dex_agents_openai_agents import openai_agents_session_flow

SERVER_ADDRESS = os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801")
MODEL = os.environ.get("AGENT_MODEL", "gpt-5.2")

ORDERS = {
    "1001": {"item": "desk lamp", "status": "shipped"},
    "1002": {"item": "office chair", "status": "processing"},
}


# The plain agent: your OpenAI Agents SDK code, unchanged by Dex.
@function_tool
def look_up_order(order_id: str) -> str:
    """Look up one order by its order ID and return its item and status."""
    order = ORDERS.get(order_id)
    return f"order {order_id}: {order}" if order else f"order {order_id} not found"


@function_tool
def cancel_order(order_id: str, reason: str) -> str:
    """Cancel one order that has not shipped yet."""
    order = ORDERS.get(order_id)
    if order is None or order["status"] == "shipped":
        return f"order {order_id} cannot be cancelled"
    order["status"] = "cancelled"
    return f"order {order_id} cancelled: {reason}"


def build_agent(model: str | Model) -> Agent:
    return Agent(
        name="OrderSupport",
        instructions="Help with orders. Look an order up before you change it.",
        model=model,
        tools=[look_up_order, cancel_order],
    )


# The Dex change: wrap the agent and give each tool a policy.
def build_session_flow(model: str | Model) -> AgentSessionFlow:
    return openai_agents_session_flow(
        build_agent(model),
        flow_type="AgentStarter",
        tool_policies={
            "look_up_order": ToolPolicy(ToolEffect.READ),
            "cancel_order": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        },
    )


ApprovalDecider = Callable[[AgentInterrupt], Awaitable[bool]]


# The host: one Worker and one Client in this process.
async def ask(
    session_flow: AgentSessionFlow,
    message: str,
    decide_approval: ApprovalDecider,
    session_id: str | None = None,
) -> list[str]:
    """Starts a session, sends message, answers approvals, and returns the replies."""
    registry = Registry((session_flow,), allow_async_handlers=True)
    cache = open_blob_cache(BlobCacheConfig(".dex-blob-cache", 1 << 30))
    worker = AsyncWorker(registry, cache, WorkerOptions(server_address=SERVER_ADDRESS))
    client = AsyncClient(registry, cache, ClientOptions(server_address=SERVER_ADDRESS))
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        sessions = AgentSessionClient(client, session_flow)
        session_id = session_id or f"agent-starter-{uuid.uuid4().hex[:8]}"
        await sessions.start_session(session_id)
        await sessions.send_user_message(session_id, message)
        answered: set[str] = set()
        while True:
            snapshot = await sessions.get_agent_snapshot(session_id)
            for interrupt in snapshot.pending_interrupts:
                if interrupt.interrupt_id not in answered:
                    answered.add(interrupt.interrupt_id)
                    approved = await decide_approval(interrupt)
                    await sessions.resolve_interrupt(
                        session_id, interrupt.interrupt_id, approved=approved
                    )
            state = snapshot.state
            if state and state.status == AgentSessionStatus.FAILED:
                raise RuntimeError(f"the session failed: {state.last_turn_error}")
            if (
                state
                and state.turn_count >= 1
                and state.status == AgentSessionStatus.WAITING_FOR_USER_MESSAGE
            ):
                return [
                    item.text
                    for item in snapshot.messages
                    if item.role == MessageRole.ASSISTANT and item.text
                ]
            await asyncio.sleep(0.5)
    finally:
        await client.close()
        await worker.close()
        await worker_task
        cache.close()


async def ask_on_terminal(interrupt: AgentInterrupt) -> bool:
    answer = await asyncio.to_thread(input, f"Approve {interrupt.tool_name}? [y/N] ")
    return answer.strip().lower() == "y"


def main() -> None:
    message = " ".join(sys.argv[1:]) or "Look up order 1001"
    replies = asyncio.run(ask(build_session_flow(MODEL), message, ask_on_terminal))
    for reply in replies:
        print(reply)


if __name__ == "__main__":
    main()
