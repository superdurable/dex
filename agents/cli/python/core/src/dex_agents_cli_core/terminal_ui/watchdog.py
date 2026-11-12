# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Worker host's watchdog: notice a dead launcher, and bound every shutdown.

It runs on its own thread, outside the event loop. Once the launcher is gone,
or a shutdown starts, the Worker host has 15 seconds to exit in order; then
the watchdog stops the registered tool groups and kills its own process
group. The bound holds only while this thread can take the GIL: a main
thread stuck in native code that holds the GIL defeats it. On Linux the
kernel also kills the Worker host when the launcher dies (``PR_SET_PDEATHSIG``).
"""

from __future__ import annotations

import asyncio
import contextlib
import ctypes
import ctypes.util
import logging
import os
import signal
import sys
import threading
from collections.abc import Callable

from dex_agents_cli_core.terminal_ui.process_groups import ToolProcessGroupRegistry

#: Seconds between checks that the launcher still runs.
PARENT_POLL_SECONDS = 1.0
#: Seconds an orderly shutdown may take before the watchdog kills the process group.
HARD_EXIT_SECONDS = 15.0
_PR_SET_PDEATHSIG = 1
_LOGGER = logging.getLogger(__name__)


def set_parent_death_signal() -> None:
    """On Linux, have the kernel send SIGKILL to this process when its parent dies."""
    if not sys.platform.startswith("linux"):
        return
    libc = ctypes.CDLL(ctypes.util.find_library("c"), use_errno=True)
    if libc.prctl(_PR_SET_PDEATHSIG, signal.SIGKILL, 0, 0, 0) != 0:
        _LOGGER.warning("PR_SET_PDEATHSIG failed: errno %d", ctypes.get_errno())


class ParentDeathWatchdog:
    """Start an orderly shutdown when the launcher dies, and end any shutdown within a bound."""

    def __init__(
        self,
        launcher_pid: int,
        loop: asyncio.AbstractEventLoop,
        on_parent_death: Callable[[], None],
        process_groups: ToolProcessGroupRegistry,
        *,
        poll_seconds: float = PARENT_POLL_SECONDS,
        hard_exit_seconds: float = HARD_EXIT_SECONDS,
    ) -> None:
        """Watch ``launcher_pid``; call ``on_parent_death`` on ``loop`` when it is gone."""
        self._launcher_pid = launcher_pid
        self._loop = loop
        self._on_parent_death = on_parent_death
        self._process_groups = process_groups
        self._poll_seconds = poll_seconds
        self._hard_exit_seconds = hard_exit_seconds
        self._shutdown_started = threading.Event()
        self._finished = threading.Event()
        self._thread = threading.Thread(
            target=self._watch, name="dex-agents-worker-host-watchdog", daemon=True
        )

    def start(self) -> None:
        """Start watching."""
        self._thread.start()

    def begin_shutdown(self) -> None:
        """Start the hard bound of an orderly shutdown that began for another reason."""
        self._shutdown_started.set()

    def finish(self) -> None:
        """Report that the orderly shutdown ended, so the watchdog stops."""
        self._finished.set()
        self._shutdown_started.set()

    def _watch(self) -> None:
        while not self._shutdown_started.wait(self._poll_seconds):
            if os.getppid() != self._launcher_pid:
                _LOGGER.warning("the launcher (pid %d) is gone; stopping", self._launcher_pid)
                with contextlib.suppress(RuntimeError):
                    self._loop.call_soon_threadsafe(self._on_parent_death)
                break
        if self._finished.wait(self._hard_exit_seconds):
            return
        _LOGGER.error("the orderly shutdown took over %.0f s; killing", self._hard_exit_seconds)
        self._process_groups.stop_all()
        os.killpg(os.getpgrp(), signal.SIGKILL)
