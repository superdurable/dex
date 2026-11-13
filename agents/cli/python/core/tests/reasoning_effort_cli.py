# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""``python -m tests.reasoning_effort_cli``: the scripted harness with efforts and suggested models.

``DEX_AGENTS_TEST_REASONING_EFFORTS`` replaces the declared efforts, and
``DEX_AGENTS_TEST_SUGGESTED_MODELS`` the suggested models, with their
comma-separated values, so a test can declare a bad one.
"""

import os

from dex_agents_cli_core import run_agent_cli
from tests.scripted_harness import ScriptedHarness

REASONING_EFFORTS_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_REASONING_EFFORTS"
#: Replaces the suggested models with its comma-separated values.
SUGGESTED_MODELS_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_SUGGESTED_MODELS"
#: The reasoning effort values the harness declares.
REASONING_EFFORTS = ("low", "medium", "high")
#: The suggested models; the default model among them is offered once.
SUGGESTED_MODELS = ("scripted-small", "scripted-model", "scripted-large")

if __name__ == "__main__":
    declared = os.environ.get(REASONING_EFFORTS_ENVIRONMENT_VARIABLE)
    suggested = os.environ.get(SUGGESTED_MODELS_ENVIRONMENT_VARIABLE)
    raise SystemExit(
        run_agent_cli(
            ScriptedHarness(
                supported_reasoning_efforts=REASONING_EFFORTS
                if declared is None
                else tuple(declared.split(",")),
                suggested_models=SUGGESTED_MODELS
                if suggested is None
                else tuple(suggested.split(",")),
            )
        )
    )
