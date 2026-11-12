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

"""The quick start web app runs a session over HTTP on a real Dex server."""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.demo_model_script import DEMO_REPLY_PREFIX
from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.quick_start.frameworks import build_quick_start_agent
from agent_harness_examples.quick_start.web import create_web_app
from tests.conftest import wait_until


async def test_web_app_approves_a_cancellation(tmp_path: Path) -> None:
    config = AgentHarnessExamplesConfig(
        server_address=os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
        http_address="127.0.0.1:0",
        blob_cache_dir=tmp_path / "cache",
    )
    order_desk = OrderDesk()
    session_flow = build_quick_start_agent("openai-agents", order_desk, config)
    web = create_web_app(session_flow, config.server_address, tmp_path / "web-cache")
    async with web.test_app() as test_app:
        client = test_app.test_client()
        started = await (await client.post("/sessions")).get_json()
        session_id = started["session_id"]
        response = await client.post(
            f"/sessions/{session_id}/messages", json={"text": "Cancel order 1002"}
        )
        assert response.status_code == 200

        async def read() -> dict[str, Any]:
            result: dict[str, Any] = await (await client.get(f"/sessions/{session_id}")).get_json()
            return result

        async def has_interrupt() -> bool:
            return bool((await read())["pending_interrupts"])

        await wait_until("the cancellation to wait for approval", has_interrupt)
        interrupt_id = (await read())["pending_interrupts"][0]["interrupt_id"]
        resolved = await client.post(
            f"/sessions/{session_id}/interrupts/{interrupt_id}", json={"approved": True}
        )
        assert (await resolved.get_json())["accepted"] is True

        async def has_reply() -> bool:
            messages = (await read())["messages"]
            return any(
                m.get("role") == "assistant" and str(m.get("text", "")).startswith(DEMO_REPLY_PREFIX)
                for m in messages
            )

        await wait_until("the agent to reply", has_reply)
        assert order_desk.cancel_order_calls == ["1002"]
