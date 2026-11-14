# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A test command whose Worker host takes 30 s to stop, and which marks its own exit steps.

In ``DEX_AGENTS_TEST_MARKER_DIRECTORY`` the Worker host creates
``worker-host-closing`` when its orderly stop begins, and the launcher creates
``dexcli-dev-stopped`` when the command's final cleanup stops its ``dexcli dev``.
"""

import asyncio
import os
import sys
from pathlib import Path
from typing import Any

from dex_agents_cli_core import cli, run_agent_cli
from dex_agents_cli_core.terminal_ui.worker_host import WORKER_HOST_COMMAND
from tests.scripted_harness import ScriptedHarness

#: The directory that receives the marker files.
MARKER_DIRECTORY_VARIABLE = "DEX_AGENTS_TEST_MARKER_DIRECTORY"
#: Created when the Worker host's orderly stop begins.
WORKER_HOST_CLOSING_MARKER = "worker-host-closing"
#: Created when the command's final cleanup stops its ``dexcli dev``.
DEXCLI_DEV_STOPPED_MARKER = "dexcli-dev-stopped"
_SLOW_CLOSE_SECONDS = 30


def _patch(markers: Path) -> None:
    if sys.argv[1:2] == [WORKER_HOST_COMMAND]:
        close = cli._HostedCliRun.close

        async def slow_close(self: Any) -> None:
            (markers / WORKER_HOST_CLOSING_MARKER).touch()
            await asyncio.sleep(_SLOW_CLOSE_SECONDS)
            await close(self)

        cli._HostedCliRun.close = slow_close  # type: ignore[method-assign]
        return
    stop = cli._CommandDexServer.stop_started_processes

    def marked_stop(self: Any) -> None:
        (markers / DEXCLI_DEV_STOPPED_MARKER).touch()
        stop(self)

    cli._CommandDexServer.stop_started_processes = marked_stop  # type: ignore[method-assign]


if __name__ == "__main__":
    _patch(Path(os.environ[MARKER_DIRECTORY_VARIABLE]))
    raise SystemExit(run_agent_cli(ScriptedHarness()))
