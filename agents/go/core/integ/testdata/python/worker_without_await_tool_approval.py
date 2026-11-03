# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Serve the Python core's Stepwise test agent, declining AwaitToolApproval tasks.

Dex routes a declined task to another Worker, so a Go Worker in the same fleet
runs every AwaitToolApproval of turns this Worker began.

Usage: worker_without_await_tool_approval.py <Python core directory>
"""

import sys

sys.path.insert(0, sys.argv[1])

from dex import _pull_worker  # noqa: E402

from dex_agents.testing import run_agent_worker  # noqa: E402
from tests.stepwise_agent import build_stepwise_agent_flow_from_environment  # noqa: E402

_unknown_task_reason = _pull_worker.PullWorkerRuntime.unknown_task_reason


def _decline_await_tool_approval(self, task):  # type: ignore[no-untyped-def]
    kind = task.WhichOneof("request")
    if kind in ("wait_for", "execute"):
        request = task.wait_for if kind == "wait_for" else task.execute
        if request.step_type == "AwaitToolApproval":
            return _pull_worker.pb.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP
    return _unknown_task_reason(self, task)


_pull_worker.PullWorkerRuntime.unknown_task_reason = _decline_await_tool_approval
run_agent_worker([build_stepwise_agent_flow_from_environment()])
