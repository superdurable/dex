# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Sessions move between a Worker of the released adapter 0.6.11 and one of this version."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tomllib
from pathlib import Path

import pytest

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentMessageOrigin,
    MessageRole,
)
from tests.conftest import ClaudeAgentHarness
from tests.older_workers import roles_of
from tests.transcript_assertions import assert_unique_transcript_entries

RELEASED_VERSION = "0.6.11"
PACKAGE_DIR = Path(__file__).resolve().parents[1]


@pytest.fixture(scope="module")
def released_python(tmp_path_factory: pytest.TempPathFactory) -> str:
    """Return an interpreter with the released adapter from PyPI and the locked Claude SDK."""
    uv = shutil.which("uv")
    assert uv is not None, "uv must be on PATH to install the released adapter"
    lock = tomllib.loads((PACKAGE_DIR / "uv.lock").read_text())
    claude_sdk_version = next(
        package["version"] for package in lock["package"] if package["name"] == "claude-agent-sdk"
    )
    venv = tmp_path_factory.mktemp("released-claude-adapter") / ".venv"
    subprocess.run([uv, "venv", "--quiet", "--python", sys.executable, str(venv)], check=True)
    python = venv / "bin" / "python"
    subprocess.run(
        [
            uv,
            "pip",
            "install",
            "--quiet",
            "--python",
            str(python),
            f"dex-agents-claude-agent-sdk=={RELEASED_VERSION}",
            f"dex-agents[connectors]=={RELEASED_VERSION}",
            f"claude-agent-sdk=={claude_sdk_version}",
        ],
        check=True,
    )
    return str(python)


async def test_a_released_worker_run_state_resumes_here_and_its_cancel_is_healed(
    harness: ClaudeAgentHarness, released_python: str
) -> None:
    # The released Worker keeps the suspended turn's entries in the run state.
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "first"}')
    first = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert roles_of(waiting.messages) == [(MessageRole.USER, "turn-1", None)]
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    assert json.loads(values[FRAMEWORK_RUN_STATE])["turn_entries"]

    released_worker.kill()
    worker = await harness.stack.start_worker()
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)
    completed = await harness.wait_for_turns(session_id, 1)
    assert roles_of(completed.messages) == [
        (MessageRole.USER, "turn-1", None),
        (MessageRole.ASSISTANT, "turn-1", "publish"),
        (MessageRole.TOOL, "turn-1", "publish"),
        (MessageRole.ASSISTANT, "turn-1", None),
    ]
    assert completed.messages[-1].text == "publish: published first"
    assert_unique_transcript_entries(completed.messages)

    # This Worker stores the deferral; the released one cancels the turn with only a note.
    assert await harness.sessions.send_user_message(session_id, 'call publish {"message": "b"}')
    second = await harness.wait_for_pending_interrupt(session_id)
    worker.kill()
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    assert await harness.sessions.cancel_turn(session_id)
    cancelled = await harness.wait_for_turns(session_id, 2)
    released_worker.kill()
    assert cancelled.messages[-1].origin == AgentMessageOrigin.TURN_CANCELLED_NOTE
    assert roles_of(cancelled.messages[len(completed.messages) :]) == [
        (MessageRole.USER, "turn-2", None),
        (MessageRole.ASSISTANT, "turn-2", "publish"),
        (MessageRole.USER, "turn-2", None),
    ]

    # The next turn answers the open call instead of asking for its approval again.
    await harness.stack.start_worker()
    assert await harness.sessions.send_user_message(session_id, "say next")
    snapshot = await harness.wait_for_turns(session_id, 3)
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert not snapshot.pending_interrupts
    assert roles_of(snapshot.messages[len(cancelled.messages) :]) == [
        (MessageRole.USER, "turn-3", None),
        (MessageRole.TOOL, "turn-3", "publish"),
        (MessageRole.ASSISTANT, "turn-3", None),
    ]
    answer = snapshot.messages[len(cancelled.messages) + 1]
    assert (answer.tool_call_id, answer.tool_output) == (
        second.tool_call_id,
        OUTCOME_UNKNOWN_TOOL_RESULT,
    )
    assert snapshot.messages[-1].text == "said: next"
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.count("publish") == 1
