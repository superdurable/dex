# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The local dexcli dev: reuse or start it, read sessions, and list connector tools."""

from __future__ import annotations

import base64
import collections
import contextlib
import json
import os
import re
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

_READY_SECONDS = 120.0
_DEXCLI_TIMEOUT_SECONDS = 60
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


@dataclass
class DexServer:
    """A dexcli dev on 127.0.0.1, reused when it answers, else started privately.

    Attributes:
        dexcli: The dexcli executable.
        dex_port: The Dex port.
        web_port: The Dex Web port.
    """

    dexcli: Path
    dex_port: int
    web_port: int
    _process: subprocess.Popen[bytes] | None = None

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

    def ensure_running(self, work_dir: Path) -> str:
        """Reuse a running dexcli dev, or start one with a private home; return what happened."""
        if self.is_answering():
            return f"reused the dexcli dev on {self.address} and {self.web_url}"
        home = work_dir / "dexcli-home"
        home.mkdir(parents=True, exist_ok=True)
        log_path = work_dir / "logs" / "dexcli-dev.log"
        log_path.parent.mkdir(parents=True, exist_ok=True)
        environment = {**os.environ, "HOME": str(home)}
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
                ],
                env=environment,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        deadline = time.monotonic() + _READY_SECONDS
        while time.monotonic() < deadline:
            if self.is_answering():
                return f"started a private dexcli dev (home {home}, log {log_path})"
            if self._process.poll() is not None:
                break
            time.sleep(1)
        raise RuntimeError(f"dexcli dev did not become ready; see {log_path}")

    def stop_if_started(self) -> None:
        """Stop the dexcli dev this object started; never one it reused."""
        if self._process is not None and self._process.poll() is None:
            os.killpg(self._process.pid, signal.SIGTERM)
            try:
                self._process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                os.killpg(self._process.pid, signal.SIGKILL)
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
        tool_records: ``(tool name, output)`` of each stored tool result.
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
