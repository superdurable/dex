# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""File-based coordination between a test and its Worker child process."""

from __future__ import annotations

import asyncio
from datetime import timedelta
from pathlib import Path

from dex_agents.testing.polling import DEFAULT_WAIT_TIMEOUT, wait_until

_GATE_POLL_INTERVAL_SECONDS = 0.05


class FileGate:
    """Pause code in a Worker child at an exact point until the test releases it.

    The child calls ``mark_reached`` and then awaits ``wait_for_release``. The
    test awaits ``wait_until_reached`` and then kills the Worker, or calls
    ``release``. Both sides construct the gate with the same directory and
    name. The gate survives Worker kills, so a replacement Worker passes a
    released gate immediately.

    Example:
        >>> # In the tool, inside the Worker child:
        >>> gate = FileGate(state_dir, "charge")
        >>> gate.mark_reached()
        >>> await gate.wait_for_release()
        >>> # In the test:
        >>> await FileGate(state_dir, "charge").wait_until_reached()
        >>> worker.kill()
    """

    def __init__(self, directory: Path, name: str) -> None:
        """Name a gate inside a directory both processes can read.

        Args:
            directory: Shared directory, created when needed.
            name: Gate name, unique within the directory.
        """
        self._reached_path = directory / f"{name}.reached"
        self._released_path = directory / f"{name}.released"

    def mark_reached(self) -> None:
        """Record that code reached the gate; repeated calls count each arrival."""
        self._reached_path.parent.mkdir(parents=True, exist_ok=True)
        with self._reached_path.open("a") as reached:
            reached.write("reached\n")

    def reached_count(self) -> int:
        """Return how many times code reached the gate.

        Returns:
            The number of ``mark_reached`` calls across all processes.
        """
        if not self._reached_path.exists():
            return 0
        return len(self._reached_path.read_text().splitlines())

    def is_released(self) -> bool:
        """Report whether the test released the gate.

        Returns:
            ``True`` after ``release``.
        """
        return self._released_path.exists()

    def release(self) -> None:
        """Let current and future waiters pass the gate."""
        self._released_path.parent.mkdir(parents=True, exist_ok=True)
        self._released_path.touch()

    async def wait_for_release(self) -> None:
        """Block until the test releases the gate.

        It never times out: the test either releases the gate or kills the
        process that waits.
        """
        while not self.is_released():
            await asyncio.sleep(_GATE_POLL_INTERVAL_SECONDS)

    async def wait_until_reached(
        self,
        count: int = 1,
        timeout: timedelta = DEFAULT_WAIT_TIMEOUT,
    ) -> None:
        """Wait until code reached the gate ``count`` times.

        Args:
            count: Arrivals to wait for. Defaults to one.
            timeout: Maximum wait. Defaults to 90 seconds.

        Raises:
            TimeoutError: If the gate was not reached often enough in time.
        """

        async def has_reached() -> bool:
            return self.reached_count() >= count

        await wait_until(
            f"gate {self._reached_path.stem} reached {count} time(s)", has_reached, timeout=timeout
        )


class InvocationLog:
    """Count how often named operations ran, across Worker processes.

    A tool in the Worker child records each real run. The test then asserts
    that a write ran exactly once, even across a Worker kill.

    Example:
        >>> InvocationLog(state_dir / "invocations.log").record("send_email")
        >>> assert InvocationLog(state_dir / "invocations.log").count("send_email") == 1
    """

    def __init__(self, path: Path) -> None:
        """Use one append-only file shared by the test and its Workers.

        Args:
            path: Log file, created on the first record.
        """
        self._path = path

    def record(self, name: str) -> None:
        """Append one run of ``name``; it survives a SIGKILL right after.

        Args:
            name: Operation name without line breaks.
        """
        self._path.parent.mkdir(parents=True, exist_ok=True)
        with self._path.open("a") as log:
            log.write(f"{name}\n")

    def count(self, name: str) -> int:
        """Return how many runs of ``name`` were recorded.

        Args:
            name: Operation name.

        Returns:
            The number of matching records.
        """
        return self.names().count(name)

    def names(self) -> list[str]:
        """Return every recorded name in order.

        Returns:
            The records, oldest first.
        """
        if not self._path.exists():
            return []
        return self._path.read_text().splitlines()
