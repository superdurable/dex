# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Render the Flow Definition Graph of a session Flow for Dex Web."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections.abc import Sequence
from importlib import resources
from pathlib import Path

_ACTION_PERMISSION_PATTERN = re.compile(r"[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*")


def render_session_flow_graph(flow_type: str, action_permission: str) -> str:
    """Render the Flow Definition Graph 2.0 file of one agent's session Flow.

    ``dexcli visualize`` cannot read the session Flow: the Flow lives in this
    library, and the application chooses its Flow type. This function fills
    both choices into the graph that ships with the package. Write the text to
    a file in the directory that Dex Web reads with ``--flow-rendering-dir``.
    Dex Web then draws the session Steps and offers the ``resolve_interrupt``,
    ``answer_question``, ``steer_user_message``, and ``cancel_turn`` Actions.
    Those Actions have no condition, because each RPC checks current state
    itself.

    The TypeScript core renders the same bytes, so a graph from either core
    serves a session on Workers of either language. Render the file again after
    you upgrade this package. The ``dex-agents-session-graph`` command does the
    same from a build or deployment script.

    Example:
        >>> from pathlib import Path
        >>> Path("definitions/support-agent.json").write_text(
        ...     render_session_flow_graph("SupportAgent", "support-agent.operate"),
        ...     encoding="utf-8",
        ... )

    .. versionadded:: 0.3.0

    Args:
        flow_type: The Flow type of the agent, as passed to ``AgentSessionFlow``.
        action_permission: The permission every session Action requires, such
            as ``"support-agent.operate"``.

    Returns:
        The graph as JSON text that ends with a newline.

    Raises:
        ValueError: If ``flow_type`` is blank, or if ``action_permission`` is
            not lowercase letters, digits, dots, and hyphens that start with a
            letter.
    """
    if not flow_type.strip():
        raise ValueError("flow_type must not be empty")
    if _ACTION_PERMISSION_PATTERN.fullmatch(action_permission) is None:
        raise ValueError(f"action_permission {action_permission!r} is not a valid permission")
    template = resources.files("dex_agents").joinpath("session_flow_graph.json")
    graph = json.loads(template.read_text(encoding="utf-8"))
    graph["flow"]["name"] = flow_type
    for action in graph["v2"]["actions"]:
        action["requiredPermission"] = action_permission
    return json.dumps(graph, indent=2, ensure_ascii=False) + "\n"


def main(arguments: Sequence[str] | None = None) -> int:
    """Run the ``dex-agents-session-graph`` command.

    Args:
        arguments: Command-line arguments. ``None`` reads ``sys.argv``.

    Returns:
        The process exit code: 0, or 2 for invalid arguments.
    """
    parser = argparse.ArgumentParser(
        prog="dex-agents-session-graph",
        description="Render the Flow Definition Graph of a session Flow for Dex Web.",
    )
    parser.add_argument("--flow-type", required=True, help="the Flow type of the agent")
    parser.add_argument("--permission", required=True, help="the permission every Action requires")
    parser.add_argument("--out", type=Path, help="file to write; stdout when omitted")
    options = parser.parse_args(arguments)
    try:
        graph = render_session_flow_graph(options.flow_type, options.permission)
    except ValueError as error:
        print(error, file=sys.stderr)
        return 2
    if options.out is None:
        sys.stdout.write(graph)
    else:
        options.out.write_text(graph, encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
