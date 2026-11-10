# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Test-only reference resolver for the Dex Agents CLI configuration.

It proves that ``agents/conformance/cli_config.json`` is consistent with the
rules in ``agents/cli/README.md``. The CLI runtimes implement the same rules
in their own code; they do not import this module.
"""

from __future__ import annotations

import copy
import json
from collections.abc import Iterable, Iterator, Mapping
from pathlib import Path
from typing import Any

from jsonschema import Draft202012Validator, ValidationError
from ruamel.yaml import YAML
from ruamel.yaml.error import YAMLError

CLI_DIR = Path(__file__).resolve().parents[1]
CONFIG_SCHEMA: dict[str, Any] = json.loads((CLI_DIR / "config.schema.json").read_text())
SESSION_SETTINGS_SCHEMA: dict[str, Any] = json.loads(
    (CLI_DIR / "session-settings.schema.json").read_text()
)
CONFIG_VALIDATOR = Draft202012Validator(CONFIG_SCHEMA)
SESSION_SETTINGS_VALIDATOR = Draft202012Validator(SESSION_SETTINGS_SCHEMA)

DEFAULT_DEX_SERVER = "auto"
DEFAULT_CONNECTORS_MCP = "auto"
DEFAULT_WORKSPACE = "~/dex-agent-workspace"


class ConfigError(Exception):
    """A rejected configuration or session override: a stable code and a JSON Pointer."""

    def __init__(self, code: str, path: str) -> None:
        super().__init__(f"{code} at {path!r}")
        self.code = code
        self.path = path


def parse_config_yaml(text: str) -> object:
    """Return the value of a configuration file's YAML 1.2 text.

    Raises:
        ConfigError: ``yaml`` at ``""`` for text that is not one YAML 1.2 document
            with unique keys.
    """
    yaml = YAML(typ="safe", pure=True)
    yaml.version = (1, 2)
    try:
        return yaml.load(text)
    except YAMLError as error:
        raise ConfigError("yaml", "") from error


def resolve_config(
    config: object, harness_section: str, harness_default_model: str | None
) -> dict[str, Any]:
    """Return the effective configuration of a parsed configuration file; an empty file is {}.

    Raises:
        ConfigError: ``schema``, ``duplicate_tool``, ``duplicate_connection``, or
            ``model_required``.
    """
    if config is None:
        config = {}
    raise_first_schema_error(CONFIG_VALIDATOR, config)
    assert isinstance(config, Mapping)
    connectors = config.get("connectors", {})
    enabled = connectors.get("enabled")
    entries = None if enabled is None else parse_connector_entries(enabled)
    model = config.get("model", harness_default_model)
    if model is None:
        raise ConfigError("model_required", "/model")
    local_tools = config.get("local_tools", {})
    shell = local_tools.get("shell", {})
    return {
        "model": model,
        "system_prompt": config.get("system_prompt"),
        "system_prompt_file": config.get("system_prompt_file"),
        "dex": {"server": config.get("dex", {}).get("server", DEFAULT_DEX_SERVER)},
        "connectors": {
            "mcp": connectors.get("mcp", DEFAULT_CONNECTORS_MCP),
            "enabled": entries,
        },
        "approval": {"always": list(config.get("approval", {}).get("always", []))},
        "workspace": config.get("workspace", DEFAULT_WORKSPACE),
        "local_tools": {
            "files": local_tools.get("files", True),
            "shell": {
                "enabled": shell.get("enabled", True),
                "allow": list(shell.get("allow", [])),
            },
            "web_fetch": local_tools.get("web_fetch", True),
            "browser": local_tools.get("browser", True),
        },
        "harness_options": copy.deepcopy(config.get(harness_section, {})),
    }


def resolve_turn(effective: Mapping[str, Any], session_settings: object) -> dict[str, Any]:
    """Return the configuration of one turn: an effective configuration with session settings.

    Raises:
        ConfigError: ``schema``, ``duplicate_tool``, ``duplicate_connection``,
            ``widens_connectors``, or ``widens_local_tools``. The whole override is rejected.
    """
    turn = copy.deepcopy(dict(effective))
    if session_settings is None:
        return turn
    raise_first_schema_error(SESSION_SETTINGS_VALIDATOR, session_settings)
    assert isinstance(session_settings, Mapping)
    if "model" in session_settings:
        turn["model"] = session_settings["model"]
    if "system_prompt" in session_settings:
        turn["system_prompt"] = session_settings["system_prompt"]
        turn["system_prompt_file"] = None

    enabled = session_settings.get("connectors", {}).get("enabled")
    if enabled is not None:
        bounds = effective["connectors"]["enabled"]
        turn["connectors"]["enabled"] = parse_connector_entries(enabled, bounds, check_bounds=True)

    always = turn["approval"]["always"]
    for tool in session_settings.get("approval", {}).get("always", []):
        if tool not in always:
            always.append(tool)

    narrow_local_tools(
        turn["local_tools"], effective["local_tools"], session_settings.get("local_tools", {})
    )
    return turn


def parse_connector_entries(
    entries: Iterable[str | Mapping[str, Any]],
    bounds: list[dict[str, Any]] | None = None,
    *,
    check_bounds: bool = False,
) -> list[dict[str, Any]]:
    """Return connector entries in effective form, checking each entry in list order."""
    parsed: list[dict[str, Any]] = []
    for index, raw_entry in enumerate(entries):
        path = f"/connectors/enabled/{index}"
        entry = parse_connector_entry(raw_entry, path)
        if any(is_same_connection_covered(earlier, entry) for earlier in parsed):
            raise ConfigError("duplicate_connection", path)
        if check_bounds and bounds is not None and not any(covers(b, entry) for b in bounds):
            raise ConfigError("widens_connectors", path)
        parsed.append(entry)
    return parsed


def parse_connector_entry(entry: str | Mapping[str, Any], path: str) -> dict[str, Any]:
    if isinstance(entry, str):
        key, tools = entry, None
    else:
        ((key, options),) = entry.items()
        tools = [canonical_operation_name(name) for name in options["tools"]]
        if len(set(tools)) != len(tools):
            raise ConfigError("duplicate_tool", path)
    connector, _, connection = key.partition("/")
    return {"connector": connector, "connection": connection or None, "tools": tools}


def canonical_operation_name(name: str) -> str:
    """Return an operation's snake-case name, as the agents cores name connector tools."""
    return "".join(f"_{c.lower()}" if "A" <= c <= "Z" else c for c in name).lstrip("_")


def is_same_connection_covered(first: Mapping[str, Any], second: Mapping[str, Any]) -> bool:
    return first["connector"] == second["connector"] and (
        first["connection"] is None
        or second["connection"] is None
        or first["connection"] == second["connection"]
    )


def covers(bound: Mapping[str, Any], entry: Mapping[str, Any]) -> bool:
    if bound["connector"] != entry["connector"]:
        return False
    if bound["connection"] is not None and bound["connection"] != entry["connection"]:
        return False
    if bound["tools"] is None:
        return True
    return entry["tools"] is not None and set(entry["tools"]) <= set(bound["tools"])


def narrow_local_tools(
    turn_local_tools: dict[str, Any],
    file_local_tools: Mapping[str, Any],
    session_local_tools: Mapping[str, Any],
) -> None:
    """Apply session local tool settings in field order, rejecting the first widening."""
    narrow_switch(turn_local_tools, file_local_tools, session_local_tools, "files", "/local_tools")
    session_shell = session_local_tools.get("shell", {})
    narrow_switch(
        turn_local_tools["shell"],
        file_local_tools["shell"],
        session_shell,
        "enabled",
        "/local_tools/shell",
    )
    if "allow" in session_shell:
        for index, command in enumerate(session_shell["allow"]):
            if command not in file_local_tools["shell"]["allow"]:
                raise ConfigError("widens_local_tools", f"/local_tools/shell/allow/{index}")
        turn_local_tools["shell"]["allow"] = list(session_shell["allow"])
    for switch in ("web_fetch", "browser"):
        narrow_switch(
            turn_local_tools, file_local_tools, session_local_tools, switch, "/local_tools"
        )


def narrow_switch(
    turn_section: dict[str, Any],
    file_section: Mapping[str, Any],
    session_section: Mapping[str, Any],
    switch: str,
    parent_path: str,
) -> None:
    if switch not in session_section:
        return
    if session_section[switch] and not file_section[switch]:
        raise ConfigError("widens_local_tools", f"{parent_path}/{switch}")
    turn_section[switch] = session_section[switch]


def is_shell_command_allowed(command: str, allow: Iterable[str]) -> bool:
    """Return whether a shell command runs without approval: an exact whole-text match."""
    return command in allow


def raise_first_schema_error(validator: Draft202012Validator, instance: object) -> None:
    error = next(iter(validator.iter_errors(instance)), None)
    if error is not None:
        raise ConfigError("schema", json_pointer(error))


def schema_errors(validator: Draft202012Validator, instance: object) -> set[tuple[str, str]]:
    """Return the instance location and keyword of every schema error, nested ones included."""
    return {
        (json_pointer(error), str(error.validator))
        for error in walk_errors(validator.iter_errors(instance))
    }


def walk_errors(errors: Iterable[ValidationError]) -> Iterator[ValidationError]:
    for error in errors:
        yield error
        yield from walk_errors(error.context or [])


def json_pointer(error: ValidationError) -> str:
    return "".join(
        "/" + str(part).replace("~", "~0").replace("/", "~1") for part in error.absolute_path
    )
