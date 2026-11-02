# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run the conformance suite's scripted-turn scenarios on the Python core.

``run_scenarios`` plays every scenario in ``agents/conformance/transcripts``
against a private Dex engine and returns each one's normalized transcript:
the completed Step methods, the snapshots, the ``AgentEvents`` messages, and
the model requests. ``write_expected_transcripts`` stores them as each
scenario's ``expected``.
"""

from __future__ import annotations

import asyncio
import json
import re
import tempfile
from pathlib import Path
from typing import Any

from dex import CodecRegistry

from dex_agents import AgentSessionClient, AgentSessionFlow, AgentSnapshot
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    WAIT_FOR_COMPLETED_EVENT,
    AgentTestStack,
    wait_for_agent_snapshot,
)
from tests.conformance_agent import (
    FLOW_TYPES,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    TRANSCRIPTS_DIR,
    build_conformance_flows,
    read_model_requests,
    read_scenarios,
)

CORE_DIR = Path(__file__).resolve().parents[1]
WORKER_MODULE = "tests.conformance_worker"
TIMESTAMP = re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z")
TIMESTAMP_PLACEHOLDER = "<timestamp>"
SETTLE_INTERVAL_SECONDS = 0.5
SCENARIO_TIMEOUT_SECONDS = 90

_SNAPSHOT_CODEC = CodecRegistry().resolve(AgentSnapshot)


async def write_expected_transcripts(transcripts_dir: Path = TRANSCRIPTS_DIR) -> None:
    """Rewrite every scenario's ``expected`` from a run on the Python core."""
    with tempfile.TemporaryDirectory(prefix="conformance-") as state:
        transcripts = await run_scenarios(Path(state))
    for path in sorted(transcripts_dir.glob("*.json")):
        scenario = json.loads(path.read_text(encoding="utf-8"))
        scenario["expected"] = transcripts[scenario["name"]]
        path.write_text(json.dumps(scenario, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


async def run_scenarios(state_dir: Path) -> dict[str, Any]:
    """Return the normalized transcript of every scenario, by scenario name."""
    agent_state_dir = state_dir / "agent-state"
    agent_state_dir.mkdir()
    flows = build_conformance_flows(agent_state_dir)
    flows_by_profile = {
        profile: next(flow for flow in flows if flow.get_flow_type() == flow_type)
        for profile, flow_type in FLOW_TYPES.items()
    }
    async with AgentTestStack(
        state_dir,
        flows,
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(agent_state_dir)},
    ) as stack:
        await stack.start_worker()
        transcripts = {}
        for scenario in read_scenarios():
            flow = flows_by_profile[scenario["agent"]]
            # A codec mismatch would otherwise wait out every poll; fail the scenario instead.
            transcripts[scenario["name"]] = await asyncio.wait_for(
                run_scenario(stack, flow, scenario, agent_state_dir),
                SCENARIO_TIMEOUT_SECONDS,
            )
        return transcripts


async def run_scenario(
    stack: AgentTestStack, flow: AgentSessionFlow, scenario: dict[str, Any], agent_state_dir: Path
) -> dict[str, Any]:
    sessions = stack.sessions(flow)
    session_id = f"conformance-{scenario['name']}"
    snapshots: dict[str, Any] = {}
    for action in scenario["actions"]:
        result = await run_action(sessions, session_id, action, snapshots)
        if "returns" in action:
            assert result == action["returns"], f"{scenario['name']}: {action} returned {result}"
    steps, events = await read_settled_history(stack, flow, session_id)
    snapshots["final"] = describe_snapshot(await sessions.get_agent_snapshot(session_id))
    transcript: dict[str, Any] = replace_timestamps(
        {
            "steps": steps,
            "events": events,
            "snapshots": snapshots,
            "model_requests": read_model_requests(agent_state_dir, session_id),
        }
    )
    return transcript


async def run_action(
    sessions: AgentSessionClient,
    session_id: str,
    action: dict[str, Any],
    snapshots: dict[str, Any],
) -> Any:
    kind = action["action"]
    if kind == "start_session":
        return await sessions.start_session(session_id)
    if kind == "send_user_message":
        return await sessions.send_user_message(
            session_id, action["text"], plan_mode=action.get("plan_mode", False)
        )
    if kind == "resolve_interrupt":
        return await sessions.resolve_interrupt(
            session_id,
            action["interrupt_id"],
            approved=action["approved"],
            rejection_message=action["rejection_message"],
        )
    if kind == "answer_question":
        return await sessions.answer_question(session_id, action["interrupt_id"], action["answer"])
    if kind == "steer_user_message":
        return await sessions.steer_user_message(session_id, text=action["text"])
    if kind == "execute_plan":
        return await sessions.execute_plan(session_id, action["revision"])
    if kind == "snapshot":
        snapshots[action["label"]] = describe_snapshot(
            await sessions.get_agent_snapshot(session_id)
        )
        return None
    if kind == "wait_for_idle":
        turn_count = action["turn_count"]
        return await wait_for_agent_snapshot(
            sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            and snapshot.queued_message_count == 0
            else None,
            description=f"{turn_count} finished turns",
        )
    if kind == "wait_for_interrupt":
        interrupt_id = action["interrupt_id"]
        return await wait_for_agent_snapshot(
            sessions,
            session_id,
            lambda snapshot: snapshot
            if any(pending.interrupt_id == interrupt_id for pending in snapshot.pending_interrupts)
            else None,
            description=f"pending interrupt {interrupt_id}",
        )
    raise ValueError(f"unknown scenario action {kind!r}")


async def read_settled_history(
    stack: AgentTestStack, flow: AgentSessionFlow, session_id: str
) -> tuple[dict[str, Any], list[Any]]:
    """Return the Step history and events once two reads half a second apart agree."""
    previous: tuple[dict[str, Any], list[Any]] | None = None
    while True:
        history = await stack.read_step_history(session_id)
        page = await stack.client.list_stream_messages(session_id, flow.agent_events, 1000)
        current = (
            describe_step_history(
                [
                    (
                        entry.event_type,
                        entry.step_type,
                        entry.step_execution_id,
                        entry.final_attempt,
                    )
                    for entry in history
                ]
            ),
            [
                {
                    "type": message.value.type,
                    "turn_id": message.value.turn_id,
                    "attempt": message.value.attempt,
                    "step_execution_id": message.value.step_execution_id,
                    "payload": canonicalize_json(json.loads(message.value.payload)),
                }
                for message in reversed(page.messages)
            ],
        )
        if current == previous:
            return current
        previous = current
        await asyncio.sleep(SETTLE_INTERVAL_SECONDS)


def describe_step_history(history: list[tuple[str, str, str, int]]) -> dict[str, Any]:
    """Return Execute completions in order, and WaitFor completions sorted.

    Parallel Steps finish their WaitFor in engine order, so only the set of them is stable.
    """
    return {
        "executed": [
            [step_type, step_execution_id, attempt]
            for event_type, step_type, step_execution_id, attempt in history
            if event_type == EXECUTE_COMPLETED_EVENT
        ],
        "waits_completed": sorted(
            [step_type, step_execution_id]
            for event_type, step_type, step_execution_id, _ in history
            if event_type == WAIT_FOR_COMPLETED_EVENT
        ),
    }


def canonicalize_json(value: Any) -> str:
    """Serialize parsed JSON in the suite's canonical form, as event payloads are compared."""
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def describe_snapshot(snapshot: AgentSnapshot) -> Any:
    return json.loads(str(_SNAPSHOT_CODEC.encode(snapshot).data))


def replace_timestamps(value: Any) -> Any:
    """Replace every RFC 3339 millisecond UTC timestamp; other formats stay and fail comparison."""
    if isinstance(value, str):
        return TIMESTAMP.sub(TIMESTAMP_PLACEHOLDER, value)
    if isinstance(value, list):
        return [replace_timestamps(item) for item in value]
    if isinstance(value, dict):
        return {key: replace_timestamps(item) for key, item in value.items()}
    return value
