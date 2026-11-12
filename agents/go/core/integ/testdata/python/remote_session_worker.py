# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Serve a built-in loop agent with every session feature, for the Go remote session tests.

The scripted model reads the current turn's last user text:

* ``say <text>`` answers ``said: <text>``.
* ``publish <message>`` calls ``publish_note``, a write that needs approval.
* ``ask <question> | <choice> ...`` calls ``ask_user`` with those choices and
  no free text.
* ``plan <task> | <task> ...`` drafts those tasks with ``write_todos``; send it
  with ``plan_mode``.
* ``stall <gate>`` waits at that ``FileGate``, then answers.
* ``describe`` answers ``saw <n> images``, counting the image bytes it got.
* The approved plan's message answers ``executed plan``.

After its tool results arrive, the model answers ``<tool>: <output>`` per
result, joined with ``; ``. At startup the Worker stores a PNG in its
``LocalDirectoryMediaStore`` and writes the reference, in snake_case JSON, to
``image-reference.json`` in the state directory.

Usage: remote_session_worker.py <Flow type>, with ``REMOTE_SESSION_STATE_DIR``
naming the state directory.
"""

from __future__ import annotations

import asyncio
import base64
import dataclasses
import json
import os
import sys
from pathlib import Path

from dex_agents import AgentSessionFlow, BuiltInTool, LocalDirectoryMediaStore, ToolEffect
from dex_agents.builtin_loop import (
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ModelRequest,
    builtin_agent_session_options,
    function_tool,
)
from dex_agents.testing import (
    FileGate,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    run_agent_worker,
)

STATE_DIR_ENVIRONMENT_VARIABLE = "REMOTE_SESSION_STATE_DIR"
APPROVED_PLAN_PREFIX = "[Dex] The user approved plan revision"
# A 1x1 PNG.
PNG_BYTES = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
)


def build_flow(
    flow_type: str, state_dir: Path, media_store: LocalDirectoryMediaStore
) -> AgentSessionFlow:
    @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
    def publish_note(message: str) -> str:
        """Publish a note."""
        return f"published {message}"

    async def respond(request: ModelRequest) -> ScriptedModelReply:
        command = ScriptedModelClient.user_texts(request)[-1]
        results = ScriptedModelClient.tool_results(request)
        verb, _, rest = command.partition(" ")
        if results:
            if verb == "plan":
                return ScriptedModelReply(text=f"drafted {len(_split(rest))} tasks")
            return ScriptedModelReply(
                text="; ".join(f"{result.tool_name}: {result.output}" for result in results)
            )
        if command.startswith(APPROVED_PLAN_PREFIX):
            return ScriptedModelReply(text="executed plan")
        if verb == "say":
            return ScriptedModelReply(text=f"said: {rest}")
        if verb == "publish":
            return ScriptedModelReply(
                tool_calls=[ScriptedToolCall(tool_name="publish_note", arguments={"message": rest})]
            )
        if verb == "ask":
            question, *choices = _split(rest)
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(
                        tool_name=BuiltInTool.ASK_USER,
                        arguments={
                            "question": question,
                            "choices": choices,
                            "allows_free_text": False,
                        },
                    )
                ]
            )
        if verb == "plan":
            todos = [{"content": task, "status": "pending"} for task in _split(rest)]
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(tool_name=BuiltInTool.WRITE_TODOS, arguments={"todos": todos})
                ]
            )
        if verb == "stall":
            gate = FileGate(state_dir, rest)
            gate.mark_reached()
            await gate.wait_for_release()
            return ScriptedModelReply(text=f"stalled at {rest}")
        if verb == "describe":
            return ScriptedModelReply(text=f"saw {len(request.image_bytes)} images")
        raise ValueError(f"unknown scripted command {command!r}")

    definition = AgentDefinition(
        name="remote-session-test",
        instructions="You are a scripted test agent.",
        model=ScriptedModelClient(respond),
        tools=[publish_note],
        built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.WRITE_TODOS}),
        plan_mode_enabled=True,
    )
    return AgentSessionFlow(
        BuiltinLoopAgentRunner(definition, media_store=media_store),
        flow_type=flow_type,
        options=builtin_agent_session_options(definition),
    )


def _split(text: str) -> list[str]:
    return [part.strip() for part in text.split("|") if part.strip()]


def main() -> None:
    flow_type = sys.argv[1]
    state_dir = Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE])
    media_store = LocalDirectoryMediaStore(state_dir / "media")
    reference = asyncio.run(media_store.put_image(PNG_BYTES))
    (state_dir / "image-reference.json").write_text(json.dumps(dataclasses.asdict(reference)))
    run_agent_worker([build_flow(flow_type, state_dir, media_store)])


if __name__ == "__main__":
    main()
