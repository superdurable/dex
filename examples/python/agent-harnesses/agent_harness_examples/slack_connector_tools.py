# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Slack Connector operations offered to an agent as tools.

The Slack manifest exposes one operation to agents: postThreadReply. Each
call runs in its own tool Step with the connector's Call ID, and the agent
asks for approval before it posts. The connection comes from the project
configuration, so the Worker holds no Slack token and none enters Flow state.
"""

from __future__ import annotations

import logging
import os
from collections.abc import Mapping
from dataclasses import dataclass, field

from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.builtin_loop import ToolSource
from dex_connectors import slack
from dex_connectors.sdk.projectconfig import (
    LOCAL_CONNECTIONS_FILE_ENVIRONMENT,
    LoadedProject,
    load_from_environment,
)

# The static connection name that the project configuration must hold.
SLACK_CONNECTION_NAME = "support-slack"
HOSTED_PROJECT_ENVIRONMENT_PREFIX = "DEX_PROJECT_"

_logger = logging.getLogger(__name__)


def build_slack_tool_sources(connection: slack.Connection | None) -> list[ToolSource]:
    """Returns the Slack tool source, or no source when Slack is not configured."""
    if connection is None:
        return []
    return [
        ConnectorToolSource([
            # The manifest does not ask for approval; this agent asks before every post.
            connector_tool(
                connection.post_thread_reply(), connection.reference, requires_approval=True
            ),
        ]),
    ]


@dataclass
class SlackProjectConnection:
    """The loaded project configuration and its Slack connection; close both on shutdown."""

    project: LoadedProject = field(repr=False)
    connection: slack.Connection

    async def aclose(self) -> None:
        try:
            await self.connection.aclose()
        finally:
            await self.project.close()


async def open_slack_project_connection(
    environment: Mapping[str, str] = os.environ,
) -> SlackProjectConnection | None:
    """Opens the Slack connection from the project configuration, or returns None without one.

    Raises:
        ValueError: If a requested project configuration is incomplete or invalid.
    """
    is_configuration_requested = bool(environment.get(LOCAL_CONNECTIONS_FILE_ENVIRONMENT)) or any(
        name.startswith(HOSTED_PROJECT_ENVIRONMENT_PREFIX) and value
        for name, value in environment.items()
    )
    if not is_configuration_requested:
        _log_slack_not_set_up(f"{LOCAL_CONNECTIONS_FILE_ENVIRONMENT} is not set")
        return None
    # An incomplete or invalid configuration raises here, so the Worker fails at startup.
    try:
        project = await load_from_environment(environment)
    except FileNotFoundError:
        _log_slack_not_set_up("the connector configuration file does not exist")
        return None
    has_connection = any(
        entry.connector_id == "slack" and entry.connection_name == SLACK_CONNECTION_NAME
        for entry in project.configuration.connections
    )
    if not has_connection:
        await project.close()
        _log_slack_not_set_up(f"the project has no Slack connection named {SLACK_CONNECTION_NAME}")
        return None
    try:
        connection = slack.new_project_connection(project, SLACK_CONNECTION_NAME)
    except BaseException:
        await project.close()
        raise
    return SlackProjectConnection(project, connection)


def _log_slack_not_set_up(reason: str) -> None:
    _logger.warning("Slack connector tools are off: %s", reason)
