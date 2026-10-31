# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Integration-test utilities for agent framework adapters.

Every adapter package tests against a real Dex Server with these tools:

* ``AgentTestStack`` starts a private ``dexcli dev --engine zd`` engine on
  free ports, opens a Client, and starts Worker child processes.
* ``AgentWorkerProcess`` runs a Worker module in its own process group, so a
  test can SIGKILL it at an exact moment and start a replacement.
* ``run_agent_worker`` is the ``__main__`` body of that Worker module.
* ``FileGate`` pauses Worker code until the test kills or releases it, and
  ``InvocationLog`` counts real tool runs across Worker processes.
* ``wait_until`` and ``wait_for_agent_snapshot`` poll with a deadline.

The tools read ``DEXCLI`` for the ``dexcli`` executable and expect
``dex-server-zd`` in the same directory, or ``DEX_SERVER_ZD_BINARY``.
"""

from dex_agents.testing.coordination import FileGate, InvocationLog
from dex_agents.testing.polling import (
    DEFAULT_POLL_INTERVAL,
    DEFAULT_WAIT_TIMEOUT,
    wait_for_agent_snapshot,
    wait_until,
)
from dex_agents.testing.processes import (
    DEX_SERVER_ZD_ENVIRONMENT_VARIABLE,
    DEXCLI_ENVIRONMENT_VARIABLE,
    ENGINE_READY_LINE,
    AgentWorkerProcess,
    KillableProcess,
    PrivateDexEngine,
    find_free_port,
    resolve_dexcli_path,
    resolve_server_zd_path,
)
from dex_agents.testing.stack import AgentTestStack
from dex_agents.testing.worker import (
    BLOB_CACHE_DIR_ENVIRONMENT_VARIABLE,
    SERVER_ADDRESS_ENVIRONMENT_VARIABLE,
    WORKER_READY_LINE,
    run_agent_worker,
    serve_agent_worker,
)

__all__ = [
    "BLOB_CACHE_DIR_ENVIRONMENT_VARIABLE",
    "DEFAULT_POLL_INTERVAL",
    "DEFAULT_WAIT_TIMEOUT",
    "DEXCLI_ENVIRONMENT_VARIABLE",
    "DEX_SERVER_ZD_ENVIRONMENT_VARIABLE",
    "ENGINE_READY_LINE",
    "SERVER_ADDRESS_ENVIRONMENT_VARIABLE",
    "WORKER_READY_LINE",
    "AgentTestStack",
    "AgentWorkerProcess",
    "FileGate",
    "InvocationLog",
    "KillableProcess",
    "PrivateDexEngine",
    "find_free_port",
    "resolve_dexcli_path",
    "resolve_server_zd_path",
    "run_agent_worker",
    "serve_agent_worker",
    "wait_for_agent_snapshot",
    "wait_until",
]
