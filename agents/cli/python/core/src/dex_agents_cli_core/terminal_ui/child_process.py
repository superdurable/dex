# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A child process whose exit the event loop learns without ever blocking on a stopped child.

asyncio's threaded child watcher waits with ``waitid(WEXITED | WNOWAIT)`` and
then reaps on the event loop with a blocking ``waitpid``. macOS's ``waitid``
also returns for a child that only stopped (SIGSTOP, SIGTSTP), so the event
loop would block until that child exits. Here a thread waits until the child
really exited, and the event loop then reaps a zombie, which never blocks.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import subprocess
import threading
import time
from collections.abc import Sequence
from typing import Any

_STOPPED_POLL_SECONDS = 0.1


class ChildProcess:
    """One spawned child: its pid, signals while it is unreaped, and its exit status."""

    def __init__(self, popen: subprocess.Popen[bytes]) -> None:
        """Watch a child that ``spawn`` started."""
        self._popen = popen
        self._loop = asyncio.get_running_loop()
        self._exit: asyncio.Future[int] = self._loop.create_future()
        self._thread = threading.Thread(
            target=self._wait_for_exit, name=f"dex-agents-child-{popen.pid}", daemon=True
        )
        self._thread.start()

    @classmethod
    def spawn(cls, argv: Sequence[str], **popen_options: Any) -> ChildProcess:
        """Start a child with ``subprocess.Popen`` options, such as ``pass_fds``.

        Raises:
            OSError: If the program cannot start.
        """
        return cls(subprocess.Popen(list(argv), **popen_options))

    @property
    def pid(self) -> int:
        """Return the child's pid."""
        return self._popen.pid

    @property
    def returncode(self) -> int | None:
        """Return the exit status, negative for a signal, or ``None`` while it is unreaped."""
        return self._exit.result() if self._exit.done() else None

    async def wait(self) -> int:
        """Wait until the child exited, and return its status, negative for a signal."""
        return await asyncio.shield(self._exit)

    def send_signal(self, signal_number: int) -> None:
        """Send a signal to the child while it is unreaped, so its pid is still its own."""
        if not self._exit.done():
            with contextlib.suppress(ProcessLookupError):
                os.kill(self.pid, signal_number)

    def signal_group(self, signal_number: int) -> None:
        """Send a signal to the process group the child leads, while it is unreaped."""
        if not self._exit.done():
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.killpg(self.pid, signal_number)

    def _wait_for_exit(self) -> None:
        # macOS has waitid only from Python 3.13; before it, asyncio reaps the same way.
        waitid = getattr(os, "waitid", None)
        if waitid is None:
            self._reap_in_thread()
            return
        # Read here, not at import: Windows has none of these, and never runs this path.
        exit_codes = {os.CLD_EXITED, os.CLD_KILLED, os.CLD_DUMPED}
        while True:
            try:
                info = waitid(os.P_PID, self.pid, os.WEXITED | os.WNOWAIT)
            except ChildProcessError:
                break
            except InterruptedError:
                continue
            if info is None or info.si_code in exit_codes:
                break
            # macOS reports a stopped child here too; wait for the real exit.
            time.sleep(_STOPPED_POLL_SECONDS)
        with contextlib.suppress(RuntimeError):
            self._loop.call_soon_threadsafe(self._reap)

    def _reap_in_thread(self) -> None:
        returncode = self._wait_status()
        with contextlib.suppress(RuntimeError):
            self._loop.call_soon_threadsafe(self._set_exit, returncode)

    def _reap(self) -> None:
        self._set_exit(self._wait_status())

    def _wait_status(self) -> int:
        try:
            _, status = os.waitpid(self.pid, 0)
        except ChildProcessError:
            return 255
        return os.waitstatus_to_exitcode(status)

    def _set_exit(self, returncode: int) -> None:
        self._popen.returncode = returncode
        if not self._exit.done():
            self._exit.set_result(returncode)
