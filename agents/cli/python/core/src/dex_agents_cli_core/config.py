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

from dex_agents.approval_display import is_credential_like_name

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
            ``schema``, ``invalid_url``, ``insecure_token_url``,
            ``insecure_mcp_server_url``, ``literal_credential_header``,
            ``auth_needs_server_url``, ``tls_needs_https_server``,
            ``duplicate_tool``, ``duplicate_connection``, ``model_required``,
            ``widens_connectors``, ``widens_local_tools``, or
            ``unsupported_reasoning_effort``.
        path: JSON Pointer of the first problem, ``""`` for the whole value.
        keyword: For ``schema``, the JSON Schema keyword of the first error,
            such as ``anyOf``; ``None`` otherwise.
        schema_errors: For ``schema``, the instance location and keyword of
            every schema error, nested ones included; empty otherwise.
        valid_forms: For an ``anyOf`` schema error, the ``title`` of the
            failed schema, which names the values it accepts; ``None`` otherwise.

    Its message is ``<code> at "<pointer>"``, followed by
    `` (schema keyword <keyword>)`` for a schema error, as in the TypeScript CLI core,
    and by ``. Use <valid forms>.`` when the failed schema names them.

    .. versionchanged:: 0.6.0
       An ``anyOf`` error names the values the field accepts.

    .. versionchanged:: 0.6.2
       The ``unsupported_reasoning_effort`` code, at ``/reasoning_effort``.

    .. versionchanged:: 0.6.2
       The ``insecure_token_url`` code, at ``/dex/auth/token_url``, the
       ``auth_needs_server_url`` code, at ``/dex/auth``, and the
       ``tls_needs_https_server`` code, at ``/dex/tls``.

    .. versionchanged:: 0.6.4
       The ``insecure_mcp_server_url`` code, at ``/mcp_servers/<name>/url``,
       and the ``literal_credential_header`` code, at
       ``/mcp_servers/<name>/headers/<header>``.
    """

    def __init__(
        self,
        code: str,
        path: str,
        *,
        keyword: str | None = None,
        schema_errors: frozenset[tuple[str, str]] = frozenset(),
        valid_forms: str | None = None,
    ) -> None:
        """Create the error.

        Args:
            code: The stable error code.
            path: JSON Pointer of the first problem.
            keyword: The failed JSON Schema keyword of a schema error.
            schema_errors: Every schema error's location and keyword.
            valid_forms: What the failed schema accepts, from its ``title``.
        """
        message = f"{code} at {json.dumps(path)}"
        if keyword is not None:
            message = f"{message} (schema keyword {keyword})"
        if valid_forms is not None:
            message = f"{message}. Use {valid_forms}."
        super().__init__(message)
        self.code = code
        self.path = path
        self.keyword = keyword
        self.schema_errors = schema_errors
        self.valid_forms = valid_forms


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
class DexAuthConfig:
    """The ``dex.auth`` section, as the file writes it: how to authenticate to Dex.

    It never holds a secret. ``resolve_dex_connection_settings`` adds the
    environment and checks that every needed setting is there.

    Attributes:
        mode: ``none``, ``api_key``, or ``bearer``.
        token_url: ``api_key`` only: the token endpoint, or ``None`` when the
            file omits it and ``DEX_TOKEN_URL`` must give it.
        server_id: ``api_key`` only: the server ID of the access tokens, or
            ``None`` when the file omits it and ``DEX_SERVER_ID`` must give it.
        source: ``bearer`` only: ``env`` or ``file``; ``None`` otherwise.
        file: ``bearer`` with ``source`` ``file`` only: the token file as
            written, or ``None`` when ``DEX_CREDENTIAL_FILE`` must give it.

    .. versionadded:: 0.6.2
    """

    mode: str = "none"
    token_url: str | None = None
    server_id: str | None = None
    source: str | None = None
    file: str | None = None

    def to_json(self) -> dict[str, Any]:
        """Return the section in the conformance file's effective form.

        Returns:
            The mode and the fields of that mode only, ``null`` when omitted.
        """
        if self.mode == "api_key":
            return {"mode": self.mode, "token_url": self.token_url, "server_id": self.server_id}
        if self.mode == "bearer" and self.source == "file":
            return {"mode": self.mode, "source": "file", "file": self.file}
        if self.mode == "bearer":
            return {"mode": self.mode, "source": "env"}
        return {"mode": "none"}


@dataclass(frozen=True)
class DexTlsConfig:
    """The ``dex.tls`` section, as the file writes it, for an ``https`` ``dex.server``.

    Attributes:
        ca_file: A PEM file of root CAs to trust as well, as written, or ``None``.
        server_name: The name the server certificate must match, or ``None``
            for the ``dex.server`` host.

    .. versionadded:: 0.6.2
    """

    ca_file: str | None = None
    server_name: str | None = None


@dataclass(frozen=True)
class DexServerConfig:
    """The ``dex`` section.

    Attributes:
        server: ``auto`` or the Dex server URL.
        auth: How to authenticate to the Dex server.

            .. versionadded:: 0.6.2
        tls: How to verify an ``https`` Dex server.

            .. versionadded:: 0.6.2
    """

    server: str = AUTO
    auth: DexAuthConfig = field(default_factory=DexAuthConfig)
    tls: DexTlsConfig = field(default_factory=DexTlsConfig)


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
class RemoteMcpServerConfig:
    """One ``mcp_servers`` entry, as the file writes it: a remote MCP server.

    It never holds a secret: each header value keeps its ``${NAME}``
    references. ``resolve_mcp_servers`` expands them from the environment.

    Example:
        >>> RemoteMcpServerConfig(
        ...     url="https://mcp.example.com/mcp",
        ...     headers={"Authorization": "Bearer ${STUDIO_API_KEY}"},
        ... )

    Attributes:
        url: The server's Streamable HTTP endpoint: ``https``, or ``http`` on
            a loopback host.
        headers: The HTTP headers sent with every request, by name, with
            their ``${NAME}`` references unexpanded. Empty by default.
        trust_tool_annotations: Whether the server's annotations and declared
            effects pick each tool's effect. ``False`` by default, so every
            call waits for approval and runs at most once.

    .. versionadded:: 0.6.4
    """

    url: str
    headers: Mapping[str, str] = field(default_factory=dict)
    trust_tool_annotations: bool = False


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
class SessionsConfig:
    """The ``sessions`` section: how the command keeps its sessions.

    Session settings never change it.

    Attributes:
        summary: ``auto``, ``on``, or ``off``: whether each session keeps the
            indexed title, last activity time, and turn count that
            ``sessions`` shows. See ``is_session_summary_indexed``.

    .. versionadded:: 0.6.0
    """

    summary: str = AUTO


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
        sessions: How the command keeps its sessions. Session settings never
            change it.

            .. versionadded:: 0.6.0
        mcp_servers: The remote MCP servers, by name, with their header
            values as written. Empty by default. Session settings never
            change it.

            .. versionadded:: 0.6.4
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
    sessions: SessionsConfig = field(default_factory=SessionsConfig)
    mcp_servers: Mapping[str, RemoteMcpServerConfig] = field(default_factory=dict)

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
            "dex": {
                "server": self.dex.server,
                "auth": self.dex.auth.to_json(),
                "tls": {"ca_file": self.dex.tls.ca_file, "server_name": self.dex.tls.server_name},
            },
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
            "mcp_servers": {
                name: {
                    "url": server.url,
                    "headers": dict(server.headers),
                    "trust_tool_annotations": server.trust_tool_annotations,
                }
                for name, server in self.mcp_servers.items()
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
            "sessions": {"summary": self.sessions.summary},
            "harness_options": json.loads(json.dumps(self.harness_options)),
        }


@dataclass(frozen=True)
class TurnConfigResolution:
    """One turn's configuration after its session settings.

    Attributes:
        config: The turn's configuration.
        warnings: JSON Pointers of the ignored top-level settings keys, sorted.
        reasoning_effort: The session settings' ``reasoning_effort``, one of
            the values the harness command declares, or ``None`` when the
            settings set none. ``None`` means the model's own default: pass
            no reasoning effort to the SDK.

            .. versionadded:: 0.6.2
    """

    config: EffectiveConfig
    warnings: tuple[str, ...] = ()
    reasoning_effort: str | None = None


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
        ConfigError: ``schema``, ``invalid_url``, ``insecure_token_url``,
            ``insecure_mcp_server_url``, ``literal_credential_header``,
            ``auth_needs_server_url``, ``tls_needs_https_server``,
            ``duplicate_tool``, ``duplicate_connection``, or
            ``model_required``, in the conformance file's check order.

    .. versionchanged:: 0.6.2
       Reads ``dex.auth`` and ``dex.tls``.

    .. versionchanged:: 0.6.4
       Reads ``mcp_servers``.
    """
    if config is None:
        config = {}
    _raise_first_schema_error(_CONFIG_VALIDATOR, config)
    assert isinstance(config, Mapping), "the schema admits only objects"
    dex = config.get("dex", {})
    connectors = config.get("connectors", {})
    server = dex.get("server", AUTO)
    auth = dex.get("auth", {"mode": "none"})
    tls = dex.get("tls", {})
    mcp = connectors.get("mcp", AUTO)
    _raise_invalid_url(server, "/dex/server")
    token_url = auth.get("token_url")
    if token_url is not None:
        _raise_invalid_url(token_url, "/dex/auth/token_url")
    _raise_invalid_url(mcp, "/connectors/mcp")
    media = config.get("media", {})
    endpoint = media.get("endpoint")
    if endpoint is not None:
        _raise_invalid_url(endpoint, "/media/endpoint")
    mcp_servers = _read_mcp_servers(config.get("mcp_servers", {}))
    for name in sorted(mcp_servers):
        _raise_invalid_url(mcp_servers[name].url, _json_pointer(["mcp_servers", name, "url"]))
    if token_url is not None and not is_secure_token_url(token_url):
        raise ConfigError("insecure_token_url", "/dex/auth/token_url")
    _raise_unsafe_mcp_servers(mcp_servers)
    if auth["mode"] != "none" and server == AUTO:
        raise ConfigError("auth_needs_server_url", "/dex/auth")
    if tls and not server.startswith("https://"):
        raise ConfigError("tls_needs_https_server", "/dex/tls")
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
        dex=DexServerConfig(
            server=server,
            auth=DexAuthConfig(
                mode=auth["mode"],
                token_url=token_url,
                server_id=auth.get("server_id"),
                source=auth.get("source"),
                file=auth.get("file"),
            ),
            tls=DexTlsConfig(ca_file=tls.get("ca_file"), server_name=tls.get("server_name")),
        ),
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
        sessions=SessionsConfig(summary=config.get("sessions", {}).get("summary", AUTO)),
        mcp_servers=mcp_servers,
    )


def _read_mcp_servers(raw_servers: Mapping[str, Any]) -> dict[str, RemoteMcpServerConfig]:
    return {
        name: RemoteMcpServerConfig(
            url=server["url"],
            headers=dict(server.get("headers", {})),
            trust_tool_annotations=server.get("trust_tool_annotations", False),
        )
        for name, server in raw_servers.items()
    }


def _raise_unsafe_mcp_servers(servers: Mapping[str, RemoteMcpServerConfig]) -> None:
    """Reject an http URL off loopback, then a credential header without a variable."""
    names = sorted(servers)
    for name in names:
        if not is_secure_token_url(servers[name].url):
            raise ConfigError(
                "insecure_mcp_server_url", _json_pointer(["mcp_servers", name, "url"])
            )
    for name in names:
        headers = servers[name].headers
        for header in sorted(headers):
            if is_credential_like_name(header) and "${" not in headers[header]:
                raise ConfigError(
                    "literal_credential_header",
                    _json_pointer(["mcp_servers", name, "headers", header]),
                )


def is_session_summary_indexed(config: EffectiveConfig) -> bool:
    """Return whether the command's sessions keep the indexed session summary.

    ``sessions.summary: auto`` keeps it only with ``dex.server: auto``, the
    ``dexcli dev`` of this machine that the command reuses or starts. A Dex
    server URL may be shared, so its search index and index keys stay
    untouched unless ``sessions.summary`` is ``on``.

    Args:
        config: The file's effective configuration.

    Returns:
        ``True`` to build the session Flow with
        ``AgentSessionOptions.summary_attributes_indexed``.

    .. versionadded:: 0.6.0
    """
    summary = config.sessions.summary
    return summary == "on" or (summary == AUTO and config.dex.server == AUTO)


def resolve_turn_config(
    effective: EffectiveConfig,
    session_settings: object,
    *,
    supported_reasoning_efforts: Sequence[str] = (),
) -> TurnConfigResolution:
    """Return one turn's configuration: the file's, narrowed by the session settings.

    The settings' ``reasoning_effort`` is checked last, after every
    narrowing rule, against the values the harness command declares.

    Example:
        >>> resolution = resolve_turn_config(
        ...     effective, {"reasoning_effort": "high"},
        ...     supported_reasoning_efforts=("low", "medium", "high"))
        >>> resolution.reasoning_effort
        'high'

    .. versionchanged:: 0.6.2
       The ``supported_reasoning_efforts`` argument, the result's
       ``reasoning_effort``, and the ``unsupported_reasoning_effort`` error.

    Args:
        effective: The file's effective configuration, which bounds the turn.
        session_settings: The session's settings, or ``None`` for none.
        supported_reasoning_efforts: The ``reasoning_effort`` values the
            harness command declares, its ``supported_reasoning_efforts``.
            Empty, the default, rejects every ``reasoning_effort``.

    Returns:
        The turn's configuration, the ignored settings keys, and the turn's
        reasoning effort.

    Raises:
        ConfigError: ``schema``, ``duplicate_tool``, ``duplicate_connection``,
            ``widens_connectors``, ``widens_local_tools``, or
            ``unsupported_reasoning_effort`` at ``/reasoning_effort`` for a
            value outside ``supported_reasoning_efforts``. The whole override
            is rejected.
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
    reasoning_effort = session_settings.get("reasoning_effort")
    if reasoning_effort is not None and reasoning_effort not in supported_reasoning_efforts:
        raise ConfigError("unsupported_reasoning_effort", "/reasoning_effort")
    return TurnConfigResolution(replace(turn, local_tools=local_tools), warnings, reasoning_effort)


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
        title = first.schema.get("title") if isinstance(first.schema, dict) else None
        raise ConfigError(
            "schema",
            _json_pointer(first.absolute_path),
            keyword=str(first.validator),
            schema_errors=schema_error_locations(errors),
            valid_forms=title if first.validator == "anyOf" and isinstance(title, str) else None,
        )


def is_secure_token_url(url: str) -> bool:
    """Report whether a token URL that passes the URL checks keeps the API key off the network.

    Args:
        url: An ``http`` or ``https`` URL that passed the schema and the URL checks.

    Returns:
        ``True`` for ``https``, and for ``http`` on ``localhost`` in any case,
        an IPv4 address in 127.0.0.0/8, or ``::1``.

    .. versionadded:: 0.6.2
    """
    if url.startswith("https://"):
        return True
    parts = _URL_PARTS.fullmatch(url)
    assert parts is not None, "the schema admits only URLs of this shape"
    host = parts["host"]
    if host.lower() == "localhost":
        return True
    try:
        address = ipaddress.ip_address(host[1:-1] if host.startswith("[") else host)
    except ValueError:
        return False
    return address.is_loopback


def is_valid_url(url: str) -> bool:
    """Report whether a URL of the schema's URL shape passes the ``invalid_url`` checks.

    Args:
        url: A URL that matches one of the schema's URL patterns.

    Returns:
        ``True`` when a WHATWG URL parser accepts it with a port of at most 65535.

    .. versionadded:: 0.6.2
    """
    try:
        _raise_invalid_url(url, "")
    except ConfigError:
        return False
    return True


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
