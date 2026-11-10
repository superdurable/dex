# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Dex MCP conventions: ``_meta`` keys that carry Dex guarantees across the MCP boundary."""

from __future__ import annotations

import logging
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any, Literal

from dex_agents.values import ToolEffect

#: Tool definition ``_meta`` key that declares the tool's effect class.
#:
#: Its value is ``"read"``, ``"idempotent_write"``, or ``"non_idempotent_write"``.
#: See the package README, "Dex MCP conventions".
#:
#: .. versionadded:: 0.5.0
TOOL_EFFECT_META_KEY = "io.superdurable.dex/effect"

#: Tool definition ``_meta`` key that recommends approval of every call.
#:
#: Its only value is ``TOOL_APPROVAL_RECOMMENDED``. It applies only together
#: with ``TOOL_EFFECT_META_KEY``.
#:
#: .. versionadded:: 0.5.0
TOOL_APPROVAL_META_KEY = "io.superdurable.dex/approval"

#: Tool result ``_meta`` key, or JSON-RPC ``error.data._meta`` key, that reports the call's outcome.
#:
#: Its value is ``TOOL_OUTCOME_UNKNOWN`` or ``TOOL_OUTCOME_REJECTED``.
#:
#: .. versionadded:: 0.5.0
TOOL_OUTCOME_META_KEY = "io.superdurable.dex/outcome"

#: ``TOOL_APPROVAL_META_KEY`` value: a person approves each call by default.
#:
#: .. versionadded:: 0.5.0
TOOL_APPROVAL_RECOMMENDED = "recommended"

#: ``TOOL_OUTCOME_META_KEY`` value: the call may or may not have taken effect.
#:
#: The session records ``outcome_unknown`` and never sends the call again.
#:
#: .. versionadded:: 0.5.0
TOOL_OUTCOME_UNKNOWN = "unknown"

#: ``TOOL_OUTCOME_META_KEY`` value: the call definitely did not take effect.
#:
#: The session records ``failed`` with the result's text.
#:
#: .. versionadded:: 0.5.0
TOOL_OUTCOME_REJECTED = "rejected"

UNKNOWN_OUTCOME_FALLBACK_MESSAGE = "the MCP server could not tell whether the tool took effect"
REJECTED_OUTCOME_FALLBACK_MESSAGE = "the MCP server rejected the call, so it did not take effect"

_LOGGER = logging.getLogger(__name__)
_DECLARABLE_EFFECTS = {
    effect.value: effect
    for effect in (
        ToolEffect.READ,
        ToolEffect.IDEMPOTENT_WRITE,
        ToolEffect.NON_IDEMPOTENT_WRITE,
    )
}

DexToolOutcome = Literal["unknown", "rejected"]


@dataclass(frozen=True)
class DexToolDeclaration:
    """What a tool definition's Dex ``_meta`` keys declare.

    ``effect`` is ``None`` when the effect key holds a value outside the
    convention; the tool then gets the session's default effect.
    """

    effect: ToolEffect | None
    is_approval_recommended: bool


def read_dex_tool_declaration(
    meta: Mapping[str, Any] | None, server_name: str, mcp_tool_name: str
) -> DexToolDeclaration | None:
    """Return the Dex declaration of a tool, or ``None`` when it has no effect key."""
    if meta is None or TOOL_EFFECT_META_KEY not in meta:
        return None
    effect_value = meta[TOOL_EFFECT_META_KEY]
    effect = _DECLARABLE_EFFECTS.get(effect_value) if isinstance(effect_value, str) else None
    if effect is None:
        _LOGGER.warning(
            "MCP tool %s/%s declares the unknown effect %r; it gets the session's default effect",
            server_name,
            mcp_tool_name,
            effect_value,
        )
        return DexToolDeclaration(effect=None, is_approval_recommended=False)
    has_approval = TOOL_APPROVAL_META_KEY in meta
    approval = meta.get(TOOL_APPROVAL_META_KEY)
    if has_approval and approval != TOOL_APPROVAL_RECOMMENDED:
        # Fail closed: any other approval value, null included, still asks a person.
        _LOGGER.warning(
            "MCP tool %s/%s declares the unknown approval %r; its calls wait for approval",
            server_name,
            mcp_tool_name,
            approval,
        )
    return DexToolDeclaration(effect=effect, is_approval_recommended=has_approval)


def read_dex_tool_outcome(meta: Any) -> DexToolOutcome | None:
    """Return the outcome a result's or an error's ``_meta`` reports, or ``None``."""
    if not isinstance(meta, Mapping) or TOOL_OUTCOME_META_KEY not in meta:
        return None
    outcome = meta[TOOL_OUTCOME_META_KEY]
    if outcome == TOOL_OUTCOME_UNKNOWN:
        return "unknown"
    if outcome == TOOL_OUTCOME_REJECTED:
        return "rejected"
    _LOGGER.warning("ignored the unknown MCP tool outcome %r", outcome)
    return None


def read_dex_error_outcome(error_data: Any) -> DexToolOutcome | None:
    """Return the outcome a JSON-RPC error's ``data._meta`` reports, or ``None``."""
    if not isinstance(error_data, Mapping):
        return None
    return read_dex_tool_outcome(error_data.get("_meta"))
