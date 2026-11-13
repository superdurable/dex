# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A test command whose Worker host closes its control channel but keeps running.

``DEX_AGENTS_TEST_CONTROL_CHANNEL_END`` picks when: ``before-ready`` closes it
at once, and ``after-ready`` right after ``worker_ready`` and the session lease.
"""

import argparse
import asyncio
import os
import sys
import time
from typing import Any

from dex_agents_cli_core import run_agent_cli
from dex_agents_cli_core.terminal_ui import worker_host
from tests.scripted_harness import ScriptedHarness

#: When the Worker host closes its control channel: ``before-ready`` or ``after-ready``.
CONTROL_CHANNEL_END_VARIABLE = "DEX_AGENTS_TEST_CONTROL_CHANNEL_END"
_STAY_SECONDS = 60


async def _close_and_stay(self: Any, channel: Any, hosted: Any) -> None:
    # The launcher takes the session's lease before the handoff; answer that first.
    message = await channel.receive()
    if message is not None and message.get("method") == worker_host.ACQUIRE_SESSION_LEASE:
        await self._answer(channel, hosted.request_handlers, message)
    await channel.close()
    await asyncio.sleep(_STAY_SECONDS)


if __name__ == "__main__":
    mode = os.environ.get(CONTROL_CHANNEL_END_VARIABLE)
    if sys.argv[1:2] == [worker_host.WORKER_HOST_COMMAND] and mode == "before-ready":
        parser = argparse.ArgumentParser()
        parser.add_argument("--control-fd", type=int)
        parser.add_argument("--launcher-pid", type=int)
        arguments, _ = parser.parse_known_args(sys.argv[2:])
        os.close(arguments.control_fd)
        time.sleep(_STAY_SECONDS)
        raise SystemExit(0)
    if mode == "after-ready":
        worker_host._WorkerHost._serve_requests = _close_and_stay  # type: ignore[method-assign]
    raise SystemExit(run_agent_cli(ScriptedHarness()))
