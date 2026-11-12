# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Build and bound the approval display of a tool call.

A tool's source returns an ``AgentInterruptDisplay`` in
``DeferredToolCall.approval_display``, and the session stores it in
``AgentInterrupt.display``. This module holds the rules every source shares:
which names hold credentials, how inline credentials are redacted, how a
connector call is described, and the largest display the session keeps. The
TypeScript core applies the same rules; the shared cases are in
``agents/conformance/approval_display.json``.

Example:
    >>> display = build_connector_tool_approval_display(
    ...     connector_id="slack",
    ...     connection_name="support-slack",
    ...     operation="postChannelMessage",
    ...     arguments='{"channel": "#eng"}',
    ... )
    >>> display.summary
    'Calls slack postChannelMessage with connection support-slack; channel: "#eng".'
"""

from __future__ import annotations

import json
import math
import re
from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from dex_agents.values import AgentInterruptDisplay

__all__ = [
    "MAX_APPROVAL_PREVIEW_BYTES",
    "MAX_APPROVAL_SUMMARY_BYTES",
    "MAX_APPROVAL_TARGET_BYTES",
    "REDACTED_ARGUMENT_VALUE",
    "build_connector_tool_approval_display",
    "is_credential_like_name",
    "redact_inline_credentials",
]

#: Largest ``AgentInterruptDisplay.preview`` the session stores, in UTF-8
#: bytes: 16 KiB. The session drops a longer preview and keeps the target and
#: summary, because the display is stored on the call's record, on its
#: interrupt, and in its ``interrupt_raised`` event.
#:
#: .. versionadded:: 0.6.0
MAX_APPROVAL_PREVIEW_BYTES = 16_384

#: Largest ``AgentInterruptDisplay.target`` the session stores, in UTF-8 bytes:
#: 1 KiB. The session cuts a longer target and ends it with ``…``.
#:
#: .. versionadded:: 0.6.0
MAX_APPROVAL_TARGET_BYTES = 1_024

#: Largest ``AgentInterruptDisplay.summary`` the session stores, in UTF-8
#: bytes: 2 KiB. The session cuts a longer summary and ends it with ``…``.
#:
#: .. versionadded:: 0.6.0
MAX_APPROVAL_SUMMARY_BYTES = 2_048

#: The text a display shows instead of a credential.
#:
#: .. versionadded:: 0.6.0
REDACTED_ARGUMENT_VALUE = "<redacted>"

_TRUNCATION_MARK = "…"
_CREDENTIAL_WORDS = frozenset(
    (
        "apikey",
        "apikeys",
        "auth",
        "authorization",
        "bearer",
        "cookie",
        "cookies",
        "cred",
        "creds",
        "credential",
        "credentials",
        "dsn",
        "jwt",
        "key",
        "otp",
        "pass",
        "passphrase",
        "passwd",
        "password",
        "passwords",
        "privatekey",
        "pwd",
        "secret",
        "secrets",
        "session",
        "sessionid",
        "signature",
        "token",
        "tokens",
    )
)
_CREDENTIAL_PHRASES = (("connection", "string"), ("conn", "str"))
_CREDENTIAL_WORD_ENDINGS = (
    "apikey",
    "credential",
    "credentials",
    "passwd",
    "password",
    "secret",
    "secrets",
    "token",
    "tokens",
)
_CAMEL_CASE_BOUNDARY = re.compile(r"(?<=[a-z0-9])(?=[A-Z])")
_WORD_SEPARATOR = re.compile(r"[^a-z0-9]+")
_MAX_DISPLAYED_ARGUMENTS = 5
_MAX_DISPLAYED_ARGUMENT_CHARACTERS = 80

# Explicit ASCII classes keep the patterns identical to the TypeScript core's.
_ESCAPE_SEQUENCE = re.compile(r"\\u([0-9a-fA-F]{4})|\\x([0-9a-fA-F]{2})|%([0-9a-fA-F]{2})")
_PRIVATE_KEY_BLOCK = re.compile(
    r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\Z)",
    re.ASCII,
)
_URL_USER_INFO = re.compile(
    r"([A-Za-z][A-Za-z0-9+.-]*://)([^ \t\n\r\f\v/:@'\"]+):([^ \t\n\r\f\v/@'\"]+)@", re.ASCII
)
_USER_FLAG = re.compile(r"(^|[ \t\n\r\f\v])(-u|--user|--proxy-user)([ \t]+|=)", re.ASCII)
_SSHPASS = re.compile(r"(^|[ \t\n\r\f\v])(sshpass[ \t]+-p)([ \t]*)", re.ASCII)
_YAML_BLOCK_INDICATOR = re.compile(r"[|>][+-]?[0-9]?[ \t]*(?:#[^\n]*)?", re.ASCII)
_NAMED_VALUE = re.compile(
    r"([\"']?)([A-Za-z_][A-Za-z0-9_.-]*)([\"']?)([ \t]*)([:=])([ \t]*)", re.ASCII
)
_FLAG_VALUE = re.compile(r"(^|[ \t\n\r\f\v])(--?)([A-Za-z][A-Za-z0-9_-]*)([ \t]+)", re.ASCII)
_STANDALONE_SCHEME = re.compile(
    r"\b(bearer|basic|digest)([ \t]+)([A-Za-z0-9._~+/-]+=*)", re.ASCII | re.IGNORECASE
)
_TOKEN_SHAPED = re.compile(r"[0-9+/=._~-]")
_AUTHORIZATION_SCHEME = re.compile(
    r"^(bearer|basic|digest|token|negotiate)[ \t]+", re.ASCII | re.IGNORECASE
)
_TOKEN_SHAPES = re.compile(
    r"\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}"
    r"|sk-[A-Za-z0-9_-]{20,}|glpat-[A-Za-z0-9_-]{20,}"
    r"|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})",
    re.ASCII,
)
_BARE_VALUE_STOPS = frozenset(" \t\n\r\f\v&;,}])")
_QUOTES = frozenset("\"'")


def is_credential_like_name(name: str) -> bool:
    """Report whether a name looks like it holds a credential.

    A display shows the value of such an argument, header, field, or variable
    as ``REDACTED_ARGUMENT_VALUE``. The name is split into words at ``_``,
    ``-``, ``.``, and spaces, and again at camelCase boundaries, ignoring
    case, so ``pAsSwOrD`` is one word. It is a
    credential name when a word is one of ``apikey``, ``auth``,
    ``authorization``, ``bearer``, ``cookie``, ``cred``, ``credential``,
    ``dsn``, ``jwt``, ``key``, ``otp``, ``pass``, ``passphrase``, ``passwd``,
    ``password``, ``privatekey``, ``pwd``, ``secret``, ``session``,
    ``sessionid``, ``signature``, or ``token`` (and the plural forms
    ``apikeys``, ``cookies``, ``creds``, ``credentials``, ``passwords``,
    ``secrets``, and ``tokens``), when a word ends in ``apikey``,
    ``credential``, ``credentials``, ``passwd``, ``password``, ``secret``,
    ``secrets``, ``token``, or ``tokens``, as in ``PGPASSWORD``, or when it
    holds the words ``connection string`` or ``conn str``. So
    ``X-Api-Key``, ``clientSecret``, ``private_key``, ``credentials``, and
    ``DATABASE_CONNECTION_STRING`` all match, and ``channel``, ``monkey``,
    ``tokenizer``, and ``author`` do not.

    .. versionadded:: 0.6.0

    Example:
        >>> is_credential_like_name("clientSecret")
        True
        >>> is_credential_like_name("channel")
        False

    Args:
        name: The name to check.

    Returns:
        ``True`` when the name looks like it holds a credential.
    """
    # Whole lowercased words first, so ``pAsSwOrD`` matches before camelCase splits it.
    return any(
        _has_credential_words([word for word in _WORD_SEPARATOR.split(spelling.lower()) if word])
        for spelling in (name, _CAMEL_CASE_BOUNDARY.sub("_", name))
    )


def _has_credential_words(words: list[str]) -> bool:
    if any(word in _CREDENTIAL_WORDS or word.endswith(_CREDENTIAL_WORD_ENDINGS) for word in words):
        return True
    return any(
        tuple(words[index : index + len(phrase)]) == phrase
        for phrase in _CREDENTIAL_PHRASES
        for index in range(len(words))
    )


def redact_inline_credentials(text: str) -> str:
    r"""Replace common credential shapes in a text with ``REDACTED_ARGUMENT_VALUE``.

    The text may be a shell command or a file; every other character stays as
    it is. It is best-effort hygiene that keeps common credentials out of an
    approval display, not a security boundary: a credential in a form it does
    not know, such as one spelled with Unicode lookalike letters, stays as it
    is, and the call's arguments still hold the full text.

    It reads ``\uXXXX``, ``\xXX``, and ``%XX`` escapes as the characters they
    stand for, so an escaped name such as ``pass\u0077ord`` or ``%70assword``
    is found, but it returns the text with the escapes as written: only the
    characters of each credential found are replaced. It replaces private key
    blocks; the password of a URL such as ``https://user:pass@host``; the
    password of ``-u user:pass``, ``--user``, ``--proxy-user``, and
    ``sshpass -p``; and the value after a name that
    ``is_credential_like_name`` matches, in a ``name=value`` assignment, a
    ``name: value`` header or field, a JSON member, or a ``--name value``
    flag, which covers query parameters such as ``?token=…``. A value runs as
    a shell word: backslash escapes, line continuations, and adjacent quoted
    parts join it, escaped quotes stay inside a quoted part, and it ends at
    whitespace, ``&``, ``;``, ``,``, ``}``, ``]``, or ``)``; an unclosed quote
    runs to the end of its line. A triple-quoted value runs to its closing
    quotes, or to the end of the text, and a YAML block scalar after
    ``name: |`` or ``name: >`` covers its indented lines. The value of an
    ``Authorization`` or ``Cookie`` header runs to the end of its line, with
    folded and continued lines, and its scheme, such as ``Bearer``, stays. A
    value that is exactly ``REDACTED_ARGUMENT_VALUE`` stays. Outside a header,
    a token-shaped word after ``Bearer``, ``Basic``, or ``Digest`` is
    replaced, and so are common token shapes, such as AWS access key IDs and
    GitHub, Slack, GitLab, and OpenAI-style keys and JWTs. The rules are
    conservative: a generic ``key=value`` is replaced too.

    .. versionadded:: 0.6.0

    Example:
        >>> redact_inline_credentials("curl -H 'Authorization: Bearer t1'")
        "curl -H 'Authorization: Bearer <redacted>'"
        >>> redact_inline_credentials("printf '\\x41'")
        "printf '\\x41'"

    Args:
        text: The text.

    Returns:
        The text with each credential found replaced and nothing else changed.
    """
    units = _read_text_units(text)
    for find_secrets in _SECRET_FINDERS:
        secrets = find_secrets("".join(unit.text for unit in units))
        if secrets:
            units = _replace_with_markers(units, secrets)
    return "".join(
        REDACTED_ARGUMENT_VALUE if unit.is_marker else text[unit.start : unit.end] for unit in units
    )


@dataclass(frozen=True)
class _TextUnit:
    """One character of the text as the rules read it.

    An escape sequence reads as the character it stands for, and a found
    credential reads as the marker. ``start`` and ``end`` are its span in the
    original text.
    """

    text: str
    start: int
    end: int
    is_marker: bool = False


def _read_text_units(text: str) -> list[_TextUnit]:
    units: list[_TextUnit] = []
    index = 0
    while index < len(text):
        escape = _ESCAPE_SEQUENCE.match(text, index)
        if escape is not None:
            code = int(escape.group(1) or escape.group(2) or escape.group(3), 16)
            # A lone surrogate reads as written, so the text the rules read stays valid Unicode.
            if not 0xD800 <= code <= 0xDFFF:
                units.append(_TextUnit(chr(code), index, escape.end()))
                index = escape.end()
                continue
        units.append(_TextUnit(text[index], index, index + 1))
        index += 1
    return units


def _replace_with_markers(
    units: list[_TextUnit], secrets: list[tuple[int, int]]
) -> list[_TextUnit]:
    """Replace the units each span touches with one marker unit covering their original text."""
    replaced: list[_TextUnit] = []
    offset = 0
    secret_index = 0
    marker: tuple[int, int] | None = None
    for unit in units:
        unit_start = offset
        unit_end = offset + len(unit.text)
        offset = unit_end
        while secret_index < len(secrets) and secrets[secret_index][1] <= unit_start:
            secret_index += 1
        secret = secrets[secret_index] if secret_index < len(secrets) else None
        if secret is not None and secret[0] < unit_end and secret[1] > unit_start:
            marker = (unit.start, unit.end) if marker is None else (marker[0], unit.end)
            if secret[1] <= unit_end:
                replaced.append(_TextUnit(REDACTED_ARGUMENT_VALUE, *marker, is_marker=True))
                marker = None
                secret_index += 1
            continue
        if marker is not None:
            replaced.append(_TextUnit(REDACTED_ARGUMENT_VALUE, *marker, is_marker=True))
            marker = None
        replaced.append(unit)
    if marker is not None:
        replaced.append(_TextUnit(REDACTED_ARGUMENT_VALUE, *marker, is_marker=True))
    return replaced


def _match_spans(text: str, pattern: re.Pattern[str], group: int) -> list[tuple[int, int]]:
    return [match.span(group) for match in pattern.finditer(text)]


def _standalone_scheme_spans(text: str) -> list[tuple[int, int]]:
    return [
        match.span(3)
        for match in _STANDALONE_SCHEME.finditer(text)
        if _TOKEN_SHAPED.search(match.group(3))
    ]


def _named_value_kind(match: re.Match[str], value_start: int, text: str) -> str | None:
    is_comparison = match.group(5) == "=" and text[value_start : value_start + 1] == "="
    if is_comparison or not is_credential_like_name(match.group(2)):
        return None
    return "header" if _is_header_name(match.group(2)) else "value"


def _flag_value_kind(match: re.Match[str], value_start: int, text: str) -> str | None:
    if is_credential_like_name(match.group(3)) and text[value_start : value_start + 1] != "-":
        return "value"
    return None


def _find_value_secrets(
    text: str,
    pattern: re.Pattern[str],
    kind_of: Callable[[re.Match[str], int, str], str | None],
) -> list[tuple[int, int]]:
    """Return the credential span of the value after each match ``kind_of`` accepts."""
    secrets: list[tuple[int, int]] = []
    position = 0
    while (match := pattern.search(text, position)) is not None:
        value_start = match.end()
        kind = kind_of(match, value_start, text)
        if kind is None:
            position = value_start
            continue
        is_named = pattern is _NAMED_VALUE
        # A quote right before a name, with none after it, is the quote the value ends at.
        has_outer_quote = is_named and match.group(1) and not match.group(3)
        outer_quote = match.group(1) if has_outer_quote else ""
        field_start = match.start() if is_named and match.group(5) == ":" else -1
        end, secret = _read_value(text, value_start, kind, outer_quote, field_start)
        if secret is not None:
            secrets.append(secret)
        position = max(end, value_start)
    return secrets


def _read_value(
    text: str, start: int, kind: str, outer_quote: str, field_start: int
) -> tuple[int, tuple[int, int] | None]:
    """Read the value that starts at ``start``.

    It is a triple-quoted string, a YAML block scalar after ``name:`` (when
    ``field_start`` is the field's index), a header line with its folded and
    continued lines, or a shell word with its escapes, adjacent quoted parts,
    and line continuations. An unclosed quote runs to the end of its line, and
    an unclosed triple quote to the end of the text.

    Returns:
        Where the value ends and the span that holds the credential, or
        ``None`` for none.
    """
    for delimiter in ('"""', "'''"):
        if text.startswith(delimiter, start):
            close = text.find(delimiter, start + 3)
            end = len(text) if close < 0 else close + 3
            return end, _secret_span(text, start + 3, len(text) if close < 0 else close)
    line_end = _find_line_end(text, start)
    if field_start >= 0 and _YAML_BLOCK_INDICATOR.fullmatch(text, start, line_end):
        block_end = _find_yaml_block_end(text, field_start, line_end)
        if block_end > line_end:
            return block_end, _secret_span(text, line_end + 1, block_end)
        return block_end, None
    if kind == "header" and text[start : start + 1] not in _QUOTES:
        end = _find_header_end(text, start, outer_quote)
        scheme = _AUTHORIZATION_SCHEME.match(text[start:end])
        return end, _secret_span(text, start + (0 if scheme is None else scheme.end()), end)
    end = _find_shell_word_end(text, start, outer_quote)
    quote = text[start : start + 1]
    is_quoted = quote in _QUOTES and bool(quote) and end - start >= 2 and text[end - 1] == quote
    secret_start = start + 1 if is_quoted else start
    secret_end = end - 1 if is_quoted else end
    if kind == "user":
        separator = text[secret_start:secret_end].find(":")
        secret_start = secret_end if separator < 0 else secret_start + separator + 1
    return end, _secret_span(text, secret_start, secret_end)


def _secret_span(text: str, start: int, end: int) -> tuple[int, int] | None:
    secret = text[start:end]
    if not secret or secret == REDACTED_ARGUMENT_VALUE:
        return None
    return start, end


def _find_line_end(text: str, start: int) -> int:
    newline = text.find("\n", start)
    return len(text) if newline < 0 else newline


def _find_yaml_block_end(text: str, field_start: int, indicator_end: int) -> int:
    """Return the end of the YAML block whose lines are indented past the field's."""
    field_line_start = text.rfind("\n", 0, field_start) + 1
    field_indent = _count_indent(text, field_line_start)
    block_end = indicator_end
    line_start = indicator_end + 1
    while line_start <= len(text) and indicator_end < len(text):
        line_end = _find_line_end(text, line_start)
        is_blank = text[line_start:line_end].strip() == ""
        if not is_blank and _count_indent(text, line_start) <= field_indent:
            break
        if not is_blank:
            block_end = line_end
        if line_end >= len(text):
            break
        line_start = line_end + 1
    return block_end


def _count_indent(text: str, line_start: int) -> int:
    index = line_start
    while index < len(text) and text[index] in " \t":
        index += 1
    return index - line_start


def _find_header_end(text: str, start: int, outer_quote: str) -> int:
    """Return the end of a header value with its continued and folded lines."""
    index = start
    while index < len(text) and text[index] != outer_quote:
        character = text[index]
        following = text[index + 1 : index + 2]
        if character == "\\" and following in ("\n", "\r"):
            index += 3 if following == "\r" and text[index + 2 : index + 3] == "\n" else 2
            continue
        if character in "\n\r":
            is_crlf = character == "\r" and following == "\n"
            next_index = index + 2 if is_crlf else index + 1
            if text[next_index : next_index + 1] in (" ", "\t") and next_index < len(text):
                index = next_index
                continue
            break
        index += 1
    while index > start and text[index - 1] in "\r\n":
        index -= 1
    return index


def _find_shell_word_end(text: str, start: int, outer_quote: str) -> int:
    """Return the end of a shell word, or of a JSON or YAML value.

    A backslash escapes the next character, a line continuation included; a
    closed quoted part continues the word; an unclosed quote runs to the end
    of its line; and a stop character or ``outer_quote`` ends it.
    """
    index = start
    while index < len(text):
        character = text[index]
        if outer_quote and character == outer_quote:
            break
        if character == "\\":
            index += 2
            continue
        if character in _QUOTES:
            quoted_end = _find_quoted_end(text, index)
            if quoted_end - 1 > index and text[quoted_end - 1] == character:
                index = quoted_end
                continue
            return _find_line_end(text, index)
        if character in _BARE_VALUE_STOPS:
            break
        index += 1
    return min(index, len(text))


def _find_quoted_end(text: str, start: int) -> int:
    """Return the index after the quote closing the one at ``start``, or the line end."""
    quote = text[start]
    index = start + 1
    while index < len(text) and text[index] != quote and text[index] != "\n":
        index += 2 if quote == '"' and text[index] == "\\" else 1
    if index < len(text) and text[index] == quote:
        index += 1
    return min(index, len(text))


def _is_header_name(name: str) -> bool:
    words = re.split(r"[^a-z0-9]+", name.lower())
    return "authorization" in words or "cookie" in words or "cookies" in words


_SECRET_FINDERS: tuple[Callable[[str], list[tuple[int, int]]], ...] = (
    lambda text: _match_spans(text, _PRIVATE_KEY_BLOCK, 0),
    lambda text: _match_spans(text, _URL_USER_INFO, 3),
    lambda text: _find_value_secrets(text, _USER_FLAG, lambda match, start, scanned: "user"),
    lambda text: _find_value_secrets(
        text,
        _SSHPASS,
        lambda match, start, scanned: None if scanned[start : start + 1] == "-" else "value",
    ),
    lambda text: _find_value_secrets(text, _NAMED_VALUE, _named_value_kind),
    lambda text: _find_value_secrets(text, _FLAG_VALUE, _flag_value_kind),
    _standalone_scheme_spans,
    lambda text: _match_spans(text, _TOKEN_SHAPES, 0),
)


def build_connector_tool_approval_display(
    *, connector_id: str, connection_name: str, operation: str, arguments: str
) -> AgentInterruptDisplay:
    """Build the approval display of one connector tool call.

    The target is ``<connector_id>/<connection_name>``. The summary names the
    operation and the connection and lists up to five top-level arguments in
    argument order: each string of at most 80 characters on one line, number,
    and boolean, as JSON. A credential-like argument (see
    ``is_credential_like_name``) shows ``REDACTED_ARGUMENT_VALUE`` instead of
    its value, and string values and the connector, connection, and
    operation names have their inline credentials replaced (see
    ``redact_inline_credentials``), as best-effort hygiene. Longer text, objects, and lists are left
    out; the approval UI shows them from the arguments. The summary never uses
    the operation's description, which says what the operation is for, not
    what this call does. ``ConnectorToolSource`` and the CLI's connector tools
    use it; a source of other connector tools may too.

    .. versionadded:: 0.6.0

    Example:
        >>> display = build_connector_tool_approval_display(
        ...     connector_id="slack",
        ...     connection_name="support-slack",
        ...     operation="postChannelMessage",
        ...     arguments='{"channel": "#eng", "api_key": "k"}',
        ... )
        >>> display.target
        'slack/support-slack'
        >>> display.summary.endswith('channel: "#eng", api_key: <redacted>.')
        True

    Args:
        connector_id: The Connector ID, such as ``slack``.
        connection_name: The connection's name, such as ``support-slack``.
        operation: The operation the call runs, such as ``postChannelMessage``.
        arguments: The call's arguments as JSON text.

    Returns:
        The display, with no preview.
    """
    key_arguments = _describe_key_arguments(arguments)
    connector_id = redact_inline_credentials(connector_id)
    connection_name = redact_inline_credentials(connection_name)
    head = (
        f"Calls {connector_id} {redact_inline_credentials(operation)} "
        f"with connection {connection_name}"
    )
    summary = f"{head}; {', '.join(key_arguments)}." if key_arguments else f"{head}."
    return AgentInterruptDisplay(target=f"{connector_id}/{connection_name}", summary=summary)


def bound_approval_display(display: object) -> AgentInterruptDisplay | None:
    """Return the display the session stores for a call.

    Returns:
        ``None`` for no display or a value that is not one; otherwise the
        display with its target and summary cut to
        ``MAX_APPROVAL_TARGET_BYTES`` and ``MAX_APPROVAL_SUMMARY_BYTES``, and
        its preview, or ``None`` when that is over
        ``MAX_APPROVAL_PREVIEW_BYTES``.
    """
    if not isinstance(display, AgentInterruptDisplay):
        return None
    if not isinstance(display.target, str) or not isinstance(display.summary, str):
        return None
    preview = display.preview
    is_preview_kept = (
        isinstance(preview, str) and _utf8_length(preview) <= MAX_APPROVAL_PREVIEW_BYTES
    )
    return AgentInterruptDisplay(
        target=_truncate_utf8(display.target, MAX_APPROVAL_TARGET_BYTES),
        summary=_truncate_utf8(display.summary, MAX_APPROVAL_SUMMARY_BYTES),
        preview=preview if is_preview_kept else None,
    )


def _truncate_utf8(text: str, max_bytes: int) -> str:
    """Return the text, or its longest prefix that fits ``max_bytes`` with ``…`` appended."""
    if _utf8_length(text) <= max_bytes:
        return text
    budget = max_bytes - _utf8_length(_TRUNCATION_MARK)
    used = 0
    kept: list[str] = []
    for character in text:
        size = _utf8_length(character)
        if used + size > budget:
            break
        kept.append(character)
        used += size
    return "".join(kept) + _TRUNCATION_MARK


def _utf8_length(text: str) -> int:
    return len(text.encode("utf-8", "surrogatepass"))


def _describe_key_arguments(arguments: str) -> list[str]:
    try:
        parsed: Any = json.loads(arguments)
    except (ValueError, RecursionError):
        return []
    if not isinstance(parsed, dict):
        return []
    described: list[str] = []
    for name, value in parsed.items():
        if len(described) == _MAX_DISPLAYED_ARGUMENTS:
            break
        if is_credential_like_name(name):
            described.append(f"{name}: {REDACTED_ARGUMENT_VALUE}")
        elif _is_short_scalar(value):
            shown = redact_inline_credentials(value) if isinstance(value, str) else value
            described.append(f"{name}: {json.dumps(shown, ensure_ascii=False)}")
    return described


def _is_short_scalar(value: object) -> bool:
    if isinstance(value, str):
        return (
            "\n" not in value
            and "\r" not in value
            and len(value) <= _MAX_DISPLAYED_ARGUMENT_CHARACTERS
        )
    if isinstance(value, bool):
        return True
    if isinstance(value, int):
        return True
    return isinstance(value, float) and math.isfinite(value)
