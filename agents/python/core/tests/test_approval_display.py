# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The approval display rules of agents/conformance/approval_display.json.

The twin of the TypeScript core's test/integ/approval-display.integration.test.ts.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    MAX_APPROVAL_PREVIEW_BYTES,
    MAX_APPROVAL_SUMMARY_BYTES,
    MAX_APPROVAL_TARGET_BYTES,
    AgentInterruptDisplay,
    build_connector_tool_approval_display,
    is_credential_like_name,
    redact_inline_credentials,
)
from dex_agents.approval_display import bound_approval_display

CASES: dict[str, Any] = json.loads(
    (Path(__file__).resolve().parents[3] / "conformance" / "approval_display.json").read_text(
        encoding="utf-8"
    )
)


@pytest.mark.parametrize("name", CASES["credential_names"]["credential"])
def test_a_credential_name_is_found_as_the_conformance_suite_says(name: str) -> None:
    assert is_credential_like_name(name)


@pytest.mark.parametrize("name", CASES["credential_names"]["not_credential"])
def test_another_name_is_not_a_credential_name(name: str) -> None:
    assert not is_credential_like_name(name)


@pytest.mark.parametrize("case", CASES["inline_credentials"], ids=lambda case: case["text"][:40])
def test_inline_credentials_are_redacted_as_the_conformance_suite_says(
    case: dict[str, str],
) -> None:
    assert redact_inline_credentials(case["text"]) == case["redacted"]


@pytest.mark.parametrize(
    "case", CASES["connector_displays"], ids=lambda case: case["arguments"][:40]
)
def test_a_connector_display_has_the_conformance_suites_target_and_summary(
    case: dict[str, str],
) -> None:
    display = build_connector_tool_approval_display(
        connector_id=case["connector_id"],
        connection_name=case["connection_name"],
        operation=case["operation"],
        arguments=case["arguments"],
    )
    assert display == AgentInterruptDisplay(target=case["target"], summary=case["summary"])


def test_the_session_cuts_a_long_target_and_summary_and_drops_a_long_preview() -> None:
    bounded = bound_approval_display(
        AgentInterruptDisplay(
            target="é" * MAX_APPROVAL_TARGET_BYTES,
            summary="Runs a shell command in /work: " + "x" * (9 * 1024 * 1024),
            preview="p" * (MAX_APPROVAL_PREVIEW_BYTES + 1),
        )
    )
    assert bounded is not None
    assert bounded.preview is None
    assert bounded.target == "é" * ((MAX_APPROVAL_TARGET_BYTES - 3) // 2) + "…"
    assert bounded.summary.startswith("Runs a shell command in /work: xxx")
    assert bounded.summary.endswith("…")
    assert len(bounded.summary.encode("utf-8")) == MAX_APPROVAL_SUMMARY_BYTES
    assert bound_approval_display(AgentInterruptDisplay(target="t", summary="s")) == (
        AgentInterruptDisplay(target="t", summary="s")
    )
    assert bound_approval_display("not a display") is None
