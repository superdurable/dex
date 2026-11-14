# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The CLI core passes every case of agents/conformance/cli_init.json."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from dex_agents_cli_core.config_file import merge_init_values

AGENTS_DIR = Path(__file__).resolve().parents[4]
FIXTURE: dict[str, Any] = json.loads(
    (AGENTS_DIR / "conformance" / "cli_init.json").read_text(encoding="utf-8")
)


@pytest.mark.parametrize("case", FIXTURE["merge_cases"], ids=lambda case: str(case["name"]))
def test_merge_case(case: dict[str, Any]) -> None:
    assert merge_init_values(case["existing"], case["init"]) == case["expected"]
