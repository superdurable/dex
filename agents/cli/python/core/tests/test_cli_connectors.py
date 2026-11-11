# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector tools from the MCP server, session narrowing, and an unavailable server."""

from __future__ import annotations

import asyncio
import contextlib
import json
import logging
import re
import threading
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from dex_agents.testing import find_free_ports
from dex_agents_cli_core import (
    ConnectorsMcpToolSource,
    TurnConfigResolver,
    resolve_config,
    session_flow_type,
)
from dex_agents_cli_core.connectors import (
    ConnectorsMcpUnavailableError,
    connectors_unavailable_line,
)
from tests.conftest import (
    CliHome,
    FakeConnectorsMcp,
    config_yaml,
    expect,
    session_client,
    wait_for_exit,
)
from tests.fake_connectors_mcp import SLACK_CONNECTION_ENVIRONMENT_VARIABLE
from tests.scripted_harness import SCRIPTED_FLOW_TYPE
from tests.test_cli_chat import read_session_id

_CONNECTOR_LINE_PATTERN = re.compile(r"Connector tools|MCP server|tool listing", re.IGNORECASE)
NO_LOCAL_TOOLS = {"files": False, "shell": {"enabled": False}, "web_fetch": False, "browser": False}


async def test_a_connector_read_runs_at_once_and_an_approved_write_runs_once(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    cli_home.write_config(
        config_yaml({"connectors": {"mcp": fake_mcp.url}, "local_tools": NO_LOCAL_TOOLS})
    )
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline("tools")
    await expect(
        child,
        "tools: ask_user,gmail_get_message,hackernews_get_top_stories,misc_untagged,"
        "reddit_export_report,slack_post_thread_reply",
    )
    child.sendline("call hackernews_get_top_stories {}")
    await expect(child, "done: hackernews_get_top_stories=1. Durable agents")
    child.sendline('call slack_post_thread_reply {"thread": "T1", "text": "On it"}')
    await expect(child, '[approval] slack_post_thread_reply {"thread": "T1"')
    child.sendline("y")
    await expect(child, "done: slack_post_thread_reply=posted 'On it' in T1")
    child.sendline('call slack_post_thread_reply {"thread": "T1", "text": "Never"}')
    await expect(child, "[approval] slack_post_thread_reply")
    child.sendline("n")
    await expect(child, "[rejected]")
    await expect(child, "done: slack_post_thread_reply=")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert fake_mcp.runs("hackernews_get_top_stories") == 1
    assert fake_mcp.runs("slack_post_thread_reply") == 1
    (sent,) = fake_mcp.request_meta("slack_post_thread_reply")
    assert sent["io.superdurable.connectors/expectedConnector"] == "slack"
    assert sent["io.superdurable.connectors/expectedConnection"] == "support-slack"
    assert sent["io.superdurable.connectors/expectedOperation"] == "postThreadReply"


async def test_the_file_enabled_tools_are_cached_before_the_flow_is_built(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    # A runner that fixes its tools when it is built, as the OpenAI Agents SDK one does, sees them.
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {"mcp": fake_mcp.url, "enabled": ["hackernews"]},
                "local_tools": {**NO_LOCAL_TOOLS, "files": True},
            }
        )
    )
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline("cached")
    await expect(
        child,
        "cached: edit_file,hackernews_get_top_stories,list_directory,read_file,search_files,"
        "write_file\r",
    )
    child.sendeof()
    assert await wait_for_exit(child) == 0


async def test_a_tool_name_rebound_to_another_connection_is_refused_before_it_is_sent(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {"mcp": fake_mcp.url, "enabled": ["slack"]},
                "local_tools": NO_LOCAL_TOOLS,
            }
        )
    )
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline('call slack_post_thread_reply {"thread": "T1", "text": "On it"}')
    await expect(child, "[approval] slack_post_thread_reply")
    # While the call waits, the same tool name moves to another Slack connection.
    fake_mcp.stop()
    await fake_mcp.start({SLACK_CONNECTION_ENVIRONMENT_VARIABLE: "personal-slack"})
    child.sendline("y")
    await expect(child, "done: slack_post_thread_reply=")
    await expect(child, "now runs slack/personal-slack postThreadReply")
    await expect(child, "it was not sent")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert fake_mcp.runs("slack_post_thread_reply") == 0


async def test_session_settings_narrow_the_next_turn_and_widening_fails_it(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp, tmp_path: Path
) -> None:
    cli_home.write_config(
        config_yaml(
            {
                "connectors": {
                    "mcp": fake_mcp.url,
                    "enabled": [
                        "hackernews",
                        "slack/support-slack",
                        {"gmail/personal": {"tools": ["getMessage"]}},
                    ],
                },
                "local_tools": {**NO_LOCAL_TOOLS, "files": True, "shell": {"enabled": True}},
            }
        )
    )
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline("tools")
    await expect(
        child,
        "tools: ask_user,edit_file,gmail_get_message,hackernews_get_top_stories,list_directory,"
        "read_file,run_shell_command,search_files,slack_post_thread_reply,write_file",
    )
    workspace = cli_home.home / "dex-agent-workspace"
    flow_type = session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        narrowed = {
            "model": "narrow-model",
            "connectors": {"enabled": ["hackernews"]},
            "local_tools": {"shell": {"enabled": False}},
            "approval": {"always": ["hackernews_get_top_stories"]},
            "theme": "dark",
        }
        assert await sessions.update_session_settings(session_id, narrowed)
        child.sendline("tools")
        await expect(child, "Session settings keys ignored: /theme")
        await expect(
            child,
            "tools: ask_user,edit_file,hackernews_get_top_stories,list_directory,read_file,"
            "search_files,write_file",
        )
        child.sendline("model")
        await expect(child, "model: narrow-model")
        child.sendline("call hackernews_get_top_stories {}")
        await expect(child, "[approval] hackernews_get_top_stories {}")
        child.sendline("y")
        await expect(child, "done: hackernews_get_top_stories=")

        widened = {"connectors": {"enabled": ["gmail/work"]}}
        assert await sessions.update_session_settings(session_id, widened)
        child.sendline("tools")
        await expect(child, "[turn failed]", timeout=120)
        await expect(child, 'widens_connectors at "/connectors/enabled/0"')
        await expect(child, "> ")
    child.sendeof()
    # A failed turn makes the command exit with code 1.
    assert await wait_for_exit(child) == 1
    assert fake_mcp.runs("gmail_get_message") == 0


async def test_an_unreachable_explicit_mcp_server_fails_the_command(
    cli_home: CliHome,
) -> None:
    (closed_port,) = find_free_ports(1)
    cli_home.write_config(
        config_yaml({"connectors": {"mcp": f"http://127.0.0.1:{closed_port}/mcp"}})
    )
    completed = cli_home.run()
    assert completed.returncode == 1
    assert "The connectors MCP server is unavailable: cannot reach" in completed.stderr


@contextlib.contextmanager
def unavailable_mcp_server(reason: str) -> Iterator[str]:
    class AnswerUnavailable(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            self._answer()

        def do_POST(self) -> None:
            self._answer()

        def _answer(self) -> None:
            body = reason.encode()
            self.send_response(503)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format: str, *args: object) -> None:
            del format, args

    server = ThreadingHTTPServer(("127.0.0.1", 0), AnswerUnavailable)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}/mcp"
    finally:
        server.shutdown()


async def test_an_explicit_mcp_server_that_is_starting_fails_with_its_reason(
    cli_home: CliHome,
) -> None:
    with unavailable_mcp_server("the connectors MCP server is starting") as url:
        cli_home.write_config(
            config_yaml({"connectors": {"mcp": url}, "local_tools": NO_LOCAL_TOOLS})
        )
        completed = await asyncio.to_thread(cli_home.run, stdin="tools\n")
    assert completed.returncode == 1
    assert "is not ready: the connectors MCP server is starting" in completed.stderr


@contextlib.contextmanager
def mcp_server_that_fails_listing(
    reason: str, successful_listings: int = 0, failure_status: int = 503
) -> Iterator[str]:
    """Answer ``initialize``, notifications, and the first listings, then ``failure_status``.

    Each successful ``tools/list`` lists no tools.
    """
    listings_left = [successful_listings]

    class AnswerInitializeOnly(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            self._answer(503, "text/plain", reason.encode())

        def do_DELETE(self) -> None:
            self._answer(204, "text/plain", b"")

        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            request = json.loads(self.rfile.read(length) or b"{}")
            method = request.get("method", "") if isinstance(request, dict) else ""
            if method == "initialize":
                result = {
                    "protocolVersion": request["params"]["protocolVersion"],
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "fails-listing", "version": "0.0.0"},
                }
                body = json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result})
                self._answer(200, "application/json", body.encode())
            elif method.startswith("notifications/"):
                self._answer(202, "text/plain", b"")
            elif method == "tools/list" and listings_left[0] > 0:
                listings_left[0] -= 1
                body = json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": {"tools": []}})
                self._answer(200, "application/json", body.encode())
            else:
                self._answer(failure_status, "text/plain", reason.encode())

        def _answer(self, status: int, content_type: str, body: bytes) -> None:
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format: str, *args: object) -> None:
            del format, args

    server = ThreadingHTTPServer(("127.0.0.1", 0), AnswerInitializeOnly)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}/mcp"
    finally:
        server.shutdown()


async def test_an_explicit_mcp_server_that_answers_initialize_but_fails_listing_only_warns(
    cli_home: CliHome,
) -> None:
    with mcp_server_that_fails_listing("the catalog is not ready") as url:
        cli_home.write_config(
            config_yaml({"connectors": {"mcp": url}, "local_tools": NO_LOCAL_TOOLS})
        )
        completed = await asyncio.to_thread(cli_home.run, stdin="hello\n")
    assert completed.returncode == 0, completed.stderr
    assert "echo: hello" in completed.stdout
    output = completed.stdout + completed.stderr
    connector_lines = [
        line
        for line in output.splitlines()
        if "connectors MCP server is unavailable" in line
        or "Connector tools are unavailable" in line
    ]
    assert len(connector_lines) == 1, output
    assert "the catalog is not ready" not in output
    log_text = (cli_home.home / ".dex" / "agents" / "scripted.log").read_text(encoding="utf-8")
    assert f"connectors MCP server {url} could not be listed" in log_text
    assert "the catalog is not ready" in log_text


async def test_a_fixed_tool_set_harness_prints_one_line_when_its_first_listing_fails(
    cli_home: CliHome,
) -> None:
    # The startup catalog read lists; the source's first listing then fails.
    with mcp_server_that_fails_listing("the catalog is not ready", successful_listings=1) as url:
        cli_home.write_config(
            config_yaml({"connectors": {"mcp": url}, "local_tools": NO_LOCAL_TOOLS})
        )
        completed = await asyncio.to_thread(
            cli_home.run, stdin="hello\n", command_module="tests.fixed_tool_set_cli"
        )
    assert completed.returncode == 0, completed.stderr
    output = completed.stdout + completed.stderr
    connector_lines = [line for line in output.splitlines() if _CONNECTOR_LINE_PATTERN.search(line)]
    assert len(connector_lines) == 1, output
    assert connector_lines[0].startswith("The connectors MCP server is unavailable. See "), output
    assert "Restart it once the connectors MCP server answers." in connector_lines[0]


@contextlib.contextmanager
def mcp_server_that_denies_access(status: int) -> Iterator[str]:
    """Answer every request with ``status``, as an endpoint that needs credentials does."""

    class DenyAccess(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            self._answer()

        def do_POST(self) -> None:
            self._answer()

        def do_DELETE(self) -> None:
            self._answer()

        def _answer(self) -> None:
            body = b"access denied"
            self.send_response(status)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format: str, *args: object) -> None:
            del format, args

    server = ThreadingHTTPServer(("127.0.0.1", 0), DenyAccess)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}/mcp"
    finally:
        server.shutdown()


async def test_an_explicit_mcp_server_that_denies_access_fails_with_the_status(
    cli_home: CliHome,
) -> None:
    with mcp_server_that_denies_access(401) as url:
        cli_home.write_config(
            config_yaml({"connectors": {"mcp": url}, "local_tools": NO_LOCAL_TOOLS})
        )
        completed = await asyncio.to_thread(cli_home.run, stdin="hello\n")
    assert completed.returncode == 1
    assert f"{url} denied access (HTTP 401)" in completed.stderr


async def test_a_server_that_denies_access_prints_one_line_that_says_so() -> None:
    config = resolve_config({"connectors": {"mcp": "auto"}}, "builtin", "scripted-model")
    lines: list[str] = []
    with mcp_server_that_denies_access(403) as url:
        source = ConnectorsMcpToolSource(
            url,
            TurnConfigResolver(config, None, lambda warnings: None),
            report=lines.append,
            describe_unavailable=lambda error: connectors_unavailable_line(error, "See the log."),
        )
        assert await source.list_file_enabled_tools() == ()
        assert await source.list_file_enabled_tools() == ()
    assert lines == [
        "Connector tools are unavailable. The connectors MCP server denied access (HTTP 403). "
        "Check its access settings and that connectors.mcp names the right server. See the log."
    ]


async def test_a_server_that_answers_initialize_but_denies_listing_prints_one_line() -> None:
    config = resolve_config({"connectors": {"mcp": "auto"}}, "builtin", "scripted-model")
    lines: list[str] = []
    with mcp_server_that_fails_listing("access denied", failure_status=401) as url:
        source = ConnectorsMcpToolSource(
            url,
            TurnConfigResolver(config, None, lambda warnings: None),
            report=lines.append,
            describe_unavailable=lambda error: connectors_unavailable_line(error, "See the log."),
        )
        assert await source.list_file_enabled_tools() == ()
        assert await source.list_file_enabled_tools() == ()
    assert lines == [
        "Connector tools are unavailable. The connectors MCP server denied access (HTTP 401). "
        "Check its access settings and that connectors.mcp names the right server. See the log."
    ]


async def test_a_command_whose_log_file_cannot_be_written_starts_without_logging(
    cli_home: CliHome,
) -> None:
    log_path = cli_home.home / ".dex" / "agents" / "scripted.log"
    log_path.mkdir(parents=True)
    completed = await asyncio.to_thread(cli_home.run, stdin="hello\n")
    assert completed.returncode == 0, completed.stderr
    assert "echo: hello" in completed.stdout
    assert f"Logging is off: cannot write {log_path}: " in completed.stderr
    # The connector line no longer points at the missing log file.
    assert (
        "Connector tools are unavailable for now; each turn tries again. "
        f"See http://127.0.0.1:{cli_home.web_port}/v1/connectors for connections."
    ) in completed.stdout


async def test_an_unauthorized_connection_prints_a_hint_and_opens_the_connectors_page(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    cli_home.write_config(
        config_yaml({"connectors": {"mcp": fake_mcp.url, "enabled": ["hackernews", "gmail/work"]}})
    )
    child = cli_home.spawn()
    connectors_page = f"http://127.0.0.1:{cli_home.web_port}/v1/connectors"
    await expect(
        child, f"No authorized connection for gmail/work. Authorize it on {connectors_page}."
    )
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert cli_home.opened_urls.read_text().split() == [connectors_page]


async def test_connectors_lists_the_tools_and_whether_the_config_enables_them(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    cli_home.write_config(
        config_yaml({"connectors": {"mcp": fake_mcp.url, "enabled": ["hackernews"]}})
    )
    completed = await asyncio.to_thread(cli_home.run, "connectors")
    assert completed.returncode == 0, completed.stderr
    assert (
        "hackernews_get_top_stories  (hackernews/default getTopStories)  enabled"
        in completed.stdout
    )
    assert "slack_post_thread_reply  (slack/support-slack postThreadReply)  not enabled" in (
        completed.stdout
    )
    assert f"Connections in Dex Web (http://127.0.0.1:{cli_home.web_port}/v1/connectors)" in (
        completed.stdout
    )


async def test_an_unavailable_auto_mcp_server_prints_one_short_line_across_turns(
    cli_home: CliHome,
) -> None:
    # This dexcli dev serves no connectors MCP server, so connectors.mcp: auto only warns.
    completed = await asyncio.to_thread(cli_home.run, stdin="hello\nagain\n")
    assert completed.returncode == 0, completed.stderr
    assert "echo: hello" in completed.stdout
    assert "echo: again" in completed.stdout
    output = completed.stdout + completed.stderr
    log_path = cli_home.home / ".dex" / "agents" / "scripted.log"
    connector_lines = [
        line
        for line in output.splitlines()
        if re.search("Connector tools|MCP server|tool listing", line, re.IGNORECASE)
    ]
    assert connector_lines == [
        "Connector tools are unavailable for now; each turn tries again. "
        f"See {log_path} for the error and "
        f"http://127.0.0.1:{cli_home.web_port}/v1/connectors for connections."
    ], output
    log = log_path.read_text(encoding="utf-8")
    failure = re.search(r"connectors MCP server \S+ is unavailable: (.+)", log)
    assert failure is not None, log
    assert failure.group(1) not in output
    assert "out of the tool listing" in log


async def test_a_server_that_stops_and_returns_prints_one_line_each_way(
    fake_mcp: FakeConnectorsMcp, caplog: pytest.LogCaptureFixture
) -> None:
    config = resolve_config({"connectors": {"mcp": fake_mcp.url}}, "builtin", "scripted-model")
    lines: list[str] = []
    source = ConnectorsMcpToolSource(
        fake_mcp.url,
        TurnConfigResolver(config, None, lambda warnings: None),
        report=lines.append,
        describe_unavailable=lambda error: connectors_unavailable_line(error, "See the log file."),
    )
    assert await source.list_file_enabled_tools()
    fake_mcp.stop()
    with caplog.at_level(logging.WARNING, logger="dex_agents_cli_core.connectors"):
        # Two turns' listings fail: one short line, and each error in the log.
        assert await source.list_file_enabled_tools() == ()
        assert await source.list_file_enabled_tools() == ()
    assert lines == [
        "Connector tools are unavailable for now; each turn tries again. See the log file."
    ]
    failures = [
        record
        for record in caplog.records
        if record.name == "dex_agents_cli_core.connectors"
        and record.getMessage().startswith(f"connectors MCP server {fake_mcp.url} is unavailable: ")
    ]
    assert len(failures) == 2
    await fake_mcp.start()
    assert await source.list_file_enabled_tools()
    unavailable = (
        "Connector tools are unavailable for now; each turn tries again. See the log file."
    )
    assert lines == [unavailable, "Connector tools are available again."]
    # A second outage and recovery print one line each way again.
    fake_mcp.stop()
    assert await source.list_file_enabled_tools() == ()
    assert await source.list_file_enabled_tools() == ()
    await fake_mcp.start()
    assert await source.list_file_enabled_tools()
    assert await source.list_file_enabled_tools()
    assert lines == [unavailable, "Connector tools are available again."] * 2


async def test_a_server_unavailable_at_startup_prints_only_that_it_is_available_again(
    fake_mcp: FakeConnectorsMcp,
) -> None:
    config = resolve_config({"connectors": {"mcp": fake_mcp.url}}, "builtin", "scripted-model")
    lines: list[str] = []
    fake_mcp.stop()
    # The command already printed the startup line for this error.
    source = ConnectorsMcpToolSource(
        fake_mcp.url,
        TurnConfigResolver(config, None, lambda warnings: None),
        report=lines.append,
        startup_error=ConnectorsMcpUnavailableError("the server is starting"),
    )
    assert await source.list_file_enabled_tools() == ()
    assert lines == []
    await fake_mcp.start()
    assert await source.list_file_enabled_tools()
    assert await source.list_file_enabled_tools()
    assert lines == ["Connector tools are available again."]
