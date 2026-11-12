# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The launcher's code over every terminal UI contract case file, and the plain-chat choice."""

from __future__ import annotations

import asyncio
import json
from collections.abc import Mapping
from dataclasses import replace
from importlib import resources
from pathlib import Path
from typing import Any

import grpc  # type: ignore[import-untyped]
import pytest
from dex import ErrorSubStatus, FlowAlreadyStartedError, FlowNotFoundError, StartFlowOptions
from jsonschema import Draft202012Validator

from dex_agents import LocalDirectoryMediaStore
from dex_agents_cli_core.config import ConnectorsConfig
from dex_agents_cli_core.environment import CommandEnvironment
from dex_agents_cli_core.terminal_ui.contract import (
    ChannelProtocolError,
    DescribeOutputError,
    IdempotencyKeyError,
    build_terminal_ui_environment,
    candidate_session_ids,
    choose_contract,
    cut_notice_text,
    derive_session_id,
    is_open_url_allowed,
    parse_describe_output,
)
from dex_agents_cli_core.terminal_ui.hosted_session import HostedChatSession
from dex_agents_cli_core.terminal_ui.local_requests import (
    IdempotencyTable,
    LocalRequestServer,
    RequestOutcome,
)
from tests.conftest import FakeConnectorsMcp
from tests.test_cli_images import png_bytes

AGENTS_DIR = Path(__file__).resolve().parents[4]
CASES_DIR = AGENTS_DIR / "conformance" / "terminal_ui"
CHANNEL_SCHEMA = json.loads((AGENTS_DIR / "cli" / "terminal-ui-channel.schema.json").read_text())


def channel_definition(name: str) -> Draft202012Validator:
    """Return a validator of one ``$defs`` entry of the channel schema."""
    return Draft202012Validator(
        {
            "$schema": CHANNEL_SCHEMA["$schema"],
            "$defs": CHANNEL_SCHEMA["$defs"],
            "$ref": f"#/$defs/{name}",
        }
    )


def load_cases(name: str) -> dict[str, Any]:
    loaded: dict[str, Any] = json.loads((CASES_DIR / name).read_text(encoding="utf-8"))
    return loaded


def case_name(case: Mapping[str, Any]) -> str:
    return str(case.get("name") or case.get("description"))


def test_the_packaged_describe_schema_is_the_shared_one() -> None:
    name = "terminal-ui-describe.schema.json"
    packaged = resources.files("dex_agents_cli_core.schemas").joinpath(name).read_bytes()
    assert packaged == (AGENTS_DIR / "cli" / name).read_bytes()


@pytest.mark.parametrize("vector", load_cases("session_ids.json")["vectors"], ids=case_name)
def test_session_id_vectors(vector: dict[str, Any]) -> None:
    key = vector["idempotency_key"]
    assert derive_session_id(vector["harness"], key) == vector["session_id"]
    assert candidate_session_ids(vector["harness"], key) == (
        vector["session_id"],
        vector["long_session_id"],
    )


@pytest.mark.parametrize("case", load_cases("session_ids.json")["invalid_keys"], ids=case_name)
def test_invalid_keys_are_refused(case: dict[str, Any]) -> None:
    with pytest.raises(IdempotencyKeyError):
        derive_session_id("openai-sdk", case["idempotency_key"])


@pytest.mark.parametrize("case", load_cases("session_ids.json")["collisions"], ids=case_name)
def test_colliding_keys_share_the_short_id_only(case: dict[str, Any]) -> None:
    candidates = [candidate_session_ids(case["harness"], key) for key in case["idempotency_keys"]]
    assert {short for short, _ in candidates} == {case["session_id"]}
    assert [long for _, long in candidates] == case["long_session_ids"]


class _FakeServer:
    """Answers StartFlow and DescribeFlow as the Dex server does for request IDs."""

    def __init__(self, started_request_ids: Mapping[str, str]) -> None:
        self.started_request_ids = dict(started_request_ids)
        self.new_run_count = 0

    async def describe_flow(self, session_id: str) -> object:
        if session_id not in self.started_request_ids:
            raise FlowNotFoundError(
                grpc.StatusCode.NOT_FOUND,
                ErrorSubStatus.FLOW_NOT_EXISTS,
                "not found",
                "DescribeFlow",
                session_id,
            )
        return object()

    async def start_session(
        self, session_id: str, *, options: StartFlowOptions, start_environment: dict[str, str]
    ) -> str:
        assert options.ignore_already_started
        started_by = self.started_request_ids.get(session_id)
        if started_by is not None and started_by != options.request_id:
            raise FlowAlreadyStartedError(
                grpc.StatusCode.ALREADY_EXISTS,
                ErrorSubStatus.FLOW_ALREADY_STARTED,
                "already started",
                "StartFlow",
                session_id,
            )
        if started_by is None:
            self.new_run_count += 1
            self.started_request_ids[session_id] = str(options.request_id)
        return session_id

    async def get_agent_snapshot(self, session_id: str) -> Any:
        return type("Snapshot", (), {"state": object()})()


def hosted_on(server: _FakeServer, harness: str) -> HostedChatSession:
    return HostedChatSession(
        harness_name=harness,
        client=server,  # type: ignore[arg-type]
        sessions=server,  # type: ignore[arg-type]
        media_store=None,  # type: ignore[arg-type]
        start_environment={},
        connectors=ConnectorsConfig(),
        connectors_mcp_url=None,
        web_url=None,
    )


def assert_start_outcome(outcome: RequestOutcome, expected: Mapping[str, Any]) -> None:
    if "error" in expected:
        assert outcome.error is not None
        assert outcome.error["code"] == expected["error"]
    else:
        assert outcome.result == {"session_id": expected["session_id"]}


@pytest.mark.parametrize(
    "case", load_cases("session_ids.json")["start_session_cases"], ids=case_name
)
async def test_start_session_cases(case: dict[str, Any]) -> None:
    hosted = hosted_on(_FakeServer(case["started_request_ids"]), case["harness"])
    assert_start_outcome(await hosted.start_session({}, case["idempotency_key"]), case["expected"])


@pytest.mark.parametrize(
    "sequence", load_cases("session_ids.json")["start_session_sequences"], ids=case_name
)
async def test_start_session_sequences(sequence: dict[str, Any]) -> None:
    server = _FakeServer({})
    hosted = hosted_on(server, sequence["harness"])
    for step in sequence["steps"]:
        if step["op"] == "purge":
            del server.started_request_ids[step["session_id"]]
            continue
        runs_before = server.new_run_count
        outcome = await hosted.start_session({}, step["idempotency_key"])
        assert_start_outcome(outcome, step["expected"])
        assert (server.new_run_count > runs_before) is step["starts_new_run"], step


@pytest.mark.parametrize("case", load_cases("open_url.json")["cases"], ids=case_name)
def test_open_url_cases(case: dict[str, Any]) -> None:
    assert is_open_url_allowed(case["web_url"], case["url"]) is case["is_allowed"]


@pytest.mark.parametrize("case", load_cases("notices.json")["text_cases"], ids=case_name)
def test_notices_are_cut_to_500_code_points(case: dict[str, Any]) -> None:
    cut = cut_notice_text(case["text"])
    assert len(cut) <= 500
    if not any(
        ord(character) < 0x20 or 0x7F <= ord(character) <= 0x9F for character in case["text"]
    ):
        assert cut == case["shown"]


@pytest.mark.parametrize("case", load_cases("environment.json")["cases"], ids=case_name)
def test_environment_cases(case: dict[str, Any]) -> None:
    command_environment = CommandEnvironment.from_environment(case["launcher_environment"])
    built = build_terminal_ui_environment(command_environment.terminal_variables, case["auth"])
    assert built == case["terminal_ui_environment"]


@pytest.mark.parametrize("case", load_cases("describe.json")["cases"], ids=case_name)
def test_describe_cases(case: dict[str, Any]) -> None:
    output = json.dumps(case["describe"]).encode() + b"\n"
    if case["is_valid"]:
        assert parse_describe_output(output) == case["describe"]
    else:
        with pytest.raises(DescribeOutputError):
            parse_describe_output(output)


@pytest.mark.parametrize("output", [b"", b"not json", b"[]", b"{} {}", b"x" * (1_048_578)])
def test_describe_output_that_is_not_one_object_is_refused(output: bytes) -> None:
    with pytest.raises(DescribeOutputError):
        parse_describe_output(output)


@pytest.mark.parametrize("case", load_cases("describe.json")["negotiation"], ids=case_name)
def test_negotiation_cases(case: dict[str, Any]) -> None:
    choice = choose_contract(case["describe"], case["mode"], case["launcher_contracts"])
    if "contract" in case["expected"]:
        assert choice.contract == case["expected"]["contract"]
    else:
        assert choice.contract is None
        assert choice.plain_reason == case["expected"]["plain_reason"]


@pytest.mark.parametrize("case", load_cases("idempotency_table.json")["cases"], ids=case_name)
def test_idempotency_table_cases(case: dict[str, Any]) -> None:
    limits = load_cases("idempotency_table.json")["limits"]
    table = IdempotencyTable(limits["capacity"], limits["retention_ms"] / 1000)
    for event in case["events"]:
        now = event["at_ms"] / 1000
        match event["op"]:
            case "arrive":
                table.arrive(event["key"], now)
            case "finish":
                table.finish(event["key"], now)
            case "fill":
                for index in range(event["count"]):
                    key = f"{event['prefix']}{index}"
                    table.arrive(key, now + index / 1000)
                    table.finish(key, now + index / 1000)
            case "query":
                assert table.state(event["key"], now) == event["state"], event


class _Requests:
    """A LocalRequestServer whose handlers echo their params, with every sent message."""

    def __init__(self, release: asyncio.Event | None = None) -> None:
        self.sent: list[dict[str, Any]] = []
        self.runs: list[str] = []
        self._release = release

        async def handler(params: Mapping[str, Any], key: str) -> RequestOutcome:
            self.runs.append(key)
            if self._release is not None:
                await self._release.wait()
            return RequestOutcome.succeeded({"echo": dict(params)})

        async def send(message: Mapping[str, Any]) -> None:
            self.sent.append(dict(message))

        methods = ("put_image", "start_session", "list_connectors", "open_url")
        self.server = LocalRequestServer({method: handler for method in methods}, send)

    async def settle(self) -> None:
        for _ in range(20):
            await asyncio.sleep(0)


_LAUNCHER_MESSAGE = channel_definition("from_launcher")


@pytest.mark.parametrize(
    "case",
    [
        case
        for case in load_cases("messages.json")["cases"]
        if case["direction"] == "tui" and case["message"]["type"] in ("request", "cancel")
    ],
    ids=case_name,
)
async def test_request_message_cases(case: dict[str, Any]) -> None:
    requests = _Requests()
    try:
        await requests.server.handle_message(case["message"])
    except ChannelProtocolError:
        assert not case["is_valid"]
        return
    await requests.settle()
    for message in requests.sent:
        _LAUNCHER_MESSAGE.validate(message)
    if case["message"]["type"] == "cancel":
        assert requests.sent == []
        return
    (response,) = requests.sent
    if case["is_valid"]:
        assert response.get("error", {}).get("code") in (None, "unknown_method")
    else:
        assert response["error"]["code"] == "invalid_params"


KEY = "01a1caff-6ed8-71f4-b2a9-0c3d5e6f7081"
OTHER_KEY = "01a1caff-786a-72e5-83b8-1d4e6f708192"


def request(
    request_id: int, method: str, key: str | None, params: dict[str, Any]
) -> dict[str, Any]:
    message: dict[str, Any] = {
        "type": "request",
        "id": request_id,
        "method": method,
        "params": params,
    }
    if key is not None:
        message["idempotency_key"] = key
    return message


async def test_a_repeated_key_runs_once_and_answers_every_request_with_the_first_outcome() -> None:
    release = asyncio.Event()
    requests = _Requests(release)
    path = {"path": "/home/user/a.png"}
    await requests.server.handle_message(request(1, "put_image", KEY, path))
    await requests.server.handle_message(request(2, "put_image", KEY, path))
    await requests.server.handle_message(
        request(3, "get_request_result", None, {"idempotency_key": KEY})
    )
    await requests.settle()
    assert requests.sent == [{"type": "response", "id": 3, "result": {"state": "running"}}]
    release.set()
    await requests.settle()
    result = {"echo": path}
    assert requests.sent[1:] == [
        {"type": "response", "id": 1, "result": result},
        {"type": "response", "id": 2, "result": result},
    ]
    assert requests.runs == [KEY]
    await requests.server.handle_message(request(4, "put_image", KEY, {"path": "/home/user/b.png"}))
    await requests.settle()
    assert requests.sent[-1]["error"]["code"] == "invalid_params"


async def test_cancel_answers_at_once_and_the_outcome_stays_under_its_key() -> None:
    release = asyncio.Event()
    requests = _Requests(release)
    await requests.server.handle_message(request(1, "start_session", KEY, {}))
    await requests.server.handle_message({"type": "cancel", "id": 1})
    await requests.server.handle_message({"type": "cancel", "id": 1})
    release.set()
    await requests.settle()
    await requests.server.handle_message(
        request(2, "get_request_result", None, {"idempotency_key": KEY})
    )
    await requests.settle()
    assert requests.sent == [
        {
            "type": "response",
            "id": 1,
            "error": {"code": "cancelled", "message": "Stopped waiting for start_session."},
        },
        {"type": "response", "id": 2, "result": {"state": "done", "result": {"echo": {}}}},
    ]


async def test_a_fifth_waiting_request_is_busy_and_get_request_result_never_is() -> None:
    release = asyncio.Event()
    requests = _Requests(release)
    keys = [f"01a1caff-706{index}-75b8-b6e5-40708192a3b4" for index in range(5)]
    for index, key in enumerate(keys, 1):
        await requests.server.handle_message(request(index, "list_connectors", key, {}))
    await requests.server.handle_message(
        request(6, "get_request_result", None, {"idempotency_key": keys[0]})
    )
    await requests.settle()
    assert [message["id"] for message in requests.sent] == [5, 6]
    assert requests.sent[0]["error"] == {
        "code": "busy",
        "message": "4 local requests are already running.",
    }
    release.set()
    await requests.settle()
    await requests.server.handle_message(request(7, "list_connectors", keys[4], {}))
    await requests.settle()
    assert requests.sent[-1] == {"type": "response", "id": 7, "result": {"echo": {}}}


async def test_an_outcome_that_is_not_remembered_leaves_the_key_unknown() -> None:
    sent: list[dict[str, Any]] = []

    async def stopped(params: Mapping[str, Any], key: str) -> RequestOutcome:
        return RequestOutcome.failed(
            "host_stopped", "The Worker host stopped.", is_remembered=False
        )

    async def send(message: Mapping[str, Any]) -> None:
        sent.append(dict(message))

    server = LocalRequestServer({"put_image": stopped}, send)
    await server.handle_message(request(1, "put_image", KEY, {"path": "/a.png"}))
    await asyncio.sleep(0.01)
    await server.handle_message(request(2, "get_request_result", None, {"idempotency_key": KEY}))
    await asyncio.sleep(0.01)
    assert sent[1]["result"] == {"state": "unknown"}
    await server.handle_message(request(3, "put_image", OTHER_KEY, {"path": "/a.png"}))
    await asyncio.sleep(0.01)
    assert sent[2]["error"]["code"] == "host_stopped"


def test_the_terminal_ui_switch_is_off_unless_set() -> None:
    assert not CommandEnvironment.from_environment({"HOME": "/home/user"}).is_terminal_ui_enabled
    for value in ("1", "true", "TRUE"):
        environment = CommandEnvironment.from_environment(
            {"HOME": "/home/user", "DEX_AGENTS_TERMINAL_UI": value}
        )
        assert environment.is_terminal_ui_enabled
    off = CommandEnvironment.from_environment({"HOME": "/home/user", "DEX_AGENTS_TERMINAL_UI": "0"})
    assert replace(off, is_terminal_ui_enabled=False) == off


def method_result_validator(method: str) -> Draft202012Validator:
    return channel_definition(f"{method}_result")


async def test_the_worker_host_results_match_the_channel_schema(
    tmp_path: Path, fake_mcp: FakeConnectorsMcp
) -> None:
    hosted = HostedChatSession(
        harness_name="scripted",
        client=_FakeServer({}),  # type: ignore[arg-type]
        sessions=_FakeServer({}),  # type: ignore[arg-type]
        media_store=LocalDirectoryMediaStore(tmp_path / "media"),
        start_environment={},
        connectors=ConnectorsConfig(),
        connectors_mcp_url=fake_mcp.url,
        web_url=None,
    )
    image = tmp_path / "chart.png"
    image.write_bytes(png_bytes())
    stored = await hosted.put_image({"path": str(image)}, KEY)
    method_result_validator("put_image").validate(stored.result)
    listed = await hosted.list_connectors({}, OTHER_KEY)
    assert listed.result is not None and listed.result["tools"]
    method_result_validator("list_connectors").validate(listed.result)
    started = await hosted.start_session({}, KEY)
    method_result_validator("start_session").validate(started.result)
    missing = await hosted.put_image({"path": str(tmp_path / "missing.png")}, KEY)
    assert missing.error == {"code": "not_found", "message": "missing.png does not exist."}
