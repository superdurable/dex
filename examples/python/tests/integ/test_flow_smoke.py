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
import json
import socket
from collections.abc import AsyncIterator, Callable
from uuid import uuid4

import pytest
import pytest_asyncio

from dex_examples.app import ExampleApp
from dex_examples.http_app import create_app
from dex_examples.products.ai_agent.http_routes import SESSION_TOKEN_HEADER, sign_session_id
from dex_agents import AgentSessionClient
from tests.integ.conftest import WAIT_TIMEOUT
from tests.integ.ai_agent_sessions import (
    is_waiting_for_timer,
    wait_for_message_text,
    wait_for_snapshot,
)
from tests.integ.flow_smoke_helper import (
    FlowSmokeEntry,
    FlowSmokeFlags,
    FlowSmokeHttpClient,
    assert_flow_smoke_no_unexpected_failures,
    assert_flow_smoke_start_step,
    parse_flow_trigger_response,
)


@pytest_asyncio.fixture(scope="session")
async def flow_smoke_http(
    example_app: ExampleApp,
) -> AsyncIterator[FlowSmokeHttpClient]:
    http_port = _available_port()
    quart_app = create_app(example_app)
    server_task = asyncio.create_task(
        quart_app.run_task(host="127.0.0.1", port=http_port, debug=False)
    )
    base_url = f"http://127.0.0.1:{http_port}"
    await _wait_for_http(base_url)
    client = FlowSmokeHttpClient(base_url, _new_flow_id)
    try:
        yield client
    finally:
        server_task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await server_task


def flow_smoke_catalog(client: FlowSmokeHttpClient) -> list[FlowSmokeEntry]:
    new_id = client.new_flow_id

    async def trigger_get(path: str, query: dict[str, str]) -> tuple[str, str]:
        flow_id, run_id, _ = await client.get(path, query)
        return flow_id, run_id

    def parallel_subflows_entry(kind: str) -> FlowSmokeEntry:
        return FlowSmokeEntry(
            f"patterns/parallel-subflows/{kind}",
            lambda c: trigger_get(
                f"/patterns/parallel-subflows/start/{kind}",
                {"workflowId": new_id(f"parallel-subflows-{kind}")},
            ),
        )

    return [
        FlowSmokeEntry("products/engagement", lambda c: trigger_get("/products/engagement/start", {})),
        FlowSmokeEntry(
            "products/microservices",
            lambda c: trigger_get(
                "/products/microservices/start",
                {"workflowId": new_id("microservices")},
            ),
        ),
        FlowSmokeEntry(
            "products/money-transfer",
            lambda c: trigger_get(
                "/products/money-transfer/start",
                {
                    "amount": "100",
                    "fromAccount": "from-smoke",
                    "toAccount": "to-smoke",
                    "notes": "smoke",
                },
            ),
        ),
        FlowSmokeEntry(
            "products/order-processing",
            lambda c: trigger_get("/products/order-processing/start", {}),
        ),
        FlowSmokeEntry(
            "products/subscription",
            lambda c: trigger_get("/products/subscription/start", {}),
        ),
        FlowSmokeEntry(
            "products/signup",
            lambda c: _signup_trigger(c, new_id),
        ),
        FlowSmokeEntry(
            "products/job-post",
            lambda c: trigger_get(
                "/products/job-post/create",
                {"title": "Smoke Test Job", "description": "Smoke test description"},
            ),
            flags=FlowSmokeFlags(no_start_step=True),
        ),
        FlowSmokeEntry(
            "products/ai-agent",
            lambda c: _ai_agent_trigger(c),
        ),
        FlowSmokeEntry(
            "patterns/polling",
            lambda c: trigger_get(
                "/patterns/polling/start",
                {"workflowId": new_id("pattern-polling")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/interruptible",
            lambda c: trigger_get(
                "/patterns/interruptible/start",
                {"workflowId": new_id("interruptible")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/reminders",
            lambda c: _reminders_trigger(c),
        ),
        FlowSmokeEntry(
            "patterns/entity-store",
            lambda c: _entity_store_trigger(c, new_id),
            flags=FlowSmokeFlags(no_start_step=True),
        ),
        FlowSmokeEntry(
            "patterns/sequentially-chunked-attribute-map",
            lambda c: _rpc_only_pattern_trigger(
                c,
                "/patterns/sequentially-chunked-attribute-map/start",
            ),
            flags=FlowSmokeFlags(no_start_step=True),
        ),
        FlowSmokeEntry(
            "patterns/hash-partitioned-attribute-map",
            lambda c: _rpc_only_pattern_trigger(
                c,
                "/patterns/hash-partitioned-attribute-map/start",
            ),
            flags=FlowSmokeFlags(no_start_step=True),
        ),
        FlowSmokeEntry(
            "patterns/manual-recovery",
            lambda c: trigger_get(
                "/patterns/manual-recovery/start",
                {"workflowId": new_id("manual-recovery")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/inactiveness-tracker-timer",
            lambda c: trigger_get(
                "/patterns/inactiveness-tracker-timer/start",
                {"workflowId": new_id("inactiveness-tracker-timer")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/parallel/static",
            lambda c: trigger_get(
                "/patterns/parallel/start/static",
                {"workflowId": new_id("parallel-static")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/parallel/dynamic",
            lambda c: trigger_get(
                "/patterns/parallel/start/dynamic",
                {"workflowId": new_id("parallel-dynamic")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/parallel/await",
            lambda c: trigger_get(
                "/patterns/parallel/start/await",
                {"workflowId": new_id("parallel-await")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/parallel/first-win",
            lambda c: trigger_get(
                "/patterns/parallel/start/first-win",
                {"workflowId": new_id("parallel-first-win")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/recovery",
            lambda c: trigger_get(
                "/patterns/recovery/start",
                {
                    "workflowId": new_id("recovery"),
                    "itemName": "smoke-item",
                    "quantity": "2",
                },
            ),
            flags=FlowSmokeFlags(step_start_may_fail=True),
        ),
        parallel_subflows_entry("basic"),
        parallel_subflows_entry("wait-for-half"),
        parallel_subflows_entry("long-lived-parent"),
        parallel_subflows_entry("short-lived-parent"),
        FlowSmokeEntry(
            "patterns/drain-channels/internal",
            lambda c: trigger_get(
                "/patterns/drain-channels/internal/start",
                {"workflowId": new_id("drain-internal")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/drain-channels/external-publishing",
            lambda c: trigger_get(
                "/patterns/drain-channels/external-publishing/start-or-publish",
                {"workflowId": new_id("drain-external")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/wait-for-step-completion",
            lambda c: trigger_get(
                "/patterns/wait-for-step-completion/start",
                {"workflowId": new_id("wait-for-state")},
            ),
        ),
        FlowSmokeEntry(
            "patterns/timeout",
            lambda c: trigger_get(
                "/patterns/timeout/start",
                {
                    "workflowId": new_id("timeout"),
                    "successfulWorkflow": "true",
                },
            ),
        ),
        FlowSmokeEntry(
            "patterns/resource-control",
            lambda c: trigger_get(
                "/patterns/resource-control/request",
                {"id": new_id("resource-request")},
            ),
        ),
        FlowSmokeEntry(
            "primitives/step",
            lambda c: trigger_get(
                "/primitives/step/start",
                {"workflowId": new_id("primitive-step"), "inputNum": "1"},
            ),
        ),
        FlowSmokeEntry(
            "primitives/step/retry",
            lambda c: trigger_get(
                "/primitives/step/retry/start",
                {
                    "workflowId": new_id("primitive-step-retry"),
                    "readyAfterAttempt": "2",
                },
            ),
        ),
        FlowSmokeEntry(
            "primitives/step/custom-retry",
            lambda c: trigger_get(
                "/primitives/step/custom-retry/start",
                {
                    "workflowId": new_id("primitive-step-custom-retry"),
                    "readyAfterAttempt": "1",
                },
            ),
        ),
        FlowSmokeEntry(
            "primitives/step/durability",
            lambda c: trigger_get(
                "/primitives/step/durability/start",
                {
                    "workflowId": new_id("primitive-step-durability"),
                    "mode": "sync",
                },
            ),
        ),
        FlowSmokeEntry(
            "primitives/step/heartbeat",
            lambda c: trigger_get(
                "/primitives/step/heartbeat/start",
                {
                    "workflowId": new_id("primitive-step-heartbeat"),
                    "batches": "0",
                },
            ),
        ),
        FlowSmokeEntry(
            "primitives/step/options-override",
            lambda c: trigger_get(
                "/primitives/step/options-override/start",
                {
                    "workflowId": new_id("primitive-step-options-override"),
                    "input": "smoke",
                },
            ),
            flags=FlowSmokeFlags(
                allowed_failure_event_types=frozenset({"StepWaitForFailed"})
            ),
        ),
        FlowSmokeEntry(
            "primitives/step/step-decision",
            lambda c: trigger_get(
                "/primitives/step/step-decision/start",
                {
                    "workflowId": new_id("primitive-step-decision"),
                    "mode": "graceful",
                },
            ),
        ),
        FlowSmokeEntry(
            "primitives/step/wait-types",
            lambda c: trigger_get(
                "/primitives/step/wait-types/start",
                {
                    "workflowId": new_id("primitive-step-wait-types"),
                    "mode": "any",
                    "timeoutSeconds": "1",
                },
            ),
        ),
        FlowSmokeEntry(
            "primitives/attribute",
            lambda c: trigger_get(
                "/primitives/attribute/start",
                {"workflowId": new_id("primitive-attribute"), "message": "smoke"},
            ),
        ),
        FlowSmokeEntry(
            "primitives/channel",
            lambda c: trigger_get(
                "/primitives/channel/start",
                {"workflowId": new_id("primitive-channel"), "inputNum": "1"},
            ),
        ),
        FlowSmokeEntry(
            "primitives/stream",
            lambda c: trigger_get(
                "/primitives/stream/start",
                {"workflowId": new_id("primitive-stream"), "input": "smoke"},
            ),
        ),
        FlowSmokeEntry(
            "primitives/timer",
            lambda c: trigger_get(
                "/primitives/timer/start",
                {"workflowId": new_id("primitive-timer"), "seconds": "1"},
            ),
        ),
        FlowSmokeEntry(
            "primitives/rpc",
            lambda c: trigger_get(
                "/primitives/rpc/start",
                {"workflowId": new_id("primitive-rpc")},
            ),
        ),
        FlowSmokeEntry(
            "primitives/subflow",
            lambda c: trigger_get(
                "/primitives/subflow/start",
                {"workflowId": new_id("primitive-subflow"), "inputNum": "1"},
            ),
        ),
        FlowSmokeEntry(
            "primitives/client-apis",
            lambda c: trigger_get(
                "/primitives/client-apis/start",
                {
                    "workflowId": new_id("primitive-client-apis"),
                    "keyword": "smoke",
                },
            ),
        ),
    ]


async def _signup_trigger(
    client: FlowSmokeHttpClient,
    new_id: Callable[[str], str],
) -> tuple[str, str]:
    username = new_id("signup")
    flow_id, run_id, body = await client.get(
        "/products/signup/submit",
        {"username": username, "email": f"{username}@example.com"},
    )
    parsed_flow_id, parsed_run_id = parse_flow_trigger_response(body, username)
    return parsed_flow_id or flow_id or username, parsed_run_id or run_id


async def _rpc_only_pattern_trigger(
    client: FlowSmokeHttpClient,
    path: str,
) -> tuple[str, str]:
    flow_id, run_id, _ = await client.post(path)
    return flow_id, run_id


async def _ai_agent_trigger(client: FlowSmokeHttpClient) -> tuple[str, str]:
    session_id, _ = await _start_ai_agent_session(client)
    return session_id, ""


async def _start_ai_agent_session(client: FlowSmokeHttpClient) -> tuple[str, dict[str, str]]:
    """Starts a session over HTTP; returns its ID and the header that authorizes its routes."""
    _, _, body = await client.post("/products/ai-agent/sessions", {})
    started = json.loads(body)
    return started["session_id"], {SESSION_TOKEN_HEADER: started["session_token"]}


async def test_ai_agent_settings_show_the_worker_configuration(
    flow_smoke_http: FlowSmokeHttpClient,
) -> None:
    _, _, settings_body = await flow_smoke_http.get("/products/ai-agent/settings")
    settings = json.loads(settings_body)
    assert settings["model"] == "mock/dex"
    assert settings["mcp_servers"] == ["test"]
    effects = {tool["name"]: tool["effect"] for tool in settings["mcp_tools"]}
    assert effects["test__lookup"] == "read"
    assert effects["test__publish"] == "unclassified"
    assert settings["built_in_tools"] == ["ask_user", "durable_wait", "write_todos"]


async def test_ai_agent_http_queue_can_delete_and_steer(
    flow_smoke_http: FlowSmokeHttpClient,
    example_app: ExampleApp,
) -> None:
    sessions = AgentSessionClient(example_app.client, example_app.ai_agent)
    session_id, token = await _start_ai_agent_session(flow_smoke_http)
    await flow_smoke_http.post(
        "/products/ai-agent/messages",
        {"session_id": session_id, "text": "/wait 30 queue test"},
        headers=token,
    )
    await wait_for_snapshot(sessions, session_id, "durable wait", is_waiting_for_timer)
    await flow_smoke_http.post(
        "/products/ai-agent/messages",
        {"session_id": session_id, "text": "delete this"},
        headers=token,
    )
    _, _, snapshot_body = await flow_smoke_http.get(
        "/products/ai-agent/snapshot",
        {"session_id": session_id},
        headers=token,
    )
    queued = json.loads(snapshot_body)["snapshot"]["queued_messages"]
    assert [message["message"]["text"] for message in queued] == ["delete this"]
    _, _, deleted_body = await flow_smoke_http.post(
        "/products/ai-agent/messages/delete",
        {"session_id": session_id, "message_id": queued[0]["message_id"]},
        headers=token,
    )
    assert json.loads(deleted_body) == {"accepted": True}

    await flow_smoke_http.post(
        "/products/ai-agent/messages",
        {"session_id": session_id, "text": "steer this"},
        headers=token,
    )
    _, _, snapshot_body = await flow_smoke_http.get(
        "/products/ai-agent/snapshot",
        {"session_id": session_id},
        headers=token,
    )
    queued = json.loads(snapshot_body)["snapshot"]["queued_messages"]
    _, _, steered_body = await flow_smoke_http.post(
        "/products/ai-agent/messages/steer",
        {"session_id": session_id, "message_id": queued[0]["message_id"]},
        headers=token,
    )
    assert json.loads(steered_body) == {"accepted": True}
    await wait_for_message_text(sessions, session_id, "Local demo response: steer this")


async def test_ai_agent_http_history_events_and_closed_sessions(
    flow_smoke_http: FlowSmokeHttpClient,
    example_app: ExampleApp,
) -> None:
    sessions = AgentSessionClient(example_app.client, example_app.ai_agent)
    http_client = create_app(example_app).test_client()
    session_id, token = await _start_ai_agent_session(flow_smoke_http)
    for index in range(3):
        await flow_smoke_http.post(
            "/products/ai-agent/messages",
            {"session_id": session_id, "text": f"page {index}"},
            headers=token,
        )
        await wait_for_message_text(sessions, session_id, f"Local demo response: page {index}")

    _, _, history_body = await flow_smoke_http.get(
        "/products/ai-agent/history",
        {"session_id": session_id, "before_sequence": "5"},
        headers=token,
    )
    history = json.loads(history_body)
    assert [message["sequence"] for message in history["messages"]] == [1, 2, 3, 4]
    assert history["next_before_sequence"] is None
    assert all("framework_item" not in message for message in history["messages"])
    bad_anchor = await http_client.get(
        "/products/ai-agent/history",
        query_string={"session_id": session_id, "before_sequence": "first"},
        headers=token,
    )
    assert bad_anchor.status_code == 400

    missing_session_id = flow_smoke_http.new_flow_id("missing-ai-agent")
    missing_token = sign_session_id(example_app.config.agent_session_key, missing_session_id)
    missing_events = await http_client.get(
        "/products/ai-agent/events",
        query_string={"session_id": missing_session_id},
        headers={SESSION_TOKEN_HEADER: missing_token},
    )
    assert missing_events.status_code == 404

    await example_app.client.stop_flow(session_id)
    await example_app.client.wait_for_flow(session_id, WAIT_TIMEOUT)
    closed = await http_client.post(
        "/products/ai-agent/messages",
        json={"session_id": session_id, "text": "too late"},
        headers=token,
    )
    assert closed.status_code == 409
    assert "session is closed" in await closed.get_data(as_text=True)


async def _reminders_trigger(client: FlowSmokeHttpClient) -> tuple[str, str]:
    _, _, body = await client.get("/patterns/reminders/start", {})
    return parse_flow_trigger_response(body, "")


async def _entity_store_trigger(
    client: FlowSmokeHttpClient,
    new_id: Callable[[str], str],
) -> tuple[str, str]:
    user_id = new_id("entity-store")
    flow_id, run_id, _ = await client.post(
        "/patterns/entity-store/profile",
        {
            "userId": user_id,
            "displayName": "Smoke Tester",
            "email": f"{user_id}@example.com",
            "marketingOptIn": True,
            "credits": 120,
            "weight": 59.5,
            "lastLoggedInTime": "2026-08-11T15:30:00+00:00",
            "metadata": {"source": "smoke", "tags": ["example"]},
        },
    )
    return flow_id or user_id, run_id


def _new_flow_id(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex}"


def _available_port() -> int:
    with socket.socket() as worker_socket:
        worker_socket.bind(("127.0.0.1", 0))
        return worker_socket.getsockname()[1]


async def _wait_for_http(base_url: str) -> None:
    import urllib.request

    deadline = asyncio.get_running_loop().time() + 20
    while asyncio.get_running_loop().time() < deadline:
        try:
            await asyncio.to_thread(
                lambda: urllib.request.urlopen(f"{base_url}/", timeout=1).read()
            )
            return
        except OSError:
            await asyncio.sleep(0.1)
    raise RuntimeError("flow smoke HTTP server did not become ready")


# Triggers are lazy, so a placeholder client only lists the entry names.
FLOW_SMOKE_ENTRY_NAMES = [
    entry.name for entry in flow_smoke_catalog(FlowSmokeHttpClient("", _new_flow_id))
]


@pytest.mark.integ
@pytest.mark.parametrize("entry_name", FLOW_SMOKE_ENTRY_NAMES)
async def test_flow_smoke_all_registered_flows_via_controller(
    flow_smoke_http: FlowSmokeHttpClient,
    entry_name: str,
) -> None:
    catalog = flow_smoke_catalog(flow_smoke_http)
    assert catalog, "flow smoke catalog is empty"
    entry = next(entry for entry in catalog if entry.name == entry_name)
    flow_id, run_id = await entry.trigger(flow_smoke_http)
    assert flow_id, f"{entry.name}: controller response did not include flowID"
    await assert_flow_smoke_start_step(entry, flow_id, run_id)
    await assert_flow_smoke_no_unexpected_failures(entry, flow_id, run_id)


@pytest.mark.integ
def test_legacy_agent_route_is_not_registered(example_app: ExampleApp) -> None:
    paths = {rule.rule for rule in create_app(example_app).url_map.iter_rules()}
    legacy_path = "/products/" + "ai-agent-" + "email"
    assert not any(path.startswith(legacy_path) for path in paths)
