# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""How a command connects to its Dex server: TLS and the credential.

The rules are the ``dex_connection`` cases of
``agents/conformance/cli_config.json``.

.. versionadded:: 0.6.3
"""

from __future__ import annotations

import posixpath
import re
from collections.abc import Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from dex import (
    ApiKeyCredential,
    ApiKeyExchangeError,
    ClientOptions,
    Credential,
    FileCredential,
    FlowServiceTlsOptions,
    PullWorkerOptions,
    WorkerOptions,
)

from dex_agents_cli_core.config import (
    CONFIG_SCHEMA,
    EffectiveConfig,
    is_secure_token_url,
    is_valid_url,
)

_TOKEN_URL_PATTERN = re.compile(
    CONFIG_SCHEMA["properties"]["dex"]["properties"]["auth"]["properties"]["token_url"]["pattern"]
)
_UNAUTHENTICATED = "UNAUTHENTICATED"
_PERMISSION_DENIED = "PERMISSION_DENIED"


class DexConnectionSettingsError(Exception):
    """Report a missing or invalid Dex connection setting.

    The command prints the message and exits with code 2. The message names
    the setting to fix and never holds a key or a token.

    Attributes:
        setting: The one setting to fix: a configuration field such as
            ``dex.auth.token_url``, or a variable such as ``DEX_API_KEY``.
        message: The exact English sentence that the conformance file fixes.

    .. versionadded:: 0.6.3
    """

    def __init__(self, setting: str, message: str) -> None:
        """Create the error.

        Args:
            setting: The setting to fix.
            message: The sentence to print.
        """
        super().__init__(message)
        self.setting = setting
        self.message = message


@dataclass(frozen=True)
class DexConnectionSettings:
    """The resolved Dex connection settings of a command, without any secret.

    ``auth`` has the shape of the terminal UI launch document's ``dex.auth``,
    plus the token file's absolute path in ``file`` for ``bearer`` with
    ``source`` ``file``. The secret stays in the environment: ``DEX_API_KEY``
    or ``DEX_CREDENTIAL``.

    Attributes:
        is_tls: Whether ``dex.server`` is an ``https`` URL.
        ca_file: The absolute path of the PEM file of added root CAs, or ``None``.
        server_name: The name the server certificate must match, or ``None``.
        auth: ``{"mode": "none"}``, ``{"mode": "api_key", "token_url": ...,
            "server_id": ...}``, ``{"mode": "bearer", "source": "env"}``, or
            ``{"mode": "bearer", "source": "file", "file": ...}``.

    .. versionadded:: 0.6.3
    """

    is_tls: bool = False
    ca_file: str | None = None
    server_name: str | None = None
    auth: Mapping[str, Any] = field(default_factory=lambda: {"mode": "none"})

    @property
    def mode(self) -> str:
        """Return ``none``, ``api_key``, or ``bearer``."""
        return str(self.auth["mode"])

    def to_json(self) -> dict[str, Any]:
        """Return the settings in the conformance file's ``settings`` shape.

        Returns:
            ``{"tls": {"enabled", "ca_file", "server_name"}, "auth": {...}}``.
        """
        return {
            "tls": self.launch_tls(),
            "auth": dict(self.auth),
        }

    @classmethod
    def from_json(cls, value: Mapping[str, Any]) -> DexConnectionSettings:
        """Read the settings that ``to_json`` returned.

        Args:
            value: The JSON object.

        Returns:
            The settings.
        """
        tls = value["tls"]
        return cls(
            is_tls=bool(tls["enabled"]),
            ca_file=tls["ca_file"],
            server_name=tls["server_name"],
            auth=dict(value["auth"]),
        )

    def launch_tls(self) -> dict[str, Any]:
        """Return the terminal UI launch document's ``dex.tls``."""
        return {"enabled": self.is_tls, "ca_file": self.ca_file, "server_name": self.server_name}

    def launch_auth(self) -> dict[str, Any]:
        """Return the terminal UI launch document's ``dex.auth``: ``auth`` without ``file``."""
        return {name: value for name, value in self.auth.items() if name != "file"}

    def terminal_ui_auth_variables(self, variables: Mapping[str, str]) -> dict[str, str]:
        """Return the one auth variable the terminal UI needs, with its value.

        Args:
            variables: The command's trimmed Dex connection variables.

        Returns:
            ``DEX_API_KEY`` or ``DEX_CREDENTIAL`` with the trimmed secret,
            ``DEX_CREDENTIAL_FILE`` with the resolved path, or nothing.
        """
        if self.mode == "api_key":
            return {"DEX_API_KEY": variables["DEX_API_KEY"]}
        if self.mode == "bearer" and self.auth["source"] == "env":
            return {"DEX_CREDENTIAL": variables["DEX_CREDENTIAL"]}
        if self.mode == "bearer":
            return {"DEX_CREDENTIAL_FILE": str(self.auth["file"])}
        return {}


def resolve_dex_connection_settings(
    config: EffectiveConfig,
    variables: Mapping[str, str],
    *,
    config_directory: Path,
    home: Path,
    working_directory: Path,
) -> DexConnectionSettings:
    """Return how a command connects to its Dex server, from the file and the environment.

    Only ``dex.auth.mode`` turns authentication on; the variables never do. A
    variable overrides the matching field of the selected mode:
    ``DEX_TOKEN_URL``, ``DEX_SERVER_ID``, and ``DEX_CREDENTIAL_FILE``.
    ``DEX_TLS_CA_FILE`` and ``DEX_TLS_SERVER_NAME`` override ``dex.tls``, and
    are read only for an ``https`` ``dex.server``. A path from the file
    expands a leading ``~`` and is relative to the file's directory; a path
    from a variable is relative to the working directory. Paths are
    normalized lexically, without reading the file system. The secret
    variables are checked to be set, and never copied into the result.

    Example:
        >>> settings = resolve_dex_connection_settings(
        ...     loaded.config,
        ...     environment.dex_connection_variables,
        ...     config_directory=loaded.directory,
        ...     home=environment.home,
        ...     working_directory=Path.cwd(),
        ... )
        >>> settings.to_json()["auth"]["mode"]
        'api_key'

    Args:
        config: The effective configuration, which passed ``resolve_config``.
        variables: The trimmed, non-empty Dex connection variables, such as
            ``CommandEnvironment.dex_connection_variables``.
        config_directory: The directory of the configuration file.
        home: The home directory, which a leading ``~`` means.
        working_directory: The directory a relative variable path is relative to.

    Returns:
        The settings.

    Raises:
        DexConnectionSettingsError: The first missing or invalid setting: for
            ``api_key`` the token URL, the server ID, then ``DEX_API_KEY``;
            for ``bearer`` the token or the token file.

    .. versionadded:: 0.6.3
    """
    trimmed = {name: value.strip() for name, value in variables.items() if value.strip()}
    dex = config.dex
    is_tls = dex.server.startswith("https://")

    def from_file(path: str | None) -> str | None:
        return None if path is None else _expand_path(path, str(config_directory), str(home))

    def from_variable(name: str) -> str | None:
        value = trimmed.get(name)
        if value is None:
            return None
        return _expand_path(value, str(working_directory), str(home))

    ca_file: str | None = None
    server_name: str | None = None
    if is_tls:
        ca_file = from_variable("DEX_TLS_CA_FILE") or from_file(dex.tls.ca_file)
        server_name = trimmed.get("DEX_TLS_SERVER_NAME") or dex.tls.server_name
    auth = dex.auth
    resolved_auth: dict[str, Any]
    if auth.mode == "api_key":
        resolved_auth = _resolve_api_key(trimmed, auth.token_url, auth.server_id)
    elif auth.mode == "bearer" and auth.source == "env":
        if "DEX_CREDENTIAL" not in trimmed:
            raise DexConnectionSettingsError(
                "DEX_CREDENTIAL",
                "DEX_CREDENTIAL is not set; dex.auth mode bearer with source env needs it.",
            )
        resolved_auth = {"mode": "bearer", "source": "env"}
    elif auth.mode == "bearer":
        token_file = from_variable("DEX_CREDENTIAL_FILE") or from_file(auth.file)
        if token_file is None:
            raise DexConnectionSettingsError(
                "dex.auth.file",
                "dex.auth mode bearer with source file needs a token file; set dex.auth.file "
                "or DEX_CREDENTIAL_FILE.",
            )
        resolved_auth = {"mode": "bearer", "source": "file", "file": token_file}
    else:
        resolved_auth = {"mode": "none"}
    return DexConnectionSettings(is_tls, ca_file, server_name, resolved_auth)


def _resolve_api_key(
    variables: Mapping[str, str], file_token_url: str | None, file_server_id: str | None
) -> dict[str, Any]:
    token_url = variables.get("DEX_TOKEN_URL")
    if token_url is not None:
        if _TOKEN_URL_PATTERN.match(token_url) is None or not is_valid_url(token_url):
            raise DexConnectionSettingsError(
                "DEX_TOKEN_URL",
                "DEX_TOKEN_URL must be an http or https URL without user info or a fragment.",
            )
        if not is_secure_token_url(token_url):
            raise DexConnectionSettingsError(
                "DEX_TOKEN_URL",
                "DEX_TOKEN_URL must use https; http is allowed only on localhost, 127.0.0.0/8, "
                "and [::1].",
            )
    token_url = token_url or file_token_url
    if token_url is None:
        raise DexConnectionSettingsError(
            "dex.auth.token_url",
            "dex.auth mode api_key needs a token URL; set dex.auth.token_url or DEX_TOKEN_URL.",
        )
    server_id = variables.get("DEX_SERVER_ID") or file_server_id
    if server_id is None:
        raise DexConnectionSettingsError(
            "dex.auth.server_id",
            "dex.auth mode api_key needs a server ID; set dex.auth.server_id or DEX_SERVER_ID.",
        )
    if "DEX_API_KEY" not in variables:
        raise DexConnectionSettingsError(
            "DEX_API_KEY", "DEX_API_KEY is not set; dex.auth mode api_key needs it."
        )
    return {"mode": "api_key", "token_url": token_url, "server_id": server_id}


def _expand_path(path: str, base_directory: str, home: str) -> str:
    if path == "~" or path.startswith("~/"):
        path = home + path[1:]
    return posixpath.normpath(posixpath.join(base_directory, path))


class _EnvironmentBearerCredential:
    """A bearer token from ``DEX_CREDENTIAL``; formatting it never shows the token."""

    def __init__(self, token: str) -> None:
        self._token = token

    def token(self) -> str:
        return self._token

    def __repr__(self) -> str:
        return "_EnvironmentBearerCredential(DEX_CREDENTIAL)"


class DexConnection:
    """The TLS options and the credential that every Client and Worker of a command shares.

    Open it once per process after ``resolve_dex_connection_settings``,
    build the Client and Worker options from it, and close it after every
    Client and Worker closed.

    Example:
        >>> connection = DexConnection.open(settings, environment.dex_connection_variables)
        >>> client = AsyncClient(registry, cache, connection.client_options(address))
        >>> ...
        >>> await client.close()
        >>> connection.close()

    Attributes:
        settings: The resolved settings.

    .. versionadded:: 0.6.3
    """

    def __init__(
        self,
        settings: DexConnectionSettings,
        tls: FlowServiceTlsOptions | None,
        credential: Credential | None,
    ) -> None:
        """Hold opened options; ``open`` builds them.

        Args:
            settings: The resolved settings.
            tls: The TLS options, or ``None`` for plaintext.
            credential: The credential, or ``None`` for none.
        """
        self.settings = settings
        self._tls = tls
        self._credential = credential

    @classmethod
    def open(cls, settings: DexConnectionSettings, variables: Mapping[str, str]) -> DexConnection:
        """Read the CA file and the token file, and create the credential.

        Args:
            settings: The resolved settings.
            variables: The trimmed Dex connection variables, which hold the
                secret that ``settings`` names.

        Returns:
            The connection.

        Raises:
            DexConnectionSettingsError: A file cannot be read or is empty, or
                the secret is not set.
        """
        tls: FlowServiceTlsOptions | None = None
        if settings.is_tls:
            tls = FlowServiceTlsOptions(
                additional_root_cas_pem=_read_ca_file(settings.ca_file),
                server_name=settings.server_name,
            )
        return cls(settings, tls, _open_credential(settings, variables))

    def client_options(self, server_address: str) -> ClientOptions:
        """Return the options of a Client of this server.

        Args:
            server_address: The ``host:port`` of the Dex server.

        Returns:
            The options with the TLS settings and the credential.
        """
        return ClientOptions(
            server_address=server_address, flow_service_tls=self._tls, credential=self._credential
        )

    def worker_options(self, server_address: str, pull: PullWorkerOptions) -> WorkerOptions:
        """Return the options of a pull Worker of this server.

        Args:
            server_address: The ``host:port`` of the Dex server.
            pull: The pull options.

        Returns:
            The options with the TLS settings and the credential.
        """
        return WorkerOptions(
            server_address=server_address,
            flow_service_tls=self._tls,
            credential=self._credential,
            pull=pull,
        )

    def describe_setup_error(self, error: ValueError) -> str:
        """Return the startup line for a Client or Worker that rejected the TLS settings.

        Args:
            error: The ``ValueError`` the Client or Worker constructor raised.

        Returns:
            One line that names the setting.
        """
        if self.settings.server_name is not None and "server_name" in str(error):
            return (
                "dex.tls.server_name or DEX_TLS_SERVER_NAME is not a valid TLS server name: "
                f"{error}"
            )
        return f"the TLS settings of dex.tls cannot be used: {error}"

    def close(self) -> None:
        """Close the credential; call it after every Client and Worker closed."""
        if isinstance(self._credential, ApiKeyCredential):
            self._credential.close()


def _read_ca_file(path: str | None) -> bytes | None:
    if path is None:
        return None
    try:
        return Path(path).read_bytes()
    except OSError as error:
        raise DexConnectionSettingsError(
            "dex.tls.ca_file",
            f"cannot read the CA file {path} that dex.tls.ca_file or DEX_TLS_CA_FILE names: "
            f"{error.strerror or error}",
        ) from error


def _open_credential(
    settings: DexConnectionSettings, variables: Mapping[str, str]
) -> Credential | None:
    auth = settings.auth
    if settings.mode == "api_key":
        api_key = variables.get("DEX_API_KEY", "").strip()
        if not api_key:
            raise DexConnectionSettingsError(
                "DEX_API_KEY", "DEX_API_KEY is not set; dex.auth mode api_key needs it."
            )
        return ApiKeyCredential(str(auth["token_url"]), str(auth["server_id"]), api_key)
    if settings.mode == "bearer" and auth["source"] == "env":
        token = variables.get("DEX_CREDENTIAL", "").strip()
        if not token:
            raise DexConnectionSettingsError(
                "DEX_CREDENTIAL",
                "DEX_CREDENTIAL is not set; dex.auth mode bearer with source env needs it.",
            )
        return _EnvironmentBearerCredential(token)
    if settings.mode == "bearer":
        path = str(auth["file"])
        try:
            return FileCredential(path)
        except ValueError as error:
            raise DexConnectionSettingsError(
                "dex.auth.file",
                f"the bearer token file {path} that dex.auth.file or DEX_CREDENTIAL_FILE names "
                "is empty",
            ) from error
        except (OSError, UnicodeError) as error:
            reason = error.strerror if isinstance(error, OSError) and error.strerror else error
            raise DexConnectionSettingsError(
                "dex.auth.file",
                f"cannot read the bearer token file {path} that dex.auth.file or "
                f"DEX_CREDENTIAL_FILE names: {reason}",
            ) from error
    return None


def describe_rejected_dex_credential(
    failure: BaseException, settings: DexConnectionSettings, server_address: str
) -> str | None:
    """Return the one-line error for a credential that the token endpoint or Dex rejected.

    It looks through the cause chain for the SDK's ``ApiKeyExchangeError``,
    then for a gRPC ``UNAUTHENTICATED`` or ``PERMISSION_DENIED``. Check it
    before an ``UNAVAILABLE``, which a failed token lookup can also cause.

    Args:
        failure: The failure of a Worker start or a Client call.
        settings: The command's connection settings.
        server_address: The ``host:port`` of the Dex server.

    Returns:
        The line, which names the setting to check and never holds a secret;
        ``None`` when no rejected credential caused the failure.

    .. versionadded:: 0.6.3
    """
    chain = _cause_chain(failure)
    for error in chain:
        if isinstance(error, ApiKeyExchangeError):
            return (
                f"could not exchange DEX_API_KEY for an access token: {error}. Check "
                "DEX_API_KEY, dex.auth.token_url, and dex.auth.server_id."
            )
    for error in chain:
        status = _grpc_status(error)
        if status is None:
            continue
        name, details = status
        what, hint = _rejected_credential_wording(settings)
        if name == _UNAUTHENTICATED:
            return (
                f"the Dex server at {server_address} rejected {what} "
                f"(UNAUTHENTICATED: {details}). {hint}"
            )
        if name == _PERMISSION_DENIED:
            return (
                f"the Dex server at {server_address} denied {what} "
                f"(PERMISSION_DENIED: {details}). Use a credential that may run Workers and "
                "start Flows."
            )
    return None


def _rejected_credential_wording(settings: DexConnectionSettings) -> tuple[str, str]:
    if settings.mode == "api_key":
        return "the access token for DEX_API_KEY", "Check DEX_API_KEY and dex.auth.server_id."
    if settings.mode == "bearer" and settings.auth["source"] == "env":
        return "the bearer token in DEX_CREDENTIAL", "Check DEX_CREDENTIAL."
    if settings.mode == "bearer":
        return (
            f"the bearer token in {settings.auth['file']}",
            "Check that file, or DEX_CREDENTIAL_FILE.",
        )
    return "the request without a credential", "Set dex.auth in the configuration file."


def _cause_chain(failure: BaseException) -> list[BaseException]:
    chain: list[BaseException] = []
    seen: set[int] = set()
    current: BaseException | None = failure
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        chain.append(current)
        current = current.__cause__ or current.__context__
    return chain


def _grpc_status(error: BaseException) -> tuple[str, str] | None:
    """Return the status name and details of a gRPC error or a Dex service error, by shape."""
    code = getattr(error, "code", None)
    if callable(code):
        code = code()
    name = getattr(code, "name", None)
    if name not in (_UNAUTHENTICATED, _PERMISSION_DENIED):
        return None
    details = getattr(error, "details", None)
    if callable(details):
        details = details()
    if not isinstance(details, str) or not details:
        details = getattr(error, "message", None) or str(error)
    return str(name), str(details)
