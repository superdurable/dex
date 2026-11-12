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
_SPACE = r"[ \t\n\r\f\v]"
_QUOTED_OR_BARE_VALUE = r"(\"[^\"]*\"|'[^']*'|[^ \t\n\r\f\v\"'&;,=][^ \t\n\r\f\v\"'&;,]*)"
_SCHEME_VALUE = re.compile(r"^(bearer|basic|digest)$", re.IGNORECASE)


def is_credential_like_name(name: str) -> bool:
    """Report whether a name looks like it holds a credential.

    A display shows the value of such an argument, header, field, or variable
    as ``REDACTED_ARGUMENT_VALUE``. The name is split into words at ``_``,
    ``-``, ``.``, spaces, and camelCase boundaries, ignoring case. It is a
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
    words = [
        word for word in _WORD_SEPARATOR.split(_CAMEL_CASE_BOUNDARY.sub("_", name).lower()) if word
    ]
    if any(word in _CREDENTIAL_WORDS or word.endswith(_CREDENTIAL_WORD_ENDINGS) for word in words):
        return True
    return any(
        tuple(words[index : index + len(phrase)]) == phrase
        for phrase in _CREDENTIAL_PHRASES
        for index in range(len(words))
    )


def redact_inline_credentials(text: str) -> str:
    """Replace the credentials written inline in a text with ``REDACTED_ARGUMENT_VALUE``.

    The text may be a shell command or a file. It replaces private key
    blocks; the password of a URL such as ``https://user:pass@host``; the value
    after ``Bearer``, ``Basic``, or ``Digest``; the password of
    ``-u user:pass``, ``--user``, and ``--proxy-user``; the value of a
    ``name=value`` assignment, a ``name: value`` header or field, and a
    ``--name value`` flag whose name ``is_credential_like_name`` matches,
    which covers query parameters such as ``?token=…``; and common token
    shapes, such as AWS access key IDs and GitHub, Slack, GitLab, and
    OpenAI-style keys and JWTs. A quoted value is replaced whole. It cannot
    find every credential: a value in an unusual form stays as it is.

    .. versionadded:: 0.6.0

    Example:
        >>> redact_inline_credentials("curl -H 'Authorization: Bearer abc123456'")
        "curl -H 'Authorization: Bearer <redacted>'"

    Args:
        text: The text.

    Returns:
        The text with each credential found replaced.
    """
    for pattern, replace in _INLINE_CREDENTIAL_RULES:
        text = pattern.sub(replace, text)
    return text


def _redact_named_value(match: re.Match[str], name: str, prefix: str, value: str) -> str:
    if (
        not is_credential_like_name(name)
        or REDACTED_ARGUMENT_VALUE in value
        or _SCHEME_VALUE.match(value)
    ):
        return match.group(0)
    quote = value[0] if value[:1] in ('"', "'") else ""
    return f"{prefix}{quote}{REDACTED_ARGUMENT_VALUE}{quote}"


_INLINE_CREDENTIAL_RULES: tuple[tuple[re.Pattern[str], Callable[[re.Match[str]], str]], ...] = (
    (
        re.compile(
            r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?"
            r"(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\Z)",
            re.ASCII,
        ),
        lambda match: REDACTED_ARGUMENT_VALUE,
    ),
    (
        re.compile(
            r"([A-Za-z][A-Za-z0-9+.-]*://)([^ \t\n\r\f\v/:@'\"]+):([^ \t\n\r\f\v/@'\"]+)@",
            re.ASCII,
        ),
        lambda match: f"{match.group(1)}{match.group(2)}:{REDACTED_ARGUMENT_VALUE}@",
    ),
    (
        re.compile(
            rf"\b(bearer|basic|digest)({_SPACE}+)([A-Za-z0-9._~+/=-]{{6,}})",
            re.ASCII | re.IGNORECASE,
        ),
        lambda match: f"{match.group(1)}{match.group(2)}{REDACTED_ARGUMENT_VALUE}",
    ),
    (
        re.compile(
            rf"(^|{_SPACE})(-u|--user|--proxy-user)({_SPACE}+|=)([\"']?)"
            r"([^ \t\n\r\f\v:\"']+):([^ \t\n\r\f\v\"']+)",
            re.ASCII,
        ),
        lambda match: (
            f"{match.group(1)}{match.group(2)}{match.group(3)}{match.group(4)}"
            f"{match.group(5)}:{REDACTED_ARGUMENT_VALUE}"
        ),
    ),
    (
        re.compile(
            rf"([A-Za-z_][A-Za-z0-9_.-]*)({_SPACE}*={_SPACE}*){_QUOTED_OR_BARE_VALUE}", re.ASCII
        ),
        lambda match: _redact_named_value(
            match, match.group(1), match.group(1) + match.group(2), match.group(3)
        ),
    ),
    (
        re.compile(
            rf"([\"']?)([A-Za-z][A-Za-z0-9_.-]*)([\"']?{_SPACE}*:{_SPACE}*){_QUOTED_OR_BARE_VALUE}",
            re.ASCII,
        ),
        lambda match: _redact_named_value(
            match,
            match.group(2),
            match.group(1) + match.group(2) + match.group(3),
            match.group(4),
        ),
    ),
    (
        re.compile(
            rf"(--?)([A-Za-z][A-Za-z0-9_-]*)({_SPACE}+)"
            r"(\"[^\"]*\"|'[^']*'|[^ \t\n\r\f\v\"'-][^ \t\n\r\f\v\"']*)",
            re.ASCII,
        ),
        lambda match: _redact_named_value(
            match,
            match.group(2),
            match.group(1) + match.group(2) + match.group(3),
            match.group(4),
        ),
    ),
    (
        re.compile(
            r"\b(?:(?:AKIA|ASIA)[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}"
            r"|sk-[A-Za-z0-9_-]{20,}|glpat-[A-Za-z0-9_-]{20,}"
            r"|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})",
            re.ASCII,
        ),
        lambda match: REDACTED_ARGUMENT_VALUE,
    ),
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
    its value, and a string value has its inline credentials replaced (see
    ``redact_inline_credentials``). Longer text, objects, and lists are left
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
    head = f"Calls {connector_id} {operation} with connection {connection_name}"
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
