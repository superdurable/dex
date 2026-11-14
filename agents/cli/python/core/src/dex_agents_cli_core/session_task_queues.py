# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Session task queues and the lines about where a session runs.

The values match the TypeScript CLI core; see
``agents/conformance/cli_session_task_queues.json``.
"""

from __future__ import annotations

import hashlib
import re
from collections.abc import Mapping

_SESSION_TASK_QUEUE_PREFIX = "s-"
_SESSION_TASK_QUEUE_HASH_LENGTH = 20
_LEGACY_SESSION_FLOW_TYPE_SUFFIX = re.compile(r"-[0-9a-z]{6}")
# The keys of a session environment that decide whether a takeover moved the session.
_ENVIRONMENT_KEYS = ("host_name", "user_id", "config_identity", "workspace")
_MAX_APP_WORKER_ID_LENGTH = 63
_NAMED_TASK_QUEUE_PROTOCOL_ERROR = re.compile(
    r"task queue .* requires (Dex )?Server protocol version"
)

#: The line printed when a takeover cancelled the turn that was in progress in the other place.
CANCELLED_TURN_LINE = (
    "Cancelled the turn that was in progress there; none of its model or tool calls run here."
)
#: The line printed when a takeover could not confirm that the turn in progress there ended.
TURN_NOT_CANCELLED_LINE = (
    "Could not confirm that the turn in progress there was cancelled; this command still "
    "refuses its model and tool calls."
)


def session_task_queue(session_id: str) -> str:
    """Return the task queue of one session.

    Every session that the CLI core starts runs on its own task queue, so only
    the command that has the session open runs its Steps and RPCs. The name is
    ``s-`` and the first 20 lowercase hexadecimal characters of the SHA-256 of
    the session ID in UTF-8. It always meets the task queue rules: 22
    characters of ``[a-z0-9-]``.

    .. versionadded:: 0.6.7

    Example:
        >>> session_task_queue("claude-sdk-20261113-203123-3efeb4")  # doctest: +SKIP
        's-...'

    Args:
        session_id: The session's Flow ID.

    Returns:
        The session's task queue.
    """
    digest = hashlib.sha256(session_id.encode("utf-8")).hexdigest()
    return f"{_SESSION_TASK_QUEUE_PREFIX}{digest[:_SESSION_TASK_QUEUE_HASH_LENGTH]}"


def is_legacy_session_flow_type(harness_flow_type: str, flow_type: str) -> bool:
    """Return whether a Flow type is a harness's environment-hashed one from before 0.6.7.

    Args:
        harness_flow_type: The harness's Flow type.
        flow_type: A session's Flow type.

    Returns:
        ``True`` for the harness Flow type, ``-``, and 6 lowercase base36 characters.
    """
    return flow_type.startswith(harness_flow_type) and (
        _LEGACY_SESSION_FLOW_TYPE_SUFFIX.fullmatch(flow_type[len(harness_flow_type) :]) is not None
    )


def describe_session_environment(environment: Mapping[str, str] | None) -> str:
    """Return ``<host name>:<workspace>`` of a session environment, or ``—`` when it names neither.

    Args:
        environment: A session's ``start_environment`` or ``SessionLastTerminalEnvironment``.
    """
    host_name = (environment or {}).get("host_name", "")
    workspace = (environment or {}).get("workspace", "")
    return "—" if not host_name and not workspace else f"{host_name}:{workspace}"


def describe_session_environment_identity(environment: Mapping[str, str]) -> str:
    """Return ``<user>@<host name>:<workspace> (<config identity>)`` of a session environment.

    The user is ``user_name``, or ``user_id`` without it. A takeover line names
    both environments this way.

    Args:
        environment: A session's ``start_environment`` or ``SessionLastTerminalEnvironment``.
    """
    user = environment.get("user_name") or environment.get("user_id", "")
    return (
        f"{user}@{environment.get('host_name', '')}:{environment.get('workspace', '')} "
        f"({environment.get('config_identity', '')})"
    )


def is_other_session_environment(previous: Mapping[str, str], current: Mapping[str, str]) -> bool:
    """Return whether two session environments differ in host, user, configuration, or workspace.

    A missing key counts as empty.

    Args:
        previous: Where the session was last open.
        current: This command's environment.
    """
    return any(previous.get(key, "") != current.get(key, "") for key in _ENVIRONMENT_KEYS)


def build_takeover_line(session_id: str, previous_environment: str, environment: str) -> str:
    """Return the line printed once a command took a session over from another environment."""
    return (
        f"Session {session_id} was last open on {previous_environment}; its model and tool "
        f"calls now run here, on {environment}."
    )


def taken_over_turn_reason(session_id: str) -> str:
    """Return why this command refuses a call of the turn in progress when it took the session."""
    return f"its turn started in another place before this command took session {session_id} over"


def refused_tool_call_error(tool_name: str, reason: str) -> str:
    """Return the error of a refused tool call."""
    return f"tool {tool_name} did not run: {reason}"


def refused_model_call_error(reason: str) -> str:
    """Return the error of a refused model call."""
    return f"the model call did not run: {reason}"


def other_environment_legacy_session_error(session_id: str, flow_type: str) -> str:
    """Return the error for ``--resume`` of another execution environment's hashed session."""
    return (
        f"session {session_id} is a {flow_type} session of another execution environment, "
        "from a release before sessions could move; resume it as the user, with the "
        "configuration file and workspace, that started it"
    )


def other_harness_session_error(session_id: str, flow_type: str, command_name: str) -> str:
    """Return the error for ``--resume`` of a session that another harness's command runs."""
    return (
        f"session {session_id} is a {flow_type} session, which {command_name} does not run; "
        "resume it with the command that started it"
    )


def foreign_task_queue_session_error(session_id: str, command_name: str) -> str:
    """Return the error for a session of the harness's Flow type on another task queue."""
    return (
        f"session {session_id} was not started by {command_name}, so it runs on another "
        "task queue and cannot be opened here"
    )


def named_task_queue_server_error(command_name: str) -> str:
    """Return the error for a Dex server without named task queues."""
    return (
        f"{command_name} needs a Dex server with named task queues, such as the one dexcli "
        "1.47.5 or later starts; this server is older. Upgrade it, or stop the dexcli dev "
        "this command reuses"
    )


def is_named_task_queue_protocol_error(error: BaseException) -> bool:
    """Return whether an SDK error says the server's protocol has no named task queues."""
    return _NAMED_TASK_QUEUE_PROTOCOL_ERROR.search(str(error)) is not None


def cli_app_worker_id(
    harness_name: str, host_name: str, process_id: int, suffix: str | None = None
) -> str:
    """Return ``dex-agents-<harness>-<host>-<pid>``, plus ``-<suffix>`` when given.

    The host name is cut so the ID has at most 63 characters, the Dex server's limit.

    Args:
        harness_name: The harness name.
        host_name: This host's name.
        process_id: This process's ID.
        suffix: Tells apart the Workers of one process.
    """
    head = f"dex-agents-{harness_name}-"
    tail = f"-{process_id}" if suffix is None else f"-{process_id}-{suffix}"
    room = max(0, _MAX_APP_WORKER_ID_LENGTH - len(head) - len(tail))
    return f"{head}{host_name[:room]}{tail}"
