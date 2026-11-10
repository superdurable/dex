# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The CLI configuration schemas, examples, and conformance cases agree with each other."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
from jsonschema import Draft202012Validator

from cli_config_reference import (
    CLI_DIR,
    CONFIG_SCHEMA,
    CONFIG_VALIDATOR,
    SESSION_SETTINGS_SCHEMA,
    SESSION_SETTINGS_VALIDATOR,
    ConfigError,
    canonical_operation_name,
    is_shell_command_allowed,
    json_pointer,
    parse_config_yaml,
    resolve_config,
    resolve_turn,
    schema_errors,
)

FIXTURE: dict[str, Any] = json.loads(
    (CLI_DIR.parent / "conformance" / "cli_config.json").read_text(encoding="utf-8")
)
EXAMPLES = sorted((CLI_DIR / "examples").glob("*.yaml"))


def case_id(case: dict[str, Any]) -> str:
    return str(case["name"])


def test_schemas_are_valid_draft_2020_12() -> None:
    Draft202012Validator.check_schema(CONFIG_SCHEMA)
    Draft202012Validator.check_schema(SESSION_SETTINGS_SCHEMA)


def test_shared_definitions_are_identical() -> None:
    for name, definition in SESSION_SETTINGS_SCHEMA["$defs"].items():
        assert CONFIG_SCHEMA["$defs"][name] == definition, name


def test_harness_sections_match_the_schema() -> None:
    sections = FIXTURE["harness_sections"]
    for section in sections:
        assert CONFIG_SCHEMA["properties"][section] == {
            "$ref": "#/$defs/harness_options",
            "description": CONFIG_SCHEMA["properties"][section]["description"],
        }
    common = {"model", "system_prompt", "system_prompt_file", "dex", "connectors"}
    common |= {"approval", "workspace", "local_tools"}
    assert set(CONFIG_SCHEMA["properties"]) == common | set(sections)


def test_every_property_is_described_and_every_config_default_is_stated() -> None:
    def walk(schema: dict[str, Any], path: str, is_config: bool) -> None:
        for name, child in schema.get("properties", {}).items():
            child_path = f"{path}/{name}"
            assert "description" in child, child_path
            walk(child, child_path, is_config)
        if is_config and path in {"/dex", "/connectors", "/approval", "/local_tools"}:
            assert "default" in schema, path

    walk(CONFIG_SCHEMA, "", True)
    walk(SESSION_SETTINGS_SCHEMA, "", False)
    assert CONFIG_SCHEMA["properties"]["workspace"]["default"] == "~/dex-agent-workspace"
    assert CONFIG_SCHEMA["properties"]["dex"]["properties"]["server"]["default"] == "auto"
    assert CONFIG_SCHEMA["properties"]["connectors"]["properties"]["mcp"]["default"] == "auto"


def test_schema_defaults_are_the_empty_file_defaults() -> None:
    effective = resolve_config({}, "claude", "m")
    properties = CONFIG_SCHEMA["properties"]
    assert properties["dex"]["default"] == effective["dex"]
    assert properties["approval"]["default"] == effective["approval"]
    assert properties["local_tools"]["default"] == effective["local_tools"]
    assert properties["connectors"]["default"] == {"mcp": effective["connectors"]["mcp"]}
    local_tools = properties["local_tools"]["properties"]
    for switch in ("files", "web_fetch", "browser"):
        assert local_tools[switch]["default"] == effective["local_tools"][switch]
    assert local_tools["shell"]["default"] == effective["local_tools"]["shell"]


@pytest.mark.parametrize("example", EXAMPLES, ids=lambda path: path.name)
def test_example_is_a_valid_configuration(example: Path) -> None:
    config = parse_config_yaml(example.read_text(encoding="utf-8"))
    assert list(CONFIG_VALIDATOR.iter_errors(config)) == []
    resolve_config(config, "claude", None)


def test_there_is_an_example() -> None:
    assert EXAMPLES


@pytest.mark.parametrize("case", FIXTURE["yaml_cases"], ids=case_id)
def test_yaml_case(case: dict[str, Any]) -> None:
    if "error" in case:
        with pytest.raises(ConfigError) as raised:
            parse_config_yaml(case["text"])
        assert {"code": raised.value.code, "path": raised.value.path} == case["error"]
    else:
        assert parse_config_yaml(case["text"]) == case["value"]


@pytest.mark.parametrize("case", FIXTURE["operation_names"], ids=lambda case: case["name"])
def test_operation_name(case: dict[str, Any]) -> None:
    assert canonical_operation_name(case["name"]) == case["canonical"]


@pytest.mark.parametrize("case", FIXTURE["shell_allow"], ids=lambda case: repr(case["command"]))
def test_shell_allow(case: dict[str, Any]) -> None:
    assert is_shell_command_allowed(case["command"], case["allow"]) is case["is_allowed"]


@pytest.mark.parametrize("case", FIXTURE["config_cases"], ids=case_id)
def test_config_case(case: dict[str, Any]) -> None:
    config = case["config"]
    if "effective" in case:
        assert list(CONFIG_VALIDATOR.iter_errors({} if config is None else config)) == []
        effective = resolve_config(config, case["harness_section"], case["harness_default_model"])
        assert effective == case["effective"]
        return
    expect_error(
        lambda: resolve_config(config, case["harness_section"], case["harness_default_model"]),
        case["error"],
        CONFIG_VALIDATOR,
        config,
    )


@pytest.mark.parametrize("case", FIXTURE["session_cases"], ids=case_id)
def test_session_case(case: dict[str, Any]) -> None:
    harness = FIXTURE["session_harness"]
    config = FIXTURE["session_configs"][case["config"]]
    effective = resolve_config(config, harness["harness_section"], harness["harness_default_model"])
    settings = case["session_settings"]
    if "turn" in case:
        if settings is not None:
            assert list(SESSION_SETTINGS_VALIDATOR.iter_errors(settings)) == []
        turn, warnings = resolve_turn(effective, settings)
        assert turn == case["turn"]
        assert warnings == case["warnings"]
        return
    expect_error(
        lambda: resolve_turn(effective, settings),
        case["error"],
        SESSION_SETTINGS_VALIDATOR,
        settings,
    )


def expect_error(
    resolve: Any, expected: dict[str, str], validator: Draft202012Validator, instance: object
) -> None:
    errors = schema_errors(validator, instance)
    if expected["code"] == "schema":
        # Exactly one violation: one top-level error, and every nested error lies inside it.
        top_level_errors = [
            (json_pointer(error), error.validator) for error in validator.iter_errors(instance)
        ]
        assert top_level_errors == [(expected["path"], expected["keyword"])], top_level_errors
        assert all(is_nested_in(path, expected["path"]) for path, _ in errors), errors
    else:
        assert errors == set(), "a non-schema error case must pass the schema"
    with pytest.raises(ConfigError) as raised:
        resolve()
    assert raised.value.code == expected["code"]
    assert raised.value.path == expected["path"]


def is_nested_in(path: str, parent: str) -> bool:
    return parent == "" or path == parent or path.startswith(parent + "/")
