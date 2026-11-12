# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Output canary for a Python command, which its launcher and Worker host load via ``PYTHONPATH``.

Once the scenario creates the trigger file, each process writes a canary line
to fds 1 and 2 and runs a child that does the same with inherited output, as a
harness library or a tool's subprocess can. None of it may reach the terminal.
"""

import os
import subprocess
import sys
import threading
import time

CANARY_DIRECTORY_VARIABLE = "DEX_TUI_E2E_CANARY_DIR"
CANARY_TEXT = "TUIE2ECANARY"


def _write_canaries(directory: str) -> None:
    role = "worker-host" if "__worker-host" in sys.argv else "launcher"
    trigger = os.path.join(directory, "trigger")
    while not os.path.exists(trigger):
        time.sleep(0.1)
    for fd in (1, 2):
        os.write(fd, f"{CANARY_TEXT} {role} fd {fd}\n".encode())
    subprocess.run(
        [
            "/bin/sh",
            "-c",
            f"echo {CANARY_TEXT} {role} child fd 1; echo {CANARY_TEXT} {role} child fd 2 >&2",
        ],
        check=False,
    )
    with open(os.path.join(directory, f"{role}-{os.getpid()}.written"), "w") as record:
        record.write(role)


_canary_directory = os.environ.get(CANARY_DIRECTORY_VARIABLE)
if _canary_directory:
    threading.Thread(target=_write_canaries, args=(_canary_directory,), daemon=True).start()
