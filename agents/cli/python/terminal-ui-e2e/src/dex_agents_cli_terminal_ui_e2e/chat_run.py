# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""One scenario's command runs: a home, a workspace, a scripted model, and terminal sessions."""

from __future__ import annotations

import json
import os
import re
import shutil
import socket
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from dex_agents_cli_terminal_ui_e2e.command_targets import TerminalUiCommand
from dex_agents_cli_terminal_ui_e2e.output_canary import (
    CANARY_DIRECTORY_VARIABLE,
    NODE_CANARY_SCRIPT,
    PYTHON_CANARY_DIRECTORY,
)
from dex_agents_cli_terminal_ui_e2e.terminal_session import TerminalSession
from dex_agents_cli_testing.e2e.dex_server import DexServer
from dex_agents_cli_testing.mock_model_api import MockModelApi
from dex_agents_cli_testing.model_scenario import ScenarioFile

UNREACHABLE_PROXY = "http://127.0.0.1:9"
NOTES_TEXT = "buy milk and eggs"
_HOST_VARIABLES = ("TMPDIR", "USER", "LOGNAME", "SHELL")
_SESSION_LINE = re.compile(r"session:\s+(\S+)")
_RESUME_LINE = re.compile(r"--resume (\S+)")


@dataclass(frozen=True)
class CommandBinaries:
    """What every run of one scenario starts.

    Attributes:
        command_argv: The command's argv prefix.
        dexcli: The dexcli with ``agents-tui``, passed as ``DEXCLI``.
        search_path: ``PATH``, with Node.js first for a TypeScript command.
    """

    command_argv: Sequence[str]
    dexcli: Path
    search_path: str


class ChatRun:
    """The directories, scripted model, and terminal sessions of one scenario.

    ``server`` is the shared ``dexcli dev``; with ``None`` each command starts its own.
    """

    def __init__(
        self,
        command: TerminalUiCommand,
        binaries: CommandBinaries,
        server: DexServer | None,
        case_dir: Path,
        turns: Sequence[Mapping[str, Any]],
    ) -> None:
        """Lay out the case directory and start the scripted model."""
        self.command = command
        self.binaries = binaries
        self.server = server
        self.case_dir = case_dir
        shutil.rmtree(case_dir, ignore_errors=True)
        self.home = case_dir / "home"
        self.workspace = case_dir / "workspace"
        self.canary_dir = case_dir / "canary"
        for directory in (self.home, self.workspace, self.canary_dir):
            directory.mkdir(parents=True)
        (self.workspace / "notes.txt").write_text(NOTES_TEXT + "\n", encoding="utf-8")
        self.config_path = case_dir / "config.yaml"
        self.scenario_path = case_dir / "scenario.json"
        self.request_log_path = case_dir / "model-requests.jsonl"
        self.write_turns(turns)
        self.sessions: list[TerminalSession] = []
        self._own_dev_ports = None if server is not None else _free_ports(2)
        self._mock = MockModelApi(ScenarioFile(self.scenario_path), self.request_log_path)
        self._mock.start()
        self.write_config()

    def write_turns(self, turns: Sequence[Mapping[str, Any]]) -> None:
        """Replace the scripted model's turns; the mock reloads the file on its next request."""
        self.scenario_path.write_text(json.dumps({"turns": list(turns)}, indent=2) + "\n")

    def write_config(self, shell_allow: Sequence[str] = ("ls",)) -> None:
        """Write the command's configuration file."""
        case = self.command.case
        config: dict[str, Any] = {
            "dex": {"server": "auto"},
            "connectors": {"mcp": "auto", "enabled": []},
            "workspace": str(self.workspace),
            "local_tools": {
                "files": True,
                "shell": {"enabled": True, "allow": list(shell_allow)},
                "web_fetch": False,
                "browser": False,
            },
            **case.provider.config_section(self._mock.base_url),
        }
        if not case.tools.are_local_writes_approved_by_default:
            config["approval"] = {"always": [case.tools.write_tool]}
        # JSON is valid YAML 1.2.
        self.config_path.write_text(json.dumps(config, indent=2) + "\n", encoding="utf-8")

    def spawn(
        self,
        *arguments: str,
        columns: int = 80,
        lines: int = 24,
        extra_environment: Mapping[str, str] | None = None,
        dexcli: Path | None = None,
        has_canary: bool = False,
    ) -> TerminalSession:
        """Start the command on a new pseudo-terminal with the terminal UI on."""
        environment = self.environment(dexcli or self.binaries.dexcli, has_canary)
        environment.update(extra_environment or {})
        session = TerminalSession(
            [*self.binaries.command_argv, "--config", str(self.config_path), *arguments],
            environment,
            self.case_dir,
            self.case_dir / f"terminal-{len(self.sessions)}.bin",
            columns,
            lines,
        )
        self.sessions.append(session)
        return session

    def environment(self, dexcli: Path, has_canary: bool) -> dict[str, str]:
        """Return the command's environment: a private home, the mock, and the terminal UI on."""
        environment = {name: os.environ[name] for name in _HOST_VARIABLES if name in os.environ}
        dex_port, web_port = (
            (self.server.dex_port, self.server.web_port)
            if self.server is not None
            else self._own_dev_ports or (0, 0)
        )
        environment.update(
            PATH=self.binaries.search_path,
            HOME=str(self.home),
            LANG="en_US.UTF-8",
            TERM="xterm-256color",
            DEXCLI=str(dexcli),
            DEX_AGENTS_TERMINAL_UI="1",
            DEX_AGENTS_DEX_PORT=str(dex_port),
            DEX_AGENTS_WEB_PORT=str(web_port),
            DEX_AGENTS_OPEN_BROWSER="0",
            NO_PROXY="127.0.0.1,localhost",
            no_proxy="127.0.0.1,localhost",
            PYTHONUNBUFFERED="1",
            CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK="1",
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
            DISABLE_AUTOUPDATER="1",
            DISABLE_TELEMETRY="1",
            DISABLE_ERROR_REPORTING="1",
            MASTRA_TELEMETRY_DISABLED="1",
            OPENAI_AGENTS_DISABLE_TRACING="1",
            # LiteLLM otherwise fetches its model cost map from GitHub at import.
            LITELLM_LOCAL_MODEL_COST_MAP="True",
        )
        if os.environ.get("DEX_SERVER_ZD_BINARY"):
            environment["DEX_SERVER_ZD_BINARY"] = os.environ["DEX_SERVER_ZD_BINARY"]
        if self.server is not None:
            # The shared dexcli dev already runs, so the command needs no network.
            environment.update(
                HTTPS_PROXY=UNREACHABLE_PROXY,
                HTTP_PROXY=UNREACHABLE_PROXY,
                https_proxy=UNREACHABLE_PROXY,
                http_proxy=UNREACHABLE_PROXY,
            )
        environment.update(self.command.case.provider.environment(self._mock.base_url))
        if has_canary:
            environment[CANARY_DIRECTORY_VARIABLE] = str(self.canary_dir)
            if self.command.case.is_python:
                environment["PYTHONPATH"] = str(PYTHON_CANARY_DIRECTORY)
            else:
                environment["NODE_OPTIONS"] = f"--require {NODE_CANARY_SCRIPT}"
        return environment

    def model_requests(self) -> list[Mapping[str, Any]]:
        """Return the mock's request log entries, oldest first."""
        if not self.request_log_path.exists():
            return []
        with self.request_log_path.open(encoding="utf-8") as log:
            return [json.loads(line) for line in log if line.strip()]

    def command_log_text(self) -> str:
        """Return the command logs under the home, where the Worker host and launcher write."""
        logs = sorted((self.home / ".dex" / "agents").glob("*.log"))
        return "\n".join(path.read_text(encoding="utf-8", errors="replace") for path in logs)

    def close(self) -> None:
        """Stop every terminal session and the scripted model."""
        for session in self.sessions:
            session.close()
        self._mock.stop()


ChatRunFactory = Callable[..., ChatRun]
"""Creates a ``ChatRun`` for scripted turns; ``own_dev_server=True`` starts no shared server."""


def session_id_on_screen(session: TerminalSession) -> str:
    """Return the session ID of the welcome block's ``session:`` line."""
    for line in session.screen.all_lines():
        match = _SESSION_LINE.search(line)
        if match:
            return match.group(1)
    raise AssertionError(f"no session line\n{session.describe()}")


def resume_id_after_exit(session: TerminalSession) -> str | None:
    """Return the session ID of the ``--resume`` line the exit summary printed."""
    for line in reversed(session.screen.output_text().splitlines()):
        match = _RESUME_LINE.search(line)
        if match:
            return match.group(1).rstrip(".")
    return None


def _free_ports(count: int) -> tuple[int, int]:
    sockets = [socket.socket() for _ in range(count)]
    try:
        for probe in sockets:
            probe.bind(("127.0.0.1", 0))
        ports = [int(probe.getsockname()[1]) for probe in sockets]
    finally:
        for probe in sockets:
            probe.close()
    return ports[0], ports[1]
