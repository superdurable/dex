# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fork a session from a CLI command: the new session ID, its task queue, and the lines.

The values match the TypeScript CLI core; see ``agents/conformance/session_fork.json``.
"""

from __future__ import annotations

import secrets
from collections.abc import Mapping
from datetime import UTC, datetime

from dex import AsyncClient, CloneForkFlowUnsupportedError, FlowNotFoundError

from dex_agents import AgentSessionFork, AgentSessionForkError, fork_agent_session
from dex_agents.session_flow import AgentSessionFlow
from dex_agents.session_fork import AgentSessionForkingFlow
from dex_agents_cli_core.session_task_queues import session_task_queue

#: The chat command that forks the open session: ``/fork [message-number]``.
FORK_COMMAND = "/fork"
#: The line ``/fork`` writes for an argument that is not a message number.
CHAT_FORK_USAGE_LINE = "[not forked] usage: /fork [message-number]"


class SessionForkFailure(Exception):
    """A fork the command could not make; the message is one line for the user."""


def new_cli_session_id(harness_name: str, now: datetime | None = None) -> str:
    """Return ``<harness>-<YYYYMMDD>-<HHMMSS>-<hex6>``, the format of every CLI session ID.

    Args:
        harness_name: The harness name.
        now: The time to use; ``None`` reads the clock.
    """
    moment = datetime.now(UTC) if now is None else now
    return f"{harness_name}-{moment:%Y%m%d-%H%M%S}-{secrets.token_hex(3)}"


def fork_task_queue(source_flow_type: str, harness_flow_type: str, session_id: str) -> str:
    """Return the task queue of a fork, or ``""`` to keep the source's.

    A session of the harness's Flow type opens only on its own task queue, so
    its fork always gets the fork's own one, also when the source ran on a
    shared queue. An environment-hashed session from before 0.6.8 keeps its
    task queue, the default one that the command's other Worker pulls.

    Args:
        source_flow_type: The source session's Flow type.
        harness_flow_type: The harness's Flow type.
        session_id: The fork's session ID.
    """
    if source_flow_type == harness_flow_type:
        return session_task_queue(session_id)
    return ""


async def fork_cli_session(
    client: AsyncClient,
    flows: Mapping[str, AgentSessionFlow | AgentSessionForkingFlow],
    harness_flow_type: str,
    source_session_id: str,
    session_id: str,
    *,
    after_message: int | None,
    command_name: str,
) -> AgentSessionFork:
    """Fork a session of one of ``flows``' Flow types into ``session_id``.

    Args:
        client: A Client whose Registry has every Flow of ``flows``.
        flows: The session Flows this command runs, by Flow type.
        harness_flow_type: The harness's Flow type; see ``fork_task_queue``.
        source_session_id: The session to fork.
        session_id: The new session's ID.
        after_message: The last message the fork keeps, or ``None`` for the current state.
        command_name: ``dex-agents-<harness>``, for the lines.

    Raises:
        SessionForkFailure: If the session cannot be forked; its message says why.
    """
    try:
        info = await client.describe_flow(source_session_id)
    except FlowNotFoundError as error:
        raise SessionForkFailure(missing_session_error(source_session_id, command_name)) from error
    flow = flows.get(info.flow_type)
    if flow is None:
        raise SessionForkFailure(
            other_harness_fork_error(source_session_id, info.flow_type, command_name)
        )
    try:
        return await fork_agent_session(
            client,
            flow,
            source_session_id,
            session_id,
            after_message=after_message,
            task_queue=fork_task_queue(info.flow_type, harness_flow_type, session_id),
        )
    except AgentSessionForkError as error:
        raise SessionForkFailure(str(error)) from error
    except CloneForkFlowUnsupportedError as error:
        raise SessionForkFailure(unsupported_server_error(command_name)) from error


def build_open_failure_error(session_id: str, error: str) -> str:
    """Return the error when a fork exists but could not be opened."""
    return f"session {session_id} was forked, but it could not be opened: {error}"


def build_forked_line(source_session_id: str, after_message: int, session_id: str) -> str:
    """Return the line written once a fork exists."""
    return (
        f"Forked session {source_session_id} after message {after_message} "
        f"into session {session_id}."
    )


def build_open_prompt(session_id: str) -> str:
    """Return the question whether to open the fork now; Enter means yes."""
    return f"Open session {session_id} now? [Y/n] "


def build_resume_hint(command_name: str, session_id: str) -> str:
    """Return the line that says how to open a session later."""
    return f"Resume it with: {command_name} --resume {session_id}"


def build_fork_usage_error(command_name: str) -> str:
    """Return the error of ``fork`` without a session ID."""
    return f"fork needs a session ID: {command_name} fork <session-id> [--at <message-number>]"


def build_chat_fork_failure_line(error: str) -> str:
    """Return the line ``/fork`` writes when the fork failed."""
    return f"[not forked] {error}"


def unsupported_server_error(command_name: str) -> str:
    """Return the error for a Dex server without clone fork."""
    return (
        f"{command_name} needs a Dex server with clone fork to fork sessions, such as the one "
        "dexcli 1.47.8 or later starts; this server is older. Upgrade it, or stop the dexcli "
        "dev this command reuses"
    )


def other_harness_fork_error(session_id: str, flow_type: str, command_name: str) -> str:
    """Return the error for a fork of a session that another harness's command runs."""
    return (
        f"session {session_id} is a {flow_type} session, which {command_name} does not run; "
        "fork it with the command that started it"
    )


def missing_session_error(session_id: str, command_name: str) -> str:
    """Return the error for a fork of a session that does not exist."""
    return f"session {session_id} does not exist; list sessions with {command_name} sessions"


def is_yes_answer(answer: str) -> bool:
    """Return whether an answer to the open prompt means yes: empty, ``y``, or ``yes``."""
    return answer.strip().lower() in ("", "y", "yes")
