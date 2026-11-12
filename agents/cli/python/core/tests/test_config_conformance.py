# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The CLI core passes every case of agents/conformance/cli_config.json."""

from __future__ import annotations

import json
from importlib import resources
from pathlib import Path
from typing import Any

import pytest

from dex_agents_cli_core.config import (
    ConfigError,
    canonical_operation_name,
    is_shell_command_allowed,
    parse_config_yaml,
    resolve_config,
    resolve_turn_config,
)

AGENTS_DIR = Path(__file__).resolve().parents[4]
FIXTURE: dict[str, Any] = json.loads(
    (AGENTS_DIR / "conformance" / "cli_config.json").read_text(encoding="utf-8")
)


def case_id(case: dict[str, Any]) -> str:
    return str(case["name"])


@pytest.mark.parametrize("name", ["config.schema.json", "session-settings.schema.json"])
def test_packaged_schema_is_the_shared_schema(name: str) -> None:
    packaged = resources.files("dex_agents_cli_core.schemas").joinpath(name).read_bytes()
    assert packaged == (AGENTS_DIR / "cli" / name).read_bytes()


def untitled_anyof_pointers(schema: Any, pointer: str = "") -> list[str]:
    """Return the JSON Pointer of every ``anyOf`` schema without a ``title``."""
    found: list[str] = []
    if isinstance(schema, dict):
        if "anyOf" in schema and not isinstance(schema.get("title"), str):
            found.append(pointer)
        for key, value in schema.items():
            found.extend(untitled_anyof_pointers(value, f"{pointer}/{key}"))
    elif isinstance(schema, list):
        for index, value in enumerate(schema):
            found.extend(untitled_anyof_pointers(value, f"{pointer}/{index}"))
    return found


@pytest.mark.parametrize("name", ["config.schema.json", "session-settings.schema.json"])
def test_every_anyof_names_its_valid_forms_in_its_title(name: str) -> None:
    schema = json.loads((AGENTS_DIR / "cli" / name).read_text(encoding="utf-8"))
    assert untitled_anyof_pointers(schema) == []


def test_an_anyof_error_carries_the_failed_schemas_title() -> None:
    with pytest.raises(ConfigError) as raised:
        resolve_config({"media": {"store": "/tmp/media"}}, "scripted", None)
    assert raised.value.valid_forms is not None
    assert str(raised.value) == (
        f'schema at "/media/store" (schema keyword anyOf). Use {raised.value.valid_forms}.'
    )
    assert raised.value.valid_forms.startswith('"auto" (a directory on this machine)')


@pytest.mark.parametrize("case", FIXTURE["yaml_cases"], ids=case_id)
def test_yaml_case(case: dict[str, Any]) -> None:
    if "error" in case:
        with pytest.raises(ConfigError) as raised:
            parse_config_yaml(case["text"])
        assert {"code": raised.value.code, "path": raised.value.path} == case["error"]
    else:
        assert parse_config_yaml(case["text"]) == case["value"]


@pytest.mark.parametrize("case", FIXTURE["operation_names"], ids=case_id)
def test_operation_name(case: dict[str, Any]) -> None:
    assert canonical_operation_name(case["name"]) == case["canonical"]


@pytest.mark.parametrize("case", FIXTURE["shell_allow"], ids=lambda case: repr(case["command"]))
def test_shell_allow(case: dict[str, Any]) -> None:
    assert is_shell_command_allowed(case["command"], case["allow"]) is case["is_allowed"]


@pytest.mark.parametrize("case", FIXTURE["config_cases"], ids=case_id)
def test_config_case(case: dict[str, Any]) -> None:
    def resolve() -> dict[str, Any]:
        effective = resolve_config(
            case["config"], case["harness_section"], case["harness_default_model"]
        )
        return effective.to_json()

    if "effective" in case:
        assert resolve() == case["effective"]
    else:
        expect_error(resolve, case["error"])


@pytest.mark.parametrize("case", FIXTURE["session_cases"], ids=case_id)
def test_session_case(case: dict[str, Any]) -> None:
    harness = FIXTURE["session_harness"]
    effective = resolve_config(
        FIXTURE["session_configs"][case["config"]],
        harness["harness_section"],
        harness["harness_default_model"],
    )

    def resolve() -> tuple[dict[str, Any], list[str]]:
        resolution = resolve_turn_config(effective, case["session_settings"])
        return resolution.config.to_json(), list(resolution.warnings)

    if "turn" in case:
        assert resolve() == (case["turn"], case["warnings"])
    else:
        expect_error(resolve, case["error"])


def expect_error(resolve: Any, expected: dict[str, str]) -> None:
    with pytest.raises(ConfigError) as raised:
        resolve()
    assert raised.value.code == expected["code"]
    if expected["code"] == "schema":
        # Validators order errors differently; the expected one must be among them.
        assert (expected["path"], expected["keyword"]) in raised.value.schema_errors
    else:
        assert raised.value.path == expected["path"]
        assert raised.value.schema_errors == frozenset()
