# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The session Flow Definition Graph matches the session Flow and renders like every core."""

from __future__ import annotations

import hashlib
import json
import subprocess
import sys
from pathlib import Path
from typing import Any

import pytest

from dex_agents import render_session_flow_graph
from dex_agents.session_graph import main
from tests.test_conformance import SUITE_DIR, generate

SESSION_ACTIONS = ["resolve_interrupt", "answer_question", "steer_user_message", "cancel_turn"]


def read_suite_json(name: str) -> Any:
    return json.loads((SUITE_DIR / name).read_text(encoding="utf-8"))


def test_the_package_ships_the_suite_s_graph_byte_for_byte() -> None:
    shipped = Path(generate.SESSION_GRAPH_CORE_COPIES[0])
    assert shipped.read_bytes() == (SUITE_DIR / "session_graph.json").read_bytes()


def test_the_rendered_text_matches_every_core_s() -> None:
    for case in read_suite_json("session_graph_renderings.json")["cases"]:
        text = render_session_flow_graph(case["flow_type"], case["action_permission"])
        assert hashlib.sha256(text.encode()).hexdigest() == case["sha256"], case["flow_type"]


def test_the_graph_names_every_registered_step_and_rpc() -> None:
    graph = json.loads(render_session_flow_graph("SupportAgent", "support-agent.operate"))
    flow = generate.build_manifest_flow()
    nodes = graph["nodes"]
    assert [node["name"] for node in nodes if node["kind"] == "step"] == [
        definition.step.get_step_type() for definition in flow.get_steps()
    ]
    names = read_suite_json("names.json")
    assert sorted(node["name"] for node in nodes if node["kind"] == "rpc") == [
        rpc["name"] for rpc in names["rpcs"]
    ]
    assert graph["flow"] == {"name": "SupportAgent", "startStepId": "step:StartAgentSession"}
    actions = graph["v2"]["actions"]
    assert [action["rpcName"] for action in actions] == SESSION_ACTIONS
    assert all(action["requiredPermission"] == "support-agent.operate" for action in actions)
    assert all("condition" not in action for action in actions)


def test_a_waiting_step_names_the_rpcs_that_answer_it() -> None:
    graph = read_suite_json("session_graph.json")
    publishes = {
        (edge["from"], edge["to"]) for edge in graph["edges"] if edge["kind"] == "resource_publish"
    }
    waits = {
        (edge["from"], edge["to"]) for edge in graph["edges"] if edge["kind"] == "wait_condition"
    }
    answering = sorted(
        owner
        for owner, channel in publishes
        if owner.startswith("rpc:") and (channel, "wait:AwaitToolApproval") in waits
    )
    assert answering == ["rpc:answer_question", "rpc:resolve_interrupt"]


@pytest.mark.parametrize(
    ("flow_type", "action_permission"),
    [(" ", "agent.operate"), ("SupportAgent", "Agent.Operate"), ("SupportAgent", "a..b")],
)
def test_the_renderer_rejects_a_blank_flow_type_or_an_invalid_permission(
    flow_type: str, action_permission: str
) -> None:
    with pytest.raises(ValueError):
        render_session_flow_graph(flow_type, action_permission)


def test_the_command_writes_the_rendered_graph(tmp_path: Path) -> None:
    out = tmp_path / "support-agent.json"
    arguments = ["--flow-type", "SupportAgent", "--permission", "support-agent.operate"]
    assert main([*arguments, "--out", str(out)]) == 0
    expected = render_session_flow_graph("SupportAgent", "support-agent.operate")
    assert out.read_text(encoding="utf-8") == expected
    printed = subprocess.run(
        [sys.executable, "-m", "dex_agents.session_graph", *arguments],
        capture_output=True,
        check=True,
        text=True,
        encoding="utf-8",
    )
    assert printed.stdout == expected
    assert main(["--flow-type", "SupportAgent", "--permission", "Bad"]) == 2
