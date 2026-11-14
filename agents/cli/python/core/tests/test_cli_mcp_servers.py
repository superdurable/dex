# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Remote MCP server tools: header variables, approvals, and secrets that never leak."""

from __future__ import annotations

import asyncio
import io
import json
import logging
import os
import subprocess
import sys
from collections.abc import AsyncIterator
from dataclasses import dataclass, replace
from pathlib import Path

import pytest

from dex_agents import (
    AgentEventWriter,
    AgentToolCall,
    DeferredToolExecution,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolTaskCancellation,
    ToolTaskPoll,
)
from dex_agents.testing import InvocationLog, find_free_ports, resolve_dexcli_path
from dex_agents_cli_core import (
    McpServersToolSource,
    TurnConfigResolver,
    mcp_server_secret_texts,
    resolve_config,
    resolve_mcp_servers,
)
from tests.conftest import (
    PACKAGE_DIR,
    CliHome,
    config_yaml,
    expect,
    session_client,
    wait_for_exit,
    wait_for_text,
)
from tests.fake_connectors_mcp import (
    INVOCATION_LOG_NAME,
    READY_LINE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
)
from tests.fake_remote_mcp import REJECTED_REQUESTS_LOG_NAME
from tests.scripted_harness import SCRIPTED_FLOW_TYPE
from tests.test_cli_chat import read_session_id

SENTINEL_TOKEN = "sk-sentinel-4f1c9a7e2b"
NO_LOCAL_TOOLS = {"files": False, "shell": {"enabled": False}, "web_fetch": False, "browser": False}


@dataclass
class FakeRemoteMcp:
    """A running fake stateless MCP server that requires one bearer token."""

    port: int
    state_dir: Path
    process: subprocess.Popen[bytes]

    @property
    def url(self) -> str:
        """Return its MCP endpoint."""
        return f"http://127.0.0.1:{self.port}/mcp"

    def runs(self, tool_name: str) -> int:
        """Return how many times a tool really ran."""
        names = InvocationLog(self.state_dir / INVOCATION_LOG_NAME).names()
        return sum(1 for name in names if name.split(" ")[0] == tool_name)

    def rejected_requests(self) -> int:
        """Return how many requests came without the expected token."""
        log = self.state_dir / REJECTED_REQUESTS_LOG_NAME
        return len(log.read_text().splitlines()) if log.exists() else 0


@pytest.fixture
async def remote_mcp(tmp_path: Path) -> AsyncIterator[FakeRemoteMcp]:
    """Start the fake remote MCP server on a free port, expecting the sentinel token."""
    (port,) = find_free_ports(1)
    state_dir = tmp_path / "fake-remote-mcp"
    state_dir.mkdir()
    log_path = tmp_path / "fake-remote-mcp.log"
    with log_path.open("w") as log:
        process = subprocess.Popen(
            [sys.executable, "-m", "tests.fake_remote_mcp", str(port), SENTINEL_TOKEN],
            cwd=PACKAGE_DIR,
            env={**os.environ, STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
            stdout=log,
            stderr=subprocess.STDOUT,
        )
    try:
        await wait_for_text(log_path, READY_LINE)
        yield FakeRemoteMcp(port=port, state_dir=state_dir, process=process)
    finally:
        process.kill()
        process.wait()


def _server(url: str, *, is_trusted: bool) -> dict[str, object]:
    return {
        "url": url,
        "headers": {"Authorization": "Bearer ${STUDIO_TOKEN}", "X-Client": "dex-tests"},
        "trust_tool_annotations": is_trusted,
    }


async def test_remote_mcp_tools_run_with_the_header_from_the_environment_and_their_approvals(
    cli_home: CliHome, remote_mcp: FakeRemoteMcp, tmp_path: Path
) -> None:
    cli_home.extra_environment["STUDIO_TOKEN"] = SENTINEL_TOKEN
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {"enabled": []},
                "mcp_servers": {
                    "lib": _server(remote_mcp.url, is_trusted=True),
                    "raw": _server(remote_mcp.url, is_trusted=False),
                    "studio": _server(remote_mcp.url, is_trusted=True),
                },
                "approval": {"always": ["studio__search_assets"]},
                "local_tools": NO_LOCAL_TOOLS,
            }
        )
    )
    child = cli_home.spawn()
    child.logfile_read = io.StringIO()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline("tools")
    tool_names = ",".join(
        f"{server}__{tool}"
        for server in ("lib", "raw", "studio")
        for tool in ("crash_with_echo", "publish_asset", "report_upstream_failure", "search_assets")
    )
    await expect(child, f"tools: ask_user,{tool_names}")
    child.sendline("cached")
    await expect(child, "cached: lib__")
    await expect(child, "lib__search_assets")
    # A trusted read-only tool runs at once.
    child.sendline('call lib__search_assets {"query": "cats"}')
    await expect(child, "done: lib__search_assets=found 2 assets for cats")
    # An untrusted server's read waits for approval.
    child.sendline('call raw__search_assets {"query": "dogs"}')
    await expect(child, '[approval] raw__search_assets {"query": "dogs"}')
    child.sendline("y")
    await expect(child, "done: raw__search_assets=found 2 assets for dogs")
    # approval.always gates a trusted read.
    child.sendline('call studio__search_assets {"query": "owls"}')
    await expect(child, "[approval] studio__search_assets")
    child.sendline("n")
    await expect(child, "[rejected]")
    await expect(child, "done: studio__search_assets=")
    # A trusted tool without hints still waits for approval.
    child.sendline('call lib__publish_asset {"asset": "a1"}')
    await expect(child, "[approval] lib__publish_asset")
    child.sendline("y")
    await expect(child, "done: lib__publish_asset=published a1")
    flow_type = SCRIPTED_FLOW_TYPE
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        snapshot = await sessions.get_agent_snapshot(session_id)
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert remote_mcp.runs("search_assets") == 2
    assert remote_mcp.runs("publish_asset") == 1
    assert remote_mcp.rejected_requests() == 0
    output = child.logfile_read.getvalue()
    log_file = cli_home.home / ".dex" / "agents" / "scripted.log"
    for text in (output, repr(snapshot), log_file.read_text()):
        assert SENTINEL_TOKEN not in text
    assert "found 2 assets for cats" in repr(snapshot)


async def test_a_rejected_token_leaves_the_remote_tools_out_with_one_line(
    cli_home: CliHome, remote_mcp: FakeRemoteMcp
) -> None:
    cli_home.extra_environment["STUDIO_TOKEN"] = "sk-wrong-token"
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {"enabled": []},
                "mcp_servers": {"studio": _server(remote_mcp.url, is_trusted=True)},
                "local_tools": NO_LOCAL_TOOLS,
            }
        )
    )
    child = cli_home.spawn()
    child.logfile_read = io.StringIO()
    await expect(child, "MCP server tools are unavailable for now; each turn tries again. See ")
    await expect(child, "> ")
    child.sendline("tools")
    await expect(child, "tools: ask_user\r\n")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert remote_mcp.rejected_requests() > 0
    assert child.logfile_read.getvalue().count("MCP server tools are unavailable") == 1
    # The 401 body echoes the header, and the log keeps the listing error.
    log_text = (cli_home.home / ".dex" / "agents" / "scripted.log").read_text()
    assert "could not be listed" in log_text
    assert "sk-wrong-token" not in log_text
    assert "sk-wrong-token" not in child.logfile_read.getvalue()


async def test_a_server_that_echoes_the_header_in_its_errors_never_leaks_it(
    cli_home: CliHome, remote_mcp: FakeRemoteMcp, tmp_path: Path
) -> None:
    cli_home.extra_environment["STUDIO_TOKEN"] = SENTINEL_TOKEN
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {"enabled": []},
                "mcp_servers": {"raw": _server(remote_mcp.url, is_trusted=False)},
                "local_tools": NO_LOCAL_TOOLS,
            }
        )
    )
    child = cli_home.spawn()
    child.logfile_read = io.StringIO()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline('call raw__report_upstream_failure {"asset": "a1"}')
    await expect(child, "[approval] raw__report_upstream_failure")
    child.sendline("y")
    await expect(child, "done: raw__report_upstream_failure=")
    await expect(child, "> ")
    child.sendline('call raw__crash_with_echo {"asset": "a2"}')
    await expect(child, "[approval] raw__crash_with_echo")
    child.sendline("y")
    await expect(child, "done: raw__crash_with_echo=")
    await expect(child, "> ")
    flow_type = SCRIPTED_FLOW_TYPE
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        snapshot = await sessions.get_agent_snapshot(session_id)
    history = await read_flow_history_text(cli_home.address, session_id)
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert remote_mcp.runs("report_upstream_failure") == 1
    assert "[redacted]" in repr(snapshot)
    log_file = cli_home.home / ".dex" / "agents" / "scripted.log"
    for where, text in (
        ("terminal", child.logfile_read.getvalue()),
        ("session snapshot", repr(snapshot)),
        ("Flow history", history),
        ("log file", log_file.read_text()),
    ):
        assert SENTINEL_TOKEN not in text, where


async def read_flow_history_text(address: str, session_id: str) -> str:
    """Return the session's whole Flow history as dexcli prints it in JSON."""
    process = await asyncio.create_subprocess_exec(
        str(resolve_dexcli_path()),
        "flow",
        "history",
        session_id,
        "--server",
        address,
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
    )
    stdout, _ = await process.communicate()
    assert process.returncode == 0
    return stdout.decode()


async def _no_heartbeat() -> None:
    return None


def _stored_call(
    tool_name: str, *, effect: ToolEffect, requires_approval: bool
) -> DeferredToolExecution:
    server_name, _, mcp_tool_name = tool_name.partition("__")
    return DeferredToolExecution(
        flow_id="session-1",
        tool_call=AgentToolCall(
            tool_call_id="call-1",
            turn_id="turn-1",
            tool_name=tool_name,
            arguments=json.dumps({"query": "cats", "asset": "a1"}),
            effect=effect,
            requires_approval=requires_approval,
            status=ToolCallStatus.APPROVED,
            tool_source_reference=json.dumps(
                {"mcp_server": server_name, "mcp_tool": mcp_tool_name}
            ),
        ),
        idempotency_key="session-1/turn-1/call-1",
        attempt=1,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=_no_heartbeat,
    )


def _untrusted_source(url: str) -> McpServersToolSource:
    config = resolve_config(
        {"mcp_servers": {"raw": _server(url, is_trusted=False)}}, "builtin", "scripted-model"
    )
    environment = {"STUDIO_TOKEN": SENTINEL_TOKEN}
    return McpServersToolSource(
        resolve_mcp_servers(config, environment),
        TurnConfigResolver(config, None, lambda warnings: None),
        secret_texts=mcp_server_secret_texts(config, environment),
    )


@pytest.mark.parametrize(
    ("effect", "requires_approval"),
    [
        (ToolEffect.READ, False),
        (ToolEffect.READ, True),
        (ToolEffect.IDEMPOTENT_WRITE, True),
        (ToolEffect.UNCLASSIFIED, False),
    ],
)
async def test_a_worker_that_does_not_trust_the_server_refuses_a_call_another_worker_trusted(
    remote_mcp: FakeRemoteMcp, effect: ToolEffect, requires_approval: bool
) -> None:
    # Unit level: a call that a trusting Worker stored, run by this untrusting Worker.
    source = _untrusted_source(remote_mcp.url)
    call = _stored_call("raw__search_assets", effect=effect, requires_approval=requires_approval)
    with pytest.raises(ToolExecutionError, match="does not trust the annotations"):
        await source.execute_deferred_tool_call(call)
    assert remote_mcp.runs("search_assets") == 0


@pytest.mark.parametrize(
    "reference",
    [
        None,
        "not json",
        json.dumps({"mcp_tool": "search_assets"}),
        json.dumps({"mcp_server": "other"}),
    ],
)
async def test_a_call_whose_reference_names_no_configured_server_is_refused_unsent(
    remote_mcp: FakeRemoteMcp, reference: str | None
) -> None:
    # Unit level: the listing must never pick the server past the trust check.
    source = _untrusted_source(remote_mcp.url)
    await source.list_file_enabled_tools()
    call = _stored_call("raw__search_assets", effect=ToolEffect.READ, requires_approval=False)
    call = replace(call, tool_call=replace(call.tool_call, tool_source_reference=reference))
    runs_before = remote_mcp.runs("search_assets")
    with pytest.raises(ToolExecutionError, match="names no MCP server that this Worker configures"):
        await source.execute_deferred_tool_call(call)
    assert remote_mcp.runs("search_assets") == runs_before


async def test_a_reference_without_mcp_tool_never_reaches_an_untrusted_server_by_tool_name(
    remote_mcp: FakeRemoteMcp,
) -> None:
    # Unit level: the reference names the trusted server; the tool name is the untrusted one.
    config = resolve_config(
        {
            "mcp_servers": {
                "trusted": _server(remote_mcp.url, is_trusted=True),
                "raw": _server(remote_mcp.url, is_trusted=False),
            }
        },
        "builtin",
        "scripted-model",
    )
    environment = {"STUDIO_TOKEN": SENTINEL_TOKEN}
    source = McpServersToolSource(
        resolve_mcp_servers(config, environment),
        TurnConfigResolver(config, None, lambda warnings: None),
        secret_texts=mcp_server_secret_texts(config, environment),
    )
    await source.list_file_enabled_tools()
    call = _stored_call("raw__search_assets", effect=ToolEffect.READ, requires_approval=False)
    record = replace(call.tool_call, tool_source_reference=json.dumps({"mcp_server": "trusted"}))
    call = replace(call, tool_call=replace(record, task_id="task-1"))
    runs_before = remote_mcp.runs("search_assets")
    with pytest.raises(ToolExecutionError, match="names no MCP server that this Worker configures"):
        await source.execute_deferred_tool_call(call)
    with pytest.raises(ToolExecutionError, match="names no MCP server that this Worker configures"):
        await source.poll_tool_task(
            ToolTaskPoll(
                flow_id="session-1",
                tool_call=call.tool_call,
                idempotency_key="session-1/turn-1/call-1",
                attempt=1,
                input_answers=(),
                events=AgentEventWriter(lambda event: None, "turn-1", 1),
                heartbeat=_no_heartbeat,
            )
        )
    await source.cancel_tool_task(
        ToolTaskCancellation(
            flow_id="session-1",
            tool_call=call.tool_call,
            idempotency_key="session-1/turn-1/call-1",
            attempt=1,
            heartbeat=_no_heartbeat,
        )
    )
    assert remote_mcp.runs("search_assets") == runs_before


def _exception_chain(error: BaseException) -> list[BaseException]:
    chain = [error]
    while (linked := chain[-1].__cause__ or chain[-1].__context__) is not None:
        chain.append(linked)
    return chain


async def test_an_untrusting_worker_runs_an_approved_single_attempt_call_with_secrets_redacted(
    remote_mcp: FakeRemoteMcp,
) -> None:
    source = _untrusted_source(remote_mcp.url)
    approved = _stored_call(
        "raw__search_assets", effect=ToolEffect.UNCLASSIFIED, requires_approval=True
    )
    outcome = await source.execute_deferred_tool_call(approved)
    assert isinstance(outcome, ToolExecutionResult)
    assert "found 2 assets for cats" in outcome.output
    failing = _stored_call(
        "raw__report_upstream_failure", effect=ToolEffect.UNCLASSIFIED, requires_approval=True
    )
    with pytest.raises(ToolExecutionError) as failed:
        await source.execute_deferred_tool_call(failing)
    assert SENTINEL_TOKEN not in str(failed.value)
    assert "[redacted]" in str(failed.value)
    crashing = _stored_call(
        "raw__crash_with_echo", effect=ToolEffect.UNCLASSIFIED, requires_approval=True
    )
    with pytest.raises(Exception) as crashed:
        await source.execute_deferred_tool_call(crashing)
    chain = _exception_chain(crashed.value)
    assert all(SENTINEL_TOKEN not in str(error) for error in chain)


def test_the_log_filter_redacts_the_message_and_the_exception_chain() -> None:
    source = _untrusted_source("http://127.0.0.1:9/mcp")
    records: list[str] = []

    class _Collecting(logging.Handler):
        def emit(self, record: logging.LogRecord) -> None:
            records.append(self.format(record))

    handler = _Collecting()
    handler.addFilter(source.secret_redacting_log_filter)
    logger = logging.getLogger("tests.mcp_servers_redaction")
    logger.addHandler(handler)
    logger.propagate = False
    try:
        cause = RuntimeError(f"HTTP 401: unauthorized: Authorization: Bearer {SENTINEL_TOKEN}")
        try:
            raise ToolExecutionError("listing failed") from cause
        except ToolExecutionError:
            logger.exception("server said %s", f"Bearer {SENTINEL_TOKEN}")
    finally:
        logger.removeHandler(handler)
    assert len(records) == 1
    assert SENTINEL_TOKEN not in records[0]
    assert records[0].count("[redacted]") == 2


def test_an_unset_header_variable_stops_the_command_with_exit_code_2(cli_home: CliHome) -> None:
    cli_home.extra_environment["STUDIO_TOKEN"] = ""
    cli_home.write_config(
        config_yaml(
            {
                "mcp_servers": {
                    "studio": {
                        "url": "https://mcp.example.com/mcp",
                        "headers": {"Authorization": "Bearer ${STUDIO_TOKEN}"},
                    }
                }
            }
        )
    )
    completed = cli_home.run(stdin="")
    assert completed.returncode == 2, completed.stderr
    assert completed.stderr.strip().endswith(
        "mcp_servers.studio.headers.Authorization needs the environment variable "
        "STUDIO_TOKEN, which is not set or is empty."
    )
    assert "Dex Web" not in completed.stdout
