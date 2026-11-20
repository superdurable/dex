# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Reads and validates the codec server configuration."""

from __future__ import annotations

import argparse
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import NoReturn
from urllib.parse import urlsplit

from dex import DEFAULT_MAX_PAYLOAD_ENVELOPE_BYTES, DEFAULT_MAX_PAYLOAD_PLAINTEXT_BYTES

AUTH_MODE_BEARER_TOKEN = "bearer-token"
AUTH_MODE_TRUSTED_PROXY_HEADER = "trusted-proxy-header"

# Fits one maximum envelope as base64 (about 22 MiB) plus JSON overhead.
DEFAULT_MAX_REQUEST_BODY_BYTES = 32 * 1024 * 1024
DEFAULT_MAX_ITEMS_PER_REQUEST = 1000
# Caps decompression amplification across the entries of one /decode request.
DEFAULT_MAX_DECODED_BYTES_PER_REQUEST = 64 * 1024 * 1024
DEFAULT_SHUTDOWN_TIMEOUT = "15s"
MINIMUM_BEARER_TOKEN_BYTES = 32

_DURATION_PATTERN = re.compile(r"^(?P<amount>\d+(?:\.\d+)?)(?P<unit>ms|s|m)?$")
_DURATION_UNIT_SECONDS = {"ms": 0.001, "s": 1.0, "m": 60.0, None: 1.0}


class CodecServerConfigurationError(ValueError):
    """The configuration is incomplete or unsafe. The message holds no secrets."""


@dataclass(frozen=True)
class PayloadSizeLimits:
    """The decode and encode limits of ``protos/payload-encryption.md`` section 7."""

    max_envelope_bytes: int
    max_encoded_text_bytes: int
    max_data_bytes: int
    max_plaintext_bytes: int

    @classmethod
    def from_envelope_and_plaintext_limits(
        cls, max_envelope_bytes: int, max_plaintext_bytes: int
    ) -> PayloadSizeLimits:
        """Derive the encoded-text and data limits from the envelope limit, as the spec does."""
        return cls(
            max_envelope_bytes=max_envelope_bytes,
            max_encoded_text_bytes=4 * ((max_envelope_bytes + 2) // 3),
            max_data_bytes=max_envelope_bytes,
            max_plaintext_bytes=max_plaintext_bytes,
        )


@dataclass(frozen=True)
class CodecServerConfig:
    """The validated configuration of one codec server process."""

    listen_host: str
    listen_port: int
    # Exactly one key source is set.
    key_file_path: str
    keys_json: str = field(repr=False)
    allowed_origins: tuple[str, ...]
    allow_credentials: bool
    auth_mode: str
    bearer_token: str = field(repr=False)
    trusted_identity_header: str
    max_request_body_bytes: int
    max_items_per_request: int
    max_decoded_bytes_per_request: int
    payload_size_limits: PayloadSizeLimits
    shutdown_timeout_seconds: float

    def validate(self) -> None:
        """Reject a configuration that is incomplete or unsafe.

        Raises:
            CodecServerConfigurationError: Naming the first problem found.
        """
        if bool(self.key_file_path) == bool(self.keys_json):
            raise CodecServerConfigurationError(
                "set exactly one key source: --key-file (DEX_CODEC_SERVER_KEY_FILE) "
                "or DEX_CODEC_SERVER_KEYS_JSON"
            )
        if not self.allowed_origins:
            raise CodecServerConfigurationError(
                "set --allowed-origins (DEX_CODEC_SERVER_ALLOWED_ORIGINS) to the Dex Web origins"
            )
        if self.auth_mode == AUTH_MODE_BEARER_TOKEN:
            if len(self.bearer_token.encode()) < MINIMUM_BEARER_TOKEN_BYTES:
                raise CodecServerConfigurationError(
                    f"the bearer token must have at least {MINIMUM_BEARER_TOKEN_BYTES} bytes"
                )
        elif self.auth_mode == AUTH_MODE_TRUSTED_PROXY_HEADER:
            if not self.trusted_identity_header:
                raise CodecServerConfigurationError(
                    "trusted-proxy-header mode needs --trusted-identity-header"
                )
        else:
            raise CodecServerConfigurationError(
                f"--auth-mode must be {AUTH_MODE_BEARER_TOKEN} or "
                f"{AUTH_MODE_TRUSTED_PROXY_HEADER}; the codec server never runs without "
                "authentication"
            )
        if (
            self.max_request_body_bytes <= 0
            or self.max_items_per_request <= 0
            or self.max_decoded_bytes_per_request <= 0
        ):
            raise CodecServerConfigurationError(
                "request body, item, and decoded-bytes limits must be positive"
            )
        limits = self.payload_size_limits
        if limits.max_envelope_bytes <= 0 or limits.max_plaintext_bytes <= 0:
            raise CodecServerConfigurationError("envelope and plaintext limits must be positive")
        if self.shutdown_timeout_seconds <= 0:
            raise CodecServerConfigurationError("--shutdown-timeout must be positive")
        if not 0 <= self.listen_port <= 65535:
            raise CodecServerConfigurationError("--listen-address has an invalid port")


class _ConfigurationArgumentParser(argparse.ArgumentParser):
    """Raises instead of exiting, so callers report every configuration error the same way."""

    def error(self, message: str) -> NoReturn:
        raise CodecServerConfigurationError(message)


def load_codec_server_config(
    arguments: Sequence[str], environment: Mapping[str, str]
) -> CodecServerConfig:
    """Read flags that default to ``DEX_CODEC_SERVER_*`` variables, then validate them.

    Secrets come only from the environment or from files, because flags show
    up in process lists.

    Args:
        arguments: Command-line arguments without the program name.
        environment: The process environment.

    Returns:
        The validated configuration.

    Raises:
        CodecServerConfigurationError: When a setting is missing, malformed, or unsafe.
    """

    def setting(name: str, fallback: str) -> str:
        return environment.get(name) or fallback

    parser = _ConfigurationArgumentParser(prog="dex-codec-server", add_help=False)
    parser.add_argument(
        "--listen-address", default=setting("DEX_CODEC_SERVER_LISTEN_ADDRESS", ":8080")
    )
    parser.add_argument("--key-file", default=setting("DEX_CODEC_SERVER_KEY_FILE", ""))
    parser.add_argument(
        "--allowed-origins", default=setting("DEX_CODEC_SERVER_ALLOWED_ORIGINS", "")
    )
    parser.add_argument(
        "--allow-credentials",
        default=setting("DEX_CODEC_SERVER_ALLOW_CREDENTIALS", "false"),
        nargs="?",
        const="true",
    )
    parser.add_argument("--auth-mode", default=setting("DEX_CODEC_SERVER_AUTH_MODE", ""))
    parser.add_argument(
        "--bearer-token-file", default=setting("DEX_CODEC_SERVER_BEARER_TOKEN_FILE", "")
    )
    parser.add_argument(
        "--trusted-identity-header",
        default=setting("DEX_CODEC_SERVER_TRUSTED_IDENTITY_HEADER", ""),
    )
    integer_flags = {
        "--max-request-body-bytes": (
            "DEX_CODEC_SERVER_MAX_REQUEST_BODY_BYTES",
            DEFAULT_MAX_REQUEST_BODY_BYTES,
        ),
        "--max-items-per-request": (
            "DEX_CODEC_SERVER_MAX_ITEMS_PER_REQUEST",
            DEFAULT_MAX_ITEMS_PER_REQUEST,
        ),
        "--max-decoded-bytes-per-request": (
            "DEX_CODEC_SERVER_MAX_DECODED_BYTES_PER_REQUEST",
            DEFAULT_MAX_DECODED_BYTES_PER_REQUEST,
        ),
        "--max-envelope-bytes": (
            "DEX_CODEC_SERVER_MAX_ENVELOPE_BYTES",
            DEFAULT_MAX_PAYLOAD_ENVELOPE_BYTES,
        ),
        "--max-plaintext-bytes": (
            "DEX_CODEC_SERVER_MAX_PLAINTEXT_BYTES",
            DEFAULT_MAX_PAYLOAD_PLAINTEXT_BYTES,
        ),
    }
    for flag, (variable, fallback) in integer_flags.items():
        parser.add_argument(flag, default=setting(variable, str(fallback)))
    parser.add_argument(
        "--shutdown-timeout",
        default=setting("DEX_CODEC_SERVER_SHUTDOWN_TIMEOUT", DEFAULT_SHUTDOWN_TIMEOUT),
    )
    parsed = parser.parse_args(list(arguments))

    integers = {
        flag: _parse_integer_setting(flag, getattr(parsed, flag[2:].replace("-", "_")))
        for flag in integer_flags
    }
    listen_host, listen_port = _parse_listen_address(parsed.listen_address)
    auth_mode = parsed.auth_mode.strip()
    bearer_token = ""
    if auth_mode == AUTH_MODE_BEARER_TOKEN:
        bearer_token = _read_bearer_token(
            environment.get("DEX_CODEC_SERVER_BEARER_TOKEN", ""), parsed.bearer_token_file
        )
    config = CodecServerConfig(
        listen_host=listen_host,
        listen_port=listen_port,
        key_file_path=parsed.key_file.strip(),
        keys_json=environment.get("DEX_CODEC_SERVER_KEYS_JSON", ""),
        allowed_origins=parse_allowed_origins(parsed.allowed_origins),
        allow_credentials=_parse_boolean_setting("--allow-credentials", parsed.allow_credentials),
        auth_mode=auth_mode,
        bearer_token=bearer_token,
        trusted_identity_header=parsed.trusted_identity_header.strip(),
        max_request_body_bytes=integers["--max-request-body-bytes"],
        max_items_per_request=integers["--max-items-per-request"],
        max_decoded_bytes_per_request=integers["--max-decoded-bytes-per-request"],
        payload_size_limits=PayloadSizeLimits.from_envelope_and_plaintext_limits(
            integers["--max-envelope-bytes"], integers["--max-plaintext-bytes"]
        ),
        shutdown_timeout_seconds=_parse_duration_seconds(
            "--shutdown-timeout", parsed.shutdown_timeout
        ),
    )
    config.validate()
    return config


def parse_allowed_origins(comma_separated: str) -> tuple[str, ...]:
    """Accept only exact ``scheme://host[:port]`` origins; reject wildcards.

    Args:
        comma_separated: Origins separated by commas.

    Returns:
        The origins in lowercase, in order.

    Raises:
        CodecServerConfigurationError: For a wildcard or a value that is not an origin.
    """
    origins: list[str] = []
    for entry in (part.strip() for part in comma_separated.split(",")):
        if not entry:
            continue
        if "*" in entry:
            raise CodecServerConfigurationError(
                f"allowed origin {entry!r}: wildcards are not allowed; list each Dex Web origin"
            )
        try:
            parts = urlsplit(entry)
            # Reading the port validates it.
            has_valid_port = parts.port is None or parts.port >= 0
        except ValueError:
            raise CodecServerConfigurationError(
                f"allowed origin {entry!r} must look like https://dex-web.example.com"
            ) from None
        if (
            not has_valid_port
            or parts.scheme not in ("http", "https")
            or not parts.hostname
            or parts.path not in ("", "/")
            or parts.query
            or parts.fragment
            or parts.username is not None
            or parts.password is not None
        ):
            raise CodecServerConfigurationError(
                f"allowed origin {entry!r} must look like https://dex-web.example.com"
            )
        origins.append(f"{parts.scheme}://{parts.netloc}".lower())
    return tuple(origins)


def _parse_listen_address(listen_address: str) -> tuple[str, int]:
    host, separator, port_text = listen_address.strip().rpartition(":")
    if not separator or not port_text.isdigit():
        raise CodecServerConfigurationError(
            "--listen-address must look like :8080 or 127.0.0.1:8080"
        )
    return host.strip("[]") or "0.0.0.0", int(port_text)


def _parse_integer_setting(flag: str, text: str) -> int:
    try:
        return int(text)
    except ValueError:
        raise CodecServerConfigurationError(f"{flag} is not an integer") from None


def _parse_boolean_setting(flag: str, text: str) -> bool:
    lowered = text.strip().lower()
    if lowered in ("1", "true", "yes"):
        return True
    if lowered in ("0", "false", "no"):
        return False
    raise CodecServerConfigurationError(f"{flag} is not a boolean")


def _parse_duration_seconds(flag: str, text: str) -> float:
    match = _DURATION_PATTERN.match(text.strip())
    if match is None:
        raise CodecServerConfigurationError(f"{flag} must look like 15s, 500ms, or 1m")
    return float(match["amount"]) * _DURATION_UNIT_SECONDS[match["unit"]]


def _read_bearer_token(environment_token: str, token_file_path: str) -> str:
    if bool(environment_token) == bool(token_file_path):
        raise CodecServerConfigurationError(
            "bearer-token mode needs exactly one of DEX_CODEC_SERVER_BEARER_TOKEN or "
            "--bearer-token-file"
        )
    if environment_token:
        return environment_token
    try:
        return Path(token_file_path).read_text(encoding="utf-8").strip()
    except OSError as error:
        raise CodecServerConfigurationError(
            f"cannot read the bearer token file: {error.strerror}"
        ) from None
