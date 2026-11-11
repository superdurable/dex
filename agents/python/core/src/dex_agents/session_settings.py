# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Per-session settings: the size limit and the check every writer runs.

A session's settings are a JSON object that the application chooses when it
starts the session, such as ``{"model": "gpt-5-mini"}``, and can replace with
``update_session_settings``. A runner reads them from
``AgentTurnContext.session_settings``; an agent on the built-in loop maps them to a model
client with ``AgentDefinition.resolve_model``.

Settings are stored in the session's Flow state and returned by
``get_agent_snapshot``, so they must never hold a secret. Store a reference,
such as a credential name, and let the Worker resolve it.

Example:
    >>> validate_session_settings({"model": "gpt-5-mini"})
    >>> session_settings_size({"model": "gpt-5-mini"})
    22
"""

from __future__ import annotations

import json
from collections.abc import Mapping
from typing import Any

#: Largest session settings, as the UTF-8 bytes of their compact JSON text: 16 KiB.
MAX_SESSION_SETTINGS_BYTES = 16 * 1024

#: Extra bytes the session's own Steps and RPC accept over ``MAX_SESSION_SETTINGS_BYTES``.
#: The cores write some numbers and characters differently, so settings that one core's
#: Client accepted could measure a few bytes more in another core's Worker; the tolerance
#: keeps that from failing a session. Clients check the exact limit.
SESSION_SETTINGS_SIZE_TOLERANCE_BYTES = 1024


def session_settings_size(session_settings: Mapping[str, Any]) -> int:
    """Return the size that ``MAX_SESSION_SETTINGS_BYTES`` limits.

    The size is the UTF-8 byte length of the compact JSON text that Python's
    ``json`` module writes: no spaces after ``,`` and ``:``, and characters
    outside ASCII written as themselves, not escaped. The TypeScript and Go
    cores measure their own JSON text, which is the same for strings,
    integers, booleans, ``null``, objects, and arrays, with two exceptions:
    Python writes floats in their shortest form, such as ``1.0`` and
    ``1e+16``, where TypeScript and Go write ``1`` and
    ``10000000000000000``, and Go escapes U+2028 and U+2029 as ``\u2028``
    and ``\u2029``. So the sizes can differ by a few bytes per float or per
    such character. The session's Steps and RPC accept
    ``SESSION_SETTINGS_SIZE_TOLERANCE_BYTES`` more than the limit, so
    settings that another core's Client accepted never fail a session.

    Args:
        session_settings: The settings.

    Returns:
        The size in bytes.

    Raises:
        ValueError: If the settings are not JSON: a non-string key, a value
            JSON cannot hold, or NaN or an infinity.
    """
    try:
        text = json.dumps(
            session_settings, separators=(",", ":"), ensure_ascii=False, allow_nan=False
        )
    except (TypeError, ValueError) as error:
        raise ValueError(f"session settings must be JSON: {error}") from error
    return len(text.encode("utf-8"))


def validate_session_settings(session_settings: object) -> None:
    """Check that a value can be a session's settings.

    ``AgentSessionClient.start_session`` and
    ``AgentSessionClient.update_session_settings`` run this check before they
    send anything. Call it to report a problem early, such as in a form
    handler, or before ``AsyncClient.start_flow``: a start whose settings the
    session refuses fails the session at once.

    Args:
        session_settings: The candidate settings.

    Raises:
        ValueError: If the value is not a JSON object with string keys, holds
            a value JSON cannot hold, or its compact JSON text is larger than
            ``MAX_SESSION_SETTINGS_BYTES``.
    """
    _check_session_settings(session_settings, MAX_SESSION_SETTINGS_BYTES)


def check_stored_session_settings(session_settings: object) -> None:
    """Check settings as the session's Steps and RPC do, with the size tolerance.

    Args:
        session_settings: The candidate settings.

    Raises:
        ValueError: As ``validate_session_settings`` does, with a limit of
            ``MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES``.
    """
    _check_session_settings(
        session_settings, MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES
    )


def _check_session_settings(session_settings: object, limit_bytes: int) -> None:
    if not isinstance(session_settings, dict):
        raise ValueError("session settings must be a JSON object")
    if any(not isinstance(key, str) for key in session_settings):
        raise ValueError("session settings keys must be strings")
    size = session_settings_size(session_settings)
    if size > limit_bytes:
        raise ValueError(f"session settings are {size} bytes, more than the {limit_bytes} allowed")
