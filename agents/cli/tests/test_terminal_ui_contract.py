# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The terminal UI contract schemas, conformance cases, and contract host agree with each other."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from collections.abc import Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import pytest
from jsonschema import Draft202012Validator

import terminal_ui_contract_host as host
from cli_config_reference import CLI_DIR, schema_errors

CONFORMANCE_DIR = CLI_DIR.parent / "conformance" / "terminal_ui"
TRANSCRIPT_PATHS = sorted((CONFORMANCE_DIR / "transcripts").glob("*.json"))
HOST_PATH = Path(host.__file__).resolve()
LAUNCH_SCHEMA: dict[str, Any] = json.loads((CLI_DIR / "terminal-ui-launch.schema.json").read_text())
CHANNEL_SCHEMA: dict[str, Any] = json.loads(
    (CLI_DIR / "terminal-ui-channel.schema.json").read_text()
)
DESCRIBE_SCHEMA: dict[str, Any] = json.loads(
    (CLI_DIR / "terminal-ui-describe.schema.json").read_text()
)
LAUNCH_VALIDATOR = Draft202012Validator(LAUNCH_SCHEMA)
DESCRIBE_VALIDATOR = Draft202012Validator(DESCRIBE_SCHEMA)
SHARED_DEFINITIONS = ("method_name", "notice", "connector_list")
COMMON_ERROR_CODES = {
    "unknown_method",
    "invalid_params",
    "timeout",
    "busy",
    "cancelled",
    "host_stopped",
}
METHOD_ERROR_CODES: dict[str, set[str]] = {
    "put_image": {"not_found", "too_large", "not_an_image", "store_failed"},
    "start_session": {"start_failed", "id_conflict"},
    "list_connectors": {"mcp_unavailable"},
    "open_url": {"refused"},
    "get_request_result": set(),
}


def channel_validator(definition: str) -> Draft202012Validator:
    return Draft202012Validator({"$defs": CHANNEL_SCHEMA["$defs"], "$ref": f"#/$defs/{definition}"})


FROM_VALIDATORS = {
    "launcher": channel_validator("from_launcher"),
    "tui": channel_validator("from_tui"),
}


def load_fixture(name: str) -> dict[str, Any]:
    fixture: dict[str, Any] = json.loads((CONFORMANCE_DIR / name).read_text(encoding="utf-8"))
    return fixture


def case_name(case: Mapping[str, Any]) -> str:
    return str(case.get("name") or case.get("description"))


REJECTION_EXIT_CODES: dict[str, int] = load_fixture("launch.json")["rejection_codes"]


# Schemas.


def test_schemas_are_valid_draft_2020_12() -> None:
    for schema in (LAUNCH_SCHEMA, CHANNEL_SCHEMA, DESCRIBE_SCHEMA):
        Draft202012Validator.check_schema(schema)


def test_shared_definitions_are_identical() -> None:
    for name in SHARED_DEFINITIONS:
        assert LAUNCH_SCHEMA["$defs"][name] == CHANNEL_SCHEMA["$defs"][name], name


def test_default_describe_passes_its_schema() -> None:
    assert not schema_errors(DESCRIBE_VALIDATOR, host.DEFAULT_DESCRIBE)


def undeclared_required_names(node: object, path: str = "#") -> list[str]:
    """Return required names missing from the same subschema's properties, which Ajv rejects."""
    if isinstance(node, list):
        return [
            name
            for index, item in enumerate(node)
            for name in undeclared_required_names(item, f"{path}/{index}")
        ]
    if not isinstance(node, dict):
        return []
    properties = node.get("properties", {})
    missing = [f"{path}: {name}" for name in node.get("required", []) if name not in properties]
    for key, value in node.items():
        missing.extend(undeclared_required_names(value, f"{path}/{key}"))
    return missing


def test_schemas_declare_every_required_name_for_ajv_strict_mode() -> None:
    for schema in (LAUNCH_SCHEMA, CHANNEL_SCHEMA, DESCRIBE_SCHEMA):
        assert not undeclared_required_names(schema)


# Launch documents.


@dataclass(frozen=True)
class LaunchVerdict:
    code: str | None
    errors: set[tuple[str, str]] = field(default_factory=set)

    @property
    def exit_code(self) -> int | None:
        return None if self.code is None else REJECTION_EXIT_CODES[self.code]


def classify_launch_line(line: bytes, environment: Mapping[str, str]) -> LaunchVerdict:
    """Apply launch.json's check order, as dexcli supporting DEFAULT_DESCRIBE does."""
    if len(line) > host.MAX_LINE_BYTES:
        return LaunchVerdict("invalid_launch")
    try:
        launch = host.parse_channel_line(line)
    except host.ChannelProtocolError:
        return LaunchVerdict("invalid_launch")
    if launch["type"] != "launch":
        return LaunchVerdict("invalid_launch")
    contract = launch.get("contract")
    if type(contract) is not int or contract not in host.SUPPORTED_CONTRACTS:
        return LaunchVerdict("contract_unsupported")
    if launch.get("mode") not in host.DEFAULT_DESCRIBE["contracts"][str(contract)]["modes"]:
        return LaunchVerdict("mode_unsupported")
    errors = schema_errors(LAUNCH_VALIDATOR, launch)
    if errors:
        return LaunchVerdict("invalid_launch", errors)
    if host.missing_auth_variable(environment, launch["dex"]["auth"]) is not None:
        return LaunchVerdict("auth_missing")
    return LaunchVerdict(None)


def padded_line(line: bytes, pad_to_bytes: int) -> bytes:
    """Pad with spaces, which JSON allows after a value, up to pad_to_bytes."""
    return line + b" " * max(0, pad_to_bytes - len(line))


def launch_case_line(case: Mapping[str, Any]) -> bytes:
    if "line" in case:
        return str(case["line"]).encode("utf-8")
    line = json.dumps(case["launch"], separators=(",", ":"), ensure_ascii=False).encode("utf-8")
    return padded_line(line, int(case.get("pad_to_bytes", 0)))


@pytest.mark.parametrize("case", load_fixture("launch.json")["cases"], ids=case_name)
def test_launch_cases(case: dict[str, Any]) -> None:
    verdict = classify_launch_line(launch_case_line(case), case["terminal_ui_environment"])
    expected = case["expected"]
    if expected.get("accepted"):
        assert verdict == LaunchVerdict(None)
        return
    assert (verdict.code, verdict.exit_code) == (expected["code"], expected["exit_code"])
    if "keyword" in expected:
        assert (expected["path"], expected["keyword"]) in verdict.errors


def test_launch_padding_reaches_the_limit_exactly() -> None:
    cases = {case["name"]: case for case in load_fixture("launch.json")["cases"]}
    assert len(launch_case_line(cases["at-limit"])) == host.MAX_LINE_BYTES
    assert len(launch_case_line(cases["too-large"])) == host.MAX_LINE_BYTES + 1


# --describe and negotiation.


@pytest.mark.parametrize("case", load_fixture("describe.json")["cases"], ids=case_name)
def test_describe_cases(case: dict[str, Any]) -> None:
    errors = schema_errors(DESCRIBE_VALIDATOR, case["describe"])
    if case["is_valid"]:
        assert not errors
    else:
        assert (case["error"]["path"], case["error"]["keyword"]) in errors


@pytest.mark.parametrize("case", load_fixture("describe.json")["negotiation"], ids=case_name)
def test_negotiation_cases(case: dict[str, Any]) -> None:
    assert not schema_errors(DESCRIBE_VALIDATOR, case["describe"])
    choice = host.choose_contract(case["launcher_contracts"], case["describe"], case["mode"])
    expected = case["expected"]
    assert (choice.contract, choice.plain_reason) == (
        expected.get("contract"),
        expected.get("plain_reason"),
    )


# Channel messages.


@pytest.mark.parametrize("case", load_fixture("messages.json")["cases"], ids=case_name)
def test_message_cases(case: dict[str, Any]) -> None:
    errors = schema_errors(FROM_VALIDATORS[case["direction"]], case["message"])
    if case["is_valid"]:
        assert not errors
    else:
        assert (case["error"]["path"], case["error"]["keyword"]) in errors


@pytest.mark.parametrize("case", load_fixture("messages.json")["result_cases"], ids=case_name)
def test_result_cases(case: dict[str, Any]) -> None:
    errors = schema_errors(channel_validator(f"{case['method']}_result"), case["result"])
    if case["is_valid"]:
        assert not errors
    else:
        assert (case["error"]["path"], case["error"]["keyword"]) in errors


# Reference rules.


@pytest.mark.parametrize("case", load_fixture("session_ids.json")["vectors"], ids=case_name)
def test_session_id_vectors(case: dict[str, Any]) -> None:
    assert host.derive_session_id(case["harness"], case["idempotency_key"]) == case["session_id"]
    message = {"session_id": case["session_id"]}
    assert not schema_errors(channel_validator("start_session_result"), message)


@pytest.mark.parametrize("case", load_fixture("session_ids.json")["invalid_keys"], ids=case_name)
def test_invalid_idempotency_keys(case: dict[str, Any]) -> None:
    with pytest.raises(host.IdempotencyKeyError):
        host.derive_session_id("openai-sdk", case["idempotency_key"])


def test_one_session_id_vector_matches_a_hand_computation() -> None:
    # printf '%s' KEY | shasum -a 256; date -u -r $((0x01a1cafd3028/1000)) +%Y%m%d-%H%M%S
    key = "01a1cafd-3028-7a3c-9b21-4f6e8d0c2a1b"
    assert host.derive_session_id("openai-sdk", key) == "openai-sdk-20261111-142233-872f0d"


@pytest.mark.parametrize("case", load_fixture("open_url.json")["cases"], ids=case_name)
def test_open_url_cases(case: dict[str, Any]) -> None:
    assert host.is_open_url_allowed(case["web_url"], case["url"]) is case["is_allowed"]


@pytest.mark.parametrize("case", load_fixture("notices.json")["text_cases"], ids=case_name)
def test_notice_text_cases(case: dict[str, Any]) -> None:
    assert host.displayed_notice_text(case["text"]) == case["shown"]
    assert len(case["shown"]) <= load_fixture("notices.json")["limits"]["max_code_points"]


@pytest.mark.parametrize("case", load_fixture("notices.json")["repeat_cases"], ids=case_name)
def test_notice_repeat_cases(case: dict[str, Any]) -> None:
    repeat_filter = host.NoticeRepeatFilter()
    for notice in case["notices"]:
        shown_text = host.displayed_notice_text(notice["text"])
        assert (
            repeat_filter.should_show(notice["at_ms"], notice["level"], shown_text)
            is (notice["is_shown"])
        ), notice


@pytest.mark.parametrize("case", load_fixture("environment.json")["cases"], ids=case_name)
def test_environment_cases(case: dict[str, Any]) -> None:
    environment = host.build_terminal_ui_environment(case["launcher_environment"], case["auth"])
    assert environment == case["terminal_ui_environment"]
    assert host.missing_auth_variable(environment, case["auth"]) == case["missing_auth_variable"]
    assert set(environment) <= set(host.ALLOWED_ENVIRONMENT_NAMES)


# Transcripts follow the contract.


@dataclass
class TranscriptChecker:
    """Walks a transcript and asserts every contract rule a message sequence can show."""

    transcript: Mapping[str, Any]
    launch: dict[str, Any] = field(default_factory=dict)
    next_request_id: int = 1
    pending: dict[int, dict[str, Any]] = field(default_factory=dict)
    is_busy_expected: dict[int, bool] = field(default_factory=dict)
    keyed_requests: dict[str, dict[str, Any]] = field(default_factory=dict)
    outcomes: dict[str, dict[str, Any]] = field(default_factory=dict)
    has_result: bool = False
    is_closed: bool = False

    def check(self) -> None:
        steps = self.transcript["steps"]
        verdict = classify_launch_line(
            self._launch_line(steps[0]), self.transcript["terminal_ui_environment"]
        )
        if verdict.code is not None:
            self._check_rejection(verdict, steps[1:])
            return
        self.launch = json.loads(self._launch_line(steps[0]))
        if self.transcript["tui_exit"] == "terminated":
            assert steps[1:] == [{"from": "tui", "silence_ms": steps[1]["silence_ms"]}]
            assert steps[1]["silence_ms"] >= host.READY_TIMEOUT_SECONDS * 1000
            return
        assert steps[1] == {"from": "tui", "message": {"type": "ready"}}
        for step in steps[2:]:
            self._check_step(step)
        assert not self.pending, f"requests without a response: {sorted(self.pending)}"
        assert isinstance(self.transcript["tui_exit"], int)
        if self.is_closed:
            assert self.transcript["tui_exit"] == 1

    def _launch_line(self, step: Mapping[str, Any]) -> bytes:
        assert step["from"] == "launcher"
        if "raw_line" in step:
            line = str(step["raw_line"]).encode("utf-8")
            return padded_line(line, int(step.get("pad_to_bytes", 0)))
        return json.dumps(step["message"], separators=(",", ":")).encode("utf-8")

    def _check_rejection(self, verdict: LaunchVerdict, steps: list[dict[str, Any]]) -> None:
        assert len(steps) == 1 and steps[0]["from"] == "tui"
        rejection = steps[0]["message"]
        assert not schema_errors(FROM_VALIDATORS["tui"], rejection)
        assert rejection["type"] == "launch_rejected" and rejection["code"] == verdict.code
        assert self.transcript["tui_exit"] == verdict.exit_code

    def _check_step(self, step: Mapping[str, Any]) -> None:
        if "pause_ms" in step:
            return
        assert not self.is_closed, "nothing follows a close"
        if "close" in step:
            self.is_closed = True
            return
        message = step["message"]
        assert not schema_errors(FROM_VALIDATORS[step["from"]], message), message
        assert not self.has_result, "the result message is the last message"
        if step["from"] == "tui":
            self._check_terminal_ui_message(message)
        elif message["type"] == "response":
            self._check_response(message)

    def _check_terminal_ui_message(self, message: dict[str, Any]) -> None:
        if message["type"] == "request":
            self._check_request(message)
        elif message["type"] == "cancel":
            assert message["id"] in self.pending, "cancel only a request still waiting"
        elif message["type"] == "result":
            assert self.launch["mode"] == "sessions" and self.transcript["tui_exit"] == 0
            self.has_result = True
        else:
            raise AssertionError(f"{message['type']} after ready")

    def _check_request(self, request: dict[str, Any]) -> None:
        assert request["id"] == self.next_request_id
        self.next_request_id += 1
        key = request.get("idempotency_key")
        if key is not None:
            host.idempotency_key_time_ms(key)
            earlier = self.keyed_requests.setdefault(key, request)
            assert (earlier["method"], earlier["params"]) == (request["method"], request["params"])
        counted = [
            pending
            for pending in self.pending.values()
            if pending["method"] != "get_request_result"
        ]
        is_busy = (
            request["method"] != "get_request_result"
            and len(counted) >= host.MAX_IN_FLIGHT_REQUESTS
        )
        self.is_busy_expected[request["id"]] = is_busy
        self.pending[request["id"]] = request

    def _check_response(self, response: dict[str, Any]) -> None:
        request = self.pending.pop(response["id"])
        method = request["method"]
        error = response.get("error")
        is_served = method in self.launch["methods"]
        assert (error is not None and error["code"] == "busy") == self.is_busy_expected[
            request["id"]
        ]
        assert (error is not None and error["code"] == "unknown_method") == (not is_served)
        if not is_served:
            return
        if error is not None:
            assert error["code"] in COMMON_ERROR_CODES | METHOD_ERROR_CODES[method], error
        else:
            result_errors = schema_errors(channel_validator(f"{method}_result"), response["result"])
            assert not result_errors, result_errors
        if method == "get_request_result":
            self._check_request_result(request, response)
            return
        if error is None or error["code"] not in ("busy", "cancelled"):
            outcome = {key: response[key] for key in ("result", "error") if key in response}
            assert self.outcomes.setdefault(request["idempotency_key"], outcome) == outcome
        if method == "start_session" and error is None:
            harness = self.launch["command"]["harness"]
            derived = host.derive_session_id(harness, request["idempotency_key"])
            assert response["result"]["session_id"] == derived
        if method == "open_url":
            is_allowed = host.is_open_url_allowed(
                self.launch["dex"]["web_url"], request["params"]["url"]
            )
            assert is_allowed == (error is None or error["code"] != "refused")

    def _check_request_result(self, request: dict[str, Any], response: dict[str, Any]) -> None:
        if "result" not in response:
            return
        key = request["params"]["idempotency_key"]
        original = self.keyed_requests[key]
        stored = response["result"]
        if stored["state"] == "done" and "result" in stored:
            original_method = original["method"]
            assert not schema_errors(
                channel_validator(f"{original_method}_result"), stored["result"]
            )


@pytest.mark.parametrize("path", TRANSCRIPT_PATHS, ids=lambda path: path.stem)
def test_transcripts_follow_the_contract(path: Path) -> None:
    transcript = host.load_transcript(path)
    assert transcript["name"] == path.stem
    assert transcript["real_roles"] and set(transcript["real_roles"]) <= {"launcher", "tui"}
    TranscriptChecker(transcript).check()


# The contract host plays both roles.


def run_host_pair(
    tmp_path: Path,
    launcher_transcript: Path,
    terminal_ui_transcript: Path | None = None,
    launcher_options: list[str] | None = None,
    environment: Mapping[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    terminal_ui_command = [
        sys.executable,
        str(HOST_PATH),
        "tui",
        "--transcript",
        str(terminal_ui_transcript or launcher_transcript),
        "--record",
        str(tmp_path / "tui.jsonl"),
        "agents-tui",
    ]
    launcher_command = [
        sys.executable,
        str(HOST_PATH),
        "launcher",
        "--transcript",
        str(launcher_transcript),
        "--record",
        str(tmp_path / "launcher.jsonl"),
        *(launcher_options or []),
        "--",
        *terminal_ui_command,
    ]
    return subprocess.run(
        launcher_command,
        capture_output=True,
        text=True,
        timeout=60,
        env=dict(environment) if environment is not None else None,
        check=False,
    )


def read_record(path: Path) -> list[dict[str, Any]]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]


@pytest.mark.parametrize("path", TRANSCRIPT_PATHS, ids=lambda path: path.stem)
def test_transcript_replays_between_the_two_roles(path: Path, tmp_path: Path) -> None:
    completed = run_host_pair(tmp_path, path)
    assert completed.returncode == 0, completed.stderr
    for event in read_record(tmp_path / "launcher.jsonl") + read_record(tmp_path / "tui.jsonl"):
        assert event["event"] != "mismatch", event


def test_launcher_role_reports_a_terminal_ui_that_differs(tmp_path: Path) -> None:
    transcript = host.load_transcript(CONFORMANCE_DIR / "transcripts" / "chat_put_image.json")
    transcript["steps"][2]["message"]["params"] = {}
    transcript["steps"][2]["ignore"] = ["idempotency_key"]
    changed = tmp_path / "changed.json"
    changed.write_text(json.dumps(transcript), encoding="utf-8")
    completed = run_host_pair(
        tmp_path, changed, CONFORMANCE_DIR / "transcripts" / "chat_put_image.json"
    )
    assert completed.returncode == 1
    assert "expected" in completed.stderr


def test_terminal_ui_role_reports_a_launcher_that_differs(tmp_path: Path) -> None:
    original = CONFORMANCE_DIR / "transcripts" / "chat_put_image.json"
    transcript = host.load_transcript(original)
    transcript["steps"][3]["message"]["result"]["name"] = "other.png"
    changed = tmp_path / "changed.json"
    changed.write_text(json.dumps(transcript), encoding="utf-8")
    completed = run_host_pair(tmp_path, changed, original)
    assert completed.returncode == 1
    events = read_record(tmp_path / "tui.jsonl")
    assert [event["event"] for event in events][-1] == "mismatch"
    assert "other.png" in events[-1]["detail"]


def test_launcher_role_passes_only_the_allow_list(tmp_path: Path) -> None:
    transcript = CONFORMANCE_DIR / "transcripts" / "launch_auth_missing.json"
    environment = {
        "TERM": "xterm-256color",
        "LANG": "en_US.UTF-8",
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "OPENAI_API_KEY": "test-openai-key-not-a-secret",
        "DEX_API_KEY": "test-api-key-not-a-secret",
        "DEX_CREDENTIAL": "test-credential-not-a-secret",
        "DEX_TOKEN_URL": "https://auth.example.com/oauth/token",
    }
    completed = run_host_pair(tmp_path, transcript, environment=environment)
    assert completed.returncode == 0, completed.stderr
    start = read_record(tmp_path / "tui.jsonl")[0]
    assert start["event"] == "start"
    # macOS adds __CF_USER_TEXT_ENCODING to every process it starts.
    names = [name for name in start["environment_names"] if name != "__CF_USER_TEXT_ENCODING"]
    assert names == ["DEX_API_KEY", "LANG", "TERM"]
    assert start["allowed_environment"]["DEX_API_KEY"] == "test-api-key-not-a-secret"


def test_launcher_role_runs_the_terminal_ui_on_a_pty_with_a_high_channel_fd(tmp_path: Path) -> None:
    transcript = CONFORMANCE_DIR / "transcripts" / "chat_ready_then_exit.json"
    completed = run_host_pair(
        tmp_path,
        transcript,
        launcher_options=["--pty", "100x30", "--screen-log", str(tmp_path / "screen.log")],
    )
    assert completed.returncode == 0, completed.stderr
    start = read_record(tmp_path / "tui.jsonl")[0]
    assert start["terminal"] == {"columns": 100, "rows": 30, "is_foreground": True}
    channel_fd = int(start["argv"][start["argv"].index("--channel-fd") + 1])
    assert channel_fd >= 10


def test_terminal_ui_role_answers_describe() -> None:
    completed = subprocess.run(
        [sys.executable, str(HOST_PATH), "tui", "agents-tui", "--describe"],
        capture_output=True,
        text=True,
        timeout=30,
        check=True,
    )
    lines = completed.stdout.splitlines()
    assert len(lines) == 1
    assert json.loads(lines[0]) == host.DEFAULT_DESCRIBE


# Redaction of the fixtures.

FORBIDDEN_SHAPES = {
    "macOS home": re.compile(r"/Users/"),
    "Windows home": re.compile(r"[A-Za-z]:\\\\Users", re.IGNORECASE),
    "home other than /home/user": re.compile(r"/home/(?!user(?:/|\"|$))"),
    "JWT": re.compile(r"[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}"),
    "OpenAI or Anthropic key": re.compile(r"\bsk-[A-Za-z0-9_-]{16,}"),
    "AWS access key": re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b"),
    "GitHub token": re.compile(r"\bgh[pousr]_[A-Za-z0-9]{20,}"),
    "Slack token": re.compile(r"\bxox[abpr]-[A-Za-z0-9-]{10,}"),
    "Google API key": re.compile(r"\bAIza[0-9A-Za-z_-]{35}"),
    "bearer header": re.compile(r"Bearer [A-Za-z0-9._~+/-]{16,}"),
}
REDACTED_FILES = sorted(CONFORMANCE_DIR.rglob("*.json")) + sorted(
    CLI_DIR.glob("terminal-ui-*.json")
)


@pytest.mark.parametrize("path", REDACTED_FILES, ids=lambda path: path.name)
def test_fixtures_hold_no_real_paths_or_tokens(path: Path) -> None:
    text = path.read_text(encoding="utf-8")
    for shape, pattern in FORBIDDEN_SHAPES.items():
        match = pattern.search(text)
        assert match is None, f"{path.name} holds a {shape}: {match.group(0) if match else ''}"
    home = os.environ.get("HOME", "")
    if home and home not in ("/", "/home/user", "/root"):
        assert home not in text, f"{path.name} holds this machine's home directory"
