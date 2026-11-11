# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The framework suite: the order support agent harness examples against the mock model API.

Each example app runs every framework's agent behind HTTP session routes.
Per framework, one session looks up an order (a read that needs no
approval) and cancels another after approval; a second session's
cancellation is rejected. Each framework gets its own model name, so the
mock's request log tells the frameworks apart. The sessions are then read
back from the Dex server.
"""

from __future__ import annotations

import json
import os
import shutil
import signal
import socket
import subprocess
import time
import urllib.error
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from dex_agents_cli_testing.e2e.command_cases import (
    MOCK_ANTHROPIC_API_KEY,
    MOCK_GOOGLE_API_KEY,
    MOCK_OPENAI_API_KEY,
)
from dex_agents_cli_testing.e2e.command_suite import UNREACHABLE_PROXY
from dex_agents_cli_testing.e2e.dex_server import DexServer, SessionRecord
from dex_agents_cli_testing.e2e.results import ResultTable, StepStatus
from dex_agents_cli_testing.e2e.toolchain import BuildFailure, PackageSource, Toolchain
from dex_agents_cli_testing.mock_model_api import MockModelApi
from dex_agents_cli_testing.model_scenario import ScenarioFile

FRAMEWORK_STEPS = (
    "build",
    "start-app",
    "a-lookup",
    "b-cancel-approve",
    "c-cancel-reject",
    "model-requests",
    "dex-history",
)
LOOKUP_PROMPT = "Look up order 1001"
APPROVED_CANCEL_PROMPT = "Please cancel order 1002"
REJECTED_CANCEL_PROMPT = "Cancel order 1003"
SINGLE_ATTEMPT_TOOL_STEP = "CallSingleAttemptTool"
_TURN_TIMEOUT_SECONDS = 180.0
_APP_READY_SECONDS = 300.0
_PYTHON_ADAPTER_DIRS = (
    "core[connectors]",
    "claude-agent-sdk",
    "google-adk",
    "langgraph",
    "openai-agents",
    "pydantic-ai",
)
_TYPESCRIPT_ADAPTER_DIRS = ("core", "claude-agent-sdk", "openai-agents", "vercel-ai", "mastra")


@dataclass(frozen=True)
class FrameworkCase:
    """One framework's order support agent.

    Attributes:
        name: The case name, such as ``py-openai-agents``.
        language: ``python`` or ``typescript``.
        harness: The route segment, such as ``openai-agents``.
        flow_type: The session Flow type.
        model_variable: The variable that picks the model, or ``None``.
        model: The model name, unique per framework so the request log tells them apart.
        skip_reason: Why the case cannot run against the mock, or ``None``.
    """

    name: str
    language: str
    harness: str
    flow_type: str
    model_variable: str | None
    model: str
    skip_reason: str | None = None


FRAMEWORK_CASES: tuple[FrameworkCase, ...] = (
    FrameworkCase(
        "py-builtin-loop",
        "python",
        "builtin",
        "",
        None,
        "",
        "no order support example on the built-in loop: examples/python/agent-harnesses has "
        "Claude Agent SDK, OpenAI Agents, Pydantic AI, LangChain, and Google ADK only",
    ),
    FrameworkCase(
        "py-claude-agent-sdk",
        "python",
        "claude-agent-sdk",
        "ClaudeAgentSDKOrderSupport",
        "DEX_CLAUDE_AGENT_SDK_MODEL",
        "claude-opus-5-5",
    ),
    FrameworkCase(
        "py-openai-agents",
        "python",
        "openai-agents",
        "OpenAIAgentsOrderSupport",
        "DEX_OPENAI_AGENTS_MODEL",
        "e2e-py-openai-agents",
    ),
    FrameworkCase(
        "py-pydantic-ai",
        "python",
        "pydantic-ai",
        "PydanticAIOrderSupport",
        "DEX_PYDANTIC_AI_MODEL",
        "openai:e2e-py-pydantic-ai",
    ),
    FrameworkCase(
        "py-langchain",
        "python",
        "langchain",
        "LangChainOrderSupport",
        "DEX_LANGCHAIN_MODEL",
        "openai:e2e-py-langchain",
    ),
    FrameworkCase(
        "py-google-adk",
        "python",
        "google-adk",
        "GoogleADKOrderSupport",
        "DEX_GOOGLE_ADK_MODEL",
        "gemini-2.5-flash",
    ),
    FrameworkCase(
        "ts-builtin-loop",
        "typescript",
        "builtin",
        "",
        None,
        "",
        "no order support example on the built-in loop: "
        "examples/typescript/src/products/agent-harnesses has Claude Agent SDK, OpenAI Agents, "
        "Vercel AI, and Mastra only",
    ),
    FrameworkCase(
        "ts-claude-agent-sdk",
        "typescript",
        "claude-agent-sdk",
        "ClaudeAgentSdkOrderSupportAgent",
        "DEX_CLAUDE_AGENT_SDK_MODEL",
        "claude-opus-5-5",
        "buildClaudeCliEnvironment (claude-agent-sdk-order-support-flow.ts) gives the Claude Code "
        "CLI only ANTHROPIC_API_KEY, PATH, HOME, TMPDIR, and the proxy variables, so "
        "ANTHROPIC_BASE_URL never reaches it and it cannot be pointed at the mock",
    ),
    FrameworkCase(
        "ts-openai-agents",
        "typescript",
        "openai-agents",
        "OpenAIAgentsOrderSupportAgent",
        "DEX_OPENAI_AGENTS_MODEL",
        "e2e-ts-openai-agents",
    ),
    FrameworkCase(
        "ts-vercel-ai",
        "typescript",
        "vercel-ai",
        "VercelAiOrderSupportAgent",
        "DEX_VERCEL_AI_MODEL",
        "e2e-ts-vercel-ai",
    ),
    FrameworkCase(
        "ts-mastra",
        "typescript",
        "mastra",
        "MastraOrderSupportAgent",
        "DEX_MASTRA_MODEL",
        "openai/gpt-5-mini",
    ),
    FrameworkCase(
        "pi",
        "typescript",
        "pi",
        "",
        None,
        "",
        "no Pi agent harness example exists",
    ),
)


class SessionRoutes:
    """The HTTP session routes of one framework; Python and TypeScript differ in shape."""

    def __init__(self, base_url: str, case: FrameworkCase) -> None:
        """Bind the routes of one framework under the app's base URL."""
        self._base = f"{base_url}/products/agent-harnesses/{case.harness}"
        self._is_python = case.language == "python"
        self._tokens: dict[str, str] = {}

    def start_session(self) -> str:
        """Start a session and keep its token."""
        started = self._call("POST", "/sessions", {})
        session_id = str(started["session_id"])
        self._tokens[session_id] = str(started["session_token"])
        return session_id

    def send_message(self, session_id: str, text: str) -> None:
        """Send a user message."""
        if self._is_python:
            self._call("POST", "/messages", {"session_id": session_id, "text": text}, session_id)
        else:
            self._call("POST", f"/sessions/{session_id}/messages", {"text": text}, session_id)

    def resolve_interrupt(self, session_id: str, interrupt_id: str, is_approved: bool) -> None:
        """Approve, or reject with a reason, one pending tool call."""
        reason = "the customer changed their mind"
        if self._is_python:
            body: dict[str, Any] = {
                "session_id": session_id,
                "interrupt_id": interrupt_id,
                "approved": is_approved,
            }
            if not is_approved:
                body["rejection_message"] = reason
            self._call("POST", "/interrupts/resolve", body, session_id)
        else:
            action = "approve" if is_approved else "reject"
            self._call(
                "POST",
                f"/sessions/{session_id}/interrupts/{interrupt_id}/{action}",
                {} if is_approved else {"reason": reason},
                session_id,
            )

    def read_snapshot(self, session_id: str) -> Mapping[str, Any]:
        """Return the session's snapshot JSON."""
        if self._is_python:
            wrapped = self._call("GET", f"/snapshot?session_id={session_id}", None, session_id)
            return dict(wrapped["snapshot"])
        return dict(self._call("GET", f"/sessions/{session_id}/snapshot", None, session_id))

    def _call(
        self,
        method: str,
        path: str,
        body: Mapping[str, Any] | None,
        session_id: str | None = None,
    ) -> Any:
        headers = {"Content-Type": "application/json"}
        if session_id is not None:
            headers["X-Agent-Session-Token"] = self._tokens[session_id]
        request = urllib.request.Request(
            self._base + path,
            data=None if body is None else json.dumps(body).encode(),
            headers=headers,
            method=method,
        )
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                return json.loads(response.read() or b"{}")
        except urllib.error.HTTPError as error:
            detail = error.read().decode("utf-8", errors="replace")[:500]
            raise RuntimeError(f"{method} {path} answered {error.code}: {detail}") from error


@dataclass
class ExampleApp:
    """One running example app process and its mock model API."""

    language: str
    process: subprocess.Popen[bytes]
    base_url: str
    log_path: Path
    mock: MockModelApi
    request_log_path: Path

    def stop(self) -> None:
        """Stop the app's process group and the mock."""
        if self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
        self.mock.stop()


def run_framework_suite(
    cases: Sequence[FrameworkCase], toolchain: Toolchain, server: DexServer, table: ResultTable
) -> None:
    """Run every framework case, one example app per language."""
    for case in cases:
        if case.skip_reason is not None:
            for step in FRAMEWORK_STEPS:
                table.record(case.name, step, StepStatus.SKIP, case.skip_reason)
    for language in ("python", "typescript"):
        runnable = [
            case for case in cases if case.language == language and case.skip_reason is None
        ]
        if runnable:
            _run_language(language, runnable, toolchain, server, table)


def _run_language(
    language: str,
    cases: Sequence[FrameworkCase],
    toolchain: Toolchain,
    server: DexServer,
    table: ResultTable,
) -> None:
    try:
        argv, cwd = _build_example_app(language, toolchain)
    except BuildFailure as failure:
        for case in cases:
            table.record(case.name, "build", StepStatus.FAIL, str(failure))
        return
    for case in cases:
        table.record(case.name, "build", StepStatus.PASS, " ".join(argv))
    try:
        app = _start_example_app(language, argv, cwd, cases, toolchain, server)
    except Exception as error:
        for case in cases:
            table.record(case.name, "start-app", StepStatus.FAIL, str(error))
        return
    try:
        for case in cases:
            table.record(case.name, "start-app", StepStatus.PASS, app.base_url)
            FrameworkRun(case, app, server, table).run()
    finally:
        app.stop()


def _build_example_app(language: str, toolchain: Toolchain) -> tuple[list[str], Path]:
    examples_dir = toolchain.checkout / "examples"
    agents_dir = toolchain.checkout / "agents"
    if language == "python":
        project_dir = examples_dir / "python" / "agent-harnesses"
        venv = toolchain.work_dir / "build" / toolchain.source.value / "examples-python"
        environment = toolchain.build_environment({"UV_PROJECT_ENVIRONMENT": str(venv)})
        log_name = f"build-examples-python-{toolchain.source.value}"
        toolchain.run_logged(
            ["uv", "sync", "--locked", "--python", toolchain.python_version],
            project_dir,
            log_name,
            environment,
        )
        if toolchain.source is PackageSource.LOCAL:
            editable_arguments: list[str] = []
            for directory in _PYTHON_ADAPTER_DIRS:
                editable_arguments += ["-e", str(agents_dir / "python" / directory)]
            toolchain.run_logged(
                [
                    "uv",
                    "pip",
                    "install",
                    "--python",
                    str(venv / "bin" / "python"),
                    *editable_arguments,
                ],
                project_dir,
                log_name,
                environment,
            )
        return [str(venv / "bin" / "python"), "-m", "agent_harness_examples"], project_dir
    project_dir = examples_dir / "typescript"
    log_name = f"build-examples-typescript-{toolchain.source.value}"
    toolchain.run_logged(["npm", "ci", "--no-fund", "--no-audit"], project_dir, log_name)
    if toolchain.source is PackageSource.LOCAL:
        for directory in _TYPESCRIPT_ADAPTER_DIRS:
            toolchain.build_npm_package(agents_dir / "typescript" / directory)
        toolchain.run_logged(
            [
                "npm",
                "install",
                "--no-save",
                "--install-links",
                "--no-fund",
                "--no-audit",
                *(
                    str(agents_dir / "typescript" / directory)
                    for directory in _TYPESCRIPT_ADAPTER_DIRS
                ),
            ],
            project_dir,
            log_name,
        )
    toolchain.run_logged(["npm", "run", "build"], project_dir, log_name)
    return ["node", str(project_dir / "dist" / "src" / "main.js")], project_dir


def _start_example_app(
    language: str,
    argv: Sequence[str],
    cwd: Path,
    cases: Sequence[FrameworkCase],
    toolchain: Toolchain,
    server: DexServer,
) -> ExampleApp:
    app_dir = toolchain.work_dir / "frameworks" / toolchain.source.value / language
    shutil.rmtree(app_dir, ignore_errors=True)
    home = app_dir / "home"
    home.mkdir(parents=True)
    scenario_path = app_dir / "scenario.json"
    scenario_path.write_text(json.dumps(_order_support_scenario(), indent=2) + "\n")
    request_log_path = app_dir / "model-requests.jsonl"
    mock = MockModelApi(ScenarioFile(scenario_path), request_log_path)
    mock.start()
    port = _free_port()
    environment = {
        name: os.environ[name]
        for name in ("LANG", "LC_ALL", "TMPDIR", "USER", "LOGNAME")
        if name in os.environ
    }
    environment.update(
        PATH=toolchain.search_path(),
        HOME=str(home),
        CLAUDE_CONFIG_DIR=str(home / ".claude"),
        DEX_FLOW_SERVICE_ADDRESS=server.address,
        DEX_BLOB_CACHE_DIR=str(app_dir / "blob-cache"),
        HTTPS_PROXY=UNREACHABLE_PROXY,
        HTTP_PROXY=UNREACHABLE_PROXY,
        https_proxy=UNREACHABLE_PROXY,
        http_proxy=UNREACHABLE_PROXY,
        NO_PROXY="127.0.0.1,localhost",
        no_proxy="127.0.0.1,localhost",
        PYTHONUNBUFFERED="1",
        CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK="1",
        CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
        MASTRA_TELEMETRY_DISABLED="1",
        OPENAI_AGENTS_DISABLE_TRACING="1",
        OPENAI_API_KEY=MOCK_OPENAI_API_KEY,
        OPENAI_BASE_URL=f"{mock.base_url}/v1",
        GOOGLE_API_KEY=MOCK_GOOGLE_API_KEY,
        GOOGLE_GEMINI_BASE_URL=mock.base_url,
    )
    if language == "python":
        environment.update(
            ANTHROPIC_API_KEY=MOCK_ANTHROPIC_API_KEY,
            ANTHROPIC_BASE_URL=mock.base_url,
            DEX_AGENT_HARNESS_HTTP_ADDRESS=f"127.0.0.1:{port}",
        )
    else:
        environment["DEX_EXAMPLES_HTTP_ADDRESS"] = f"127.0.0.1:{port}"
    for case in cases:
        if case.model_variable is not None:
            environment[case.model_variable] = case.model
    log_path = app_dir / "app.log"
    with log_path.open("ab") as log:
        process = subprocess.Popen(
            list(argv),
            cwd=cwd,
            env=environment,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
    app = ExampleApp(
        language, process, f"http://127.0.0.1:{port}", log_path, mock, request_log_path
    )
    deadline = time.monotonic() + _APP_READY_SECONDS
    while time.monotonic() < deadline:
        if process.poll() is not None:
            app.stop()
            raise RuntimeError(
                f"the app exited with {process.returncode}; log {log_path}: {_tail(log_path)}"
            )
        if "listening on" in log_path.read_text(errors="replace"):
            return app
        time.sleep(1)
    app.stop()
    raise RuntimeError(f"the app did not start; log {log_path}: {_tail(log_path)}")


def _order_support_scenario() -> Mapping[str, Any]:
    def turn(
        prompt: str, tool: str, arguments: Mapping[str, Any], marker: str
    ) -> Mapping[str, Any]:
        return {
            "prompt": prompt,
            "steps": [
                {"tool_calls": [{"name": tool, "arguments": arguments}]},
                {"text": f"{marker} {{tool_results}}"},
            ],
        }

    reason = {"reason": "requested by the customer"}
    return {
        "turns": [
            turn(LOOKUP_PROMPT, "look_up_order", {"order_id": "1001"}, "E2E-LOOKUP"),
            turn(
                APPROVED_CANCEL_PROMPT, "cancel_order", {"order_id": "1002", **reason}, "E2E-CANCEL"
            ),
            turn(
                REJECTED_CANCEL_PROMPT,
                "cancel_order",
                {"order_id": "1003", **reason},
                "E2E-REJECTED",
            ),
        ]
    }


@dataclass
class FrameworkRun:
    """One framework case's sessions and checks."""

    case: FrameworkCase
    app: ExampleApp
    server: DexServer
    table: ResultTable

    def record(self, step: str, status: StepStatus, detail: str = "") -> None:
        """Record one step result for this case."""
        self.table.record(self.case.name, step, status, detail)

    def run(self) -> None:
        """Run both sessions, then check the request log and the Dex history."""
        routes = SessionRoutes(self.app.base_url, self.case)
        approved_session = self._run_approved_session(routes)
        rejected_session = self._run_rejected_session(routes)
        self._check_model_requests()
        self._check_dex_history(approved_session, rejected_session)

    def _run_approved_session(self, routes: SessionRoutes) -> str | None:
        try:
            session_id = routes.start_session()
            routes.send_message(session_id, LOOKUP_PROMPT)
            snapshot = _wait_for_turn(routes, session_id, 1)
            reply = _last_assistant_text(snapshot)
            problems = _turn_problems(snapshot, reply, "E2E-LOOKUP", "1001")
            if snapshot.get("pending_interrupts"):
                problems.append("the read asked for approval")
            self._record_problems("a-lookup", problems, f"{session_id}: {reply[:120]!r}")
        except Exception as error:
            self.record("a-lookup", StepStatus.FAIL, f"{type(error).__name__}: {error}")
            self.record("b-cancel-approve", StepStatus.SKIP, "the session did not start")
            return None
        try:
            routes.send_message(session_id, APPROVED_CANCEL_PROMPT)
            interrupt = _wait_for_interrupt(routes, session_id)
            if interrupt is None:
                self.record(
                    "b-cancel-approve", StepStatus.FAIL, _snapshot_detail(routes, session_id)
                )
                return session_id
            routes.resolve_interrupt(session_id, str(interrupt["interrupt_id"]), is_approved=True)
            snapshot = _wait_for_turn(routes, session_id, 2)
            reply = _last_assistant_text(snapshot)
            problems = _turn_problems(snapshot, reply, "E2E-CANCEL", "cancelled")
            self._record_problems(
                "b-cancel-approve",
                problems,
                f"approval for {interrupt.get('tool_name')}; reply {reply[:120]!r}",
            )
        except Exception as error:
            self.record("b-cancel-approve", StepStatus.FAIL, f"{type(error).__name__}: {error}")
        return session_id

    def _run_rejected_session(self, routes: SessionRoutes) -> str | None:
        try:
            session_id = routes.start_session()
            routes.send_message(session_id, REJECTED_CANCEL_PROMPT)
            interrupt = _wait_for_interrupt(routes, session_id)
            if interrupt is None:
                self.record(
                    "c-cancel-reject", StepStatus.FAIL, _snapshot_detail(routes, session_id)
                )
                return session_id
            routes.resolve_interrupt(session_id, str(interrupt["interrupt_id"]), is_approved=False)
            snapshot = _wait_for_turn(routes, session_id, 1)
            reply = _last_assistant_text(snapshot)
            problems = _turn_problems(snapshot, reply, "E2E-REJECTED", "")
            if "is cancelled" in reply:
                problems.append(f"the rejected cancellation ran: {reply[:200]!r}")
            self._record_problems("c-cancel-reject", problems, f"{session_id}: {reply[:120]!r}")
            return session_id
        except Exception as error:
            self.record("c-cancel-reject", StepStatus.FAIL, f"{type(error).__name__}: {error}")
            return None

    def _check_model_requests(self) -> None:
        entries = []
        if self.app.request_log_path.exists():
            with self.app.request_log_path.open(encoding="utf-8") as log:
                entries = [json.loads(line) for line in log]
        mine = [entry for entry in entries if _is_model_of(entry.get("model"), self.case.model)]
        if not mine:
            self.record(
                "model-requests", StepStatus.FAIL, f"no request for model {self.case.model}"
            )
            return
        results = {
            prompt: [
                result["output"]
                for entry in mine
                if entry.get("prompt") == prompt
                for result in entry.get("tool_results") or []
            ]
            for prompt in (LOOKUP_PROMPT, APPROVED_CANCEL_PROMPT, REJECTED_CANCEL_PROMPT)
        }
        missing = [prompt for prompt, outputs in results.items() if not outputs]
        apis = sorted({str(entry.get("api")) for entry in mine})
        if missing:
            self.record(
                "model-requests", StepStatus.FAIL, f"no tool result reached the model for {missing}"
            )
        else:
            rejected = str(results[REJECTED_CANCEL_PROMPT][0])[:120]
            self.record(
                "model-requests",
                StepStatus.PASS,
                f"{len(mine)} requests over {apis}; the rejected call returned {rejected!r}",
            )

    def _check_dex_history(
        self, approved_session: str | None, rejected_session: str | None
    ) -> None:
        problems = []
        details = []
        for session_id, expected_single_attempts in ((approved_session, 1), (rejected_session, 0)):
            if session_id is None:
                problems.append("a session did not start")
                continue
            try:
                record = SessionRecord.read(self.server, session_id)
            except Exception as error:
                problems.append(f"{session_id}: {type(error).__name__}: {error}")
                continue
            if record.flow_type != self.case.flow_type:
                problems.append(f"{session_id} has Flow type {record.flow_type}")
            single_attempts = record.step_type_counts.get(SINGLE_ATTEMPT_TOOL_STEP, 0)
            if single_attempts != expected_single_attempts:
                problems.append(
                    f"{session_id} ran {SINGLE_ATTEMPT_TOOL_STEP} {single_attempts} times, "
                    f"not {expected_single_attempts}"
                )
            details.append(record.describe())
        self._record_problems("dex-history", problems, " || ".join(details))

    def _record_problems(self, step: str, problems: Sequence[str], passing_detail: str) -> None:
        if problems:
            self.record(step, StepStatus.FAIL, "; ".join(problems))
        else:
            self.record(step, StepStatus.PASS, passing_detail)


def _wait_for_turn(routes: SessionRoutes, session_id: str, turn_count: int) -> Mapping[str, Any]:
    deadline = time.monotonic() + _TURN_TIMEOUT_SECONDS
    snapshot: Mapping[str, Any] = {}
    while time.monotonic() < deadline:
        snapshot = routes.read_snapshot(session_id)
        state = snapshot.get("state") or {}
        if int(state.get("turn_count") or 0) >= turn_count and not state.get("active_turn_id"):
            return snapshot
        time.sleep(1)
    raise TimeoutError(f"turn {turn_count} did not end; state {snapshot.get('state')}")


def _wait_for_interrupt(routes: SessionRoutes, session_id: str) -> Mapping[str, Any] | None:
    deadline = time.monotonic() + _TURN_TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        snapshot = routes.read_snapshot(session_id)
        for interrupt in snapshot.get("pending_interrupts") or []:
            if interrupt.get("tool_name", "").endswith("cancel_order"):
                return dict(interrupt)
        state = snapshot.get("state") or {}
        if state.get("last_turn_error"):
            return None
        time.sleep(1)
    return None


def _turn_problems(
    snapshot: Mapping[str, Any], reply: str, marker: str, expected_text: str
) -> list[str]:
    problems = []
    state = snapshot.get("state") or {}
    if state.get("last_turn_error"):
        problems.append(f"turn error: {state['last_turn_error']}")
    if marker not in reply:
        problems.append(f"the reply lacks {marker}: {reply[:300]!r}")
    if expected_text and expected_text not in reply:
        problems.append(f"the reply lacks {expected_text!r}: {reply[:300]!r}")
    return problems


def _last_assistant_text(snapshot: Mapping[str, Any]) -> str:
    texts = [
        str(message.get("text") or "")
        for message in snapshot.get("messages") or []
        if message.get("role") == "assistant" and message.get("text")
    ]
    return texts[-1] if texts else ""


def _snapshot_detail(routes: SessionRoutes, session_id: str) -> str:
    snapshot = routes.read_snapshot(session_id)
    return (
        f"no pending cancel_order approval; state {snapshot.get('state')}; "
        f"last reply {_last_assistant_text(snapshot)[:200]!r}"
    )


def _is_model_of(logged_model: object, configured_model: str) -> bool:
    if not isinstance(logged_model, str):
        return False
    bare_model = configured_model.split(":", 1)[-1].split("/", 1)[-1]
    return logged_model in (configured_model, bare_model) or logged_model.endswith(bare_model)


def _free_port() -> int:
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return int(probe.getsockname()[1])


def _tail(path: Path) -> str:
    return path.read_text(errors="replace")[-2000:] if path.exists() else ""
