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
from collections.abc import Mapping, Sequence
from datetime import UTC, datetime
from importlib import resources
from pathlib import Path
from typing import Any

from dex import FlowDefinitionDocument

from dex_agents.session_trigger_bindings import (
    canonical_session_trigger_binding,
    parse_session_trigger_binding_flag,
    read_session_trigger_bindings_file,
    resolve_session_trigger_binding,
)

#: When the session graph template last changed. It is the ``generated_at`` of
#: every document that ``session_flow_definition_document`` returns.
#:
#: The Dex Server replaces a stored definition only when the uploaded one has a
#: later ``generated_at``. So this time changes, to a later one, in the same
#: change as the template, and stays the same otherwise. ``generate.py`` of the
#: conformance suite refuses a template change that keeps it. The TypeScript
#: core uses the same time.
SESSION_FLOW_GRAPH_GENERATED_AT = datetime(2026, 11, 14, 12, 0, 0, tzinfo=UTC)

_ACTION_PERMISSION_PATTERN = re.compile(r"[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*")


def render_session_flow_graph(
    flow_type: str,
    action_permission: str,
    *,
    connector_trigger_bindings: Sequence[Mapping[str, Any]] = (),
) -> str:
    """Render the Flow Definition Graph 2.0 file of one agent's session Flow.

    ``dexcli visualize`` cannot read the session Flow: the Flow lives in this
    library, and the application chooses its Flow type. This function fills
    both choices into the graph that ships with the package. Write the text to
    a file in the directory that Dex Web reads with ``--flow-rendering-dir``.
    Dex Web then draws the session Steps and offers the ``resolve_interrupt``,
    ``answer_question``, ``steer_user_message``, and ``cancel_turn`` Actions.
    Those Actions have no condition, because each RPC checks current state
    itself. With server-zd 0.1.26 or later, let the Worker upload the graph
    instead: pass ``session_flow_definition_document`` to its options.

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

    .. versionchanged:: 0.6.12
        ``connector_trigger_bindings`` adds Connector Trigger bindings to the
        graph.

    Args:
        flow_type: The Flow type of the agent, as passed to ``AgentSessionFlow``.
        action_permission: The permission every session Action requires, such
            as ``"support-agent.operate"``.
        connector_trigger_bindings: The Connector Trigger bindings that deliver
            into the session Flow, from ``resolve_session_trigger_binding``.
            The graph lists them in ``v2.connectorTriggerBindings``, in this
            order, so a hosting platform can configure them. When empty, the
            graph has no such field and its text is the same as without this
            argument.

    Returns:
        The graph as JSON text that ends with a newline.

    Raises:
        ValueError: If ``flow_type`` is blank, if ``action_permission`` is not
            lowercase letters, digits, dots, and hyphens that start with a
            letter, if a binding is invalid, or if two bindings share a
            Connector ID, connection name, Trigger name, and binding name.
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
    bindings = [
        canonical_session_trigger_binding(binding) for binding in connector_trigger_bindings
    ]
    if len(bindings) > 256:
        raise ValueError("a session graph declares at most 256 Connector Trigger bindings")
    binding_keys: set[tuple[str, str, str, str]] = set()
    for binding in bindings:
        key = (
            binding["connectorId"],
            binding["connectionName"],
            binding["triggerName"],
            binding["bindingName"],
        )
        if key in binding_keys:
            raise ValueError(
                f"Trigger binding {binding['bindingName']} of {binding['connectorId']} "
                f"{binding['triggerName']} on connection {binding['connectionName']} "
                "is declared twice"
            )
        binding_keys.add(key)
    if bindings:
        graph["v2"]["connectorTriggerBindings"] = bindings
    return json.dumps(graph, indent=2, ensure_ascii=False) + "\n"


def session_flow_definition_document(
    flow_type: str,
    action_permission: str,
    *,
    connector_trigger_bindings: Sequence[Mapping[str, Any]] = (),
) -> FlowDefinitionDocument:
    """Return the session graph of one agent as a document for the Worker to upload.

    Pass it in ``WorkerOptions(flow_definitions=...)``. The Worker uploads it to
    the Dex Server when it starts, and Dex Web draws the agent's sessions and
    offers their Actions without ``--flow-rendering-dir``. The upload needs
    server-zd 0.1.26 or later. With an older server the Worker logs a warning
    and starts anyway; render the file with ``render_session_flow_graph`` for
    such a server instead.

    The content is ``render_session_flow_graph(flow_type, action_permission,
    connector_trigger_bindings=connector_trigger_bindings)``.
    Its ``generated_at`` is ``SESSION_FLOW_GRAPH_GENERATED_AT`` of the
    ``dex_agents.session_graph`` module: the time the graph template last
    changed, not the current time. Restarting a Worker
    therefore writes nothing, and upgrading this package replaces the graph of
    an older release. Workers of either core upload the same document. The
    server keeps a stored graph whose ``generated_at`` is the same or later, so
    a new ``action_permission`` or new ``connector_trigger_bindings`` alone do
    not replace it. To change only those, upload the document once with
    ``Client.upload_flow_definitions(..., force=True)``.

    Example:
        >>> from dex import AsyncWorker, Registry, WorkerOptions
        >>> from dex_agents.builtin_loop import builtin_agent_session_flow
        >>> flow = builtin_agent_session_flow(definition, flow_type="SupportAgent")
        >>> worker = AsyncWorker(
        ...     Registry([flow], allow_async_handlers=True),
        ...     blob_cache,
        ...     WorkerOptions(
        ...         flow_definitions=[
        ...             session_flow_definition_document(
        ...                 "SupportAgent", "support-agent.operate"
        ...             )
        ...         ]
        ...     ),
        ... )
        >>> await worker.start()

    .. versionadded:: 0.5.0

    .. versionchanged:: 0.6.12
        Added ``connector_trigger_bindings``.

    Args:
        flow_type: The Flow type of the agent, as passed to ``AgentSessionFlow``.
            The Worker uploads the document only when it registers this Flow type.
        action_permission: The permission every session Action requires, such
            as ``"support-agent.operate"``.
        connector_trigger_bindings: The Connector Trigger bindings, as for
            ``render_session_flow_graph``.

    Returns:
        A ``FlowDefinitionDocument`` with the Flow type, the rendered graph, and
        ``SESSION_FLOW_GRAPH_GENERATED_AT``.

    Raises:
        ValueError: If ``flow_type`` is blank, if ``action_permission`` is not
            lowercase letters, digits, dots, and hyphens that start with a
            letter, or if a binding is invalid.
    """
    return FlowDefinitionDocument(
        flow_type=flow_type,
        content=render_session_flow_graph(
            flow_type, action_permission, connector_trigger_bindings=connector_trigger_bindings
        ),
        generated_at=SESSION_FLOW_GRAPH_GENERATED_AT,
    )


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
    parser.add_argument(
        "--trigger-binding",
        action="append",
        default=[],
        metavar="CONNECTION=PACKAGE@VERSION:TRIGGER:BINDING",
        help="a Connector Trigger binding that delivers into the session Flow; repeatable",
    )
    parser.add_argument(
        "--trigger-bindings",
        type=Path,
        metavar="FILE",
        help="a JSON array of Connector Trigger bindings",
    )
    options = parser.parse_args(arguments)
    try:
        specs = [parse_session_trigger_binding_flag(flag) for flag in options.trigger_binding]
        if options.trigger_bindings is not None:
            specs.extend(read_session_trigger_bindings_file(options.trigger_bindings))
        graph = render_session_flow_graph(
            options.flow_type,
            options.permission,
            connector_trigger_bindings=[resolve_session_trigger_binding(spec) for spec in specs],
        )
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
