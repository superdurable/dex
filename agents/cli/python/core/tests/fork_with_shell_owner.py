# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fork while a shell tool call runs, and print how long the child took to exit.

``python -m tests.fork_with_shell_owner [--hold-lock]``: with ``--hold-lock``,
the fork happens while the owners' lock is held, as another thread might.
"""

import asyncio
import os
import sys
import tempfile
import threading
import time
from pathlib import Path

from dex_agents_cli_core.local_tools import shell


def main() -> None:
    is_lock_held = "--hold-lock" in sys.argv[1:]
    loop = asyncio.new_event_loop()
    threading.Thread(target=loop.run_forever, daemon=True).start()
    with tempfile.TemporaryDirectory() as workspace:
        call = asyncio.run_coroutine_threadsafe(
            shell.run_shell_command("sleep 5", Path(workspace), 30), loop
        )
        while not shell._OWNERS:
            time.sleep(0.01)
        if is_lock_held:
            shell._OWNERS_LOCK.acquire()
        child = os.fork()
        if child == 0:
            # The child exits through the interpreter, so the exit hook runs.
            sys.exit(0)
        if is_lock_held:
            shell._OWNERS_LOCK.release()
        started = time.monotonic()
        deadline = started + 10
        while time.monotonic() < deadline:
            finished, _ = os.waitpid(child, os.WNOHANG)
            if finished:
                break
            time.sleep(0.01)
        else:
            os.kill(child, 9)
            os.waitpid(child, 0)
        print(f"child exited after {time.monotonic() - started:.2f} s", flush=True)
        call.cancel()
        loop.call_soon_threadsafe(loop.stop)


if __name__ == "__main__":
    main()
