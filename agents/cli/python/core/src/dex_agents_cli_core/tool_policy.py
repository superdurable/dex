# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Per-turn tool policy and narrowing, for the CLI core's tools and a harness's own replacements."""

from __future__ import annotations

import json

from dex_agents import (
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolEffect,
    ToolExecutionError,
    ToolPolicy,
)
from dex_agents_cli_core.config import EffectiveConfig, is_shell_command_allowed
from dex_agents_cli_core.harness import LocalToolGroup

# From least to most cautious: a policy may move a tool right, never left.
_EFFECT_CAUTION = {
    ToolEffect.READ: 0,
    ToolEffect.IDEMPOTENT_WRITE: 1,
    ToolEffect.NON_IDEMPOTENT_WRITE: 2,
    ToolEffect.UNCLASSIFIED: 3,
}
_APPROVED_BY_DEFAULT = frozenset({ToolEffect.NON_IDEMPOTENT_WRITE, ToolEffect.UNCLASSIFIED})


def resolve_turn_tool_policy(
    turn: AgentTurnContext,
    tool_name: str,
    effect: ToolEffect,
    requires_approval: bool,
    turn_config: EffectiveConfig,
) -> ToolPolicy:
    """Return one tool call's policy from the tool's own, an explicit session policy, and the turn.

    The tool's effect is a floor. An explicit ``ToolPolicy`` for the tool in
    ``AgentSessionOptions.tool_policies`` may make the effect more cautious,
    such as ``read`` to ``non_idempotent_write``, but never less, so a write
    is never retried as a read; the ranking is ``read``,
    ``idempotent_write``, ``non_idempotent_write``, then ``unclassified``.
    Approval is a floor too: the call waits when the tool asks for it, when
    the explicit policy asks for it, or, when the explicit policy leaves it
    unset, when the combined effect needs approval by default. An explicit
    ``requires_approval=False`` never removes the tool's own approval. A tool
    in the turn's ``approval.always`` always waits.

    A harness that replaces a local tool group, such as with Claude Code's own
    tools, uses it to give those tools the CLI core's policy.

    Example:
        >>> turn_config = context.current_turn()
        >>> policy = resolve_turn_tool_policy(
        ...     turn, "Bash", ToolEffect.NON_IDEMPOTENT_WRITE,
        ...     is_shell_command_approval_required(turn_config, command), turn_config)

    Args:
        turn: The ``RunModel`` context, whose explicit policies may tighten the tool.
        tool_name: The tool name as the agent sees it.
        effect: The tool's own effect.
        requires_approval: Whether the tool itself asks for approval.
        turn_config: The turn's configuration, for ``approval.always``.

    Returns:
        The policy for ``DeferredToolCall.policy``.
    """
    is_always_approved = tool_name in turn_config.approval.always
    explicit = turn.tool_policies.get(tool_name)
    if explicit is None:
        return ToolPolicy(effect, requires_approval=requires_approval or is_always_approved)
    if _EFFECT_CAUTION[explicit.effect] > _EFFECT_CAUTION[effect]:
        effect = explicit.effect
    if explicit.requires_approval is None:
        is_explicitly_approved = effect in _APPROVED_BY_DEFAULT
    else:
        is_explicitly_approved = explicit.requires_approval
    return ToolPolicy(
        effect,
        requires_approval=requires_approval or is_explicitly_approved or is_always_approved,
    )


def is_local_tool_group_enabled(turn: EffectiveConfig, group: LocalToolGroup) -> bool:
    """Report whether a turn switches a local tool group on.

    A harness that replaces a group offers its own tools only while this is ``True``.

    Example:
        >>> if not is_local_tool_group_enabled(context.current_turn(), LocalToolGroup.SHELL):
        ...     return deny("shell commands are switched off for this session")

    Args:
        turn: The turn's configuration.
        group: The local tool group.

    Returns:
        The group's ``local_tools`` switch for the turn.
    """
    local_tools = turn.local_tools
    match group:
        case LocalToolGroup.FILES:
            return local_tools.files
        case LocalToolGroup.SHELL:
            return local_tools.shell.enabled
        case LocalToolGroup.WEB_FETCH:
            return local_tools.web_fetch
        case LocalToolGroup.BROWSER:
            return local_tools.browser


def is_shell_command_approval_required(turn: EffectiveConfig, command: str) -> bool:
    """Report whether a shell command waits for approval in a turn.

    Args:
        turn: The turn's configuration.
        command: The command text.

    Returns:
        ``True`` unless the turn's ``local_tools.shell.allow`` has the command's exact text.
    """
    return not is_shell_command_allowed(command, turn.local_tools.shell.allow)


#: Key of the reference a source stores for a call of a tool its turn switched off.
DISABLED_TOOL_REFERENCE_KEY = "disabled_tool"


def disabled_tool_call(tool_call_id: str, tool_name: str, arguments: str) -> DeferredToolCall:
    """Return a call that its tool Step refuses: the turn switched the tool off.

    A runner can still name a tool the turn does not offer, such as from a
    listing it kept. The refusal is stored with the call, so the tool Step
    refuses it on any Worker without running anything.

    Args:
        tool_call_id: The model's tool call ID.
        tool_name: The tool name.
        arguments: The arguments as JSON text.

    Returns:
        A read call without approval whose reference marks it switched off.
    """
    return DeferredToolCall(
        tool_call_id=tool_call_id,
        tool_name=tool_name,
        arguments=arguments,
        policy=ToolPolicy(ToolEffect.READ, requires_approval=False),
        tool_source_reference=json.dumps({DISABLED_TOOL_REFERENCE_KEY: tool_name}),
    )


def raise_if_disabled(call: DeferredToolExecution) -> None:
    """Refuse a call that ``disabled_tool_call`` built.

    Args:
        call: The tool Step's call.

    Raises:
        ToolExecutionError: If the call's turn switched its tool off.
    """
    reference = call.tool_call.tool_source_reference
    if reference is None:
        return
    try:
        parsed = json.loads(reference)
    except json.JSONDecodeError:
        return
    if isinstance(parsed, dict) and DISABLED_TOOL_REFERENCE_KEY in parsed:
        raise ToolExecutionError(
            f"tool {call.tool_call.tool_name} is switched off for this session"
        )
