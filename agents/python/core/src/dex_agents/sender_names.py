# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Rules for the display name that the sender of a user message declares."""

from __future__ import annotations

import unicodedata

#: The most Unicode code points ``AgentUserMessage.sender_name`` may have.
#:
#: .. versionadded:: 0.6.2
MAX_SENDER_NAME_LENGTH = 64

# Format characters (Cf) can reverse or hide a label, and a lone surrogate (Cs) does not round-trip.
_REFUSED_CATEGORIES = frozenset({"Cc", "Cf", "Cs", "Zl", "Zp"})


def is_valid_sender_name(sender_name: str) -> bool:
    """Report whether a declared sender display name is valid.

    ``send_user_message`` and ``steer_user_message`` refuse a message whose
    ``sender_name`` fails this check. Check a name a person typed with it
    before you send it, after you trim it. Every core applies the same rule.

    Example:
        >>> is_valid_sender_name("Alice")
        True
        >>> is_valid_sender_name(" Alice")
        False

    .. versionadded:: 0.6.2

    Args:
        sender_name: The name to check.

    Returns:
        ``True`` when the name has 1 to ``MAX_SENDER_NAME_LENGTH`` code
        points, contains no control character (Unicode category ``Cc``), no
        format character such as a bidirectional override or a zero-width
        character (``Cf``), no lone surrogate (``Cs``), and no line or
        paragraph separator (``Zl``, ``Zp``), and neither starts nor ends
        with a space separator (``Zs``); ``False`` otherwise.
    """
    if not 1 <= len(sender_name) <= MAX_SENDER_NAME_LENGTH:
        return False
    if any(unicodedata.category(character) in _REFUSED_CATEGORIES for character in sender_name):
        return False
    return (
        unicodedata.category(sender_name[0]) != "Zs"
        and unicodedata.category(sender_name[-1]) != "Zs"
    )


def build_sender_named_text(sender_name: str | None, text: str) -> str:
    """Return the text the model sees for one user message.

    The session prefixes a message that has a sender name with that name, so
    the model knows who speaks when several people use one session.

    Example:
        >>> build_sender_named_text("Alice", "hello")
        'Alice: hello'

    .. versionadded:: 0.6.2

    Args:
        sender_name: ``AgentUserMessage.sender_name``, or ``None``.
        text: The message text.

    Returns:
        ``"<sender_name>: <text>"`` when ``sender_name`` is set, and ``text``
        unchanged when it is ``None``.
    """
    if sender_name is None:
        return text
    return f"{sender_name}: {text}"
