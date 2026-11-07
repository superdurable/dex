# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A harness is checked per segment, and its usage arrives when the segment ends."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from dex_agents import MODEL_CALL_REFUSED_ERROR_PREFIX
from tests.claude_test_agent import (
    SPEND_CHECKS_LOG_NAME,
    SPEND_LIMIT_ENVIRONMENT_VARIABLE,
    SPEND_REFUSAL_FILE_NAME,
    SPEND_REPORTS_LOG_NAME,
)
from tests.conftest import ClaudeAgentHarness


def _read_log(state_dir: Path, file_name: str) -> list[dict[str, Any]]:
    path = state_dir / file_name
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


async def test_one_segment_is_one_check_and_one_report_of_all_its_model_calls(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker({SPEND_LIMIT_ENVIRONMENT_VARIABLE: "1"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "x"}')
    await harness.wait_for_turns(session_id, 1)

    assert len(_read_log(harness.state_dir, SPEND_CHECKS_LOG_NAME)) == 1
    (report,) = _read_log(harness.state_dir, SPEND_REPORTS_LOG_NAME)
    assert report["usage"]["model_calls"] == 2
    assert report["usage"]["input_tokens"] > 0


async def test_a_refusal_before_the_next_segment_ends_the_turn_and_keeps_the_tool_result(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker({SPEND_LIMIT_ENVIRONMENT_VARIABLE: "1"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "hello"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    (harness.state_dir / SPEND_REFUSAL_FILE_NAME).write_text("out of budget")

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error == f"{MODEL_CALL_REFUSED_ERROR_PREFIX}: out of budget"
    assert harness.invocations.count("publish") == 1
    assert len(_read_log(harness.state_dir, SPEND_REPORTS_LOG_NAME)) == 1
