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

"""Runs agent.py against a real Dex server with a keyless scripted model.

Point DEX_FLOW_SERVICE_ADDRESS at the server (default 127.0.0.1:8801).
"""

from __future__ import annotations

import uuid

import agent
from dex_agents import AgentInterrupt
from tests.scripted_model import OrderScriptModel


async def test_read_runs_without_approval() -> None:
    asked: list[str] = []

    async def decide(interrupt: AgentInterrupt) -> bool:
        asked.append(interrupt.tool_name)
        return True

    replies = await agent.ask(
        agent.build_session_flow(OrderScriptModel()),
        "Look up order 1001",
        decide,
        session_id=f"starter-read-{uuid.uuid4().hex}",
    )
    assert asked == []
    assert any("desk lamp" in reply for reply in replies)


async def test_write_waits_for_approval_and_runs_once() -> None:
    agent.ORDERS["1002"]["status"] = "processing"
    asked: list[str] = []

    async def decide(interrupt: AgentInterrupt) -> bool:
        asked.append(interrupt.tool_name)
        return True

    replies = await agent.ask(
        agent.build_session_flow(OrderScriptModel()),
        "Cancel order 1002",
        decide,
        session_id=f"starter-cancel-{uuid.uuid4().hex}",
    )
    assert asked == ["cancel_order"]
    assert agent.ORDERS["1002"]["status"] == "cancelled"
    assert any("cancelled" in reply for reply in replies)
