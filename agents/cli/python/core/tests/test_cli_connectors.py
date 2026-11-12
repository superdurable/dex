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
import threading
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from dex_agents.testing import find_free_ports
from dex_agents_cli_core import session_flow_type
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


async def test_an_unauthorized_connection_prints_a_hint_and_opens_the_connectors_page(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    cli_home.write_config(
        config_yaml({"connectors": {"mcp": fake_mcp.url, "enabled": ["hackernews", "gmail/work"]}})
    )
    # The fake BROWSER records the URL, so this test may let the command open it.
    cli_home.extra_environment["DEX_AGENTS_OPEN_BROWSER"] = "1"
    child = cli_home.spawn()
    connectors_page = f"http://127.0.0.1:{cli_home.web_port}/v1/connectors"
    await expect(
        child, f"No authorized connection for gmail/work. Authorize it on {connectors_page}."
    )
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert cli_home.opened_urls.read_text().split() == [connectors_page]


async def test_dex_agents_open_browser_0_keeps_the_connectors_page_closed_at_a_terminal(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    cli_home.write_config(
        config_yaml({"connectors": {"mcp": fake_mcp.url, "enabled": ["hackernews", "gmail/work"]}})
    )
    assert cli_home.environment()["DEX_AGENTS_OPEN_BROWSER"] == "0"
    child = cli_home.spawn()
    connectors_page = f"http://127.0.0.1:{cli_home.web_port}/v1/connectors"
    await expect(
        child, f"No authorized connection for gmail/work. Authorize it on {connectors_page}."
    )
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert not cli_home.opened_urls.exists()


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
