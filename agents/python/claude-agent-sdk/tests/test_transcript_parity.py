# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Python and TypeScript adapters build the same transcript entries from the same inputs.

Mixed Workers of one Flow type must store the same entries, so a turn that
either adapter ends or resumes gives the Claude Code CLI the same session. The
fixture's expected entries are the TypeScript adapter's ``JSON.stringify``
output; Python's compact ``json.dumps`` of the same objects must match it byte
for byte, key order included.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, AgentImageReference
from dex_agents_claude_agent_sdk.transcript import (
    TranscriptToolUse,
    UnfinishedToolResult,
    build_resume_transcript,
    build_unfinished_tool_result_entries,
)

FIXTURE = json.loads(
    (
        Path(__file__).resolve().parents[3] / "conformance" / "claude_transcript_parity.json"
    ).read_text()
)


def _typescript_json(value: object) -> str:
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False)


@pytest.mark.parametrize("case", FIXTURE["unfinished"], ids=lambda case: case["name"])
def test_unfinished_tool_result_entries_match_the_typescript_adapter(case: dict[str, Any]) -> None:
    def describe(tool_use: TranscriptToolUse) -> UnfinishedToolResult:
        result = case["results"][tool_use.tool_use_id]
        return UnfinishedToolResult(
            result["text"],
            is_error=result["is_error"],
            images=[AgentImageReference(**image) for image in result.get("images", [])],
        )

    entries = build_unfinished_tool_result_entries(
        case["session_id"], case["turn_entries"], describe
    )

    assert _typescript_json(entries) == case["expected_entries"]


@pytest.mark.parametrize("case", FIXTURE["resume"], ids=lambda case: case["name"])
def test_resume_transcript_matches_the_typescript_adapter(case: dict[str, Any]) -> None:
    entries_before = _typescript_json(case["entries"])

    transcript = build_resume_transcript(
        case["session_id"], case["entries"], case["ended_turn_entries"], case["answer_text"]
    )

    assert _typescript_json(transcript.entries) == case["expected_entries"]
    assert _typescript_json(transcript.new_answer_entries) == case["expected_new_answer_entries"]
    # Placing answers never changes the stored entries.
    assert _typescript_json(case["entries"]) == entries_before


def test_the_fixture_uses_the_core_unknown_outcome_text() -> None:
    assert {case["answer_text"] for case in FIXTURE["resume"]} == {OUTCOME_UNKNOWN_TOOL_RESULT}
