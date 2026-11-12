# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Wait until a child process exited, without reaping it, so its pid stays its own.

A caller that reaps the child itself afterwards, under the lock that guards
its signals, never signals a reused pid.
"""

from __future__ import annotations

import os
import select
import sys
import time

_STOPPED_POLL_SECONDS = 0.1


def wait_until_exited(pid: int) -> None:
    """Block until a child exited; a child that only stopped does not count. Never reaps it."""
    waitid = getattr(os, "waitid", None)
    if waitid is None:
        # macOS before Python 3.13 has no waitid; kqueue reports the exit without reaping.
        _wait_with_kqueue(pid)
        return
    exit_codes = {os.CLD_EXITED, os.CLD_KILLED, os.CLD_DUMPED}
    while True:
        try:
            info = waitid(os.P_PID, pid, os.WEXITED | os.WNOWAIT)
        except ChildProcessError:
            return
        except InterruptedError:
            continue
        if info is None or info.si_code in exit_codes:
            return
        # macOS reports a stopped child here too; wait for the real exit.
        time.sleep(_STOPPED_POLL_SECONDS)


def _wait_with_kqueue(pid: int) -> None:
    if sys.platform != "darwin":
        raise RuntimeError("only macOS lacks os.waitid")
    queue = select.kqueue()
    try:
        event = select.kevent(
            pid, filter=select.KQ_FILTER_PROC, flags=select.KQ_EV_ADD, fflags=select.KQ_NOTE_EXIT
        )
        try:
            queue.control([event], 1, None)
        except ProcessLookupError:
            return
    finally:
        queue.close()
