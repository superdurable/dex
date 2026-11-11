# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Dex Agents CLI configuration: parsing, defaults, and session narrowing.

The rules are the shared contract in ``agents/cli/README.md``, checked by
``agents/conformance/cli_config.json``.
"""

from __future__ import annotations

import ipaddress
import json
import re
from collections.abc import Iterable, Iterator, Mapping, Sequence
from dataclasses import dataclass, field, replace
from importlib import resources
from typing import Any

from jsonschema import Draft202012Validator, ValidationError
from ruamel.yaml import YAML
from ruamel.yaml.error import YAMLError

#: ``dex.server``, ``connectors.mcp``, and ``media.store`` value that lets the CLI core choose.
AUTO = "auto"
#: ``workspace`` when the file omits it.
DEFAULT_WORKSPACE = "~/dex-agent-workspace"
#: The JSON Schema of the configuration file.
CONFIG_SCHEMA: dict[str, Any] = json.loads(
    resources.files("dex_agents_cli_core.schemas").joinpath("config.schema.json").read_text("utf-8")
)
#: The JSON Schema of one session's settings.
SESSION_SETTINGS_SCHEMA: dict[str, Any] = json.loads(
    resources.files("dex_agents_cli_core.schemas")
    .joinpath("session-settings.schema.json")
    .read_text("utf-8")
)

_CONFIG_VALIDATOR = Draft202012Validator(CONFIG_SCHEMA)
_SESSION_SETTINGS_VALIDATOR = Draft202012Validator(SESSION_SETTINGS_SCHEMA)
_SESSION_SETTINGS_KEYS = frozenset(SESSION_SETTINGS_SCHEMA["properties"])
_MAX_PORT = 65535
_MAX_IPV4_PART = 255
_URL_PARTS = re.compile(
    r"https?://(?P<host>\[[^\]]*\]|[^:/]+)(?::(?P<port>[0-9]+))?(?:/.*)?", re.DOTALL
)


class ConfigError(Exception):
    """Report a rejected configuration file or session settings.

    Attributes:
        code: The stable error code from the conformance file: ``yaml``,
            ``schema``, ``invalid_url``, ``duplicate_tool``,
            ``duplicate_connection``, ``model_required``,
            ``widens_connectors``, or ``widens_local_tools``.
        path: JSON Pointer of the first problem, ``""`` for the whole value.
        keyword: For ``schema``, the JSON Schema keyword of the first error,
            such as ``anyOf``; ``None`` otherwise.
        schema_errors: For ``schema``, the instance location and keyword of
            every schema error, nested ones included; empty otherwise.

    Its message is ``<code> at "<pointer>"``, followed by
    `` (schema keyword <keyword>)`` for a schema error, as in the TypeScript CLI core.
    """

    def __init__(
        self,
        code: str,
        path: str,
        *,
        keyword: str | None = None,
        schema_errors: frozenset[tuple[str, str]] = frozenset(),
    ) -> None:
        """Create the error.

        Args:
            code: The stable error code.
            path: JSON Pointer of the first problem.
            keyword: The failed JSON Schema keyword of a schema error.
            schema_errors: Every schema error's location and keyword.
        """
        message = f"{code} at {json.dumps(path)}"
        super().__init__(message if keyword is None else f"{message} (schema keyword {keyword})")
        self.code = code
        self.path = path
        self.keyword = keyword
        self.schema_errors = schema_errors


@dataclass(frozen=True)
class ConnectorEntry:
    """One ``connectors.enabled`` entry in effective form.

    Attributes:
        connector: Connector ID, such as ``slack``.
        connection: Connection name, or ``None`` for every connection.
        tools: Snake-case operation names, or ``None`` for every operation.
    """

    connector: str
    connection: str | None
    tools: tuple[str, ...] | None

    def covers_connection(self, connector: str, connection: str) -> bool:
        """Report whether this entry names a connection.

        Args:
            connector: Connector ID.
            connection: Connection name.

        Returns:
            ``True`` for the same connector and, when the entry names one, connection.
        """
        return self.connector == connector and self.connection in (None, connection)

    def allows_operation(self, operation: str) -> bool:
        """Report whether this entry allows an operation.

        Args:
            operation: Operation name in either spelling.

        Returns:
            ``True`` when the entry lists no tools or lists this one.
        """
        return self.tools is None or canonical_operation_name(operation) in self.tools


@dataclass(frozen=True)
class DexServerConfig:
    """The ``dex`` section.

    Attributes:
        server: ``auto`` or the Dex server URL.
    """

    server: str = AUTO


@dataclass(frozen=True)
class ConnectorsConfig:
    """The ``connectors`` section.

    Attributes:
        mcp: ``auto`` or the loopback URL of the connectors MCP server.
        enabled: The entries, or ``None`` for every authorized connection.
    """

    mcp: str = AUTO
    enabled: tuple[ConnectorEntry, ...] | None = None

    def allows_tool(self, connector: str, connection: str, operation: str) -> bool:
        """Report whether a connector operation on one connection is enabled.

        Args:
            connector: Connector ID.
            connection: Connection name.
            operation: Operation name in either spelling.

        Returns:
            ``True`` when ``enabled`` is ``None`` or an entry covers the call.
        """
        if self.enabled is None:
            return True
        return any(
            entry.covers_connection(connector, connection) and entry.allows_operation(operation)
            for entry in self.enabled
        )


@dataclass(frozen=True)
class ApprovalConfig:
    """The ``approval`` section.

    Attributes:
        always: Tool names whose every call waits for approval.
    """

    always: tuple[str, ...] = ()


@dataclass(frozen=True)
class ShellConfig:
    """The ``local_tools.shell`` section.

    Attributes:
        enabled: Whether shell commands are offered.
        allow: Commands that run without approval, matched as whole text.
    """

    enabled: bool = True
    allow: tuple[str, ...] = ()


@dataclass(frozen=True)
class LocalToolsConfig:
    """The ``local_tools`` section.

    Attributes:
        files: Whether the file tools are offered.
        shell: The shell tool.
        web_fetch: Whether web fetching is offered.
        browser: Whether the browser tools are offered.
    """

    files: bool = True
    shell: ShellConfig = field(default_factory=ShellConfig)
    web_fetch: bool = True
    browser: bool = True


@dataclass(frozen=True)
class MediaConfig:
    """The ``media`` section: where the images of the sessions are stored.

    Session settings never change it.

    Attributes:
        store: ``auto`` for the local directory ``~/.dex/agents/media``, or
            ``s3://<bucket>`` or ``s3://<bucket>/<prefix>``.
        endpoint: The URL of an S3-compatible service, or ``None`` for AWS S3.
            It is only set with an ``s3://`` store.

    .. versionadded:: 0.5.0
    """

    store: str = AUTO
    endpoint: str | None = None


@dataclass(frozen=True)
class EffectiveConfig:
    """A configuration with every default applied: a file's, or one turn's.

    ``workspace`` and ``system_prompt_file`` keep their text as written; the
    CLI core expands ``~`` and relative paths when it uses them.

    Attributes:
        model: The model ID.
        system_prompt: The instructions, or ``None``.
        system_prompt_file: The instructions file, or ``None``.
        dex: The Dex server.
        connectors: The connector tools.
        approval: Extra approval.
        workspace: The local tools' directory.
        local_tools: The local tools.
        harness_options: The command's own harness section, ``{}`` by default.
        media: The media store of the images. Session settings never change it.

            .. versionadded:: 0.5.0
    """

    model: str
    system_prompt: str | None = None
    system_prompt_file: str | None = None
    dex: DexServerConfig = field(default_factory=DexServerConfig)
    connectors: ConnectorsConfig = field(default_factory=ConnectorsConfig)
    approval: ApprovalConfig = field(default_factory=ApprovalConfig)
    workspace: str = DEFAULT_WORKSPACE
    local_tools: LocalToolsConfig = field(default_factory=LocalToolsConfig)
    harness_options: Mapping[str, Any] = field(default_factory=dict)
    media: MediaConfig = field(default_factory=MediaConfig)

    def to_json(self) -> dict[str, Any]:
        """Return the configuration in the conformance file's JSON shape.

        Returns:
            A JSON object with lists for sequences and ``null`` for omitted values.
        """
        enabled = self.connectors.enabled
        return {
            "model": self.model,
            "system_prompt": self.system_prompt,
            "system_prompt_file": self.system_prompt_file,
            "dex": {"server": self.dex.server},
            "connectors": {
                "mcp": self.connectors.mcp,
                "enabled": None
                if enabled is None
                else [
                    {
                        "connector": entry.connector,
                        "connection": entry.connection,
                        "tools": None if entry.tools is None else list(entry.tools),
                    }
                    for entry in enabled
                ],
            },
            "approval": {"always": list(self.approval.always)},
            "workspace": self.workspace,
            "local_tools": {
                "files": self.local_tools.files,
                "shell": {
                    "enabled": self.local_tools.shell.enabled,
                    "allow": list(self.local_tools.shell.allow),
                },
                "web_fetch": self.local_tools.web_fetch,
                "browser": self.local_tools.browser,
            },
            "media": {"store": self.media.store, "endpoint": self.media.endpoint},
            "harness_options": json.loads(json.dumps(self.harness_options)),
        }


@dataclass(frozen=True)
class TurnConfigResolution:
    """One turn's configuration after its session settings.

    Attributes:
        config: The turn's configuration.
        warnings: JSON Pointers of the ignored top-level settings keys, sorted.
    """

    config: EffectiveConfig
    warnings: tuple[str, ...] = ()


def parse_config_yaml(text: str) -> object:
    """Return the value of a configuration file's YAML 1.2 text.

    Args:
        text: The file's text.

    Returns:
        The parsed value; ``None`` for an empty file.

    Raises:
        ConfigError: ``yaml`` at ``""`` for text that is not one YAML 1.2
            document with unique keys.
    """
    yaml = YAML(typ="safe", pure=True)
    yaml.version = (1, 2)
    try:
        return yaml.load(text)
    except YAMLError as error:
        raise ConfigError("yaml", "") from error


def resolve_config(
    config: object, harness_section: str, harness_default_model: str | None
) -> EffectiveConfig:
    """Return the effective configuration of a parsed configuration file.

    Args:
        config: The parsed file; ``None`` is an empty file.
        harness_section: The command's harness section, such as ``claude``.
        harness_default_model: The command's default model, or ``None``.

    Returns:
        The configuration with every default applied.

    Raises:
        ConfigError: ``schema``, ``invalid_url``, ``duplicate_tool``,
            ``duplicate_connection``, or ``model_required``, in the
            conformance file's check order.
    """
    if config is None:
        config = {}
    _raise_first_schema_error(_CONFIG_VALIDATOR, config)
    assert isinstance(config, Mapping), "the schema admits only objects"
    dex = config.get("dex", {})
    connectors = config.get("connectors", {})
    server = dex.get("server", AUTO)
    mcp = connectors.get("mcp", AUTO)
    _raise_invalid_url(server, "/dex/server")
    _raise_invalid_url(mcp, "/connectors/mcp")
    media = config.get("media", {})
    endpoint = media.get("endpoint")
    if endpoint is not None:
        _raise_invalid_url(endpoint, "/media/endpoint")
    enabled = connectors.get("enabled")
    entries = None if enabled is None else _parse_connector_entries(enabled, None)
    model = config.get("model", harness_default_model)
    if model is None:
        raise ConfigError("model_required", "/model")
    local_tools = config.get("local_tools", {})
    shell = local_tools.get("shell", {})
    return EffectiveConfig(
        model=model,
        system_prompt=config.get("system_prompt"),
        system_prompt_file=config.get("system_prompt_file"),
        dex=DexServerConfig(server=server),
        connectors=ConnectorsConfig(mcp=mcp, enabled=entries),
        approval=ApprovalConfig(always=tuple(config.get("approval", {}).get("always", []))),
        workspace=config.get("workspace", DEFAULT_WORKSPACE),
        local_tools=LocalToolsConfig(
            files=local_tools.get("files", True),
            shell=ShellConfig(
                enabled=shell.get("enabled", True), allow=tuple(shell.get("allow", []))
            ),
            web_fetch=local_tools.get("web_fetch", True),
            browser=local_tools.get("browser", True),
        ),
        harness_options=json.loads(json.dumps(config.get(harness_section, {}))),
        media=MediaConfig(store=media.get("store", AUTO), endpoint=endpoint),
    )


def resolve_turn_config(
    effective: EffectiveConfig, session_settings: object
) -> TurnConfigResolution:
    """Return one turn's configuration: the file's, narrowed by the session settings.

    Args:
        effective: The file's effective configuration, which bounds the turn.
        session_settings: The session's settings, or ``None`` for none.

    Returns:
        The turn's configuration and the ignored settings keys.

    Raises:
        ConfigError: ``schema``, ``duplicate_tool``, ``duplicate_connection``,
            ``widens_connectors``, or ``widens_local_tools``. The whole
            override is rejected.
    """
    if session_settings is None:
        return TurnConfigResolution(effective)
    _raise_first_schema_error(_SESSION_SETTINGS_VALIDATOR, session_settings)
    assert isinstance(session_settings, Mapping), "the schema admits only objects"
    warnings = tuple(
        _json_pointer([key])
        for key in sorted(session_settings)
        if key not in _SESSION_SETTINGS_KEYS
    )
    turn = effective
    if "model" in session_settings:
        turn = replace(turn, model=session_settings["model"])
    if "system_prompt" in session_settings:
        turn = replace(
            turn, system_prompt=session_settings["system_prompt"], system_prompt_file=None
        )
    enabled = session_settings.get("connectors", {}).get("enabled")
    if enabled is not None:
        entries = _parse_connector_entries(enabled, effective.connectors.enabled)
        turn = replace(turn, connectors=replace(turn.connectors, enabled=entries))
    added = [
        tool
        for tool in dict.fromkeys(session_settings.get("approval", {}).get("always", []))
        if tool not in effective.approval.always
    ]
    turn = replace(turn, approval=ApprovalConfig(always=(*effective.approval.always, *added)))
    local_tools = _narrow_local_tools(
        effective.local_tools, session_settings.get("local_tools", {})
    )
    return TurnConfigResolution(replace(turn, local_tools=local_tools), warnings)


def canonical_operation_name(name: str) -> str:
    """Return an operation's snake-case name by the connectors library's lowerCamelToSnake rule.

    A capital letter starts a new word after a lowercase letter or a digit, or
    when it ends an acronym run before a lowercase letter that is not a plural
    ``s``. A snake-case name is returned unchanged.

    Example:
        >>> canonical_operation_name("listStoryIDs")
        'list_story_ids'

    Args:
        name: A snake-case name or an operationId.

    Returns:
        The snake-case name.
    """
    converted: list[str] = []
    for index, character in enumerate(name):
        if not _is_ascii_upper(character):
            converted.append(character)
            continue
        previous = name[index - 1] if index > 0 else ""
        following = name[index + 1] if index + 1 < len(name) else ""
        after_following = name[index + 2] if index + 2 < len(name) else ""
        is_after_lower_or_digit = _is_ascii_lower(previous) or previous.isdigit()
        is_acronym_plural = following == "s" and not _is_ascii_lower(after_following)
        is_end_of_acronym = (
            _is_ascii_upper(previous) and _is_ascii_lower(following) and not is_acronym_plural
        )
        if is_after_lower_or_digit or is_end_of_acronym:
            converted.append("_")
        converted.append(character.lower())
    return "".join(converted)


def is_shell_command_allowed(command: str, allow: Iterable[str]) -> bool:
    """Report whether a shell command runs without approval.

    Args:
        command: The command text as the agent sent it.
        allow: The allow list.

    Returns:
        ``True`` only when the whole text equals an entry; ``ls -la`` does not match ``ls``.
    """
    return command in tuple(allow)


def schema_error_locations(errors: Iterable[ValidationError]) -> frozenset[tuple[str, str]]:
    """Return the instance location and keyword of every schema error, nested ones included.

    Args:
        errors: Errors from a validator.

    Returns:
        ``(JSON Pointer, keyword)`` pairs.
    """
    return frozenset(
        (_json_pointer(error.absolute_path), str(error.validator)) for error in _walk(errors)
    )


def _walk(errors: Iterable[ValidationError]) -> Iterator[ValidationError]:
    for error in errors:
        yield error
        yield from _walk(error.context or [])


def _raise_first_schema_error(validator: Draft202012Validator, instance: object) -> None:
    errors = list(validator.iter_errors(instance))
    if errors:
        first = errors[0]
        raise ConfigError(
            "schema",
            _json_pointer(first.absolute_path),
            keyword=str(first.validator),
            schema_errors=schema_error_locations(errors),
        )


def _raise_invalid_url(url: str, path: str) -> None:
    if url == AUTO:
        return
    parts = _URL_PARTS.fullmatch(url)
    assert parts is not None, "the schema admits only URLs of this shape"
    host, port = parts["host"], parts["port"]
    if port is not None and int(port) > _MAX_PORT:
        raise ConfigError("invalid_url", path)
    if host.startswith("["):
        try:
            ipaddress.IPv6Address(host[1:-1])
        except ValueError as error:
            raise ConfigError("invalid_url", path) from error
    elif re.fullmatch(r"[0-9.]+", host):
        # A leading zero means octal to a WHATWG parser, so only canonical decimals pass.
        if any(
            not re.fullmatch(r"0|[1-9][0-9]{0,2}", part) or int(part) > _MAX_IPV4_PART
            for part in host.split(".")
        ):
            raise ConfigError("invalid_url", path)
    elif any(label.lower().startswith("xn--") for label in host.split(".")):
        raise ConfigError("invalid_url", path)


def _parse_connector_entries(
    raw_entries: Sequence[str | Mapping[str, Any]],
    bounds: tuple[ConnectorEntry, ...] | None,
) -> tuple[ConnectorEntry, ...]:
    """Parse entries in list order; ``bounds`` is the file's list when narrowing a session."""
    parsed: list[ConnectorEntry] = []
    for index, raw_entry in enumerate(raw_entries):
        path = f"/connectors/enabled/{index}"
        entry = _parse_connector_entry(raw_entry, path)
        if any(_covers_same_connection(earlier, entry) for earlier in parsed):
            raise ConfigError("duplicate_connection", path)
        if bounds is not None and not any(_bounds_entry(bound, entry) for bound in bounds):
            raise ConfigError("widens_connectors", path)
        parsed.append(entry)
    return tuple(parsed)


def _parse_connector_entry(raw_entry: str | Mapping[str, Any], path: str) -> ConnectorEntry:
    if isinstance(raw_entry, str):
        key, tools = raw_entry, None
    else:
        ((key, options),) = raw_entry.items()
        tools = tuple(canonical_operation_name(name) for name in options["tools"])
        if len(set(tools)) != len(tools):
            raise ConfigError("duplicate_tool", path)
    connector, _, connection = key.partition("/")
    return ConnectorEntry(connector=connector, connection=connection or None, tools=tools)


def _covers_same_connection(first: ConnectorEntry, second: ConnectorEntry) -> bool:
    return first.connector == second.connector and (
        first.connection is None
        or second.connection is None
        or first.connection == second.connection
    )


def _bounds_entry(bound: ConnectorEntry, entry: ConnectorEntry) -> bool:
    if bound.connector != entry.connector:
        return False
    if bound.connection is not None and bound.connection != entry.connection:
        return False
    if bound.tools is None:
        return True
    return entry.tools is not None and set(entry.tools) <= set(bound.tools)


def _narrow_local_tools(
    file_tools: LocalToolsConfig, session_tools: Mapping[str, Any]
) -> LocalToolsConfig:
    """Apply session local tool settings in check order, rejecting the first widening."""
    files = _narrow_switch(file_tools.files, session_tools, "files", "/local_tools")
    session_shell = session_tools.get("shell", {})
    shell_enabled = _narrow_switch(
        file_tools.shell.enabled, session_shell, "enabled", "/local_tools/shell"
    )
    allow = file_tools.shell.allow
    if "allow" in session_shell:
        for index, command in enumerate(session_shell["allow"]):
            if command not in file_tools.shell.allow:
                raise ConfigError("widens_local_tools", f"/local_tools/shell/allow/{index}")
        allow = tuple(session_shell["allow"])
    web_fetch = _narrow_switch(file_tools.web_fetch, session_tools, "web_fetch", "/local_tools")
    browser = _narrow_switch(file_tools.browser, session_tools, "browser", "/local_tools")
    return LocalToolsConfig(
        files=files,
        shell=ShellConfig(enabled=shell_enabled, allow=allow),
        web_fetch=web_fetch,
        browser=browser,
    )


def _narrow_switch(
    file_value: bool, session_section: Mapping[str, Any], switch: str, parent_path: str
) -> bool:
    if switch not in session_section:
        return file_value
    session_value = bool(session_section[switch])
    if session_value and not file_value:
        raise ConfigError("widens_local_tools", f"{parent_path}/{switch}")
    return session_value


def _is_ascii_upper(character: str) -> bool:
    return character != "" and "A" <= character <= "Z"


def _is_ascii_lower(character: str) -> bool:
    return character != "" and "a" <= character <= "z"


def _json_pointer(parts: Iterable[object]) -> str:
    return "".join("/" + str(part).replace("~", "~0").replace("/", "~1") for part in parts)
