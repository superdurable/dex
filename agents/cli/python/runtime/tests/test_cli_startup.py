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
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from dex_agents.testing import find_free_ports
from dex_agents_cli import parse_config_yaml
from tests.conftest import CliHome, config_yaml, expect, wait_for_exit


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


async def test_without_a_terminal_it_runs_on_defaults_and_writes_no_file(cli_home: CliHome) -> None:
    completed = cli_home.run(stdin="hello there\n")
    assert completed.returncode == 0, completed.stderr
    assert f"No configuration at {cli_home.config_path}; using the defaults." in completed.stdout
    assert "Run `dex-agents-scripted init` to create one." in completed.stdout
    assert f"Using the running dexcli dev at {cli_home.address}" in completed.stdout
    assert "echo: hello there" in completed.stdout
    # This dexcli dev serves no connectors MCP server, so connectors.mcp: auto only warns.
    assert "The connectors MCP server is unavailable" in completed.stdout
    assert not cli_home.config_path.exists()
    # The default workspace is a dedicated directory under the home, not the home itself.
    assert (cli_home.home / "dex-agent-workspace").is_dir()


async def test_an_invalid_config_names_the_error_code_and_pointer(cli_home: CliHome) -> None:
    cli_home.write_config(config_yaml({"local_tools": {"shell": {"allow": "ls"}}}))
    completed = cli_home.run()
    assert completed.returncode == 2
    assert 'schema at "/local_tools/shell/allow" (schema keyword type)' in completed.stderr


async def test_auto_starts_dexcli_dev_when_none_runs_and_later_commands_reuse_it(
    tmp_path: Path,
) -> None:
    dex_port, web_port = find_free_ports(2)
    home = CliHome(tmp_path / "home", dex_port, web_port, tmp_path / "opened-urls.txt")
    home.home.mkdir()
    home.write_config(config_yaml({"connectors": {"enabled": []}}))
    try:
        first = home.run(stdin="first\n")
        assert first.returncode == 0, first.stdout + first.stderr
        assert "Starting dexcli dev in the background" in first.stdout
        assert "dexcli dev is ready (pid" in first.stdout
        assert "echo: first" in first.stdout
        assert (home.home / ".dex" / "agents" / "dexcli-dev.json").exists()
        second = home.run(stdin="second\n")
        assert second.returncode == 0, second.stdout + second.stderr
        assert f"Using the running dexcli dev at 127.0.0.1:{dex_port}" in second.stdout
        assert "echo: second" in second.stdout
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
