# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A test command whose harness writes to its standard output and error, as libraries do.

It writes at import time, and when it builds the Flow it also starts a child
that inherits both streams, so a test can check what reaches the terminal.
"""

import os
import subprocess

from dex_agents import AgentSessionFlow
from dex_agents_cli_core import HarnessContext, run_agent_cli
from tests.scripted_harness import ScriptedHarness

IMPORT_NOISE = "IMPORT-NOISE"
BUILD_NOISE = "BUILD-NOISE"
GRANDCHILD_NOISE = "GRANDCHILD-NOISE"
#: The line the inheriting child writes for each descriptor from 3 to 40 that it holds.
OPEN_DESCRIPTOR = "GRANDCHILD-FD-OPEN"
_LIST_DESCRIPTORS = (
    f"fd=3; while [ $fd -le 40 ]; do (: >&$fd) 2>/dev/null && echo {OPEN_DESCRIPTOR} $fd; "
    "fd=$((fd + 1)); done"
)

os.write(2, f"{IMPORT_NOISE}\n".encode())


class NoisyHarness(ScriptedHarness):
    """The scripted harness, writing to fd 1 and 2 and from an inheriting child as it builds."""

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        os.write(1, f"{BUILD_NOISE} on stdout\n".encode())
        os.write(2, f"{BUILD_NOISE} on stderr\n".encode())
        # close_fds=False, as some libraries spawn: the child keeps every inheritable descriptor.
        subprocess.run(
            [
                "/bin/sh",
                "-c",
                f"echo {GRANDCHILD_NOISE}; echo {GRANDCHILD_NOISE} >&2; {_LIST_DESCRIPTORS}",
            ],
            check=True,
            close_fds=False,
        )
        return super().create_session_flow(context)


if __name__ == "__main__":
    raise SystemExit(run_agent_cli(NoisyHarness()))
