# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A private dexcli dev that fails to start leaves no process behind its exited parent."""

from __future__ import annotations

import contextlib
import os
import signal
import time
from pathlib import Path

import pytest

from dex_agents_cli_testing.e2e.dex_server import DexServer

FAKE_DEXCLI = """#!/bin/sh
sleep 300 &
echo $! > "$FAKE_DEXCLI_CHILD_PID_FILE"
exit 1
"""


def test_stop_if_started_stops_children_after_dexcli_exits(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake_dexcli = tmp_path / "fake-dexcli"
    fake_dexcli.write_text(FAKE_DEXCLI)
    fake_dexcli.chmod(0o755)
    child_pid_file = tmp_path / "child.pid"
    monkeypatch.setenv("FAKE_DEXCLI_CHILD_PID_FILE", str(child_pid_file))
    server = DexServer.private(fake_dexcli)
    child_pid: int | None = None
    try:
        with pytest.raises(RuntimeError, match="did not become ready"):
            server.start(tmp_path / "work", seed=None, connectors_mcp_binary=None)
        child_pid = int(child_pid_file.read_text())
        assert _is_running(child_pid), "the fake dexcli child should outlive its parent"

        server.stop_if_started()

        assert _wait_until_gone(child_pid, timeout_seconds=10)
    finally:
        if child_pid is not None:
            with contextlib.suppress(ProcessLookupError):
                os.kill(child_pid, signal.SIGKILL)


def _wait_until_gone(pid: int, timeout_seconds: float) -> bool:
    deadline = time.monotonic() + timeout_seconds
    while _is_running(pid):
        if time.monotonic() >= deadline:
            return False
        time.sleep(0.1)
    return True


def _is_running(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    return True
