# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Python core passes the cross-language conformance suite in agents/conformance."""

from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType
from typing import Any

import pytest

from dex_agents import (
    DEFAULT_MAX_DURABLE_WAIT,
    MAX_SESSION_SETTINGS_BYTES,
    AgentSessionFlow,
    BuiltInTool,
    ListAgentMessagesRequest,
    ToolCallStatus,
    build_plan_execution_message,
)
from dex_agents.session_flow import ALL_PENDING_MESSAGES, MESSAGE_PAGE_LIMIT, SNAPSHOT_MESSAGE_LIMIT
from dex_agents.session_settings import (
    SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
    check_stored_session_settings,
    session_settings_size,
    validate_session_settings,
)
from dex_agents.stepwise import DEFAULT_MAX_MODEL_CALLS_PER_TURN
from dex_agents.stepwise.runner import _read_tool_source_envelope, _write_tool_source_envelope

SUITE_DIR = Path(__file__).resolve().parents[3] / "conformance"


def load_generator() -> ModuleType:
    spec = importlib.util.spec_from_file_location("conformance_generate", SUITE_DIR / "generate.py")
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


generate = load_generator()


def value_documents() -> list[dict[str, Any]]:
    return [
        json.loads(path.read_text(encoding="utf-8"))
        for path in sorted((SUITE_DIR / "values").glob("*.json"))
    ]


def value_cases() -> list[tuple[str, dict[str, str]]]:
    return [
        (document["type"], case) for document in value_documents() for case in document["cases"]
    ]


def test_the_suite_files_match_the_python_core() -> None:
    assert (
        generate.stale_files(generate.build_files()) == []
    ), "run: cd agents/python/core && uv run --frozen python ../../conformance/generate.py"


@pytest.mark.parametrize(
    ("type_name", "case"),
    value_cases(),
    ids=[f"{type_name}/{case['name']}" for type_name, case in value_cases()],
)
def test_every_golden_value_reencodes_to_its_canonical_form(
    type_name: str, case: dict[str, str]
) -> None:
    assert generate.wire_kind(type_name) == next(
        document["wire_kind"] for document in value_documents() if document["type"] == type_name
    )
    assert generate.reencode(type_name, case["wire"]) == case["canonical"]
    assert generate.reencode(type_name, case["canonical"]) == case["canonical"]


def test_every_named_type_and_event_type_has_golden_values() -> None:
    names = json.loads((SUITE_DIR / "names.json").read_text(encoding="utf-8"))
    covered = {document["type"] for document in value_documents()}
    used = {entry["value_type"] for entry in names["persistence"]}
    used |= {entry["input"] for entry in names["step_types"]}
    used |= {entry["input"] for entry in names["rpcs"] if entry["input"] is not None}
    used |= {entry["output"] for entry in names["rpcs"]}
    assert used - covered == set()
    event_cases = {
        case["name"]
        for document in value_documents()
        if document["type"] == "AgentEvent"
        for case in document["cases"]
    }
    assert {f"type-{event_type}" for event_type in names["event_types"]} <= event_cases


def test_the_names_manifest_matches_the_registered_session_flow() -> None:
    names = json.loads((SUITE_DIR / "names.json").read_text(encoding="utf-8"))
    flow = generate.build_manifest_flow()
    assert isinstance(flow, AgentSessionFlow)
    assert [step["name"] for step in names["step_types"]] == [
        definition.step.get_step_type() for definition in flow.get_steps()
    ]
    assert names["enums"]["BuiltInTool"] == [str(tool) for tool in BuiltInTool]
    assert flow.agent_events.name == "AgentEvents"


def test_the_text_table_matches_the_python_core() -> None:
    texts = json.loads((SUITE_DIR / "texts.json").read_text(encoding="utf-8"))
    assert texts == json.loads(generate.render_json(generate.build_texts()))
    [case, *_] = texts["templates"]["plan_execution_message"]
    assert build_plan_execution_message(case["input"]["revision"]) == case["text"]


def test_the_python_core_reads_every_core_s_tool_source_envelope() -> None:
    names = json.loads((SUITE_DIR / "names.json").read_text(encoding="utf-8"))
    for case in names["stepwise_tool_source_reference"]["cases"]:
        for written in case["written"]:
            assert _read_tool_source_envelope(written) == (case["source_name"], case["reference"])
        own = _write_tool_source_envelope(case["source_name"], case["reference"])
        assert generate.canonicalize_json(json.loads(own)) == case["canonical"]


def test_session_settings_sizes_and_the_limit_match_the_python_core() -> None:
    sizes = json.loads((SUITE_DIR / "names.json").read_text(encoding="utf-8"))[
        "session_settings_sizes"
    ]
    for example in sizes["examples"]:
        expected = example.get("bytes_by_core", {}).get("python", example["bytes"])
        assert session_settings_size(example["settings"]) == expected, example["name"]
    for case in sizes["limit"]:
        settings = {"note": "x" * case["note_length"]}
        assert session_settings_size(settings) == case["bytes"]
        for check, is_accepted in (
            (validate_session_settings, case["is_accepted"]),
            (check_stored_session_settings, case["is_accepted_by_session"]),
        ):
            if is_accepted:
                check(settings)
            else:
                with pytest.raises(ValueError):
                    check(settings)


def test_limits_and_final_statuses_match_the_python_constants() -> None:
    names = json.loads((SUITE_DIR / "names.json").read_text(encoding="utf-8"))
    assert names["limits"] == {
        "snapshot_message_limit": SNAPSHOT_MESSAGE_LIMIT,
        "list_agent_messages_default_limit": ListAgentMessagesRequest().limit,
        "list_agent_messages_maximum_limit": MESSAGE_PAGE_LIMIT,
        "default_max_durable_wait_seconds": int(DEFAULT_MAX_DURABLE_WAIT.total_seconds()),
        "turn_control_at_most": ALL_PENDING_MESSAGES,
        "stepwise_default_max_model_calls_per_turn": DEFAULT_MAX_MODEL_CALLS_PER_TURN,
        "max_session_settings_bytes": MAX_SESSION_SETTINGS_BYTES,
        "session_settings_size_tolerance_bytes": SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
    }
    assert names["final_tool_call_statuses"] == [
        str(status) for status in ToolCallStatus if status.is_final
    ]
