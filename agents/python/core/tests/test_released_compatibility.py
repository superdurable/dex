# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Code and sessions from the released dex-agents 0.1.0 keep working on this version."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    AGENT_MESSAGE_CHUNKS,
    AGENT_MESSAGE_INDEX,
    AGENT_MESSAGES,
    AgentEventType,
    AgentMessageIndex,
    InterruptKind,
    message_sequence_key,
)
from dex_agents.testing import wait_for_agent_snapshot
from tests.conftest import ScriptedAgentHarness
from tests.public_api_shape import describe_public_api
from tests.scripted_agent import (
    BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE,
    MESSAGE_WINDOW_ENVIRONMENT_VARIABLE,
)

RELEASED_DEX_AGENTS = "dex-agents==0.1.0"
PUBLIC_MODULES = ["dex_agents", "dex_agents.testing"]
SHAPE_SCRIPT = Path(__file__).with_name("public_api_shape.py")


@pytest.fixture(scope="module")
def released_python(tmp_path_factory: pytest.TempPathFactory) -> str:
    """Return an interpreter with the released dex-agents from PyPI installed."""
    uv = shutil.which("uv")
    assert uv is not None, "uv must be on PATH to install the released dex-agents"
    venv = tmp_path_factory.mktemp("released-dex-agents") / ".venv"
    subprocess.run([uv, "venv", "--quiet", "--python", sys.executable, str(venv)], check=True)
    python = venv / "bin" / "python"
    subprocess.run(
        [uv, "pip", "install", "--quiet", "--python", str(python), RELEASED_DEX_AGENTS], check=True
    )
    return str(python)


def test_code_written_for_the_released_api_still_works(released_python: str) -> None:
    described = subprocess.run(
        [released_python, "-I", str(SHAPE_SCRIPT), *PUBLIC_MODULES],
        capture_output=True,
        check=True,
        text=True,
    )
    released = json.loads(described.stdout)
    assert len(released) >= 80

    assert public_api_breaks(released, describe_public_api(PUBLIC_MODULES)) == []


async def test_a_session_started_on_the_released_worker_continues_on_this_one(
    harness: ScriptedAgentHarness,
    released_python: str,
) -> None:
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "upgrade"}')
    interrupt = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="the approval raised by the released Worker",
    )
    raised = [
        json.loads(event.payload)
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.INTERRUPT_RAISED
    ]
    # The released Worker writes the 0.1.0 payload, without the question keys.
    assert [sorted(payload) for payload in raised] == [
        ["deadline", "interrupt_id", "tool_call_id", "tool_name"]
    ]
    assert (interrupt.kind, interrupt.question, interrupt.choices) == (
        InterruptKind.TOOL_APPROVAL,
        None,
        [],
    )

    released_worker.kill()
    await harness.stack.start_worker({BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE: "ask_user"})
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    first = await harness.wait_for_turns(session_id, 1)
    assert first.messages[-1].text == "publish: published upgrade"
    assert harness.invocations.count("publish") == 1

    # The next turn uses a feature the released version does not have.
    assert await harness.sessions.send_user_message(
        session_id, 'call ask_user {"question": "Ready for the next step?"}'
    )
    question = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(
            (
                pending
                for pending in snapshot.pending_interrupts
                if pending.kind is InterruptKind.QUESTION
            ),
            None,
        ),
        description="an ask_user question on the upgraded Worker",
    )
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "yes")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == 'ask_user: {"status":"answered","answer":"yes"}'
    assert "AwaitQuestionAnswer" in await harness.executed_step_types(session_id)


async def test_a_released_session_runs_without_settings_until_one_is_set(
    harness: ScriptedAgentHarness,
    released_python: str,
) -> None:
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "upgrade"}')
    interrupt = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="the approval raised by the released Worker",
    )
    released_worker.kill()
    await harness.stack.start_worker()
    assert (await harness.sessions.get_agent_snapshot(session_id)).session_settings is None

    # The update lands mid-turn: the released turn finishes without settings.
    assert await harness.sessions.update_session_settings(session_id, {"model": "alpha"})
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.send_user_message(session_id, "say upgraded")
    second = await harness.wait_for_turns(session_id, 2)

    assert second.messages[-1].text == "said: upgraded"
    assert second.session_settings == {"model": "alpha"}
    assert harness.session_settings_log.names() == [
        "turn-1:{}",
        "turn-1:{}",
        'turn-2:{"model": "alpha"}',
    ]


async def test_a_turn_a_released_worker_began_ignores_an_earlier_turns_settings(
    harness: ScriptedAgentHarness,
    released_python: str,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "say first", session_settings={"model": "alpha"}
    )
    await harness.wait_for_turns(session_id, 1)
    worker.kill()

    # The released Worker begins turn-2 without writing its settings copy.
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    assert await harness.sessions.send_user_message(
        session_id, 'call publish {"message": "rollout"}'
    )
    interrupt = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="the approval raised by the released Worker",
    )
    released_worker.kill()
    await harness.stack.start_worker()
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    await harness.wait_for_turns(session_id, 2)

    # turn-1's copy is stale, so turn-2 runs without settings on every Worker.
    assert harness.session_settings_log.names() == [
        'turn-1:{"model": "alpha"}',
        "turn-2:{}",
        "turn-2:{}",
    ]


async def test_a_session_the_released_worker_stored_moves_into_chunks_over_several_commits(
    harness: ScriptedAgentHarness,
    released_python: str,
) -> None:
    """The 0.1.0 layout, one AgentMessages instance per message, migrates four chunks a commit."""
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    session_id = harness.new_session_id()
    says = [f"say turn {turn}" for turn in range(1, 26)]
    await harness.sessions.start_session(session_id, says[0])
    for command in says[1:]:
        assert await harness.sessions.send_user_message(session_id, command)
    await harness.wait_for_turns(session_id, len(says))
    # The released Worker starts this turn and stores its user message, sequence 51.
    assert await harness.sessions.send_user_message(
        session_id, 'call publish {"message": "upgrade"}'
    )
    interrupt = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="the approval raised by the released Worker",
    )
    released = await _read_message_layout(harness, session_id, 52)
    assert released.index is None
    assert released.window_sequences == list(range(1, 52))

    released_worker.kill()
    await harness.stack.start_worker({MESSAGE_WINDOW_ENVIRONMENT_VARIABLE: "3,2"})
    # Before any write, this release reads the 0.1.0 layout as it is.
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    assert [message.sequence for message in snapshot.messages] == list(range(1, 52))
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    upgraded = await harness.wait_for_turns(session_id, 26)
    assert upgraded.messages[-1].text == "publish: published upgrade"
    assert harness.invocations.count("publish") == 1
    assert upgraded.state is not None
    next_sequence = upgraded.state.next_message_sequence

    # The commit that finished the turn moved only four chunks of two; the rest stays in the window.
    half_moved = await _read_message_layout(harness, session_id, next_sequence)
    assert half_moved.index is not None
    assert half_moved.index.window_first_sequence == 9
    assert sorted(half_moved.chunks) == [1, 3, 5, 7]
    assert half_moved.window_sequences == list(range(9, next_sequence))
    publish_turn = [message for message in upgraded.messages if message.turn_id == "turn-26"]

    # The next turns keep moving four chunks a commit, and the model input spans both layouts.
    assert await harness.sessions.send_user_message(session_id, "recall")
    recalled = await harness.wait_for_turns(session_id, 27)
    assert recalled.messages[-1].text == "history: " + " | ".join(
        [*says, 'call publish {"message": "upgrade"}']
    )
    turns = 27
    while True:
        assert recalled.state is not None
        layout = await _read_message_layout(
            harness, session_id, recalled.state.next_message_sequence
        )
        assert layout.index is not None
        if recalled.state.next_message_sequence - layout.index.window_first_sequence <= 3:
            break
        assert turns < 34, "the migration did not finish"
        turns += 1
        assert await harness.sessions.send_user_message(session_id, f"say more {turns}")
        recalled = await harness.wait_for_turns(session_id, turns)
    index = layout.index
    assert index.first_sequence == 1
    assert [chunk.first_sequence for chunk in index.chunks] == sorted(layout.chunks)
    assert layout.window_sequences == list(
        range(index.window_first_sequence, recalled.state.next_message_sequence)
    )
    assert [message.sequence for message in recalled.messages] == list(
        range(
            max(1, recalled.state.next_message_sequence - 200), recalled.state.next_message_sequence
        )
    )
    # The turn the released Worker started counts only what followed its user message.
    completed = [
        json.loads(event.payload)["message_count"]
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.TURN_COMPLETED
    ]
    assert completed[25] == len(publish_turn) - 1
    assert completed == [1] * 25 + [len(publish_turn) - 1] + [1] * (turns - 26)


@dataclass(frozen=True)
class MessageLayout:
    index: AgentMessageIndex | None
    window_sequences: list[int]
    chunks: list[int]


async def _read_message_layout(
    harness: ScriptedAgentHarness, session_id: str, next_sequence: int
) -> MessageLayout:
    """Read which sequences have an AgentMessages instance and which start a chunk."""
    sequences = range(1, next_sequence)
    window_reads = [AGENT_MESSAGES.instance(message_sequence_key(n)) for n in sequences]
    chunk_reads = [AGENT_MESSAGE_CHUNKS.instance(message_sequence_key(n)) for n in sequences]
    values = await harness.stack.client.get_attributes(
        session_id, AGENT_MESSAGE_INDEX, *window_reads, *chunk_reads
    )
    return MessageLayout(
        index=values.get(AGENT_MESSAGE_INDEX),
        window_sequences=[
            n for n, read in zip(sequences, window_reads, strict=False) if values.get(read)
        ],
        chunks=[n for n, read in zip(sequences, chunk_reads, strict=False) if values.get(read)],
    )


def public_api_breaks(released: dict[str, Any], current: dict[str, Any]) -> list[str]:
    """Return every way the current API breaks code written for the released one."""
    breaks: list[str] = []
    for name, shape in released.items():
        now = current.get(name)
        if now is None:
            breaks.append(f"{name} was removed")
        elif now["kind"] != shape["kind"]:
            breaks.append(f"{name} changed from a {shape['kind']} to a {now['kind']}")
        elif shape["kind"] == "enum":
            breaks += [
                f"{name}.{member} changed or was removed"
                for member, value in shape["members"].items()
                if now["members"].get(member) != value
            ]
        elif shape["kind"] == "function":
            breaks += _parameter_breaks(name, shape["parameters"], now["parameters"])
        elif shape["kind"] in ("class", "dataclass"):
            if shape["kind"] == "dataclass":
                breaks += _parameter_breaks(
                    f"{name}()", _field_parameters(shape), _field_parameters(now)
                )
            breaks += _method_breaks(name, shape["methods"], now["methods"])
    return breaks


def _method_breaks(name: str, released: dict[str, Any], current: dict[str, Any]) -> list[str]:
    breaks: list[str] = []
    for method, info in released.items():
        now = current.get(method)
        if now is None:
            breaks.append(f"{name}.{method} was removed")
        elif info["kind"] == "method" and now["kind"] == "method":
            breaks += _parameter_breaks(f"{name}.{method}", info["parameters"], now["parameters"])
    return breaks


def _field_parameters(shape: dict[str, Any]) -> list[dict[str, Any]]:
    return [
        {
            "name": field["name"],
            "kind": "KEYWORD_ONLY" if field["kw_only"] else "POSITIONAL_OR_KEYWORD",
            "has_default": field["has_default"],
        }
        for field in shape["fields"]
    ]


def _parameter_breaks(
    where: str, released: list[dict[str, Any]], current: list[dict[str, Any]]
) -> list[str]:
    variadic = ("VAR_POSITIONAL", "VAR_KEYWORD")
    positional = ("POSITIONAL_ONLY", "POSITIONAL_OR_KEYWORD")
    breaks: list[str] = []
    released_positional = [p["name"] for p in released if p["kind"] in positional]
    current_positional = [p["name"] for p in current if p["kind"] in positional]
    if current_positional[: len(released_positional)] != released_positional:
        breaks.append(f"{where} positional parameters changed: {current_positional}")
    current_by_name = {parameter["name"]: parameter for parameter in current}
    for parameter in released:
        now = current_by_name.get(parameter["name"])
        if now is None and parameter["kind"] not in variadic:
            breaks.append(f"{where} lost parameter {parameter['name']}")
        elif now is not None and parameter["has_default"] and not now["has_default"]:
            breaks.append(f"{where} parameter {parameter['name']} lost its default")
    released_names = {parameter["name"] for parameter in released}
    breaks += [
        f"{where} has a new required parameter {parameter['name']}"
        for parameter in current
        if parameter["name"] not in released_names
        and not parameter["has_default"]
        and parameter["kind"] not in variadic
    ]
    return breaks
