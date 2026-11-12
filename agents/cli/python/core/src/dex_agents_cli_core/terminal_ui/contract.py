# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The launcher's rules of terminal UI contract 1, from ``agents/cli/TERMINAL_UI_CONTRACT.md``.

Every rule that the shared conformance cases fix lives here, so a contract
change touches one module: framing, keys and derived session IDs, the
``--describe`` choice, the environment allow-list, ``open_url``, and notices.
"""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from functools import cache
from importlib import resources
from typing import Any, Final
from urllib.parse import urlsplit

from jsonschema import Draft202012Validator

#: The contract versions this launcher speaks, highest first.
LAUNCHER_CONTRACTS: Final = (1,)
#: The first dexcli release whose ``agents-tui`` speaks contract 1.
TERMINAL_UI_MINIMUM_DEXCLI_VERSION: Final = "1.46.0"
#: The longest channel line, in bytes of UTF-8 without the newline.
MAX_LINE_BYTES: Final = 1_048_576
#: How long ``dexcli agents-tui --describe`` may take.
DESCRIBE_TIMEOUT_SECONDS: Final = 5.0
#: How long the terminal UI may take, from its spawn, to send ``ready``.
READY_TIMEOUT_SECONDS: Final = 5.0
#: How many requests other than ``get_request_result`` may wait at once.
MAX_IN_FLIGHT_REQUESTS: Final = 4
#: How many idempotency keys the launcher side remembers.
REMEMBERED_KEY_LIMIT: Final = 256
#: How long the launcher side remembers each key's outcome.
REMEMBERED_KEY_SECONDS: Final = 600.0
#: The hex digits of the session ID suffixes ``start_session`` tries, in order.
SESSION_ID_SUFFIX_LENGTHS: Final = (6, 12)
#: The longest notice text, in code points.
NOTICE_MAX_CODE_POINTS: Final = 500

PUT_IMAGE: Final = "put_image"
START_SESSION: Final = "start_session"
LIST_CONNECTORS: Final = "list_connectors"
OPEN_URL: Final = "open_url"
GET_REQUEST_RESULT: Final = "get_request_result"
#: The methods a chat launch document lists, in the documented order.
CHAT_METHODS: Final = (PUT_IMAGE, START_SESSION, LIST_CONNECTORS, OPEN_URL, GET_REQUEST_RESULT)

#: The variables the terminal UI gets besides the one auth variable.
TERMINAL_ENVIRONMENT_NAMES: Final = (
    "HOME",
    "TERM",
    "COLORTERM",
    "TERM_PROGRAM",
    "NO_COLOR",
    "LANG",
    "LC_ALL",
    "LC_CTYPE",
    "TZ",
    "DEX_AGENTS_TUI_DEBUG",
)
#: The variables that may hold the secret ``dex.auth`` names.
AUTH_ENVIRONMENT_NAMES: Final = ("DEX_CREDENTIAL", "DEX_CREDENTIAL_FILE", "DEX_API_KEY")

_UUID_V7 = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}")
# Keys carry a UUIDv7 time between 2000-01-01T00:00:00Z and 9999-12-31T23:59:59.999Z.
_MIN_KEY_TIME_MS: Final = 946_684_800_000
_MAX_KEY_TIME_MS: Final = 253_402_300_799_999
_DEFAULT_PORTS: Final = {"http": 80, "https": 443}
_AUTHORITY = re.compile(
    r"(?P<host>\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?)(?::(?P<port>[^:]*))?"
)
_PORT = re.compile(r"[1-9][0-9]{0,4}")


class ChannelProtocolError(Exception):
    """A channel line breaks the framing rules, which ends the contract."""


class IdempotencyKeyError(ValueError):
    """An idempotency key is not a lowercase UUIDv7 with a time in the allowed years."""


class DescribeOutputError(ValueError):
    """``dexcli agents-tui --describe`` printed something other than one valid document."""


@dataclass(frozen=True)
class ContractChoice:
    """The contract to hand off with, or why the command uses the plain chat.

    Attributes:
        contract: The contract version, or ``None`` for the plain chat.
        plain_reason: ``no_common_contract`` or ``mode_missing`` when ``contract`` is ``None``.
    """

    contract: int | None
    plain_reason: str | None


def encode_message(message: Mapping[str, Any]) -> bytes:
    """Return one message as a compact UTF-8 JSON line with its newline.

    Raises:
        ChannelProtocolError: If the line is longer than ``MAX_LINE_BYTES``.
    """
    line = json.dumps(message, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if len(line) > MAX_LINE_BYTES:
        raise ChannelProtocolError(f"a {len(line)}-byte message is over the line limit")
    return line + b"\n"


def parse_message_line(line: bytes) -> dict[str, Any]:
    """Parse one line, without its newline, into a message.

    Raises:
        ChannelProtocolError: If the line is not a UTF-8 JSON object with a string ``type``.
    """
    try:
        message = json.loads(line.decode("utf-8"))
    except (UnicodeDecodeError, ValueError) as error:
        raise ChannelProtocolError("a channel line is not UTF-8 JSON") from error
    if not isinstance(message, dict) or not isinstance(message.get("type"), str):
        raise ChannelProtocolError("a channel line is not a JSON object with a string type")
    return message


def idempotency_key_time_ms(idempotency_key: object) -> int:
    """Return the Unix time in milliseconds that a valid key carries.

    Raises:
        IdempotencyKeyError: If the key is not a lowercase hyphenated UUIDv7
            whose time lies in the years 2000 to 9999.
    """
    if not isinstance(idempotency_key, str) or _UUID_V7.fullmatch(idempotency_key) is None:
        raise IdempotencyKeyError("the idempotency key is not a lowercase UUIDv7")
    time_ms = int(idempotency_key[0:8] + idempotency_key[9:13], 16)
    if not _MIN_KEY_TIME_MS <= time_ms <= _MAX_KEY_TIME_MS:
        raise IdempotencyKeyError("the idempotency key carries a time outside the allowed years")
    return time_ms


def derive_session_id(harness: str, idempotency_key: str, suffix_length: int = 6) -> str:
    """Return a session ID ``start_session`` derives from a key.

    Returns:
        ``<harness>-<YYYYMMDD>-<HHMMSS>-<suffix>``: the key's UTC time floored
        to the second, then the first ``suffix_length`` hex digits of SHA-256
        over the key's text.

    Raises:
        IdempotencyKeyError: If the key is invalid.
    """
    time_ms = idempotency_key_time_ms(idempotency_key)
    started = datetime.fromtimestamp(time_ms // 1000, tz=UTC)
    digest = hashlib.sha256(idempotency_key.encode("ascii")).hexdigest()
    return f"{harness}-{started:%Y%m%d-%H%M%S}-{digest[:suffix_length]}"


def candidate_session_ids(harness: str, idempotency_key: str) -> tuple[str, ...]:
    """Return the session IDs ``start_session`` tries for a key, in order: 6, then 12 hex digits.

    Raises:
        IdempotencyKeyError: If the key is invalid.
    """
    return tuple(
        derive_session_id(harness, idempotency_key, length) for length in SESSION_ID_SUFFIX_LENGTHS
    )


def invalid_params_message(method: str, params: Mapping[str, Any]) -> str | None:
    """Return why a known method's params are invalid, or ``None`` when they are valid."""
    match method:
        case "put_image":
            path = params.get("path")
            if not isinstance(path, str) or not path.startswith("/"):
                return "put_image needs the absolute path of a file."
        case "open_url":
            if not isinstance(params.get("url"), str):
                return "open_url needs a url."
        case "get_request_result":
            try:
                idempotency_key_time_ms(params.get("idempotency_key"))
            except IdempotencyKeyError:
                return "The idempotency key is not a lowercase UUIDv7."
    return None


def parse_describe_output(stdout: bytes) -> dict[str, Any]:
    """Return the ``--describe`` document that ``dexcli agents-tui --describe`` printed.

    Raises:
        DescribeOutputError: If the output is over 1 MiB, not one UTF-8 JSON
            object, or fails ``terminal-ui-describe.schema.json``.
    """
    if len(stdout) > MAX_LINE_BYTES + 1:
        raise DescribeOutputError("the output is over 1 MiB")
    try:
        document = json.loads(stdout.decode("utf-8"))
    except (UnicodeDecodeError, ValueError) as error:
        raise DescribeOutputError("the output is not one UTF-8 JSON object") from error
    if not isinstance(document, dict):
        raise DescribeOutputError("the output is not a JSON object")
    violation = next(iter(_describe_validator().iter_errors(document)), None)
    if violation is not None:
        raise DescribeOutputError(f"the output fails the describe schema: {violation.message}")
    return document


def choose_contract(
    describe: Mapping[str, Any],
    mode: str,
    launcher_contracts: Sequence[int] = LAUNCHER_CONTRACTS,
) -> ContractChoice:
    """Return the highest contract both sides speak whose modes include ``mode``.

    Args:
        describe: A document that ``parse_describe_output`` accepted.
        mode: The mode the subcommand needs, such as ``chat``.
        launcher_contracts: The versions the launcher speaks.

    Returns:
        The choice; its plain reason is ``no_common_contract`` or ``mode_missing``.
    """
    described: Mapping[str, Any] = describe["contracts"]
    common = sorted({int(version) for version in described} & set(launcher_contracts))
    if not common:
        return ContractChoice(None, "no_common_contract")
    for version in reversed(common):
        if mode in described[str(version)]["modes"]:
            return ContractChoice(version, None)
    return ContractChoice(None, "mode_missing")


def required_auth_variable(auth: Mapping[str, Any]) -> str | None:
    """Return the one variable that holds the secret ``dex.auth`` names, or ``None``."""
    match auth.get("mode"):
        case "bearer":
            return "DEX_CREDENTIAL_FILE" if auth.get("source") == "file" else "DEX_CREDENTIAL"
        case "api_key":
            return "DEX_API_KEY"
        case _:
            return None


def build_terminal_ui_environment(
    variables: Mapping[str, str], auth: Mapping[str, Any]
) -> dict[str, str]:
    """Return the terminal UI's environment: the allow-listed names the launcher has.

    Args:
        variables: The launcher's variables.
        auth: The launch document's ``dex.auth``.

    Returns:
        The terminal and locale variables, and the one auth variable ``auth`` names.
    """
    names = list(TERMINAL_ENVIRONMENT_NAMES)
    auth_variable = required_auth_variable(auth)
    if auth_variable is not None:
        names.append(auth_variable)
    return {name: variables[name] for name in names if name in variables}


def is_open_url_allowed(web_url: str | None, url: str) -> bool:
    """Return whether ``open_url`` may open ``url`` for a launch document's ``web_url``."""
    if web_url is None or not _has_only_printable_ascii(url):
        return False
    base_origin = _url_origin(web_url)
    if base_origin is None or _url_origin(url) != base_origin:
        return False
    target_path = urlsplit(url).path
    if any(_is_dot_segment(segment) for segment in target_path.split("/")):
        return False
    base_path = urlsplit(web_url).path.rstrip("/")
    return not base_path or target_path == base_path or target_path.startswith(base_path + "/")


def cut_notice_text(text: str) -> str:
    """Return a notice text cut to 500 code points: the first 499 and ``…``."""
    if len(text) > NOTICE_MAX_CODE_POINTS:
        return text[: NOTICE_MAX_CODE_POINTS - 1] + "…"
    return text


def _has_only_printable_ascii(url: str) -> bool:
    return all(0x20 < ord(character) < 0x7F and character != "\\" for character in url)


def _url_origin(url: str) -> tuple[str, str, int] | None:
    """Return the scheme, host, and port of an http or https URL ``open_url`` may compare."""
    if not _has_only_printable_ascii(url):
        return None
    parts = urlsplit(url)
    scheme = parts.scheme.lower()
    if scheme not in _DEFAULT_PORTS or "@" in parts.netloc or "%" in parts.netloc:
        return None
    match = _AUTHORITY.fullmatch(parts.netloc)
    if match is None:
        return None
    host, port_text = match.group("host").lower(), match.group("port")
    if port_text is None:
        return scheme, host, _DEFAULT_PORTS[scheme]
    if _PORT.fullmatch(port_text) is None or int(port_text) > 65535:
        return None
    return scheme, host, int(port_text)


def _is_dot_segment(segment: str) -> bool:
    return segment.lower().replace("%2e", ".") in (".", "..")


@cache
def _describe_validator() -> Draft202012Validator:
    text = (
        resources.files("dex_agents_cli_core.schemas")
        .joinpath("terminal-ui-describe.schema.json")
        .read_text("utf-8")
    )
    return Draft202012Validator(json.loads(text))
