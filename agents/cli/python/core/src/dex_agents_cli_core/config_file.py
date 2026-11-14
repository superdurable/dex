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
import os
import secrets
import stat
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from ruamel.yaml import YAML

from dex_agents_cli_core.config import (
    DEFAULT_WORKSPACE,
    ConfigError,
    EffectiveConfig,
    parse_config_yaml,
    resolve_config,
)
from dex_agents_cli_core.environment import CommandEnvironment
from dex_agents_cli_core.harness import AgentHarness


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


@dataclass(frozen=True)
class ExistingConfigFile:
    """The configuration file ``init`` finds before it writes.

    Attributes:
        values: The parsed file when it is valid, which ``init`` keeps except
            for the fields it asks about; ``None`` when it is absent or invalid.
        is_invalid: Whether the file exists but is not YAML or fails the
            configuration rules; ``init`` replaces it.

    .. versionadded:: 0.6.9
    """

    values: dict[str, Any] | None
    is_invalid: bool


# The configuration may hold Dex server settings, so a new file is private to its owner.
_DEFAULT_CONFIG_FILE_MODE = 0o600

# init asks for the model, so an existing file without one is still kept.
_MODEL_PLACEHOLDER_FOR_EXISTING_FILE = "init/model-placeholder"


def default_config_path(environment: CommandEnvironment, harness: AgentHarness) -> Path:
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


def read_existing_config_file(path: Path, harness: AgentHarness) -> ExistingConfigFile:
    """Read the configuration file that ``init`` is about to write over.

    A file counts as valid when it is YAML and passes the configuration rules.
    A missing ``model`` does not make it invalid, because ``init`` asks for it.

    Args:
        path: The file.
        harness: The command's harness.

    Returns:
        Whether the file is absent, valid with its parsed values, or invalid.

    Raises:
        ConfigFileError: If the file exists but cannot be read.

    .. versionadded:: 0.6.9
    """
    if not path.exists():
        return ExistingConfigFile(values=None, is_invalid=False)
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as error:
        raise ConfigFileError(f"cannot read {path}: {error.strerror}") from error
    try:
        values = parse_config_yaml(text)
        resolve_config(
            values,
            harness.config_section,
            harness.default_model or _MODEL_PLACEHOLDER_FOR_EXISTING_FILE,
        )
    except ConfigError:
        return ExistingConfigFile(values=None, is_invalid=True)
    return ExistingConfigFile(values=values if isinstance(values, dict) else {}, is_invalid=False)


def back_up_invalid_config_file(path: Path) -> Path:
    """Copy an invalid configuration file to ``NAME.yaml.bak`` before ``init`` replaces it.

    The copy replaces an earlier ``NAME.yaml.bak``. When that path is a
    symbolic link, the copy replaces the link itself and leaves the file it
    pointed at unchanged.

    Args:
        path: The invalid file.

    Returns:
        The path of the copy.

    Raises:
        ConfigFileError: If the file cannot be read or the copy cannot be
            written.

    .. versionadded:: 0.6.9
    """
    backup_path = path.with_name(path.name + ".bak")
    try:
        _replace_file_atomically(backup_path, path.read_bytes(), stat.S_IMODE(path.stat().st_mode))
    except OSError as error:
        raise ConfigFileError(
            f"cannot back up {path} to {backup_path}: {error.strerror or error}"
        ) from error
    return backup_path


def describe_replaced_invalid_config_file(path: Path, backup_path: Path) -> str:
    """Return the line ``init`` prints after it replaced an invalid configuration file.

    Args:
        path: The replaced file.
        backup_path: The copy of the old file.

    Returns:
        One line.

    .. versionadded:: 0.6.9
    """
    return f"{path} was invalid, so init replaced it; the old file is at {backup_path}."


def merge_init_values(existing: dict[str, Any], init_values: dict[str, Any]) -> dict[str, Any]:
    """Return the file ``init`` writes over an existing valid file.

    The result is the answers' values with every field of the existing file
    that ``init`` does not ask about:

    - ``model``, ``system_prompt``, and ``workspace`` come from the answers only.
    - ``system_prompt_file`` stays unless the answers have ``system_prompt``.
    - ``connectors`` stays except ``enabled``, which comes from the answers.
    - ``local_tools`` comes from the answers, but an empty ``shell.allow``
      keeps the existing list.
    - Every other top-level key of the existing file replaces the answers'
      value whole.
    - A top-level key the existing file lacks keeps the answers' value.

    Both CLI cores pass the cases of ``agents/conformance/cli_init.json``.

    Args:
        existing: The parsed existing file.
        init_values: The values ``init`` writes without an existing file.

    Returns:
        The merged values.

    .. versionadded:: 0.6.9
    """
    merged = dict(init_values)
    for key, value in existing.items():
        if key in ("model", "system_prompt", "workspace"):
            continue
        if key == "system_prompt_file":
            if "system_prompt" not in init_values:
                merged[key] = value
        elif key == "connectors":
            merged[key] = _merge_init_connectors(value, init_values.get("connectors"))
        elif key == "local_tools":
            merged[key] = _merge_init_local_tools(value, init_values.get("local_tools"))
        else:
            merged[key] = value
    return merged


def default_config_values(
    harness: AgentHarness, existing: dict[str, Any] | None = None
) -> dict[str, Any]:
    """Return the values ``init --yes`` writes: the defaults, stated in full.

    Args:
        harness: The command's harness.
        existing: The parsed existing valid file, or ``None`` for none.

    Returns:
        The configuration file's values; merge them into an existing file
        with :func:`merge_init_values`.

    .. versionchanged:: 0.6.9
       With an existing file, the values are the file's, the same answers
       that pressing Enter at every question gives.
    """
    if existing is not None:
        return ask_config_values(harness, _take_default_answer, existing)
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
            "media": {"store": "auto"},
            "sessions": {"summary": "auto"},
        }
    )
    return values


def ask_config_values(
    harness: AgentHarness,
    ask: Callable[[str, str], str],
    existing: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Ask the init questions and return the configuration file's values.

    Args:
        harness: The command's harness.
        ask: Shows a question with its default and returns the answer, the
            default when the answer is empty.
        existing: The parsed existing file, whose values become the defaults,
            or ``None`` for none.

    Returns:
        The values to write; merge them into an existing file with
        :func:`merge_init_values`.

    .. versionchanged:: 0.6.9
       With an existing file, each default is the file's value.
    """
    current: dict[str, Any] = existing or {}
    current_connectors = _mapping(current.get("connectors"))
    current_local_tools = _mapping(current.get("local_tools"))
    current_shell = _mapping(current_local_tools.get("shell"))
    values = default_config_values(harness)
    model = _answer_or_exact_default(
        ask, "Model", current.get("model") or harness.default_model or ""
    )
    if model:
        values["model"] = model
    if "system_prompt" in current:
        prompt_question = "System prompt (empty keeps the current prompt)"
    elif "system_prompt_file" in current:
        prompt_question = "System prompt (empty keeps the current system_prompt_file)"
    else:
        prompt_question = "System prompt (empty for the harness default)"
    prompt = ask(prompt_question, "").strip()
    if prompt:
        values["system_prompt"] = prompt
    elif "system_prompt" in current:
        values["system_prompt"] = current["system_prompt"]
    current_entries = current_connectors.get("enabled")
    shown_entries = (
        ", ".join(_connector_entry_key(entry) for entry in current_entries)
        if isinstance(current_entries, list)
        else ""
    )
    connectors = ask(
        "Connectors to enable, comma-separated (empty for all authorized)", shown_entries
    )
    entries = [entry.strip() for entry in connectors.split(",") if entry.strip()]
    if isinstance(current_entries, list) and connectors == shown_entries:
        # Keeps map entries and their tools lists, which the shown keys leave out.
        values["connectors"]["enabled"] = current_entries
    elif entries:
        values["connectors"]["enabled"] = entries
    values["workspace"] = _answer_or_exact_default(
        ask, "Workspace for local files and commands", current.get("workspace") or DEFAULT_WORKSPACE
    )
    local_tools = values["local_tools"]
    local_tools["files"] = _is_yes(
        ask(
            "Allow file tools in the workspace? (y/n)", _yes_or_no(current_local_tools.get("files"))
        )
    )
    local_tools["shell"]["enabled"] = _is_yes(
        ask("Allow shell commands? (y/n)", _yes_or_no(current_shell.get("enabled")))
    )
    if local_tools["shell"]["enabled"]:
        current_allow = current_shell.get("allow")
        shown_allow = ", ".join(current_allow) if isinstance(current_allow, list) else ""
        allow = ask("Commands that run without approval, comma-separated", shown_allow)
        if isinstance(current_allow, list) and allow == shown_allow:
            # A listed command may itself contain a comma.
            local_tools["shell"]["allow"] = current_allow
        else:
            local_tools["shell"]["allow"] = [
                part.strip() for part in allow.split(",") if part.strip()
            ]
    local_tools["web_fetch"] = _is_yes(
        ask("Allow web fetching? (y/n)", _yes_or_no(current_local_tools.get("web_fetch")))
    )
    local_tools["browser"] = _is_yes(
        ask("Allow a local browser? (y/n)", _yes_or_no(current_local_tools.get("browser")))
    )
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
    # A symbolic link stays: init writes the file it points at.
    target = path.resolve() if path.is_symlink() else path
    try:
        target.parent.mkdir(parents=True, exist_ok=True)
        mode = _existing_file_mode(target)
        _replace_file_atomically(
            target,
            (header + buffer.getvalue()).encode("utf-8"),
            _DEFAULT_CONFIG_FILE_MODE if mode is None else mode,
        )
    except OSError as error:
        raise ConfigFileError(f"cannot write {path}: {error.strerror or error}") from error
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


def _existing_file_mode(path: Path) -> int | None:
    try:
        return stat.S_IMODE(path.stat().st_mode)
    except FileNotFoundError:
        return None


def _replace_file_atomically(path: Path, content: bytes, mode: int) -> None:
    """Write a temporary file beside ``path``, sync it, and rename it over ``path``.

    A crash or a full disk leaves either the old file or the new one, never a
    truncated file. The rename replaces a symbolic link at ``path`` rather than
    the file it points at.
    """
    temporary = path.with_name(f".{path.name}.{os.getpid()}.{secrets.token_hex(6)}.tmp")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    try:
        with os.fdopen(descriptor, "wb") as file:
            os.chmod(temporary, mode)
            file.write(content)
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, path)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise
    _sync_directory(path.parent)


def _sync_directory(directory: Path) -> None:
    # Windows cannot open a directory to sync it.
    if os.name == "nt":
        return
    descriptor = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _take_default_answer(question: str, default: str) -> str:
    return default


def _answer_or_exact_default(ask: Callable[[str, str], str], question: str, default: str) -> str:
    """Return a typed answer, stripped, or the default exactly as the file has it."""
    answer = ask(question, default)
    return default if answer == default else answer.strip()


def _yes_or_no(current: object) -> str:
    """Return a local tool switch's y/n default: on unless the file switches it off."""
    return "n" if current is False else "y"


def _mapping(value: object) -> dict[str, Any]:
    return value if isinstance(value, dict) else {}


def _connector_entry_key(entry: object) -> str:
    if isinstance(entry, dict):
        return str(next(iter(entry), ""))
    return str(entry)


def _merge_init_connectors(existing: object, init_connectors: object) -> dict[str, Any]:
    kept = {key: value for key, value in _mapping(existing).items() if key != "enabled"}
    init_mapping = _mapping(init_connectors)
    if "enabled" in init_mapping:
        kept["enabled"] = init_mapping["enabled"]
    return kept


def _merge_init_local_tools(existing: object, init_local_tools: object) -> object:
    existing_shell = _mapping(_mapping(existing).get("shell"))
    init_mapping = _mapping(init_local_tools)
    init_shell = _mapping(init_mapping.get("shell"))
    if "allow" not in existing_shell or "shell" not in init_mapping or init_shell.get("allow"):
        return init_local_tools
    return {**init_mapping, "shell": {**init_shell, "allow": existing_shell["allow"]}}
