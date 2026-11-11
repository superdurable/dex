# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The test command: ``python -m tests.scripted_cli`` runs the runtime with the scripted harness.

``DEX_AGENTS_TEST_WITHOUT_IMAGES=1`` runs a harness whose runner takes no images.
"""

import os

from dex_agents_cli import run_agent_cli
from tests.scripted_harness import WITHOUT_IMAGES_ENVIRONMENT_VARIABLE, ScriptedHarness

if __name__ == "__main__":
    supports_images = os.environ.get(WITHOUT_IMAGES_ENVIRONMENT_VARIABLE) != "1"
    raise SystemExit(run_agent_cli(ScriptedHarness(supports_images=supports_images)))
