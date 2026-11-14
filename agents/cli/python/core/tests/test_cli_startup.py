# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Starting a command: configuration, init, and finding or starting dexcli dev."""

from __future__ import annotations

import asyncio
import json
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from dex_agents.testing import find_free_ports
from dex_agents_cli_core import parse_config_yaml
from dex_agents_cli_core.dex_server import describe_unavailable_dex_server
from tests.conftest import PACKAGE_DIR, CliHome, config_yaml, expect, wait_for_exit
from tests.unavailable_dex_server import UNAVAILABLE_DETAILS


async def test_tty_without_a_config_asks_the_init_questions_then_chats(cli_home: CliHome) -> None:
    child = cli_home.spawn()
    answers = {
        "Model": "",
        "System prompt": "You answer in haiku.",
        "Connectors to enable": "",
        "Workspace": "",
        "Allow file tools": "y",
        "Allow shell commands": "y",
        "Commands that run without approval": "ls, git status",
        "Allow web fetching": "n",
        "Allow a local browser": "n",
    }
    for question, answer in answers.items():
        await expect(child, question)
        child.sendline(answer)
    await expect(child, f"Wrote {cli_home.config_path}.")
    await expect(child, f"Using the running dexcli dev at {cli_home.address}")
    await expect(child, "> ")
    child.sendline("prompt")
    await expect(child, "prompt: You answer in haiku.")
    child.sendeof()
    await expect(child, "keeps running. Resume it with: dex-agents-scripted --resume")
    assert await wait_for_exit(child) == 0
    written = parse_config_yaml(cli_home.config_path.read_text())
    assert written == {
        "model": "scripted-model",
        "system_prompt": "You answer in haiku.",
        "dex": {"server": "auto"},
        "connectors": {"mcp": "auto"},
        "workspace": "~/dex-agent-workspace",
        "local_tools": {
            "files": True,
            "shell": {"enabled": True, "allow": ["ls", "git status"]},
            "web_fetch": False,
            "browser": False,
        },
        "media": {"store": "auto"},
        "sessions": {"summary": "auto"},
    }


async def test_yes_writes_the_default_config_without_questions(cli_home: CliHome) -> None:
    child = cli_home.spawn("--yes")
    await expect(child, f"Wrote the default configuration to {cli_home.config_path}.")
    await expect(child, "> ")
    child.sendline("hello")
    await expect(child, "echo: hello")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    written = parse_config_yaml(cli_home.config_path.read_text())
    assert isinstance(written, dict)
    assert written["model"] == "scripted-model"


async def test_a_message_typed_with_the_last_answer_reaches_the_chat(cli_home: CliHome) -> None:
    child = cli_home.spawn()
    for question in (
        "Model",
        "System prompt",
        "Connectors to enable",
        "Workspace",
        "Allow file tools",
        "Allow shell commands",
        "Commands that run without approval",
        "Allow web fetching",
    ):
        await expect(child, question)
        child.sendline("")
    await expect(child, "Allow a local browser")
    child.send("\nhello\n")
    await expect(child, f"Wrote {cli_home.config_path}.")
    await expect(child, "echo: hello")
    child.sendeof()
    assert await wait_for_exit(child) == 0


async def test_ctrl_c_at_the_first_init_question_exits_with_130_and_writes_no_file(
    cli_home: CliHome,
) -> None:
    child = cli_home.spawn("init")
    await expect(child, "Model")
    child.sendcontrol("c")
    await expect(child, "Cancelled; no configuration written.")
    assert await wait_for_exit(child) == 130
    assert "Traceback" not in (child.before or "")
    assert not cli_home.config_path.exists()


async def test_ctrl_c_at_a_later_question_of_the_first_run_setup_exits_with_130(
    cli_home: CliHome,
) -> None:
    child = cli_home.spawn()
    await expect(child, "Model")
    child.sendline("")
    await expect(child, "System prompt")
    child.sendline("You answer in haiku.")
    await expect(child, "Connectors to enable")
    child.sendcontrol("c")
    await expect(child, "Cancelled; no configuration written.")
    assert await wait_for_exit(child) == 130
    assert "Traceback" not in (child.before or "")
    assert not cli_home.config_path.exists()


async def test_ctrl_d_at_an_init_question_exits_with_1_and_writes_no_file(
    cli_home: CliHome,
) -> None:
    child = cli_home.spawn("init")
    await expect(child, "Model")
    child.sendline("")
    await expect(child, "System prompt")
    child.sendeof()
    await expect(child, "Cancelled; no configuration written.")
    assert await wait_for_exit(child) == 1
    assert not cli_home.config_path.exists()


_REMOTE_SERVER_CONFIG = {
    "model": "scripted-old",
    "system_prompt": "Old instructions.",
    "dex": {"server": "https://dex.example.com", "auth": {"mode": "bearer", "source": "env"}},
    "connectors": {"enabled": ["slack/team"]},
    "mcp_servers": {"docs": {"url": "https://mcp.example.com/mcp"}},
    "workspace": "~/agent workspace ",
    "local_tools": {"shell": {"allow": ["git status"]}},
    "sessions": {"summary": "off"},
    "builtin": {"temperature": 0.2},
}


async def test_init_at_a_terminal_offers_the_file_values_and_keeps_unasked_fields(
    cli_home: CliHome,
) -> None:
    cli_home.write_config(config_yaml(_REMOTE_SERVER_CONFIG))
    child = cli_home.spawn("init")
    answers = {
        "Model [scripted-old]": "scripted-new",
        "System prompt (empty keeps the current prompt)": "",
        "Connectors to enable, comma-separated (empty for all authorized) [slack/team]": (
            "hackernews"
        ),
        "Workspace": "",
        "Allow file tools in the workspace? (y/n) [y]": "",
        "Allow shell commands? (y/n) [y]": "",
        "Commands that run without approval, comma-separated [git status]": "ls, pwd",
        "Allow web fetching": "n",
        "Allow a local browser": "",
    }
    for question, answer in answers.items():
        await expect(child, question)
        child.sendline(answer)
    await expect(child, f"Wrote {cli_home.config_path}.")
    assert await wait_for_exit(child) == 0
    assert parse_config_yaml(cli_home.config_path.read_text()) == {
        "model": "scripted-new",
        "system_prompt": "Old instructions.",
        "dex": _REMOTE_SERVER_CONFIG["dex"],
        "connectors": {"enabled": ["hackernews"]},
        "mcp_servers": _REMOTE_SERVER_CONFIG["mcp_servers"],
        "workspace": "~/agent workspace ",
        "local_tools": {
            "files": True,
            "shell": {"enabled": True, "allow": ["ls", "pwd"]},
            "web_fetch": False,
            "browser": True,
        },
        "media": {"store": "auto"},
        "sessions": {"summary": "off"},
        "builtin": {"temperature": 0.2},
    }


def test_init_yes_takes_the_file_values_and_keeps_every_other_field(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml(_REMOTE_SERVER_CONFIG))
    completed = cli_home.run("init", "--yes")
    assert completed.returncode == 0, completed.stderr
    assert parse_config_yaml(cli_home.config_path.read_text()) == {
        "model": "scripted-old",
        "system_prompt": "Old instructions.",
        "dex": _REMOTE_SERVER_CONFIG["dex"],
        "connectors": {"enabled": ["slack/team"]},
        "mcp_servers": _REMOTE_SERVER_CONFIG["mcp_servers"],
        "workspace": "~/agent workspace ",
        "local_tools": {
            "files": True,
            "shell": {"enabled": True, "allow": ["git status"]},
            "web_fetch": True,
            "browser": True,
        },
        "media": {"store": "auto"},
        "sessions": {"summary": "off"},
        "builtin": {"temperature": 0.2},
    }


def test_init_writes_through_a_symbolic_link_and_keeps_the_link(cli_home: CliHome) -> None:
    target = cli_home.config_path.parent / "shared" / "scripted.yaml"
    target.parent.mkdir(parents=True)
    target.write_text(config_yaml(_REMOTE_SERVER_CONFIG), encoding="utf-8")
    cli_home.config_path.symlink_to(target)
    completed = cli_home.run("init", "--yes")
    assert completed.returncode == 0, completed.stderr
    assert cli_home.config_path.is_symlink()
    written = parse_config_yaml(target.read_text())
    assert isinstance(written, dict)
    assert written["dex"] == _REMOTE_SERVER_CONFIG["dex"]
    assert written["media"] == {"store": "auto"}


def test_init_follows_a_link_chain_to_a_missing_file_and_keeps_every_link(
    cli_home: CliHome,
) -> None:
    directory = cli_home.config_path.parent
    directory.mkdir(parents=True, exist_ok=True)
    alias = directory / "alias.yaml"
    alias.symlink_to("missing.yaml")
    cli_home.config_path.symlink_to("alias.yaml")
    completed = cli_home.run("init", "--yes")
    assert completed.returncode == 0, completed.stderr
    assert cli_home.config_path.is_symlink()
    assert alias.is_symlink()
    written = parse_config_yaml((directory / "missing.yaml").read_text())
    assert isinstance(written, dict)
    assert written["model"] == "scripted-model"


def test_init_stops_at_a_loop_of_symbolic_links(cli_home: CliHome) -> None:
    directory = cli_home.config_path.parent
    directory.mkdir(parents=True, exist_ok=True)
    loop = directory / "loop.yaml"
    cli_home.config_path.symlink_to("loop.yaml")
    loop.symlink_to(cli_home.config_path)
    completed = cli_home.run("init", "--yes")
    assert completed.returncode != 0
    assert "cannot write" in completed.stdout + completed.stderr
    assert cli_home.config_path.is_symlink()
    assert loop.is_symlink()


@pytest.mark.skipif(sys.platform == "win32", reason="POSIX directory permissions")
def test_a_failed_write_leaves_the_existing_config_unchanged(cli_home: CliHome) -> None:
    original_text = config_yaml(_REMOTE_SERVER_CONFIG)
    cli_home.write_config(original_text)
    directory = cli_home.config_path.parent
    # A read-only directory refuses the temporary file; only an in-place write could succeed.
    directory.chmod(0o555)
    try:
        completed = cli_home.run("init", "--yes")
    finally:
        directory.chmod(0o755)
    assert completed.returncode != 0
    assert "cannot write" in completed.stdout + completed.stderr
    assert cli_home.config_path.read_text() == original_text
    assert [path.name for path in directory.iterdir() if path.name.endswith(".tmp")] == []


def test_init_replaces_a_symbolic_link_at_the_backup_path(cli_home: CliHome) -> None:
    invalid_text = "local_tools:\n  files: yes\n"
    cli_home.write_config(invalid_text)
    unrelated = cli_home.config_path.parent / "unrelated.txt"
    unrelated.write_text("keep me\n", encoding="utf-8")
    backup_path = cli_home.config_path.with_name(cli_home.config_path.name + ".bak")
    backup_path.symlink_to(unrelated)
    completed = cli_home.run("init", "--yes")
    assert completed.returncode == 0, completed.stderr
    assert unrelated.read_text() == "keep me\n"
    assert not backup_path.is_symlink()
    assert backup_path.read_text() == invalid_text


def test_init_replaces_an_invalid_file_keeps_a_backup_and_says_so(cli_home: CliHome) -> None:
    invalid_text = "dex:\n  server: https://dex.example.com\nlocal_tools:\n  files: yes\n"
    cli_home.write_config(invalid_text)
    completed = cli_home.run("init", "--yes")
    assert completed.returncode == 0, completed.stderr
    backup_path = cli_home.config_path.with_name(cli_home.config_path.name + ".bak")
    assert (
        f"{cli_home.config_path} was invalid, so init replaced it; the old file is at "
        f"{backup_path}." in completed.stdout
    )
    assert backup_path.read_text() == invalid_text
    assert parse_config_yaml(cli_home.config_path.read_text()) == {
        "model": "scripted-model",
        "dex": {"server": "auto"},
        "connectors": {"mcp": "auto"},
        "workspace": "~/dex-agent-workspace",
        "local_tools": {
            "files": True,
            "shell": {"enabled": True, "allow": []},
            "web_fetch": True,
            "browser": True,
        },
        "media": {"store": "auto"},
        "sessions": {"summary": "auto"},
    }


async def test_without_a_terminal_it_runs_on_defaults_and_writes_no_file(cli_home: CliHome) -> None:
    completed = cli_home.run(stdin="hello there\n")
    assert completed.returncode == 0, completed.stderr
    assert f"No configuration at {cli_home.config_path}; using the defaults." in completed.stdout
    assert "Run `dex-agents-scripted init` to create one." in completed.stdout
    assert f"Using the running dexcli dev at {cli_home.address}" in completed.stdout
    assert "echo: hello there" in completed.stdout
    # This dexcli dev serves no connectors MCP server, so connectors.mcp: auto only warns.
    assert "Connector tools are unavailable for now; each turn tries again." in completed.stdout
    assert not cli_home.config_path.exists()
    # The default workspace is a dedicated directory under the home, not the home itself.
    assert (cli_home.home / "dex-agent-workspace").is_dir()


async def test_a_fixed_tool_set_harness_is_told_to_restart_for_connector_tools(
    cli_home: CliHome,
) -> None:
    completed = cli_home.run(stdin="hello\n", command_module="tests.fixed_tool_set_cli")
    assert completed.returncode == 0, completed.stderr
    # This dexcli dev serves no connectors MCP server, as with one that is still starting.
    assert "The connectors MCP server is unavailable" in completed.stdout
    assert (
        "dex-agents-scripted fixes its tools when it starts, so this run has no connector "
        "tools. Restart it once the connectors MCP server answers."
    ) in completed.stdout
    assert "each turn tries again" not in completed.stdout


async def test_an_invalid_config_names_the_error_code_and_pointer(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml({"local_tools": {"shell": {"allow": "ls"}}}))
    completed = cli_home.run()
    assert completed.returncode == 2
    assert 'schema at "/local_tools/shell/allow" (schema keyword type)' in completed.stderr


async def test_an_anyof_error_names_the_values_the_field_accepts(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml({"dex": {"server": "localhost:7233"}}))
    completed = cli_home.run()
    assert completed.returncode == 2
    assert (
        'schema at "/dex/server" (schema keyword anyOf). Use "auto" (dexcli dev on this '
        "machine), or the http or https URL of a Dex server, without user info."
    ) in completed.stderr


async def test_an_unreachable_dex_server_fails_with_one_line_that_says_how_to_fix_it(
    cli_home: CliHome,
) -> None:
    (closed_port,) = find_free_ports(1)
    cli_home.write_config(
        config_yaml(
            {"dex": {"server": f"http://127.0.0.1:{closed_port}"}, "connectors": {"enabled": []}}
        )
    )
    completed = cli_home.run(stdin="hello\n")
    assert completed.returncode == 1, completed.stdout + completed.stderr
    assert completed.stderr == (
        f"dex-agents-scripted: cannot reach the Dex server at 127.0.0.1:{closed_port}, which "
        "dex.server names. Start that server, or set dex.server to auto to use dexcli dev on "
        "this machine.\n"
    )


async def test_a_reachable_dex_server_that_answers_unavailable_is_named_with_its_details(
    cli_home: CliHome,
) -> None:
    server = subprocess.Popen(
        [sys.executable, "-m", "tests.unavailable_dex_server"],
        cwd=PACKAGE_DIR,
        stdout=subprocess.PIPE,
        text=True,
    )
    try:
        assert server.stdout is not None
        port = int(server.stdout.readline())
        cli_home.write_config(
            config_yaml(
                {"dex": {"server": f"http://127.0.0.1:{port}"}, "connectors": {"enabled": []}}
            )
        )
        completed = cli_home.run(stdin="hello\n")
    finally:
        server.kill()
        server.wait()
    assert completed.returncode == 1, completed.stdout + completed.stderr
    assert completed.stderr == (
        f"dex-agents-scripted: the Dex server at 127.0.0.1:{port} returned UNAVAILABLE: "
        f"{UNAVAILABLE_DETAILS}. It may still be starting; try again in a moment.\n"
    )


async def test_a_dexcli_variable_that_names_no_executable_says_how_to_fix_it(
    cli_home: CliHome,
) -> None:
    missing = cli_home.home / "no-such-dexcli"
    cli_home.extra_environment["DEXCLI"] = str(missing)
    # Other ports than the shared dexcli dev's, so the command has to start one.
    cli_home.dex_port, cli_home.web_port = find_free_ports(2)
    cli_home.write_config(config_yaml({"connectors": {"enabled": []}}))
    completed = cli_home.run(stdin="hello\n")
    assert completed.returncode == 1
    assert completed.stderr == (
        f"dex-agents-scripted: DEXCLI is not an executable file: {missing}. Unset DEXCLI to use "
        "the dexcli on PATH or the downloaded release, or set it to the path of a dexcli "
        "executable.\n"
    )
    assert "Starting dexcli dev" not in completed.stdout


async def test_auto_starts_dexcli_dev_when_none_runs_and_stops_it_when_input_ends(
    tmp_path: Path,
) -> None:
    dex_port, web_port = find_free_ports(2)
    home = CliHome(tmp_path / "home", dex_port, web_port, tmp_path / "opened-urls.txt")
    home.home.mkdir()
    home.write_config(config_yaml({"connectors": {"enabled": []}}))
    try:
        for message in ("first", "second"):
            completed = home.run(stdin=f"{message}\n")
            assert completed.returncode == 0, completed.stdout + completed.stderr
            assert "Starting dexcli dev in the background" in completed.stdout
            assert "dexcli dev is ready (pid" in completed.stdout
            assert "it stops when this command exits" in completed.stdout
            assert f"echo: {message}" in completed.stdout
            assert "Stopped the dexcli dev this command started." in completed.stdout
            assert not (home.home / ".dex" / "agents" / "dexcli-dev.json").exists()
    finally:
        home.close()


@pytest.mark.parametrize(
    "recorded_url",
    ["started server 9.9.9 with 0 tools at http://127.0.0.1:1/mcp", "http://[bad"],
    ids=["log-line", "malformed-url"],
)
async def test_auto_reuse_tolerates_a_recorded_connectors_url_that_is_not_a_url(
    tmp_path: Path, recorded_url: str
) -> None:
    dex_port, web_port = find_free_ports(2)
    home = CliHome(tmp_path / "home", dex_port, web_port, tmp_path / "opened-urls.txt")
    home.home.mkdir()
    home.write_config(config_yaml({"connectors": {"mcp": "auto"}}))
    state_path = home.home / ".dex" / "agents" / "dexcli-dev.json"
    try:
        # The first session keeps the dexcli dev it started, and its record, while the second runs.
        first = home.spawn()
        await expect(first, "> ", timeout=120)
        recorded = json.loads(state_path.read_text())
        # A dexcli without the Connectors MCP proxy prints no such ready line.
        assert recorded["connectors_mcp_url"] in (f"http://127.0.0.1:{web_port}/mcp", None)
        recorded["connectors_mcp_url"] = recorded_url
        state_path.write_text(json.dumps(recorded))
        second = await asyncio.to_thread(home.run, stdin="tools\n")
        assert second.returncode == 0, second.stdout + second.stderr
        assert f"Using the running dexcli dev at 127.0.0.1:{dex_port}" in second.stdout
        assert "tools: ask_user" in second.stdout
        assert "Stopped the dexcli dev" not in second.stdout
        first.sendline("/exit")
        await expect(first, "Stopped the dexcli dev this command started.")
        assert await wait_for_exit(first) == 0
    finally:
        home.close()


@pytest.mark.parametrize(
    "readiness",
    [
        b'{"status": "ready"}',
        b'{"status": "ready", "definitionRevision": 1, "source": "x", "definitionCount": "2"}',
    ],
    ids=["missing-fields", "wrong-types"],
)
async def test_a_service_that_only_looks_ready_is_not_taken_for_dexcli_dev(
    tmp_path: Path, readiness: bytes
) -> None:
    class LooksReady(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            body = readiness
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format: str, *args: object) -> None:
            del format, args

    impostor = ThreadingHTTPServer(("127.0.0.1", 0), LooksReady)
    threading.Thread(target=impostor.serve_forever, daemon=True).start()
    (dex_port,) = find_free_ports(1)
    home = CliHome(
        tmp_path / "home", dex_port, impostor.server_address[1], tmp_path / "opened-urls.txt"
    )
    home.home.mkdir()
    home.write_config(config_yaml({"connectors": {"enabled": []}}))
    try:
        completed = await asyncio.to_thread(home.run, stdin="hello\n")
    finally:
        impostor.shutdown()
        home.close()
    assert completed.returncode == 1
    assert "answers, but it is not a dexcli dev" in completed.stderr
    assert "echo: hello" not in completed.stdout


class _FakeRpcError(Exception):
    """An error shaped like grpc.RpcError, with a status code name and details."""

    def __init__(self, code_name: str, details: str) -> None:
        super().__init__(details)
        self._code = type("Code", (), {"name": code_name})()
        self._details = details

    def code(self) -> object:
        return self._code

    def details(self) -> str:
        return self._details


def test_an_unavailable_cause_is_found_below_another_status_code() -> None:
    outer = _FakeRpcError("DEADLINE_EXCEEDED", "deadline exceeded")
    outer.__cause__ = _FakeRpcError("UNAVAILABLE", "failed to connect to all addresses")
    assert describe_unavailable_dex_server(outer, "auto", "127.0.0.1:7233") == (
        "cannot reach the Dex server of dexcli dev at 127.0.0.1:7233. Run dexcli dev, or set "
        "dex.server to the URL of a running Dex server."
    )
    other = _FakeRpcError("NOT_FOUND", "no such Flow")
    assert describe_unavailable_dex_server(other, "auto", "127.0.0.1:7233") is None
