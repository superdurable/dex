# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The dexcli dev of the e2e: a private or a named one; read sessions and list tools."""

from __future__ import annotations

import base64
import collections
import contextlib
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_READY_SECONDS = 180.0
CONNECTIONS_SCHEMA_VERSION = "connectors.dex.dev/local-connections/v1alpha1"
HACKER_NEWS_CONNECTOR_ID = "hacker-news-daily"
HACKER_NEWS_MODULE_PATH = "github.com/superdurable/dex-connectors-library/connectors/hackernews/go"
_DEXCLI_TIMEOUT_SECONDS = 60
_STOP_SECONDS = 30.0
_KILL_SECONDS = 10.0
_STEP_EXECUTION_NUMBER = re.compile(r"-\d+$")


@dataclass(frozen=True)
class ConnectorTool:
    """One tool the connectors MCP server lists.

    Attributes:
        name: The MCP tool name.
        connector: The connector, from the tool's ``_meta``.
        connection: The connection, from the tool's ``_meta``.
        operation_name: The snake-case operation name, from the tool's ``_meta``.
        input_schema: The tool's JSON Schema.
    """

    name: str
    connector: str
    connection: str
    operation_name: str
    input_schema: Mapping[str, Any]


@dataclass(frozen=True)
class HackerNewsConnectionSeed:
    """A Hacker News connection written into a private dexcli dev's connector directory.

    Attributes:
        module_version: The Hacker News Connector release, such as ``v0.24.0``.
        connection_name: The connection name.
    """

    module_version: str
    connection_name: str = "public"

    def connections_file(self) -> Mapping[str, Any]:
        """Return a ``connections.json`` with only this connection, which needs no credentials."""
        return {
            "schemaVersion": CONNECTIONS_SCHEMA_VERSION,
            "connections": [
                {
                    "connectorId": HACKER_NEWS_CONNECTOR_ID,
                    "modulePath": HACKER_NEWS_MODULE_PATH,
                    "moduleVersion": self.module_version,
                    "provider": "hacker-news",
                    "connectionName": self.connection_name,
                    "configuration": {},
                    "credentials": {},
                }
            ],
        }


@dataclass
class DexServer:
    """The dexcli dev the e2e uses: a private one on free ports, or one named with --reuse-server.

    A private server has its own home, zd data directory, and connector configuration
    directory, so no session and no connector call reaches another server or account.

    Attributes:
        dexcli: The dexcli executable.
        dex_port: The Dex port.
        web_port: The Dex Web port.
        is_private: Whether this object starts and stops the server.
    """

    dexcli: Path
    dex_port: int
    web_port: int
    is_private: bool
    _process: subprocess.Popen[bytes] | None = None

    @staticmethod
    def private(dexcli: Path) -> DexServer:
        """Plan a private server on two free ports."""
        dex_port, web_port = _free_ports(2)
        return DexServer(dexcli, dex_port, web_port, is_private=True)

    @staticmethod
    def reused(dexcli: Path, web_url: str, dex_port: int) -> DexServer:
        """Name a running dexcli dev by its Dex Web URL on 127.0.0.1 and its Dex port."""
        parsed = urllib.parse.urlsplit(web_url)
        if parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "localhost"):
            raise ValueError(f"--reuse-server must be an http URL on 127.0.0.1: {web_url}")
        if parsed.port is None:
            raise ValueError(f"--reuse-server needs a port: {web_url}")
        return DexServer(dexcli, dex_port, parsed.port, is_private=False)

    @property
    def address(self) -> str:
        """Return the FlowService address."""
        return f"127.0.0.1:{self.dex_port}"

    @property
    def web_url(self) -> str:
        """Return the Dex Web URL."""
        return f"http://127.0.0.1:{self.web_port}"

    def is_answering(self) -> bool:
        """Report whether Dex Web and the Dex port both answer."""
        try:
            with urllib.request.urlopen(self.web_url, timeout=5) as response:
                if response.status != 200:
                    return False
        except (urllib.error.URLError, OSError):
            return False
        with (
            contextlib.suppress(OSError),
            socket.create_connection(("127.0.0.1", self.dex_port), 5),
        ):
            return True
        return False

    def start(
        self,
        work_dir: Path,
        seed: HackerNewsConnectionSeed | None,
        connectors_mcp_binary: Path | None,
    ) -> str:
        """Start the private server, or check the reused one; return what happened.

        ``connectors_mcp_binary`` replaces the Connectors MCP release dexcli dev downloads.
        """
        if not self.is_private:
            if not self.is_answering():
                raise RuntimeError(f"nothing answers on {self.web_url} and {self.address}")
            return f"reused the dexcli dev on {self.address} and {self.web_url} (--reuse-server)"
        server_dir = work_dir / "dexcli-dev"
        shutil.rmtree(server_dir, ignore_errors=True)
        home = server_dir / "home"
        connector_dir = server_dir / "connectors"
        zd_data_dir = server_dir / "zd"
        home.mkdir(parents=True)
        connector_dir.mkdir(mode=0o700)
        if seed is not None:
            connections_path = connector_dir / "connections.json"
            connections_path.write_text(json.dumps(seed.connections_file(), indent=2) + "\n")
            connections_path.chmod(0o600)
        log_path = work_dir / "logs" / "dexcli-dev.log"
        log_path.parent.mkdir(parents=True, exist_ok=True)
        with log_path.open("ab") as log:
            self._process = subprocess.Popen(
                [
                    str(self.dexcli),
                    "dev",
                    "-open=false",
                    "-dex-port",
                    str(self.dex_port),
                    "-web-port",
                    str(self.web_port),
                    "-zd-data-dir",
                    str(zd_data_dir),
                    "-connector-config-dir",
                    str(connector_dir),
                    *(
                        ["-connectors-mcp-binary", str(connectors_mcp_binary)]
                        if connectors_mcp_binary is not None
                        else []
                    ),
                ],
                env={**os.environ, "HOME": str(home)},
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        deadline = time.monotonic() + _READY_SECONDS
        while time.monotonic() < deadline:
            if self.is_answering():
                return (
                    f"started a private dexcli dev on {self.address} and {self.web_url} "
                    f"(directory {server_dir}, log {log_path})"
                )
            if self._process.poll() is not None:
                break
            time.sleep(1)
        raise RuntimeError(f"dexcli dev did not become ready; see {log_path}")

    def stop_if_started(self) -> None:
        """Stop the dexcli dev this object started, with every process of its group.

        The group is stopped even after dexcli itself exited, since its children can
        outlive it. A reused server is never stopped.
        """
        process = self._process
        if process is None:
            return
        # start_new_session made dexcli the leader, so its PID is the group ID.
        process_group_id = process.pid
        _signal_process_group(process_group_id, signal.SIGTERM)
        if not _wait_for_process_group_exit(process, process_group_id, _STOP_SECONDS):
            _signal_process_group(process_group_id, signal.SIGKILL)
            if not _wait_for_process_group_exit(process, process_group_id, _KILL_SECONDS):
                raise RuntimeError(f"dexcli dev process group {process_group_id} did not exit")
        self._process = None

    def dexcli_json(self, *arguments: str) -> Any:
        """Run one dexcli query against this server and return its JSON output."""
        completed = subprocess.run(
            [str(self.dexcli), *arguments[:2], "-server", self.address, *arguments[2:]],
            capture_output=True,
            text=True,
            timeout=_DEXCLI_TIMEOUT_SECONDS,
            check=False,
        )
        if completed.returncode != 0:
            raise RuntimeError(
                f"dexcli {' '.join(arguments)} exited with {completed.returncode}: "
                f"{(completed.stdout + completed.stderr)[-1500:]}"
            )
        return json.loads(completed.stdout)

    def flow_summary(self, flow_id: str) -> Mapping[str, Any]:
        """Return ``dexcli flow summary``."""
        return dict(self.dexcli_json("flow", "summary", flow_id))

    def flow_state(self, flow_id: str) -> Mapping[str, Any]:
        """Return ``dexcli flow state``: the active Steps and the Attributes."""
        return dict(self.dexcli_json("flow", "state", flow_id))

    def flow_history(self, flow_id: str) -> Sequence[Mapping[str, Any]]:
        """Return every history event of the Flow's current run."""
        return list(self.dexcli_json("flow", "history", "-all", flow_id)["events"])

    def flow_definition(self, flow_type: str) -> Mapping[str, Any] | None:
        """Return the uploaded Flow Definition Graph of a Flow type, or ``None`` without one."""
        listed = self.dexcli_json("api", "call", "ListFlowDefinitions")
        if flow_type not in (listed.get("flowTypes") or []):
            return None
        fetched = self.dexcli_json(
            "api", "call", "GetFlowDefinitions", "-data", json.dumps({"flowTypes": [flow_type]})
        )
        for definition in fetched.get("definitions") or []:
            graph = json.loads(base64.b64decode(definition["content"]))
            if isinstance(graph, dict):
                return graph
        return None

    def list_connector_tools(self) -> list[ConnectorTool]:
        """List the tools of the connectors MCP server that dexcli dev serves."""
        url = f"{self.web_url}/mcp"
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
        }
        initialize = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "dex-agents-cli-e2e", "version": "0"},
            },
        }
        session_headers = dict(headers)
        _, response_headers = _post_json_rpc(url, initialize, headers)
        if session_id := response_headers.get("mcp-session-id"):
            session_headers["Mcp-Session-Id"] = session_id
        session_headers["MCP-Protocol-Version"] = "2025-06-18"
        _post_json_rpc(
            url, {"jsonrpc": "2.0", "method": "notifications/initialized"}, session_headers
        )
        listed, _ = _post_json_rpc(
            url, {"jsonrpc": "2.0", "id": 2, "method": "tools/list"}, session_headers
        )
        tools = []
        for tool in (listed or {}).get("result", {}).get("tools", []):
            meta = tool.get("_meta") or {}
            tools.append(
                ConnectorTool(
                    name=str(tool["name"]),
                    connector=str(meta.get("io.superdurable.connectors/connector", "")),
                    connection=str(meta.get("io.superdurable.connectors/connection", "")),
                    operation_name=str(meta.get("io.superdurable.connectors/operationName", "")),
                    input_schema=tool.get("inputSchema") or {},
                )
            )
        return tools


@dataclass(frozen=True)
class SessionRecord:
    """What the Dex server holds for one agent session.

    Attributes:
        flow_type: The session's Flow type.
        status: The Flow status.
        user_texts: The text of each stored user message.
        tool_records: ``(tool name, output)`` of each stored tool result: a tool message,
            or a message that holds both the call and its output, as Mastra stores them.
        turn_ids: The distinct turn IDs of the stored messages.
        step_type_counts: How many Step executions of each type the history has.
        graph: The uploaded Flow Definition Graph of the Flow type, or ``None``.
    """

    flow_type: str
    status: str
    user_texts: Sequence[str]
    tool_records: Sequence[tuple[str, str]]
    turn_ids: Sequence[str]
    step_type_counts: Mapping[str, int]
    graph: Mapping[str, Any] | None

    @staticmethod
    def read(server: DexServer, session_id: str) -> SessionRecord:
        """Read a session from the server."""
        summary = server.flow_summary(session_id)
        flow_type = str(summary.get("flowType", ""))
        state = server.flow_state(session_id)
        messages = [
            attribute.get("value") or {}
            for attribute in state.get("attributes") or []
            if str(attribute.get("key", "")).startswith("AgentMessages/")
        ]
        step_execution_ids: set[str] = set()
        for event in server.flow_history(session_id):
            context = (event.get("payload") or {}).get("context") or {}
            step_execution_id = context.get("stepExecutionId")
            if isinstance(step_execution_id, str):
                step_execution_ids.add(step_execution_id)
        step_type_counts = collections.Counter(
            _STEP_EXECUTION_NUMBER.sub("", step_execution_id)
            for step_execution_id in step_execution_ids
        )
        return SessionRecord(
            flow_type=flow_type,
            status=str(summary.get("flowStatus", "")),
            user_texts=[
                str(message.get("text")) for message in messages if message.get("role") == "user"
            ],
            tool_records=[
                (str(message.get("tool_name")), json.dumps(message.get("tool_output")))
                for message in messages
                if message.get("role") == "tool"
                or (message.get("tool_name") and message.get("tool_output") is not None)
            ],
            turn_ids=list(dict.fromkeys(str(message.get("turn_id")) for message in messages)),
            step_type_counts=dict(step_type_counts),
            graph=server.flow_definition(flow_type) if flow_type else None,
        )

    def describe(self) -> str:
        """Return a one-line summary."""
        steps = ", ".join(
            f"{name}x{count}" for name, count in sorted(self.step_type_counts.items())
        )
        graph_nodes = len((self.graph or {}).get("nodes") or [])
        return (
            f"flow type {self.flow_type} ({self.status}); {len(self.turn_ids)} turns; "
            f"{len(self.user_texts)} user messages; tool records "
            f"{[name for name, _ in self.tool_records]}; graph nodes {graph_nodes}; steps {steps}"
        )


def _post_json_rpc(
    url: str, payload: Mapping[str, Any], headers: Mapping[str, str]
) -> tuple[Mapping[str, Any] | None, Mapping[str, str]]:
    request = urllib.request.Request(
        url, data=json.dumps(payload).encode(), headers=dict(headers), method="POST"
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        text = response.read().decode("utf-8", errors="replace")
        response_headers = {name.lower(): value for name, value in response.headers.items()}
    if not text.strip():
        return None, response_headers
    for line in text.splitlines():
        if line.startswith("data: "):
            return json.loads(line.removeprefix("data: ")), response_headers
    return json.loads(text), response_headers


def _signal_process_group(process_group_id: int, signal_number: signal.Signals) -> None:
    with contextlib.suppress(ProcessLookupError):
        os.killpg(process_group_id, signal_number)


def _wait_for_process_group_exit(
    leader: subprocess.Popen[bytes], process_group_id: int, timeout_seconds: float
) -> bool:
    """Return whether the group emptied within the timeout; reaps the leader meanwhile."""
    deadline = time.monotonic() + timeout_seconds
    while True:
        # An unreaped leader stays a zombie member of the group.
        leader.poll()
        try:
            os.killpg(process_group_id, 0)
        except ProcessLookupError:
            return True
        if time.monotonic() >= deadline:
            return False
        time.sleep(0.1)


def _free_ports(count: int) -> list[int]:
    sockets = [socket.socket() for _ in range(count)]
    try:
        for probe in sockets:
            probe.bind(("127.0.0.1", 0))
        return [int(probe.getsockname()[1]) for probe in sockets]
    finally:
        for probe in sockets:
            probe.close()
