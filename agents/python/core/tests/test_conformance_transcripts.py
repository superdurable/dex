# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Python core plays the conformance suite's scripted turns as the transcripts expect."""

from __future__ import annotations

from pathlib import Path

from tests.conformance_agent import read_scenarios
from tests.conformance_transcripts import run_scenarios


async def test_every_scripted_turn_scenario_matches_its_expected_transcript(
    tmp_path: Path,
) -> None:
    transcripts = await run_scenarios(tmp_path)
    for scenario in read_scenarios():
        assert transcripts[scenario["name"]] == scenario["expected"], (
            f"{scenario['name']} differs; if the change is intended, regenerate with "
            "uv run --frozen python ../../conformance/generate.py --transcripts"
        )
