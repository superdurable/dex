# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The test command: ``python -m tests.scripted_cli`` runs the CLI core with the scripted harness.

``DEX_AGENTS_TEST_WITHOUT_IMAGES=1`` runs a harness whose runner takes no images.
``DEX_AGENTS_TEST_FORCED_SUMMARY=on`` or ``off`` runs a harness that ignores
``HarnessContext.is_session_summary_indexed``.
"""

import os

from dex_agents_cli_core import run_agent_cli
from tests.scripted_harness import (
    FORCED_SUMMARY_ENVIRONMENT_VARIABLE,
    WITHOUT_IMAGES_ENVIRONMENT_VARIABLE,
    ScriptedHarness,
)

if __name__ == "__main__":
    supports_images = os.environ.get(WITHOUT_IMAGES_ENVIRONMENT_VARIABLE) != "1"
    forced = os.environ.get(FORCED_SUMMARY_ENVIRONMENT_VARIABLE)
    raise SystemExit(
        run_agent_cli(
            ScriptedHarness(
                supports_images=supports_images,
                forced_summary_indexed=None if forced is None else forced == "on",
            )
        )
    )
