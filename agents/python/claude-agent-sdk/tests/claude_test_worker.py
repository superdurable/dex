# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Worker child process that serves the Claude test agent."""

from dex_agents.testing import run_agent_worker
from tests.claude_test_agent import build_claude_test_agent_flow_from_environment

if __name__ == "__main__":
    run_agent_worker([build_claude_test_agent_flow_from_environment()])
