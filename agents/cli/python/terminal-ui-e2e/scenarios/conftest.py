# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fixtures of the terminal UI scenarios: the commands, a shared dexcli dev, and chat runs.

Every scenario runs once per command named with ``--command``. A scenario
marked ``needs_ask_user`` or ``needs_slow_streams`` runs only for commands
with that feature, so the other commands do not collect it.
"""

from __future__ import annotations

import os
import re
import shutil
import tempfile
from collections.abc import Callable, Iterator, Mapping, Sequence
from pathlib import Path
from typing import Any

import pytest

from dex_agents_cli_terminal_ui_e2e.chat_run import ChatRun, ChatRunFactory, CommandBinaries
from dex_agents_cli_terminal_ui_e2e.command_targets import (
    TERMINAL_UI_COMMANDS,
    TerminalUiCommand,
)
from dex_agents_cli_testing.e2e.dex_server import DexServer
from dex_agents_cli_testing.e2e.toolchain import PackageSource, Toolchain

_CHECKOUT = Path(__file__).resolve().parents[5]
_NO_COMMAND_ID = "no-command-with-this-feature"
_FEATURE_MARKERS: dict[str, Callable[[TerminalUiCommand], bool]] = {
    "needs_ask_user": lambda command: command.has_ask_user,
    "needs_slow_streams": lambda command: command.has_slow_streams,
}


def pytest_addoption(parser: pytest.Parser) -> None:
    group = parser.getgroup("terminal UI scenarios")
    group.addoption(
        "--command",
        action="append",
        dest="commands",
        choices=sorted(TERMINAL_UI_COMMANDS),
        help="a command to run every scenario with; repeat it; default py-openai-sdk",
    )
    group.addoption(
        "--dexcli",
        type=Path,
        default=Path(os.environ["DEXCLI"]) if os.environ.get("DEXCLI") else None,
        help="a dexcli with agents-tui, such as one built from this checkout (default DEXCLI)",
    )
    group.addoption(
        "--work-dir",
        type=Path,
        default=Path(tempfile.gettempdir()) / "dex-agents-terminal-ui-e2e",
        help="builds, homes, transcripts, and logs",
    )
    group.addoption(
        "--checkout",
        type=Path,
        default=_CHECKOUT,
        help="the checkout whose agents/ the commands are built from (default this one)",
    )
    group.addoption("--python", default="3.13", help="the Python of the command environments")
    group.addoption("--node-bin-dir", type=Path, help="a Node.js 22 or 24 directory")


def pytest_configure(config: pytest.Config) -> None:
    for marker in _FEATURE_MARKERS:
        config.addinivalue_line("markers", f"{marker}: run only for commands with this feature")


def pytest_generate_tests(metafunc: pytest.Metafunc) -> None:
    if "command" not in metafunc.fixturenames:
        return
    names = metafunc.config.getoption("commands") or ["py-openai-sdk"]
    commands = [TERMINAL_UI_COMMANDS[name] for name in dict.fromkeys(names)]
    for marker, has_feature in _FEATURE_MARKERS.items():
        if metafunc.definition.get_closest_marker(marker) is not None:
            commands = [command for command in commands if has_feature(command)]
    if not commands:
        # Deselected in pytest_collection_modifyitems: no chosen command has the feature.
        metafunc.parametrize("command", [None], ids=[_NO_COMMAND_ID])
        return
    metafunc.parametrize("command", commands, ids=[command.name for command in commands])


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    unsupported = [item for item in items if f"[{_NO_COMMAND_ID}" in item.name]
    if unsupported:
        config.hook.pytest_deselected(items=unsupported)
        items[:] = [item for item in items if item not in unsupported]


@pytest.fixture(scope="session")
def work_dir(pytestconfig: pytest.Config) -> Path:
    directory = Path(pytestconfig.getoption("work_dir")).resolve()
    directory.mkdir(parents=True, exist_ok=True)
    return directory


@pytest.fixture(scope="session")
def dexcli(pytestconfig: pytest.Config) -> Path:
    path = pytestconfig.getoption("dexcli")
    if path is None:
        raise pytest.UsageError("pass --dexcli or set DEXCLI to a dexcli with agents-tui")
    return Path(path).resolve()


@pytest.fixture(scope="session")
def toolchain(pytestconfig: pytest.Config, work_dir: Path, dexcli: Path) -> Toolchain:
    return Toolchain(
        checkout=Path(pytestconfig.getoption("checkout")).resolve(),
        work_dir=work_dir,
        dexcli=dexcli,
        node_bin_dir=pytestconfig.getoption("node_bin_dir"),
        python_version=pytestconfig.getoption("python"),
        source=PackageSource.LOCAL,
        published_version="",
    )


@pytest.fixture(scope="session")
def shared_dex_server(dexcli: Path, work_dir: Path) -> Iterator[DexServer]:
    """One private dexcli dev for every scenario that does not start its own."""
    server = DexServer.private(dexcli)
    try:
        print(server.start(work_dir, None, None))
        yield server
    finally:
        server.stop_if_started()


@pytest.fixture(scope="session")
def built_commands(toolchain: Toolchain) -> dict[str, list[str]]:
    """Each command's argv prefix, built from the checkout once per pytest run."""
    return {}


@pytest.fixture
def binaries(
    command: TerminalUiCommand,
    toolchain: Toolchain,
    built_commands: dict[str, list[str]],
    dexcli: Path,
) -> CommandBinaries:
    case = command.case
    if case.name not in built_commands:
        if case.is_python:
            argv = toolchain.build_python_command(case.package_dir_name, case.command)
        else:
            argv = toolchain.build_typescript_command(
                case.package_dir_name, case.adapter_dir_name, case.command
            )
        built_commands[case.name] = argv
    return CommandBinaries(built_commands[case.name], dexcli, toolchain.search_path())


@pytest.fixture
def chat_runs(
    request: pytest.FixtureRequest,
    command: TerminalUiCommand,
    binaries: CommandBinaries,
    work_dir: Path,
) -> Iterator[ChatRunFactory]:
    """Create chat runs; ``own_dev_server=True`` lets the command start its own dexcli dev."""
    runs: list[ChatRun] = []
    case_name = re.sub(r"[^A-Za-z0-9_.-]+", "-", request.node.name)

    def create(turns: Sequence[Mapping[str, Any]], own_dev_server: bool = False) -> ChatRun:
        server = None if own_dev_server else request.getfixturevalue("shared_dex_server")
        run = ChatRun(
            command,
            binaries,
            server,
            work_dir / "cases" / f"{case_name}-{len(runs)}",
            turns,
        )
        runs.append(run)
        return run

    yield create
    for run in runs:
        run.close()


@pytest.fixture
def stub_dexcli_without_terminal_ui(tmp_path: Path, dexcli: Path) -> Path:
    """A dexcli that has no ``agents-tui``, as releases before 1.46.0, and runs the rest."""
    stub = tmp_path / "dexcli-without-agents-tui"
    stub.write_text(
        "#!/bin/sh\n"
        'if [ "$1" = agents-tui ]; then\n'
        '  echo "unknown command: agents-tui" >&2\n'
        "  exit 2\n"
        "fi\n"
        f'exec "{dexcli}" "$@"\n',
        encoding="utf-8",
    )
    stub.chmod(0o755)
    assert shutil.which(str(stub)) is not None
    return stub
