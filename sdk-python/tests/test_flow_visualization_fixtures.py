# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import importlib.util
import json
from pathlib import Path
from types import ModuleType
from typing import Any, cast

from dex import Registry
from dex._server_protocol import NegotiatedServerProtocol
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb

# The dexcli visualizer golden for this fixture must match what the SDK routes at runtime.
FIXTURE_DIRECTORY = (
    Path(__file__).resolve().parents[2] / "cli/internal/flowviz/testdata/python"
)
FIXTURE_INPUTS = ("instance", "reference", "movement", "complete")


class PassthroughHydrator:
    @staticmethod
    def execute_request(
        request: pb.InvokeExecuteMethodRequest,
    ) -> pb.InvokeExecuteMethodRequest:
        return request


def test_step_instances_visualization_matches_runtime_routing() -> None:
    fixture = load_fixture("step_instances")
    flow = fixture.StepInstancesFlow()
    registry = Registry((flow,))
    registered = registry._registered_flows[flow.get_flow_type()]
    values = ValueMapper(registry.codec_registry)
    dispatcher = WorkerDispatcher(
        registry, values, cast(Any, PassthroughHydrator()), NegotiatedServerProtocol()
    )
    routes: dict[str, set[tuple[str, str]]] = {}
    for step_type, step in registered.steps.items():
        step_routes = routes.setdefault(step_type, set())
        options = step.step.get_step_options()
        if options is not None and options._execute_failure_target is not None:
            failure_target = registered.step_target(options._execute_failure_target)
            step_routes.add(("failure_transition", failure_target.name))
        for step_input in FIXTURE_INPUTS:
            outputs = list(
                dispatcher.invoke_execute(
                    pb.InvokeExecuteMethodRequest(
                        flow_type=flow.get_flow_type(),
                        step_type=step_type,
                        step_input=values.encode(step_input, step.input_codec),
                    )
                )
            )
            decision = outputs[-1].result.step_decision
            step_routes.update(
                ("transition", movement.step_type) for movement in decision.next_steps
            )
            step_routes.update(
                ("cancel", cancel_type) for cancel_type in decision.cancel_step_types
            )
            step_routes.update(
                ("cancel", cancel_type)
                for cancel_type in decision.cancel_sibling_step_types
            )
    assert routes == golden_routes("step_instances")


def load_fixture(name: str) -> ModuleType:
    specification = importlib.util.spec_from_file_location(
        f"flow_visualization_{name}", FIXTURE_DIRECTORY / f"{name}.py"
    )
    assert specification is not None and specification.loader is not None
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


def golden_routes(name: str) -> dict[str, set[tuple[str, str]]]:
    graph = json.loads(
        (FIXTURE_DIRECTORY / f"{name}.golden.json").read_text(encoding="utf-8")
    )
    owners = {node["id"]: node.get("parentId", node["id"]) for node in graph["nodes"]}
    step_types = {
        node["id"]: node["name"] for node in graph["nodes"] if node["kind"] == "step"
    }
    routes: dict[str, set[tuple[str, str]]] = {
        step_type: set() for step_type in step_types.values()
    }
    for edge in graph["edges"]:
        if edge["kind"] not in {"transition", "cancel", "failure_transition"}:
            continue
        owner = step_types[owners[edge["from"]]]
        routes[owner].add((edge["kind"], step_types[edge["to"]]))
    return routes
