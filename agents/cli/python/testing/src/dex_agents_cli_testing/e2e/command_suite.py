# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command suite: one chat scenario per agent CLI command against the mock model API.

The scenario reads a workspace file, writes one file after approval and is
refused another, runs an allowed shell command and one that asks, calls a
Hacker News connector tool when a connection is authorized, then exits and
resumes the session for one more turn. Every step checks the terminal, the
workspace, and the mock's request log; the session is then read back from
the Dex server.
"""

from __future__ import annotations

import json
import os
import re
import shutil
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from dex_agents_cli_testing.e2e.command_cases import CommandCase
from dex_agents_cli_testing.e2e.dex_server import ConnectorTool, DexServer, SessionRecord
from dex_agents_cli_testing.e2e.results import ResultTable, StepStatus
from dex_agents_cli_testing.e2e.terminal_chat import TerminalChat, TurnOutcome
from dex_agents_cli_testing.e2e.toolchain import BuildFailure, Toolchain
from dex_agents_cli_testing.mock_model_api import MockModelApi
from dex_agents_cli_testing.model_scenario import ScenarioFile

UNREACHABLE_PROXY = "http://127.0.0.1:9"
NOTES_TEXT = "buy milk and eggs"
APPROVED_TEXT = "approved by the e2e check"
SHELL_ALLOWED_COMMAND = "ls"
SHELL_ASKING_COMMAND = "echo off-list > shell.txt"
HACKER_NEWS_OPERATION = "list_story_ids"
COMMAND_STEPS = (
    "build",
    "start",
    "a-read",
    "b-write-approve",
    "c-write-reject",
    "d-shell-allowed",
    "d-shell-asks",
    "e-connector",
    "f-resume",
    "dex-session",
    "dex-graph",
)
_TURN_TIMEOUT_SECONDS = 180.0
_HOST_VARIABLES = ("LANG", "LC_ALL", "TMPDIR", "USER", "LOGNAME", "SHELL")


@dataclass(frozen=True)
class ScenarioPrompt:
    """One user prompt of the scenario and the marker its final reply starts with.

    Attributes:
        step: The step name.
        text: The prompt; the mock's scenario turn is keyed by it.
        marker: The final reply's first word.
    """

    step: str
    text: str
    marker: str


PROMPTS = {
    prompt.step: prompt
    for prompt in (
        ScenarioPrompt("a-read", "e2e step a: read the notes file", "E2E-A-DONE"),
        ScenarioPrompt("b-write-approve", "e2e step b: write the approved file", "E2E-B-DONE"),
        ScenarioPrompt("c-write-reject", "e2e step c: write the rejected file", "E2E-C-DONE"),
        ScenarioPrompt("d-shell-allowed", "e2e step d1: list the workspace", "E2E-D1-DONE"),
        ScenarioPrompt("d-shell-asks", "e2e step d2: run an unlisted command", "E2E-D2-DONE"),
        ScenarioPrompt("e-connector", "e2e step e: list Hacker News stories", "E2E-E-DONE"),
        ScenarioPrompt("f-resume", "e2e step f: recall the conversation", "E2E-F-DONE"),
    )
}


@dataclass(frozen=True)
class HackerNewsCall:
    """The Hacker News connector call of step (e).

    Attributes:
        tool: The connector tool.
        arguments: The call's arguments, with a feed and a limit.
    """

    tool: ConnectorTool
    arguments: Mapping[str, Any]

    @staticmethod
    def find(tools: Sequence[ConnectorTool]) -> HackerNewsCall | None:
        """Return the call when an authorized connection offers the operation."""
        for tool in tools:
            if tool.operation_name == HACKER_NEWS_OPERATION and "hacker" in tool.connector:
                properties = tool.input_schema.get("properties") or {}
                feed_schema = properties.get("feed") or {}
                feeds = feed_schema.get("enum") or ["top"]
                return HackerNewsCall(tool, {"feed": feeds[0], "limit": 3})
        return None


@dataclass
class CommandRun:
    """One command case's directories, mock model API, and environment."""

    case: CommandCase
    toolchain: Toolchain
    server: DexServer
    hacker_news: HackerNewsCall | None
    table: ResultTable

    def __post_init__(self) -> None:
        """Lay out a fresh case directory."""
        self.case_dir = (
            self.toolchain.work_dir / "cases" / self.toolchain.source.value / self.case.name
        )
        shutil.rmtree(self.case_dir, ignore_errors=True)
        self.home = self.case_dir / "home"
        self.workspace = self.case_dir / "workspace"
        self.config_path = self.case_dir / "config.yaml"
        self.scenario_path = self.case_dir / "scenario.json"
        self.request_log_path = self.case_dir / "model-requests.jsonl"
        self.transcript_path = self.case_dir / "terminal.log"
        self.home.mkdir(parents=True)
        self.workspace.mkdir(parents=True)
        self.session_id: str | None = None

    def record(self, step: str, status: StepStatus, detail: str = "") -> None:
        """Record one step result for this case."""
        self.table.record(self.case.name, step, status, detail)

    def run(self) -> None:
        """Build the command and run the whole scenario; later steps skip after a fatal one."""
        try:
            argv = self._build()
        except BuildFailure as failure:
            self.record("build", StepStatus.FAIL, str(failure))
            self._skip_rest("build", "the build failed")
            return
        self.record("build", StepStatus.PASS, " ".join(argv))
        self.scenario_path.write_text(json.dumps(self._scenario(), indent=2) + "\n")
        mock = MockModelApi(ScenarioFile(self.scenario_path), self.request_log_path)
        mock.start()
        try:
            self._write_config(mock.base_url)
            self._run_chat(argv, mock.base_url)
        finally:
            mock.stop()
        self._check_dex_server()

    def _build(self) -> list[str]:
        if self.case.is_python:
            return self.toolchain.build_python_command(
                self.case.package_dir_name, self.case.command
            )
        return self.toolchain.build_typescript_command(
            self.case.package_dir_name, self.case.adapter_dir_name, self.case.command
        )

    def _scenario(self) -> Mapping[str, Any]:
        tools = self.case.tools
        workspace = self.workspace.resolve()
        calls: dict[str, tuple[str, Mapping[str, Any]]] = {
            "a-read": (tools.read_tool, tools.read_arguments(workspace, "notes.txt")),
            "b-write-approve": (
                tools.write_tool,
                tools.write_arguments(workspace, "approved.txt", APPROVED_TEXT),
            ),
            "c-write-reject": (
                tools.write_tool,
                tools.write_arguments(workspace, "rejected.txt", "never written"),
            ),
            "d-shell-allowed": (tools.shell_tool, tools.shell_arguments(SHELL_ALLOWED_COMMAND)),
            "d-shell-asks": (tools.shell_tool, tools.shell_arguments(SHELL_ASKING_COMMAND)),
        }
        if self.hacker_news is not None:
            calls["e-connector"] = (self.hacker_news.tool.name, self.hacker_news.arguments)
        turns: list[Mapping[str, Any]] = [
            {
                "prompt": PROMPTS[step].text,
                "steps": [
                    {"tool_calls": [{"name": name, "arguments": arguments}]},
                    {"text": f"{PROMPTS[step].marker} {{tool_results}}"},
                ],
            }
            for step, (name, arguments) in calls.items()
        ]
        recall = PROMPTS["f-resume"]
        turns.append(
            {
                "prompt": recall.text,
                "steps": [{"text": f"{recall.marker} history: {{earlier_prompts}}"}],
            }
        )
        return {"turns": turns}

    def _write_config(self, base_url: str) -> None:
        connectors: list[Any] = []
        if self.hacker_news is not None:
            tool = self.hacker_news.tool
            connectors = [{f"{tool.connector}/{tool.connection}": {"tools": [tool.operation_name]}}]
        config: dict[str, Any] = {
            "dex": {"server": "auto"},
            "connectors": {"mcp": "auto", "enabled": connectors},
            "workspace": str(self.workspace),
            "local_tools": {
                "files": True,
                "shell": {"enabled": True, "allow": [SHELL_ALLOWED_COMMAND]},
                "web_fetch": False,
                "browser": False,
            },
            **self.case.provider.config_section(base_url),
        }
        # JSON is valid YAML 1.2.
        self.config_path.write_text(json.dumps(config, indent=2) + "\n", encoding="utf-8")
        (self.workspace / "notes.txt").write_text(NOTES_TEXT + "\n", encoding="utf-8")

    def _environment(self, base_url: str) -> dict[str, str]:
        environment = {name: os.environ[name] for name in _HOST_VARIABLES if name in os.environ}
        environment.update(
            PATH=self.toolchain.search_path(),
            HOME=str(self.home),
            DEXCLI=str(self.toolchain.dexcli),
            DEX_AGENTS_DEX_PORT=str(self.server.dex_port),
            DEX_AGENTS_WEB_PORT=str(self.server.web_port),
            HTTPS_PROXY=UNREACHABLE_PROXY,
            HTTP_PROXY=UNREACHABLE_PROXY,
            https_proxy=UNREACHABLE_PROXY,
            http_proxy=UNREACHABLE_PROXY,
            NO_PROXY="127.0.0.1,localhost",
            no_proxy="127.0.0.1,localhost",
            PYTHONUNBUFFERED="1",
            TERM="dumb",
            CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK="1",
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
            DISABLE_AUTOUPDATER="1",
            DISABLE_TELEMETRY="1",
            DISABLE_ERROR_REPORTING="1",
            MASTRA_TELEMETRY_DISABLED="1",
            OPENAI_AGENTS_DISABLE_TRACING="1",
        )
        environment.update(self.case.provider.environment(base_url))
        return environment

    def _spawn(self, argv: Sequence[str], base_url: str, *arguments: str) -> TerminalChat:
        return TerminalChat(
            [*argv, "--config", str(self.config_path), *arguments],
            self._environment(base_url),
            self.case_dir,
            self.transcript_path,
            _TURN_TIMEOUT_SECONDS,
        )

    def _run_chat(self, argv: Sequence[str], base_url: str) -> None:
        chat = self._spawn(argv, base_url)
        try:
            try:
                self.session_id = chat.wait_for_session()
            except Exception as error:
                self.record(
                    "start", StepStatus.FAIL, f"{type(error).__name__}; tail: {chat.tail()}"
                )
                self._skip_rest("start", "the command did not start a session")
                return
            self.record("start", StepStatus.PASS, f"session {self.session_id}")
            self._step_read(chat)
            self._step_write_approved(chat)
            self._step_write_rejected(chat)
            self._step_shell_allowed(chat)
            self._step_shell_asks(chat)
            self._step_connector(chat)
            exit_code, resume_id = chat.exit_chat()
        finally:
            chat.close()
        self._step_resume(argv, base_url, exit_code, resume_id)

    def _turn(
        self,
        chat: TerminalChat,
        step: str,
        approval_answer: str | None,
        before_answer: Callable[[], None] | None = None,
    ) -> TurnOutcome:
        prompt = PROMPTS[step]
        return chat.send_turn(prompt.text, prompt.marker, approval_answer, before_answer)

    def _step_read(self, chat: TerminalChat) -> None:
        outcome = self._turn(chat, "a-read", None)
        problems = self._turn_problems(outcome, "a-read", expects_approval=False)
        if NOTES_TEXT not in outcome.reply:
            problems.append(f"reply lacks the file text: {outcome.reply[:300]!r}")
        self._record_turn("a-read", problems, f"reply {outcome.reply[:120]!r}")

    def _step_write_approved(self, chat: TerminalChat) -> None:
        target = self.workspace / "approved.txt"
        written_early: list[str] = []

        def check_not_written() -> None:
            if target.exists():
                written_early.append("approved.txt existed before the approval")

        outcome = self._turn(chat, "b-write-approve", "y", check_not_written)
        problems = self._turn_problems(outcome, "b-write-approve", expects_approval=True)
        problems += written_early
        content = target.read_text(encoding="utf-8") if target.exists() else None
        if content is None or content.strip() != APPROVED_TEXT:
            problems.append(f"approved.txt holds {content!r}")
        self._record_turn("b-write-approve", problems, "written after approval")

    def _step_write_rejected(self, chat: TerminalChat) -> None:
        outcome = self._turn(chat, "c-write-reject", "n not now")
        problems = self._turn_problems(outcome, "c-write-reject", expects_approval=True)
        if (self.workspace / "rejected.txt").exists():
            problems.append("rejected.txt was written")
        results = self._logged_tool_results("c-write-reject")
        self._record_turn("c-write-reject", problems, f"model saw {results[:1]!r}")

    def _step_shell_allowed(self, chat: TerminalChat) -> None:
        outcome = self._turn(chat, "d-shell-allowed", None)
        problems = self._turn_problems(outcome, "d-shell-allowed", expects_approval=False)
        if "notes.txt" not in outcome.reply:
            problems.append(f"reply lacks the ls output: {outcome.reply[:300]!r}")
        self._record_turn("d-shell-allowed", problems, "ran without approval")

    def _step_shell_asks(self, chat: TerminalChat) -> None:
        target = self.workspace / "shell.txt"
        outcome = self._turn(chat, "d-shell-asks", "y")
        problems = self._turn_problems(outcome, "d-shell-asks", expects_approval=True)
        content = target.read_text(encoding="utf-8").strip() if target.exists() else None
        if content != "off-list":
            problems.append(f"shell.txt holds {content!r}")
        self._record_turn("d-shell-asks", problems, "asked, then ran after approval")

    def _step_connector(self, chat: TerminalChat) -> None:
        if self.hacker_news is None:
            self.record(
                "e-connector",
                StepStatus.SKIP,
                "the connectors MCP server lists no Hacker News list_story_ids tool: "
                "no Hacker News connection is authorized in this dexcli dev",
            )
            return
        outcome = self._turn(chat, "e-connector", None)
        problems = self._turn_problems(outcome, "e-connector", expects_approval=False)
        self._record_turn("e-connector", problems, f"reply {outcome.reply[:160]!r}")

    def _step_resume(
        self, argv: Sequence[str], base_url: str, exit_code: int, resume_id: str | None
    ) -> None:
        problems = []
        if exit_code != 0:
            problems.append(f"the first run exited with {exit_code}")
        if resume_id != self.session_id:
            problems.append(f"the exit line names {resume_id!r}, not {self.session_id!r}")
        chat = self._spawn(argv, base_url, "--resume", str(self.session_id))
        try:
            resumed_id = chat.wait_for_session()
            if resumed_id != self.session_id:
                problems.append(f"--resume opened {resumed_id}")
            prompt = PROMPTS["f-resume"]
            outcome = chat.send_turn(prompt.text, prompt.marker, None)
            if outcome.failure:
                problems.append(outcome.failure)
            if PROMPTS["a-read"].text not in outcome.reply:
                problems.append(f"the model did not get the history: {outcome.reply[:300]!r}")
            second_exit, _ = chat.exit_chat()
            if second_exit != 0:
                problems.append(f"the resumed run exited with {second_exit}")
        except Exception as error:
            problems.append(f"{type(error).__name__}; tail: {chat.tail()}")
        finally:
            chat.close()
        self._record_turn("f-resume", problems, "the resumed session kept its history")

    def _check_dex_server(self) -> None:
        if self.session_id is None:
            return
        try:
            record = SessionRecord.read(self.server, self.session_id)
        except Exception as error:
            self.record("dex-session", StepStatus.FAIL, f"{type(error).__name__}: {error}")
            self.record("dex-graph", StepStatus.SKIP, "the session could not be read")
            return
        problems = []
        if not re.fullmatch(r"DexAgents\w+CliSession-[a-z0-9]{6}", record.flow_type):
            problems.append(
                f"the Flow type {record.flow_type!r} is not a namespaced CLI session type"
            )
        missing = [
            prompt.text for prompt in self._sent_prompts() if prompt.text not in record.user_texts
        ]
        if missing:
            problems.append(f"user messages missing: {missing}")
        tool_names = {name for name, _ in record.tool_records}
        expected_tools = {
            self.case.tools.read_tool,
            self.case.tools.write_tool,
            self.case.tools.shell_tool,
        }
        if not expected_tools <= {name.rsplit("__", 1)[-1] for name in tool_names}:
            problems.append(
                f"tool records {sorted(tool_names)} lack some of {sorted(expected_tools)}"
            )
        self._record_turn("dex-session", problems, record.describe())
        graph = record.graph
        if graph is None:
            self.record("dex-graph", StepStatus.FAIL, f"no uploaded graph for {record.flow_type}")
        elif (graph.get("flow") or {}).get("name") != record.flow_type:
            self.record("dex-graph", StepStatus.FAIL, f"graph names {graph.get('flow')}")
        else:
            nodes = len(graph.get("nodes") or [])
            self.record(
                "dex-graph", StepStatus.PASS, f"schema {graph.get('schemaVersion')}, {nodes} nodes"
            )

    def _sent_prompts(self) -> list[ScenarioPrompt]:
        return [
            prompt
            for step, prompt in PROMPTS.items()
            if step != "e-connector" or self.hacker_news is not None
        ]

    def _turn_problems(self, outcome: TurnOutcome, step: str, expects_approval: bool) -> list[str]:
        problems = []
        if outcome.failure:
            problems.append(outcome.failure)
        if expects_approval and not outcome.approval_prompts:
            problems.append("no approval prompt")
        if not expects_approval and outcome.approval_prompts:
            problems.append(f"unexpected approval prompt: {outcome.approval_prompts[0][-200:]!r}")
        if not self._logged_tool_results(step):
            problems.append("the mock never received a tool result for this turn")
        return problems

    def _logged_tool_results(self, step: str) -> list[str]:
        if not self.request_log_path.exists():
            return []
        results = []
        with self.request_log_path.open(encoding="utf-8") as log:
            for line in log:
                entry = json.loads(line)
                if entry.get("prompt") == PROMPTS[step].text:
                    results += [
                        str(result["output"])[:200] for result in entry.get("tool_results") or []
                    ]
        return results

    def _record_turn(self, step: str, problems: Sequence[str], passing_detail: str) -> None:
        if problems:
            self.record(step, StepStatus.FAIL, "; ".join(problems))
        else:
            self.record(step, StepStatus.PASS, passing_detail)

    def _skip_rest(self, after_step: str, reason: str) -> None:
        for step in COMMAND_STEPS[COMMAND_STEPS.index(after_step) + 1 :]:
            self.record(step, StepStatus.SKIP, reason)


def run_command_suite(
    cases: Sequence[CommandCase], toolchain: Toolchain, server: DexServer, table: ResultTable
) -> None:
    """Run the scenario for each command case."""
    hacker_news = HackerNewsCall.find(server.list_connector_tools())
    for case in cases:
        CommandRun(case, toolchain, server, hacker_news, table).run()
