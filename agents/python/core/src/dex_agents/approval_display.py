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
which argument names hold credentials, how a connector call is described, and
the largest preview the session keeps.

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
from typing import Any

from dex_agents.values import AgentInterruptDisplay

__all__ = [
    "MAX_APPROVAL_PREVIEW_BYTES",
    "REDACTED_ARGUMENT_VALUE",
    "build_connector_tool_approval_display",
    "is_credential_like_name",
]

#: Largest ``AgentInterruptDisplay.preview`` the session stores, in UTF-8
#: bytes: 16 KiB. The session drops a longer preview and keeps the target and
#: summary, because the display is stored on the call's record, on its
#: interrupt, and in its ``interrupt_raised`` event.
#:
#: .. versionadded:: 0.6.0
MAX_APPROVAL_PREVIEW_BYTES = 16_384

#: The text a display shows instead of the value of a credential-like argument.
#:
#: .. versionadded:: 0.6.0
REDACTED_ARGUMENT_VALUE = "<redacted>"

_CREDENTIAL_NAMES = frozenset(
    (
        "api_key",
        "apikey",
        "key",
        "token",
        "access_token",
        "refresh_token",
        "id_token",
        "secret",
        "client_secret",
        "password",
        "authorization",
        "proxy_authorization",
        "cookie",
        "set_cookie",
    )
)
_CREDENTIAL_NAME_SUFFIXES = ("_token", "_secret", "_key", "_password")
_CAMEL_CASE_BOUNDARY = re.compile(r"(?<=[a-z0-9])(?=[A-Z])")
_MAX_DISPLAYED_ARGUMENTS = 5
_MAX_DISPLAYED_ARGUMENT_CHARACTERS = 80


def is_credential_like_name(name: str) -> bool:
    """Report whether an argument, header, or field name looks like it holds a credential.

    A display shows the value of such an argument as
    ``REDACTED_ARGUMENT_VALUE``. The check ignores case and treats ``-`` and
    camelCase boundaries as ``_``. It matches ``api_key``, ``apikey``,
    ``key``, ``token``, ``access_token``, ``refresh_token``, ``id_token``,
    ``secret``, ``client_secret``, ``password``, ``authorization``,
    ``proxy_authorization``, ``cookie``, and ``set_cookie``, and every name
    that ends in ``_token``, ``_secret``, ``_key``, or ``_password``. So
    ``X-Api-Key``, ``clientSecret``, and ``client_secret`` all match. The
    TypeScript core applies the same rule.

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
    words = _CAMEL_CASE_BOUNDARY.sub("_", name).lower().replace("-", "_")
    return words in _CREDENTIAL_NAMES or words.endswith(_CREDENTIAL_NAME_SUFFIXES)


def build_connector_tool_approval_display(
    *, connector_id: str, connection_name: str, operation: str, arguments: str
) -> AgentInterruptDisplay:
    """Build the approval display of one connector tool call.

    The target is ``<connector_id>/<connection_name>``. The summary names the
    operation and the connection and lists up to five top-level arguments in
    argument order: each string of at most 80 characters on one line, number,
    and boolean, as JSON. A credential-like argument (see
    ``is_credential_like_name``) shows ``REDACTED_ARGUMENT_VALUE`` instead of
    its value. Longer text, objects, and lists are left out; the approval UI
    shows them from the arguments. The summary never uses the operation's
    description, which says what the operation is for, not what this call
    does. ``ConnectorToolSource`` and the CLI's connector tools use it; a
    source of other connector tools may too.

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
        ``None`` for no display or a value that is not one, and the display
        without its preview when the preview is over
        ``MAX_APPROVAL_PREVIEW_BYTES``; otherwise the display itself.
    """
    if not isinstance(display, AgentInterruptDisplay):
        return None
    if not isinstance(display.target, str) or not isinstance(display.summary, str):
        return None
    preview = display.preview
    is_preview_kept = (
        isinstance(preview, str)
        and len(preview.encode("utf-8", "surrogatepass")) <= MAX_APPROVAL_PREVIEW_BYTES
    )
    return AgentInterruptDisplay(
        target=display.target,
        summary=display.summary,
        preview=preview if is_preview_kept else None,
    )


def _describe_key_arguments(arguments: str) -> list[str]:
    try:
        parsed: Any = json.loads(arguments)
    except ValueError:
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
            described.append(f"{name}: {json.dumps(value, ensure_ascii=False)}")
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
