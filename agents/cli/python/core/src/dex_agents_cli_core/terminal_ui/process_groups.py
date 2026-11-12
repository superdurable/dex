# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Worker host's run record and tool process groups, and cleanup of leftover ones.

The Worker host records itself, its launcher, and every process group a local
tool starts in ``~/.dex/agents/run/<harness>-<pid>.json``. It signals a tool
group only while the group's leader is its unreaped child, the one proof that
the group is still its own. A later command stops what a killed Worker host
left behind, after checking each process's identity again before each signal.
"""

from __future__ import annotations

import contextlib
import json
import logging
import os
import signal
import subprocess
import threading
import time
from collections.abc import Callable, Iterable
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any

#: Seconds between SIGTERM and SIGKILL when a process group is stopped.
STOP_GRACE_SECONDS = 2.0
_PS_TIMEOUT_SECONDS = 5.0
_POLL_SECONDS = 0.05
_LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class ProcessIdentity:
    """Who a process is, as ``ps`` shows it, so a reused pid is never signaled.

    Attributes:
        pid: The process ID.
        pgid: Its process group ID.
        started: Its start time, as ``ps -o lstart`` prints it in the C locale.
        command: Its full command line.
    """

    pid: int
    pgid: int
    started: str
    command: str

    def is_group_leader(self) -> bool:
        """Return whether the process leads its own process group."""
        return self.pid == self.pgid


def read_process_identity(pid: int) -> ProcessIdentity | None:
    """Return a running process's identity, or ``None`` when it is gone or unreadable."""
    try:
        completed = subprocess.run(
            ["ps", "-ww", "-o", "pgid=", "-o", "lstart=", "-o", "command=", "-p", str(pid)],
            capture_output=True,
            text=True,
            timeout=_PS_TIMEOUT_SECONDS,
            env={"LC_ALL": "C", "PATH": "/bin:/usr/bin"},
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        _LOGGER.warning("cannot read the identity of process %d: %s", pid, error)
        return None
    fields = completed.stdout.strip().split(maxsplit=6)
    if completed.returncode != 0 or len(fields) < 6 or not fields[0].isdigit():
        return None
    command = fields[6] if len(fields) == 7 else ""
    return ProcessIdentity(
        pid=pid, pgid=int(fields[0]), started=" ".join(fields[1:6]), command=command
    )


class ToolProcessGroupRegistry:
    """The process groups the Worker host's local tools started, mirrored in its run record.

    A tool registers its child right after the spawn and unregisters it right
    after reaping it. Every method is thread-safe, because the watchdog thread
    stops the groups when the event loop does not.
    """

    def __init__(
        self,
        record_path: Path,
        worker_host: ProcessIdentity | None,
        launcher: ProcessIdentity | None,
    ) -> None:
        """Keep the record at ``record_path`` for a Worker host and its launcher."""
        self._record_path = record_path
        self._worker_host = worker_host
        self._launcher = launcher
        self._groups: dict[int, ProcessIdentity | None] = {}
        self._lock = threading.Lock()

    @property
    def record_path(self) -> Path:
        """Return the run record's path."""
        return self._record_path

    def write_record(self) -> None:
        """Write the run record with the groups registered now."""
        with self._lock:
            self._write_record_locked()

    def register(self, pid: int) -> None:
        """Register a tool's child, which leads its own process group, right after its spawn."""
        identity = read_process_identity(pid)
        with self._lock:
            self._groups[pid] = identity
            self._write_record_locked()

    def unregister(self, pid: int) -> None:
        """Unregister a tool's child right after reaping it."""
        with self._lock:
            if pid in self._groups:
                del self._groups[pid]
                self._write_record_locked()

    def stop_all(self, grace_seconds: float = STOP_GRACE_SECONDS) -> None:
        """Send SIGTERM to every registered group, then SIGKILL to those left after the grace."""
        with self._lock:
            groups = list(self._groups)
        stop_process_groups(groups, grace_seconds)

    def remove_record(self) -> None:
        """Delete the run record after an orderly exit."""
        with self._lock, contextlib.suppress(FileNotFoundError):
            self._record_path.unlink()

    def _write_record_locked(self) -> None:
        record = {
            "worker_host": _identity_fields(self._worker_host),
            "launcher": _identity_fields(self._launcher),
            "tool_process_groups": [
                _identity_fields(identity) for identity in self._groups.values() if identity
            ],
        }
        self._record_path.parent.mkdir(parents=True, exist_ok=True)
        temporary = self._record_path.with_suffix(".tmp")
        temporary.write_text(json.dumps(record), encoding="utf-8")
        temporary.replace(self._record_path)


def stop_process_groups(pgids: Iterable[int], grace_seconds: float = STOP_GRACE_SECONDS) -> None:
    """Send SIGTERM to process groups the caller owns, then SIGKILL after the grace.

    Call it only for groups whose leader is the caller's unreaped child.
    """
    signaled = [pgid for pgid in pgids if _signal_group(pgid, signal.SIGTERM)]
    deadline = time.monotonic() + grace_seconds
    while signaled and time.monotonic() < deadline:
        signaled = [pgid for pgid in signaled if _group_exists(pgid)]
        if signaled:
            time.sleep(_POLL_SECONDS)
    for pgid in signaled:
        _signal_group(pgid, signal.SIGKILL)


@dataclass(frozen=True)
class LeftoverCleanup:
    """What a cleanup of one run record stopped.

    Attributes:
        worker_host_pid: The Worker host it stopped, or ``None``.
        tool_group_count: How many tool process groups it stopped.
    """

    worker_host_pid: int | None
    tool_group_count: int

    def describe(self) -> str | None:
        """Return the line to show, or ``None`` when nothing was stopped."""
        groups = "tool process group" if self.tool_group_count == 1 else "tool process groups"
        if self.worker_host_pid is not None:
            tools = f" and {self.tool_group_count} {groups}" if self.tool_group_count else ""
            return f"Stopped a leftover worker host (pid {self.worker_host_pid}){tools}."
        if self.tool_group_count:
            return f"Stopped {self.tool_group_count} leftover {groups}."
        return None


def stop_leftover_processes(
    run_directory: Path, report: Callable[[str], None], *, grace_seconds: float = STOP_GRACE_SECONDS
) -> None:
    """Stop what Worker hosts whose launcher is gone left behind, and delete their records.

    Args:
        run_directory: ``~/.dex/agents/run``.
        report: Prints one line per record that stopped something.
        grace_seconds: Seconds between SIGTERM and SIGKILL.
    """
    if not run_directory.is_dir():
        return
    for record_path in sorted(run_directory.glob("*.json")):
        cleanup = stop_recorded_processes(
            record_path, is_launcher_checked=True, grace_seconds=grace_seconds
        )
        line = cleanup.describe() if cleanup is not None else None
        if line is not None:
            report(line)


def stop_recorded_processes(
    record_path: Path, *, is_launcher_checked: bool, grace_seconds: float = STOP_GRACE_SECONDS
) -> LeftoverCleanup | None:
    """Stop the Worker host and tool groups one run record names, then delete the record.

    Each process gets a signal only while it still has the recorded start
    time, command line, and process group, and leads that group.

    Args:
        record_path: The run record.
        is_launcher_checked: Whether to leave the record alone while its launcher still runs.
        grace_seconds: Seconds between SIGTERM and SIGKILL.

    Returns:
        What it stopped, or ``None`` when the launcher still runs.
    """
    try:
        record: dict[str, Any] = json.loads(record_path.read_text(encoding="utf-8"))
        worker_host = _identity_from_fields(record.get("worker_host"))
        launcher = _identity_from_fields(record.get("launcher"))
        tools = [_identity_from_fields(fields) for fields in record["tool_process_groups"]]
    except (OSError, ValueError, KeyError, TypeError):
        with contextlib.suppress(FileNotFoundError):
            record_path.unlink()
        return LeftoverCleanup(None, 0)
    if is_launcher_checked and launcher is not None and _is_same_process(launcher):
        return None
    targets = [identity for identity in (worker_host, *tools) if identity is not None]
    signaled = [identity for identity in targets if _signal_if_same(identity, signal.SIGTERM)]
    deadline = time.monotonic() + grace_seconds
    remaining = list(signaled)
    while remaining and time.monotonic() < deadline:
        remaining = [identity for identity in remaining if _group_exists(identity.pgid)]
        if remaining:
            time.sleep(_POLL_SECONDS)
    for identity in remaining:
        _signal_if_same(identity, signal.SIGKILL)
    with contextlib.suppress(FileNotFoundError):
        record_path.unlink()
    return LeftoverCleanup(
        worker_host.pid if worker_host is not None and worker_host in signaled else None,
        sum(1 for identity in signaled if identity is not worker_host),
    )


def _signal_if_same(identity: ProcessIdentity, signal_number: int) -> bool:
    if not identity.is_group_leader() or not _is_same_process(identity):
        return False
    return _signal_group(identity.pgid, signal_number)


def _is_same_process(identity: ProcessIdentity) -> bool:
    return read_process_identity(identity.pid) == identity


def _signal_group(pgid: int, signal_number: int) -> bool:
    try:
        os.killpg(pgid, signal_number)
    except (ProcessLookupError, PermissionError):
        return False
    return True


def _group_exists(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def _identity_fields(identity: ProcessIdentity | None) -> dict[str, Any] | None:
    return None if identity is None else asdict(identity)


def _identity_from_fields(fields: object) -> ProcessIdentity | None:
    if fields is None:
        return None
    if not isinstance(fields, dict):
        raise TypeError("a run record identity is not an object")
    return ProcessIdentity(
        pid=int(fields["pid"]),
        pgid=int(fields["pgid"]),
        started=str(fields["started"]),
        command=str(fields["command"]),
    )
