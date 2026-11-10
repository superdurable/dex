# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Find, load, and create a command's configuration file."""

from __future__ import annotations

import io
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from ruamel.yaml import YAML

from dex_agents_cli.config import (
    DEFAULT_WORKSPACE,
    ConfigError,
    EffectiveConfig,
    parse_config_yaml,
    resolve_config,
)
from dex_agents_cli.environment import RuntimeEnvironment
from dex_agents_cli.harness import AgentHarness


@dataclass(frozen=True)
class LoadedConfig:
    """A command's effective configuration and where it came from.

    Attributes:
        config: The effective configuration.
        path: The file, or ``None`` when the command runs on defaults.
        directory: The directory relative paths in the file are relative to.
    """

    config: EffectiveConfig
    path: Path | None
    directory: Path


class ConfigFileError(Exception):
    """Report a configuration file that cannot be read or is invalid."""


def default_config_path(environment: RuntimeEnvironment, harness: AgentHarness) -> Path:
    """Return ``~/.dex/agents/<name>.yaml``.

    Args:
        environment: The machine settings.
        harness: The command's harness.

    Returns:
        The default path.
    """
    return environment.agents_directory / f"{harness.name}.yaml"


def load_config_file(path: Path, harness: AgentHarness) -> LoadedConfig:
    """Read and resolve a configuration file.

    Args:
        path: The file.
        harness: The command's harness.

    Returns:
        The effective configuration.

    Raises:
        ConfigFileError: If the file cannot be read or fails a rule, with the
            error code and JSON Pointer.
    """
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as error:
        raise ConfigFileError(f"cannot read {path}: {error.strerror}") from error
    try:
        config = resolve_config(
            parse_config_yaml(text), harness.config_section, harness.default_model
        )
    except ConfigError as error:
        raise ConfigFileError(f"{path}: {error}") from error
    return LoadedConfig(config=config, path=path, directory=path.parent)


def default_config_values(harness: AgentHarness) -> dict[str, Any]:
    """Return the values ``init --yes`` writes: the defaults, stated in full.

    Args:
        harness: The command's harness.

    Returns:
        The configuration file's values.
    """
    values: dict[str, Any] = {}
    if harness.default_model is not None:
        values["model"] = harness.default_model
    values.update(
        {
            "dex": {"server": "auto"},
            "connectors": {"mcp": "auto"},
            "workspace": DEFAULT_WORKSPACE,
            "local_tools": {
                "files": True,
                "shell": {"enabled": True, "allow": []},
                "web_fetch": True,
                "browser": True,
            },
        }
    )
    return values


def ask_config_values(harness: AgentHarness, ask: Callable[[str, str], str]) -> dict[str, Any]:
    """Ask the init questions and return the configuration file's values.

    Args:
        harness: The command's harness.
        ask: Shows a question with its default and returns the answer, the
            default when the answer is empty.

    Returns:
        The values to write.
    """
    values = default_config_values(harness)
    model = ask("Model", harness.default_model or "").strip()
    if model:
        values["model"] = model
    prompt = ask("System prompt (empty for the harness default)", "").strip()
    if prompt:
        values["system_prompt"] = prompt
    connectors = ask("Connectors to enable, comma-separated (empty for all authorized)", "")
    entries = [entry.strip() for entry in connectors.split(",") if entry.strip()]
    if entries:
        values["connectors"]["enabled"] = entries
    values["workspace"] = ask("Workspace for local files and commands", DEFAULT_WORKSPACE).strip()
    local_tools = values["local_tools"]
    local_tools["files"] = _is_yes(ask("Allow file tools in the workspace? (y/n)", "y"))
    local_tools["shell"]["enabled"] = _is_yes(ask("Allow shell commands? (y/n)", "y"))
    if local_tools["shell"]["enabled"]:
        allow = ask("Commands that run without approval, comma-separated", "")
        local_tools["shell"]["allow"] = [part.strip() for part in allow.split(",") if part.strip()]
    local_tools["web_fetch"] = _is_yes(ask("Allow web fetching? (y/n)", "y"))
    local_tools["browser"] = _is_yes(ask("Allow a local browser? (y/n)", "y"))
    return values


def write_config_file(path: Path, values: dict[str, Any], harness: AgentHarness) -> LoadedConfig:
    """Check values against the configuration rules and write them as YAML.

    Args:
        path: The file to create or replace.
        values: The configuration file's values.
        harness: The command's harness.

    Returns:
        The effective configuration of the written file.

    Raises:
        ConfigFileError: If the values fail a rule; nothing is written.
    """
    try:
        resolve_config(values, harness.config_section, harness.default_model)
    except ConfigError as error:
        raise ConfigFileError(f"the answers are not a valid configuration: {error}") from error
    yaml = YAML(typ="safe", pure=True)
    yaml.default_flow_style = False
    buffer = io.StringIO()
    yaml.dump(values, buffer)
    header = (
        f"# The configuration of dex-agents-{harness.name}; regenerate it with "
        f"`dex-agents-{harness.name} init`.\n"
        "# Provider keys stay in the environment. Schema: agents/cli/config.schema.json "
        "in the Dex repository.\n"
    )
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(header + buffer.getvalue(), encoding="utf-8")
    return load_config_file(path, harness)


def defaults_without_file(harness: AgentHarness, directory: Path) -> LoadedConfig:
    """Return the configuration of a command that runs without a file.

    Args:
        harness: The command's harness.
        directory: The directory relative paths resolve against.

    Returns:
        Every default.

    Raises:
        ConfigFileError: If the harness has no default model.
    """
    try:
        config = resolve_config({}, harness.config_section, harness.default_model)
    except ConfigError as error:
        raise ConfigFileError(
            f"dex-agents-{harness.name} has no default model; run `dex-agents-{harness.name} init`"
        ) from error
    return LoadedConfig(config=config, path=None, directory=directory)


def _is_yes(answer: str) -> bool:
    return answer.strip().lower() in ("y", "yes", "true")
