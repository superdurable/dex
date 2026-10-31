# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Worker child process that serves the scripted ADK agent."""

from dex_agents.testing import run_agent_worker
from tests.scripted_adk_agent import build_scripted_adk_flow, build_scripted_adk_pipeline_flow
from tests.scripted_adk_topologies import build_topology_flows

if __name__ == "__main__":
    run_agent_worker(
        [build_scripted_adk_flow(), build_scripted_adk_pipeline_flow(), *build_topology_flows()]
    )
