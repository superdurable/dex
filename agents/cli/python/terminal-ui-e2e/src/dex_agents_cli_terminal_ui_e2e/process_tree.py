# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The processes a command started: recorded while it runs, stopped and checked after it exits.

A command starts its Worker host and, without a running ``dexcli dev``, a
``dexcli dev`` with ``dex-server-zd``, each in its own process group. They
outlive the command when it dies, so the scenario records every descendant
while the command runs and checks after exit that each one is gone.
"""

from __future__ import annotations

import contextlib
import os
import signal
import subprocess
import time
from dataclasses import dataclass

_EXIT_WAIT_SECONDS = 30.0
_KILL_WAIT_SECONDS = 10.0
_POLL_SECONDS = 0.2


@dataclass(frozen=True)
class OwnedProcess:
    """One descendant of a command.

    Attributes:
        pid: The process ID.
        process_group_id: Its process group.
        command: Its command line, which tells it from a later process with the same PID.
    """

    pid: int
    process_group_id: int
    command: str


class ProcessTreeRecord:
    """Every process that descended from one root process while it was recorded."""

    def __init__(self, root_pid: int) -> None:
        """Record descendants of ``root_pid``; nothing is read until ``record``."""
        self._root_pid = root_pid
        self._owned: dict[int, OwnedProcess] = {}

    @property
    def owned(self) -> list[OwnedProcess]:
        """Return every recorded descendant."""
        return list(self._owned.values())

    def record(self) -> None:
        """Add the root's current descendants to the record."""
        table = _process_table()
        known = {self._root_pid, *self._owned}
        is_growing = True
        while is_growing:
            is_growing = False
            for process, parent_pid in table:
                if parent_pid in known and process.pid not in known:
                    known.add(process.pid)
                    self._owned[process.pid] = process
                    is_growing = True

    def stop_survivors(self) -> list[OwnedProcess]:
        """Wait for every recorded descendant to exit; stop the ones that do not.

        Returns:
            The descendants still running after the wait, which this then signalled:
            SIGTERM to their process groups, then SIGKILL.
        """
        survivors = self._wait_for_exit(_EXIT_WAIT_SECONDS)
        for signal_number in (signal.SIGTERM, signal.SIGKILL):
            remaining = self._running(survivors)
            if not remaining:
                break
            for process_group_id in {process.process_group_id for process in remaining}:
                with contextlib.suppress(ProcessLookupError, PermissionError):
                    os.killpg(process_group_id, signal_number)
            self._wait_for_exit(_KILL_WAIT_SECONDS, remaining)
        return survivors

    def _wait_for_exit(
        self, timeout_seconds: float, processes: list[OwnedProcess] | None = None
    ) -> list[OwnedProcess]:
        watched = self.owned if processes is None else processes
        deadline = time.monotonic() + timeout_seconds
        while True:
            running = self._running(watched)
            if not running or time.monotonic() >= deadline:
                return running
            time.sleep(_POLL_SECONDS)

    def _running(self, processes: list[OwnedProcess]) -> list[OwnedProcess]:
        current = {process.pid: process.command for process, _ in _process_table()}
        return [process for process in processes if current.get(process.pid) == process.command]


def _process_table() -> list[tuple[OwnedProcess, int]]:
    """Return every process with its parent PID, from ``ps``; zombies are left out."""
    listing = subprocess.run(
        ["ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,command="],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    table = []
    for line in listing.splitlines():
        parts = line.split(None, 4)
        if len(parts) < 5 or parts[3].startswith("Z"):
            continue
        table.append((OwnedProcess(int(parts[0]), int(parts[2]), parts[4]), int(parts[1])))
    return table
